// Package claudecode is the Claude Code adapter of the hooks (설계 · 지연 회상·호스트 계약 11절): it reads
// Claude Code's hook input into host contract requests and prints a context the way Claude Code reads
// it from a hook's stdout.
package claudecode

import (
	"encoding/json"
	"slices"

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
	Prompt    string `json:"prompt"`    // UserPromptSubmit
	Source    string `json:"source"`    // SessionStart: startup·resume·clear·compact
	ToolName  string `json:"tool_name"` // PreToolUse
	AgentID   string `json:"agent_id"`  // SubagentStart, 서브에이전트 안의 훅
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
		req.Prompt, req.Origin = in.Prompt, host.OriginUser // 이 훅은 사람이 낸 요청에만 돈다
		return []hook.Step{{Op: host.OpRecall, Request: req}}
	case hook.PreTool:
		req.Tool = in.ToolName // 도구 입력은 읽지도 바꾸지도 않는다
		return []hook.Step{{Op: host.OpToolContext, Request: req}}
	case hook.SubagentStart:
		req.Audience = host.AudienceSubagent
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
