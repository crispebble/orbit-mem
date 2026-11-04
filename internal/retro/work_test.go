package retro

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
	"github.com/onidot-labs/orbit-mem/internal/project"
)

// AU-5(ONID-130) 작업 회고: OpenOrbit이 끝난 작업의 기록을 넘기면 그 기록 하나를 회고한다.

var workEnd = time.Date(2026, 10, 27, 15, 0, 0, 0, kst)

func workIn(t *testing.T, ref string, endedAt time.Time, text string) WorkIn {
	t.Helper()
	return WorkIn{Ref: ref, Cwd: t.TempDir(), Title: "로그인 오류 고치기", Status: "completed", EndedAt: endedAt.UnixMilli(), Text: text, Verified: true}
}

func (e *env) prepWork(t *testing.T, in WorkIn) Prepared {
	t.Helper()
	p, err := e.r.PrepareWork("claude", in)
	if err != nil {
		t.Fatalf("prepare work: %v", err)
	}
	return p
}

// doneWork prepares, submits and finishes one work retrospective; it returns the report.
func (e *env) doneWork(t *testing.T, in WorkIn, sub Submission) (Prepared, string) {
	t.Helper()
	p := e.prepWork(t, in)
	if p.Status != StatusRunning {
		t.Fatalf("prepare = %+v", p)
	}
	if sub.Summary == "" {
		sub.Summary = "작업 회고"
	}
	report, err := Submit(e.s, e.r.Paths, p.ID, sub)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if f, err := e.r.Finish(p.ID, HostResult{}); err != nil || f.Status != StatusDone {
		t.Fatalf("finish = %+v %v", f, err)
	}
	return p, report
}

func workRows(t *testing.T, e *env) int {
	t.Helper()
	var n int
	e.s.DB().QueryRow(`SELECT COUNT(*) FROM retros WHERE kind=?`, KindWork).Scan(&n)
	return n
}

func sourcesOf(m memory.Memory, kind string) []memory.Source {
	var out []memory.Source
	for _, s := range m.Sources {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func refsOf(srcs []memory.Source) []string {
	var out []string
	for _, s := range srcs {
		if !slices.Contains(out, s.Ref) {
			out = append(out, s.Ref)
		}
	}
	slices.Sort(out)
	return out
}

func findTitle(t *testing.T, e *env, title string) []memory.Memory {
	t.Helper()
	all, err := e.s.List()
	if err != nil {
		t.Fatal(err)
	}
	var out []memory.Memory
	for _, m := range all {
		if m.Title == title {
			full, _ := e.s.Get(m.ID)
			out = append(out, full)
		}
	}
	return out
}

func TestWork_준비는_실행_빈_건너뜀을_돌려주고_같은_작업_끝은_한_번만_돈다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-1", workEnd, "로그인 오류를 고치고 시험을 돌렸다")
	p := e.prepWork(t, in)
	want := `{"status":"running","id":"` + p.ID + `","kind":"work","period":"2026-10-27","session":"openorbit:w-1","sourceChecks":0}`
	if got := marshal(t, p); got != want {
		t.Fatalf("running\n got %s\nwant %s", got, want)
	}
	if _, err := os.Stat(e.r.Paths.RetroDir() + "/" + p.ID + "/material.json"); err != nil {
		t.Fatalf("material: %v", err)
	}
	st, _ := getStatus(e.s, p.ID)
	if st.Kind != KindWork || st.Status != StatusRunning || st.Host != "claude" || st.Period != "2026-10-27" {
		t.Fatalf("row = %+v", st)
	}
	// 실행 중인 같은 작업 끝은 건너뛴다(새 줄 없음).
	skipped := e.prepWork(t, in)
	if got := marshal(t, skipped); got != `{"status":"skipped","kind":"work","period":"2026-10-27"}` {
		t.Fatalf("skipped while running = %s", got)
	}
	if n := workRows(t, e); n != 1 {
		t.Fatalf("rows = %d", n)
	}
	if _, err := Submit(e.s, e.r.Paths, p.ID, Submission{Summary: "로그인"}); err != nil {
		t.Fatal(err)
	}
	e.r.Finish(p.ID, HostResult{})
	if again := e.prepWork(t, in); again.Status != StatusSkipped || workRows(t, e) != 1 {
		t.Fatalf("skipped after done = %+v rows %d", again, workRows(t, e))
	}
	// 작업 회고는 대화 기록이 아니라 읽은 구간을 남기지 않는다.
	var cov int
	e.s.DB().QueryRow(`SELECT COUNT(*) FROM retro_coverage`).Scan(&cov)
	if cov != 0 {
		t.Fatalf("retro_coverage rows = %d", cov)
	}
	// 다시 연 작업이 다시 끝나면(끝난 시각이 다르면) 새 회고다.
	reopened := in
	reopened.EndedAt = workEnd.Add(26 * time.Hour).UnixMilli()
	if r := e.prepWork(t, reopened); r.Status != StatusRunning || r.Period != "2026-10-28" || r.ID == p.ID {
		t.Fatalf("reopened = %+v", r)
	}
	// 기록 글이 비면 빈 결과다.
	empty := e.prepWork(t, workIn(t, "w-empty", workEnd, "  \n "))
	if got := marshal(t, empty); got != `{"status":"empty","id":"`+empty.ID+`","kind":"work","period":"2026-10-27"}` {
		t.Fatalf("empty = %s", got)
	}
	// 실패한 회고는 다시 돈다.
	failIn := workIn(t, "w-fail", workEnd, "실패할 회고")
	f := e.prepWork(t, failIn)
	if res, _ := e.r.Finish(f.ID, HostResult{ExecError: "exit 1"}); res.Status != StatusFailed {
		t.Fatalf("finish = %+v", res)
	}
	if rerun := e.prepWork(t, failIn); rerun.Status != StatusRunning || rerun.ID == f.ID {
		t.Fatalf("rerun after failed = %+v", rerun)
	}
}

func TestWork_준비_입력이_틀리면_오류이고_줄을_남기지_않는다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	ok := workIn(t, "w-1", workEnd, "기록")
	for name, mut := range map[string]func(*WorkIn){
		"ref 없음":     func(w *WorkIn) { w.Ref = " " },
		"cwd 상대경로":   func(w *WorkIn) { w.Cwd = "app" },
		"cwd 없음":     func(w *WorkIn) { w.Cwd = "" },
		"endedAt 0":  func(w *WorkIn) { w.EndedAt = 0 },
		"endedAt 음수": func(w *WorkIn) { w.EndedAt = -5 },
		"status 모름":  func(w *WorkIn) { w.Status = "done" },
		"status 비었음": func(w *WorkIn) { w.Status = "" },
	} {
		in := ok
		mut(&in)
		if err := in.Validate(); !errors.Is(err, ErrWorkInput) {
			t.Errorf("%s: validate = %v", name, err)
		}
		if _, err := e.r.PrepareWork("claude", in); !errors.Is(err, ErrWorkInput) {
			t.Errorf("%s: prepare = %v", name, err)
		}
	}
	for _, st := range []string{"completed", "failed", "stopped", "cancelled"} {
		in := ok
		in.Status = st
		if err := in.Validate(); err != nil {
			t.Errorf("%s: %v", st, err)
		}
	}
	if n := workRows(t, e); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}

func TestWork_같은_작업_끝을_동시에_준비해도_하나만_돈다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-race", workEnd, "동시에 끝난 작업")
	const n = 8
	var wg sync.WaitGroup
	results := make([]Prepared, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = e.r.PrepareWork("claude", in)
		}(i)
	}
	wg.Wait()
	running := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("prepare %d: %v", i, errs[i])
		}
		switch results[i].Status {
		case StatusRunning:
			running++
		case StatusSkipped:
		default:
			t.Fatalf("prepare %d = %+v", i, results[i])
		}
	}
	if running != 1 || workRows(t, e) != 1 {
		t.Fatalf("running = %d rows = %d", running, workRows(t, e))
	}
	dirs, _ := os.ReadDir(e.r.Paths.RetroDir())
	if len(dirs) != 1 {
		t.Fatalf("material dirs = %d (건너뛴 준비의 재료가 남음)", len(dirs))
	}
}

func TestWork_재료는_작업_머리_증거_사용자_말_붙인_기억_프로젝트_기억을_싣는다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-mat", workEnd, "로그인 오류를 고쳤다. 키 sk-ant-abcdefghijklmnopqrstuvwxyz0123 는 가린다")
	in.Title = "로그인 오류 고치기"
	in.Evidence = []WorkEvidenceIn{{Ref: "step s1", Text: "npm test → ok: Tests 5 passed"}}
	in.UserWords = []WorkWordIn{
		{Ref: "d-1", Text: "세션 만료도 같이 봐 줘", At: workEnd.Add(-time.Hour).UnixMilli()},
		{Ref: "d-2", Text: "아니 그 파일 말고 auth.ts를 고쳐", Correction: true},
	}
	pr := project.Resolve(in.Cwd)
	mine, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "이 프로젝트 시험은 npm test", Body: "본문 이 프로젝트", Scope: pr.Scope, Project: pr.Label, Source: memory.Source{Kind: memory.SourceSession}})
	other, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "다른 프로젝트 사실", Scope: "project:/elsewhere", Source: memory.Source{Kind: memory.SourceSession}})
	lesson, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "전역 교훈 하나", Source: memory.Source{Kind: memory.SourceSession}})
	gfact, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "전역 사실 하나", Source: memory.Source{Kind: memory.SourceSession}})
	recalled, _ := e.s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "붙인 기억 본문", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "답은 존댓말"}})
	in.Recalled = []string{recalled.ID, "m-none"}
	e.s.RecordJudgment(memory.Judgment{Kind: memory.KindFact, Title: "그날만 맞는 경로", Verdict: memory.VerdictReject, Reason: "일회성"})
	p := e.prepWork(t, in)
	text := readAll(t, e, p.ID)
	for _, want := range []string{"# 작업 회고 재료", "w-mat", "로그인 오류 고치기", "completed", in.Cwd, "검증 통과",
		"## 검증 증거", "step s1: npm test → ok: Tests 5 passed",
		"## 사용자 말", "세션 만료도 같이 봐 줘", "(교정) 아니 그 파일 말고 auth.ts를 고쳐",
		"## 이 작업에 붙인 기억", recalled.ID, "붙인 기억 본문",
		"## 지금 있는 기억(이 프로젝트)", mine.ID, "본문 이 프로젝트", lesson.ID,
		"## 최근 거절", "일회성", "## 작업 기록", "로그인 오류를 고쳤다", "[가림]"} {
		if !strings.Contains(text, want) {
			t.Errorf("material missing %q", want)
		}
	}
	for _, not := range []string{other.ID, gfact.ID, "m-none", "abcdefghijklmnopqrstuvwxyz0123", "## 대화"} {
		if strings.Contains(text, not) {
			t.Errorf("material has %q", not)
		}
	}
	if t.Failed() {
		t.Log(text)
	}
}

func TestWork_제출은_작업_출처로_후보를_남기고_증거와_인용으로만_확정한다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-sub", workEnd, "로그인 오류를 고치고 시험을 돌렸다")
	in.Evidence = []WorkEvidenceIn{{Ref: "step s1", Text: "npm test → ok: Tests 5 passed"}}
	in.UserWords = []WorkWordIn{{Ref: "d-1", Text: "금요일에는 배포하지 않는다, 앞으로 기억해"}}
	pr := project.Resolve(in.Cwd)
	e.s.Now = func() time.Time { return workEnd.AddDate(0, 0, -3) }
	protected, _ := e.s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 한글 존댓말", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "답은 한글 존댓말로 해"}})
	anchor, _ := e.s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: "인증은 세션 쿠키로 한다", Scope: pr.Scope, Project: pr.Label, Source: memory.Source{Kind: memory.SourceSession}})
	e.s.Now = func() time.Time { return workEnd.Add(time.Hour) }
	_, report := e.doneWork(t, in, Submission{
		Summary: "로그인 오류",
		Journal: &JournalIn{Title: "로그인 오류 고침", Body: "한 일"},
		Memories: []MemoryIn{
			{Kind: "lesson", Title: "로그인 오류는 세션 만료부터 본다", Scope: "project", Tags: []string{"절차", "auth"},
				Links: []LinkIn{{ID: anchor.ID, Relation: "basis", Note: "세션 쿠키라서"}, {ID: "m-none", Relation: "basis"}, {ID: anchor.ID, Relation: "near"},
					{ID: protected.ID, Relation: "exception", Note: "사용자 말 기억에 잇기"}}},
			{Kind: "fact", Title: "이 프로젝트 시험 명령은 npm test", Scope: "project", Evidence: "Tests 5 passed"},
			{Kind: "fact", Title: "시험은 9개다", Scope: "project", Evidence: "Tests 9 passed"},
			{Kind: "decision", Title: "금요일에는 배포하지 않는다", Quote: "금요일에는 배포하지 않는다, 앞으로 기억해"},
			{Kind: "decision", Title: "월요일 아침에는 배포하지 않는다", Quote: "월요일 아침에는 배포하지 않는다고 했잖아"},
		},
		Proposals: []ProposalIn{
			{Kind: "stale", Title: "말투 규칙 정리", MemoryIDs: []string{protected.ID}, Body: "안 쓰는 듯"},
			{Kind: "edit", Title: "말투 규칙 고침", MemoryIDs: []string{protected.ID}, NewTitle: "답은 존댓말"},
			{Kind: "reclassify", Title: "재분류", MemoryIDs: []string{anchor.ID}, NewKind: "fact"},
		},
	})
	lesson := findTitle(t, e, "로그인 오류는 세션 만료부터 본다")
	if len(lesson) != 1 || lesson[0].State != memory.StateCandidate || lesson[0].Scope != pr.Scope || lesson[0].Project != pr.Label {
		t.Fatalf("lesson = %+v (%s)", lesson, report)
	}
	if w := sourcesOf(lesson[0], memory.SourceWork); len(w) != 1 || w[0].Ref != "w-sub" {
		t.Fatalf("lesson sources = %+v", lesson[0].Sources)
	}
	if !slices.Contains(lesson[0].Tags, "procedure") || slices.Contains(lesson[0].Tags, "절차") || !slices.Contains(lesson[0].Tags, "auth") {
		t.Fatalf("lesson tags = %v", lesson[0].Tags)
	}
	// 연결은 자동 변경으로 적용되고 원장에 남는다.
	links := autoOf(t, e, memory.ProposalLink)
	if len(links) != 1 || !slices.Contains(links[0].MemoryIDs, lesson[0].ID) || !slices.Contains(links[0].MemoryIDs, anchor.ID) ||
		links[0].Payload.Note != "같은 결정의 근거: 세션 쿠키라서" || links[0].Payload.Actor != memory.ActorAutomation {
		t.Fatalf("link proposals = %+v", links)
	}
	if got, _ := e.s.Get(lesson[0].ID); len(got.Links) != 1 || got.Links[0].MemoryID != anchor.ID {
		t.Fatalf("live links = %+v", got.Links)
	}
	ledger, _ := e.s.Ledger(memory.LedgerFilter{})
	linked := false
	for _, l := range ledger {
		linked = linked || (l.ProposalKind == memory.ProposalLink && l.ProposalID == links[0].ID)
	}
	if !linked {
		t.Fatalf("no ledger line for the link: %+v", ledger)
	}
	if !strings.Contains(report, "연결 1건") {
		t.Fatalf("report must count the applied link: %s", report)
	}
	if !strings.Contains(report, "m-none") || !strings.Contains(report, "near") {
		t.Fatalf("bad links must be noted: %s", report)
	}
	// 검증 증거에 그대로 있는 프로젝트 사실은 증거 ③으로 확정된다.
	fact := findTitle(t, e, "이 프로젝트 시험 명령은 npm test")
	if len(fact) != 1 || fact[0].State != memory.StateConfirmed || fact[0].Scope != pr.Scope {
		t.Fatalf("verified fact = %+v (%s)", fact, report)
	}
	if v := sourcesOf(fact[0], memory.SourceVerification); len(v) != 1 || v[0].Ref != "w-sub" || v[0].Excerpt != "Tests 5 passed" {
		t.Fatalf("verification source = %+v", fact[0].Sources)
	}
	// 증거가 재료에 없으면 후보로 남고 보고에 적는다.
	unverified := findTitle(t, e, "시험은 9개다")
	if len(unverified) != 1 || unverified[0].State != memory.StateCandidate || len(sourcesOf(unverified[0], memory.SourceVerification)) != 0 {
		t.Fatalf("unverified fact = %+v", unverified)
	}
	if !strings.Contains(report, "Tests 9 passed") {
		t.Fatalf("report must note the missing evidence: %s", report)
	}
	// 사용자 말에 있는 인용은 바로 확정(출처 quote, ref = 작업 id). 없는 인용은 후보·작업 출처.
	quoted := findTitle(t, e, "금요일에는 배포하지 않는다")
	if len(quoted) != 1 || quoted[0].State != memory.StateConfirmed {
		t.Fatalf("quoted decision = %+v", quoted)
	}
	if q := sourcesOf(quoted[0], memory.SourceQuote); len(q) != 1 || q[0].Ref != "w-sub" {
		t.Fatalf("quote source = %+v", quoted[0].Sources)
	}
	if refs := refsOf(sourcesOf(quoted[0], memory.SourceWork)); !slices.Equal(refs, []string{"w-sub"}) {
		t.Fatalf("quoted decision must also keep the work: %+v", quoted[0].Sources)
	}
	notQuoted := findTitle(t, e, "월요일 아침에는 배포하지 않는다")
	if len(notQuoted) != 1 || notQuoted[0].State != memory.StateCandidate || len(sourcesOf(notQuoted[0], memory.SourceQuote)) != 0 ||
		len(sourcesOf(notQuoted[0], memory.SourceWork)) != 1 || notQuoted[0].Scope != memory.ScopeGlobal {
		t.Fatalf("unquoted decision = %+v", notQuoted)
	}
	// 일지: 그 작업 프로젝트 범위, 확정, 작업 출처.
	row, _ := Recent(e.s, 1)
	journal, err := e.s.Get(row[0].JournalID)
	if err != nil || journal.Kind != memory.KindJournal || journal.Title != "2026-10-27 작업: 로그인 오류 고침" || journal.Scope != pr.Scope ||
		journal.State != memory.StateConfirmed || len(sourcesOf(journal, memory.SourceWork)) != 1 || journal.Sources[0].Ref != "w-sub" {
		t.Fatalf("journal = %+v %v", journal, err)
	}
	// 사용자 인용 기억에 닿는 정리와 고치기는 적용하지 않고, 대기 제안 없이 까닭을 알린다(설계 · orbit-mem 2판 18.3).
	// 받지 않는 종류는 적고 뺀다.
	if got, _ := e.s.Get(protected.ID); got.State != memory.StateConfirmed || got.Version != protected.Version {
		t.Fatalf("protected memory changed: %+v", got)
	}
	if p := pendingOf(t, e, memory.ProposalStale); len(p) != 0 {
		t.Fatalf("pending stale = %+v", p)
	}
	if p := pendingOf(t, e, memory.ProposalEdit); len(p) != 0 {
		t.Fatalf("pending edit = %+v", p)
	}
	if !strings.Contains(report, "적용하지 않은 것 2건") {
		t.Fatalf("report must say what was not applied: %s", report)
	}
	if p := pendingOf(t, e, memory.ProposalReclassify); len(p) != 0 || !strings.Contains(report, "재분류") {
		t.Fatalf("reclassify must be skipped with a note: %+v (%s)", p, report)
	}
}

// K5: 서로 다른 작업이 같은 교훈을 내도(제목이 같거나 sameAs) 기억은 하나이고 작업마다 출처가 붙는다.
// 「절차」 교훈이 서로 다른 작업 셋에서 나오면 하네스 개선 후보가 된다.
func TestWork_K5_같은_교훈은_하나로_모이고_절차는_세_작업부터_하네스_후보다(t *testing.T) {
	e := setup(t, workEnd.AddDate(0, 0, 3))
	cwd := t.TempDir()
	mk := func(ref string, day int) WorkIn {
		in := workIn(t, ref, workEnd.AddDate(0, 0, day), "배포 전에 마이그레이션 dry-run을 돌렸다")
		in.Cwd = cwd
		return in
	}
	title := "배포 전에 마이그레이션 dry-run을 돌린다"
	in1 := mk("w-a", 0)
	e.doneWork(t, in1, Submission{Memories: []MemoryIn{{Kind: "lesson", Title: title, Tags: []string{"절차"}}}})
	first := findTitle(t, e, title)
	if len(first) != 1 {
		t.Fatalf("first = %+v", first)
	}
	if hc, _ := e.s.HarnessCandidates(); len(hc) != 0 {
		t.Fatalf("one work is not a candidate: %+v", hc)
	}
	e.doneWork(t, mk("w-b", 1), Submission{Memories: []MemoryIn{{Kind: "lesson", Title: title, SameAs: first[0].ID}}})
	if hc, _ := e.s.HarnessCandidates(); len(hc) != 0 {
		t.Fatalf("two works are not a candidate: %+v", hc)
	}
	e.doneWork(t, mk("w-c", 1), Submission{Memories: []MemoryIn{{Kind: "lesson", Title: title, Tags: []string{"절차"}}}})
	all := findTitle(t, e, title)
	if len(all) != 1 {
		t.Fatalf("memories = %d, want one", len(all))
	}
	m := all[0]
	if refs := refsOf(sourcesOf(m, memory.SourceWork)); !slices.Equal(refs, []string{"w-a", "w-b", "w-c"}) {
		t.Fatalf("work refs = %v", refs)
	}
	if len(m.Days) != 2 {
		t.Fatalf("sightings = %v", m.Days)
	}
	hc, err := e.s.HarnessCandidates()
	if err != nil || len(hc) != 1 {
		t.Fatalf("harness = %+v %v", hc, err)
	}
	if c := hc[0]; c.TargetKind != memory.TargetKindProcedure || c.Target != m.ID || c.Title != title || c.Sessions != 3 || !c.Over || !c.Harness ||
		c.Key != "correction:procedure:"+m.ID {
		t.Fatalf("candidate = %+v", c)
	}
	// 같은 작업 끝을 다시 준비하면 건너뛴다.
	if again := e.prepWork(t, in1); again.Status != StatusSkipped {
		t.Fatalf("again = %+v", again)
	}
}

func TestWork_붙인_기억의_사용_판정과_교정을_남긴다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	e.s.Policy.FollowedVerified = 1
	e.s.Now = func() time.Time { return workEnd.AddDate(0, 0, -3) }
	recalled, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "시험을 먼저 쓴다", Source: memory.Source{Kind: memory.SourceSession}})
	other, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "붙이지 않은 기억", Source: memory.Source{Kind: memory.SourceSession}})
	e.s.Now = func() time.Time { return workEnd.Add(time.Hour) }
	in := workIn(t, "w-use", workEnd, "시험을 먼저 쓰고 고쳤다")
	in.Recalled = []string{recalled.ID}
	in.UserWords = []WorkWordIn{{Ref: "d-1", Text: "시험부터 쓰라고 했잖아", At: workEnd.Add(-time.Hour).UnixMilli(), Correction: true}}
	_, report := e.doneWork(t, in, Submission{
		Usage: []UsageIn{
			{MemoryID: recalled.ID, Verdict: "followed", Verified: true, Note: "npm test ok"},
			{MemoryID: other.ID, Verdict: "followed", Verified: true},
		},
		Corrections: []CorrectionIn{{Quote: "시험부터 쓰라고 했잖아", Skill: "orbit:tdd"}},
	})
	evs, _ := e.s.Events(memory.EventFilter{FromDay: "2026-10-27", ToDay: "2026-10-27", KindPrefix: memory.EventMemoryUsed})
	if len(evs) != 1 || evs[0].Host != "openorbit" || evs[0].Session != "w-use" {
		t.Fatalf("usage events = %+v (%s)", evs, report)
	}
	var d struct {
		MemoryID string `json:"memoryId"`
		Verdict  string `json:"verdict"`
		Verified bool   `json:"verified"`
	}
	json.Unmarshal(evs[0].Data, &d)
	if d.MemoryID != recalled.ID || d.Verdict != "followed" || !d.Verified {
		t.Fatalf("usage = %s", evs[0].Data)
	}
	if !strings.Contains(report, "넣지 않은 기억이라 뺐습니다") {
		t.Fatalf("report = %s", report)
	}
	if got, _ := e.s.Get(recalled.ID); got.State != memory.StateConfirmed {
		t.Fatalf("followed+verified recalled memory not confirmed: %s", got.State)
	}
	cs, _ := e.s.RepeatedCorrections(true)
	if len(cs) != 1 || cs[0].Target != "orbit:tdd" {
		t.Fatalf("corrections = %+v", cs)
	}
	// 검증을 통과하지 못한 작업의 「따름」은 증거 ②가 아니다.
	in2 := workIn(t, "w-unverified", workEnd, "검증 없이 끝난 작업")
	in2.Verified = false
	in2.Recalled = []string{other.ID}
	e.doneWork(t, in2, Submission{Usage: []UsageIn{{MemoryID: other.ID, Verdict: "followed", Verified: true}}})
	if got, _ := e.s.Get(other.ID); got.State != memory.StateCandidate {
		t.Fatalf("unverified work confirmed a memory: %s", got.State)
	}
}

// 검토 B1: orb가 준비 뒤 죽어 남은 실행 줄은 정해진 시간이 지나면 막지 않는다. 그 줄은 실패로 닫고 새로 돈다.
func TestWork_오래된_실행_줄은_실패로_닫고_다시_돈다(t *testing.T) {
	start := workEnd.Add(time.Hour)
	e := setup(t, start)
	in := workIn(t, "w-stale", workEnd, "준비 뒤 orb가 죽은 작업")
	old := e.prepWork(t, in)
	// 아직 한도 안: 건너뛴다.
	e.r.Now = func() time.Time { return start.Add(workStaleAfter - time.Minute) }
	if p := e.prepWork(t, in); p.Status != StatusSkipped {
		t.Fatalf("fresh running = %+v", p)
	}
	// 제출 중에 죽은 줄도 같다.
	e.s.DB().Exec(`UPDATE retros SET status='submitting' WHERE id=?`, old.ID)
	e.r.Now = func() time.Time { return start.Add(workStaleAfter + time.Minute) }
	p := e.prepWork(t, in)
	if p.Status != StatusRunning || p.ID == old.ID {
		t.Fatalf("after stale limit = %+v", p)
	}
	st, _ := getStatus(e.s, old.ID)
	if st.Status != StatusFailed || st.Error == "" || st.FinishedAt == 0 {
		t.Fatalf("old row = %+v", st)
	}
	if _, err := os.Stat(e.r.Paths.RetroDir() + "/" + old.ID); !os.IsNotExist(err) {
		t.Fatalf("old material left: %v", err)
	}
	if n := workRows(t, e); n != 2 {
		t.Fatalf("rows = %d", n)
	}
	// 끝난 회고는 시간이 지나도 다시 돌지 않는다.
	Submit(e.s, e.r.Paths, p.ID, Submission{Summary: "끝"})
	e.r.Now = func() time.Time { return start.Add(10 * workStaleAfter) }
	if again := e.prepWork(t, in); again.Status != StatusSkipped {
		t.Fatalf("done = %+v", again)
	}
}

// 검토 S1: 검증 출처는 그 작업 자신의 프로젝트 사실에만 붙는다(모델이 준 다른 프로젝트 경로·sameAs 대상 포함).
func TestWork_검증_출처는_그_작업_프로젝트에만_붙는다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-scope", workEnd, "시험을 돌렸다")
	in.Evidence = []WorkEvidenceIn{{Ref: "step s1", Text: "go test ./... → ok: 18 packages"}}
	other := t.TempDir()
	op := project.Resolve(other)
	elsewhere, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "다른 프로젝트 시험은 go test", Scope: op.Scope, Project: op.Label, Source: memory.Source{Kind: memory.SourceSession}})
	_, report := e.doneWork(t, in, Submission{Memories: []MemoryIn{
		{Kind: "fact", Title: "남의 프로젝트 사실", Scope: "project", Project: other, Evidence: "ok: 18 packages"},
		{Kind: "fact", Title: "다른 프로젝트 시험은 go test", SameAs: elsewhere.ID, Evidence: "ok: 18 packages"},
	}})
	for _, title := range []string{"남의 프로젝트 사실", "다른 프로젝트 시험은 go test"} {
		got := findTitle(t, e, title)
		if len(got) != 1 || got[0].State != memory.StateCandidate || len(sourcesOf(got[0], memory.SourceVerification)) != 0 || got[0].Scope != op.Scope {
			t.Fatalf("%s = %+v (%s)", title, got, report)
		}
	}
	if !strings.Contains(report, "이 작업의 프로젝트") {
		t.Fatalf("report must say why: %s", report)
	}
}

// 검토 S2: 일지를 남긴 뒤 실패한 제출을 다시 받으면 같은 작업의 일지를 다시 쓴다(이름을 바꾼 둘째 일지를 만들지 않는다).
func TestWork_다시_제출해도_일지는_하나다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-j", workEnd, "일지를 남긴 작업")
	pr := project.Resolve(in.Cwd)
	p := e.prepWork(t, in)
	// 앞선 제출이 일지까지 남기고 실패한 꼴(상태는 running으로 돌아감).
	prev, _ := e.s.Save(memory.SaveInput{Kind: memory.KindJournal, Title: "2026-10-27 작업: 로그인", Body: "앞 제출", Scope: pr.Scope, Project: pr.Label,
		State: memory.StateConfirmed, Day: "2026-10-27", Source: memory.Source{Kind: memory.SourceWork, Ref: "w-j"}})
	if _, err := Submit(e.s, e.r.Paths, p.ID, Submission{Summary: "로그인", Journal: &JournalIn{Title: "로그인", Body: "다시"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	e.s.DB().QueryRow(`SELECT COUNT(*) FROM memories WHERE kind='journal'`).Scan(&n)
	st, _ := getStatus(e.s, p.ID)
	if n != 1 || st.JournalID != prev.ID {
		t.Fatalf("journals = %d, row journal %s want %s", n, st.JournalID, prev.ID)
	}
	// 다른 작업이 같은 제목을 내면 작업 id를 단 둘째 일지다.
	e.doneWork(t, workIn2(in, "w-k"), Submission{Journal: &JournalIn{Title: "로그인", Body: "다른 작업"}})
	if got := findTitle(t, e, "2026-10-27 작업: 로그인 (w-k)"); len(got) != 1 {
		t.Fatalf("second work journal = %+v", got)
	}
}

func workIn2(in WorkIn, ref string) WorkIn { in.Ref = ref; return in }

// 설계 · orbit-mem 2판 S3(검토 지적): 작업 회고의 「지금 있는 기억」은 그 작업 폴더에서 보이는 제품 기억도 싣는다.
func TestWork_재료는_작업_폴더의_제품_기억도_싣는다(t *testing.T) {
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-prod", workEnd, "상단바를 고쳤다")
	pr := project.Resolve(in.Cwd)
	e.r.Cfg.Products = place.Map{"desk": {Match: []string{pr.Label}}, "atlas": {Match: []string{"github.com/onidot-labs/atlas"}}}
	desk, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "데스크 제품 사실", Scope: "product:desk", Source: memory.Source{Kind: memory.SourceSession}})
	unsure, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "이 폴더 미정 사실", Scope: "product:?", Project: pr.Label, Source: memory.Source{Kind: memory.SourceSession}})
	atlas, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "아틀라스 사실", Scope: "product:atlas", Source: memory.Source{Kind: memory.SourceSession}})
	text := readAll(t, e, e.prepWork(t, in).ID)
	if !strings.Contains(text, desk.ID) || !strings.Contains(text, unsure.ID) || strings.Contains(text, atlas.ID) {
		t.Fatalf("material:\n%s", text)
	}
}
