package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// 확정 규칙(A2, 설계 · OpenOrbit 자율 완수와 학습 「확정 규칙」, 계획 · AU-6). 규칙은 이 저장 계층에 있다:
// 회고든 다른 자동화든 이 API를 거치면 같은 규칙을 지킨다.

// Who writes: the person (CLI, Orbit Studio), the AI in a host conversation,
// or an automation such as a retrospective. A tool argument never sets it; the
// server option or the process environment does.
const (
	ActorUser       = "user"       // 사람: CLI, Orbit Studio 화면
	ActorAI         = "ai"         // 호스트 대화 중의 AI
	ActorAutomation = "automation" // 회고·정리 같은 자동화
)

// Actors are the writers the store knows.
var Actors = []string{ActorUser, ActorAI, ActorAutomation}

// Who is the writer of a change and, when it changes what the user said, the
// user's own words that ask for it.
type Who struct {
	Actor     string // 비우면 사람(user)
	UserQuote string // 사용자 말 그대로. 사용자 인용 기억을 바꿀 때 필요하고 새 출처로 남는다
	Ref       string // 그 말의 위치(호스트:세션 id 같은 것)
}

func (w Who) actor() string {
	if w.Actor == "" {
		return ActorUser
	}
	return w.Actor
}

func (w Who) human() bool { return w.actor() == ActorUser }

func (w Who) quote() string { return strings.TrimSpace(w.UserQuote) }

// ErrProtected means a writer other than the person tried to change what the
// user said (a memory with a quote source) without the user's words.
var ErrProtected = i18n.Err("memory.err.protected")

// ErrConfirmNeedsWords means a writer other than the person tried to confirm a
// candidate without the user's words (automatic confirmation needs evidence).
var ErrConfirmNeedsWords = i18n.Err("memory.err.confirm_words")

// ErrNoEvidence means an automatic confirmation has no evidence.
var ErrNoEvidence = i18n.Err("memory.err.no_evidence")

// ErrAutoSkip means an automatic change has nothing left to do (already done another way).
var ErrAutoSkip = i18n.Err("memory.err.auto_skip")

// Evidence codes of an automatic confirmation.
const (
	EvidenceSeenDays         = "seen_days"         // ① 서로 다른 날 다시 나옴
	EvidenceFollowedVerified = "followed_verified" // ② 회상돼 따랐고 검증 통과·교정 없음
	EvidenceVerifiedFact     = "verified_fact"     // ③ 작업 검증이 직접 확인한 프로젝트 사실
)

// Policy holds the counts of the confirmation rules. FollowedVerified and
// CorrectionSessions are E2: measured on a copy of this PC's memory database
// (2026-11-01) and waiting for the user's decision; the defaults are the
// recommendation (계획 · AU-6 「E2」).
type Policy struct {
	SeenDays           int `json:"confirmSeenDays"`          // ① 서로 다른 날 수(지금 규칙 2)
	FollowedVerified   int `json:"confirmFollowedVerified"`  // ② 따르고 검증 통과·교정 없는 세션 수
	CorrectionSessions int `json:"correctionRepeatSessions"` // 반복 교정 표시: 같은 대상 교정이 나온 세션 수
}

// DefaultPolicy is the recommendation until the user decides (E2).
func DefaultPolicy() Policy { return Policy{SeenDays: 2, FollowedVerified: 2, CorrectionSessions: 3} }

func (p Policy) orDefault() Policy {
	d := DefaultPolicy()
	if p.SeenDays < 1 {
		p.SeenDays = d.SeenDays
	}
	if p.FollowedVerified < 1 {
		p.FollowedVerified = d.FollowedVerified
	}
	if p.CorrectionSessions < 1 {
		p.CorrectionSessions = d.CorrectionSessions
	}
	return p
}

// protectedTx says whether memory id carries the user's own words.
func protectedTx(q querier, id string) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM sources WHERE memory_id=? AND kind=?`, id, SourceQuote).Scan(&n)
	return n > 0, err
}

// Protected says whether memory id is the user's own words: only the person,
// or a writer that brings the user's words, changes it.
func (s *Store) Protected(id string) (bool, error) { return protectedTx(s.db, id) }

// guardTx stops a writer other than the person from changing a protected
// memory without the user's words. With the words the change goes through and
// the words stay on the memory as a quote source.
func guardTx(tx *sql.Tx, w Who, now int64, ids ...string) error {
	if w.human() {
		return nil
	}
	for _, id := range uniq(ids) {
		p, err := protectedTx(tx, id)
		if err != nil {
			return err
		}
		if !p {
			continue
		}
		if w.quote() == "" {
			return fmt.Errorf("%w: %s", ErrProtected, id)
		}
	}
	return attachQuoteTx(tx, w, now, ids...)
}

// attachQuoteTx keeps the user's words that asked for a change on each memory.
func attachQuoteTx(tx *sql.Tx, w Who, now int64, ids ...string) error {
	if w.quote() == "" {
		return nil
	}
	for _, id := range uniq(ids) {
		if err := addSourceTx(tx, id, Source{Kind: SourceQuote, Ref: w.Ref, Excerpt: w.quote()}, now); err != nil {
			return err
		}
	}
	return nil
}

// --- 증거 확정 ---

// confirmBlocked is why memory id may not be confirmed automatically: an open
// conflict mark, the person put it back to candidate (hold), or the person
// undid an automatic confirmation of it before.
func (s *Store) confirmBlocked(id string) (string, error) {
	var kind string
	err := s.db.QueryRow(`SELECT kind FROM audit_flags WHERE memory_id=? AND resolved_at=0 LIMIT 1`, id).Scan(&kind)
	if err == nil {
		return kind, nil // 어긋남(conflict), 사람이 내린 보류(hold)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	rows, err := s.db.Query(`SELECT targets FROM ledger WHERE proposal_kind=? AND reverted_by<>''`, ProposalConfirm)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var targets string
		if rows.Scan(&targets) != nil {
			continue
		}
		var ids []string
		_ = json.Unmarshal([]byte(targets), &ids)
		for _, t := range ids {
			if t == id {
				return "reverted", nil
			}
		}
	}
	return "", rows.Err()
}

// EvidenceFor is the first evidence that confirms candidate id, or "" when there is none.
func (s *Store) EvidenceFor(id string) (string, error) {
	m, err := getTx(s.db, id)
	if err != nil {
		return "", err
	}
	if m.State != StateCandidate {
		return "", nil
	}
	pol := s.Policy.orDefault()
	var days int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sightings WHERE memory_id=?`, id).Scan(&days); err != nil {
		return "", err
	}
	if days >= pol.SeenDays {
		return EvidenceSeenDays, nil
	}
	if m.Kind == KindFact && m.Scope != ScopeGlobal { // 저장소·제품·제품 미정 사실(user 사실은 아님)
		var n int
		// 검증 출처는 무엇을 돌렸는지(ref)와 그 결과(excerpt)가 있어야 증거다.
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sources WHERE memory_id=? AND kind=? AND ref<>'' AND excerpt<>''`, id, SourceVerification).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			return EvidenceVerifiedFact, nil
		}
	}
	n, err := s.cleanVerifiedUses(id)
	if err != nil {
		return "", err
	}
	if n >= pol.FollowedVerified {
		return EvidenceFollowedVerified, nil
	}
	return "", nil
}

// cleanVerifiedUses counts the sessions where memory id was recalled and
// followed, the work passed its verification, and nothing in that session
// corrected the memory or found it violated.
func (s *Store) cleanVerifiedUses(id string) (int, error) {
	rows, err := s.db.Query(`SELECT id, session, kind, data FROM events WHERE kind IN (?,?) AND (data LIKE ? OR data LIKE ?)`,
		EventMemoryUsed, EventCorrection, `%"memoryId":"`+id+`"%`, `%"target":"`+id+`"%`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	good := map[string]bool{}
	bad := map[string]bool{}
	for rows.Next() {
		var eid int64
		var session, kind, data string
		if rows.Scan(&eid, &session, &kind, &data) != nil {
			continue
		}
		var d struct {
			MemoryID   string `json:"memoryId"`
			Target     string `json:"target"`
			TargetKind string `json:"targetKind"`
			Verdict    string `json:"verdict"`
			Verified   bool   `json:"verified"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		if d.MemoryID != id && !(d.Target == id && (d.TargetKind == "" || d.TargetKind == TargetKindMemory)) {
			continue
		}
		key := session
		if key == "" {
			key = fmt.Sprintf("event-%d", eid)
		}
		switch {
		case kind == EventCorrection, d.Verdict == UsageViolated:
			bad[key] = true
		case d.Verdict == UsageFollowed && d.Verified:
			good[key] = true
		}
	}
	n := 0
	for k := range good {
		if !bad[k] {
			n++
		}
	}
	return n, rows.Err()
}

// ConfirmByEvidence confirms candidate id when it has evidence and nothing
// blocks it, as an automatic change kept in the ledger. It reports whether it did.
func (s *Store) ConfirmByEvidence(id string) (DecideResult, bool, error) {
	ev, err := s.EvidenceFor(id)
	if err != nil || ev == "" {
		return DecideResult{}, false, err
	}
	if why, err := s.confirmBlocked(id); err != nil || why != "" {
		return DecideResult{}, false, err
	}
	m, err := getTx(s.db, id)
	if err != nil {
		return DecideResult{}, false, err
	}
	res, err := s.AutoApply(AutoInput{Kind: ProposalConfirm, Title: "자동 확정: " + m.Title, Body: evidenceText(ev),
		MemoryIDs: []string{id}, Evidence: ev, Who: Who{Actor: ActorAutomation}})
	if errors.Is(err, ErrAutoSkip) {
		return res, false, nil
	}
	return res, err == nil, err
}

// ConfirmCandidates confirms every candidate that has evidence and returns how many.
func (s *Store) ConfirmCandidates() (int, error) {
	cands, err := s.list([]string{StateCandidate})
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, m := range cands {
		_, ok, err := s.ConfirmByEvidence(m.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			n++
		}
	}
	return n, errors.Join(errs...)
}

func evidenceText(code string) string {
	switch code {
	case EvidenceSeenDays:
		return "증거 ①: 서로 다른 날 다시 나옴"
	case EvidenceFollowedVerified:
		return "증거 ②: 회상돼 따랐고 그 일이 검증을 통과했으며 교정이 없었던 세션이 기준 횟수 이상"
	case EvidenceVerifiedFact:
		return "증거 ③: 작업 검증이 직접 확인한 프로젝트 사실"
	}
	return code
}
