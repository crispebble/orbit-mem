// Package mcpserver is the Orbit MCP server: six tools to find, read, save and
// fix memories and to see and decide proposals.
package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/project"
)

// Options says where the server runs.
type Options struct {
	Project      string // 이 세션의 프로젝트 범위("project:<키>")
	ProjectLabel string
	Version      string
}

const instructions = `Orbit은 이 PC의 기억이다. 작업에 기대는 규칙·결정·교훈이 있을 법하면 memory_find로 찾고 memory_get으로 읽는다.
사용자가 "기억해"라고 하거나 다시 쓸 만한 사실·결정·교훈이 생기면 memory_save로 출처와 함께 남긴다. 사용자의 말을 그대로 옮긴 출처(kind=quote)면 바로 확정, 아니면 후보가 된다.
틀린 기억은 memory_update로 고치거나 낡음으로 표시한다. 사용자가 "제안 보여줘"라고 하면 proposals로 보여 주고 사용자의 답대로 proposal_decide를 부른다.`

// New builds the server over the store.
func New(s *memory.Store, o Options) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "orbit", Version: o.Version}, &mcp.ServerOptions{Instructions: instructions})
	h := handlers{s: s, o: o}
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_find", Description: "질의·종류·범위로 기억을 찾는다. 기본은 전역과 이 프로젝트의 후보·확정 기억이다."}, h.find)
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_get", Description: "id로 기억을 읽는다(여러 개). 출처와 후보가 나온 날을 함께 준다."}, h.get)
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_save", Description: "기억을 출처와 함께 한 번에 남긴다. 출처가 사용자 말 인용(quote)이면 확정, 아니면 후보. 같은 제목의 후보가 다른 날 다시 저장되면 확정된다."}, h.save)
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_update", Description: "기억을 고친다. action: edit(기본)·confirm·stale(낡음)·supersede(supersededBy가 대체)·restore(후보로 되돌림)."}, h.update)
	mcp.AddTool(srv, &mcp.Tool{Name: "proposals", Description: "대기 중인 제안(후보 확정, 낡은 기억 정리·합치기, 재분류, 파일 반영 diff, 방향)을 대상 기억과 함께 보여 준다."}, h.proposals)
	mcp.AddTool(srv, &mcp.Tool{Name: "proposal_decide", Description: "제안을 승인·거절한다. title·body를 주면 고쳐서 승인한다. 파일 반영은 파일만 바꾸며 커밋하지 않는다. 사용자의 결정을 받은 뒤에만 부른다."}, h.decide)
	return srv
}

type handlers struct {
	s *memory.Store
	o Options
}

// FindIn is memory_find's input.
type FindIn struct {
	Query  string   `json:"query,omitempty" jsonschema:"찾을 말. 비우면 최근 것부터"`
	Kinds  []string `json:"kinds,omitempty" jsonschema:"rule·fact·decision·lesson·journal 중 고른 것"`
	Scope  string   `json:"scope,omitempty" jsonschema:"here(전역+이 프로젝트, 기본)·global·all"`
	States []string `json:"states,omitempty" jsonschema:"candidate·confirmed·stale. 기본은 candidate와 confirmed"`
	Limit  int      `json:"limit,omitempty" jsonschema:"최대 개수(기본 20, 최대 50)"`
}

// Summary is a short view of a memory in a list.
type Summary struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	State     string   `json:"state"`
	Scope     string   `json:"scope"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Version   int      `json:"version"`
	UpdatedAt int64    `json:"updatedAt"`
}

// FindOut is memory_find's result.
type FindOut struct {
	Items []Summary `json:"items"`
}

func summarize(m memory.Memory) Summary {
	r := []rune(m.Body)
	if len(r) > 200 {
		r = append(r[:200], '…')
	}
	return Summary{ID: m.ID, Kind: m.Kind, State: m.State, Scope: m.Scope, Title: m.Title, Summary: string(r), Tags: m.Tags, Version: m.Version, UpdatedAt: m.UpdatedAt}
}

func toolError(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
}

func (h handlers) find(_ context.Context, _ *mcp.CallToolRequest, in FindIn) (*mcp.CallToolResult, FindOut, error) {
	limit := in.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	f := memory.FindInput{Query: in.Query, Kinds: in.Kinds, States: in.States, Project: h.o.Project, Limit: limit}
	switch in.Scope {
	case "", "here":
	case "global":
		f.GlobalOnly = true
	case "all":
		f.AllProjects = true
	default:
		r, _ := toolError(fmt.Errorf("scope는 here·global·all입니다"))
		return r, FindOut{}, nil
	}
	ms, err := h.s.Find(f)
	if err != nil {
		r, _ := toolError(err)
		return r, FindOut{}, nil
	}
	out := FindOut{Items: []Summary{}}
	for _, m := range ms {
		out.Items = append(out.Items, summarize(m))
	}
	return nil, out, nil
}

// GetIn is memory_get's input.
type GetIn struct {
	IDs     []string `json:"ids" jsonschema:"읽을 기억 id들"`
	History bool     `json:"history,omitempty" jsonschema:"true면 버전 이력도 준다"`
}

// GetOut is memory_get's result.
type GetOut struct {
	Items   []memory.Memory             `json:"items"`
	History map[string][]memory.Version `json:"history,omitempty"`
}

func (h handlers) get(_ context.Context, _ *mcp.CallToolRequest, in GetIn) (*mcp.CallToolResult, GetOut, error) {
	out := GetOut{Items: []memory.Memory{}}
	if len(in.IDs) == 0 || len(in.IDs) > 20 {
		r, _ := toolError(fmt.Errorf("ids는 1~20개입니다"))
		return r, out, nil
	}
	for _, id := range in.IDs {
		m, err := h.s.Get(id)
		if err != nil {
			r, _ := toolError(err)
			return r, out, nil
		}
		out.Items = append(out.Items, m)
		if in.History {
			if out.History == nil {
				out.History = map[string][]memory.Version{}
			}
			out.History[id], _ = h.s.History(id)
		}
	}
	return nil, out, nil
}

// SourceIn is where a memory came from.
type SourceIn struct {
	Kind    string `json:"kind" jsonschema:"quote(사용자 말 그대로)·file·command·session·host_memory"`
	Ref     string `json:"ref,omitempty" jsonschema:"파일 절대경로, 명령, 세션 id 같은 위치"`
	Excerpt string `json:"excerpt,omitempty" jsonschema:"인용이나 발췌. quote면 사용자 말 그대로"`
}

// SaveIn is memory_save's input.
type SaveIn struct {
	Kind    string   `json:"kind" jsonschema:"rule(사용자가 원하는 방식)·fact·decision·lesson·journal"`
	Title   string   `json:"title" jsonschema:"한 줄 제목(200자 이하)"`
	Body    string   `json:"body,omitempty" jsonschema:"짧은 본문"`
	Tags    []string `json:"tags,omitempty"`
	Scope   string   `json:"scope,omitempty" jsonschema:"global(기본) 또는 project(이 프로젝트에만)"`
	Project string   `json:"project,omitempty" jsonschema:"범위가 project일 때 프로젝트 디렉터리 절대경로. 비우면 이 세션의 프로젝트"`
	Source  SourceIn `json:"source"`
}

// MemoryOut is a single memory result.
type MemoryOut struct {
	Memory memory.Memory `json:"memory"`
}

func (h handlers) scope(scope, dir string) (string, string, error) {
	switch scope {
	case "", "global":
		return memory.ScopeGlobal, "", nil
	case "project":
		if dir != "" {
			p := project.Resolve(dir)
			return p.Scope, p.Label, nil
		}
		if h.o.Project == "" {
			return "", "", fmt.Errorf("이 세션의 프로젝트를 모릅니다. project에 디렉터리 절대경로를 주세요")
		}
		return h.o.Project, h.o.ProjectLabel, nil
	}
	return "", "", fmt.Errorf("scope는 global 또는 project입니다")
}

func (h handlers) save(_ context.Context, _ *mcp.CallToolRequest, in SaveIn) (*mcp.CallToolResult, MemoryOut, error) {
	if in.Source.Kind == memory.SourceLegacy {
		r, _ := toolError(fmt.Errorf("legacy 출처는 oni migrate만 씁니다"))
		return r, MemoryOut{}, nil
	}
	scope, label, err := h.scope(in.Scope, in.Project)
	if err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	if in.Source.Kind == memory.SourceQuote && strings.TrimSpace(in.Source.Excerpt) == "" {
		r, _ := toolError(fmt.Errorf("quote 출처에는 사용자 말 그대로(excerpt)가 필요합니다"))
		return r, MemoryOut{}, nil
	}
	m, err := h.s.Save(memory.SaveInput{Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Scope: scope, Project: label,
		Source: memory.Source{Kind: in.Source.Kind, Ref: in.Source.Ref, Excerpt: in.Source.Excerpt}})
	if err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	return nil, MemoryOut{Memory: m}, nil
}

// UpdateIn is memory_update's input.
type UpdateIn struct {
	ID              string    `json:"id"`
	ExpectedVersion int       `json:"expectedVersion,omitempty" jsonschema:"읽은 판본. 주면 그사이 바뀌었을 때 거절한다"`
	Action          string    `json:"action,omitempty" jsonschema:"edit(기본)·confirm·stale·supersede·restore"`
	Kind            *string   `json:"kind,omitempty"`
	Title           *string   `json:"title,omitempty"`
	Body            *string   `json:"body,omitempty"`
	Tags            *[]string `json:"tags,omitempty"`
	Scope           string    `json:"scope,omitempty" jsonschema:"global 또는 project로 범위를 옮긴다"`
	SupersededBy    string    `json:"supersededBy,omitempty" jsonschema:"supersede일 때 대신하는 기억 id"`
	Reason          string    `json:"reason,omitempty"`
}

func (h handlers) update(_ context.Context, _ *mcp.CallToolRequest, in UpdateIn) (*mcp.CallToolResult, MemoryOut, error) {
	var m memory.Memory
	var err error
	if in.ExpectedVersion != 0 {
		cur, gerr := h.s.Get(in.ID)
		if gerr == nil && cur.Version != in.ExpectedVersion {
			r, _ := toolError(memory.ErrVersionConflict)
			return r, MemoryOut{}, nil
		}
	}
	switch in.Action {
	case "", "edit", "confirm", "restore", "stale":
		u := memory.UpdateInput{ID: in.ID, ExpectedVersion: in.ExpectedVersion, Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Reason: in.Reason, Action: in.Action}
		if in.Scope != "" {
			sc, _, serr := h.scope(in.Scope, "")
			if serr != nil {
				r, _ := toolError(serr)
				return r, MemoryOut{}, nil
			}
			u.Scope = &sc
		}
		state := map[string]string{"confirm": memory.StateConfirmed, "restore": memory.StateCandidate, "stale": memory.StateStale}[in.Action]
		if state != "" {
			u.State = &state
		}
		m, err = h.s.Update(u)
	case "supersede":
		if in.SupersededBy == "" {
			err = fmt.Errorf("supersede에는 supersededBy가 필요합니다")
			break
		}
		m, err = h.s.Supersede(in.ID, in.SupersededBy, in.Reason)
	default:
		err = fmt.Errorf("action은 edit·confirm·stale·supersede·restore입니다")
	}
	if err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	return nil, MemoryOut{Memory: m}, nil
}

// ProposalsIn is proposals' input.
type ProposalsIn struct {
	Status string `json:"status,omitempty" jsonschema:"pending(기본)·approved·rejected·all"`
}

// ProposalView is a proposal with the memories it concerns.
type ProposalView struct {
	memory.Proposal
	Memories []Summary `json:"memories,omitempty"`
}

// ProposalsOut is proposals' result.
type ProposalsOut struct {
	Items []ProposalView `json:"items"`
}

func (h handlers) proposals(_ context.Context, _ *mcp.CallToolRequest, in ProposalsIn) (*mcp.CallToolResult, ProposalsOut, error) {
	status := in.Status
	switch status {
	case "":
		status = memory.StatusPending
	case "all":
		status = ""
	}
	ps, err := h.s.Proposals(status)
	if err != nil {
		r, _ := toolError(err)
		return r, ProposalsOut{}, nil
	}
	out := ProposalsOut{Items: []ProposalView{}}
	for _, p := range ps {
		v := ProposalView{Proposal: p}
		for _, id := range p.MemoryIDs {
			if m, err := h.s.Get(id); err == nil {
				v.Memories = append(v.Memories, summarize(m))
			}
		}
		out.Items = append(out.Items, v)
	}
	return nil, out, nil
}

// DecideIn is proposal_decide's input.
type DecideIn struct {
	ID       string  `json:"id"`
	Decision string  `json:"decision" jsonschema:"approve 또는 reject"`
	Title    *string `json:"title,omitempty" jsonschema:"고쳐서 승인할 제목"`
	Body     *string `json:"body,omitempty" jsonschema:"고쳐서 승인할 본문(파일 반영이면 새 파일 내용 전체)"`
	Note     string  `json:"note,omitempty" jsonschema:"결정 이유"`
}

// DecideOut is proposal_decide's result.
type DecideOut struct {
	memory.DecideResult
}

func (h handlers) decide(_ context.Context, _ *mcp.CallToolRequest, in DecideIn) (*mcp.CallToolResult, DecideOut, error) {
	res, err := h.s.Decide(memory.DecideInput{ID: in.ID, Decision: in.Decision, Title: in.Title, Body: in.Body, Note: in.Note})
	if err != nil {
		r, _ := toolError(err)
		return r, DecideOut{}, nil
	}
	return nil, DecideOut{res}, nil
}
