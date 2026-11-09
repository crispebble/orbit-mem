package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

type injected struct {
	Text      string   `json:"text"`
	MemoryIDs []string `json:"memoryIds"`
}

func runInject(t *testing.T, stdin string, args ...string) injected {
	t.Helper()
	out, errOut, code := run(t, stdin, append([]string{"inject"}, args...)...)
	var v injected
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
		t.Fatalf("inject %v: code %d out %q err %q", args, code, out, errOut)
	}
	return v
}

// 훅이 없는 호스트의 기억 원천: 세션 묶음과 요청별 기억을 JSON으로 주고 사용 사건을 --host 이름으로 남긴다.
func TestInject_훅_없는_호스트에_기억_문맥을_JSON으로_준다(t *testing.T) {
	home(t)
	s := seed(t)
	r := pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	d, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "atlas main 병합은 운영 배포", Body: "병합 전 확인", Source: memory.Source{Kind: "quote", Excerpt: "배포"}})

	start := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "pi-1", "--host", "host-x")
	if !strings.Contains(start.Text, "답은 존댓말") || !containsID(start.MemoryIDs, r.ID) {
		t.Errorf("세션 묶음 = %+v", start)
	}
	prompt := runInject(t, "atlas main 병합 배포", "prompt", "--cwd", "/tmp", "--session", "pi-1", "--host", "host-x")
	if !containsID(prompt.MemoryIDs, d.ID) {
		t.Errorf("요청별 기억 = %+v", prompt)
	}
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if len(evs) != 2 || evs[0].Host != "host-x" || evs[0].Session != "pi-1" || evs[0].Kind != memory.EventInjectSessionStart || evs[1].Kind != memory.EventInjectPrompt {
		t.Errorf("사건 = %+v", evs)
	}
}

func TestInject_DB가_없으면_빈_문맥이고_인자가_틀리면_사용법(t *testing.T) {
	home(t)
	if v := runInject(t, "", "session-start", "--cwd", "/tmp", "--host", "host-x"); v.Text != "" || len(v.MemoryIDs) != 0 || v.MemoryIDs == nil {
		t.Errorf("빈 DB = %+v", v)
	}
	if _, _, code := run(t, "", "inject", "bogus", "--host", "host-x"); code != 2 {
		t.Errorf("틀린 인자 code %d", code)
	}
}

// 기획 · orbit-mem M-3: inject·tool은 기본 호스트가 없다. --host가 빠지면 사용법(2)이다(주입 진단이 틀리지 않게).
func TestInject와_Tool은_host가_꼭_있어야_한다(t *testing.T) {
	home(t)
	seed(t)
	for _, args := range [][]string{
		{"inject", "session-start", "--cwd", "/tmp", "--session", "s"},
		{"inject", "prompt", "--session", "s"},
		{"inject", "delivered", "--session", "s", "--ids", "m-a"},
		{"tool", "memory_find", "--cwd", "/tmp"},
	} {
		if out, _, code := run(t, `{"query":"x"}`, args...); code != 2 || out != "" {
			t.Errorf("%v: code %d out %q", args, code, out)
		}
	}
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// --rules brief·full은 받기만 하고 효과가 없다(짧은 꼴은 없어졌다, T-27·D3): 세 꼴이 같은 묶음과 같은 회상이고,
// 세션 시작은 상시 핵심과 「먼저 찾는 곳」 줄만이다(S3-R6). 틀린 값은 지금처럼 사용법 오류다.
func TestInject_rules_brief는_받기만_하고_같은_글이다(t *testing.T) {
	home(t)
	s := seed(t)
	r, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말을 쓰지 않는다", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	brief := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "c-1", "--host", "host-x", "--rules", "brief")
	full := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "c-2", "--host", "host-x", "--rules", "full")
	plain := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "p-1", "--host", "host-x")
	if containsID(brief.MemoryIDs, r.ID) || !strings.Contains(brief.Text, "규칙·결정이 일에 닿으면 memory_find로 먼저 찾는다.") ||
		brief.Text != full.Text || brief.Text != plain.Text {
		t.Errorf("묶음 brief %q / full %q / 없음 %q", brief.Text, full.Text, plain.Text)
	}
	// 묶음 밖 확정 규칙은 요청과 맞으면 전문과 함께 회상된다(꼴과 상관없이).
	for _, rules := range []string{"brief", "full"} {
		p := runInject(t, "답은 존댓말 반말 쓰면 안돼?", "prompt", "--cwd", "/tmp", "--session", "c-"+rules, "--host", "host-x", "--rules", rules)
		if !containsID(p.MemoryIDs, r.ID) || !strings.Contains(p.Text, "반말을 쓰지 않는다") {
			t.Errorf("%s 회상 = %+v", rules, p)
		}
	}
	if _, _, c := run(t, "", "inject", "session-start", "--host", "host-x", "--rules", "tiny"); c != 2 {
		t.Errorf("틀린 꼴 code %d", c)
	}
	if _, _, c := run(t, "", "why", "m-x", "--rules", "tiny"); c != 2 {
		t.Errorf("why 틀린 꼴 code %d", c)
	}
}

// 호스트가 회상을 모델 요청에 실었다는 것(전달됨)을 남긴다. 「왜 안 나왔나」 진단이 읽는다.
func TestInject_delivered는_전달_사건을_남긴다(t *testing.T) {
	home(t)
	s := seed(t)
	if _, _, c := run(t, "", "inject", "delivered", "--host", "orbit-code", "--session", "c-1", "--ids", "m-a,m-b"); c != 0 {
		t.Fatalf("code %d", c)
	}
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: memory.EventInjectDelivered})
	if len(evs) != 1 || evs[0].Host != "orbit-code" || evs[0].Session != "c-1" || !strings.Contains(string(evs[0].Data), `"m-b"`) {
		t.Errorf("사건 = %+v", evs)
	}
	if _, _, c := run(t, "", "inject", "delivered", "--host", "orbit-code"); c != 2 {
		t.Errorf("세션·id 없이 code %d", c)
	}
}
