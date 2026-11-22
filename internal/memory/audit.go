package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// 감사 목록(A2 「기억 정리」 → 감사 목록): 최근 자동 확정·자동 정리, 어긋남 표시, 기준을 넘은 반복 교정과
// 하네스 개선 후보. 사람은 보고 싶을 때 보고 틀린 것만 되돌리거나 고친다.

const auditSchema = `
CREATE TABLE IF NOT EXISTS audit_flags (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL, memory_id TEXT NOT NULL, other_id TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '', actor TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL,
  resolved_at INTEGER NOT NULL DEFAULT 0, resolution TEXT NOT NULL DEFAULT '', resolved_by TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS audit_flags_memory ON audit_flags(memory_id) WHERE resolved_at = 0;
CREATE TABLE IF NOT EXISTS audit_acks (key TEXT PRIMARY KEY, at INTEGER NOT NULL, actor TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS events_kind ON events(kind);
PRAGMA user_version=4;
`

// Kinds of marks on a memory.
const (
	FlagConflict = "conflict" // 후보가 사용자 규칙과 어긋남(감사 목록에 보인다)
	FlagHold     = "hold"     // 사람이 확정을 후보로 내림: 자동화가 다시 확정하지 않는다
)

// holdConfirm keeps the person's choice to put a memory back to candidate.
func (s *Store) holdConfirm(memoryID string, w Who) {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_flags WHERE memory_id=? AND kind=? AND resolved_at=0`, memoryID, FlagHold).Scan(&n)
	if n > 0 {
		return
	}
	_, _ = s.db.Exec(`INSERT INTO audit_flags(id,kind,memory_id,note,actor,at) VALUES(?,?,?,?,?,?)`, NewID("f-"), FlagHold, memoryID, "사람이 후보로 내림", w.actor(), s.nowMs())
}

// Correction targets.
const (
	TargetKindMemory = "memory"
	TargetKindSkill  = "skill"
	TargetKindRole   = "role"
)

// TargetKindProcedure is a procedure (a lesson tagged TagProcedure) that came
// out of several works: a harness improvement candidate, not a correction target.
const TargetKindProcedure = "procedure"

// TagProcedure marks a lesson as a procedure (how a kind of work is done).
const TagProcedure = "procedure"

// TargetKinds are what a correction can point at.
var TargetKinds = []string{TargetKindMemory, TargetKindSkill, TargetKindRole}

// Flag is a mark on a memory for the person to look at.
type Flag struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	MemoryID   string `json:"memoryId"`
	OtherID    string `json:"otherId,omitempty"` // 어긋나는 사용자 규칙
	Note       string `json:"note,omitempty"`
	Actor      string `json:"actor,omitempty"`
	At         int64  `json:"at"`
	ResolvedAt int64  `json:"resolvedAt,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

const flagCols = `id,kind,memory_id,other_id,note,actor,at,resolved_at,resolution`

func scanFlag(row interface{ Scan(...any) error }) (Flag, error) {
	var f Flag
	err := row.Scan(&f.ID, &f.Kind, &f.MemoryID, &f.OtherID, &f.Note, &f.Actor, &f.At, &f.ResolvedAt, &f.Resolution)
	return f, err
}

// FlagConflict marks candidate memoryID as going against the user's rule
// ruleID. A marked candidate is not confirmed automatically; the audit list
// shows it. The same open mark is returned as is.
func (s *Store) FlagConflict(memoryID, ruleID, note string, w Who) (Flag, error) {
	m, err := getTx(s.db, memoryID)
	if err != nil {
		return Flag{}, err
	}
	if m.State != StateCandidate {
		return Flag{}, i18n.Errorf("memory.err.flag_candidate", ErrInvalid, memoryID)
	}
	if ruleID == memoryID {
		return Flag{}, ErrInvalid
	}
	if _, err := getTx(s.db, ruleID); err != nil {
		return Flag{}, err
	}
	f, err := scanFlag(s.db.QueryRow(`SELECT `+flagCols+` FROM audit_flags WHERE kind=? AND memory_id=? AND other_id=? AND resolved_at=0`, FlagConflict, memoryID, ruleID))
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Flag{}, err
	}
	f = Flag{ID: NewID("f-"), Kind: FlagConflict, MemoryID: memoryID, OtherID: ruleID, Note: ClipRunes(MaskSecrets(note), 300), Actor: w.actor(), At: s.nowMs()}
	_, err = s.db.Exec(`INSERT INTO audit_flags(id,kind,memory_id,other_id,note,actor,at) VALUES(?,?,?,?,?,?,?)`, f.ID, f.Kind, f.MemoryID, f.OtherID, f.Note, f.Actor, f.At)
	return f, err
}

// ResolveFlag closes a mark with how it was settled (dismissed, confirmed, hidden…).
func (s *Store) ResolveFlag(id, resolution string, w Who) error {
	res, err := s.db.Exec(`UPDATE audit_flags SET resolved_at=?, resolution=?, resolved_by=? WHERE id=? AND resolved_at=0`, s.nowMs(), resolution, w.actor(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return i18n.Errorf("memory.err.no_flag", ErrInvalid, id)
	}
	// 사람이(또는 사용자 말을 들고) 어긋남을 dismissed로 닫는 것은 옛 기억을 유지하기로 한 것이다: 어긋나는 새 것이 아닌 옛 기억
	// 쪽을 확인한 것으로 센다(L-1). revertedOldTx도 이 쌍의 옛 기억을 사람이 고른 값으로 본다.
	if resolution == "dismissed" && (w.Actor == ActorUser || w.quote() != "") {
		var oldID string
		if s.db.QueryRow(`SELECT other_id FROM audit_flags WHERE id=? AND kind=?`, id, FlagConflict).Scan(&oldID) == nil && oldID != "" {
			s.confirmKept(oldID, w)
		}
	}
	return nil
}

// confirmKept moves the review date of a live memory the user decided to keep (L-1). It is a best effort after
// the decision is written: a memory that is gone or not live is left, and a failure does not undo the decision.
func (s *Store) confirmKept(id string, w Who) {
	if m, err := getTx(s.db, id); err == nil && (m.State == StateCandidate || m.State == StateConfirmed) {
		_, _ = s.Reviewed(id, w)
	}
}

// OpenFlags lists the marks not settled yet, newest first.
func (s *Store) OpenFlags() ([]Flag, error) {
	rows, err := s.db.Query(`SELECT ` + flagCols + ` FROM audit_flags WHERE resolved_at=0 ORDER BY at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flag
	for rows.Next() {
		f, err := scanFlag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// closeFlagsOf settles the open marks of a memory that is no longer a candidate.
func (s *Store) closeFlagsOf(memoryID, resolution string) {
	_, _ = s.db.Exec(`UPDATE audit_flags SET resolved_at=?, resolution=? WHERE memory_id=? AND resolved_at=0`, s.nowMs(), resolution, memoryID)
}

// --- 사건: 교정과 사용 판정 ---

// CorrectionInput is one moment the user corrected the AI.
type CorrectionInput struct {
	Quote      string // 사용자 말 그대로
	Note       string
	Host       string
	Session    string
	Day        string // 비우면 오늘
	TargetKind string // memory·skill·role. 비우면 대상 없음
	Target     string // 기억 id, 스킬·역할 이름(orbit-code:tdd 같은)
	Injected   string // 기억 대상: 그 세션에 넣었나(yes·no·unknown)
	RetroID    string
	OccurredAt int64
}

// RecordCorrection keeps a correction as an event. Its target (a memory, a
// skill or a role) is what repeated corrections are counted by.
// No orbit-mem path writes it since the retrospectives were removed (2026-11-08, ORBIT-13);
// it keeps the event format the readers count, for old events and for tests.
func (s *Store) RecordCorrection(in CorrectionInput) error {
	q := strings.Join(strings.Fields(in.Quote), " ")
	if q == "" {
		return i18n.Errorf("memory.err.correction_quote", ErrInvalid)
	}
	if in.Target != "" && !slices.Contains(TargetKinds, in.TargetKind) {
		return i18n.Errorf("memory.err.correction_target", ErrInvalid, strings.Join(TargetKinds, "|"))
	}
	data := map[string]any{"quote": ClipRunes(MaskSecrets(q), 300), "note": ClipRunes(MaskSecrets(in.Note), 200),
		"injected": in.Injected, "retroId": in.RetroID}
	if in.OccurredAt > 0 {
		data["occurredAt"] = in.OccurredAt
	}
	memoryID := ""
	if in.Target != "" {
		data["targetKind"], data["target"] = in.TargetKind, ClipRunes(in.Target, 120)
		if in.TargetKind == TargetKindMemory {
			memoryID = in.Target
		}
	}
	data["memoryId"] = memoryID
	return s.RecordEvent(Event{Kind: EventCorrection, Day: in.Day, Host: in.Host, Session: in.Session}, data)
}

// UsageInput is the verdict on one memory recalled in a session or a work.
type UsageInput struct {
	MemoryID   string
	Verdict    string // followed·violated·irrelevant
	Verified   bool   // 그 일이 실행 검증(시험·빌드)을 통과했다
	Note       string
	Host       string
	Session    string
	Day        string
	RetroID    string
	OccurredAt int64
}

// RecordUsage keeps a usage verdict as an event and, when it is evidence ②,
// checks whether the candidate is now confirmed.
// No orbit-mem path writes it since the retrospectives were removed (2026-11-08, ORBIT-13);
// it keeps the event format the readers count, for old events and for tests.
func (s *Store) RecordUsage(in UsageInput) error {
	if !slices.Contains(UsageVerdicts, in.Verdict) || in.MemoryID == "" {
		return i18n.Errorf("memory.err.usage_verdict", ErrInvalid, strings.Join(UsageVerdicts, "·"))
	}
	data := map[string]any{"memoryId": in.MemoryID, "verdict": in.Verdict, "note": ClipRunes(MaskSecrets(in.Note), 200), "retroId": in.RetroID}
	if in.Verified {
		data["verified"] = true
	}
	if in.OccurredAt > 0 {
		data["occurredAt"] = in.OccurredAt
	}
	if err := s.RecordEvent(Event{Kind: EventMemoryUsed, Day: in.Day, Host: in.Host, Session: in.Session}, data); err != nil {
		return err
	}
	if in.Verdict == UsageFollowed && in.Verified {
		_, _, _ = s.ConfirmByEvidence(in.MemoryID)
	}
	return nil
}

// --- 반복 교정 ---

// RepeatedCorrection is how often the user corrected the AI about one target.
type RepeatedCorrection struct {
	Key         string   `json:"key"` // correction:<종류>:<대상>
	TargetKind  string   `json:"targetKind"`
	Target      string   `json:"target"`
	Title       string   `json:"title,omitempty"` // 기억이면 제목
	Sessions    int      `json:"sessions"`        // 교정이 나온 서로 다른 세션 수(확인함 뒤)
	Corrections int      `json:"corrections"`
	Threshold   int      `json:"threshold"`
	Over        bool     `json:"over"`
	Harness     bool     `json:"harness"` // 스킬·역할: 하네스 개선 후보
	First       int64    `json:"first"`
	Last        int64    `json:"last"`
	Quotes      []string `json:"quotes,omitempty"` // 최근 말부터 셋
	AckedAt     int64    `json:"ackedAt,omitempty"`
}

func correctionKey(kind, target string) string { return "correction:" + kind + ":" + target }

// RepeatedCorrections counts the corrections of each target since the person
// last marked it reviewed. With all false only the targets at or over the
// threshold (Policy.CorrectionSessions) come back, most sessions first.
func (s *Store) RepeatedCorrections(all bool) ([]RepeatedCorrection, error) {
	acks := map[string]int64{}
	arows, err := s.db.Query(`SELECT key, at FROM audit_acks`)
	if err != nil {
		return nil, err
	}
	for arows.Next() {
		var k string
		var at int64
		if arows.Scan(&k, &at) == nil {
			acks[k] = at
		}
	}
	arows.Close()
	rows, err := s.db.Query(`SELECT id, at, session, data FROM events WHERE kind=? ORDER BY id`, EventCorrection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type agg struct {
		r        RepeatedCorrection
		sessions map[string]bool
	}
	byKey := map[string]*agg{}
	var order []string
	for rows.Next() {
		var id, at int64
		var session, data string
		if rows.Scan(&id, &at, &session, &data) != nil {
			continue
		}
		var d struct {
			Quote      string `json:"quote"`
			MemoryID   string `json:"memoryId"`
			TargetKind string `json:"targetKind"`
			Target     string `json:"target"`
			OccurredAt int64  `json:"occurredAt"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		kind, target := d.TargetKind, d.Target
		if target == "" && d.MemoryID != "" {
			kind, target = TargetKindMemory, d.MemoryID
		}
		if target == "" || kind == "" {
			continue
		}
		if d.OccurredAt > 0 {
			at = d.OccurredAt
		}
		key := correctionKey(kind, target)
		if acked := acks[key]; acked > 0 && at <= acked {
			continue
		}
		a := byKey[key]
		if a == nil {
			a = &agg{r: RepeatedCorrection{Key: key, TargetKind: kind, Target: target, First: at, AckedAt: acks[key],
				Harness: kind == TargetKindSkill || kind == TargetKindRole}, sessions: map[string]bool{}}
			byKey[key] = a
			order = append(order, key)
		}
		sk := session
		if sk == "" {
			sk = fmt.Sprintf("event-%d", id)
		}
		a.sessions[sk] = true
		a.r.Corrections++
		a.r.First, a.r.Last = min(a.r.First, at), max(a.r.Last, at)
		if q := strings.TrimSpace(d.Quote); q != "" {
			a.r.Quotes = append([]string{q}, a.r.Quotes...)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	threshold := s.Policy.orDefault().CorrectionSessions
	var out []RepeatedCorrection
	for _, key := range order {
		a := byKey[key]
		r := a.r
		r.Sessions, r.Threshold = len(a.sessions), threshold
		r.Over = r.Sessions >= threshold
		if !all && !r.Over {
			continue
		}
		if len(r.Quotes) > 3 {
			r.Quotes = r.Quotes[:3]
		}
		if r.TargetKind == TargetKindMemory {
			if m, err := getTx(s.db, r.Target); err == nil {
				r.Title = m.Title
			}
		}
		out = append(out, r)
	}
	return sortRepeated(out), nil
}

func sortRepeated(out []RepeatedCorrection) []RepeatedCorrection {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Last > out[j].Last
	})
	return out
}

// RepeatedProcedures counts, for each live lesson tagged TagProcedure, the
// different works (refs of its work sources) it came out of since the person
// last marked it reviewed (key correction:procedure:<id>). Sessions is that
// number of works. With all false only those at or over the threshold
// (Policy.CorrectionSessions) come back.
func (s *Store) RepeatedProcedures(all bool) ([]RepeatedCorrection, error) {
	rows, err := s.db.Query(`SELECT m.id, m.title, m.tags, src.ref, src.at FROM memories m JOIN sources src ON src.memory_id=m.id
		WHERE m.kind=? AND m.state IN (?,?) AND src.kind=? AND src.ref<>'' ORDER BY src.at, src.rowid`,
		KindLesson, StateCandidate, StateConfirmed, SourceWork)
	if err != nil {
		return nil, err
	}
	type agg struct {
		r     RepeatedCorrection
		works map[string]bool
	}
	byID := map[string]*agg{}
	var order []string
	for rows.Next() {
		var id, title, tags, ref string
		var at int64
		if rows.Scan(&id, &title, &tags, &ref, &at) != nil {
			continue
		}
		a := byID[id]
		if a == nil {
			var tt []string
			_ = json.Unmarshal([]byte(tags), &tt)
			if !slices.Contains(tt, TagProcedure) {
				continue
			}
			a = &agg{r: RepeatedCorrection{Key: correctionKey(TargetKindProcedure, id), TargetKind: TargetKindProcedure, Target: id, Title: title,
				Harness: true}, works: map[string]bool{}}
			byID[id] = a
			order = append(order, id)
		}
		a.r.Corrections++ // 데려온 작업 출처 수(같은 작업이 두 번이면 둘)
		if a.r.First == 0 || at < a.r.First {
			a.r.First = at
		}
		a.r.Last = max(a.r.Last, at)
		a.works[ref] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	threshold := s.Policy.orDefault().CorrectionSessions
	var out []RepeatedCorrection
	for _, id := range order {
		a := byID[id]
		var acked int64
		_ = s.db.QueryRow(`SELECT at FROM audit_acks WHERE key=?`, a.r.Key).Scan(&acked)
		if acked > 0 { // 확인함 뒤에 붙은 출처만 다시 센다
			if err := s.countProcedureSince(&a.r, a.works, acked); err != nil {
				return nil, err
			}
		}
		r := a.r
		r.Sessions, r.Threshold, r.AckedAt = len(a.works), threshold, acked
		r.Over = r.Sessions >= threshold
		if r.Sessions == 0 || (!all && !r.Over) {
			continue
		}
		out = append(out, r)
	}
	return sortRepeated(out), nil
}

// countProcedureSince recounts a procedure's work sources added after since.
func (s *Store) countProcedureSince(r *RepeatedCorrection, works map[string]bool, since int64) error {
	clear(works)
	r.Corrections, r.First, r.Last = 0, 0, 0
	rows, err := s.db.Query(`SELECT ref, at FROM sources WHERE memory_id=? AND kind=? AND ref<>'' AND at>?`, r.Target, SourceWork, since)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		var at int64
		if rows.Scan(&ref, &at) != nil {
			continue
		}
		works[ref] = true
		r.Corrections++
		if r.First == 0 || at < r.First {
			r.First = at
		}
		r.Last = max(r.Last, at)
	}
	return rows.Err()
}

// Acknowledge marks an audit item reviewed: a repeated correction counts again
// from now on.
func (s *Store) Acknowledge(key string, w Who) error {
	if !strings.HasPrefix(key, "correction:") {
		return i18n.Errorf("memory.err.no_flag", ErrInvalid, key)
	}
	_, err := s.db.Exec(`INSERT INTO audit_acks(key,at,actor) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET at=excluded.at, actor=excluded.actor`, key, s.nowMs(), w.actor())
	return err
}

// --- 감사 목록 ---

// AuditFilter picks how far back the audit list looks.
type AuditFilter struct {
	Days  int // 자동 변경을 볼 날 수. 비우면 14
	Limit int // 자동 변경 최대 수. 비우면 100
	// Products are the names of the product map, for the two-topic title check (S4-12).
	Products []string
}

// AuditChange is one automatic change (or the automatic undo of a harmful one).
type AuditChange struct {
	LedgerID     string   `json:"ledgerId"`
	ProposalID   string   `json:"proposalId"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Evidence     string   `json:"evidence,omitempty"`
	Actor        string   `json:"actor,omitempty"`
	At           int64    `json:"at"`
	Targets      []string `json:"targets,omitempty"`
	Change       string   `json:"change,omitempty"`
	Verdict      string   `json:"verdict"`
	RevertedBy   string   `json:"revertedBy,omitempty"`
	RevertOf     string   `json:"revertOf,omitempty"`
	AutoReverted bool     `json:"autoReverted,omitempty"` // 해로움 판정으로 스스로 되돌렸다
}

// AuditConflict is an open conflict mark with the two memories' titles.
type AuditConflict struct {
	Flag
	Title      string `json:"title"`
	State      string `json:"state"`
	OtherTitle string `json:"otherTitle,omitempty"`
}

// maxAuditReview is how many memories due for review the audit list names; ReviewDueCount counts them all.
const maxAuditReview = 30

// auditReviewTitleRunes is how much of a title the audit list's review list shows (a title may be 200 runes).
const auditReviewTitleRunes = 80

// AuditReview is a memory due for review (L-1) in the audit list, the list the session start's count line points to.
type AuditReview struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	AppliesTo string `json:"appliesTo"`
	Title     string `json:"title"`
	ReviewAt  int64  `json:"reviewAt"` // 재검토일(Unix ms): 마지막 확인 + 90일
}

// Audit is the audit list.
type Audit struct {
	Changes     []AuditChange        `json:"changes"`
	Conflicts   []AuditConflict      `json:"conflicts"`
	Corrections []RepeatedCorrection `json:"corrections"`
	Pending     int                  `json:"pending"` // 대기열을 없애기 전에 남은 옛 대기 제안(proposals drain으로 정리)
	Policy      Policy               `json:"policy"`
	// PolicyPending names the counts still waiting for the user's decision (E2).
	PolicyPending []string `json:"policyPending,omitempty"`
	// TwoTopics are rules and decisions whose 「A — B」 title glues two topics (S4-12): split them.
	TwoTopics []TwoTopicTitle `json:"twoTopics"`
	// ReviewDue are the memories due for review, the oldest review date first (at most maxAuditReview), of every
	// scope: the session start counts only what its place sees, this list is the whole of it (명세 L-3).
	ReviewDue      []AuditReview `json:"reviewDue"`
	ReviewDueCount int           `json:"reviewDueCount"`
}

// PolicyPendingKeys are the configuration keys still waiting for the user (E2).
var PolicyPendingKeys = []string{"confirmFollowedVerified", "correctionRepeatSessions"}

// Audit reads the audit list: recent automatic changes, open conflict marks,
// repeated corrections over the threshold, and how many proposals wait.
func (s *Store) Audit(f AuditFilter) (Audit, error) {
	a := Audit{Changes: []AuditChange{}, Conflicts: []AuditConflict{}, Corrections: []RepeatedCorrection{}, Policy: s.Policy.orDefault(), PolicyPending: PolicyPendingKeys}
	days := f.Days
	if days <= 0 {
		days = 14
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	from := s.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	rows, err := s.db.Query(`SELECT `+prefixed("l.", ledgerCols)+`, p.title, p.payload FROM ledger l JOIN proposals p ON p.id=l.proposal_id
		WHERE p.status=? AND l.at>=? ORDER BY l.at DESC, l.rowid DESC LIMIT ?`, StatusAuto, from, limit)
	if err != nil {
		return a, err
	}
	for rows.Next() {
		var title, payload string
		e, err := scanLedgerWith(rows, &title, &payload)
		if err != nil {
			rows.Close()
			return a, err
		}
		var pl Payload
		_ = json.Unmarshal([]byte(payload), &pl)
		a.Changes = append(a.Changes, AuditChange{LedgerID: e.ID, ProposalID: e.ProposalID, Kind: e.ProposalKind, Title: title,
			Evidence: pl.Evidence, Actor: pl.Actor, At: e.At, Targets: e.Targets, Change: ClipRunes(e.Change, 2000), Verdict: e.Verdict,
			RevertedBy: e.RevertedBy, RevertOf: e.RevertOf})
	}
	rows.Close()
	for i := range a.Changes {
		if c := &a.Changes[i]; c.RevertedBy != "" {
			if r, err := s.LedgerEntry(c.RevertedBy); err == nil && s.isAuto(r) {
				c.AutoReverted = true
			}
		}
	}
	flags, err := s.OpenFlags()
	if err != nil {
		return a, err
	}
	for _, fl := range flags {
		if fl.Kind != FlagConflict {
			continue
		}
		c := AuditConflict{Flag: fl}
		if m, err := getTx(s.db, fl.MemoryID); err == nil {
			c.Title, c.State = m.Title, m.State
		}
		if o, err := getTx(s.db, fl.OtherID); err == nil {
			c.OtherTitle = o.Title
		}
		a.Conflicts = append(a.Conflicts, c)
	}
	if a.Corrections, err = s.RepeatedCorrections(false); err != nil {
		return a, err
	}
	procs, err := s.RepeatedProcedures(false)
	if err != nil {
		return a, err
	}
	a.Corrections = sortRepeated(append(a.Corrections, procs...))
	if a.Corrections == nil {
		a.Corrections = []RepeatedCorrection{}
	}
	a.Pending = s.PendingCount()
	if a.TwoTopics, err = s.TwoTopicTitles(f.Products); err != nil {
		return a, err
	}
	due, err := s.ReviewDueMemories(nil)
	if err != nil {
		return a, err
	}
	a.ReviewDue, a.ReviewDueCount = []AuditReview{}, len(due)
	for _, m := range due[:min(len(due), maxAuditReview)] {
		a.ReviewDue = append(a.ReviewDue, AuditReview{ID: m.ID, Kind: m.Kind, State: m.State, AppliesTo: m.AppliesTo, Title: ClipRunes(m.Title, auditReviewTitleRunes), ReviewAt: ReviewDate(m)})
	}
	return a, nil
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ",")
}

func scanLedgerWith(row interface{ Scan(...any) error }, extra ...any) (LedgerEntry, error) {
	var e LedgerEntry
	var targets, observe, before, obs string
	var evidence int
	dst := []any{&e.ID, &e.At, &e.Day, &e.ProposalID, &e.ProposalKind, &e.TargetKind, &targets, &observe, &e.Change, &e.Basis,
		&before, &e.Verdict, &evidence, &obs, &e.JudgedAt, &e.LessonID, &e.RevertOf, &e.RevertedBy}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return e, err
	}
	e.Evidence = evidence == 1
	_ = json.Unmarshal([]byte(targets), &e.Targets)
	_ = json.Unmarshal([]byte(observe), &e.Observe)
	_ = json.Unmarshal([]byte(before), &e.Before)
	_ = json.Unmarshal([]byte(obs), &e.Observation)
	return e, nil
}

// RecentAutoReverts counts the harmful automatic changes undone since a time (ms).
func (s *Store) RecentAutoReverts(sinceMs int64) int {
	rows, err := s.db.Query(`SELECT data FROM events WHERE kind=? AND at>=?`, EventAutoReverted, sinceMs)
	if err != nil {
		return 0
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var data string
		var d struct {
			Error string `json:"error"`
		}
		if rows.Scan(&data) == nil && json.Unmarshal([]byte(data), &d) == nil && d.Error == "" {
			n++
		}
	}
	return n
}

// openConflictQuery reads the open conflict marks that have a live value (candidate or confirmed) on both sides,
// as (old id, new id) pairs. The pair's old side is the mark's other_id: the memory a recall line shows with its
// newer, disagreeing value beside it. A mark whose old side went stale (the user replaced it) is not open here:
// there is no current value left to warn about.
const openConflictQuery = `SELECT f.other_id, f.memory_id FROM audit_flags f JOIN memories n ON n.id=f.memory_id JOIN memories o ON o.id=f.other_id
	WHERE f.kind=? AND f.resolved_at=0 AND f.other_id<>'' AND n.state IN ('candidate','confirmed') AND o.state IN ('candidate','confirmed')`

// conflictChunk is how many ids one OpenConflictsOf query binds, well under SQLite's variable limit.
const conflictChunk = 400

// OpenConflictsOf gives, for each of ids that is the old side of an open conflict, the ids of the newer values
// that disagree with it (oldest mark first): the 「어긋나는 새 사실」 a recall line, memory_find and memory_get
// show beside it (명세 C-5). A pair whose newer side is no longer a live value (rejected, replaced, forgotten)
// is not open. An id with no open conflict is absent from the map. It reads once per chunk of ids however many
// memories the caller shows, and nothing when there are no ids.
func (s *Store) OpenConflictsOf(ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	ids = uniq(ids)
	for len(ids) > 0 {
		n := min(len(ids), conflictChunk)
		args := []any{FlagConflict}
		marks := make([]string, n)
		for i, id := range ids[:n] {
			marks[i] = "?"
			args = append(args, id)
		}
		if err := s.collectConflicts(out, openConflictQuery+` AND f.other_id IN (`+strings.Join(marks, ",")+`) ORDER BY f.at, f.rowid`, args...); err != nil {
			return nil, err
		}
		ids = ids[n:]
	}
	return out, nil
}

// OpenConflicts is OpenConflictsOf for every memory at once: one query however many conflicts are open. A reader
// that looks at many memories (the session start) takes it instead of binding their ids.
func (s *Store) OpenConflicts() (map[string][]string, error) {
	out := map[string][]string{}
	err := s.collectConflicts(out, openConflictQuery+` ORDER BY f.at, f.rowid`, FlagConflict)
	return out, err
}

// OpenConflictSides is OpenConflictsOf with the newer values read as memories (without their sources): their
// title, scope, state and days, which a caller needs to judge whether a conflict line may show them
// (Viewer.ShownSides). Two queries however many pairs: the pairs, then the newer values together.
func (s *Store) OpenConflictSides(ids []string) (map[string][]Memory, error) {
	pairs, err := s.OpenConflictsOf(ids)
	if err != nil {
		return nil, err
	}
	return s.sidesOf(pairs)
}

// OpenConflictSidesAll is OpenConflictSides for every open conflict: the session start reads the whole core
// layer and the recent decisions, so it takes them all instead of binding their ids.
func (s *Store) OpenConflictSidesAll() (map[string][]Memory, error) {
	pairs, err := s.OpenConflicts()
	if err != nil {
		return nil, err
	}
	return s.sidesOf(pairs)
}

func (s *Store) sidesOf(pairs map[string][]string) (map[string][]Memory, error) {
	out := map[string][]Memory{}
	if len(pairs) == 0 {
		return out, nil
	}
	var all []string
	for _, news := range pairs {
		all = append(all, news...)
	}
	b, _ := json.Marshal(uniq(all))
	rows, err := s.db.Query(`SELECT `+memoryCols+` FROM memories WHERE id IN (SELECT value FROM json_each(?))`, string(b))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]Memory{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		byID[m.ID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for oldID, news := range pairs {
		for _, id := range news {
			if m, ok := byID[id]; ok {
				out[oldID] = append(out[oldID], m)
			}
		}
	}
	return out, nil
}

func (s *Store) collectConflicts(out map[string][]string, query string, args ...any) error {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var oldID, newID string
		if err := rows.Scan(&oldID, &newID); err != nil {
			return err
		}
		if !slices.Contains(out[oldID], newID) {
			out[oldID] = append(out[oldID], newID)
		}
	}
	return rows.Err()
}

// OpenConflictCount is how many open conflict pairs have a live value on both sides: what the session start
// counts in one line when the auto mode lets conflicts pass without asking (명세 4.3).
func (s *Store) OpenConflictCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT DISTINCT f.other_id, f.memory_id FROM audit_flags f
		JOIN memories n ON n.id=f.memory_id JOIN memories o ON o.id=f.other_id
		WHERE f.kind=? AND f.resolved_at=0 AND n.state IN ('candidate','confirmed') AND o.state IN ('candidate','confirmed'))`, FlagConflict).Scan(&n)
	return n, err
}

// settleConflictsAfterQuote settles the open conflict pairs of memory id after the user's own words were added to
// it (a quote source on a save or an edit), so the value the person chose is the current one (명세 C-6, and the
// first row of the replacement table: the user's words replace any memory):
//
//   - id is the newer side: it is confirmed and the old memory is replaced by it, as a replacement (stale,
//     superseded_by, a version reason). When the replacement is refused (a trust or protection check) the pair
//     stays open and nothing is closed: the pair must not look settled when it is not.
//   - id is the old side: it stays the current value; the mark is closed and the newer candidate is left as it
//     is. Nothing here keeps that candidate from confirming itself later (see confirmBlocked: the open mark was
//     what blocked it).
//
// A closed mark says who closed it (resolved_by = the user). It reports whether it changed a memory.
func (s *Store) settleConflictsAfterQuote(id, quote string) bool {
	rows, err := s.db.Query(`SELECT id, memory_id, other_id FROM audit_flags WHERE kind=? AND resolved_at=0 AND (memory_id=? OR other_id=?)`, FlagConflict, id, id)
	if err != nil {
		return false
	}
	type pair struct{ flag, newID, oldID string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if rows.Scan(&p.flag, &p.newID, &p.oldID) == nil {
			pairs = append(pairs, p)
		}
	}
	rows.Close()
	if quote = strings.TrimSpace(quote); quote == "" {
		quote = "사용자 말"
	}
	w := Who{Actor: ActorAI, UserQuote: quote}
	changed := false
	for _, p := range pairs {
		if p.newID == id {
			nw, err := getTx(s.db, p.newID)
			if err != nil {
				continue
			}
			if old, err := getTx(s.db, p.oldID); err == nil && (old.State == StateCandidate || old.State == StateConfirmed) {
				if _, err := s.SupersedeAs(p.oldID, p.newID, "사용자 말로 대체: "+nw.Title, w); err != nil {
					continue // 막힌 쌍은 열어 둔다
				}
				changed = true
			}
			if nw.State == StateCandidate {
				confirmed := StateConfirmed
				if _, err := s.Update(UpdateInput{ID: nw.ID, State: &confirmed, Who: w, Reason: "사용자 말로 정함"}); err != nil {
					continue
				}
				changed = true
			}
		}
		_, _ = s.db.Exec(`UPDATE audit_flags SET resolved_at=CASE WHEN resolved_at=0 THEN ? ELSE resolved_at END,
			resolution=CASE WHEN resolution='' THEN 'user_words' ELSE resolution END, resolved_by=? WHERE id=?`, s.nowMs(), ActorUser, p.flag)
	}
	return changed
}
