package contract

import (
	"encoding/json"
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
	var answered Example
	for _, ex := range exs {
		if ex.Name == "decision_answer.ok" {
			answered = ex
		}
	}
	v, err := ResponseValidator(1, "decision_answer")
	if err != nil {
		t.Fatal(err)
	}
	card := func(m map[string]any) map[string]any { return m["decision"].(map[string]any) }
	cases := map[string]func(m map[string]any){
		"모르는 상태":        func(m map[string]any) { card(m)["state"] = "pending" },
		"답 없는 answered": func(m map[string]any) { delete(card(m), "answer") },
		"모르는 owner":     func(m map[string]any) { card(m)["owner"] = "openclaw" },
		"모르는 칸":         func(m map[string]any) { card(m)["extra"] = 1 },
		"초 없는 시각":       func(m map[string]any) { card(m)["createdAt"] = "2026-10-28T05:12:30Z" },
		"계약 이름":         func(m map[string]any) { m["contract"].(map[string]any)["name"] = "orbit.work" },
	}
	for name, mutate := range cases {
		var m map[string]any
		_ = json.Unmarshal(answered.Result, &m)
		mutate(m)
		b, _ := json.Marshal(m)
		if v.Validate(b) == nil {
			t.Errorf("%s: Schema가 거르지 않았습니다", name)
		}
	}
}
