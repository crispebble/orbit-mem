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

func example(t *testing.T, name string) map[string]any {
	t.Helper()
	exs, _ := Examples(1)
	for _, ex := range exs {
		if ex.Name == name {
			var m map[string]any
			_ = json.Unmarshal(ex.Result, &m)
			return m
		}
	}
	t.Fatalf("예시 %s가 없습니다", name)
	return nil
}

// 스키마가 실제로 거르는지: 틀린 값을 넣으면 실패해야 한다.
func TestResponseSchemaRejects(t *testing.T) {
	v, err := ResponseValidator(1, "memory_graph")
	if err != nil {
		t.Fatal(err)
	}
	nodes := func(m map[string]any) []any { return m["nodes"].([]any) }
	edge := func(m map[string]any, kind string) map[string]any {
		for _, e := range m["edges"].([]any) {
			if e.(map[string]any)["kind"] == kind {
				return e.(map[string]any)
			}
		}
		t.Fatalf("%s 선이 없습니다", kind)
		return nil
	}
	group := func(m map[string]any, kind string) map[string]any {
		for _, g := range m["groups"].([]any) {
			if g.(map[string]any)["kind"] == kind {
				return g.(map[string]any)
			}
		}
		t.Fatalf("%s 묶음이 없습니다", kind)
		return nil
	}
	cases := map[string]func(m map[string]any){
		"모르는 기억 상태":    func(m map[string]any) { nodes(m)[0].(map[string]any)["state"] = "archived" },
		"모르는 범위":       func(m map[string]any) { nodes(m)[0].(map[string]any)["scope"] = "team" },
		"초 없는 시각":      func(m map[string]any) { nodes(m)[0].(map[string]any)["updatedAt"] = "2026-10-29T08:20:00Z" },
		"본문 칸":         func(m map[string]any) { nodes(m)[0].(map[string]any)["body"] = "…" },
		"방향 있는 연결":     func(m map[string]any) { edge(m, "link")["directed"] = true },
		"모르는 연결 종류":    func(m map[string]any) { edge(m, "link")["relation"] = "causes" },
		"모르는 만든 쪽":     func(m map[string]any) { edge(m, "link")["origin"] = "import" },
		"방향 없는 대체":     func(m map[string]any) { edge(m, "supersede")["directed"] = false },
		"소속 하나인 묶음":    func(m map[string]any) { g := group(m, "tag"); g["members"] = g["members"].([]any)[:1] },
		"호스트 없는 세션 묶음": func(m map[string]any) { delete(group(m, "session"), "host") },
		"상대경로 파일 묶음":   func(m map[string]any) { group(m, "file")["key"] = "AGENTS.md" },
		"truncated 없음": func(m map[string]any) { delete(m, "truncated") },
		"계약 이름":        func(m map[string]any) { m["contract"].(map[string]any)["name"] = "orbit.work" },
	}
	for name, mutate := range cases {
		m := example(t, "memory_graph.all")
		mutate(m)
		b, _ := json.Marshal(m)
		if v.Validate(b) == nil {
			t.Errorf("%s: Schema가 거르지 않았습니다", name)
		}
	}
}

// 설계 「판 규칙·오류」: 예시는 모든 관계 종류, 거르기, 주변 읽기, 잘림, 모르는 값, 오류를 한 번씩 담는다.
func TestExamplesCoverContract(t *testing.T) {
	exs, _ := Examples(1)
	seen := map[string]bool{}
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
			seen[key+"="+x] = true
		case bool:
			if x {
				seen[key+"=true"] = true
			}
		}
	}
	for _, ex := range exs {
		var m map[string]any
		_ = json.Unmarshal(ex.Result, &m)
		walk("", m)
		for k := range ex.Args {
			seen["arg:"+k] = true
		}
		if ex.IsError {
			seen["error:"+m["error"].(map[string]any)["code"].(string)] = true
		}
		if m["ignoredArgs"] != nil {
			seen["ignoredArgs"] = true
		}
		for _, n := range arr(m["nodes"]) {
			if strings.HasPrefix(n.(map[string]any)["scope"].(string), "project:") {
				seen["scope=project"] = true
			}
		}
	}
	want := []string{
		"kind=rule", "kind=fact", "kind=decision", "kind=lesson", "kind=journal",
		"state=candidate", "state=confirmed", "state=stale", "state=hidden", "scope=global", "scope=project",
		"kind=link", "kind=supersede", "kind=tag", "kind=session", "kind=file", "relation=related",
		"origin=ai", "origin=retro", "origin=user", "truncated=true", "ignoredArgs",
		"error:UNSUPPORTED_MAJOR", "error:INVALID_ARGUMENT", "error:NOT_FOUND",
		"arg:focus", "arg:depth", "arg:via", "arg:kinds", "arg:states", "arg:scopes", "arg:tags",
		"arg:updatedFrom", "arg:updatedTo", "arg:relations", "arg:limit", "arg:major",
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
	s, err := InputSchema(1, "memory_graph")
	if err != nil {
		t.Fatal(err)
	}
	if s["type"] != "object" {
		t.Error("type=object가 아닙니다")
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "$ref") {
		t.Error("$ref가 남았습니다")
	}
	if _, ok := s["properties"].(map[string]any)["major"]; !ok {
		t.Error("major 칸이 없습니다")
	}
}

// 예시 안과 예시끼리 값이 맞는지 본다. 읽는 쪽(Forma 카탈로그·Studio 시험)이 예시를 고정 데이터로 쓴다.
func TestExamplesConsistent(t *testing.T) {
	exs, _ := Examples(1)
	byID := map[string]string{}
	for _, ex := range exs {
		var m map[string]any
		_ = json.Unmarshal(ex.Result, &m)
		// ignoredArgs는 입력 Schema에 없는 칸 이름과 같다
		in, _ := InputSchema(1, ex.Tool)
		var unknown []string
		for k := range ex.Args {
			if _, ok := in["properties"].(map[string]any)[k]; !ok {
				unknown = append(unknown, k)
			}
		}
		sort.Strings(unknown)
		if ex.IsError {
			continue
		}
		if got := strs(m["ignoredArgs"]); strings.Join(got, ",") != strings.Join(unknown, ",") {
			t.Errorf("%s: ignoredArgs %v, 모르는 칸 %v", ex.Name, got, unknown)
		}
		nodes := map[string]map[string]any{}
		for _, x := range arr(m["nodes"]) {
			n := x.(map[string]any)
			id := n["id"].(string)
			nodes[id] = n
			// 같은 기억은 예시마다 같은 모양이다
			b, _ := json.Marshal(n)
			if prev, ok := byID[id]; ok && prev != string(b) {
				t.Errorf("%s: 기억 %s가 다른 예시와 다릅니다", ex.Name, id)
			}
			byID[id] = string(b)
		}
		// 노드는 최근에 고친 것부터
		var prev string
		for _, x := range arr(m["nodes"]) {
			u := x.(map[string]any)["updatedAt"].(string)
			if prev != "" && u > prev {
				t.Errorf("%s: 노드가 최근에 고친 순이 아닙니다", ex.Name)
			}
			prev = u
		}
		// 선·묶음은 응답 노드 안의 기억만 가리킨다
		for _, x := range arr(m["edges"]) {
			e := x.(map[string]any)
			for _, end := range []string{"from", "to"} {
				if _, ok := nodes[e[end].(string)]; !ok {
					t.Errorf("%s: 선 %s의 %s가 노드에 없습니다", ex.Name, e["id"], end)
				}
			}
			if e["kind"] == "link" && e["from"].(string) >= e["to"].(string) {
				t.Errorf("%s: 연결 %s의 양 끝이 id 순이 아닙니다", ex.Name, e["id"])
			}
			if e["kind"] == "supersede" && nodes[e["from"].(string)]["supersededBy"] != e["to"] {
				t.Errorf("%s: 대체 선 %s가 supersededBy와 다릅니다", ex.Name, e["id"])
			}
		}
		for _, x := range arr(m["groups"]) {
			g := x.(map[string]any)
			for _, id := range strs(g["members"]) {
				if _, ok := nodes[id]; !ok {
					t.Errorf("%s: 묶음 %s의 %s가 노드에 없습니다", ex.Name, g["key"], id)
				}
			}
		}
		// 대체된 기억과 대신하는 기억이 둘 다 있고 대체를 담기로 했으면 대체 선이 있다
		wantSup := 0
		for _, n := range nodes {
			if to, ok := n["supersededBy"].(string); ok {
				if _, in := nodes[to]; in {
					wantSup++
				}
			}
		}
		rel := strs(ex.Args["relations"])
		if len(rel) == 0 || contains(rel, "supersede") {
			got := 0
			for _, x := range arr(m["edges"]) {
				if x.(map[string]any)["kind"] == "supersede" {
					got++
				}
			}
			if got != wantSup {
				t.Errorf("%s: 대체 선 %d개, 기대 %d개", ex.Name, got, wantSup)
			}
		}
		total := int(m["total"].(float64))
		if m["truncated"] == true {
			if len(nodes) >= total {
				t.Errorf("%s: truncated인데 노드 %d개, total %d", ex.Name, len(nodes), total)
			}
			if lim, ok := ex.Args["limit"].(float64); ok && len(nodes) > int(lim) {
				t.Errorf("%s: limit %v보다 노드가 많습니다", ex.Name, lim)
			}
		} else if len(nodes) != total {
			t.Errorf("%s: 잘리지 않았는데 노드 %d개, total %d", ex.Name, len(nodes), total)
		}
		if f, ok := ex.Args["focus"].(string); ok {
			if m["focus"] != f {
				t.Errorf("%s: focus를 돌려주지 않았습니다", ex.Name)
			}
			if _, ok := nodes[f]; !ok {
				t.Errorf("%s: focus 기억이 노드에 없습니다", ex.Name)
			}
		}
	}
}

func arr(v any) []any {
	a, _ := v.([]any)
	return a
}

func strs(v any) []string {
	var out []string
	for _, x := range arr(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
