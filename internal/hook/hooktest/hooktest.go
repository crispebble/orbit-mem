// Package hooktest is the contract test every hook adapter passes (ORBIT-23 D5): the hook input
// of each event becomes the requests of the host contract pairs (contract/host/v1), in order, and a
// context is printed as the bytes the hooks have always printed. Only tests import it.
package hooktest

import (
	"encoding/json"
	"reflect"
	"slices"
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

// Want is one expected request: a pair's input, with the host the adapter writes and some fields
// set (a nil value takes the field out).
type Want struct {
	Pair string
	Set  map[string]any
}

// Case is one hook input and the requests it becomes. An adapter adds its own host's cases (the input
// shapes only its host has) to the shared ones.
type Case struct {
	Name  string
	Event hook.Event
	Input string // 훅 입력 JSON 그대로
	Want  []Want
}

// In is a hook input of the contract test's session with fields.
func In(fields map[string]any) string {
	m := map[string]any{"session_id": Session, "cwd": Cwd}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

var planCases = []Case{
	{"session-start startup", hook.SessionStart, In(map[string]any{"hook_event_name": "SessionStart", "source": "startup"}),
		[]Want{{"context-main-memories", nil}}},
	{"session-start resume", hook.SessionStart, In(map[string]any{"hook_event_name": "SessionStart", "source": "resume"}),
		[]Want{{"context-main-memories", map[string]any{"source": "resume"}}}},
	{"session-start fork", hook.SessionStart, In(map[string]any{"hook_event_name": "SessionStart", "source": "fork"}),
		[]Want{{"context-main-memories", map[string]any{"source": "fork"}}}},
	// 압축·/clear 뒤에는 앞선 문맥이 없다: 표시를 먼저 비운다(reset 다음 context, 설계 11절).
	{"session-start compact", hook.SessionStart, In(map[string]any{"hook_event_name": "SessionStart", "source": "compact"}),
		[]Want{{"reset-ok", nil}, {"context-main-memories", map[string]any{"source": "compact"}}}},
	{"session-start clear", hook.SessionStart, In(map[string]any{"hook_event_name": "SessionStart", "source": "clear"}),
		[]Want{{"reset-ok", nil}, {"context-main-memories", map[string]any{"source": "clear"}}}},
	// 모르는·빠진 source는 계약에 넣지 않는다(사용법 오류로 문맥을 잃지 않게).
	{"session-start without source", hook.SessionStart, In(map[string]any{"hook_event_name": "SessionStart"}),
		[]Want{{"context-main-memories", map[string]any{"source": nil}}}},
	{"session-start unknown source", hook.SessionStart, In(map[string]any{"hook_event_name": "SessionStart", "source": "weird"}),
		[]Want{{"context-main-memories", map[string]any{"source": nil}}}},
	{"prompt", hook.Prompt, In(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "보고 한국어로"}),
		[]Want{{"recall-user", map[string]any{"origin": nil}}}},
	{"prompt in a subagent", hook.Prompt, In(map[string]any{"prompt": "보고 한국어로", "agent_id": "a-1"}),
		[]Want{{"recall-user", map[string]any{"agent": "a-1", "origin": nil}}}},
	{"pre-tool", hook.PreTool, In(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}}),
		[]Want{{"tool-context-bash", nil}}},
	{"pre-tool in a subagent", hook.PreTool, In(map[string]any{"tool_name": "Bash", "agent_id": "a-1"}),
		[]Want{{"tool-context-bash", map[string]any{"agent": "a-1"}}}},
	// 서브에이전트 종류는 짝 맞추기에 쓴다(위임 글을 적을 때의 종류와 같은 이름, 설계 4.1).
	{"subagent-start", hook.SubagentStart, In(map[string]any{"hook_event_name": "SubagentStart", "agent_id": "a-1", "agent_type": "worker"}),
		[]Want{{"context-subagent", map[string]any{"agentKind": "worker"}}}},
	{"session-end", hook.SessionEnd, In(map[string]any{"hook_event_name": "SessionEnd", "reason": "exit"}),
		[]Want{{"session-end-ok", nil}}},
	// 읽지 못하는 입력은 빈 입력이다: 훅은 계속한다(지금과 같다).
	{"broken input", hook.Prompt, `not json`,
		[]Want{{"recall-user", map[string]any{"session": nil, "cwd": nil, "prompt": nil, "origin": nil}}}},
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

// Plan checks that each hook event's input (the shared cases, then the adapter's own) becomes the
// pairs' requests in order. dir is the session folder the adapter is expected to give as the contract
// cwd whatever the input says ("" for the input's own cwd).
func Plan(t *testing.T, a hook.Adapter, hostName, dir string, own ...Case) {
	t.Helper()
	ps := pairs(t)
	for _, c := range append(slices.Clone(planCases), own...) {
		t.Run(c.Name, func(t *testing.T) {
			plan := a.Plan(c.Event, []byte(c.Input))
			if len(plan.Steps) != len(c.Want) {
				t.Fatalf("steps %+v, want %d", plan.Steps, len(c.Want))
			}
			for i, w := range c.Want {
				p, ok := ps[w.Pair]
				if !ok {
					t.Fatalf("no pair %s", w.Pair)
				}
				got := plan.Steps[i]
				if got.Op != p.Op {
					t.Fatalf("step %d op %q, want %q (%s)", i, got.Op, p.Op, p.Name)
				}
				want := expected(t, p, hostName, w.Set)
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
			_ = json.Unmarshal([]byte(c.Input), &raw)
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
