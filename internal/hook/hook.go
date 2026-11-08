// Package hook runs a host's hook over the host contract (설계 · 지연 회상·호스트 계약 11절, ORBIT-23 D5):
// it reads the hook's input, has the host's adapter turn it into contract operations, runs them on a
// host.Service and prints what the adapter makes of the result. It knows the hook events, not the
// hosts: each host's input and output shapes are an Adapter, which the assembly point picks.
//
// A hook never fails its host (설계 10.2): any error, timeout or panic prints nothing and the hook
// ends as a success. The failures are told to the Runner's Failed and kept by the Service as
// diagnostic events.
package hook

import (
	"slices"

	"github.com/crispebble/orbit-mem/internal/host"
)

// Event is a hook event, by the name the hook command takes.
type Event string

// The hook events (설계 11절).
const (
	SessionStart  Event = "session-start"
	Prompt        Event = "prompt"
	PreTool       Event = "pre-tool"
	SubagentStart Event = "subagent-start"
	SessionEnd    Event = "session-end"
)

// Events are the hook events in the order of the contract table.
var Events = []Event{SessionStart, Prompt, PreTool, SubagentStart, SessionEnd}

// ParseEvent reads a hook event name.
func ParseEvent(name string) (Event, bool) {
	if ev := Event(name); slices.Contains(Events, ev) {
		return ev, true
	}
	return "", false
}

// Step is one contract operation of a hook.
type Step struct {
	Op      string
	Request host.Request
}

// Plan is what an adapter reads from one hook input: the operations in order (the last one's
// result is the hook's context) and the input's own working folder.
type Plan struct {
	Steps []Step
	Cwd   string // 훅 입력의 cwd 그대로(세션 폴더가 아닐 수 있다)
}

// Adapter is one host's side of the hooks: how its hook input becomes contract requests and how a
// context is printed for it.
type Adapter interface {
	// Plan reads one hook input. Input that cannot be read is read as empty: the hook goes on.
	Plan(ev Event, input []byte) Plan
	// Output is what the hook prints for a context; "" prints nothing.
	Output(ev Event, text string) string
}
