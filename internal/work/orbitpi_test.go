package work

import (
	"strings"
	"testing"
)

// orbit-pi 오케스트레이터(설계 · OpenOrbit 4차 R4-5)는 계획 단계를 목록 orbit-pi-plan으로 남기고,
// 실행·증거·판정에 단계 id(step)를 직접 준다. 병렬로 진행 중인 단계가 여럿이어도 정확히 붙어야 한다.

const pi = "orbit-pi"

func (w *tw) pi(session, kind string, payload any) Result { return w.ev(kind, pi, session, payload) }

func piPlan(w *tw, session, list string) {
	w.start(pi, session, SessionStart{Source: "startup", ListID: list, Launcher: pi})
	w.pi(session, KindTaskCreate, TaskCreate{List: list, ID: "a", Subject: "가 단계", Description: "가를 한다.\n둘째 줄"})
	w.pi(session, KindTaskCreate, TaskCreate{List: list, ID: "b", Subject: "나 단계", Description: "나를 한다"})
	w.pi(session, KindTaskCreate, TaskCreate{List: list, ID: "c", Subject: "다 단계"})
	w.pi(session, KindTaskUpdate, TaskUpdate{List: list, ID: "c", AddBlockedBy: []string{"a", "b"}})
}

func stepByNative(t *testing.T, doc Work, native string) Step {
	t.Helper()
	for _, s := range doc.Steps {
		if s.Native != nil && s.Native.ID == native {
			return s
		}
	}
	t.Fatalf("단계 %s가 없습니다", native)
	return Step{}
}

func TestOrbitPi_병렬_단계에_실행과_증거가_정확히_붙는다(t *testing.T) {
	w := newTW(t)
	piPlan(w, "P1", "L1")
	w.pi("P1", KindTaskUpdate, TaskUpdate{List: "L1", ID: "a", Status: str("in_progress")})
	w.pi("P1", KindTaskUpdate, TaskUpdate{List: "L1", ID: "b", Status: str("in_progress")})
	w.pi("P1", KindRunStart, RunStart{Agent: "ra", Type: "worker", Step: "a"})
	w.pi("P1", KindRunStart, RunStart{Agent: "rb", Type: "gate", Step: "b"})
	w.pi("P1", KindCommand, Command{Text: "go test ./...", OK: true, Step: "a"})
	usd := 0.42
	turns := 3
	cost := &Cost{Engine: "claude", Model: "sonnet", USD: &usd, InputTokens: 100, OutputTokens: 20, CacheReadTokens: 5, CacheWriteTokens: 1, Turns: &turns}
	w.pi("P1", KindRunStop, RunStop{Agent: "rb", Type: "gate", Step: "b", Cost: cost, Verdict: &Verdict{Role: "gate", Result: "pass"}})

	doc := w.only()
	if doc.Origin.Host != pi || doc.Origin.List.Kind != ListOrbitPiPlan || doc.Origin.List.ID != "L1" || doc.Origin.Launcher != pi {
		t.Fatalf("작업 출처: %+v", doc.Origin)
	}
	a, b, c := stepByNative(t, doc, "a"), stepByNative(t, doc, "b"), stepByNative(t, doc, "c")
	if a.Native.Kind != ListOrbitPiPlan {
		t.Errorf("단계 native 종류 %q", a.Native.Kind)
	}
	if len(c.After) != 2 || c.After[0].Source != "native" || c.After[1].Source != "native" || c.Status != "pending" || c.Wave != 1 {
		t.Errorf("c 선행: %+v %s %d", c.After, c.Status, c.Wave)
	}
	if a.Detail != "가를 한다.\n둘째 줄" || b.Detail != "나를 한다" || c.Detail != "" {
		t.Errorf("detail: %q %q %q", a.Detail, b.Detail, c.Detail)
	}
	runs := map[string]Run{}
	for _, r := range doc.Runs {
		runs[r.Role] = r
	}
	if r := runs["worker"]; r.StepID != a.ID || r.Link != "step" || r.Host != pi || r.Cost != nil {
		t.Errorf("a 실행: %+v", r)
	}
	r := runs["gate"]
	if r.StepID != b.ID || r.Link != "step" || r.State != "finished" {
		t.Errorf("b 실행: %+v", r)
	}
	if r.Cost == nil || r.Cost.Engine != "claude" || r.Cost.Model != "sonnet" || r.Cost.USD == nil || *r.Cost.USD != 0.42 ||
		r.Cost.InputTokens != 100 || r.Cost.OutputTokens != 20 || r.Cost.CacheReadTokens != 5 || r.Cost.CacheWriteTokens != 1 || r.Cost.Turns == nil || *r.Cost.Turns != 3 {
		t.Errorf("b 비용: %+v", r.Cost)
	}
	if a.Verification.State != "pass" || len(a.EvidenceIDs) != 1 {
		t.Errorf("a 검증: %+v %v", a.Verification, a.EvidenceIDs)
	}
	if b.Verification.State != "pass" || len(b.EvidenceIDs) != 1 {
		t.Errorf("b 판정: %+v %v", b.Verification, b.EvidenceIDs)
	}
	for _, e := range doc.Evidence {
		if (e.Kind == "command" && e.StepID != a.ID) || (e.Kind == "verdict" && e.StepID != b.ID) {
			t.Errorf("증거가 다른 단계에 붙었습니다: %+v", e)
		}
	}
	// 세션 쪽의 실행에도 비용이 있다
	d, err := w.s.SessionEvents("P1", "", 0, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range d.Runs {
		if x.Role == "gate" && x.Cost != nil && x.Cost.Model == "sonnet" {
			found = true
		}
	}
	if !found {
		t.Errorf("세션 실행에 비용이 없습니다: %+v", d.Runs)
	}
}

// 구독 엔진은 usd를 주지 않는다. 비용 객체는 남되 usd는 없다.
func TestOrbitPi_구독_비용은_usd가_없다(t *testing.T) {
	w := newTW(t)
	piPlan(w, "P1", "L1")
	w.pi("P1", KindRunStop, RunStop{Agent: "r1", Type: "worker", Step: "a", Cost: &Cost{Engine: "codex", Model: "gpt", InputTokens: 7}})
	doc := w.only()
	if len(doc.Runs) != 1 || doc.Runs[0].Cost == nil || doc.Runs[0].Cost.USD != nil || doc.Runs[0].Cost.Turns != nil || doc.Runs[0].StepID != stepByNative(t, doc, "a").ID {
		t.Fatalf("실행: %+v", doc.Runs)
	}
}

// 끝 사건이 먼저 오고 시작이 뒤에 와도 단계는 한 번 정해진 대로다. 모르는 단계 id는 잇지 않는다.
func TestOrbitPi_단계_id가_없거나_모르면_잇지_않는다(t *testing.T) {
	w := newTW(t)
	piPlan(w, "P1", "L1")
	w.pi("P1", KindTaskUpdate, TaskUpdate{List: "L1", ID: "a", Status: str("in_progress")})
	w.pi("P1", KindRunStart, RunStart{Agent: "rx", Type: "worker", Step: "zz"})
	w.pi("P1", KindCommand, Command{Text: "go test", OK: false, Step: "zz"})
	doc := w.only()
	if len(doc.Runs) != 0 || len(doc.Evidence) != 0 {
		t.Fatalf("모르는 단계에 붙었습니다: %+v %+v", doc.Runs, doc.Evidence)
	}
}

func TestOrbitPi_다른_세션이_같은_목록으로_재개한다(t *testing.T) {
	w := newTW(t)
	piPlan(w, "P1", "L1")
	w.pi("P1", KindTaskUpdate, TaskUpdate{List: "L1", ID: "a", Status: str("completed")})
	w.pi("P1", KindTaskUpdate, TaskUpdate{List: "L1", ID: "b", Status: str("completed")})
	first := w.only()
	w.pi("P1", KindSessionEnd, SessionEnd{Reason: "exit"})
	w.start(pi, "P2", SessionStart{Source: "resume", ListID: "L1", Launcher: pi})
	// list를 비우면 그 세션의 목록 id
	w.pi("P2", KindTaskUpdate, TaskUpdate{ID: "c", Status: str("in_progress")})
	w.pi("P2", KindRunStart, RunStart{Agent: "rc", Type: "worker", Step: "c"})
	w.pi("P2", KindTaskUpdate, TaskUpdate{List: "L1", ID: "c", Status: str("completed")})
	page, _ := w.s.ListWorks(Filter{}, ReadOptions{})
	if len(page.Items) != 1 || page.Items[0].ID != first.ID {
		t.Fatalf("재개가 새 작업을 열었습니다: %+v", page.Items)
	}
	doc, _, _ := w.s.GetWork(first.ID, false, ReadOptions{})
	c := stepByNative(t, doc, "c")
	if c.Status != "done" || doc.State != "done" || len(doc.Steps) != 3 {
		t.Errorf("재개 뒤: %s %s %d", c.Status, doc.State, len(doc.Steps))
	}
	if len(doc.Runs) != 1 || doc.Runs[0].StepID != c.ID || doc.Runs[0].SessionID != "P2" {
		t.Errorf("재개 세션 실행: %+v", doc.Runs)
	}
}

func TestOrbitPi_자식_claude_세션이_단계에_이어진다(t *testing.T) {
	w := newTW(t)
	piPlan(w, "P1", "L1")
	w.pi("P1", KindTaskUpdate, TaskUpdate{List: "L1", ID: "a", Status: str("in_progress")})
	w.pi("P1", KindTaskUpdate, TaskUpdate{List: "L1", ID: "b", Status: str("in_progress")})
	w.start("claude", "C1", SessionStart{Surface: "headless", Launcher: pi, Parent: &ParentRef{Host: pi, Session: "P1", Step: "b"}})
	w.ev(KindTaskCreate, "claude", "C1", TaskCreate{List: "C1", ID: "1", Subject: "하위 일"})
	page, _ := w.s.ListWorks(Filter{}, ReadOptions{})
	if len(page.Items) != 2 {
		t.Fatalf("작업 %d", len(page.Items))
	}
	var parent Work
	for _, it := range page.Items {
		if it.Parent == nil {
			parent, _, _ = w.s.GetWork(it.ID, true, ReadOptions{})
		}
	}
	b := stepByNative(t, parent, "b")
	if len(b.ChildWorkIDs) != 1 || len(parent.Children) != 1 || parent.Children[0].Parent.StepID != b.ID {
		t.Fatalf("자식 작업: %+v", b.ChildWorkIDs)
	}
	if a := stepByNative(t, parent, "a"); len(a.ChildWorkIDs) != 0 {
		t.Errorf("다른 단계에 붙었습니다: %+v", a.ChildWorkIDs)
	}
}

func TestOrbitPi_detail은_비밀값을_가리고_자른다(t *testing.T) {
	w := newTW(t)
	w.start(pi, "P1", SessionStart{ListID: "L1"})
	secret := "ghp_" + strings.Repeat("A", 36)
	w.pi("P1", KindTaskCreate, TaskCreate{List: "L1", ID: "a", Subject: "가", Description: "토큰 " + secret + " " + strings.Repeat("가", 5000)})
	d := w.only().Steps[0].Detail
	if strings.Contains(d, secret) {
		t.Errorf("비밀값이 남았습니다")
	}
	if n := len([]rune(d)); n > DetailMax {
		t.Errorf("detail %d자, 상한 %d", n, DetailMax)
	}
}

func TestListWorks_launcher로_거른다(t *testing.T) {
	w := newTW(t)
	piPlan(w, "P1", "L1")
	w.start("claude", "S1", SessionStart{Launcher: "human"})
	w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "1", Subject: "사람 일"})
	for _, c := range []struct {
		launcher string
		want     int
	}{{"", 2}, {pi, 1}, {"human", 1}, {"studio", 0}} {
		p, err := w.s.ListWorks(Filter{Launcher: c.launcher}, ReadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Items) != c.want {
			t.Errorf("launcher %q: %d개, %d개여야 합니다", c.launcher, len(p.Items), c.want)
		}
		for _, it := range p.Items {
			if c.launcher != "" && it.Origin.Launcher != c.launcher {
				t.Errorf("launcher %q에 %q", c.launcher, it.Origin.Launcher)
			}
		}
	}
}

// 1.2 칸(detail·cost)이 생기기 전 판(3)의 DB도 원장에서 다시 만들어 칸을 채운다.
func TestOpenUpgradesV3KeepsData(t *testing.T) {
	w := newTW(t)
	piPlan(w, "P1", "L1")
	w.pi("P1", KindRunStop, RunStop{Agent: "r1", Type: "worker", Step: "a", Cost: &Cost{Engine: "claude", Model: "m", InputTokens: 1}})
	before := w.only()
	path := w.s.Path()
	for _, q := range []string{`ALTER TABLE steps DROP COLUMN detail`, `ALTER TABLE runs DROP COLUMN cost`, `PRAGMA user_version=3`} {
		if _, err := w.s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	w.s.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.s = s
	t.Cleanup(func() { s.Close() })
	after := w.only()
	if after.ID != before.ID || stepByNative(t, after, "a").Detail != "가를 한다.\n둘째 줄" || len(after.Runs) != 1 || after.Runs[0].Cost == nil {
		t.Errorf("다시 만든 그래프: %+v", after)
	}
}
