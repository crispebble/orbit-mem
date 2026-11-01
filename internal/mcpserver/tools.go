package mcpserver

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/activity"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/project"
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
	res, err := activity.Find(h.s, h.o.WorkDB, q)
	if err != nil {
		r, _ := toolError(err)
		return r, activity.Result{}, nil
	}
	return nil, res, nil
}

// Tools are the AI host tools as plain functions, for the thin CLI (설계 S6): the same
// handlers the MCP server calls, without the use events a host's calls record.
type Tools struct{ h handlers }

// NewTools gives the tools over the store for a connection like o.
func NewTools(s *memory.Store, o Options) Tools { return Tools{handlers{s: s, o: o}} }

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
