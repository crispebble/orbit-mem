package memory

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpen_옛_DB에_2차_열과_표를_더한다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// 1차(0.2.0) retros 표: session 열이 없다.
	if _, err := db.Exec(`CREATE TABLE retros (id TEXT PRIMARY KEY, kind TEXT NOT NULL, period TEXT NOT NULL, status TEXT NOT NULL, host TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0, summary TEXT NOT NULL DEFAULT '', flags TEXT NOT NULL DEFAULT '[]',
		journal_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '');
		INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-old','daily','2026-10-26','done',1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var session string
	if err := s.DB().QueryRow(`SELECT session FROM retros WHERE id='r-old'`).Scan(&session); err != nil || session != "" {
		t.Fatalf("session column: %q %v", session, err)
	}
	for _, table := range []string{"session_ends", "retro_coverage", "session_bundles", "judgments", "ledger"} {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
	}
	s.Close()
	if s, err = Open(path); err != nil { // 두 번 열어도 된다
		t.Fatal(err)
	}
}

func TestJudgment_거절은_기억없이_사유와_남고_최근_거절로_읽힌다(t *testing.T) {
	s, c := newTestStore(t)
	if _, err := s.RecordJudgment(Judgment{RetroID: "r-1", Kind: KindFact, Title: "일회성 경로", Verdict: VerdictReject, Reason: "그 사안에만 맞는 사실"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordJudgment(Judgment{RetroID: "r-1", Kind: KindFact, Title: "아무 사유 없음", Verdict: VerdictReject}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reject without reason: %v", err)
	}
	if _, err := s.RecordJudgment(Judgment{RetroID: "r-1", Kind: KindFact, Title: "이상한 판정", Verdict: "maybe"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict: %v", err)
	}
	c.t = c.t.Add(time.Hour)
	m := candidate(t, s, "남길 교훈")
	if _, err := s.RecordJudgment(Judgment{RetroID: "r-1", MemoryID: m.ID, Kind: m.Kind, Title: m.Title, Verdict: VerdictKeep}); err != nil {
		t.Fatal(err)
	}
	rej, err := s.RecentRejections(10)
	if err != nil || len(rej) != 1 || rej[0].Title != "일회성 경로" || rej[0].Reason != "그 사안에만 맞는 사실" {
		t.Fatalf("rejections = %+v %v", rej, err)
	}
}

func TestJudgment_사용자가_거절하거나_고쳐서_승인하면_같은_기록에_붙는다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "가 후보")
	b := candidate(t, s, "나 후보")
	for _, m := range []Memory{a, b} {
		if _, err := s.RecordJudgment(Judgment{RetroID: "r-1", MemoryID: m.ID, Kind: m.Kind, Title: m.Title, Verdict: VerdictKeep, Reason: "다시 쓸 것"}); err != nil {
			t.Fatal(err)
		}
	}
	pa, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "가 확정", MemoryIDs: []string{a.ID}})
	pb, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "나 확정", MemoryIDs: []string{b.ID}})
	if _, err := s.Decide(DecideInput{ID: pa.ID, Decision: "reject", Note: "이건 사실이 아니다"}); err != nil {
		t.Fatal(err)
	}
	title := "나 고친 제목"
	if _, err := s.Decide(DecideInput{ID: pb.ID, Decision: "approve", Title: &title, Note: "제목이 모호"}); err != nil {
		t.Fatal(err)
	}
	ja, _ := s.JudgmentsFor(a.ID)
	if len(ja) != 1 || ja[0].UserDecision != "reject" || ja[0].UserNote != "이건 사실이 아니다" || ja[0].ProposalID != pa.ID || ja[0].Verdict != VerdictKeep {
		t.Fatalf("a = %+v", ja)
	}
	jb, _ := s.JudgmentsFor(b.ID)
	if len(jb) != 1 || jb[0].UserDecision != "edit" || jb[0].UserNote != "제목이 모호" {
		t.Fatalf("b = %+v", jb)
	}
	rej, _ := s.RecentRejections(10)
	if len(rej) != 2 {
		t.Fatalf("recent rejections = %+v", rej)
	}
	// 기억 판정이 없는 제안의 거절도 제안마다 한 줄로 남는다.
	pd, _ := s.Propose(ProposalInput{Kind: ProposalDirection, Title: "방향", Body: "이유"})
	if _, err := s.Decide(DecideInput{ID: pd.ID, Decision: "reject", Note: "지금은 아님"}); err != nil {
		t.Fatal(err)
	}
	rej, _ = s.RecentRejections(10)
	if len(rej) != 3 || rej[0].ProposalID != pd.ID || rej[0].UserNote != "지금은 아님" || rej[0].Kind != ProposalDirection {
		t.Fatalf("direction rejection = %+v", rej)
	}
}

func TestProposal_고치기_제안은_제목_본문_태그_범위를_바꾼다(t *testing.T) {
	s, _ := newTestStore(t)
	m := candidate(t, s, "모호한 교훈")
	if _, err := s.Propose(ProposalInput{Kind: ProposalEdit, Title: "고치기", MemoryIDs: []string{m.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("edit without change: %v", err)
	}
	p, err := s.Propose(ProposalInput{Kind: ProposalEdit, Title: "문구 고치기", Body: "어김이 반복됨", MemoryIDs: []string{m.ID},
		Payload: Payload{Title: "분명한 교훈", Body: "새 본문", Tags: []string{"가"}, Scope: "project:abc", Project: "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body, "+제목: 분명한 교훈") {
		t.Fatalf("diff missing: %q", p.Body)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(m.ID)
	if got.Title != "분명한 교훈" || got.Body != "새 본문" || got.Scope != "project:abc" || got.Project != "abc" || len(got.Tags) != 1 {
		t.Fatalf("got %+v", got)
	}
	// 고쳐서 승인하면 사용자가 준 제목을 쓴다.
	p2, _ := s.Propose(ProposalInput{Kind: ProposalEdit, Title: "다시", MemoryIDs: []string{m.ID}, Payload: Payload{Title: "회고가 낸 제목"}})
	title := "사용자 제목"
	if _, err := s.Decide(DecideInput{ID: p2.ID, Decision: "approve", Title: &title}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(m.ID); got.Title != "사용자 제목" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestLedger_승인하면_한_줄이_남고_대상과_변경이_보인다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "가 교훈")
	p, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "가 확정", Body: "회고 근거", MemoryIDs: []string{a.ID}, RetroID: "r-9"})
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if res.LedgerID == "" {
		t.Fatal("no ledger id")
	}
	rows, _ := s.Ledger(LedgerFilter{})
	if len(rows) != 1 {
		t.Fatalf("ledger = %+v", rows)
	}
	e := rows[0]
	if e.ProposalID != p.ID || e.TargetKind != TargetMemory || len(e.Targets) != 1 || e.Targets[0] != a.ID || e.Verdict != VerdictPending ||
		!strings.Contains(e.Change, "+상태: confirmed") || !strings.Contains(e.Basis, "회고 근거") || !strings.Contains(e.Basis, "r-9") {
		t.Fatalf("entry = %+v", e)
	}
	// 거절은 원장에 남지 않는다.
	b := candidate(t, s, "나 교훈")
	pb, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "나 확정", MemoryIDs: []string{b.ID}})
	s.Decide(DecideInput{ID: pb.ID, Decision: "reject"})
	if rows, _ := s.Ledger(LedgerFilter{}); len(rows) != 1 {
		t.Fatalf("rejection recorded: %d", len(rows))
	}
}

func TestLedger_파일_반영은_하네스와_지침을_가른다(t *testing.T) {
	s, _ := newTestStore(t)
	dir := t.TempDir()
	skill := filepath.Join(dir, "source", "skills", "tdd", "SKILL.md")
	os.MkdirAll(filepath.Dir(skill), 0o755)
	os.WriteFile(skill, []byte("옛\n"), 0o644)
	guide := filepath.Join(dir, "AGENTS.md")
	os.WriteFile(guide, []byte("옛\n"), 0o644)
	rule := candidate(t, s, "규칙")
	for _, c := range []struct{ path, kind string }{{skill, TargetHarness}, {guide, TargetInstruction}} {
		p, err := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "반영", MemoryIDs: []string{rule.ID}, Payload: Payload{Path: c.path, Content: "새\n"}})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
		if err != nil {
			t.Fatal(err)
		}
		e, _ := s.LedgerEntry(res.LedgerID)
		if e.TargetKind != c.kind || e.Targets[0] != c.path || len(e.Observe) != 1 || e.Observe[0] != rule.ID || !strings.Contains(e.Change, "+새") {
			t.Fatalf("%s: %+v", c.path, e)
		}
	}
}

// signal records a correction or usage judgment of id at time at.
func signal(t *testing.T, s *Store, kind, session, id, verdict string, at time.Time) {
	t.Helper()
	data := map[string]any{"memoryId": id, "occurredAt": at.UnixMilli()}
	if kind == EventMemoryUsed {
		data["verdict"] = verdict
	} else {
		data["injected"] = "yes"
	}
	if err := s.RecordEvent(Event{Kind: kind, At: at.UnixMilli(), Day: Day(at), Session: session}, data); err != nil {
		t.Fatal(err)
	}
}

func injectAt(t *testing.T, s *Store, session string, at time.Time, ids ...string) {
	t.Helper()
	if err := s.RecordEvent(Event{Kind: EventInjectSessionStart, At: at.UnixMilli(), Day: Day(at), Session: session}, map[string]any{"memoryIds": ids}); err != nil {
		t.Fatal(err)
	}
}

func approveEdit(t *testing.T, s *Store, id, title string) string {
	t.Helper()
	p, err := s.Propose(ProposalInput{Kind: ProposalEdit, Title: "고치기 " + title, MemoryIDs: []string{id}, Payload: Payload{Title: title}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	return res.LedgerID
}

func TestLedger_관찰_기간이_지나면_판정한다(t *testing.T) {
	const window = 7
	day := 24 * time.Hour
	cases := []struct {
		name     string
		before   int // 변경 전 나쁜 신호
		after    int
		verdict  string
		evidence bool
	}{
		{"줄면 도움됨", 3, 1, VerdictHelped, true},
		{"늘면 해로움", 1, 2, VerdictHarmful, true},
		{"같으면 효과 없음", 2, 2, VerdictNoEffect, true},
		{"신호가 없으면 증거 없음", 0, 0, VerdictNoEffect, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, clk := newTestStore(t)
			m := candidate(t, s, "대상 규칙")
			start := clk.t
			// 변경 전·후 각각 세 세션에 넣고, 앞 세션들에 나쁜 신호를 붙인다.
			for i := 0; i < 3; i++ {
				at := start.Add(-time.Duration(i+1) * day)
				sess := "s-b" + string(rune('a'+i))
				injectAt(t, s, sess, at, m.ID)
				if i < c.before {
					signal(t, s, EventCorrection, sess, m.ID, "", at.Add(time.Minute))
				}
			}
			id := approveEdit(t, s, m.ID, "고친 규칙")
			for i := 0; i < 3; i++ {
				at := start.Add(time.Duration(i+1) * day)
				sess := "s-a" + string(rune('a'+i))
				injectAt(t, s, sess, at, m.ID)
				if i < c.after {
					signal(t, s, EventMemoryUsed, sess, m.ID, "violated", at.Add(time.Minute))
				}
			}
			clk.t = start.Add(6 * day)
			if n, _ := s.JudgeLedger(window); n != 0 {
				t.Fatal("judged before the window ended")
			}
			clk.t = start.Add(window*day + time.Minute)
			if n, err := s.JudgeLedger(window); err != nil || n != 1 {
				t.Fatalf("judged %d %v", n, err)
			}
			e, _ := s.LedgerEntry(id)
			if e.Verdict != c.verdict || e.Evidence != c.evidence || e.JudgedAt == 0 {
				t.Fatalf("entry = %+v", e)
			}
			if e.Observation.Before.Bad != c.before || e.Observation.After.Bad != c.after {
				t.Fatalf("observation = %+v", e.Observation)
			}
		})
	}
}

func TestLedger_같은_대상을_같은_기간에_건드리면_섞임(t *testing.T) {
	s, clk := newTestStore(t)
	m := candidate(t, s, "대상")
	other := candidate(t, s, "다른 대상")
	start := clk.t
	first := approveEdit(t, s, m.ID, "첫 고침")
	clk.t = start.Add(48 * time.Hour)
	second := approveEdit(t, s, m.ID, "둘째 고침")
	alone := approveEdit(t, s, other.ID, "따로 고침")
	clk.t = start.Add(10 * 24 * time.Hour)
	if _, err := s.JudgeLedger(7); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first, second} {
		if e, _ := s.LedgerEntry(id); e.Verdict != VerdictMixed {
			t.Fatalf("%s = %+v", id, e)
		}
	}
	if e, _ := s.LedgerEntry(alone); e.Verdict == VerdictMixed || e.Verdict == VerdictPending {
		t.Fatalf("alone = %+v", e)
	}
}

func TestLedger_되돌리기는_기억과_파일을_바꾸기_전으로(t *testing.T) {
	s, _ := newTestStore(t)
	m := candidate(t, s, "원래 제목")
	id := approveEdit(t, s, m.ID, "바뀐 제목")
	p, err := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Body: "해로움", Payload: Payload{LedgerID: id}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body, "바뀐 제목") {
		t.Fatalf("revert body should show the change: %q", p.Body)
	}
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(m.ID); got.Title != "원래 제목" || got.State != StateCandidate {
		t.Fatalf("memory = %+v", got)
	}
	orig, _ := s.LedgerEntry(id)
	rev, _ := s.LedgerEntry(res.LedgerID)
	if orig.RevertedBy != rev.ID || rev.RevertOf != id {
		t.Fatalf("links: %+v / %+v", orig, rev)
	}
	if _, err := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "또", Payload: Payload{LedgerID: id}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reverting twice: %v", err)
	}

	// 파일: 바꾸기 전 내용으로, 그 뒤 바뀌었으면 멈춘다.
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	os.WriteFile(path, []byte("옛\n"), 0o644)
	pf, _ := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "반영", Payload: Payload{Path: path, Content: "새\n"}})
	rf, _ := s.Decide(DecideInput{ID: pf.ID, Decision: "approve"})
	pr, _ := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "파일 되돌리기", Payload: Payload{LedgerID: rf.LedgerID}})
	os.WriteFile(path, []byte("사람이 고침\n"), 0o644)
	if _, err := s.Decide(DecideInput{ID: pr.ID, Decision: "approve"}); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("changed file: %v", err)
	}
	os.WriteFile(path, []byte("새\n"), 0o644)
	if _, err := s.Decide(DecideInput{ID: pr.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "옛\n" {
		t.Fatalf("file = %q", b)
	}

	// 합치기 되돌리기: 새 기억은 낡음, 대체된 기억은 되살린다.
	x, y := candidate(t, s, "엑스"), candidate(t, s, "와이")
	pm, _ := s.Propose(ProposalInput{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{x.ID, y.ID}, Payload: Payload{MergedTitle: "엑스와이"}})
	rm, _ := s.Decide(DecideInput{ID: pm.ID, Decision: "approve"})
	prm, _ := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "합치기 되돌리기", Payload: Payload{LedgerID: rm.LedgerID}})
	if _, err := s.Decide(DecideInput{ID: prm.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(rm.Created); got.State != StateStale {
		t.Fatalf("merged = %+v", got)
	}
	for _, id := range []string{x.ID, y.ID} {
		if got, _ := s.Get(id); got.State != StateCandidate || got.SupersededBy != "" {
			t.Fatalf("restored = %+v", got)
		}
	}
}

func TestLedger_교훈은_증거_있는_판정에만_잇는다(t *testing.T) {
	s, clk := newTestStore(t)
	m := candidate(t, s, "대상")
	start := clk.t
	injectAt(t, s, "s1", start.Add(-time.Hour), m.ID)
	signal(t, s, EventCorrection, "s1", m.ID, "", start.Add(-time.Minute))
	id := approveEdit(t, s, m.ID, "고침")
	empty := approveEdit(t, s, candidate(t, s, "조용한 것").ID, "고침2")
	clk.t = start.Add(8 * 24 * time.Hour)
	s.JudgeLedger(7)
	lesson := candidate(t, s, "교훈")
	if err := s.SetLedgerLesson(empty, lesson.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lesson without evidence: %v", err)
	}
	if err := s.SetLedgerLesson(id, lesson.ID); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.LedgerEntry(id); e.LessonID != lesson.ID {
		t.Fatalf("entry = %+v", e)
	}
	if err := s.SetLedgerLesson(id, lesson.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second lesson: %v", err)
	}
}

func TestSessionBundle_처음_만든_묶음을_그대로_돌려준다(t *testing.T) {
	s, _ := newTestStore(t)
	if _, _, ok := s.SessionBundle("claude", "s1"); ok {
		t.Fatal("bundle before saving")
	}
	s.SaveSessionBundle("claude", "s1", "첫 묶음", []string{"m-1"})
	s.SaveSessionBundle("claude", "s1", "둘째 묶음", []string{"m-2"})
	text, ids, ok := s.SessionBundle("claude", "s1")
	if !ok || text != "첫 묶음" || len(ids) != 1 || ids[0] != "m-1" {
		t.Fatalf("bundle = %q %v %v", text, ids, ok)
	}
	if _, _, ok := s.SessionBundle("codex", "s1"); ok {
		t.Fatal("host must be part of the key")
	}
}

func TestUsage_기간_안의_사용_판정을_기억마다_센다(t *testing.T) {
	s, clk := newTestStore(t)
	signal(t, s, EventMemoryUsed, "s1", "m-a", "followed", clk.t)
	signal(t, s, EventMemoryUsed, "s2", "m-a", "irrelevant", clk.t)
	signal(t, s, EventMemoryUsed, "s3", "m-a", "irrelevant", clk.t)
	signal(t, s, EventMemoryUsed, "s3", "m-b", "violated", clk.t)
	signal(t, s, EventMemoryUsed, "s3", "m-c", "followed", clk.t.AddDate(0, 0, -30))
	u, err := s.UsageCounts(Day(clk.t.AddDate(0, 0, -7)), Day(clk.t))
	if err != nil {
		t.Fatal(err)
	}
	if u["m-a"] != (Usage{Followed: 1, Irrelevant: 2}) || u["m-b"] != (Usage{Violated: 1}) || u["m-c"] != (Usage{}) {
		t.Fatalf("usage = %+v", u)
	}
}

func TestLastUse_주입_읽기_찾기_사용판정을_마지막_사용으로_본다(t *testing.T) {
	s, clk := newTestStore(t)
	old := clk.t.AddDate(0, 0, -40)
	injectAt(t, s, "s0", old, "m-old")
	injectAt(t, s, "s1", clk.t.AddDate(0, 0, -2), "m-inj")
	s.RecordEvent(Event{Kind: EventToolPrefix + "memory_get"}, map[string]any{"ok": true, "ids": []string{"m-get"}})
	s.RecordEvent(Event{Kind: EventToolPrefix + "memory_find"}, map[string]any{"ok": true, "ids": []string{"m-find"}})
	signal(t, s, EventMemoryUsed, "s2", "m-used", "followed", clk.t.AddDate(0, 0, -1))
	last, earliest, err := s.LastUse()
	if err != nil {
		t.Fatal(err)
	}
	if earliest != old.UnixMilli() {
		t.Fatalf("earliest = %d", earliest)
	}
	for _, id := range []string{"m-old", "m-inj", "m-get", "m-find", "m-used"} {
		if last[id] == 0 {
			t.Fatalf("%s missing: %v", id, last)
		}
	}
	if last["m-old"] != old.UnixMilli() {
		t.Fatalf("old = %d", last["m-old"])
	}
}

func TestLedger_되돌리기는_그_뒤에_바뀐_기억을_덮지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	m := candidate(t, s, "A")
	first := approveEdit(t, s, m.ID, "B")
	approveEdit(t, s, m.ID, "C")
	p, err := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "첫 고침 되돌리기", Payload: Payload{LedgerID: first}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); !errors.Is(err, ErrMemoryChanged) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := s.Get(m.ID); got.Title != "C" {
		t.Fatalf("later edit overwritten: %+v", got)
	}
	// 사용자가 직접 고친 것도 지키지 않는다.
	n := candidate(t, s, "가")
	id := approveEdit(t, s, n.ID, "나")
	body := "사용자가 고친 본문"
	s.Update(UpdateInput{ID: n.ID, Body: &body})
	p2, _ := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: id}})
	if _, err := s.Decide(DecideInput{ID: p2.ID, Decision: "approve"}); !errors.Is(err, ErrMemoryChanged) {
		t.Fatalf("hand edit: %v", err)
	}
}

func TestLedger_파일과_기억을_함께_되돌리다_멈춰도_다시_승인하면_끝난다(t *testing.T) {
	s, _ := newTestStore(t)
	m := candidate(t, s, "규칙")
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	os.WriteFile(path, []byte("옛\n"), 0o644)
	p, _ := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "반영", MemoryIDs: []string{m.ID}, Payload: Payload{Path: path, Content: "새\n"}})
	res, _ := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	pr, _ := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: res.LedgerID}})
	// 파일만 먼저 옛 내용이 된 상태(앞선 승인이 중간에 멈춤)에서 다시 승인한다.
	os.WriteFile(path, []byte("옛\n"), 0o644)
	if _, err := s.Decide(DecideInput{ID: pr.ID, Decision: "approve"}); err != nil {
		t.Fatalf("retry after partial revert: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "옛\n" {
		t.Fatalf("file = %q", b)
	}
}

func TestLedger_새로_만든_기억은_변경_전이_없어_증거_없음(t *testing.T) {
	s, clk := newTestStore(t)
	start := clk.t
	p, _ := s.Propose(ProposalInput{Kind: ProposalDirection, Title: "새 방향", Body: "이유"})
	res, _ := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i+1) * 24 * time.Hour)
		sess := "s" + string(rune('a'+i))
		injectAt(t, s, sess, at, res.Created)
		if i == 0 {
			signal(t, s, EventMemoryUsed, sess, res.Created, "violated", at.Add(time.Minute))
		}
	}
	clk.t = start.Add(8 * 24 * time.Hour)
	s.JudgeLedger(7)
	if e, _ := s.LedgerEntry(res.LedgerID); e.Verdict != VerdictNoEffect || e.Evidence {
		t.Fatalf("new memory judged without a before period: %+v", e)
	}
}

func TestOpen_옛_DB를_여러_프로세스가_동시에_처음_열어도_된다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, _ := sql.Open("sqlite", "file:"+path)
	db.Exec(`CREATE TABLE retros (id TEXT PRIMARY KEY, kind TEXT NOT NULL, period TEXT NOT NULL, status TEXT NOT NULL, host TEXT NOT NULL DEFAULT '',
		started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0, summary TEXT NOT NULL DEFAULT '', flags TEXT NOT NULL DEFAULT '[]',
		journal_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '')`)
	db.Close()
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			s, err := Open(path)
			if err == nil {
				s.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestProposal_고쳐서_승인할_때_빈_본문은_그대로_둔다(t *testing.T) {
	s, _ := newTestStore(t)
	m, _ := s.Save(SaveInput{Kind: KindLesson, Title: "제목", Body: "원래 본문", Source: Source{Kind: SourceSession}})
	p, _ := s.Propose(ProposalInput{Kind: ProposalEdit, Title: "고치기", MemoryIDs: []string{m.ID}, Payload: Payload{Title: "회고 제목"}})
	title, empty := "사용자 제목", ""
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Title: &title, Body: &empty}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(m.ID); got.Title != "사용자 제목" || got.Body != "원래 본문" {
		t.Fatalf("got %+v", got)
	}
}

func TestPruneSessionState_오래된_묶음과_처리한_대기열을_지운다(t *testing.T) {
	s, clk := newTestStore(t)
	s.SaveSessionBundle("claude", "old", "옛 묶음", nil)
	s.DB().Exec(`INSERT INTO session_ends(host,session,ended_at,retro_id) VALUES('claude','old-done',?,'r-1'),('claude','old-wait',?,'')`,
		clk.t.UnixMilli(), clk.t.UnixMilli())
	clk.t = clk.t.AddDate(0, 0, 31)
	s.SaveSessionBundle("claude", "new", "새 묶음", nil)
	if err := s.PruneSessionState(30); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.SessionBundle("claude", "old"); ok {
		t.Fatal("old bundle kept")
	}
	if _, _, ok := s.SessionBundle("claude", "new"); !ok {
		t.Fatal("new bundle removed")
	}
	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM session_ends`).Scan(&n)
	if n != 1 {
		t.Fatalf("session_ends = %d (waiting one must stay)", n)
	}
}

func TestLedger_같은_세션의_교정과_어김은_나쁜_신호_한_번이다(t *testing.T) {
	s, clk := newTestStore(t)
	m := candidate(t, s, "대상")
	start := clk.t
	before := start.Add(-24 * time.Hour)
	injectAt(t, s, "s-b", before, m.ID)
	signal(t, s, EventCorrection, "s-b", m.ID, "", before.Add(time.Minute))
	id := approveEdit(t, s, m.ID, "고침")
	after := start.Add(24 * time.Hour)
	injectAt(t, s, "s-a", after, m.ID)
	signal(t, s, EventCorrection, "s-a", m.ID, "", after.Add(time.Minute))
	signal(t, s, EventMemoryUsed, "s-a", m.ID, "violated", after.Add(2*time.Minute))
	clk.t = start.Add(8 * 24 * time.Hour)
	s.JudgeLedger(7)
	e, _ := s.LedgerEntry(id)
	if e.Observation.Before.Bad != 1 || e.Observation.After.Bad != 1 || e.Verdict != VerdictNoEffect {
		t.Fatalf("entry = %+v", e)
	}
}
