// Package hooktest is the contract test every hook adapter passes (ORBIT-23 D5): the hook input
// of each event becomes the requests of the host contract pairs (contract/host/v1), in order, and a
// context is printed as the bytes the hooks have always printed. Only tests import it.
package hooktest

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/crispebble/orbit-mem/contract"
	"github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/inject"
)

// Cwd and Session are the session the hook inputs of the contract test carry (those of the pairs).
const (
	Cwd     = "/tmp/orbit-contract"
	Session = "s-1"
)

// want is one expected request: a pair's input, with the host the adapter writes and some fields
// set (a nil value takes the field out).
type want struct {
	pair string
	set  map[string]any
}

type planCase struct {
	name  string
	ev    hook.Event
	input string // 훅 입력 JSON 그대로
	want  []want
}

func in(fields map[string]any) string {
	m := map[string]any{"session_id": Session, "cwd": Cwd}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

var planCases = []planCase{
	{"session-start startup", hook.SessionStart, in(map[string]any{"hook_event_name": "SessionStart", "source": "startup"}),
		[]want{{"context-main-memories", nil}}},
	{"session-start resume", hook.SessionStart, in(map[string]any{"hook_event_name": "SessionStart", "source": "resume"}),
		[]want{{"context-main-memories", map[string]any{"source": "resume"}}}},
	{"session-start fork", hook.SessionStart, in(map[string]any{"hook_event_name": "SessionStart", "source": "fork"}),
		[]want{{"context-main-memories", map[string]any{"source": "fork"}}}},
	// 압축·/clear 뒤에는 앞선 문맥이 없다: 표시를 먼저 비운다(reset 다음 context, 설계 11절).
	{"session-start compact", hook.SessionStart, in(map[string]any{"hook_event_name": "SessionStart", "source": "compact"}),
		[]want{{"reset-ok", nil}, {"context-main-memories", map[string]any{"source": "compact"}}}},
	{"session-start clear", hook.SessionStart, in(map[string]any{"hook_event_name": "SessionStart", "source": "clear"}),
		[]want{{"reset-ok", nil}, {"context-main-memories", map[string]any{"source": "clear"}}}},
	// 모르는·빠진 source는 계약에 넣지 않는다(사용법 오류로 문맥을 잃지 않게).
	{"session-start without source", hook.SessionStart, in(map[string]any{"hook_event_name": "SessionStart"}),
		[]want{{"context-main-memories", map[string]any{"source": nil}}}},
	{"session-start unknown source", hook.SessionStart, in(map[string]any{"hook_event_name": "SessionStart", "source": "weird"}),
		[]want{{"context-main-memories", map[string]any{"source": nil}}}},
	{"prompt", hook.Prompt, in(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "보고 한국어로"}),
		[]want{{"recall-user", nil}}},
	{"prompt in a subagent", hook.Prompt, in(map[string]any{"prompt": "보고 한국어로", "agent_id": "a-1"}),
		[]want{{"recall-user", map[string]any{"agent": "a-1"}}}},
	{"pre-tool", hook.PreTool, in(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}}),
		[]want{{"tool-context-bash", nil}}},
	{"pre-tool in a subagent", hook.PreTool, in(map[string]any{"tool_name": "Bash", "agent_id": "a-1"}),
		[]want{{"tool-context-bash", map[string]any{"agent": "a-1"}}}},
	{"subagent-start", hook.SubagentStart, in(map[string]any{"hook_event_name": "SubagentStart", "agent_id": "a-1", "agent_type": "worker"}),
		[]want{{"context-subagent", nil}}},
	{"session-end", hook.SessionEnd, in(map[string]any{"hook_event_name": "SessionEnd", "reason": "exit"}),
		[]want{{"session-end-ok", nil}}},
	// 읽지 못하는 입력은 빈 입력이다: 훅은 계속한다(지금과 같다).
	{"broken input", hook.Prompt, `not json`,
		[]want{{"recall-user", map[string]any{"session": nil, "cwd": nil, "prompt": nil}}}},
}

func pairs(t *testing.T) map[string]contract.Pair {
	t.Helper()
	list, err := contract.HostV1()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]contract.Pair{}
	for _, p := range list {
		out[p.Name] = p
	}
	return out
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// expected is the request of a want: the pair's input, its host the adapter's.
func expected(t *testing.T, p contract.Pair, hostName string, set map[string]any) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(p.Input, &m); err != nil {
		t.Fatalf("%s: %v", p.Name, err)
	}
	m["host"] = hostName
	for k, v := range set {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

// Plan checks that each hook event's input becomes the pairs' requests in order. dir is the
// session folder the adapter is expected to give as the contract cwd whatever the input says
// ("" for the input's own cwd).
func Plan(t *testing.T, a hook.Adapter, hostName, dir string) {
	t.Helper()
	ps := pairs(t)
	for _, c := range planCases {
		t.Run(c.name, func(t *testing.T) {
			plan := a.Plan(c.ev, []byte(c.input))
			if len(plan.Steps) != len(c.want) {
				t.Fatalf("steps %+v, want %d", plan.Steps, len(c.want))
			}
			for i, w := range c.want {
				p, ok := ps[w.pair]
				if !ok {
					t.Fatalf("no pair %s", w.pair)
				}
				got := plan.Steps[i]
				if got.Op != p.Op {
					t.Fatalf("step %d op %q, want %q (%s)", i, got.Op, p.Op, p.Name)
				}
				want := expected(t, p, hostName, w.set)
				if dir != "" {
					want["cwd"] = dir
				}
				if g := asMap(t, got.Request); !reflect.DeepEqual(g, want) {
					t.Fatalf("step %d request\n got %v\nwant %v (%s)", i, g, want, p.Name)
				}
			}
			var raw struct {
				Cwd string `json:"cwd"`
			}
			_ = json.Unmarshal([]byte(c.input), &raw)
			if plan.Cwd != raw.Cwd {
				t.Fatalf("plan cwd %q, want the input's own %q", plan.Cwd, raw.Cwd)
			}
		})
	}
}

// Sample is a context with what JSON escapes: quotes, new lines, HTML characters and Korean.
const Sample = "[orbit-mem 기억]\n- \"규칙\" <a> & 'b'\n\ttab"

// Output checks the hook output of every event: nothing for an empty context, else the bytes
// the hooks have printed so far (inject.HookOutput, and the same bytes written out).
func Output(t *testing.T, a hook.Adapter, names map[hook.Event]string) {
	t.Helper()
	for _, ev := range hook.Events {
		name, ok := names[ev]
		if !ok {
			t.Fatalf("no hook event name for %s", ev)
		}
		if got := a.Output(ev, ""); got != "" {
			t.Fatalf("%s: empty context printed %q", ev, got)
		}
		text, _ := json.Marshal(Sample)
		literal := `{"hookSpecificOutput":{"additionalContext":` + string(text) + `,"hookEventName":"` + name + `"}}`
		got := a.Output(ev, Sample)
		if got != literal {
			t.Fatalf("%s output\n got %s\nwant %s", ev, got, literal)
		}
		if got != inject.HookOutput(name, Sample) {
			t.Fatalf("%s output differs from the hooks so far", ev)
		}
	}
}

// Names are the hook event names Claude Code and Codex both use.
var Names = map[hook.Event]string{
	hook.SessionStart:  "SessionStart",
	hook.Prompt:        "UserPromptSubmit",
	hook.PreTool:       "PreToolUse",
	hook.SubagentStart: "SubagentStart",
	hook.SessionEnd:    "SessionEnd",
}
