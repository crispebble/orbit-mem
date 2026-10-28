package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func responseDef(ex Example) string {
	if ex.IsError {
		return "error"
	}
	return ex.Tool
}

func TestExamplesMatchSchemas(t *testing.T) {
	exs, err := Examples(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(exs) == 0 {
		t.Fatal("예시가 없습니다")
	}
	for _, ex := range exs {
		v, err := ResponseValidator(1, responseDef(ex))
		if err != nil {
			t.Fatalf("%s: %v", ex.Name, err)
		}
		if err := v.Validate(ex.Result); err != nil {
			t.Errorf("%s 응답이 Schema에 맞지 않습니다: %v", ex.Name, err)
		}
		in, err := InputValidator(1, ex.Tool)
		if err != nil {
			t.Fatalf("%s: %v", ex.Name, err)
		}
		args, _ := json.Marshal(ex.Args)
		if err := in.Validate(args); err != nil && !ex.IsError {
			t.Errorf("%s 입력이 Schema에 맞지 않습니다: %v", ex.Name, err)
		}
	}
}

// 스키마가 실제로 거르는지: 틀린 값을 넣으면 실패해야 한다.
func TestResponseSchemaRejects(t *testing.T) {
	exs, _ := Examples(1)
	var get Example
	for _, ex := range exs {
		if ex.Name == "work_get.claude-task" {
			get = ex
		}
	}
	v, err := ResponseValidator(1, "work_get")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(m map[string]any){
		"모르는 상태값":       func(m map[string]any) { step(m, 0)["status"] = "paused" },
		"초 없는 시각":       func(m map[string]any) { work(m)["openedAt"] = "2026-10-28T05:12:30Z" },
		"UTC 아닌 시각":     func(m map[string]any) { work(m)["openedAt"] = "2026-10-28T14:12:30.123+09:00" },
		"막힘 없는 blocked": func(m map[string]any) { delete(step(m, 3), "block") },
		"막힘 있는 done": func(m map[string]any) {
			step(m, 0)["block"] = map[string]any{"reason": "stale", "since": "2026-10-28T05:12:30.123Z"}
		},
		"모르는 칸":     func(m map[string]any) { work(m)["extra"] = 1 },
		"계약 이름":     func(m map[string]any) { m["contract"].(map[string]any)["name"] = "orbit.tasks" },
		"commit 결과": func(m map[string]any) { work(m)["evidence"].([]any)[4].(map[string]any)["result"] = "pass" },
	}
	for name, mutate := range cases {
		var m map[string]any
		_ = json.Unmarshal(get.Result, &m)
		mutate(m)
		b, _ := json.Marshal(m)
		if v.Validate(b) == nil {
			t.Errorf("%s: Schema가 거르지 않았습니다", name)
		}
	}
}

func work(m map[string]any) map[string]any { return m["work"].(map[string]any) }
func step(m map[string]any, i int) map[string]any {
	return work(m)["steps"].([]any)[i].(map[string]any)
}

// 설계 「구현 순서」 1: 예시는 모든 단계 상태, 막힘 사유, 증거 종류, 세션 상태,
// reset·more·쪽 넘김 응답, titleSource 두 값을 한 번씩 담는다.
func TestExamplesCoverContract(t *testing.T) {
	exs, _ := Examples(1)
	seen := map[string]bool{}
	mark := func(k string) { seen[k] = true }
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, y := range x {
				walk(k, y)
			}
		case []any:
			for _, y := range x {
				walk(key, y)
			}
		case string:
			mark(key + "=" + x)
		case bool:
			if x {
				mark(key + "=true")
			}
		}
	}
	for _, ex := range exs {
		var m map[string]any
		_ = json.Unmarshal(ex.Result, &m)
		walk("", m)
		if m["nextCursor"] != nil {
			mark("page:" + ex.Tool + ":next")
		}
		if c, ok := ex.Args["cursor"].(string); ok && c != "" && m["nextCursor"] == nil {
			mark("page:" + ex.Tool + ":last")
		}
		if _, ok := m["session"]; ok {
			mark("sessionEvents")
		}
		if ex.IsError {
			mark("error:" + m["error"].(map[string]any)["code"].(string))
		}
		if len(m) > 0 && m["ignoredArgs"] != nil {
			mark("ignoredArgs")
		}
		if w, ok := m["work"].(map[string]any); ok && w["children"] != nil {
			mark("children")
		}
		if ex.Tool == "work_changes" && !ex.IsError {
			ids, _ := m["workIds"].([]any)
			if m["reset"] == false && m["more"] == false && len(ids) == 0 {
				mark("changes:empty")
			}
			if len(ids) > 0 && m["more"] == false {
				mark("changes:some")
			}
		}
	}
	want := []string{
		"titleSource=ai", "titleSource=step", "state=open", "state=done",
		"kind=claude-task", "kind=claude-todo", "kind=claude-app-plan", "kind=codex-plan",
		"surface=cli", "surface=desktop", "surface=headless", "surface=unknown",
		"source=native", "source=order", "link=owner", "link=single_active", "link=parent_env", "link=none",
		"state=running", "state=finished", "state=failed", "state=lost",
		"endReason=completed", "endReason=failed", "endReason=session_ended",
		"kind=command", "kind=commit", "kind=verdict",
		"result=ok", "result=fail", "result=pass", "result=needs_fix", "result=needs_user",
		"state=none", "state=pass", "state=fail", "state=needs_user", "outdated=true",
		"state=waiting_user", "state=idle", "state=stale", "state=ended", "endReason=session_end", "endReason=unknown",
		"reset=true", "more=true", "changes:empty", "changes:some",
		"page:work_list:next", "page:work_list:last", "page:work_sessions:next", "page:work_sessions:last", "sessionEvents",
		"error:UNSUPPORTED_MAJOR", "error:INVALID_ARGUMENT", "error:AMBIGUOUS_WORK", "error:NOT_FOUND",
		"ignoredArgs", "children",
	}
	for _, s := range []string{"pending", "ready", "in_progress", "blocked", "done", "cancelled"} {
		want = append(want, "status="+s)
	}
	for _, r := range []string{"waiting_user", "run_failed", "run_lost", "interrupted", "stale", "declared"} {
		want = append(want, "reason="+r)
	}
	for _, k := range []string{"session.started", "turn.started", "turn.ended", "step.changed", "run.started", "run.ended", "evidence.added", "block.opened", "block.cleared", "session.ended"} {
		want = append(want, "kind="+k)
	}
	var missing []string
	for _, w := range want {
		if !seen[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Errorf("예시에 없는 값: %s", strings.Join(missing, ", "))
	}
}

func TestInputSchemaInlined(t *testing.T) {
	for _, tool := range []string{"work_list", "work_get", "work_title", "work_sessions", "work_changes"} {
		s, err := InputSchema(1, tool)
		if err != nil {
			t.Fatal(err)
		}
		if s["type"] != "object" {
			t.Errorf("%s: type=object가 아닙니다", tool)
		}
		b, _ := json.Marshal(s)
		if strings.Contains(string(b), "$ref") {
			t.Errorf("%s: $ref가 남았습니다", tool)
		}
		if _, ok := s["properties"].(map[string]any)["major"]; !ok {
			t.Errorf("%s: major 칸이 없습니다", tool)
		}
	}
}
