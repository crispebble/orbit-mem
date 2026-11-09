package host_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/project"
	"github.com/crispebble/orbit-mem/internal/version"
)

// 시험 안의 가짜 어댑터: 호스트 어댑터가 하듯 계약 JSON 요청을 만들어 Decode → Service로 보낸다.
// 호스트 이름은 기록에만 쓰이므로 아무 이름(host-x)이나 쓴다.
type fakeAdapter struct {
	t       *testing.T
	svc     *host.Service
	name    string
	session string
	agent   string
	cwd     string
	opts    host.Options
}

func (f fakeAdapter) request(fields map[string]any) host.Request {
	f.t.Helper()
	req := map[string]any{"contract": 1, "host": f.name, "session": f.session, "cwd": f.cwd}
	if f.agent != "" {
		req["agent"] = f.agent
	}
	for k, v := range fields {
		req[k] = v
	}
	b, err := json.Marshal(req)
	if err != nil {
		f.t.Fatal(err)
	}
	r, herr := host.Decode(b)
	if herr != nil {
		f.t.Fatalf("decode %s: %v", b, herr)
	}
	return r
}

func (f fakeAdapter) do(op string, fields map[string]any) (any, *host.Error) {
	f.t.Helper()
	return f.svc.Do(op, f.request(fields), f.opts)
}

// recall runs a recall-shaped op and decodes its JSON output.
func (f fakeAdapter) recall(op string, fields map[string]any) host.Recall {
	f.t.Helper()
	out, err := f.do(op, fields)
	if err != nil {
		f.t.Fatalf("%s: %v", op, err)
	}
	var r host.Recall
	b, _ := json.Marshal(out)
	if err := json.Unmarshal(b, &r); err != nil {
		f.t.Fatalf("%s output %s: %v", op, b, err)
	}
	if !strings.Contains(string(b), `"memoryIds":[`) {
		f.t.Fatalf("memoryIds는 늘 배열: %s", b)
	}
	return r
}

type upkeepCount struct {
	mu sync.Mutex
	n  int
}

func (u *upkeepCount) AfterSessionStart(s *memory.Store, cfg config.Config) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if s != nil {
		u.n++
	}
}

func (u *upkeepCount) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.n
}

type env struct {
	t      *testing.T
	dir    string
	db     string
	cfg    config.Config
	upkeep *upkeepCount
	svc    *host.Service
}

// newEnv makes a service over a DB in a temporary folder. ORBIT_HOME points at another
// empty folder so a service that read the environment would show up there.
func newEnv(t *testing.T) *env {
	t.Helper()
	trap := t.TempDir()
	t.Setenv("ORBIT_HOME", trap)
	t.Cleanup(func() {
		if entries, _ := os.ReadDir(trap); len(entries) != 0 {
			t.Errorf("ORBIT_HOME에 무언가 생겼다: %v", entries)
		}
	})
	dir := t.TempDir()
	e := &env{t: t, dir: dir, db: filepath.Join(dir, "memory.db"), cfg: config.Default(), upkeep: &upkeepCount{}}
	e.svc = host.New(host.Setup{DB: e.db, LoadConfig: func() (config.Config, error) { return e.cfg, nil }, Upkeep: e.upkeep, Launcher: "human", Parent: "p-default"})
	t.Cleanup(e.svc.Wait)
	return e
}

func (e *env) adapter(session string) fakeAdapter {
	return fakeAdapter{t: e.t, svc: e.svc, name: "host-x", session: session, cwd: e.dir}
}

// store opens the DB for setup and checks (a write open: it makes the DB).
func (e *env) store() *memory.Store {
	e.t.Helper()
	s, err := memory.Open(e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { s.Close() })
	return s
}

func (e *env) where() string {
	p := project.Resolve(e.dir)
	if p.Dir != "" {
		return p.Dir
	}
	return p.Scope
}

func save(t *testing.T, s *memory.Store, in memory.SaveInput) memory.Memory {
	t.Helper()
	if in.Source.Kind == "" {
		in.Source = memory.Source{Kind: memory.SourceQuote, Excerpt: in.Title}
	}
	m, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func pin(t *testing.T, s *memory.Store, in memory.SaveInput) memory.Memory {
	t.Helper()
	m := save(t, s, in)
	if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	return m
}

func events(t *testing.T, s *memory.Store) []memory.Event {
	t.Helper()
	evs, err := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func kinds(evs []memory.Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind+"@"+e.Session)
	}
	return out
}

func activities(t *testing.T, s *memory.Store) []memory.Activity {
	t.Helper()
	acts, err := s.Activities(memory.ActivityFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return acts
}

// 끝 기준: context main은 같은 기억 묶음에서 inject.SessionBundle과 같은 글·id이고, 표시·사건(inject.session_start)·
// 활동 시작(launcher·parent·시각)을 남기고 유지보수 포트를 한 번 부른다.
func TestContext_main은_SessionBundle과_같고_시작을_기록한다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	rule := pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	dec := save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "기록은 짧게 한다", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	a.opts.At = 12345
	got := a.recall(host.OpContext, map[string]any{"source": "startup", "audience": "main", "parent": "p-req"})
	want := inject.SessionBundle(s, e.cfg, e.where(), "host-x", "other-session")
	if got.Text != want.Text || !slices.Equal(got.MemoryIDs, want.MemoryIDs) || got.Text == "" {
		t.Fatalf("context = %+v\nwant %+v", got, want)
	}
	// 「최근 결정」 줄은 없다(T-3): 핵심 밖 결정은 제목도 들지 않는다.
	if !slices.Contains(got.MemoryIDs, rule.ID) || strings.Contains(got.Text, dec.Title) || got.Skipped != "" {
		t.Fatalf("context = %+v", got)
	}
	if got.Budget.Chars != want.Chars || got.Budget.Limit != inject.MaxSessionStartRunes {
		t.Fatalf("budget = %+v", got.Budget)
	}
	if _, ids, ok := s.SessionBundle("host-x", "s-1"); !ok || !slices.Equal(ids, got.MemoryIDs) {
		t.Fatal("세션 묶음을 남기지 않았다")
	}
	if evs := events(t, s); len(evs) != 1 || evs[0].Kind != memory.EventInjectSessionStart || evs[0].Host != "host-x" || evs[0].Session != "s-1" {
		t.Fatalf("events = %v", kinds(evs))
	}
	acts := activities(t, s)
	if len(acts) != 1 || acts[0].Session != "s-1" || acts[0].Launcher != "human" || acts[0].Parent != "p-req" || acts[0].StartedAt != 12345 || acts[0].Cwd != e.dir {
		t.Fatalf("activity = %+v", acts)
	}
	if e.upkeep.count() != 1 {
		t.Fatalf("upkeep = %d", e.upkeep.count())
	}
	// parent가 없으면 조립 지점이 준 기본 부모다.
	a.session = "s-2"
	a.recall(host.OpContext, map[string]any{"source": "resume"})
	for _, act := range activities(t, s) {
		if act.Session == "s-2" && act.Parent != "p-default" {
			t.Fatalf("parent = %+v", act)
		}
	}
	if e.upkeep.count() != 2 {
		t.Fatalf("upkeep = %d", e.upkeep.count())
	}
}

// 세션 시작은 inject.SessionBundleWith와 같은 글이다(짧은 꼴은 없어졌다, T-27).
func TestContext_세션_시작은_SessionBundleWith와_같은_글이다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	for i, title := range []string{"규칙 하나", "규칙 둘"} {
		save(t, s, memory.SaveInput{Kind: memory.KindRule, Title: title, Body: strings.Repeat("본문 ", 20+i), Scope: memory.ScopeGlobal})
	}
	a := e.adapter("b-1")
	got := a.recall(host.OpContext, map[string]any{"source": "startup"})
	want := inject.SessionBundleWith(s, e.cfg, e.where(), "host-x", "b-2", nil)
	if got.Text != want.Text || !slices.Equal(got.MemoryIDs, want.MemoryIDs) {
		t.Fatalf("brief = %q\nwant %q", got.Text, want.Text)
	}
}

// subagent는 inject.SubagentStart이고 표시·사건은 「세션/agent」 키, 활동·유지보수는 없다(지금 subagent-start 훅과 같음).
func TestContext_subagent는_SubagentStart이고_서브에이전트_키로_센다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	a.agent = "ag-1"
	got := a.recall(host.OpContext, map[string]any{"source": "startup", "audience": "subagent"})
	want := inject.SubagentStart(s, e.cfg)
	if got.Text != want.Text || !slices.Equal(got.MemoryIDs, want.MemoryIDs) || got.Text == "" || got.Budget.Limit != memory.UserCoreRunes {
		t.Fatalf("subagent = %+v want %+v", got, want)
	}
	if evs := events(t, s); len(evs) != 1 || evs[0].Kind != memory.EventInjectSubagent || evs[0].Session != "s-1/ag-1" {
		t.Fatalf("events = %v", kinds(evs))
	}
	if len(activities(t, s)) != 0 || e.upkeep.count() != 0 {
		t.Fatal("subagent는 활동·유지보수가 아니다")
	}
}

// recall: origin이 user가 아니면 빈 결과 skipped "origin"이고 아무것도 남기지 않는다. user(또는 없음)면
// inject.PromptInjectionFor 그대로, 사건 inject.prompt는 「세션/agent」 키, 활동 요청은 세션 키다(M3).
func TestRecall_origin과_세션_키(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	dec := save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "금요일 배포 금지", Body: "주말 장애", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	a.agent = "ag-1"
	for _, origin := range []string{"agent", "system"} {
		got := a.recall(host.OpRecall, map[string]any{"prompt": "금요일 배포 금지 맞나", "origin": origin})
		if got.Skipped != "origin" || got.Text != "" || len(got.MemoryIDs) != 0 {
			t.Fatalf("%s = %+v", origin, got)
		}
	}
	if len(events(t, s)) != 0 || len(activities(t, s)) != 0 {
		t.Fatal("건너뛴 회상은 기록하지 않는다")
	}
	got := a.recall(host.OpRecall, map[string]any{"prompt": "금요일 배포 금지 맞나"})
	want := inject.PromptInjectionFor(s, e.cfg, e.where(), "금요일 배포 금지 맞나", "host-x", "other", inject.Options{})
	if got.Text != want.Text || !slices.Equal(got.MemoryIDs, want.MemoryIDs) || !slices.Contains(got.MemoryIDs, dec.ID) {
		t.Fatalf("recall = %+v want %+v", got, want)
	}
	if got.Budget.Limit != inject.MaxHookRunes || got.Budget.Chars != want.Chars {
		t.Fatalf("budget = %+v", got.Budget)
	}
	if evs := events(t, s); len(evs) != 1 || evs[0].Kind != memory.EventInjectPrompt || evs[0].Session != "s-1/ag-1" {
		t.Fatalf("events = %v", kinds(evs))
	}
	if acts := activities(t, s); len(acts) != 1 || acts[0].Session != "s-1" || acts[0].Prompts != 1 || acts[0].FirstPrompt != "금요일 배포 금지 맞나" {
		t.Fatalf("activity = %+v", acts)
	}
	if _, err := a.do(host.OpRecall, map[string]any{"prompt": "x", "origin": "bot"}); err == nil || err.Code != host.CodeUsage || err.Exit() != 2 {
		t.Fatalf("bad origin = %v", err)
	}
}

// 사람 요청 판정: 틀만 있는 요청은 DB를 열기 전에 not-person으로 건너뛴다(origin이 user여도). 아무것도 남기지 않는다.
func TestRecall_틀만_있는_요청은_DB를_열지_않고_not_person이다(t *testing.T) {
	e := newEnv(t) // DB를 만들지 않는다
	a := e.adapter("s-1")
	for _, prompt := range []string{
		"[Subagent hand-back]\nstep done",
		"<task-notification>\n<status>completed</status>\n</task-notification>",
		"Another Claude session sent a message:\nplease check the build",
		"<system-reminder>알림</system-reminder>",
	} {
		for _, origin := range []string{"", "user"} {
			f := map[string]any{"prompt": prompt}
			if origin != "" {
				f["origin"] = origin
			}
			got := a.recall(host.OpRecall, f)
			if got.Skipped != host.SkippedNotPerson || got.Text != "" || len(got.MemoryIDs) != 0 {
				t.Fatalf("%q/%q = %+v", prompt, origin, got)
			}
		}
	}
	if _, err := os.Stat(e.db); err == nil {
		t.Fatal("DB를 열지 않아야 한다")
	}
	// origin이 agent면 그대로 origin이다.
	if got := a.recall(host.OpRecall, map[string]any{"prompt": "금요일 배포", "origin": "agent"}); got.Skipped != host.SkippedOrigin {
		t.Fatalf("agent = %+v", got)
	}
}

func TestRecall_틀만_있는_요청은_활동도_사용_사건도_남기지_않는다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "금요일 배포 금지", Body: "주말 장애", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	got := a.recall(host.OpRecall, map[string]any{"prompt": "<task-notification>\n금요일 배포 금지\n</task-notification>"})
	if got.Skipped != host.SkippedNotPerson {
		t.Fatalf("got %+v", got)
	}
	if len(events(t, s)) != 0 || len(activities(t, s)) != 0 {
		t.Fatal("사람 요청이 아니면 기록하지 않는다")
	}
}

// 사람이 틀 글을 붙여 넣고 질문하면 회상한다. 활동 기록에는 사람 글(틀을 걷은 것)이 남는다.
func TestRecall_붙여_넣고_질문하면_회상하고_사람_글을_기록한다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	dec := save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "금요일 배포 금지", Body: "주말 장애", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	got := a.recall(host.OpRecall, map[string]any{"prompt": "<task-notification>\n<status>completed</status>\n</task-notification>\n\n금요일 배포 금지 맞나"})
	if got.Skipped != "" || !slices.Contains(got.MemoryIDs, dec.ID) {
		t.Fatalf("got %+v", got)
	}
	if acts := activities(t, s); len(acts) != 1 || acts[0].FirstPrompt != "금요일 배포 금지 맞나" {
		t.Fatalf("activity = %+v", acts)
	}
}

// 인자 있는 슬래시 명령은 인자로 회상한다. 인자 없는 슬래시 명령은 사소하다.
func TestRecall_슬래시_명령은_인자로_회상한다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	dec := save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "금요일 배포 금지", Body: "주말 장애", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	got := a.recall(host.OpRecall, map[string]any{"prompt": "/review 금요일 배포 금지 맞나"})
	if !slices.Contains(got.MemoryIDs, dec.ID) {
		t.Fatalf("got %+v", got)
	}
	if got := a.recall(host.OpRecall, map[string]any{"prompt": "/clear"}); got.Text != "" || got.Skipped != "" {
		t.Fatalf("인자 없는 명령 = %+v", got)
	}
}

// 사소한 요청은 요청 회상(B1)만 건너뛴다. 세션 중 다른 곳에서 확정된 새 기억 줄(B3)은 나오고 활동은 남는다.
func TestRecall_사소한_요청은_B1만_건너뛰고_B3와_활동은_남긴다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "금요일 배포 금지", Body: "주말 장애", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	a.recall(host.OpContext, map[string]any{"source": "startup"})
	time.Sleep(5 * time.Millisecond)
	fresh := save(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "릴리스 노트는 한국어로", Body: "노트 문서는 한국어", Topic: "릴리스 노트", Scope: memory.ScopeGlobal})
	got := a.recall(host.OpRecall, map[string]any{"prompt": "ㅇㅇ"})
	if got.Skipped != "" || !slices.Equal(got.MemoryIDs, []string{fresh.ID}) || !strings.Contains(got.Text, "새 기억") {
		t.Fatalf("got %+v", got)
	}
	if strings.Contains(got.Text, "금요일 배포 금지") {
		t.Fatalf("B1이 나왔다: %q", got.Text)
	}
	acts := activities(t, s)
	if len(acts) != 1 || acts[0].Prompts != 1 {
		t.Fatalf("activity = %+v", acts)
	}
	for _, ev := range events(t, s) {
		if ev.Kind == memory.EventInjectPrompt {
			t.Fatalf("사소한 요청은 요청 회상 사용이 아니다: %v", kinds(events(t, s)))
		}
	}
}

// tool-context는 inject.ToolInjection이고 계산·표시·사건 모두 「세션/agent」 키다. 넣은 것이 없으면 사건이 없다.
func TestToolContext는_ToolInjection이고_agent_키로_한_번만_준다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	m := save(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "문서는 위키에 쓴다", Body: "로컬 파일 금지", Scope: memory.ScopeGlobal, Tools: []string{"doc_write"}})
	a := e.adapter("s-1")
	a.agent = "ag-1"
	got := a.recall(host.OpToolContext, map[string]any{"tool": "doc_write"})
	if !slices.Equal(got.MemoryIDs, []string{m.ID}) || got.Budget.Limit != inject.MaxToolRunes {
		t.Fatalf("tool = %+v", got)
	}
	if marks := s.SessionMarks("host-x", "s-1/ag-1", memory.MarkTool); !marks[m.ID] {
		t.Fatalf("marks = %v", marks)
	}
	again := a.recall(host.OpToolContext, map[string]any{"tool": "doc_write"})
	if len(again.MemoryIDs) != 0 {
		t.Fatalf("한 세션에 한 번: %+v", again)
	}
	// 다른 서브에이전트는 따로 받는다.
	a.agent = "ag-2"
	if other := a.recall(host.OpToolContext, map[string]any{"tool": "doc_write"}); !slices.Equal(other.MemoryIDs, []string{m.ID}) {
		t.Fatalf("other agent = %+v", other)
	}
	if evs := events(t, s); !slices.Equal(kinds(evs), []string{memory.EventInjectTool + "@s-1/ag-1", memory.EventInjectTool + "@s-1/ag-2"}) {
		t.Fatalf("events = %v", kinds(evs))
	}
	if len(activities(t, s)) != 0 {
		t.Fatal("도구 문맥은 활동이 아니다")
	}
}

// 사건 조건(M4): inject off면 아무 사건도 없고, 빈 요청은 inject.prompt가 아니다.
func TestInject_수준과_빈_요청은_사건을_남기지_않는다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "금요일 배포 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	a.recall(host.OpRecall, map[string]any{"prompt": "   "})
	if evs := events(t, s); len(evs) != 0 {
		t.Fatalf("빈 요청 = %v", kinds(evs))
	}
	e.cfg.Inject = config.InjectOff
	a.recall(host.OpContext, map[string]any{"source": "startup"})
	a.recall(host.OpRecall, map[string]any{"prompt": "금요일 배포 금지 맞나"})
	if evs := events(t, s); len(evs) != 0 {
		t.Fatalf("off = %v", kinds(evs))
	}
	e.cfg.Inject = config.InjectRules
	a.recall(host.OpRecall, map[string]any{"prompt": "금요일 배포 금지 맞나"})
	if evs := events(t, s); len(evs) != 0 {
		t.Fatalf("rules 수준의 요청 = %v", kinds(evs))
	}
	// 새 키 recall도 같은 뜻이다(T-27): on_demand는 요청 회상 사건이 없고, off는 세션 시작 사건도 없다.
	e.cfg.Inject = config.InjectFull
	e.cfg.Recall = config.RecallOnDemand
	a.recall(host.OpRecall, map[string]any{"prompt": "금요일 배포 금지 맞나"})
	e.cfg.Recall = config.RecallOff
	a.recall(host.OpContext, map[string]any{"source": "startup"})
	if evs := events(t, s); len(evs) != 0 {
		t.Fatalf("recall on_demand·off = %v", kinds(evs))
	}
}

// 빈 세션(M5): 묶음 저장·표시·활동을 건너뛰고 사건은 지금처럼 남는다.
func TestContext_빈_세션은_묶음_표시_활동을_건너뛴다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("")
	got := a.recall(host.OpContext, map[string]any{"source": "startup"})
	if got.Text == "" {
		t.Fatal("빈 세션도 문맥은 준다")
	}
	var bundles, marks int
	s.DB().QueryRow(`SELECT COUNT(*) FROM session_bundles`).Scan(&bundles)
	s.DB().QueryRow(`SELECT COUNT(*) FROM session_marks`).Scan(&marks)
	if bundles != 0 || marks != 0 || len(activities(t, s)) != 0 {
		t.Fatalf("bundles %d marks %d activities %d", bundles, marks, len(activities(t, s)))
	}
	if evs := events(t, s); len(evs) != 1 || evs[0].Session != "" {
		t.Fatalf("events = %v", kinds(evs))
	}
}

// call: 다섯 도구는 mcpserver.Tools.Call과 같은 결과를 {name, result}로 낸다. 그 밖의 이름(memory_search 포함)은
// unknown-tool, 도구 오류는 tool(name과 함께)이고 진단 사건 host.fail을 남긴다.
func TestCall_다섯_도구와_모르는_이름(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	a := e.adapter("c-1")
	call := func(name, args string) (map[string]any, *host.Error) {
		t.Helper()
		out, err := a.do(host.OpCall, map[string]any{"name": name, "args": json.RawMessage(args), "idempotencyKey": "k-1"})
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(out)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		if v["name"] != name || v["result"] == nil {
			t.Fatalf("%s envelope = %s", name, b)
		}
		return v["result"].(map[string]any), nil
	}
	saved, err := call("memory_save", `{"kind":"fact","appliesTo":"user","title":"빌드는 make로 한다","source":{"kind":"session"}}`)
	if err != nil {
		t.Fatal(err)
	}
	id := saved["memory"].(map[string]any)["id"].(string)
	found, err := call("memory_find", `{"query":"빌드"}`)
	if err != nil || !strings.Contains(mustJSON(found), id) {
		t.Fatalf("find = %v %v", found, err)
	}
	if got, err := call("memory_get", `{"ids":["`+id+`"]}`); err != nil || !strings.Contains(mustJSON(got), id) {
		t.Fatalf("get = %v %v", got, err)
	}
	if _, err := call("memory_update", `{"id":"`+id+`","title":"빌드는 make all로 한다"}`); err != nil {
		t.Fatal(err)
	}
	if got, err := call("activity_find", `{"project":"all"}`); err != nil || got["items"] == nil {
		t.Fatalf("activity = %v %v", got, err)
	}
	for _, name := range []string{"memory_search", "proposal_decide", ""} {
		_, err := call(name, `{}`)
		want := host.CodeUnknownTool
		if name == "" {
			want = host.CodeUsage
		}
		if err == nil || err.Code != want {
			t.Fatalf("%q = %v", name, err)
		}
	}
	_, err = call("memory_get", `{"ids":["m-none"]}`)
	if err == nil || err.Code != host.CodeTool || err.Name != "memory_get" || err.Message == "" || err.Exit() != 1 {
		t.Fatalf("tool error = %+v", err)
	}
	b, _ := json.Marshal(err)
	if !strings.Contains(string(b), `"code":"tool"`) || !strings.Contains(string(b), `"name":"memory_get"`) || !strings.Contains(string(b), `"error":`) {
		t.Fatalf("error JSON = %s", b)
	}
	evs := events(t, s)
	var toolKinds, fails []string
	for _, ev := range evs {
		if strings.HasPrefix(ev.Kind, memory.EventToolPrefix) {
			toolKinds = append(toolKinds, ev.Kind)
		}
		if ev.Kind == host.EventFail {
			fails = append(fails, string(ev.Data))
		}
	}
	if !slices.Equal(toolKinds, []string{"tool.memory_save", "tool.memory_find", "tool.memory_get", "tool.memory_update", "tool.activity_find", "tool.memory_get"}) {
		t.Fatalf("tool events = %v", toolKinds)
	}
	if !slices.Equal(fails, []string{`{"code":"tool","op":"call"}`}) {
		t.Fatalf("host.fail = %v", fails)
	}
	if evs[0].Host != "host-x" || evs[0].Session != "c-1" {
		t.Fatalf("event = %+v", evs[0])
	}
	// 같은 결과 한 곳: 결과는 mcpserver.Tools.Call과 같다.
	direct, derr := mcpserver.NewTools(s, mcpserver.Options{Actor: memory.ActorAI}).Call("memory_get", []byte(`{"ids":["`+id+`"]}`), "h", "")
	viaHost, herr := call("memory_get", `{"ids":["`+id+`"]}`)
	var directMap map[string]any
	_ = json.Unmarshal([]byte(mustJSON(direct)), &directMap)
	if derr != nil || herr != nil || mustJSON(directMap) != mustJSON(viaHost) {
		t.Fatalf("host call ≠ Tools.Call:\n%s\n%s", mustJSON(direct), mustJSON(viaHost))
	}
}

// call의 쓰는 쪽은 Go 쪽 선택지(Actor)이고, 없으면 호스트의 AI다: AI는 사용자 말 기억을 사용자 말 없이 고치지 못한다.
func TestCall_쓰는_쪽은_Go_선택지다(t *testing.T) {
	e := newEnv(t)
	a := e.adapter("c-1")
	out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"rule","appliesTo":"user","topic":"보고 언어","title":"보고는 한국어로","source":{"kind":"quote","excerpt":"보고는 한국어로 해"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	id := out.(host.CallOut).Result.(mcpserver.SaveOut).Memory.ID
	edit := map[string]any{"name": "memory_update", "args": json.RawMessage(`{"id":"` + id + `","title":"보고는 영어로"}`)}
	if _, err := a.do(host.OpCall, edit); err == nil || err.Code != host.CodeTool {
		t.Fatalf("AI가 사용자 말 기억을 고쳤다: %v", err)
	}
	a.opts.Actor = memory.ActorUser
	if _, err := a.do(host.OpCall, edit); err != nil {
		t.Fatalf("사람은 고친다: %v", err)
	}
}

// memory_save는 DB가 없으면 만들고(지금 tool과 같음), 읽기 도구와 memory_update는 DB가 없으면 no-db다.
func TestCall_memory_save는_DB가_없으면_만든다(t *testing.T) {
	e := newEnv(t)
	a := e.adapter("c-1")
	for _, name := range []string{"memory_find", "memory_get", "memory_update", "activity_find"} {
		if _, err := a.do(host.OpCall, map[string]any{"name": name, "args": json.RawMessage(`{"id":"m-x","ids":["m-x"]}`)}); err == nil || err.Code != host.CodeNoDB {
			t.Fatalf("%s = %v", name, err)
		}
	}
	if _, err := os.Stat(e.db); err == nil {
		t.Fatal("읽기 도구가 DB를 만들었다")
	}
	out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"fact","appliesTo":"user","title":"첫 기억","source":{"kind":"session"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(e.db); serr != nil || !strings.Contains(mustJSON(out), "첫 기억") {
		t.Fatalf("save = %s %v", mustJSON(out), serr)
	}
}

// delivered는 사건 inject.delivered(「세션/agent」 키), reset은 세션의 묶음·표시를 지우고, session-end는 활동을 끝낸다.
// delivered에 세션이나 id가 없으면 사용법 오류(종료 2)다.
func TestDelivered_Reset_SessionEnd는_기록한다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("s-1")
	a.recall(host.OpContext, map[string]any{"source": "startup"})

	out, err := a.do(host.OpDelivered, map[string]any{"memoryIds": []string{"m-1", " ", "m-2"}})
	if err != nil || mustJSON(out) != `{"ok":true}` {
		t.Fatalf("delivered = %v %v", out, err)
	}
	evs := events(t, s)
	last := evs[len(evs)-1]
	if last.Kind != memory.EventInjectDelivered || last.Session != "s-1" || last.Host != "host-x" || string(last.Data) != `{"memoryIds":["m-1","m-2"]}` {
		t.Fatalf("delivered event = %+v %s", last, last.Data)
	}
	for _, f := range []map[string]any{{"memoryIds": []string{}}, {"memoryIds": []string{" "}}} {
		if _, err := a.do(host.OpDelivered, f); err == nil || err.Code != host.CodeUsage || err.Exit() != 2 {
			t.Fatalf("empty ids = %v", err)
		}
	}
	empty := e.adapter("")
	if _, err := empty.do(host.OpDelivered, map[string]any{"memoryIds": []string{"m-1"}}); err == nil || err.Code != host.CodeUsage {
		t.Fatalf("empty session = %v", err)
	}

	if _, _, ok := s.SessionBundle("host-x", "s-1"); !ok {
		t.Fatal("묶음이 없다")
	}
	if out, err := a.do(host.OpReset, nil); err != nil || mustJSON(out) != `{"ok":true}` {
		t.Fatalf("reset = %v %v", out, err)
	}
	if _, _, ok := s.SessionBundle("host-x", "s-1"); ok {
		t.Fatal("reset이 묶음을 지우지 않았다")
	}

	if out, err := a.do(host.OpSessionEnd, nil); err != nil || mustJSON(out) != `{"ok":true}` {
		t.Fatalf("session-end = %v %v", out, err)
	}
	if acts := activities(t, s); len(acts) != 1 || acts[0].EndedAt == 0 {
		t.Fatalf("activity = %+v", acts)
	}
	for _, ev := range events(t, s) {
		if ev.Kind == host.EventFail {
			t.Fatalf("성공한 op가 host.fail을 남겼다: %s", ev.Data)
		}
	}
}

func setMeta(t *testing.T, db, version string) {
	t.Helper()
	s, err := memory.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta("schema.version", version); err != nil {
		t.Fatal(err)
	}
	s.Close()
}

// 판 상태별(설계 10.2): context·recall·tool-context는 빈 결과 + skipped로 성공, call의 도구·delivered는 오류,
// version은 판 상태를 보인다. 어느 경우에도 DB를 만들거나 이전하지 않는다.
func TestDB_판_상태별_결과(t *testing.T) {
	cases := []struct {
		state string
		setup func(t *testing.T, db string)
		code  string
	}{
		{"missing", func(t *testing.T, db string) {}, host.CodeNoDB},
		{"empty", func(t *testing.T, db string) { os.WriteFile(db, nil, 0o600) }, host.CodeNoDB},
		{"newer", func(t *testing.T, db string) { setMeta(t, db, "99") }, host.CodeSchemaNewer},
		{"older", func(t *testing.T, db string) { setMeta(t, db, "3") }, host.CodeSchemaOlder},
	}
	for _, c := range cases {
		t.Run(c.state, func(t *testing.T) {
			e := newEnv(t)
			c.setup(t, e.db)
			before, _ := os.ReadFile(e.db)
			a := e.adapter("s-1")
			for _, op := range []string{host.OpContext, host.OpRecall, host.OpToolContext} {
				got := a.recall(op, map[string]any{"source": "startup", "prompt": "배포", "tool": "doc_write"})
				if got.Skipped != c.code || got.Text != "" || len(got.MemoryIDs) != 0 {
					t.Fatalf("%s = %+v", op, got)
				}
			}
			for _, name := range []string{"memory_find", "memory_get", "activity_find"} {
				if _, err := a.do(host.OpCall, map[string]any{"name": name, "args": json.RawMessage(`{"ids":["m-1"],"id":"m-1"}`)}); err == nil || err.Code != c.code || err.Exit() != 1 {
					t.Fatalf("%s = %v", name, err)
				}
			}
			for _, op := range []string{host.OpDelivered, host.OpReset, host.OpSessionEnd} {
				if _, err := a.do(op, map[string]any{"memoryIds": []string{"m-1"}}); err == nil || err.Code != c.code {
					t.Fatalf("%s = %v", op, err)
				}
			}
			out, err := a.do(host.OpVersion, nil)
			if err != nil {
				t.Fatal(err)
			}
			v := out.(host.VersionOut)
			if v.Binary != version.Version || v.Contract != host.Contract || v.Schema != memory.SchemaVersion || v.DB.State != c.state {
				t.Fatalf("version = %+v", v)
			}
			e.svc.Wait()
			after, _ := os.ReadFile(e.db)
			if string(before) != string(after) {
				t.Fatal("읽기 op가 DB를 바꿨다")
			}
			if _, err := os.Stat(filepath.Join(e.dir, "backups")); err == nil {
				t.Fatal("읽기 op가 사본을 만들었다")
			}
			if c.state == "missing" {
				if _, err := os.Stat(e.db); err == nil {
					t.Fatal("읽기 op가 DB를 만들었다")
				}
			}
			// 쓰기 도구는 쓰기 열기다: 새 판 DB는 이전하지 않고 schema-newer, 없으면 memory_update는 no-db,
			// 그 밖은 만들거나 이전한 뒤 도구를 돌린다(없는 id는 도구 오류).
			_, uerr := a.do(host.OpCall, map[string]any{"name": "memory_update", "args": json.RawMessage(`{"id":"m-none","title":"x"}`)})
			wantUpdate := map[string]string{"missing": host.CodeNoDB, "empty": host.CodeTool, "newer": host.CodeSchemaNewer, "older": host.CodeTool}[c.state]
			if uerr == nil || uerr.Code != wantUpdate {
				t.Fatalf("update = %v, want %s", uerr, wantUpdate)
			}
			if c.state == "missing" {
				if _, err := os.Stat(e.db); err == nil {
					t.Fatal("memory_update가 DB를 만들었다")
				}
			}
			_, serr := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"fact","appliesTo":"user","title":"저장","source":{"kind":"session"}}`)})
			if c.state == "newer" {
				if serr == nil || serr.Code != host.CodeSchemaNewer {
					t.Fatalf("save on newer = %v", serr)
				}
			} else if serr != nil {
				t.Fatalf("save = %v", serr)
			}
		})
	}
}

// 정상 판 DB의 version은 판 상태 ok와 DB 판을 보인다. JSON 칸은 binary·contract·schema·db{schema,state}.
func TestVersion_정상_DB(t *testing.T) {
	e := newEnv(t)
	e.store()
	out, err := e.adapter("").do(host.OpVersion, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"binary":"` + version.Version + `","contract":1,"schema":` + jsonInt(memory.SchemaVersion) + `,"db":{"schema":` + jsonInt(memory.SchemaVersion) + `,"state":"ok"}}`
	if mustJSON(out) != want {
		t.Fatalf("version = %s\nwant %s", mustJSON(out), want)
	}
}

// 시간 한도(설계 10.1·10.2): hostTimeouts로 바꾸고, 한도 안에 계산이 끝나지 않으면 timeout(종료 1)이다.
// 남은 고루틴은 Wait로 끝까지 기다리고, 그동안 임시 DB 밖(ORBIT_HOME)을 열지 않는다. 시간 초과 때만
// host.fail({op, code: timeout})을 남기고 표시·사건·활동·유지보수는 하지 않는다.
func TestTimeout_설정이_한도를_바꾸고_시간_초과는_결과를_내지_않는다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("t-1")
	e.cfg.HostTimeouts = map[string]string{"context": "1ns", "recall": "1ns", "call": "1ns"}
	for _, f := range []struct {
		op     string
		fields map[string]any
	}{
		{host.OpContext, map[string]any{"source": "startup"}},
		{host.OpRecall, map[string]any{"prompt": "존댓말"}},
		{host.OpCall, map[string]any{"name": "memory_find", "args": json.RawMessage(`{}`)}},
	} {
		out, err := a.do(f.op, f.fields)
		if err == nil || err.Code != host.CodeTimeout || err.Exit() != 1 || out != nil {
			t.Fatalf("%s = %v %v", f.op, out, err)
		}
	}
	e.svc.Wait()
	var fails []string
	for _, ev := range events(t, s) {
		if ev.Kind != host.EventFail {
			t.Fatalf("시간 초과 뒤 기록 = %s", ev.Kind)
		}
		fails = append(fails, string(ev.Data))
	}
	slices.Sort(fails)
	if !slices.Equal(fails, []string{`{"code":"timeout","op":"call"}`, `{"code":"timeout","op":"context"}`, `{"code":"timeout","op":"recall"}`}) {
		t.Fatalf("host.fail = %v", fails)
	}
	var written int
	s.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM session_bundles) + (SELECT COUNT(*) FROM session_marks)`).Scan(&written)
	if len(activities(t, s)) != 0 || e.upkeep.count() != 0 || written != 0 {
		t.Fatal("시간 초과 뒤 활동·유지보수·표시를 남겼다")
	}
	// 기본 한도면 같은 요청이 결과를 낸다.
	e.cfg.HostTimeouts = nil
	if got := a.recall(host.OpContext, map[string]any{"source": "startup"}); got.Text == "" {
		t.Fatal("기본 한도에서 빈 결과")
	}
}

// 결과는 계산이 끝나면 바로 받고(Result), 기록은 그 뒤에 끝난다(Recorded). Result는 여러 번 불러도 같다.
func TestRun_결과가_기록보다_먼저다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("r-1")
	block := make(chan struct{})
	e.svc = host.New(host.Setup{DB: e.db, LoadConfig: func() (config.Config, error) { return e.cfg, nil }, Launcher: "human",
		Upkeep: host.UpkeepFunc(func(*memory.Store, config.Config) { <-block })})
	run := e.svc.Start(host.OpContext, a.request(map[string]any{"source": "startup"}), host.Options{})
	out, err := run.Result()
	if err != nil || out.(host.Recall).Text == "" {
		t.Fatalf("result = %v %v", out, err)
	}
	select {
	case <-run.Recorded():
		t.Fatal("유지보수가 끝나기 전에 기록이 끝났다")
	default:
	}
	close(block)
	<-run.Recorded()
	again, _ := run.Result()
	if mustJSON(again) != mustJSON(out) {
		t.Fatal("Result가 바뀌었다")
	}
	e.svc.Wait()
}

// 다른 연결이 쓰기 잠금을 쥐어 표시·사건·활동이 실패해도 결과는 한도 안에 나온다(설계 10.2: 표시 없이 넘어간다).
func TestLock_기록이_잠금으로_실패해도_결과는_나온다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	rule := pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	save(t, s, memory.SaveInput{Kind: memory.KindDecision, Title: "금요일 배포 금지", Scope: memory.ScopeGlobal})
	ctx := context.Background()
	other, err := sql.Open("sqlite", "file:"+e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	// 한도는 넉넉히 준다: 결과가 한도에 걸려 나온 것(timeout 오류)이 아니라 기록 실패와 상관없이 나왔음을 본다.
	// 잠금은 시험이 쥐고 있다가 결과를 받은 뒤에 푼다.
	e.cfg.HostTimeouts = map[string]string{host.OpContext: "30s", host.OpRecall: "30s"}
	const bound = 10 * time.Second
	a := e.adapter("l-1")
	result := func(op string, fields map[string]any) (host.Recall, time.Duration) {
		t.Helper()
		start := time.Now()
		out, err := e.svc.Start(op, a.request(fields), host.Options{}).Result()
		took := time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v after %v", op, err, took)
		}
		return out.(host.Recall), took
	}
	got, ctxTook := result(host.OpContext, map[string]any{"source": "startup"})
	rec, recTook := result(host.OpRecall, map[string]any{"prompt": "금요일 배포 금지 맞나"})
	if ctxTook > bound || recTook > bound {
		t.Fatalf("결과가 잠금과 상관없이 나와야 한다: context %v recall %v", ctxTook, recTook)
	}
	if !slices.Contains(got.MemoryIDs, rule.ID) || rec.Text == "" {
		t.Fatalf("context %+v recall %+v", got, rec)
	}
	waitStart := time.Now()
	e.svc.Wait() // 기록은 잠금에 막혀 실패한 채로 끝난다(결과 시각과 따로 본다)
	if waited := time.Since(waitStart); waited > bound {
		t.Fatalf("기록 대기 %v", waited)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if evs := events(t, s); len(evs) != 0 {
		t.Fatalf("잠금 중 사건 = %v", kinds(evs))
	}
	if len(activities(t, s)) != 0 {
		t.Fatal("잠금 중 활동")
	}
}

// 계약 입력: contract가 없으면 1, 1이 아니면 contract 오류(종료 1). 깨진 JSON·모르는 op·틀린 값은 usage(종료 2).
// profile·readScope·sandboxed·task·requestId는 받기만 한다(idempotencyKey는 call memory_save의 args에 합친다). trust는 글 하나(owner·agent·untrusted·system)만 받는다.
func TestDecode_계약과_사용법(t *testing.T) {
	e := newEnv(t)
	e.store()
	if _, err := host.Decode([]byte(`not json`)); err == nil || err.Code != host.CodeUsage || err.Exit() != 2 {
		t.Fatalf("broken = %v", err)
	}
	req, err := host.Decode([]byte(`{"host":"h","session":"s","profile":"p","trust":"agent","readScope":["a"],"sandboxed":true,"task":"t","requestId":"r"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Do(host.OpContext, req, host.Options{}); err != nil {
		t.Fatalf("contract 없음 = %v", err)
	}
	for _, bad := range []string{`{"level":"x"}`, `"boss"`, `7`, `["owner"]`, `""`} {
		r, derr := host.Decode([]byte(`{"session":"s","trust":` + bad + `}`))
		if derr != nil {
			t.Fatal(derr)
		}
		for _, op := range []string{host.OpContext, host.OpCall} {
			_, err := e.svc.Do(op, r, host.Options{})
			if err == nil || err.Code != host.CodeUsage || err.Exit() != 2 || err.Detail == nil || err.Detail.Reason != host.DetailBadValue || err.Detail.Args[0] != "trust" {
				t.Errorf("trust %s %s = %+v", bad, op, err)
			}
		}
	}
	two, _ := host.Decode([]byte(`{"contract":2,"session":"s"}`))
	if _, err := e.svc.Do(host.OpContext, two, host.Options{}); err == nil || err.Code != host.CodeContract || err.Exit() != 1 {
		t.Fatalf("contract 2 = %v", err)
	}
	if _, err := e.svc.Do("ingest-session", req, host.Options{}); err == nil || err.Code != host.CodeUsage {
		t.Fatalf("unknown op = %v", err)
	}
	a := e.adapter("s")
	for _, f := range []map[string]any{{"source": "later"}, {"audience": "team"}} {
		if _, err := a.do(host.OpContext, f); err == nil || err.Code != host.CodeUsage {
			t.Fatalf("%v = %v", f, err)
		}
	}
	if !slices.Equal(host.Ops, []string{"version", "context", "recall", "tool-context", "call", "delivered", "reset", "session-end"}) {
		t.Fatalf("ops = %v", host.Ops)
	}
	// 설정의 시간 한도 표는 op 표와 같다.
	defaults := config.DefaultHostTimeouts()
	for _, op := range host.Ops {
		if defaults[op] == 0 {
			t.Errorf("%s의 기본 한도가 없다", op)
		}
	}
	if len(defaults) != len(host.Ops) {
		t.Errorf("한도 표 %v", defaults)
	}
	var codes []string
	for _, c := range []string{host.CodeUsage, host.CodeContract, host.CodeTimeout, host.CodeNoDB, host.CodeSchemaNewer, host.CodeSchemaOlder, host.CodeUnknownTool, host.CodeTool, host.CodeInternal} {
		codes = append(codes, c)
	}
	if strings.Join(codes, ",") != "usage,contract,timeout,no-db,schema-newer,schema-older,unknown-tool,tool,internal" {
		t.Fatalf("codes = %v", codes)
	}
}

// 설정을 읽지 못하면(검토 M1) call은 기본 설정으로 진행한다(옛 tool과 같다: 깨진 설정이 기억 도구를 막지 않는다).
// 회상 op(context·recall·tool-context)는 internal 오류이고 DB를 열지 않는다: 사용자가 끈 회상을 기본값으로 켜지 않는다.
// version은 설정 없이도 답한다. 읽다 만 설정(여기서는 call 한도 1ns)은 쓰지 않는다.
func TestConfig_설정을_읽지_못하면_call은_기본_설정이고_회상_op는_internal(t *testing.T) {
	e := newEnv(t)
	broken := config.Default()
	broken.Inject = config.InjectOff
	broken.HostTimeouts = map[string]string{"call": "1ns"}
	e.svc = host.New(host.Setup{DB: e.db, LoadConfig: func() (config.Config, error) { return broken, os.ErrPermission }})
	a := e.adapter("s")
	for _, op := range []string{host.OpContext, host.OpRecall, host.OpToolContext} {
		if _, err := a.do(op, map[string]any{"prompt": "존댓말", "tool": "Bash"}); err == nil || err.Code != host.CodeInternal {
			t.Fatalf("%s = %v", op, err)
		}
	}
	if _, err := os.Stat(e.db); !os.IsNotExist(err) {
		t.Fatalf("회상 op가 DB를 열었다: %v", err)
	}
	if _, err := a.do(host.OpVersion, nil); err != nil {
		t.Fatalf("version = %v", err)
	}
	out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"lesson","title":"깨진 설정에도 저장","source":{"kind":"session","ref":"x:s"}}`)})
	if err != nil {
		t.Fatalf("memory_save = %v", err)
	}
	var saved struct {
		Result struct {
			Memory struct{ ID string } `json:"memory"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(mustJSON(out)), &saved) != nil || saved.Result.Memory.ID == "" {
		t.Fatalf("memory_save = %s", mustJSON(out))
	}
	id := saved.Result.Memory.ID
	out, err = a.do(host.OpCall, map[string]any{"name": "memory_get", "args": json.RawMessage(`{"ids":["` + id + `"]}`)})
	if err != nil || !strings.Contains(mustJSON(out), "깨진 설정에도 저장") {
		t.Fatalf("memory_get = %v %v", mustJSON(out), err)
	}
	e.svc.Wait()
}

// 쓰기 도구(memory_save·memory_update)의 timeout은 「반영됐을 수 있음」(mayHaveApplied)을 알린다(검토 M3): 한도를 넘긴
// 계산도 끝까지 돌아 쓰기가 커밋될 수 있다. 읽기 도구와 다른 op의 timeout에는 이 칸이 없다.
func TestTimeout_쓰기_도구의_timeout만_반영됐을_수_있음을_알린다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	m := save(t, s, memory.SaveInput{Kind: memory.KindLesson, Title: "고칠 교훈", Scope: memory.ScopeGlobal})
	a := e.adapter("t-1")
	e.cfg.HostTimeouts = map[string]string{"context": "1ns", "call": "1ns"}
	for _, f := range []struct {
		op     string
		fields map[string]any
		write  bool
	}{
		{host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"lesson","title":"x","source":{"kind":"session","ref":"x:s"}}`)}, true},
		{host.OpCall, map[string]any{"name": "memory_update", "args": json.RawMessage(`{"id":"` + m.ID + `","title":"바뀜"}`)}, true},
		{host.OpCall, map[string]any{"name": "memory_find", "args": json.RawMessage(`{}`)}, false},
		{host.OpCall, map[string]any{"name": "memory_get", "args": json.RawMessage(`{"id":"` + m.ID + `"}`)}, false},
		{host.OpContext, map[string]any{"source": "startup"}, false},
	} {
		_, err := a.do(f.op, f.fields)
		if err == nil || err.Code != host.CodeTimeout || err.MayHaveApplied != f.write {
			t.Fatalf("%s %v = %+v", f.op, f.fields["name"], err)
		}
		if got := strings.Contains(mustJSON(err), `"mayHaveApplied":true`); got != f.write {
			t.Fatalf("%s %v JSON = %s", f.op, f.fields["name"], mustJSON(err))
		}
	}
}

// 한도를 넘긴 뒤 나온 결과도 timeout이다(결정적). 막는 쪽은 시험이 놓을 때까지 막는다(검토 L1: 시간 여유에 기대지 않는다).
// ① memory_save가 DB를 연 뒤(한도 검사를 지난 뒤) 저장 중 기억 저장소의 시계에서 시험이 문을 열 때까지 멈춘다.
// Result는 200ms 한도에서 timeout을 낸다: 한도 + 1초 안에 돌아오고, 그때 계산은 끝나지 않았다(문이 닫혀 있다).
// 문을 열면 저장은 한도 뒤에 커밋된다: 그래서 쓰기 도구의 timeout은 mayHaveApplied이고, 다시 불러도 timeout이다.
// ② 계산이 한도 뒤에 끝난 다음 Result를 불러도 timeout이다(context: 세션 묶음 저장이 잠금에 막혀 한도를 넘긴다).
func TestTimeout_계산이_한도를_넘기면_결과도_timeout이다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	const limit = 200 * time.Millisecond
	e.cfg.HostTimeouts = map[string]string{"context": limit.String(), "call": limit.String()}
	gate := make(chan struct{})
	var opened sync.Once
	open := func() { opened.Do(func() { close(gate) }) }
	gated := host.New(host.Setup{DB: e.db, LoadConfig: func() (config.Config, error) { return e.cfg, nil },
		Now: func() time.Time { <-gate; return time.Now() }})
	t.Cleanup(gated.Wait)
	t.Cleanup(open)
	a := e.adapter("late-1")

	start := time.Now()
	run := gated.Start(host.OpCall, a.request(map[string]any{"name": "memory_save",
		"args": json.RawMessage(`{"kind":"lesson","title":"한도 뒤에 커밋된 교훈","source":{"kind":"session","ref":"x:s"}}`)}), host.Options{At: 1})
	out, err := run.Result()
	if err == nil || err.Code != host.CodeTimeout || !err.MayHaveApplied || out != nil {
		t.Fatalf("result = %v %+v", out, err)
	}
	if took := time.Since(start); took >= limit+time.Second {
		t.Fatalf("Result가 %v 걸렸다", took)
	}
	select {
	case <-run.Recorded():
		t.Fatal("Result가 계산이 끝나기를 기다렸다")
	default:
	}
	open()
	gated.Wait()
	if _, err := run.Result(); err == nil || err.Code != host.CodeTimeout || !err.MayHaveApplied {
		t.Fatalf("끝난 뒤 result = %+v", err)
	}
	found, ferr := s.Find(memory.FindInput{Query: "한도 뒤에 커밋된 교훈", AllProjects: true})
	if ferr != nil || len(found) != 1 {
		t.Fatalf("한도 뒤의 쓰기가 커밋되지 않았다: %v %v", found, ferr)
	}

	release := holdWriteLock(t, e.db)
	a.session = "late-2"
	run = e.svc.Start(host.OpContext, a.request(map[string]any{"source": "startup"}), host.Options{})
	e.svc.Wait() // 묶음 저장이 잠금 대기(300ms) 끝에 실패한 뒤: 계산은 한도(200ms) 뒤에 끝났다
	if out, err := run.Result(); err == nil || err.Code != host.CodeTimeout || err.MayHaveApplied || out != nil {
		t.Fatalf("끝난 뒤 result = %v %+v", out, err)
	}
	release()
	if len(activities(t, s)) != 0 || e.upkeep.count() != 0 {
		t.Fatal("시간 초과 뒤 활동·유지보수")
	}
}

// reset이 표시를 지우지 못하면(쓰기 잠금) 성공으로 숨기지 않고 internal이다(검토 L4): 숨기면 압축 뒤 회상에서
// 기억이 「이미 보인 것」으로 빠진다. 잠금이 풀리면 다시 reset해 지운다.
func TestReset_표시를_지우지_못하면_internal이다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("rs-1")
	a.recall(host.OpContext, map[string]any{"source": "startup"})
	e.cfg.HostTimeouts = map[string]string{"reset": "10s"} // 잠금 대기(300ms) 끝의 실패를 한도와 섞지 않는다
	release := holdWriteLock(t, e.db)
	if out, err := a.do(host.OpReset, nil); err == nil || err.Code != host.CodeInternal || out != nil {
		t.Fatalf("reset under lock = %v %+v", out, err)
	}
	release()
	if _, _, ok := s.SessionBundle("host-x", "rs-1"); !ok {
		t.Fatal("묶음이 이미 없다")
	}
	if out, err := a.do(host.OpReset, nil); err != nil || mustJSON(out) != `{"ok":true}` {
		t.Fatalf("reset = %v %v", out, err)
	}
	if _, _, ok := s.SessionBundle("host-x", "rs-1"); ok {
		t.Fatal("reset이 묶음을 지우지 않았다")
	}
}

// 사용법·계약 오류의 세부는 사람 글이 아니라 구조(Detail: 세부 코드와 인자)다(검토 L5): 글은 CLI가 언어 설정대로 만든다.
// Detail은 계약 JSON에 나가지 않는다(짝 파일의 오류 꼴 {error, code}는 그대로).
func TestError_사용법과_계약_오류의_세부는_구조다(t *testing.T) {
	e := newEnv(t)
	e.store()
	a := e.adapter("s")
	_, broken := host.Decode([]byte(`not json`))
	two, _ := host.Decode([]byte(`{"contract":2,"session":"s"}`))
	errOf := func(op string, req host.Request) *host.Error {
		_, err := e.svc.Do(op, req, host.Options{})
		return err
	}
	cases := []struct {
		name string
		err  *host.Error
		code string
		want host.Detail
	}{
		{"broken json", broken, host.CodeUsage, host.Detail{Reason: host.DetailInput}},
		{"unknown op", errOf("ingest-session", a.request(nil)), host.CodeUsage, host.Detail{Reason: host.DetailUnknownOp, Args: []string{"ingest-session"}}},
		{"contract 2", errOf(host.OpContext, two), host.CodeContract, host.Detail{Reason: host.DetailContract, Args: []string{"2", "1"}}},
		{"bad source", errOf(host.OpContext, a.request(map[string]any{"source": "later"})), host.CodeUsage, host.Detail{Reason: host.DetailBadValue, Args: []string{"source", "later"}}},
		{"bad audience", errOf(host.OpContext, a.request(map[string]any{"audience": "team"})), host.CodeUsage, host.Detail{Reason: host.DetailBadValue, Args: []string{"audience", "team"}}},
		{"bad origin", errOf(host.OpRecall, a.request(map[string]any{"origin": "bot"})), host.CodeUsage, host.Detail{Reason: host.DetailBadValue, Args: []string{"origin", "bot"}}},
		{"no name", errOf(host.OpCall, a.request(nil)), host.CodeUsage, host.Detail{Reason: host.DetailMissing, Args: []string{"name"}}},
		{"no ids", errOf(host.OpDelivered, a.request(map[string]any{"memoryIds": []string{" "}})), host.CodeUsage, host.Detail{Reason: host.DetailMissing, Args: []string{"memoryIds"}}},
		{"no session", errOf(host.OpDelivered, e.adapter("").request(map[string]any{"memoryIds": []string{"m-1"}})), host.CodeUsage, host.Detail{Reason: host.DetailMissing, Args: []string{"session"}}},
		{"neither", errOf(host.OpDelivered, e.adapter("").request(nil)), host.CodeUsage, host.Detail{Reason: host.DetailMissing, Args: []string{"session", "memoryIds"}}},
	}
	for _, c := range cases {
		if c.err == nil || c.err.Code != c.code || c.err.Detail == nil || c.err.Detail.Reason != c.want.Reason || !slices.Equal(c.err.Detail.Args, c.want.Args) {
			t.Errorf("%s = %+v (detail %+v)", c.name, c.err, c.err.Detail)
			continue
		}
		if j := mustJSON(c.err); strings.Contains(j, "detail") || strings.Contains(j, "reason") {
			t.Errorf("%s: Detail이 JSON에 나갔다: %s", c.name, j)
		}
	}
	// 그 밖의 오류에는 세부가 없다.
	e.cfg.HostTimeouts = map[string]string{"context": "1ns"}
	if _, err := a.do(host.OpContext, nil); err == nil || err.Code != host.CodeTimeout || err.Detail != nil {
		t.Fatalf("timeout = %+v", err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func jsonInt(n int) string { return mustJSON(n) }

// holdWriteLock makes another connection hold the DB's write lock until the returned func runs.
func holdWriteLock(t *testing.T, db string) func() {
	t.Helper()
	ctx := context.Background()
	other, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			conn.ExecContext(ctx, "ROLLBACK")
			conn.Close()
			other.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// 기록은 남은 시간 안에서만 한다: 결과는 한도 안에 나왔지만 사건·활동 쓰기가 잠금에 막혀 한도를 넘기면
// 남은 기록(유지보수)은 하지 않는다.
func TestRecord_기록은_남은_시간_안에서만_한다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("rec-1")
	a.recall(host.OpContext, map[string]any{"source": "startup"}) // 묶음을 만들어 둔다: 다음 계산은 쓰지 않는다
	if e.upkeep.count() != 1 {
		t.Fatalf("upkeep = %d", e.upkeep.count())
	}
	e.cfg.HostTimeouts = map[string]string{"context": "400ms"}
	holdWriteLock(t, e.db)
	run := e.svc.Start(host.OpContext, a.request(map[string]any{"source": "resume"}), host.Options{})
	if out, err := run.Result(); err != nil || out.(host.Recall).Text == "" {
		t.Fatalf("result = %v %v", out, err)
	}
	e.svc.Wait()
	if e.upkeep.count() != 1 {
		t.Fatalf("한도가 지난 뒤 유지보수를 했다: %d", e.upkeep.count())
	}
}

// 통로 상한(조립 지점이 Options로 넣는다)은 agent다. 공통 칸 trust는 이번 호출의 상한을 낮추기만 한다.
func TestCall_trust는_이번_호출의_상한을_낮추기만_한다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	save := func(limit, trust, title string) string {
		t.Helper()
		// 서로 비슷한 제목의 다른 기억을 일부러 만든다: 저장 때 정리의 고르기 없이 새로 저장한다(resolve add).
		args, _ := json.Marshal(map[string]any{"kind": "fact", "appliesTo": "user", "title": title, "body": "본문 " + title, "source": map[string]any{"kind": "session", "ref": "x:s"}, "resolve": "add"})
		fields := map[string]any{"name": "memory_save", "args": json.RawMessage(args)}
		if trust != "" {
			fields["trust"] = trust
		}
		b, _ := json.Marshal(fields)
		req, derr := host.Decode(b)
		if derr != nil {
			t.Fatal(derr)
		}
		out, err := e.svc.Do(host.OpCall, req, host.Options{TrustLimit: limit})
		if err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		id := out.(host.CallOut).Result.(mcpserver.SaveOut).Memory.ID
		m, gerr := s.Get(id)
		if gerr != nil {
			t.Fatal(gerr)
		}
		return m.Trust
	}
	for _, c := range []struct{ limit, trust, want string }{
		{memory.TrustAgent, "", memory.TrustAgent},
		{"", "", memory.TrustAgent},
		{memory.TrustAgent, memory.TrustUntrusted, memory.TrustUntrusted},
		{memory.TrustAgent, memory.TrustSystem, memory.TrustSystem},
		{memory.TrustAgent, memory.TrustOwner, memory.TrustAgent}, // 올리지 못한다
		{memory.TrustOwner, memory.TrustUntrusted, memory.TrustUntrusted},
		{memory.TrustOwner, memory.TrustAgent, memory.TrustAgent},
	} {
		if got := save(c.limit, c.trust, "상한 "+c.limit+" 요청 "+c.trust); got != c.want {
			t.Errorf("limit %q trust %q = %q, want %q", c.limit, c.trust, got, c.want)
		}
	}
}

// 세션 시작 안내 줄(Options.SessionNotes)은 묶음을 새로 만들 때만 불리고 묶음 글 끝에 함께 저장된다(설계 보완 T-20):
// resume은 부르지 않고 바이트 같은 묶음, reset(compact·clear) 뒤에는 다시 부른다. subagent·recall off는 부르지 않는다.
func TestContext_안내_줄은_묶음을_만들_때만_불리고_함께_저장된다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal})
	a := e.adapter("n-1")
	calls := 0
	a.opts.SessionNotes = func() []string { calls++; return []string{"판 안내", " ", "새 판 안내"} }
	first := a.recall(host.OpContext, map[string]any{"source": "startup"})
	if calls != 1 || !strings.Contains(first.Text, "답은 존댓말") || !strings.HasSuffix(first.Text, "\n판 안내\n새 판 안내") {
		t.Fatalf("calls %d first %q", calls, first.Text)
	}
	if text, _, ok := s.SessionBundle("host-x", "n-1"); !ok || text != first.Text {
		t.Fatalf("saved bundle %v %q", ok, text)
	}
	again := a.recall(host.OpContext, map[string]any{"source": "resume"})
	if calls != 1 || again.Text != first.Text || !slices.Equal(again.MemoryIDs, first.MemoryIDs) {
		t.Fatalf("resume calls %d %q", calls, again.Text)
	}
	if _, err := a.do(host.OpReset, nil); err != nil {
		t.Fatal(err)
	}
	a.opts.SessionNotes = func() []string { calls++; return []string{"다른 판 안내"} }
	renewed := a.recall(host.OpContext, map[string]any{"source": "compact"})
	if calls != 2 || !strings.HasSuffix(renewed.Text, "\n다른 판 안내") || strings.Contains(renewed.Text, "새 판 안내") {
		t.Fatalf("after reset calls %d %q", calls, renewed.Text)
	}
	sub := a
	sub.agent = "ag-1"
	sub.recall(host.OpContext, map[string]any{"source": "startup", "audience": "subagent"})
	if calls != 2 {
		t.Fatalf("subagent called the notes: %d", calls)
	}
	e.cfg.Recall = config.RecallOff
	off := e.adapter("n-2")
	off.opts.SessionNotes = a.opts.SessionNotes
	// recall=off: 기억은 0자이고 안내 줄만 나간다(예전 inject=off와 같다).
	if got := off.recall(host.OpContext, map[string]any{"source": "startup"}); calls != 3 || got.Text != "다른 판 안내" {
		t.Fatalf("recall off calls %d %q", calls, got.Text)
	}
}

// 안내 줄 만들개가 panic해도 문맥 op는 핵심 묶음을 내고 남긴다. 안내 줄만 없다(원칙 6: 실패하면 빈 결과로 넘어간다).
func TestContext_안내_줄이_panic해도_핵심은_나오고_안내_줄만_없다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Scope: memory.ScopeGlobal})
	a := e.adapter("p-1")
	a.opts.SessionNotes = func() []string { panic("notes") }
	out, err := a.do(host.OpContext, map[string]any{"source": "startup"})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	res, ok := out.(host.Recall)
	if !ok || !strings.Contains(res.Text, "답은 존댓말") || strings.Contains(res.Text, "notes") {
		t.Fatalf("out %#v", out)
	}
	text, _, saved := s.SessionBundle("host-x", "p-1")
	if !saved || text != res.Text {
		t.Fatalf("saved %v %q", saved, text)
	}
}
