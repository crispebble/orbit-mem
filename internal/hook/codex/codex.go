// Package codex is the Codex adapter of the hooks (설계 · 지연 회상·호스트 계약 11절): it reads Codex's
// hook input into host contract requests and prints a context the way Codex reads it from a hook's
// stdout.
package codex

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/host"
)

// Host is the host name the requests carry (the records' key, as the hooks have always written it).
const Host = "codex"

// Adapter reads Codex's hook input and prints its hook output.
type Adapter struct{}

// New makes the adapter.
func New() Adapter { return Adapter{} }

// input is the part of Codex's hook input the hooks read.
type input struct {
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	Prompt    string          `json:"prompt"`
	Source    string          `json:"source"`
	ToolName  string          `json:"tool_name"`
	AgentID   string          `json:"agent_id"`
	AgentType string          `json:"agent_type"` // SubagentStart
	ToolInput json.RawMessage `json:"tool_input"` // PreToolUse: 위임 도구의 것만 읽는다
}

// delegationTools are Codex's tools that start a subagent (Codex hooks 문서의 PreToolUse matcher).
var delegationTools = []string{"spawn_agent", "Agent"}

// delegation is the part of spawn_agent's input the hooks read. 소스 기준이고 실제 훅 입력은 확인하지 못했다:
// openai/codex 8703d32 codex-rs/core/src/tools/handlers/multi_agents/spawn.rs(v1: message·items·agent_type),
// multi_agents_v2/spawn.rs(v2: message·task_name·agent_type). message가 글이 아니면(항목 목록) task_name을 쓴다.
type delegation struct {
	Message   json.RawMessage `json:"message"`
	TaskName  string          `json:"task_name"`
	AgentType string          `json:"agent_type"`
}

// delegate puts the task of a delegation tool call into the request. A call without a text hands no task.
func delegate(req *host.Request, in input) {
	if !slices.Contains(delegationTools, in.ToolName) {
		return
	}
	var d delegation
	if json.Unmarshal(in.ToolInput, &d) != nil {
		return
	}
	var task string
	if json.Unmarshal(d.Message, &task) != nil || strings.TrimSpace(task) == "" {
		task = d.TaskName
	}
	if strings.TrimSpace(task) == "" {
		return
	}
	req.Task, req.AgentKind = task, d.AgentType
}

var sources = []string{host.SourceStartup, host.SourceResume, host.SourceFork, host.SourceCompact, host.SourceClear}

// Plan reads one hook input. Codex gives its hooks no start folder: the session folder is the
// input's cwd.
func (Adapter) Plan(ev hook.Event, b []byte) hook.Plan {
	var in input
	_ = json.Unmarshal(b, &in) // 읽지 못하는 입력은 빈 입력이다
	req := host.Request{Contract: host.Contract, Host: Host, Session: in.SessionID, Agent: in.AgentID, Cwd: in.Cwd}
	var steps []hook.Step
	switch ev {
	case hook.SessionStart:
		ctx := req
		ctx.Audience = host.AudienceMain
		if slices.Contains(sources, in.Source) {
			ctx.Source = in.Source
		}
		if in.Source == host.SourceCompact || in.Source == host.SourceClear { // 앞선 문맥이 없다: 표시를 먼저 비운다
			steps = append(steps, hook.Step{Op: host.OpReset, Request: req})
		}
		steps = append(steps, hook.Step{Op: host.OpContext, Request: ctx})
	case hook.Prompt:
		req.Prompt = in.Prompt // 사람 글 후보는 prompt 칸이다. 입력에 출처 칸이 없어 사람 요청인지는 recall op가 판정한다(origin은 비운다)
		steps = []hook.Step{{Op: host.OpRecall, Request: req}}
	case hook.PreTool:
		req.Tool = in.ToolName
		delegate(&req, in)
		steps = []hook.Step{{Op: host.OpToolContext, Request: req}}
	case hook.SubagentStart: // 서브에이전트 훅의 session_id는 부모 세션이다: 같은 세션·같은 종류의 위임 글과 짝을 맞춘다(host)
		req.Audience = host.AudienceSubagent
		req.AgentKind = in.AgentType
		steps = []hook.Step{{Op: host.OpContext, Request: req}}
	case hook.SessionEnd:
		steps = []hook.Step{{Op: host.OpSessionEnd, Request: req}}
	}
	return hook.Plan{Cwd: in.Cwd, Steps: steps}
}

var names = map[hook.Event]string{
	hook.SessionStart:  "SessionStart",
	hook.Prompt:        "UserPromptSubmit",
	hook.PreTool:       "PreToolUse",
	hook.SubagentStart: "SubagentStart",
	hook.SessionEnd:    "SessionEnd",
}

// Output is the hookSpecificOutput Codex reads; an empty context prints nothing.
func (Adapter) Output(ev hook.Event, text string) string {
	if text == "" {
		return ""
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": names[ev], "additionalContext": text}})
	return string(b)
}
