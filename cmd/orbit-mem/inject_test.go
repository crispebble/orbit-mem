package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/memory"
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

// orbit-pi의 기억 원천: 세션 묶음과 요청별 기억을 JSON으로 주고 사용 사건을 orbit-pi 호스트로 남긴다.
func TestInject_orbit_pi에_기억_문맥을_JSON으로_준다(t *testing.T) {
	home(t)
	s := seed(t)
	r := pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	d, _ := s.Save(memory.SaveInput{Kind: "decision", Title: "atlas main 병합은 운영 배포", Body: "병합 전 확인", Source: memory.Source{Kind: "quote", Excerpt: "배포"}})

	start := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "pi-1")
	if !strings.Contains(start.Text, "답은 존댓말") || !containsID(start.MemoryIDs, r.ID) {
		t.Errorf("세션 묶음 = %+v", start)
	}
	prompt := runInject(t, "atlas main 병합 배포", "prompt", "--cwd", "/tmp", "--session", "pi-1")
	if !containsID(prompt.MemoryIDs, d.ID) {
		t.Errorf("요청별 기억 = %+v", prompt)
	}
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31"})
	if len(evs) != 2 || evs[0].Host != "orbit-pi" || evs[0].Session != "pi-1" || evs[0].Kind != memory.EventInjectSessionStart || evs[1].Kind != memory.EventInjectPrompt {
		t.Errorf("사건 = %+v", evs)
	}
}

func TestInject_DB가_없으면_빈_문맥이고_인자가_틀리면_사용법(t *testing.T) {
	home(t)
	if v := runInject(t, "", "session-start", "--cwd", "/tmp"); v.Text != "" || len(v.MemoryIDs) != 0 || v.MemoryIDs == nil {
		t.Errorf("빈 DB = %+v", v)
	}
	if _, _, code := run(t, "", "inject", "bogus"); code != 2 {
		t.Errorf("틀린 인자 code %d", code)
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

// Orbit Code는 짧은 꼴을 받는다. 세션 시작은 두 꼴 모두 상시 핵심과 「먼저 찾는 곳」 줄만이고(S3-R6), 짧은 꼴의 그 줄이 더 짧다.
func TestInject_orbit_code는_규칙을_짧은_꼴로_받는다(t *testing.T) {
	home(t)
	s := seed(t)
	r, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말을 쓰지 않는다", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	code := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "c-1", "--host", "orbit-code")
	if containsID(code.MemoryIDs, r.ID) || !strings.Contains(code.Text, "먼저 찾는 곳") || !strings.Contains(code.Text, "memory_find") {
		t.Errorf("orbit-code 묶음 = %+v", code)
	}
	full := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "c-2", "--host", "orbit-code", "--rules", "full")
	pi := runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "p-1")
	if !strings.Contains(full.Text, "무엇보다 먼저") || !strings.Contains(pi.Text, "무엇보다 먼저") || len(code.Text) >= len(full.Text) {
		t.Errorf("전문 꼴 = %q / %q", full.Text, pi.Text)
	}
	// 짧은 꼴에서는 요청과 맞는 확정 규칙이 전문과 함께 회상된다.
	p := runInject(t, "답은 존댓말 반말 쓰면 안돼?", "prompt", "--cwd", "/tmp", "--session", "c-1", "--host", "orbit-code")
	if !containsID(p.MemoryIDs, r.ID) || !strings.Contains(p.Text, "반말을 쓰지 않는다") {
		t.Errorf("짧은 꼴 회상 = %+v", p)
	}
	if _, _, c := run(t, "", "inject", "session-start", "--rules", "tiny"); c != 2 {
		t.Errorf("틀린 꼴 code %d", c)
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
