package codex

import (
	"github.com/crispebble/orbit-mem/internal/hook"
	"testing"

	"github.com/crispebble/orbit-mem/internal/hook/hooktest"
)

// 계약 시험(ORBIT-23 D5): Codex 훅 입력이 짝 파일의 요청(차례 포함)이 되고, 문맥이 지금과 같은 훅 출력 바이트가 된다.
func TestCodex_훅_입력은_짝_파일의_계약_요청이_된다(t *testing.T) {
	hooktest.Plan(t, New(), "codex", "")
}

// Codex는 CLAUDE_PROJECT_DIR를 주지 않는다: 세션 폴더는 훅 입력의 cwd다. 같은 프로세스에 그 변수가 있어도 읽지 않는다.
func TestCodex_세션_폴더는_입력_cwd다(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "/w/start")
	hooktest.Plan(t, New(), "codex", "")
}

func TestCodex_훅_출력은_지금과_같은_바이트다(t *testing.T) {
	hooktest.Output(t, New(), hooktest.Names)
}

// 위임 도구(spawn_agent) 직전: 도구 입력의 message(없거나 글이 아니면 task_name)와 agent_type을 tool-context의 task·agentKind로
// 옮긴다. 입력 칸은 Codex 소스 기준이다(실제 훅 입력은 확인하지 못했다).
const delegationTask = "결제 모듈 동시성 시험을 고친다. 경합을 실제로 일으키는 시험을 쓴다"

var delegations = []hooktest.Case{
	{Name: "pre-tool spawn_agent message", Event: hook.PreTool, Input: hooktest.In(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "spawn_agent",
		"tool_input": map[string]any{"message": delegationTask, "agent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "spawn_agent"}}}},
	{Name: "pre-tool spawn_agent task_name (v2)", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "spawn_agent",
		"tool_input": map[string]any{"task_name": delegationTask, "agent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "spawn_agent"}}}},
	{Name: "pre-tool spawn_agent items, no message text", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "spawn_agent",
		"tool_input": map[string]any{"message": []any{map[string]any{"type": "text"}}, "items": []any{}, "task_name": delegationTask, "agent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "spawn_agent"}}}},
	{Name: "pre-tool Agent", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"message": delegationTask, "agent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation"}}},
	// 종류가 없거나 비면 두 훅 모두 Codex의 기본 역할 이름(default)이다: spawn_agent는 agent_type을 다듬어 비면 역할 없음으로,
	// SubagentStart의 agent_type은 역할이 없으면 "default"로 채운다(openai/codex 8703d32 multi_agents/spawn.rs·hook_runtime.rs
	// subagent_hook_context, agent/role.rs DEFAULT_ROLE_NAME). 실제 훅 입력은 확인하지 못했다.
	{Name: "pre-tool spawn_agent without a type", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "spawn_agent",
		"tool_input": map[string]any{"message": delegationTask}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "spawn_agent", "agentKind": "default"}}}},
	{Name: "pre-tool spawn_agent blank type", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "spawn_agent",
		"tool_input": map[string]any{"message": delegationTask, "agent_type": "  "}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "spawn_agent", "agentKind": "default"}}}},
	{Name: "pre-tool spawn_agent padded type", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "spawn_agent",
		"tool_input": map[string]any{"message": delegationTask, "agent_type": " worker "}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "spawn_agent"}}}},
	{Name: "subagent-start without a type", Event: hook.SubagentStart, Input: hooktest.In(map[string]any{"hook_event_name": "SubagentStart", "agent_id": "a-1"}),
		Want: []hooktest.Want{{Pair: "context-subagent", Set: map[string]any{"agentKind": "default"}}}},
	{Name: "subagent-start blank type", Event: hook.SubagentStart, Input: hooktest.In(map[string]any{"agent_id": "a-1", "agent_type": " "}),
		Want: []hooktest.Want{{Pair: "context-subagent", Set: map[string]any{"agentKind": "default"}}}},
	{Name: "pre-tool another tool with a message", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Bash",
		"tool_input": map[string]any{"message": delegationTask, "agent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-bash"}}},
}

func TestCodex_위임_도구의_입력은_task와_agentKind가_된다(t *testing.T) {
	hooktest.Plan(t, New(), "codex", "", delegations...)
}
