package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// Orbit Code는 MCP 대신 `orbit-mem tool <이름>`으로 같은 기억 도구를 부른다(입력·출력은 MCP 도구와 같은 JSON).
func TestTool_Orbit_Code가_기억_도구를_CLI로_부른다(t *testing.T) {
	home(t)
	s := seed(t)
	args := []string{"--cwd", "/tmp", "--host", "orbit-code", "--session", "c-1"}
	out, errOut, code := run(t, `{"kind":"lesson","title":"codex exec는 stdin을 닫는다","source":{"kind":"session","ref":"orbit-code:c-1"}}`,
		append([]string{"tool", "memory_save"}, args...)...)
	var saved struct {
		Memory memory.Memory `json:"memory"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &saved) != nil || saved.Memory.State != memory.StateCandidate {
		t.Fatalf("save: code %d out %q err %q", code, out, errOut)
	}
	out, _, code = run(t, `{"query":"codex stdin"}`, append([]string{"tool", "memory_find"}, args...)...)
	if code != 0 || !strings.Contains(out, saved.Memory.ID) {
		t.Fatalf("find: %d %s", code, out)
	}
	out, _, code = run(t, `{"ids":["`+saved.Memory.ID+`"]}`, append([]string{"tool", "memory_get"}, args...)...)
	if code != 0 || !strings.Contains(out, "codex exec는 stdin을 닫는다") {
		t.Fatalf("get: %d %s", code, out)
	}
	// 사용자 말 인용이면 바로 확정이고, 쓰는 쪽은 AI라 사용자 말 기억은 사용자 말 없이 고치지 못한다.
	out, _, _ = run(t, `{"kind":"rule","appliesTo":"user","title":"보고는 한국어로","topic":"보고 언어","source":{"kind":"quote","excerpt":"보고는 한국어로 해"}}`, append([]string{"tool", "memory_save"}, args...)...)
	var q struct {
		Memory memory.Memory `json:"memory"`
	}
	_ = json.Unmarshal([]byte(out), &q)
	if q.Memory.State != memory.StateConfirmed {
		t.Fatalf("quote: %s", out)
	}
	if out, _, code := run(t, `{"id":"`+q.Memory.ID+`","title":"보고는 영어로"}`, append([]string{"tool", "memory_update"}, args...)...); code != 1 || !strings.Contains(out, `"error"`) {
		t.Fatalf("ai edited a quote: %d %s", code, out)
	}
	// 거부된 저장은 JSON 오류(종료 1)다.
	out, _, code = run(t, `{"kind":"fact","title":"키","body":"token: ghp_abcdefghijklmnopqrstuvwxyz0123","source":{"kind":"session"}}`, append([]string{"tool", "memory_save"}, args...)...)
	if code != 1 || !strings.Contains(out, `"error"`) {
		t.Fatalf("secret: %d %s", code, out)
	}
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: memory.EventToolPrefix})
	if len(evs) != 6 || evs[0].Host != "orbit-code" || evs[0].Session != "c-1" || evs[2].Kind != "tool.memory_get" || !strings.Contains(string(evs[2].Data), saved.Memory.ID) {
		t.Fatalf("events = %+v", evs)
	}
	if _, _, code := run(t, `{}`, "tool", "proposal_decide"); code != 2 {
		t.Fatalf("unknown tool code %d", code)
	}
}

// 「왜 안 나왔나」 진단: 여섯 단계를 JSON(파이프)이나 사람 글로 준다.
func TestWhy_기억이_안_나온_까닭을_단계로_보인다(t *testing.T) {
	home(t)
	s := seed(t)
	m, _ := s.Save(memory.SaveInput{Kind: "lesson", Title: "codex exec는 stdin을 닫는다", Source: memory.Source{Kind: "session"}})
	out, errOut, code := run(t, "", "why", m.ID, "codex", "설치", "방법", "--cwd", "/tmp")
	var d struct {
		StoppedAt  string `json:"stoppedAt"`
		LastProven string `json:"lastProven"`
		Stages     []struct {
			Name, Status, Code string
		} `json:"stages"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &d) != nil || d.StoppedAt != "selected" || len(d.Stages) != 6 || d.Stages[2].Code != "weak_match" {
		t.Fatalf("why: %d %s %s", code, out, errOut)
	}
	i18n.Set(i18n.KO)
	defer i18n.Set(i18n.EN)
	if got := whyText("weak_match", []string{"codex", "codex 설치 방법"}); !strings.Contains(got, "맞은 낱말 [codex] / 요청 낱말 [codex 설치 방법]") {
		t.Fatalf("ko: %s", got)
	}
	if _, _, code := run(t, "", "why"); code != 2 {
		t.Fatalf("no id code %d", code)
	}
}
