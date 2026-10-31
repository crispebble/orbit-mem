package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/work/contract"
)

type emitOut struct {
	Events  int      `json:"events"`
	WorkIDs []string `json:"workIds"`
}

func emit(t *testing.T, lines ...string) emitOut {
	t.Helper()
	out, errOut, code := run(t, strings.Join(lines, "\n")+"\n", "work", "emit")
	if code != 0 {
		t.Fatalf("emit 종료 %d: %s", code, errOut)
	}
	var o emitOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("emit 출력: %q", out)
	}
	return o
}

func line(kind, session string, data map[string]any) string {
	b, _ := json.Marshal(map[string]any{"kind": kind, "session": session, "cwd": "/tmp/orbit-pi-repo", "data": data})
	return string(b)
}

// contractDoc checks a CLI result against the orbit.work response schema and decodes it.
func contractDoc(t *testing.T, def, out string) map[string]any {
	t.Helper()
	v, err := contract.ResponseValidator(1, def)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate([]byte(out)); err != nil {
		t.Fatalf("%s 출력이 계약에 맞지 않습니다: %v\n%s", def, err, out)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	return m
}

func workGet(t *testing.T, id string, extra ...string) map[string]any {
	t.Helper()
	out, errOut, code := run(t, "", append([]string{"work", "get", id}, extra...)...)
	if code != 0 {
		t.Fatalf("work get 종료 %d: %s", code, errOut)
	}
	return contractDoc(t, "work_get", out)["work"].(map[string]any)
}

func stepOf(t *testing.T, w map[string]any, native string) map[string]any {
	t.Helper()
	for _, s := range w["steps"].([]any) {
		st := s.(map[string]any)
		if n, ok := st["native"].(map[string]any); ok && n["id"] == native {
			return st
		}
	}
	t.Fatalf("단계 %s가 없습니다", native)
	return nil
}

func piScenario(t *testing.T) string {
	t.Helper()
	o := emit(t,
		line("session.start", "P1", map[string]any{"listId": "L1", "source": "startup"}),
		line("task.create", "P1", map[string]any{"list": "L1", "id": "a", "subject": "가 단계", "description": "가를 한다\n자세히"}),
		line("task.create", "P1", map[string]any{"list": "L1", "id": "b", "subject": "나 단계", "description": "나를 한다"}),
		line("task.create", "P1", map[string]any{"list": "L1", "id": "c", "subject": "다 단계", "description": "다를 한다"}),
		line("task.update", "P1", map[string]any{"list": "L1", "id": "c", "addBlockedBy": []string{"a", "b"}}),
	)
	if o.Events != 5 || len(o.WorkIDs) != 1 {
		t.Fatalf("계획 기록: %+v", o)
	}
	id := o.WorkIDs[0]
	if o := emit(t, line("title.set", "P1", map[string]any{"work": id, "title": "오케스트라 시험"})); o.Events != 1 || len(o.WorkIDs) != 1 || o.WorkIDs[0] != id {
		t.Fatalf("제목: %+v", o)
	}
	emit(t,
		line("task.update", "P1", map[string]any{"list": "L1", "id": "a", "status": "in_progress"}),
		line("task.update", "P1", map[string]any{"list": "L1", "id": "b", "status": "in_progress"}),
		line("run.start", "P1", map[string]any{"agent": "ra", "type": "worker", "step": "a"}),
		line("run.start", "P1", map[string]any{"agent": "rb", "type": "gate", "step": "b"}),
		line("evidence.command", "P1", map[string]any{"text": "go test ./...", "ok": true, "agent": "ra", "step": "a"}),
		line("run.stop", "P1", map[string]any{"agent": "rb", "type": "gate", "step": "b", "verdict": map[string]any{"role": "gate", "result": "pass"},
			"cost": map[string]any{"engine": "claude", "model": "sonnet", "usd": 0.31, "inputTokens": 1200, "outputTokens": 300, "cacheReadTokens": 50, "cacheWriteTokens": 7, "turns": 4}}),
	)
	return id
}

func TestWorkEmit_병렬_단계_실행_증거_비용이_정확한_단계에_붙는다(t *testing.T) {
	home(t)
	id := piScenario(t)
	w := workGet(t, id)
	if w["title"] != "오케스트라 시험" {
		t.Errorf("제목 %v", w["title"])
	}
	origin := w["origin"].(map[string]any)
	if origin["host"] != "orbit-pi" || origin["launcher"] != "orbit-pi" || origin["list"].(map[string]any)["kind"] != "orbit-pi-plan" {
		t.Errorf("출처: %v", origin)
	}
	a, b, c := stepOf(t, w, "a"), stepOf(t, w, "b"), stepOf(t, w, "c")
	after := c["after"].([]any)
	if len(after) != 2 || after[0].(map[string]any)["source"] != "native" || after[1].(map[string]any)["source"] != "native" {
		t.Errorf("c 선행: %v", after)
	}
	ids := map[string]bool{a["id"].(string): true, b["id"].(string): true}
	for _, x := range after {
		if !ids[x.(map[string]any)["stepId"].(string)] {
			t.Errorf("c 선행이 a·b가 아닙니다: %v", after)
		}
	}
	if a["detail"] != "가를 한다\n자세히" || c["detail"] != "다를 한다" {
		t.Errorf("detail: %v / %v", a["detail"], c["detail"])
	}
	if v := a["verification"].(map[string]any); v["state"] != "pass" {
		t.Errorf("a 검증: %v", v)
	}
	if v := b["verification"].(map[string]any); v["state"] != "pass" {
		t.Errorf("b 판정: %v", v)
	}
	for _, e := range w["evidence"].([]any) {
		ev := e.(map[string]any)
		if (ev["kind"] == "command" && ev["stepId"] != a["id"]) || (ev["kind"] == "verdict" && ev["stepId"] != b["id"]) {
			t.Errorf("증거가 다른 단계에: %v", ev)
		}
	}
	var gate map[string]any
	for _, r := range w["runs"].([]any) {
		run := r.(map[string]any)
		want := a["id"]
		if run["role"] == "gate" {
			gate, want = run, b["id"]
		}
		if run["stepId"] != want || run["link"] != "step" {
			t.Errorf("실행 단계: %v", run)
		}
	}
	cost, _ := gate["cost"].(map[string]any)
	if cost == nil || cost["usd"] != 0.31 || cost["inputTokens"] != float64(1200) || cost["turns"] != float64(4) || cost["model"] != "sonnet" {
		t.Errorf("비용: %v", gate)
	}
}

func TestWorkEmit_다른_세션이_같은_목록으로_재개한다(t *testing.T) {
	home(t)
	id := piScenario(t)
	emit(t,
		line("task.update", "P1", map[string]any{"list": "L1", "id": "a", "status": "completed"}),
		line("task.update", "P1", map[string]any{"list": "L1", "id": "b", "status": "completed"}),
		line("session.end", "P1", map[string]any{"reason": "exit"}),
	)
	o := emit(t,
		line("session.start", "P2", map[string]any{"listId": "L1", "source": "resume"}),
		line("task.update", "P2", map[string]any{"list": "L1", "id": "c", "status": "in_progress"}),
		line("task.update", "P2", map[string]any{"list": "L1", "id": "c", "status": "completed"}),
	)
	if len(o.WorkIDs) != 1 || o.WorkIDs[0] != id {
		t.Fatalf("재개가 다른 작업을 건드렸습니다: %+v (원래 %s)", o, id)
	}
	if p := works(t); len(p.Items) != 1 {
		t.Fatalf("작업 %d개, 하나여야 합니다", len(p.Items))
	}
	w := workGet(t, id)
	if c := stepOf(t, w, "c"); c["status"] != "done" || w["state"] != "done" {
		t.Errorf("c %v, 작업 %v", c["status"], w["state"])
	}
}

// orbit-pi가 띄운 claude 하위 세션(ORBIT_PARENT=orbit-pi:<세션>:<단계>)의 작업은 그 단계의 자식이다.
func TestWorkEmit_ORBIT_PARENT로_띄운_claude_세션이_단계에_이어진다(t *testing.T) {
	home(t)
	id := piScenario(t)
	cwd := t.TempDir()
	t.Setenv("ORBIT_PARENT", "orbit-pi:P1:b")
	t.Setenv("CLAUDE_CODE_TASK_LIST_ID", "")
	for _, ev := range []string{"SessionStart.1", "PostToolUse.TaskCreate.1"} {
		which := "work"
		if ev == "SessionStart.1" {
			which = "session-start"
		}
		if _, errOut, code := run(t, withSession(t, probeInput(t, ev), "C1", cwd), "hook", "--host", "claude", which); code != 0 {
			t.Fatalf("훅 %s: %s", ev, errOut)
		}
	}
	w := workGet(t, id, "--children")
	b := stepOf(t, w, "b")
	kids := b["childWorkIds"].([]any)
	if len(kids) != 1 {
		t.Fatalf("b의 자식 작업: %v", kids)
	}
	if a := stepOf(t, w, "a"); len(a["childWorkIds"].([]any)) != 0 {
		t.Errorf("a에 붙었습니다")
	}
	child := w["children"].([]any)[0].(map[string]any)
	if child["parent"].(map[string]any)["stepId"] != b["id"] || child["origin"].(map[string]any)["host"] != "claude" {
		t.Errorf("자식 작업: %v", child)
	}
}

func TestWorkEmit_잘못된_줄이_있으면_아무것도_쓰지_않는다(t *testing.T) {
	home(t)
	good := line("session.start", "P1", map[string]any{"listId": "L1", "source": "startup"})
	for name, bad := range map[string]string{
		"모르는 kind": line("task.delete", "P1", map[string]any{"id": "a"}),
		"형이 틀림":    line("task.create", "P1", map[string]any{"list": "L1", "id": 3, "subject": "x"}),
		"필수 칸 없음":  line("run.start", "P1", map[string]any{"agent": "r", "type": "worker"}),
		"상태 값":     line("task.update", "P1", map[string]any{"list": "L1", "id": "a", "status": "doing"}),
		"판정 값":     line("run.stop", "P1", map[string]any{"agent": "r", "type": "gate", "step": "a", "verdict": map[string]any{"role": "gate", "result": "ok"}}),
		"ok 없음":    line("evidence.command", "P1", map[string]any{"text": "go test", "step": "a"}),
		"세션 없음":    `{"kind":"session.end","data":{}}`,
		"JSON 아님":  `{"kind":`,
		"source 값": line("session.start", "P1", map[string]any{"listId": "L1", "source": "clear"}),
		"at 형":     `{"kind":"session.end","session":"P1","at":"지금","data":{}}`,
		"비용 토큰 형":  line("run.stop", "P1", map[string]any{"agent": "r", "type": "gate", "step": "a", "cost": map[string]any{"engine": "e", "model": "m", "inputTokens": "많이"}}),
	} {
		out, errOut, code := run(t, good+"\n"+bad+"\n", "work", "emit")
		if code != 1 || strings.TrimSpace(out) != "" || !strings.Contains(errOut, "2") {
			t.Errorf("%s: 종료 %d, 출력 %q, 오류 %q", name, code, out, errOut)
		}
	}
	if _, err := os.Stat(paths().WorkDB()); err == nil {
		if p := works(t); len(p.Items) != 0 {
			t.Errorf("거부한 입력이 기록됐습니다")
		}
		out, _, _ := run(t, "", "work", "list")
		if n := len(contractDoc(t, "work_list", out)["items"].([]any)); n != 0 {
			t.Errorf("작업 %d개", n)
		}
	}
}

func TestWorkEmit_기록을_끄면_쓰지_않는다(t *testing.T) {
	h := home(t)
	cfg := config.Default()
	cfg.Work = config.WorkOff
	if err := config.Save(filepath.Join(h, "config.json"), cfg); err != nil {
		t.Fatal(err)
	}
	in := line("session.start", "P1", map[string]any{"listId": "L1", "source": "startup"}) + "\n" +
		line("task.create", "P1", map[string]any{"list": "L1", "id": "a", "subject": "가"}) + "\n"
	out, errOut, code := run(t, in, "work", "emit")
	var o struct {
		Events   int      `json:"events"`
		WorkIDs  []string `json:"workIds"`
		Disabled bool     `json:"disabled"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil || code != 0 {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if o.Events != 0 || o.WorkIDs == nil || len(o.WorkIDs) != 0 || !o.Disabled {
		t.Errorf("꺼진 기록: %s", out)
	}
	if !strings.Contains(errOut, "work") || !strings.Contains(errOut, "off") {
		t.Errorf("꺼졌다는 경고가 없습니다: %q", errOut)
	}
	// 켜져 있을 때는 disabled 칸이 없다
	cfg.Work = config.WorkOnValue
	_ = config.Save(filepath.Join(h, "config.json"), cfg)
	out, errOut, _ = run(t, in, "work", "emit")
	if strings.Contains(out, "disabled") || strings.TrimSpace(errOut) != "" {
		t.Errorf("켜진 기록: %q %q", out, errOut)
	}
}

func TestWorkEmit_같은_멱등_키는_한_번만(t *testing.T) {
	home(t)
	l := `{"kind":"session.start","session":"P1","key":"k1","data":{"listId":"L1","source":"startup"}}`
	if o := emit(t, l, l); o.Events != 1 {
		t.Errorf("기록 수 %d", o.Events)
	}
}

func TestWorkList_launcher와_cwd로_거른다(t *testing.T) {
	home(t)
	id := piScenario(t)
	cwd := t.TempDir()
	t.Setenv("ORBIT_PARENT", "")
	t.Setenv("CLAUDE_CODE_TASK_LIST_ID", "")
	for _, ev := range []string{"SessionStart.1", "PostToolUse.TaskCreate.1"} {
		which := map[bool]string{true: "session-start", false: "work"}[ev == "SessionStart.1"]
		run(t, withSession(t, probeInput(t, ev), "H1", cwd), "hook", "--host", "claude", which)
	}
	ids := func(args ...string) []string {
		t.Helper()
		out, errOut, code := run(t, "", append([]string{"work", "list"}, args...)...)
		if code != 0 {
			t.Fatalf("work list %v: %d %s", args, code, errOut)
		}
		var got []string
		for _, it := range contractDoc(t, "work_list", out)["items"].([]any) {
			got = append(got, it.(map[string]any)["id"].(string))
		}
		return got
	}
	if got := ids(); len(got) != 2 {
		t.Errorf("전체 %v", got)
	}
	if got := ids("--launcher", "orbit-pi"); len(got) != 1 || got[0] != id {
		t.Errorf("orbit-pi %v", got)
	}
	if got := ids("--cwd", "/tmp/orbit-pi-repo", "--state", "open", "--limit", "5"); len(got) != 1 || got[0] != id {
		t.Errorf("cwd %v", got)
	}
	if got := ids("--cwd", "/nowhere", "--cwd", "/tmp/orbit-pi-repo"); len(got) != 1 {
		t.Errorf("cwd 여럿 %v", got)
	}
	out, _, code := run(t, "", "work", "list", "--state", "half")
	if code != 1 || contractDoc(t, "error", out)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Errorf("틀린 state: %d %s", code, out)
	}
	out, _, code = run(t, "", "work", "get", "w-없음")
	if code != 1 || contractDoc(t, "error", out)["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Errorf("없는 작업: %d %s", code, out)
	}
	if _, _, code := run(t, "", "work"); code != 2 {
		t.Errorf("인자 없는 work: %d", code)
	}
}
