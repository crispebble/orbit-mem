package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onidot-labs/openorbit/internal/i18n"
)

// What an approved change touched (설계 「변경 효과 원장」).
const (
	TargetMemory      = "memory"      // 기억
	TargetHarness     = "harness"     // Orbit 플러그인 원본(스킬·역할)
	TargetInstruction = "instruction" // 지침 파일
)

// Effect verdicts of a change.
const (
	VerdictPending  = "pending"   // 관찰 중
	VerdictHelped   = "helped"    // 도움됨
	VerdictHarmful  = "harmful"   // 해로움
	VerdictNoEffect = "no_effect" // 효과 없음
	VerdictMixed    = "mixed"     // 섞임: 같은 대상을 같은 기간에 건드린 변경이 여럿
)

// FileBefore is a file as it was before a change, to undo it.
type FileBefore struct {
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Absent    bool   `json:"absent,omitempty"`
	AfterHash string `json:"afterHash,omitempty"` // 바꾼 직후 hash. 되돌릴 때 그 뒤 바뀌었는지 본다
}

// LedgerBefore is what a change replaced.
type LedgerBefore struct {
	Memories []Memory       `json:"memories,omitempty"` // 바뀐 기억의 바꾸기 전 판본
	Created  []string       `json:"created,omitempty"`  // 새로 만든 기억
	File     *FileBefore    `json:"file,omitempty"`
	After    map[string]int `json:"after,omitempty"` // 바꾼 직후 기억 판본. 되돌릴 때 그 뒤 바뀌었는지 본다
	Link     string         `json:"link,omitempty"`  // 승인으로 생긴 연결. 되돌릴 때 이 연결만 뗀다
}

// ErrMemoryChanged means a memory changed again after the change being undone.
var ErrMemoryChanged = i18n.Err("memory.err.memory_changed")

// Window counts the signals about a change's memories in one period.
type Window struct {
	Bad        int `json:"bad"`        // 그 기억을 가리킨 교정 + 어김 판정
	Irrelevant int `json:"irrelevant"` // 상관없음 판정
	Sessions   int `json:"sessions"`   // 그 기억을 넣은 세션
}

// Observation compares the period before a change with the period after.
type Observation struct {
	From      int64    `json:"from"`
	Split     int64    `json:"split"`
	To        int64    `json:"to"`
	Before    Window   `json:"before"`
	After     Window   `json:"after"`
	MixedWith []string `json:"mixedWith,omitempty"`
}

// LedgerEntry is one approved change and its effect.
type LedgerEntry struct {
	ID           string       `json:"id"`
	At           int64        `json:"at"`
	Day          string       `json:"day"`
	ProposalID   string       `json:"proposalId"`
	ProposalKind string       `json:"proposalKind"`
	TargetKind   string       `json:"targetKind"`
	Targets      []string     `json:"targets"`
	Observe      []string     `json:"observe"` // 효과를 볼 기억
	Change       string       `json:"change"`
	Basis        string       `json:"basis"`
	Before       LedgerBefore `json:"-"`
	Verdict      string       `json:"verdict"`
	Evidence     bool         `json:"evidence"`
	Observation  Observation  `json:"observation"`
	JudgedAt     int64        `json:"judgedAt,omitempty"`
	LessonID     string       `json:"lessonId,omitempty"`
	RevertOf     string       `json:"revertOf,omitempty"`
	RevertedBy   string       `json:"revertedBy,omitempty"`
}

// FileTarget says whether a file is part of the harness (the plugin's skills
// and roles) or an instruction file.
func FileTarget(path string) string {
	p := filepath.ToSlash(path)
	base := filepath.Base(p)
	if strings.Contains(p, "/skills/") || strings.Contains(p, "/agents/") || strings.Contains(p, "/knowledge/") || base == "agents-common.md" || base == "plugin.json" {
		return TargetHarness
	}
	return TargetInstruction
}

const ledgerCols = `id,at,day,proposal_id,proposal_kind,target_kind,targets,observe,change,basis,before,verdict,evidence,observation,judged_at,lesson_id,revert_of,reverted_by`

func scanLedger(row interface{ Scan(...any) error }) (LedgerEntry, error) {
	var e LedgerEntry
	var targets, observe, before, obs string
	var evidence int
	if err := row.Scan(&e.ID, &e.At, &e.Day, &e.ProposalID, &e.ProposalKind, &e.TargetKind, &targets, &observe, &e.Change, &e.Basis,
		&before, &e.Verdict, &evidence, &obs, &e.JudgedAt, &e.LessonID, &e.RevertOf, &e.RevertedBy); err != nil {
		return e, err
	}
	e.Evidence = evidence == 1
	_ = json.Unmarshal([]byte(targets), &e.Targets)
	_ = json.Unmarshal([]byte(observe), &e.Observe)
	_ = json.Unmarshal([]byte(before), &e.Before)
	_ = json.Unmarshal([]byte(obs), &e.Observation)
	return e, nil
}

// LedgerEntry reads one line of the ledger.
func (s *Store) LedgerEntry(id string) (LedgerEntry, error) {
	e, err := scanLedger(s.db.QueryRow(`SELECT `+ledgerCols+` FROM ledger WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, i18n.Errorf("memory.err.not_in_ledger", ErrInvalid, id)
	}
	return e, err
}

// LedgerFilter selects ledger lines by time (ms, [From, To)) and verdict.
type LedgerFilter struct {
	FromMs, ToMs int64
	Verdicts     []string
	Limit        int
}

// Ledger lists ledger lines, newest first.
func (s *Store) Ledger(f LedgerFilter) ([]LedgerEntry, error) {
	q := `SELECT ` + ledgerCols + ` FROM ledger WHERE 1=1`
	var args []any
	if f.FromMs > 0 {
		q += ` AND at>=?`
		args = append(args, f.FromMs)
	}
	if f.ToMs > 0 {
		q += ` AND at<?`
		args = append(args, f.ToMs)
	}
	if len(f.Verdicts) > 0 {
		q += ` AND verdict IN (?` + strings.Repeat(",?", len(f.Verdicts)-1) + `)`
		for _, v := range f.Verdicts {
			args = append(args, v)
		}
	}
	q += ` ORDER BY at DESC, rowid DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		e, err := scanLedger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LedgerSummary counts the changes made between two times and the verdicts given between them.
type LedgerSummary struct {
	Changes  int            `json:"changes"`
	Judged   map[string]int `json:"judged"`
	Pending  int            `json:"pending"` // 지금 관찰 중인 변경(기간과 무관)
	Evidence int            `json:"evidence"`
}

// SummarizeLedger is the ledger part of the metrics.
func (s *Store) SummarizeLedger(fromMs, toMs int64) (LedgerSummary, error) {
	sum := LedgerSummary{Judged: map[string]int{}}
	// 실험 줄(lab)은 승인한 변경이 아니다. 실험실 지표가 따로 센다.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger WHERE at>=? AND at<? AND proposal_kind<>?`, fromMs, toMs, LedgerKindLab).Scan(&sum.Changes); err != nil {
		return sum, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger WHERE verdict=? AND proposal_kind<>?`, VerdictPending, LedgerKindLab).Scan(&sum.Pending); err != nil {
		return sum, err
	}
	rows, err := s.db.Query(`SELECT verdict, evidence FROM ledger WHERE judged_at>=? AND judged_at<? AND proposal_kind<>?`, fromMs, toMs, LedgerKindLab)
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var ev int
		if rows.Scan(&v, &ev) == nil {
			sum.Judged[v]++
			sum.Evidence += ev
		}
	}
	return sum, rows.Err()
}

func memoryText(m Memory) string {
	return fmt.Sprintf("제목: %s\n종류: %s\n범위: %s\n상태: %s\n태그: %s\n\n%s\n", m.Title, m.Kind, m.Scope, m.State, strings.Join(m.Tags, ", "), m.Body)
}

func uniq(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// snapshotFor captures what an approval is about to change, so the ledger can
// show the change and undo it later.
func (s *Store) snapshotFor(p Proposal) (LedgerBefore, error) {
	var b LedgerBefore
	ids := p.MemoryIDs
	switch p.Kind {
	case ProposalLink:
		ids = nil // 연결은 기억 판을 바꾸지 않는다
	case ProposalApplyFile:
		ids = nil
		b.File = fileBefore(p.Payload.Path)
	case ProposalRevert:
		e, err := s.LedgerEntry(p.Payload.LedgerID)
		if err != nil {
			return b, err
		}
		ids = nil
		for _, m := range e.Before.Memories {
			ids = append(ids, m.ID)
		}
		ids = append(ids, e.Before.Created...)
		if e.Before.File != nil {
			b.File = fileBefore(e.Before.File.Path)
		}
	}
	for _, id := range uniq(ids) {
		m, err := getTx(s.db, id)
		if err != nil {
			return b, err
		}
		b.Memories = append(b.Memories, m)
	}
	return b, nil
}

func fileBefore(path string) *FileBefore {
	f := &FileBefore{Path: path}
	if c, err := os.ReadFile(path); err == nil {
		f.Content = string(c)
	} else {
		f.Absent = true
	}
	return f
}

// recordLedger writes the line for an approved proposal.
func (s *Store) recordLedger(p Proposal, before LedgerBefore, res DecideResult) (string, error) {
	e := LedgerEntry{ID: NewID("l-"), At: s.nowMs(), ProposalID: p.ID, ProposalKind: p.Kind, TargetKind: TargetMemory, Verdict: VerdictPending}
	e.Day = Day(time.UnixMilli(e.At))
	var change strings.Builder
	if before.File != nil {
		e.TargetKind = FileTarget(before.File.Path)
		e.Targets = []string{before.File.Path}
		after, _ := os.ReadFile(before.File.Path)
		before.File.AfterHash = FileHash(before.File.Path)
		change.WriteString("파일: " + before.File.Path + "\n" + LineDiff(before.File.Content, string(after), 3))
	}
	if res.Created != "" {
		before.Created = append(before.Created, res.Created)
	}
	var memIDs []string
	for _, m := range before.Memories {
		memIDs = append(memIDs, m.ID)
		after, err := getTx(s.db, m.ID)
		if err != nil {
			continue
		}
		if d := LineDiff(memoryText(m), memoryText(after), 2); strings.TrimSpace(d) != "" {
			fmt.Fprintf(&change, "기억 %s\n%s", m.ID, d)
		}
	}
	for _, id := range before.Created {
		memIDs = append(memIDs, id)
		if m, err := getTx(s.db, id); err == nil {
			fmt.Fprintf(&change, "새 기억 %s\n%s", id, LineDiff("", memoryText(m), 0))
		}
	}
	if res.Link != "" {
		if l, err := s.linkByID(res.Link); err == nil {
			verb := "연결 추가"
			if p.Kind == ProposalRevert {
				verb = "연결 뗌"
			} else {
				before.Link = l.ID
			}
			fmt.Fprintf(&change, "%s %s: %s — %s", verb, l.ID, l.A, l.B)
			if l.Note != "" {
				change.WriteString(" (" + l.Note + ")")
			}
			change.WriteString("\n")
			memIDs = append(memIDs, l.A, l.B)
		}
	}
	memIDs = uniq(memIDs)
	before.After = map[string]int{}
	for _, id := range memIDs {
		if m, err := getTx(s.db, id); err == nil {
			before.After[id] = m.Version
		}
	}
	if before.File == nil {
		e.Targets = memIDs
		e.Observe = memIDs
	} else {
		e.Observe = uniq(p.MemoryIDs)
	}
	e.Change = change.String()
	e.Basis = strings.TrimSpace(p.Title + "\n" + ClipRunes(p.Body, 600))
	if p.RetroID != "" {
		e.Basis += "\n회고 " + p.RetroID
	}
	if p.Kind == ProposalRevert {
		e.RevertOf = p.Payload.LedgerID
	}
	e.Before = before
	targets, _ := json.Marshal(nonNil(e.Targets))
	observe, _ := json.Marshal(nonNil(e.Observe))
	bb, _ := json.Marshal(e.Before)
	obs, _ := json.Marshal(e.Observation)
	if _, err := s.db.Exec(`INSERT INTO ledger(`+ledgerCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.At, e.Day, e.ProposalID, e.ProposalKind, e.TargetKind, string(targets), string(observe), e.Change, e.Basis,
		string(bb), e.Verdict, 0, string(obs), 0, "", e.RevertOf, ""); err != nil {
		return "", err
	}
	if e.RevertOf != "" {
		if _, err := s.db.Exec(`UPDATE ledger SET reverted_by=? WHERE id=?`, e.ID, e.RevertOf); err != nil {
			return e.ID, err
		}
	}
	return e.ID, nil
}

// revert undoes a ledger line: memories go back to the version before the
// change, memories the change made go stale, and a file gets its old content.
// Nothing is undone when a memory or the file changed again since: the undo
// would wipe that later change. A file already back to its old content (an
// earlier attempt stopped half way) counts as undone.
func (s *Store) revert(p Proposal, res *DecideResult) error {
	e, err := s.LedgerEntry(p.Payload.LedgerID)
	if err != nil {
		return err
	}
	if e.RevertedBy != "" {
		return i18n.Errorf("memory.err.already_reverted", ErrInvalid, e.RevertedBy)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := make([]string, 0, len(e.Before.Memories)+len(e.Before.Created))
	for _, m := range e.Before.Memories {
		ids = append(ids, m.ID)
	}
	ids = append(ids, e.Before.Created...)
	for _, id := range uniq(ids) {
		cur, err := getTx(tx, id)
		if err != nil {
			return err
		}
		if v, ok := e.Before.After[id]; ok && cur.Version != v {
			return i18n.Errorf("memory.err.memory_changed_at", ErrMemoryChanged, id, v, cur.Version)
		}
	}
	writeFile := false
	if f := e.Before.File; f != nil {
		old := "absent"
		if !f.Absent {
			old = hashBytes([]byte(f.Content))
		}
		switch FileHash(f.Path) {
		case f.AfterHash:
			writeFile = true
		case old: // 앞선 시도가 파일만 되돌리고 멈췄다
		default:
			return ErrFileChanged
		}
	}
	now := s.nowMs()
	restored := map[string]bool{}
	for _, snap := range e.Before.Memories {
		m, err := getTx(tx, snap.ID)
		if err != nil {
			return err
		}
		m.Kind, m.Scope, m.Project, m.State = snap.Kind, snap.Scope, snap.Project, snap.State
		m.Title, m.Body, m.Tags = snap.Title, snap.Body, snap.Tags
		m.SupersededBy, m.StaleReason = snap.SupersededBy, snap.StaleReason
		m.Version++
		m.UpdatedAt = now
		if err := writeTx(tx, m, "revert", "되돌림 "+p.ID, now); err != nil {
			return err
		}
		restored[m.ID] = true
		res.Changed = append(res.Changed, m.ID)
	}
	for _, id := range e.Before.Created {
		if restored[id] {
			continue
		}
		m, err := getTx(tx, id)
		if err != nil {
			return err
		}
		m.State, m.StaleReason = StateStale, "되돌림 "+p.ID
		m.Version++
		m.UpdatedAt = now
		if err := writeTx(tx, m, StateStale, "되돌림 "+p.ID, now); err != nil {
			return err
		}
		res.Changed = append(res.Changed, id)
	}
	// 파일은 기억을 모두 확인한 뒤, 기억을 확정하기 전에 쓴다. 쓰고 나서 기억 쪽이
	// 실패하면 다음 승인이 「이미 옛 내용」으로 보고 기억만 마저 되돌린다.
	if f := e.Before.File; f != nil {
		if writeFile {
			target, err := realTarget(f.Path)
			if err != nil {
				return err
			}
			if f.Absent {
				if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
					return err
				}
			} else if err := writeFileAtomic(target, []byte(f.Content)); err != nil {
				return err
			}
		}
		res.File = f.Path
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 연결은 그 연결 id만 뗀다. 사용자가 이미 뗐으면 할 일이 없고, 그 뒤 다시 이은 연결은 건드리지 않는다.
	if e.Before.Link != "" {
		removed, err := s.UnlinkID(e.Before.Link, LinkOriginRetroRevert, "되돌림 "+p.ID)
		if err != nil {
			return err
		}
		if removed {
			res.Link = e.Before.Link
		}
	}
	return nil
}

// SetLedgerLesson links the lesson a retrospective drew from a judged change.
// Only a change with evidence that it helped or harmed gets one, and only one.
func (s *Store) SetLedgerLesson(id, lessonID string) error {
	e, err := s.LedgerEntry(id)
	if err != nil {
		return err
	}
	if !e.Evidence || (e.Verdict != VerdictHelped && e.Verdict != VerdictHarmful) || e.LessonID != "" {
		return i18n.Errorf("memory.err.lesson_verdict", ErrInvalid)
	}
	_, err = s.db.Exec(`UPDATE ledger SET lesson_id=? WHERE id=? AND lesson_id=''`, lessonID, id)
	return err
}

func subset(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range b {
		set[x] = true
	}
	for _, x := range a {
		if !set[x] {
			return false
		}
	}
	return true
}

func overlaps(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if set[x] {
			return true
		}
	}
	return false
}

// JudgeLedger judges every pending change whose period after the change
// (windowDays) has ended, and returns how many it judged.
func (s *Store) JudgeLedger(windowDays int) (int, error) {
	if windowDays < 1 {
		return 0, i18n.Errorf("memory.err.observe_days", ErrInvalid)
	}
	w := int64(windowDays) * 24 * 3600 * 1000
	now := s.nowMs()
	all, err := s.Ledger(LedgerFilter{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range all {
		if e.Verdict != VerdictPending || e.At+w > now {
			continue
		}
		obs := Observation{From: e.At - w, Split: e.At, To: e.At + w}
		for _, o := range all {
			if o.ID == e.ID || o.ProposalKind == LedgerKindLab || o.At <= e.At-w || o.At >= e.At+w {
				continue
			}
			if overlaps(append(append([]string{}, e.Targets...), e.Observe...), append(append([]string{}, o.Targets...), o.Observe...)) {
				obs.MixedWith = append(obs.MixedWith, o.ID)
			}
		}
		verdict, evidence := VerdictMixed, false
		if len(e.Observe) > 0 {
			if err := s.observe(&obs, e.Observe); err != nil {
				return n, err
			}
			// 이 변경이 만든 기억만 보면 변경 전에는 넣을 수 없었다: 비교할 전이 없다.
			comparable := !subset(e.Observe, e.Before.Created)
			evidence = comparable && obs.Before.Bad+obs.After.Bad+obs.Before.Irrelevant+obs.After.Irrelevant > 0
		}
		if len(obs.MixedWith) == 0 {
			verdict = judge(obs, evidence)
		}
		ob, _ := json.Marshal(obs)
		ev := 0
		if evidence {
			ev = 1
		}
		if _, err := s.db.Exec(`UPDATE ledger SET verdict=?, evidence=?, observation=?, judged_at=? WHERE id=? AND verdict=?`,
			verdict, ev, string(ob), now, e.ID, VerdictPending); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// compareRate compares after with before, per session when both periods have
// sessions, else by count: -1 fewer after, 1 more after, 0 same.
func compareRate(before, after, sb, sa int) int {
	l, r := after, before
	if sb > 0 && sa > 0 {
		l, r = after*sb, before*sa
	}
	switch {
	case l < r:
		return -1
	case l > r:
		return 1
	}
	return 0
}

func judge(obs Observation, evidence bool) string {
	if !evidence {
		return VerdictNoEffect
	}
	b, a := obs.Before, obs.After
	c := compareRate(b.Bad, a.Bad, b.Sessions, a.Sessions)
	if c == 0 {
		c = compareRate(b.Irrelevant, a.Irrelevant, b.Sessions, a.Sessions)
	}
	switch c {
	case -1:
		return VerdictHelped
	case 1:
		return VerdictHarmful
	}
	return VerdictNoEffect
}

// observe counts the signals about ids before and after obs.Split.
func (s *Store) observe(obs *Observation, ids []string) error {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	evs, err := s.Events(EventFilter{FromDay: Day(time.UnixMilli(obs.From)), ToDay: Day(time.UnixMilli(obs.To))})
	if err != nil {
		return err
	}
	sessions := [2]map[string]bool{{}, {}}
	// 한 세션에서 같은 기억을 교정하고 어김으로도 판정하면 나쁜 신호 한 번이다.
	// 세션 끝 회고는 교정이 가리킨 기억을 어김으로 맞추므로, 따로 세면 변경 뒤만 부풀어 오른다.
	bad := [2]map[string]bool{{}, {}}
	for _, e := range evs {
		var d struct {
			MemoryIDs  []string `json:"memoryIds"`
			MemoryID   string   `json:"memoryId"`
			Verdict    string   `json:"verdict"`
			OccurredAt int64    `json:"occurredAt"`
		}
		_ = json.Unmarshal(e.Data, &d)
		at := e.At
		if d.OccurredAt > 0 {
			at = d.OccurredAt
		}
		if at < obs.From || at >= obs.To {
			continue
		}
		side, win := 0, &obs.Before
		if at >= obs.Split {
			side, win = 1, &obs.After
		}
		badKey := e.Session + "\x00" + d.MemoryID
		if e.Session == "" {
			badKey = fmt.Sprintf("event-%d", e.ID)
		}
		switch e.Kind {
		case EventInjectSessionStart, EventInjectPrompt:
			for _, id := range d.MemoryIDs {
				if want[id] {
					key := e.Session
					if key == "" {
						key = fmt.Sprintf("event-%d", e.ID)
					}
					sessions[side][key] = true
					break
				}
			}
		case EventCorrection:
			if want[d.MemoryID] {
				bad[side][badKey] = true
			}
		case EventMemoryUsed:
			if !want[d.MemoryID] {
				continue
			}
			switch d.Verdict {
			case UsageViolated:
				bad[side][badKey] = true
			case UsageIrrelevant:
				win.Irrelevant++
			}
		}
	}
	obs.Before.Sessions, obs.After.Sessions = len(sessions[0]), len(sessions[1])
	obs.Before.Bad, obs.After.Bad = len(bad[0]), len(bad[1])
	return nil
}
