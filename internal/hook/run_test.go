package hook_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 실행기는 호스트를 모른다: 시험은 가짜 어댑터로 본다(설계 · 포트-어댑터, ORBIT-23 D5).
type fake struct {
	plan      hook.Plan
	panicPlan bool
}

func (f fake) Plan(ev hook.Event, input []byte) hook.Plan {
	if f.panicPlan {
		panic("plan")
	}
	return f.plan
}

func (fake) Output(ev hook.Event, text string) string {
	if text == "" {
		return ""
	}
	return "OUT " + string(ev) + " " + text
}

type world struct {
	t   *testing.T
	db  string
	cfg config.Config
	svc *host.Service
	mu  sync.Mutex
	bad []string // Failed로 알린 op:code
}

func newWorld(t *testing.T, withDB bool) *world {
	t.Helper()
	w := &world{t: t, db: filepath.Join(t.TempDir(), "memory.db"), cfg: config.Default()}
	if withDB {
		s, err := memory.Open(w.db)
		if err != nil {
			t.Fatal(err)
		}
		m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Topic: "말투", Scope: memory.ScopeGlobal,
			Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "존댓말로"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	w.svc = host.New(host.Setup{DB: w.db, LoadConfig: func() (config.Config, error) { return w.cfg, nil }, Launcher: "human"})
	t.Cleanup(w.svc.Wait) // 시간 초과 뒤에도 남은 op 고루틴이 끝난 뒤 돌아온다
	return w
}

func (w *world) runner(notes func(hook.Event, hook.Plan) []string) hook.Runner {
	return hook.Runner{Service: w.svc, Notes: notes, Failed: func(op string, err *host.Error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.bad = append(w.bad, op+":"+err.Code)
	}}
}

func (w *world) failed() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.bad)
}

func step(op string, r host.Request) hook.Step {
	r.Contract, r.Host, r.Cwd = host.Contract, "test", "/tmp/orbit-hook"
	if r.Session == "" {
		r.Session = "s-1"
	}
	return hook.Step{Op: op, Request: r}
}

func startPlan() hook.Plan {
	return hook.Plan{Cwd: "/raw/cwd", Steps: []hook.Step{
		step(host.OpReset, host.Request{}),
		step(host.OpContext, host.Request{Source: host.SourceCompact, Audience: host.AudienceMain}),
	}}
}

func TestRunner_마지막_op의_문맥을_어댑터_꼴로_한_줄_낸다(t *testing.T) {
	w := newWorld(t, true)
	var out bytes.Buffer
	w.runner(nil).Run(fake{plan: startPlan()}, hook.SessionStart, nil, &out)
	got := out.String()
	if !strings.HasPrefix(got, "OUT session-start ") || !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") < 1 || !strings.Contains(got, "답은 존댓말") {
		t.Fatalf("output %q", got)
	}
	if f := w.failed(); len(f) != 0 {
		t.Fatalf("failed %v", f)
	}
}

func TestRunner_op는_차례대로_돌고_앞_op가_실패해도_다음_op를_돈다(t *testing.T) {
	w := newWorld(t, true)
	var out bytes.Buffer
	plan := hook.Plan{Steps: []hook.Step{step("bogus-1", host.Request{}), step("bogus-2", host.Request{}),
		step(host.OpContext, host.Request{Audience: host.AudienceMain})}}
	w.runner(nil).Run(fake{plan: plan}, hook.SessionStart, nil, &out)
	if f := w.failed(); !slices.Equal(f, []string{"bogus-1:usage", "bogus-2:usage"}) {
		t.Fatalf("failed %v", f)
	}
	if !strings.Contains(out.String(), "답은 존댓말") {
		t.Fatalf("output %q", out.String())
	}
}

// 안내 줄은 세션 묶음을 새로 만들 때만 만들어져 묶음과 함께 저장된다(설계 보완 T-20): 만들개는 사건과 훅 입력의
// 날 cwd를 받고, 같은 세션을 다시 열면(resume) 불리지 않고 바이트 같은 출력이다. reset 뒤에는 다시 불린다.
func TestRunner_안내_줄은_묶음을_만들_때만_불리고_날_cwd를_받는다(t *testing.T) {
	w := newWorld(t, true)
	var out bytes.Buffer
	var got []string
	n := 0
	notes := func(ev hook.Event, p hook.Plan) []string {
		got = append(got, string(ev)+" "+p.Cwd)
		n++
		return []string{"안내 " + strconv.Itoa(n)}
	}
	open := hook.Plan{Cwd: "/raw/cwd", Steps: []hook.Step{step(host.OpContext, host.Request{Source: host.SourceStartup, Audience: host.AudienceMain})}}
	w.runner(notes).Run(fake{plan: open}, hook.SessionStart, nil, &out)
	first := out.String()
	if !strings.Contains(first, "답은 존댓말") || !strings.HasSuffix(first, "\n안내 1\n") || !slices.Equal(got, []string{"session-start /raw/cwd"}) {
		t.Fatalf("output %q notes %v", first, got)
	}
	out.Reset()
	resume := open
	resume.Steps = []hook.Step{step(host.OpContext, host.Request{Source: host.SourceResume, Audience: host.AudienceMain})}
	w.runner(notes).Run(fake{plan: resume}, hook.SessionStart, nil, &out)
	if out.String() != first || len(got) != 1 {
		t.Fatalf("resume %q notes %v", out.String(), got)
	}
	out.Reset()
	w.runner(notes).Run(fake{plan: startPlan()}, hook.SessionStart, nil, &out) // reset + compact
	if !strings.HasSuffix(out.String(), "\n안내 2\n") || strings.Contains(out.String(), "안내 1") || len(got) != 2 {
		t.Fatalf("after reset %q notes %v", out.String(), got)
	}
	// 세션 묶음이 아닌 문맥(도구 훅)에는 안내 줄이 없고 만들개도 불리지 않는다.
	out.Reset()
	w.runner(notes).Run(fake{plan: hook.Plan{Steps: []hook.Step{step(host.OpToolContext, host.Request{Tool: "Bash"})}}}, hook.PreTool, nil, &out)
	if strings.Contains(out.String(), "안내") || len(got) != 2 {
		t.Fatalf("pre-tool %q notes %v", out.String(), got)
	}
}

// 읽지 못하는 DB(없음)·결과가 문맥이 아닌 op·오류에서는 안내 줄도 붙지 않고 아무것도 내지 않는다.
func TestRunner_DB가_없으면_아무것도_내지_않고_DB를_만들지_않는다(t *testing.T) {
	w := newWorld(t, false)
	var out bytes.Buffer
	called := false
	notes := func(hook.Event, hook.Plan) []string { called = true; return []string{"안내"} }
	for _, ev := range hook.Events {
		w.runner(notes).Run(fake{plan: startPlan()}, ev, nil, &out)
	}
	if out.Len() != 0 || called {
		t.Fatalf("output %q note %v", out.String(), called)
	}
	if _, err := os.Stat(w.db); !os.IsNotExist(err) {
		t.Fatalf("DB made: %v", err)
	}
	// reset은 DB가 없으면 오류다(a3): 실패로 알리고 다음 op를 돈다.
	if f := w.failed(); len(f) == 0 || f[0] != "reset:no-db" {
		t.Fatalf("failed %v", f)
	}
}

func TestRunner_결과가_문맥이_아닌_op는_아무것도_내지_않는다(t *testing.T) {
	w := newWorld(t, true)
	var out bytes.Buffer
	w.runner(func(hook.Event, hook.Plan) []string { return []string{"안내"} }).Run(
		fake{plan: hook.Plan{Steps: []hook.Step{step(host.OpSessionEnd, host.Request{})}}}, hook.SessionEnd, nil, &out)
	if out.Len() != 0 {
		t.Fatalf("output %q", out.String())
	}
}

func TestRunner_시간_한도를_넘기면_빈_출력이고_실패로_알린다(t *testing.T) {
	w := newWorld(t, true)
	w.cfg.HostTimeouts = map[string]string{"context": "1ns", "reset": "1ns"}
	var out bytes.Buffer
	start := time.Now()
	w.runner(func(hook.Event, hook.Plan) []string { return []string{"안내"} }).Run(fake{plan: startPlan()}, hook.SessionStart, nil, &out)
	if out.Len() != 0 {
		t.Fatalf("output %q", out.String())
	}
	if f := w.failed(); !slices.Equal(f, []string{"reset:timeout", "context:timeout"}) {
		t.Fatalf("failed %v", f)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestRunner_어댑터가_panic해도_아무것도_내지_않고_panic을_넘기지_않는다(t *testing.T) {
	w := newWorld(t, true)
	var out bytes.Buffer
	w.runner(nil).Run(fake{panicPlan: true}, hook.Prompt, nil, &out)
	if out.Len() != 0 {
		t.Fatalf("output %q", out.String())
	}
	if f := w.failed(); !slices.Equal(f, []string{":internal"}) {
		t.Fatalf("failed %v", f)
	}
}

// 안내 줄 만들개가 panic하면 안내 줄만 빠지고 핵심 묶음은 그대로 나간다(원칙 6).
func TestRunner_안내_줄이_panic해도_핵심_문맥은_나간다(t *testing.T) {
	w := newWorld(t, true)
	var out bytes.Buffer
	w.runner(func(hook.Event, hook.Plan) []string { panic("note") }).Run(fake{plan: startPlan()}, hook.SessionStart, nil, &out)
	if !strings.Contains(out.String(), "답은 존댓말") || len(w.failed()) != 0 {
		t.Fatalf("output after a note panic %q failed %v", out.String(), w.failed())
	}
}

func TestRunner_서비스가_없거나_op가_없어도_아무것도_내지_않는다(t *testing.T) {
	var out bytes.Buffer
	hook.Runner{}.Run(fake{plan: startPlan()}, hook.SessionStart, nil, &out)
	newWorld(t, true).runner(nil).Run(fake{}, hook.SessionStart, nil, &out)
	if out.Len() != 0 {
		t.Fatalf("output %q", out.String())
	}
}

type blocked struct{ done chan struct{} }

func (b blocked) Read(p []byte) (int, error) { <-b.done; return 0, io.EOF }

type panicking struct{}

func (panicking) Read(p []byte) (int, error) { panic("read") }

func TestReadInput_끝나지_않는_입력은_기다림_한도에서_포기한다(t *testing.T) {
	b, err := hook.ReadInput(strings.NewReader(`{"a":1}`), 1<<20, time.Second)
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("read %q %v", b, err)
	}
	if b, err := hook.ReadInput(strings.NewReader("abcdef"), 3, time.Second); err != nil || string(b) != "abc" {
		t.Fatalf("limit %q %v", b, err)
	}
	never := blocked{done: make(chan struct{})}
	defer close(never.done)
	start := time.Now()
	if _, err := hook.ReadInput(never, 1<<20, 50*time.Millisecond); err != hook.ErrInputWait {
		t.Fatalf("a reader that never ends: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
	// 읽다가 panic하는 입력은 읽지 못한 입력이다(그 고루틴의 panic이 프로세스를 끝내지 않는다).
	if b, err := hook.ReadInput(panicking{}, 1<<20, time.Second); err == nil || err == hook.ErrInputWait || b != nil {
		t.Fatalf("a reader that panics: %q %v", b, err)
	}
}

func TestParseEvent(t *testing.T) {
	for _, ev := range hook.Events {
		if got, ok := hook.ParseEvent(string(ev)); !ok || got != ev {
			t.Fatalf("%s: %v %v", ev, got, ok)
		}
	}
	for _, bad := range []string{"", "work", "SessionStart", "bogus"} {
		if _, ok := hook.ParseEvent(bad); ok {
			t.Fatalf("%q read as an event", bad)
		}
	}
}
