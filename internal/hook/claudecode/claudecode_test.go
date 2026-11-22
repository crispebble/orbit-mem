package claudecode

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/hook/hooktest"
	"github.com/crispebble/orbit-mem/internal/host"
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
// 도구 입력의 글(toolInput)은 다른 도구처럼 host.ToolInputText다: 위임 도구는 문자열 값을 키 차례로 이어 붙인 것이다.
var delegations = []hooktest.Case{
	{Name: "pre-tool Agent", Event: hook.PreTool, Input: hooktest.In(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Agent", "tool_use_id": "tu-1",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"toolInput": delegationTask + " worker"}}}},
	{Name: "pre-tool Agent with a description", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"description": "동시성 시험 고치기", "prompt": delegationTask, "subagent_type": "worker", "model": "x"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"task": "동시성 시험 고치기\n" + delegationTask, "toolInput": "동시성 시험 고치기 x " + delegationTask + " worker"}}}},
	{Name: "pre-tool Agent without a type", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"prompt": delegationTask}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"agentKind": "general-purpose", "toolInput": delegationTask}}}},
	{Name: "pre-tool Task (old name)", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Task",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"tool": "Task", "toolInput": delegationTask + " worker"}}}},
	{Name: "pre-tool Agent without a prompt", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"task": nil, "agentKind": nil, "toolInput": "worker"}}}},
	// 서브에이전트 시작 쪽도 같은 규칙이다: 종류가 없으면 general-purpose로 맞춰 위임 도구 쪽 종류와 짝이 맞는다.
	{Name: "subagent-start without a type", Event: hook.SubagentStart, Input: hooktest.In(map[string]any{"hook_event_name": "SubagentStart", "agent_id": "a-1"}),
		Want: []hooktest.Want{{Pair: "context-subagent", Set: map[string]any{"agentKind": "general-purpose"}}}},
	{Name: "pre-tool Agent blank type", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Agent",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": " "}}),
		Want: []hooktest.Want{{Pair: "tool-context-delegation", Set: map[string]any{"agentKind": "general-purpose", "toolInput": delegationTask}}}},
	{Name: "pre-tool another tool with a prompt", Event: hook.PreTool, Input: hooktest.In(map[string]any{"tool_name": "Bash",
		"tool_input": map[string]any{"prompt": delegationTask, "subagent_type": "worker"}}),
		Want: []hooktest.Want{{Pair: "tool-context-bash"}}},
}

const delegationTask = "결제 모듈 동시성 시험을 고친다. 경합을 실제로 일으키는 시험을 쓴다"

func TestClaudeCode_위임_도구의_입력은_task와_agentKind가_된다(t *testing.T) {
	hooktest.Plan(t, New(env(nil)), "claude", "", delegations...)
}

// 도구 직전 회상의 도구 입력(명세 T-4): PreToolUse의 tool_input을 host.ToolInputText의 글로 toolInput에 싣는다. Bash는 명령,
// Edit·Read·Write는 file_path, 그 밖은 문자열 값을 이어 붙인 것이다. 입력이 없거나 그 칸이 없으면 칸이 없다(이름만으로 판정).
func TestClaudeAdapter_PreToolUse의_tool_input을_toolInput으로_싣는다(t *testing.T) {
	cases := []struct {
		name, tool string
		input      any
		want       string
	}{
		{"Bash 명령", "Bash", map[string]any{"command": "git commit -m x", "description": "커밋"}, "git commit -m x"},
		{"Edit 경로", "Edit", map[string]any{"file_path": "/w/app/package.json", "old_string": "a", "new_string": "b"}, "/w/app/package.json"},
		{"MCP 도구는 문자열 값", "mcp__docs__write_page", map[string]any{"space": "draft", "title": "배포 절차", "n": 3}, "draft 배포 절차"},
		{"입력이 없다", "Bash", nil, ""},
		{"명령 칸이 없다", "Bash", map[string]any{"description": "x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fields := map[string]any{"hook_event_name": "PreToolUse", "tool_name": c.tool}
			if c.input != nil {
				fields["tool_input"] = c.input
			}
			plan := New(env(nil)).Plan(hook.PreTool, []byte(hooktest.In(fields)))
			if len(plan.Steps) != 1 || plan.Steps[0].Op != host.OpToolContext {
				t.Fatalf("steps %+v", plan.Steps)
			}
			if got := plan.Steps[0].Request; got.ToolInput != c.want || got.Tool != c.tool {
				t.Fatalf("tool %q toolInput %q, want %q", got.Tool, got.ToolInput, c.want)
			}
		})
	}
}
