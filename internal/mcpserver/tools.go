package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/activity"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/project"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ActivityIn is activity_find's input.
type ActivityIn struct {
	Query   string `json:"query,omitempty" jsonschema:"프로젝트·경로·세션 id·요청 글·옛 작업 제목에 든 말"`
	Host    string `json:"host,omitempty" jsonschema:"claude·codex 중 하나만"`
	Project string `json:"project,omitempty" jsonschema:"비우면 이 프로젝트, all이면 전부, 아니면 프로젝트 디렉터리 절대경로"`
	Since   string `json:"since,omitempty" jsonschema:"이때 이후 활동만: YYYY-MM-DD, RFC 3339, 7d·12h(며칠·몇 시간 전)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"최대 개수(기본 20, 최대 100)"`
}

// activityQuery turns the tool input into a query of this connection's project.
func (h handlers) activityQuery(in ActivityIn) (activity.Query, error) {
	since, err := activity.ParseSince(in.Since, time.Now())
	if err != nil {
		return activity.Query{}, err
	}
	q := activity.Query{Query: in.Query, Host: in.Host, Since: since, Limit: in.Limit}
	switch p := strings.TrimSpace(in.Project); {
	case p == "":
		q.ProjectScope = h.o.Project // 프로젝트 밖의 연결이면 비어 전부가 된다
	case p == "all":
	case strings.HasPrefix(p, "project:"):
		q.ProjectScope = p
	case filepath.IsAbs(p):
		q.ProjectScope = project.Resolve(p).Scope
	default:
		return q, errors.New("project는 비우거나(이 프로젝트) all, 또는 프로젝트 디렉터리 절대경로입니다")
	}
	return q, nil
}

func (h handlers) activity(_ context.Context, _ *mcp.CallToolRequest, in ActivityIn) (*mcp.CallToolResult, activity.Result, error) {
	q, err := h.activityQuery(in)
	if err != nil {
		r, _ := toolError(err)
		return r, activity.Result{}, nil
	}
	res, err := activity.Find(h.s, q)
	if err != nil {
		r, _ := toolError(err)
		return r, activity.Result{}, nil
	}
	return nil, res, nil
}

// Tools are the AI host tools as plain functions, for the thin CLI (설계 S6): the same
// handlers the MCP server calls, without the use events a host's calls record.
type Tools struct{ h handlers }

// NewTools gives the tools over the store for a connection like o. The thin
// CLI is the person at a terminal: without an actor it writes as user.
func NewTools(s *memory.Store, o Options) Tools {
	if o.Actor == "" {
		o.Actor = memory.ActorUser
	}
	return Tools{handlers{s: s, o: o}}
}

// Lower gives the tools with the trust limit of a call lowered to trust at most: min(the limit now, trust).
// It never raises it. An unknown trust is an error (memory.ErrInvalid).
func (t Tools) Lower(trust string) (Tools, error) {
	limit, err := memory.ResolveTrust(t.h.o.TrustLimit, "", trust)
	if err != nil {
		return t, err
	}
	t.h.o.TrustLimit = limit
	return t, nil
}

// asError turns a tool error result into an error.
func asError(res *mcp.CallToolResult) error {
	if res == nil || !res.IsError {
		return nil
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return errors.New(b.String())
}

func (t Tools) Find(in FindIn) (FindOut, error) {
	res, out, _ := t.h.find(context.Background(), nil, in)
	return out, asError(res)
}

func (t Tools) Get(in GetIn) (GetOut, error) {
	res, out, _ := t.h.get(context.Background(), nil, in)
	return out, asError(res)
}

func (t Tools) Update(in UpdateIn) (MemoryOut, error) {
	res, out, _ := t.h.update(context.Background(), nil, in)
	return out, asError(res)
}

func (t Tools) Activity(in ActivityIn) (activity.Result, error) {
	res, out, _ := t.h.activity(context.Background(), nil, in)
	return out, asError(res)
}

// HostTools are the memory tools a host without an MCP client calls through
// the CLI (`orbit-mem tool <name>`, `orbit-mem host call`): the same input and
// output as the MCP tools, under orbit-mem's own names only (host 계약 v1, T-25).
var HostTools = []string{"memory_find", "memory_get", "memory_save", "memory_update", "activity_find"}

// Call runs one host tool on its MCP input and records the call as the
// "tool.<name>" event of that host and session (설계 「측정」), which the
// recall diagnosis reads as use.
func (t Tools) Call(name string, input []byte, host, session string) (any, error) {
	var out any
	var data map[string]any
	var err error
	ids := func(items []string) map[string]any { return map[string]any{"results": len(items), "ids": items} }
	switch name {
	case "memory_find":
		var in FindIn
		if err = decode(input, &in); err == nil {
			var r FindOut
			if r, err = t.Find(in); err == nil {
				list := []string{}
				for _, m := range r.Items {
					list = append(list, m.ID)
				}
				out, data = r, ids(list)
				data["query"] = memory.ClipRunes(memory.MaskSecrets(in.Query), 200)
			}
		}
	case "memory_get":
		var in GetIn
		if err = decode(input, &in); err == nil {
			var r GetOut
			if r, err = t.Get(in); err == nil {
				list := []string{}
				for _, m := range r.Items {
					list = append(list, m.ID)
				}
				out, data = r, ids(list)
			}
		}
	case "memory_save":
		var in SaveIn
		if err = decode(input, &in); err == nil {
			var r MemoryOut
			if r, err = t.Save(in); err == nil {
				out, data = r, map[string]any{"kind": in.Kind, "sourceKind": in.Source.Kind, "state": r.Memory.State, "memoryId": r.Memory.ID}
			}
		}
	case "memory_update":
		var in UpdateIn
		if err = decode(input, &in); err == nil {
			var r MemoryOut
			if r, err = t.Update(in); err == nil {
				out, data = r, map[string]any{"action": in.Action, "memoryId": in.ID}
			}
		}
	case "activity_find":
		var in ActivityIn
		if err = decode(input, &in); err == nil {
			var r activity.Result
			if r, err = t.Activity(in); err == nil {
				list := []string{}
				for _, a := range r.Items {
					list = append(list, a.Session)
				}
				out, data = r, ids(list)
				data["query"] = memory.ClipRunes(memory.MaskSecrets(in.Query), 200)
				data["project"] = in.Project
			}
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["ok"] = err == nil
	_ = t.h.s.RecordEvent(memory.Event{Kind: memory.EventToolPrefix + name, Host: host, Session: session, Scope: t.h.o.Project}, data)
	return out, err
}

// ErrUnknownTool is a tool name Call does not know.
var ErrUnknownTool = errors.New("unknown tool")

func decode(b []byte, v any) error {
	if len(strings.TrimSpace(string(b))) == 0 {
		b = []byte("{}")
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("입력 JSON을 읽지 못했습니다: %w", err)
	}
	return nil
}

func (t Tools) Save(in SaveIn) (MemoryOut, error) {
	res, out, _ := t.h.save(context.Background(), nil, in)
	return out, asError(res)
}
