package mcpserver

import (
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/work"
)

// seedOrbitPi adds an orbit-pi plan: steps with detail, runs linked by step with cost (orbit.work 1.2).
func seedOrbitPi(t *testing.T, e workEnv) string {
	t.Helper()
	s, err := work.Open(e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 10, 29, 5, 0, 0, 0, time.UTC).UnixMilli()
	var opened string
	ev := func(kind string, p any) {
		at += 1000
		x := work.NewEvent(kind, work.HostOrbitPi, "P1", at, p)
		x.Cwd = e.cwd
		res, err := s.Append(x)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Opened) > 0 {
			opened = res.Opened[0]
		}
	}
	str := func(s string) *string { return &s }
	usd := 0.12
	ev(work.KindSessionStart, work.SessionStart{Source: "startup", ListID: "L1", Launcher: "orbit-pi"})
	ev(work.KindTaskCreate, work.TaskCreate{List: "L1", ID: "a", Subject: "가", Description: "가를 한다\n자세히"})
	ev(work.KindTaskCreate, work.TaskCreate{List: "L1", ID: "b", Subject: "나"})
	ev(work.KindTaskUpdate, work.TaskUpdate{List: "L1", ID: "b", AddBlockedBy: []string{"a"}})
	ev(work.KindTaskUpdate, work.TaskUpdate{List: "L1", ID: "a", Status: str("in_progress")})
	ev(work.KindRunStart, work.RunStart{Agent: "r1", Type: "worker", StepRef: work.StepRef{Step: "a"}})
	ev(work.KindRunStop, work.RunStop{Agent: "r1", Type: "worker", StepRef: work.StepRef{Step: "a"},
		Cost: &work.Cost{Engine: "claude", Model: "sonnet", USD: &usd, InputTokens: 10, OutputTokens: 2}, Verdict: &work.Verdict{Role: "worker", Result: "pass"}})
	return opened
}

func TestWork_orbit_pi_응답이_1_2_계약에_맞는다(t *testing.T) {
	e := newWorkEnv(t)
	seedWork(t, e)
	id := seedOrbitPi(t, e)
	cs := aiHost(t, e)
	g := validate(t, "work_get", callRaw(t, cs, "work_get", map[string]any{"workId": id}))
	if c := g["contract"].(map[string]any); c["minor"] != float64(2) {
		t.Errorf("계약 판: %v", c)
	}
	w := g["work"].(map[string]any)
	if w["steps"].([]any)[0].(map[string]any)["detail"] != "가를 한다\n자세히" {
		t.Errorf("detail: %v", w["steps"])
	}
	run := w["runs"].([]any)[0].(map[string]any)
	if run["link"] != "step" || run["cost"].(map[string]any)["usd"] != 0.12 {
		t.Errorf("실행: %v", run)
	}
	all := validate(t, "work_list", callRaw(t, cs, "work_list", nil))
	pi := validate(t, "work_list", callRaw(t, cs, "work_list", map[string]any{"launcher": "orbit-pi"}))
	if len(all["items"].([]any)) < 2 || len(pi["items"].([]any)) != 1 || pi["ignoredArgs"] != nil {
		t.Errorf("launcher 필터: 전체 %d, orbit-pi %v", len(all["items"].([]any)), pi)
	}
}
