package main

// 수용 기준 L-4(설계 11절): 훅이 모델에 내는 글은 호스트와 무관하게 상한 안이다. 큰 가짜 기억을 DB에 쌓고 훅 출력 경로
// (cmd/orbit-mem hook → 어댑터 Output)의 additionalContext 글자 수를 두 호스트 모두 센다. 글이 비어 있으면 시험이
// 아무것도 보지 못한 것이라 비어 있지 않음도 확인한다.

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestL4_훅_글_상한은_설계_값이다(t *testing.T) {
	for name, c := range map[string]struct{ got, want int }{
		"요청 훅":      {inject.MaxHookRunes, 2500},
		"도구 훅":      {inject.MaxToolRunes, 2500},
		"서브에이전트 시작": {inject.MaxSubagentRunes, 3000},
		"세션 시작":     {inject.MaxSessionStartRunes, 6000},
	} {
		if c.got != c.want {
			t.Errorf("%s 상한 %d (설계 %d)", name, c.got, c.want)
		}
	}
}

func TestL4_훅_글은_두_호스트_모두_상한_안이다(t *testing.T) {
	home(t)
	s := seed(t)
	long := strings.Repeat("배포 스모크 시험 드라이런 점검 본문 ", 40)
	for i := 0; i < 3; i++ {
		pinSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: fmt.Sprintf("핵심 규칙 %d 배포 스모크 시험", i), Body: "핵심 " + string([]rune(long)[:200]), Topic: fmt.Sprintf("핵심%d", i),
			Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}})
	}
	for i := 0; i < 120; i++ {
		if _, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: fmt.Sprintf("배포 스모크 시험 드라이런 규칙 %d", i), Body: long, Topic: fmt.Sprintf("배포%d", i),
			Tools: []string{"mcp__atlas__write_page"}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	const prompt = "배포 스모크 시험 드라이런 규칙"
	count := func(out, event string) int { return utf8.RuneCountInString(eventContext(t, out, event)) }
	type delegate struct{ tool, input string }
	delegations := map[string]delegate{
		"claude": {"Agent", `{"description":"배포 점검","prompt":"` + prompt + `","subagent_type":"worker"}`},
		"codex":  {"spawn_agent", `{"message":"` + prompt + `","agent_type":"worker"}`},
	}
	for _, hn := range []string{"claude", "codex"} {
		t.Run(hn, func(t *testing.T) {
			in := func(session, event, extra string) string {
				return `{"session_id":"` + session + `","cwd":"/tmp","hook_event_name":"` + event + `"` + extra + `}`
			}
			hk := func(name, stdin string) string {
				out, _, code := run(t, stdin, "hook", name, "--host", hn)
				if code != 0 {
					t.Fatalf("hook %s: exit %d", name, code)
				}
				return out
			}
			if n := count(hk("session-start", in(hn+"-ss", "SessionStart", `,"source":"startup"`)), "SessionStart"); n == 0 || n > 6000 {
				t.Errorf("세션 시작 %d자 (상한 6000)", n)
			}
			if n := count(hk("prompt", in(hn+"-pr", "UserPromptSubmit", `,"prompt":"`+prompt+`"`)), "UserPromptSubmit"); n == 0 || n > 2500 {
				t.Errorf("요청 %d자 (상한 2500)", n)
			}
			if n := count(hk("pre-tool", in(hn+"-pt", "PreToolUse", `,"tool_name":"mcp__atlas__write_page","tool_input":{}`)), "PreToolUse"); n == 0 || n > 2500 {
				t.Errorf("도구 %d자 (상한 2500)", n)
			}
			d := delegations[hn]
			sub := hn + "-sa"
			hk("pre-tool", in(sub, "PreToolUse", `,"tool_name":"`+d.tool+`","tool_input":`+d.input))
			if n := count(hk("subagent-start", in(sub, "SubagentStart", `,"agent_id":"a1","agent_type":"worker"`)), "SubagentStart"); n == 0 || n > 3000 {
				t.Errorf("서브에이전트 시작(위임 글 있음) %d자 (상한 3000)", n)
			}
			// 짝이 없는 서브에이전트(맞는 위임 글이 없음)는 역할 지침뿐이라 더 짧다.
			if n := count(hk("subagent-start", in(hn+"-sb", "SubagentStart", `,"agent_id":"a2","agent_type":"worker"`)), "SubagentStart"); n == 0 || n > 600 {
				t.Errorf("서브에이전트 시작(맞는 기억 없음) %d자 (상한 600)", n)
			}
		})
	}
}
