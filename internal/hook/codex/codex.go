// Package codex is the Codex adapter of the hooks (설계 · 지연 회상·호스트 계약 11절): it reads Codex's
// hook input into host contract requests and prints a context the way Codex reads it from a hook's
// stdout.
package codex

import (
	"encoding/json"
	"slices"

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
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
	Source    string `json:"source"`
	ToolName  string `json:"tool_name"`
	AgentID   string `json:"agent_id"`
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
		steps = []hook.Step{{Op: host.OpToolContext, Request: req}}
	case hook.SubagentStart:
		req.Audience = host.AudienceSubagent
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
