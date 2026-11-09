package claudecode

import (
	"github.com/crispebble/orbit-mem/internal/hook"
	"testing"

	"github.com/crispebble/orbit-mem/internal/hook/hooktest"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// 계약 시험(ORBIT-23 D5): Claude Code 훅 입력이 짝 파일의 요청(차례 포함)이 되고, 문맥이 지금과 같은 훅 출력 바이트가 된다.
func TestClaudeCode_훅_입력은_짝_파일의_계약_요청이_된다(t *testing.T) {
	hooktest.Plan(t, New(env(nil)), "claude", "")
}

// 세션 폴더는 Claude Code가 훅에 주는 CLAUDE_PROJECT_DIR(세션을 시작한 폴더)이고, 없을 때만 입력 cwd다(S3-R1).
// 안내 줄용 날 cwd(Plan.Cwd)는 입력 그대로다.
func TestClaudeCode_세션_폴더는_CLAUDE_PROJECT_DIR가_먼저다(t *testing.T) {
	hooktest.Plan(t, New(env(map[string]string{"CLAUDE_PROJECT_DIR": "/w/start"})), "claude", "/w/start")
}

func TestClaudeCode_훅_출력은_지금과_같은_바이트다(t *testing.T) {
	hooktest.Output(t, New(env(nil)), hooktest.Names)
}

// 위임 도구(Agent, 옛 이름 Task) 직전: 도구 입력의 위임 글(설명 + 프롬프트)과 서브에이전트 종류를 tool-context의 task·agentKind로
// 옮긴다(도구 입력은 바꾸지 않는다, 설계 4.1). 종류가 없으면 Claude Code의 기본 에이전트(general-purpose)다. 다른 도구는 옮기지 않는다.
var delegations = []hooktest.Case{
	{Name: "pre-tool Agent", Event: hook.PreTool, Input: hooktest.In(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Agent", "tool_use_id": "tu-1",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation"}}},
	{Name: "pre-tool Agent with a description", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"description": "동시성 시험 고치기", "prompt": delegationTask, "subagent_type": "worker", "model": "x"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"task": "동시성 시험 고치기\n" + delegationTask}}}},
	{Name: "pre-tool Agent without a type", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"prompt": delegationTask}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"agentKind": "general-purpose"}}}},
	{Name: "pre-tool Task (old name)", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Task",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "Task"}}}},
	{Name: "pre-tool Agent without a prompt", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"task": nil, "agentKind": nil}}}},
	// 서브에이전트 시작 쪽도 같은 규칙이다: 종류가 없으면 general-purpose로 맞춰 위임 도구 쪽 종류와 짝이 맞는다.
	{Name: "subagent-start without a type", Event: hook.SubagentStart, Input: hooktest.In(map[string]any{"hook_event_name": "SubagentStart", "agent_id": "a-1"}),
		Want: []hooktest.Want{{Pair: "context-subagent", Set: map[string]any{"agentKind": "general-purpose"}}}},
	{Name: "pre-tool Agent blank type", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": " "}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"agentKind": "general-purpose"}}}},
	{Name: "pre-tool another tool with a prompt", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Bash",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-bash"}}},
}

const delegationTask = "결제 모듈 동시성 시험을 고친다. 경합을 실제로 일으키는 시험을 쓴다"

func TestClaudeCode_위임_도구의_입력은_task와_agentKind가_된다(t *testing.T) {
	hooktest.Plan(t, New(env(nil)), "claude", "", delegations...)
}
