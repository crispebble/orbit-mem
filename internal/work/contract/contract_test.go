package contract

import (
	"encoding/json"
	"sort"
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
			// 1.2: 단계 detail, 실행 cost(usd가 있는 것·구독)
			if _, ok := x["detail"]; ok && x["verification"] != nil {
				mark("step.detail")
			}
			if c, ok := x["cost"].(map[string]any); ok {
				if _, usd := c["usd"]; usd {
					mark("cost:usd")
				} else {
					mark("cost:subscription")
				}
			}
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
		// 1.2
		"kind=orbit-pi-plan", "host=orbit-pi", "link=step", "step.detail", "cost:usd", "cost:subscription",
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

// 예시끼리, 예시 안에서 값이 맞는지 본다. 읽는 쪽이 예시를 고정 데이터로 쓰므로
// 목록과 상세가 다르거나 파생 값이 규칙과 다르면 읽는 쪽 시험이 엉뚱하게 깨진다.
func TestExamplesConsistent(t *testing.T) {
	exs, _ := Examples(1)
	works := map[string]map[string]any{}
	var collect func(w map[string]any)
	collect = func(w map[string]any) {
		works[w["id"].(string)] = w
		for _, c := range arr(w["children"]) {
			collect(c.(map[string]any))
		}
	}
	sessions := map[string]map[string]any{}
	for _, ex := range exs {
		var m map[string]any
		_ = json.Unmarshal(ex.Result, &m)
		if ex.Tool == "work_get" && !ex.IsError {
			collect(m["work"].(map[string]any))
		}
		if s, ok := m["session"].(map[string]any); ok {
			sessions[s["sessionId"].(string)] = s
		}
		// ignoredArgs는 입력 Schema에 없는 칸 이름과 같다
		in, _ := InputSchema(1, ex.Tool)
		var unknown []string
		for k := range ex.Args {
			if _, ok := in["properties"].(map[string]any)[k]; !ok {
				unknown = append(unknown, k)
			}
		}
		if got := strs(m["ignoredArgs"]); strings.Join(got, ",") != strings.Join(unknown, ",") {
			t.Errorf("%s: ignoredArgs %v, 모르는 칸 %v", ex.Name, got, unknown)
		}
	}
	for id, w := range works {
		checkWork(t, id, w)
	}
	for _, ex := range exs {
		var m map[string]any
		_ = json.Unmarshal(ex.Result, &m)
		if ex.Tool != "work_list" {
			goto sessionsList
		}
		for _, it := range arr(m["items"]) {
			s := it.(map[string]any)
			w, ok := works[s["id"].(string)]
			if !ok {
				continue
			}
			want := summaryOf(w)
			b1, _ := json.Marshal(want)
			b2, _ := json.Marshal(s)
			if string(b1) != string(b2) {
				t.Errorf("%s: 작업 %s 요약이 work_get과 다릅니다\n목록 %s\n상세 %s", ex.Name, s["id"], b2, b1)
			}
		}
	sessionsList:
		for _, it := range arr(m["sessions"]) {
			s := it.(map[string]any)
			if d, ok := sessions[s["sessionId"].(string)]; ok {
				b1, _ := json.Marshal(d)
				b2, _ := json.Marshal(s)
				if string(b1) != string(b2) {
					t.Errorf("%s: 세션 %s가 세션 상세와 다릅니다", ex.Name, s["sessionId"])
				}
			}
		}
	}
}

func checkWork(t *testing.T, id string, w map[string]any) {
	t.Helper()
	steps := map[string]map[string]any{}
	for _, s := range arr(w["steps"]) {
		st := s.(map[string]any)
		steps[st["id"].(string)] = st
	}
	counts := map[string]float64{}
	for _, st := range steps {
		counts[st["status"].(string)]++
	}
	for k, v := range w["counts"].(map[string]any) {
		if v.(float64) != counts[k] {
			t.Errorf("%s: counts.%s=%v, 단계 %v", id, k, v, counts[k])
		}
	}
	var wave func(id string, depth int) int
	wave = func(sid string, depth int) int {
		if depth > len(steps) {
			t.Fatalf("%s: 선행에 순환이 있습니다", id)
		}
		best := 0
		for _, a := range arr(steps[sid]["after"]) {
			if n := wave(a.(map[string]any)["stepId"].(string), depth+1) + 1; n > best {
				best = n
			}
		}
		return best
	}
	evs := map[string]map[string]any{}
	for _, e := range arr(w["evidence"]) {
		ev := e.(map[string]any)
		evs[ev["id"].(string)] = ev
	}
	for sid, st := range steps {
		if int(st["wave"].(float64)) != wave(sid, 0) {
			t.Errorf("%s/%s: wave %v, 가장 긴 선행 경로 %d", id, sid, st["wave"], wave(sid, 0))
		}
		open := false
		for _, a := range arr(st["after"]) {
			p := steps[a.(map[string]any)["stepId"].(string)]
			if p == nil {
				t.Errorf("%s/%s: 없는 선행", id, sid)
				continue
			}
			if s := p["status"]; s != "done" && s != "cancelled" {
				open = true
			}
		}
		if s := st["status"]; (s == "ready" && open) || (s == "pending" && !open) {
			t.Errorf("%s/%s: %s인데 끝나지 않은 선행=%v", id, sid, s, open)
		}
		// evidenceIds는 그 단계의 증거 id, 검증 결과는 파생 규칙대로
		var mine []map[string]any
		for _, e := range arr(w["evidence"]) {
			if ev := e.(map[string]any); ev["stepId"] == sid {
				mine = append(mine, ev)
			}
		}
		var ids []string
		for _, ev := range mine {
			ids = append(ids, ev["id"].(string))
		}
		if strings.Join(ids, ",") != strings.Join(strs(st["evidenceIds"]), ",") {
			t.Errorf("%s/%s: evidenceIds %v, 증거 %v", id, sid, st["evidenceIds"], ids)
		}
		state, chosen := "none", map[string]any(nil)
		for _, kind := range []string{"verdict", "command"} {
			for _, ev := range mine {
				if ev["kind"] == kind && (chosen == nil || ev["at"].(string) >= chosen["at"].(string)) {
					chosen = ev
				}
			}
			if chosen != nil {
				break
			}
		}
		outdated := false
		if chosen != nil {
			state = map[string]string{"pass": "pass", "ok": "pass", "fail": "fail", "needs_fix": "fail", "needs_user": "needs_user"}[chosen["result"].(string)]
			for _, ev := range mine {
				if ev["kind"] == "commit" && ev["at"].(string) > chosen["at"].(string) {
					outdated = true
				}
			}
		}
		v := st["verification"].(map[string]any)
		if v["state"] != state || v["outdated"] != outdated || (chosen != nil && v["evidenceId"] != chosen["id"]) {
			t.Errorf("%s/%s: verification %v, 규칙으로는 %s outdated=%v", id, sid, v, state, outdated)
		}
	}
	for _, r := range arr(w["runs"]) {
		if sid, ok := r.(map[string]any)["stepId"].(string); ok && steps[sid] == nil {
			t.Errorf("%s: 실행 %s가 없는 단계 %s에 이어졌습니다", id, r.(map[string]any)["id"], sid)
		}
	}
}

func summaryOf(w map[string]any) map[string]any {
	s := map[string]any{}
	for k, v := range w {
		if k != "steps" && k != "runs" && k != "evidence" && k != "children" {
			s[k] = v
		}
	}
	v := map[string]any{"none": 0.0, "pass": 0.0, "fail": 0.0, "needs_user": 0.0, "outdated": 0.0}
	for _, x := range arr(w["steps"]) {
		ver := x.(map[string]any)["verification"].(map[string]any)
		v[ver["state"].(string)] = v[ver["state"].(string)].(float64) + 1
		if ver["outdated"] == true {
			v["outdated"] = v["outdated"].(float64) + 1
		}
	}
	s["stepCount"] = float64(len(arr(w["steps"])))
	s["verification"] = v
	return s
}

func arr(v any) []any { a, _ := v.([]any); return a }

func strs(v any) []string {
	var out []string
	for _, x := range arr(v) {
		out = append(out, x.(string))
	}
	sort.Strings(out)
	return out
}
