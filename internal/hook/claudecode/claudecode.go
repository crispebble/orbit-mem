// Package claudecode is the Claude Code adapter of the hooks (설계 · 지연 회상·호스트 계약 11절): it reads
// Claude Code's hook input into host contract requests and prints a context the way Claude Code reads
// it from a hook's stdout.
package claudecode

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/host"
)

// Host is the host name the requests carry (the records' key, as the hooks have always written it).
const Host = "claude"

// projectDirEnv is the folder the session started in, which Claude Code gives its hooks and its
// MCP servers alike.
const projectDirEnv = "CLAUDE_PROJECT_DIR"

// Adapter reads Claude Code's hook input and prints its hook output.
type Adapter struct {
	getenv func(string) string
}

// New makes the adapter. getenv reads the hook's environment (os.Getenv; tests give their own).
func New(getenv func(string) string) Adapter { return Adapter{getenv: getenv} }

// input is the part of Claude Code's hook input the hooks read.
type input struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`     // UserPromptSubmit
	Source    string `json:"source"`     // SessionStart: startup·resume·fork·clear·compact
	ToolName  string `json:"tool_name"`  // PreToolUse
	AgentID   string `json:"agent_id"`   // SubagentStart, 서브에이전트 안의 훅
	AgentType string `json:"agent_type"` // SubagentStart: 서브에이전트 종류
	// ToolInput is the tool's input (PreToolUse). Only a delegation tool's is read, never changed.
	ToolInput json.RawMessage `json:"tool_input"`
}

// delegationTools are Claude Code's tools that start a subagent: Agent, and Task, its name before 2.1.
var delegationTools = []string{"Agent", "Task"}

// defaultAgentType is the subagent Claude Code starts when the Agent tool names none.
const defaultAgentType = "general-purpose"

// agentKind is the kind both hooks put in a request, by one rule so a delegation and its subagent pair up:
// the type given, the default subagent when it is empty.
func agentKind(agentType string) string {
	if strings.TrimSpace(agentType) == "" {
		return defaultAgentType
	}
	return agentType
}

// delegation is the part of a delegation tool's input the hooks read (Claude Code hooks 문서, 2.1.319 확인).
type delegation struct {
	Prompt       string `json:"prompt"`
	Description  string `json:"description"`
	SubagentType string `json:"subagent_type"`
}

// delegate puts the task of a delegation tool call into the request: the description, then the prompt.
// A call without a prompt hands no task.
func delegate(req *host.Request, in input) {
	if !slices.Contains(delegationTools, in.ToolName) {
		return
	}
	var d delegation
	if json.Unmarshal(in.ToolInput, &d) != nil || strings.TrimSpace(d.Prompt) == "" {
		return
	}
	req.Task = d.Prompt
	if desc := strings.TrimSpace(d.Description); desc != "" && !strings.HasPrefix(d.Prompt, desc) {
		req.Task = desc + "\n" + d.Prompt
	}
	req.AgentKind = agentKind(d.SubagentType)
}

// sources are the session start sources the contract takes; another one is left out.
var sources = []string{host.SourceStartup, host.SourceResume, host.SourceFork, host.SourceCompact, host.SourceClear}

// Plan reads one hook input. The session folder is where the session started (CLAUDE_PROJECT_DIR),
// so recall looks from the same folder as the MCP server even after the session moves; without it,
// the input's cwd.
func (a Adapter) Plan(ev hook.Event, b []byte) hook.Plan {
	var in input
	_ = json.Unmarshal(b, &in) // 읽지 못하는 입력은 빈 입력이다
	dir := ""
	if a.getenv != nil {
		dir = a.getenv(projectDirEnv)
	}
	if dir == "" {
		dir = in.Cwd
	}
	req := host.Request{Contract: host.Contract, Host: Host, Session: in.SessionID, Agent: in.AgentID, Cwd: dir}
	return hook.Plan{Cwd: in.Cwd, Steps: steps(ev, req, in)}
}

func steps(ev hook.Event, req host.Request, in input) []hook.Step {
	switch ev {
	case hook.SessionStart:
		ctx := req
		ctx.Audience = host.AudienceMain
		if slices.Contains(sources, in.Source) {
			ctx.Source = in.Source
		}
		if in.Source == host.SourceCompact || in.Source == host.SourceClear { // 앞선 문맥이 없다: 표시를 먼저 비운다
			return []hook.Step{{Op: host.OpReset, Request: req}, {Op: host.OpContext, Request: ctx}}
		}
		return []hook.Step{{Op: host.OpContext, Request: ctx}}
	case hook.Prompt:
		req.Prompt = in.Prompt // 사람 글 후보는 prompt 칸이다. 이 훅은 예약 작업·서브에이전트 보고·다른 세션 메시지에도 돌고 입력에 출처 칸이 없어, 사람 요청인지는 recall op가 판정한다(origin은 비운다)
		return []hook.Step{{Op: host.OpRecall, Request: req}}
	case hook.PreTool:
		req.Tool = in.ToolName // 도구 입력은 바꾸지 않는다(위임 도구의 위임 글만 읽는다)
		delegate(&req, in)
		return []hook.Step{{Op: host.OpToolContext, Request: req}}
	case hook.SubagentStart: // 입력에 위임 글이 없다: 같은 세션·같은 종류의 먼저 온 위임 글과 짝을 맞춘다(host)
		req.Audience = host.AudienceSubagent
		req.AgentKind = agentKind(in.AgentType)
		return []hook.Step{{Op: host.OpContext, Request: req}}
	case hook.SessionEnd:
		return []hook.Step{{Op: host.OpSessionEnd, Request: req}}
	}
	return nil
}

// names are Claude Code's hook event names.
var names = map[hook.Event]string{
	hook.SessionStart:  "SessionStart",
	hook.Prompt:        "UserPromptSubmit",
	hook.PreTool:       "PreToolUse",
	hook.SubagentStart: "SubagentStart",
	hook.SessionEnd:    "SessionEnd",
}

// Output is the hookSpecificOutput Claude Code reads; an empty context prints nothing.
func (a Adapter) Output(ev hook.Event, text string) string {
	if text == "" {
		return ""
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": names[ev], "additionalContext": text}})
	return string(b)
}
