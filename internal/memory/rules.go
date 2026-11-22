package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// 확정 규칙(A2, 설계 · orbit-code 자율 완수와 학습 「확정 규칙」, 계획 · AU-6). 규칙은 이 저장 계층에 있다:
// 회고든 다른 자동화든 이 API를 거치면 같은 규칙을 지킨다.

// Who writes: the person (CLI), the AI in a host conversation,
// or an automation such as the daily tidy. A tool argument never sets it; the
// server option or the process environment does.
const (
	ActorUser       = "user"       // 사람: CLI
	ActorAI         = "ai"         // 호스트 대화 중의 AI
	ActorAutomation = "automation" // 하루 정리 같은 자동화(옛 회고도)
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

// ErrForgetNeedsWords means a writer other than the person tried to forget memories without the user's
// words that ask for it. Forget returns it so that it is ErrProtected as well (명세 · memory_forget 5.1).
var ErrForgetNeedsWords = i18n.Err("memory.err.forget_words")

// ErrForgetAutomation means an automation tried to forget memories: it never does, words or not.
var ErrForgetAutomation = i18n.Err("memory.err.forget_automation")

// ErrForgotten means a memory or a save request points at a memory that was forgotten (deleted for good).
var ErrForgotten = i18n.Err("memory.err.forgotten")

// ErrLedgerForgotten means a ledger change cannot be reverted: a memory it changed was forgotten.
var ErrLedgerForgotten = i18n.Err("memory.err.ledger_forgotten")

// ErrNoEvidence means an automatic confirmation has no evidence.
var ErrNoEvidence = i18n.Err("memory.err.no_evidence")

// ErrAutoSkip means an automatic change has nothing left to do (already done another way).
var ErrAutoSkip = i18n.Err("memory.err.auto_skip")

// Evidence codes of an automatic confirmation.
const (
	EvidenceSeenDays         = "seen_days"         // ① 서로 다른 세션·다른 날 다시 나옴(E-1). 코드는 원장에 남아 그대로 둔다
	EvidenceFollowedVerified = "followed_verified" // ② 회상돼 따랐고 검증 통과·교정 없음
	EvidenceVerifiedFact     = "verified_fact"     // ③ 서버가 근거를 다시 확인한 검증 출처의 프로젝트 사실(E-4)
)

// Policy holds the counts of the confirmation rules. FollowedVerified and
// CorrectionSessions are E2: measured on a copy of this PC's memory database
// (2026-11-01) and waiting for the user's decision; the defaults are the
// recommendation (계획 · AU-6 「E2」).
type Policy struct {
	SeenDays           int `json:"confirmSeenDays"`          // ① 세션도 날도 서로 다른 다시 나옴 수(지금 규칙 2, seenApartTx)
	FollowedVerified   int `json:"confirmFollowedVerified"`  // ② 따르고 검증 통과·교정 없는 세션 수
	CorrectionSessions int `json:"correctionRepeatSessions"` // 반복 교정 표시: 같은 대상 교정이 나온 세션 수
	// AutoMode is the setting autoMode (C-9, off by default): on, a verified fact whose ground the server could
	// not read again (a command's output) still replaces the user's words on its own, kept in the ledger to be
	// undone (replace.go). Off, it stays a conflict. Rules and decisions are never replaced either way.
	AutoMode bool `json:"autoMode"`
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

// blockKeptOld is why confirmBlocked stops a candidate that is the newer side of a same-place pair whose old
// side the user kept with their own words (resolutionKeepOld).
const blockKeptOld = "kept_old"

// confirmBlocked is why memory id may not be confirmed automatically: an open
// conflict mark, the person put it back to candidate (hold), the person
// undid an automatic confirmation of it before, or the person undid its
// automatic replacement of another memory (blockRevertedReplace, C-3), or the user kept the old value of its
// same-place pair with their own words (blockKeptOld).
func (s *Store) confirmBlocked(id string) (string, error) {
	// 출처가 믿을 수 없는 것뿐이면 증거가 쌓여도 스스로 확정하지 않는다(D7, 확정 길 ①).
	if t, err := memoryTrustTx(s.db, id); err != nil {
		return "", err
	} else if Untrusted(t) {
		return "untrusted", nil
	}
	// 사람이 그 자동 대체를 되돌렸으면 새 기억은 어떤 증거로도(검증 사실 즉시 확정 포함) 스스로 확정하지 않는다.
	// 옛 기억을 지워도 쌍 줄은 남아 막는다(forget.go keepTables).
	var reverted int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM supersede_reverts WHERE new_id=?`, id).Scan(&reverted); err != nil {
		return "", err
	} else if reverted > 0 {
		return blockRevertedReplace, nil
	}
	var kind string
	err := s.db.QueryRow(`SELECT kind FROM audit_flags WHERE memory_id=? AND resolved_at=0 LIMIT 1`, id).Scan(&kind)
	if err == nil {
		return kind, nil // 어긋남(conflict), 사람이 내린 보류(hold)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// 사용자가 같은 자리의 옛 값을 말로 유지한 쌍의 새 쪽이다: 옛 값이 아직 살아 있는 동안 스스로 확정해 그것을 가리지 않는다.
	// 새 쪽에 사용자 말이 붙으면(마음을 바꿈) settle이 대체하는 길이라 여기서 막지 않는다.
	var kept int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_flags f JOIN memories o ON o.id=f.other_id
		WHERE f.kind=? AND f.memory_id=? AND f.resolved_at>0 AND f.resolution=? AND o.state IN ('candidate','confirmed')`,
		FlagConflict, id, resolutionKeepOld).Scan(&kept); err != nil {
		return "", err
	} else if kept > 0 {
		return blockKeptOld, nil
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
	seen, err := seenApartTx(s.db, id)
	if err != nil {
		return "", err
	}
	if seen >= pol.SeenDays {
		// 증거 ①에만 건다: 다시 나온 사이에 사용자가 반대로 교정했으면 그 날들은 근거가 아니다(E-2). ②③은 그대로 본다.
		// ②는 지금 새로 쌓이는 경로가 없고(사용 판정을 남기던 세션 끝 회고를 걷어냈다), ③은 검증된 사실의 즉시 확정이라 막지 않는다.
		corrected, err := s.correctedSince(m)
		if err != nil {
			return "", err
		}
		if !corrected {
			return EvidenceSeenDays, nil
		}
	}
	if m.Kind == KindFact && m.Scope != ScopeGlobal { // 저장소·제품·제품 미정 사실(user 사실은 아님)
		var n int
		// 검증 출처는 무엇을 돌렸는지(ref)와 그 결과(excerpt)가 있고, 서버가 그 근거를 다시 확인했어야(rechecked) 증거다(E-4).
		// AI가 적은 근거만으로는 즉시 확정하지 않는다: 그 글만으로 확정되면 출처 등급이 무너진다(C-1과 같은 기준).
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sources WHERE memory_id=? AND kind=? AND ref<>'' AND excerpt<>'' AND rechecked=1`, id, SourceVerification).Scan(&n); err != nil {
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

// seenApartTx is how many sightings of memory id can be picked so that no two share a session or a day: the
// count evidence ① compares with Policy.SeenDays (E-1). Two days of one session, or two sessions of one day,
// give 1; the sightings of before schema 6 are all one session (legacySession). It is a maximum matching of
// sessions to days with the sightings as edges, so a session seen on many days is not counted twice.
func seenApartTx(q querier, id string) (int, error) {
	rows, err := q.Query(`SELECT session, day FROM sighting_sessions WHERE memory_id=? ORDER BY session, day`, id)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	daysOf := map[string][]string{}
	var sessions []string
	for rows.Next() {
		var session, day string
		if err := rows.Scan(&session, &day); err != nil {
			return 0, err
		}
		if _, ok := daysOf[session]; !ok {
			sessions = append(sessions, session)
		}
		daysOf[session] = append(daysOf[session], day)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	taken := map[string]string{} // 날 → 그 날을 쓴 세션
	var pick func(session string, tried map[string]bool) bool
	pick = func(session string, tried map[string]bool) bool {
		for _, day := range daysOf[session] {
			if tried[day] {
				continue
			}
			tried[day] = true
			if other, ok := taken[day]; !ok || pick(other, tried) {
				taken[day] = session
				return true
			}
		}
		return false
	}
	n := 0
	for _, session := range sessions {
		if pick(session, map[string]bool{}) {
			n++
		}
	}
	return n, nil
}

// correctedSince says whether the user went against candidate m after it was made (E-2), so its sightings are
// no evidence: an open conflict pair on m or on a memory of m's scope and topic, or a memory of that scope and
// topic rejected (hidden) at or after m's creation. A rejection before m was made does not count. Without a
// topic only the pairs on m count: a rejected memory of no topic says nothing about m.
func (s *Store) correctedSince(m Memory) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_flags f WHERE f.kind=? AND f.resolved_at=0 AND (f.memory_id=? OR f.other_id=?
		OR (?<>'' AND EXISTS (SELECT 1 FROM memories x WHERE x.id IN (f.memory_id, f.other_id) AND x.scope=? AND x.topic=?)))`,
		FlagConflict, m.ID, m.ID, m.Topic, m.Scope, m.Topic).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 || m.Topic == "" {
		return n > 0, nil
	}
	// 거절된 때는 지금의 숨김이 시작된 버전의 시각이다(그 전에 숨김이 아니던 마지막 버전 뒤의 첫 숨김 버전).
	// 고친 시각(updated_at)으로 보지 않는 까닭: 숨긴 기억도 같은 제목이 다시 오면 고친 시각이 바뀐다.
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memories h WHERE h.state=? AND h.scope=? AND h.topic=? AND h.id<>?
		AND (SELECT MIN(v.at) FROM memory_versions v WHERE v.memory_id=h.id AND json_extract(v.snapshot,'$.state')=?
			AND v.version > COALESCE((SELECT MAX(p.version) FROM memory_versions p WHERE p.memory_id=h.id AND json_extract(p.snapshot,'$.state')<>?), 0)) >= ?`,
		StateHidden, m.Scope, m.Topic, m.ID, StateHidden, StateHidden, m.CreatedAt).Scan(&n)
	return n > 0, err
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

// ConfirmCandidates confirms every candidate that has evidence and returns how many. A candidate
// holding the user's words is left for the user, not counted as an error.
func (s *Store) ConfirmCandidates() (int, error) {
	cands, err := s.list([]string{StateCandidate})
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, m := range cands {
		_, ok, err := s.ConfirmByEvidence(m.ID)
		if errors.Is(err, ErrProtected) { // 사용자 말을 품은 후보는 사람을 기다린다: 실패가 아니다
			continue
		}
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
		return "증거 ①: 서로 다른 세션·다른 날 다시 나옴"
	case EvidenceFollowedVerified:
		return "증거 ②: 회상돼 따랐고 그 일이 검증을 통과했으며 교정이 없었던 세션이 기준 횟수 이상"
	case EvidenceVerifiedFact:
		return "증거 ③: 서버가 근거 파일을 다시 읽어 확인한 검증 출처의 프로젝트 사실"
	}
	return code
}
