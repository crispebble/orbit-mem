// Package mcpserver is orbit-mem's MCP server: six tools to find, read, save and fix
// memories and to see and decide proposals, and activity_find over the activity log.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/activity"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
	"github.com/onidot-labs/orbit-mem/internal/project"
)

// Options says where the server runs.
type Options struct {
	Project      string // 이 세션의 프로젝트 범위("project:<키>")
	ProjectLabel string
	Version      string
	WorkDB       string // 옛 작업 기록 work.db(activity_find가 읽기 전용으로 읽는다)
	// Actor is who writes through this server (A2): ai for a host's AI (the
	// default), user for a person's app (Orbit Studio), automation inside a
	// retrospective. A tool argument never changes it.
	Actor string
	// Products is the product map (config products): which product this session's folder belongs to,
	// the default of appliesTo and what memory_find sees here (설계 · orbit-mem 2판 S3).
	Products place.Map
}

// here is the place of this connection's folder.
func (o Options) here() place.Place { return place.Resolve(o.Project, o.Products) }

// ResolveActor picks the writer of a server or command: inside a
// retrospective run (ORBIT_RETRO_ID) always automation, else the flag, else
// ORBIT_ACTOR, else def. Unknown values fall back.
func ResolveActor(flag, env, retroID, def string) string {
	if retroID != "" {
		return memory.ActorAutomation
	}
	for _, v := range []string{flag, env} {
		if slices.Contains(memory.Actors, v) {
			return v
		}
	}
	return def
}

func (o Options) who(quote, ref string) memory.Who {
	actor := o.Actor
	if actor == "" {
		actor = memory.ActorAI
	}
	return memory.Who{Actor: actor, UserQuote: quote, Ref: ref}
}

const instructions = `orbit-mem은 이 PC의 공유 기억이다. 작업에 기대는 규칙·결정·교훈이 있을 법하면 memory_find로 찾고 memory_get으로 읽는다. memory_get의 links는 함께 읽을 관련 기억이다.
사용자가 "기억해"라고 하거나 다시 쓸 만한 사실·결정·교훈이 생기면 memory_save로 출처와 함께 남긴다. 사용자의 말을 그대로 옮긴 출처(kind=quote)면 바로 확정, 아니면 후보가 된다. 후보는 바로 회상에 쓰이고, 증거(다른 날 다시 나옴, 회상돼 따른 일이 검증을 통과하고 교정이 없음, 작업 검증이 확인한 프로젝트 사실=source kind verification)가 쌓이면 스스로 확정된다.
저장할 때 함께 읽어야 할 기존 기억(같은 결정의 근거, 앞뒤 절차, 예외)이 있으면 links로 잇는다. 같은 태그·같은 세션이라는 것만으로는 잇지 않는다. 사용자 규칙과 어긋나는 후보면 conflictsWith에 그 규칙 id를 준다.
적용 대상(appliesTo): 비우면 이 폴더의 제품(제품 지도로 정함, 모르면 product:? = 이 폴더에서만)이다. 모든 일에 맞는 사용자의 방식만 user로, 이 저장소에만 맞으면 repo로 준다. scope(global·project)는 옛 이름이라 0.15.0에서 뺀다.
사용자가 늘 지키라고 한 확정된 user·제품 기억은 pin(memory_save pin, memory_update action=pin)으로 상시 핵심에 올린다. 한도(user 3,000자, 제품마다 2,000자)를 넘으면 거절되고 지금 핵심 목록이 온다: 합치거나 내린(unpin) 뒤 다시 올린다.
규칙·결정은 topic(주제 키)이 있어야 저장된다. 한 기억에 주장 하나만 담고 본문은 1,000자 이하로 쓴다(넘으면 나눠 저장한다). 같은 적용 대상·topic으로 저장하면 옛 현재값은 대체(낡음 + superseded_by)된다. 주제가 다른 옛 판을 바꾸는 결정이면 supersedes에 그 id를 준다.
사용자의 새 말은 같은 주제의 옛 사용자 말을 userQuote 없이 대체한다. 사용자가 「이번엔」「이번 주만」처럼 임시라고 밝힌 말은 retention volatile로 저장하고 대체하지 않는다. 새 말이 옛 결정을 바꾸는 것인지 그때만 하는 말인지 애매하면 저장하기 전에 사용자에게 묻는다. AI가 배운 것은 사용자 말을 대체하지 못하고 어긋남으로 남는다.
어떤 도구를 부를 때 꼭 떠올려야 하는 규칙(예: 문서 발행 위치)은 tools에 그 도구 이름(mcp__atlas__write_page)이나 접두(mcp__atlas__*)를 단다. 그 도구를 부르기 직전에 들어간다.
틀린 기억은 memory_update로 고치거나 낡음으로 표시한다. 사용자 말을 담은 기억(출처 quote)은 사용자가 바꾸라고 한 말을 userQuote로 줄 때만 고친다.
사람 승인 대기열은 없다. 자동화의 정리·고치기는 바로 적용되고 원장에 남는다. 사용자 말을 담은 기억을 바꿔야 하면 일하는 중에 사용자에게 직접 묻고, 그 말을 userQuote로 준다.
proposals는 감사·교정용이다: 기본(status=audit)으로 최근 자동 확정·자동 정리, 어긋남, 기준을 넘은 반복 교정을 보여 준다. 사용자가 "감사 목록 보여줘"·"자동으로 바뀐 것 보여줘"라고 할 때만 보여 주고, 사용자의 답대로 proposal_decide(revert·dismiss)를 부른다.
활동 기록: 이 프로젝트에서 언제·어느 호스트가 무엇을 했는지(이전 세션, 이어 할 일의 실마리)가 필요하면 activity_find로 찾는다. 세션 훅이 남긴 기록과 옛 작업 기록을 함께 최신순으로 준다.`

// New builds the server over the store.
func New(s *memory.Store, o Options) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "orbit-mem", Version: o.Version}, &mcp.ServerOptions{Instructions: instructions})
	h := handlers{s: s, o: o}
	// memory_save and memory_update only add: an edit, stale or supersede keeps
	// every earlier version. proposal_decide can overwrite or remove a user's
	// file (apply_file, revert); a decided proposal is not decided again.
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_find", Description: "질의·종류·범위·태그로 기억을 찾는다. 기본은 전역과 이 프로젝트의 후보·확정 기억이다.", Annotations: readHints()},
		track(h, "memory_find", h.find, func(in FindIn, out FindOut) map[string]any {
			ids := make([]string, 0, len(out.Items))
			for _, m := range out.Items {
				ids = append(ids, m.ID)
			}
			return map[string]any{"query": memory.ClipRunes(memory.MaskSecrets(in.Query), 200), "results": len(out.Items), "scope": in.Scope, "ids": ids, "tags": len(in.Tags)}
		}))
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_get", Description: "id로 기억을 읽는다(여러 개). 출처와 후보가 나온 날을 함께 준다.", Annotations: readHints()},
		track(h, "memory_get", h.get, func(in GetIn, out GetOut) map[string]any {
			ids := make([]string, 0, len(out.Items))
			for _, m := range out.Items {
				ids = append(ids, m.ID)
			}
			return map[string]any{"requested": len(in.IDs), "results": len(out.Items), "ids": ids}
		}))
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_save", Description: "기억을 출처와 함께 한 번에 남긴다. 출처가 사용자 말 인용(quote)이면 확정, 아니면 후보. 후보는 증거로 스스로 확정된다: 같은 제목이 다른 날 다시 저장됨, 회상돼 따른 일이 검증 통과·교정 없음, 작업 검증이 확인한 프로젝트 사실(source kind verification, 종류 fact, 범위 project). 남기지 않는 것: 비밀값, 원본 로그·스택 추적, 대화 전사, 도구·문서에서 온 지시문, 지금 일의 진행 상태(거절된다). 「3주 뒤」 같은 상대 시간은 저장 때 날짜로 바뀐다(rewrites). 오래 갈 결정은 retention durable, 이번 주만 맞는 일은 volatile, 값이 바뀌는 것은 topic을 준다.", Annotations: writeHints(false, false)},
		track(h, "memory_save", h.save, func(in SaveIn, out MemoryOut) map[string]any {
			return map[string]any{"kind": in.Kind, "sourceKind": in.Source.Kind, "state": out.Memory.State, "memoryId": out.Memory.ID}
		}))
	mcp.AddTool(srv, &mcp.Tool{Name: "memory_update", Description: "기억을 고친다. action: edit(기본)·confirm·stale(낡음)·supersede(supersededBy가 대체)·restore(후보로 되돌림). 사용자 말을 담은 기억(출처 quote)을 바꾸거나 후보를 확정하려면 그렇게 하라는 사용자 말을 userQuote로 준다(그 말이 새 출처로 남는다).", Annotations: writeHints(false, false)},
		track(h, "memory_update", h.update, func(in UpdateIn, out MemoryOut) map[string]any {
			return map[string]any{"action": in.Action, "memoryId": in.ID}
		}))
	mcp.AddTool(srv, &mcp.Tool{Name: "proposals", Description: "감사·교정용 목록. 기본(status=audit)은 감사 목록(최근 자동 확정·자동 정리·고치기와 그 증거·효과 판정, 해로움으로 자동 되돌린 것, 사용자 규칙과 어긋나는 후보, 기준을 넘은 반복 교정·하네스 개선 후보)이다. 사람 승인 대기열은 없다: 사람은 틀린 것만 되돌린다. status=pending은 대기열을 없애기 전에 남은 옛 제안을 대상 기억과 함께 보여 준다(orbit-mem proposals drain으로 정리)", Annotations: readHints()},
		track(h, "proposals", h.proposals, func(in ProposalsIn, out ProposalsOut) map[string]any {
			return map[string]any{"status": in.Status, "results": len(out.Items)}
		}))
	mcp.AddTool(srv, &mcp.Tool{Name: "proposal_decide", Description: "감사 항목을 결정한다. revert: 감사 목록의 자동 변경(proposalId나 ledgerId)을 되돌려 전 판으로(자동 확정은 후보로, 같은 자동 변경은 다시 하지 않는다). dismiss: 어긋남 표시(f-…)나 반복 교정(correction:…)을 확인함으로 닫는다. approve·reject는 남은 옛 대기 제안에만 쓴다(title·body를 주면 고쳐서 승인, 되돌리기·파일 반영·잇기·태그는 고쳐서 승인할 수 없다). ids로 여럿을 같은 답으로 한 번에 결정하며, 실패한 것은 이유와 함께 돌려주고 나머지는 결정한다. 파일 반영·되돌리기는 파일만 바꾸며 커밋하지 않는다. 사용자의 결정을 받은 뒤에만 부른다.", Annotations: writeHints(true, true)},
		track(h, "proposal_decide", h.decide, func(in DecideIn, out DecideOut) map[string]any {
			return map[string]any{"decision": in.Decision, "proposalId": in.ID, "ids": len(in.IDs), "failed": len(out.Failed)}
		}))
	mcp.AddTool(srv, &mcp.Tool{Name: "activity_find", Description: "세션 활동 기록(언제·어느 호스트·어느 프로젝트·무슨 요청)을 찾는다. 기본은 이 프로젝트, project=all이면 전부. 옛 작업 기록(work.db)의 세션과 그 작업 제목도 함께, 출처(source)를 붙여 최신순으로 준다.", Annotations: readHints()},
		track(h, "activity_find", h.activity, func(in ActivityIn, out activity.Result) map[string]any {
			return map[string]any{"query": memory.ClipRunes(memory.MaskSecrets(in.Query), 200), "project": in.Project, "results": len(out.Items)}
		}))
	return srv
}

// readHints and writeHints are the MCP annotations of a tool. Every OpenOrbit tool
// stays on this PC. Without annotations a host such as Codex treats a tool as
// destructive and open-world and asks for approval, which fails under approval
// policy never.
func readHints() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &no}
}

func writeHints(destructive, idempotent bool) *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &no}
}

type handlers struct {
	s *memory.Store
	o Options
}

// track records each call of a tool as a "tool.<name>" event (설계 「측정」).
// The retrospective's own server does not use it: its calls are not use.
func track[In, Out any](h handlers, name string, f mcp.ToolHandlerFor[In, Out], data func(In, Out) map[string]any) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := f(ctx, req, in)
		ok := err == nil && (res == nil || !res.IsError)
		d := map[string]any{}
		if ok {
			d = data(in, out)
		}
		d["ok"] = ok
		_ = h.s.RecordEvent(memory.Event{Kind: memory.EventToolPrefix + name, Scope: h.o.Project}, d)
		return res, out, err
	}
}

// FindIn is memory_find's input.
type FindIn struct {
	Query  string   `json:"query,omitempty" jsonschema:"찾을 말. 비우면 최근 것부터"`
	Kinds  []string `json:"kinds,omitempty" jsonschema:"rule·fact·decision·lesson·journal 중 고른 것"`
	Scope  string   `json:"scope,omitempty" jsonschema:"here(이 폴더에서 보이는 것: user·이 저장소·이 폴더의 제품, 기본)·global(user만)·all"`
	States []string `json:"states,omitempty" jsonschema:"candidate·confirmed·stale. 기본은 candidate와 confirmed"`
	Tags   []string `json:"tags,omitempty" jsonschema:"이 태그 중 하나라도 가진 기억만. 대소문자·앞뒤 공백·-와 _ 차이는 무시한다"`
	Limit  int      `json:"limit,omitempty" jsonschema:"최대 개수(기본 20, 최대 50)"`
}

// Summary is a short view of a memory in a list.
type Summary struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	State     string   `json:"state"`
	Scope     string   `json:"scope"`
	AppliesTo string   `json:"appliesTo"`
	Pinned    bool     `json:"pinned,omitempty"`
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
	return Summary{ID: m.ID, Kind: m.Kind, State: m.State, Scope: m.Scope, AppliesTo: m.AppliesTo, Pinned: m.Pinned, Title: m.Title, Summary: string(r), Tags: m.Tags, Version: m.Version, UpdatedAt: m.UpdatedAt}
}

func toolError(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
}

func (h handlers) find(_ context.Context, _ *mcp.CallToolRequest, in FindIn) (*mcp.CallToolResult, FindOut, error) {
	limit := in.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	f := memory.FindInput{Query: in.Query, Kinds: in.Kinds, States: in.States, Tags: in.Tags, Project: h.o.Project, Limit: limit}
	switch in.Scope {
	case "", "here":
		here := h.o.here()
		f.Place = &here
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
	Kind    string `json:"kind" jsonschema:"quote(사용자 말 그대로)·file·command·session·host_memory·verification(작업 검증이 직접 확인: ref에 명령·작업 id, excerpt에 결과)·work(OpenOrbit 작업 회고가 읽은 끝난 작업: ref에 작업 id)"`
	Ref     string `json:"ref,omitempty" jsonschema:"파일 절대경로, 명령, 세션 id 같은 위치"`
	Excerpt string `json:"excerpt,omitempty" jsonschema:"인용이나 발췌. quote면 사용자 말 그대로"`
}

// SaveIn is memory_save's input.
type SaveIn struct {
	Kind  string   `json:"kind" jsonschema:"rule(사용자가 원하는 방식)·fact·decision·lesson·journal"`
	Title string   `json:"title" jsonschema:"한 줄 제목(200자 이하)"`
	Body  string   `json:"body,omitempty" jsonschema:"짧은 본문"`
	Tags  []string `json:"tags,omitempty"`
	// AppliesTo is 설계 · orbit-mem 2판 4.1 (S3 명세 2.4).
	AppliesTo string `json:"appliesTo,omitempty" jsonschema:"어디에 적용되나. 비우면 이 폴더의 제품(제품을 모르면 product:?, 이 폴더에서만 회상). user(모든 일에 적용: 사용자 말투·보고 방식처럼 정말 어디서나 맞을 때만)·product(이 폴더의 제품)·product:<이름>·product:?·repo(이 저장소에만)·repo:<키>"`
	// Scope and Project are the old names, accepted for one version (0.14.x, S3 명세 2.5).
	Scope   string   `json:"scope,omitempty" jsonschema:"옛 이름(0.15.0에서 뺀다): global=appliesTo user, project=이 저장소(repo). appliesTo를 쓴다"`
	Project string   `json:"project,omitempty" jsonschema:"옛 scope project일 때 프로젝트 디렉터리 절대경로. 비우면 이 세션의 프로젝트"`
	Pin     bool     `json:"pin,omitempty" jsonschema:"true면 상시 핵심으로 승격한다(세션 시작마다 늘 들어감). 사용자가 늘 지키라고 한 확정된 user·제품 기억만. 한도(user 3,000자·제품마다 2,000자)를 넘으면 아무것도 저장하지 않고 거절한다"`
	Source  SourceIn `json:"source"`
	Links   []LinkIn `json:"links,omitempty" jsonschema:"관련 있는 기존 기억과 잇는다(같은 내용이면 잇지 말고 그 기억을 고친다)"`
	// ConflictsWith marks the new candidate as going against these rules of the user (A2).
	ConflictsWith []string `json:"conflictsWith,omitempty" jsonschema:"이 후보와 어긋나는 사용자 규칙 id. 어긋남이 표시된 후보는 자동 확정되지 않고 감사 목록에 보인다"`
	// Retention, TTLDays, ReviewAfterDays and Topic are the lifecycle (기획 · Orbit Code K15).
	Retention       string `json:"retention,omitempty" jsonschema:"보존 등급: volatile(1~7일 뒤 회상에서 빠짐, 이번 주만 맞는 일)·normal(기본)·durable(오래 갈 결정·규칙, 재검토일 기본 90일)"`
	TTLDays         int    `json:"ttlDays,omitempty" jsonschema:"volatile일 때 며칠 갈지(1~7, 기본 7)"`
	ReviewAfterDays int    `json:"reviewAfterDays,omitempty" jsonschema:"며칠 뒤 다시 볼지. 지나면 낡음 후보가 된다(durable 기본 90)"`
	Topic           string `json:"topic,omitempty" jsonschema:"주제 키(예: orbit code 기본 모델). 규칙·결정은 필수다. 같은 범위·주제로 저장하면 옛 현재값을 대체한다(임시인 volatile은 대체하지 않음)"`
	// Supersedes and Tools are 설계 · orbit-mem 2판 4.2·6절.
	Supersedes []string `json:"supersedes,omitempty" jsonschema:"이 저장이 대체하는 옛 기억 id(주제·범위가 달라도). 저장과 함께 낡음 + superseded_by가 된다. AI가 배운 것은 사용자 말을 대체하지 못한다"`
	Tools      []string `json:"tools,omitempty" jsonschema:"이 도구를 부르기 직전에 회상한다: 도구 이름(mcp__atlas__write_page) 또는 *로 끝나는 접두(mcp__atlas__*)"`
}

// LinkIn links to another memory.
type LinkIn struct {
	ID   string `json:"id" jsonschema:"이을 기억 id"`
	Note string `json:"note,omitempty" jsonschema:"왜 관련 있는지 한 줄"`
}

// UnlinkIn removes a link to another memory.
type UnlinkIn struct {
	ID     string `json:"id" jsonschema:"연결을 뗄 상대 기억 id"`
	Reason string `json:"reason,omitempty"`
}

// checkLinks makes sure every target exists and is not self, before anything is written.
func (h handlers) checkLinks(self string, ids ...string) error {
	for _, id := range ids {
		if id == self {
			return fmt.Errorf("%w: 기억을 자기 자신과 이을 수 없습니다", memory.ErrInvalid)
		}
		if _, err := h.s.Get(id); err != nil {
			return err
		}
	}
	return nil
}

func linkIDs(in []LinkIn) []string {
	ids := make([]string, len(in))
	for i, l := range in {
		ids[i] = l.ID
	}
	return ids
}

// applyLinks links and unlinks for memory id and returns it read again (with its links).
func (h handlers) applyLinks(id string, link []LinkIn, unlink []UnlinkIn) (memory.Memory, error) {
	for _, l := range link {
		if l.ID == id {
			continue // 같은 제목 기억에 합쳐져 자기 자신을 가리키게 된 연결
		}
		if _, _, err := h.s.Link(memory.LinkInput{From: id, To: l.ID, Note: l.Note, Origin: memory.LinkOriginAI}); err != nil {
			return memory.Memory{}, err
		}
	}
	for _, u := range unlink {
		if _, err := h.s.Unlink(id, u.ID, memory.LinkOriginAI, u.Reason); err != nil {
			return memory.Memory{}, err
		}
	}
	return h.s.Get(id)
}

// MemoryOut is a single memory result.
type MemoryOut struct {
	Memory memory.Memory `json:"memory"`
	// Rewrites are the relative times memory_save turned into dates ("3주 뒤 → 2026-11-24").
	Rewrites []string `json:"rewrites,omitempty"`
	// Superseded and Conflicts are what memory_save replaced and the user's words it left alone.
	Superseded []string `json:"superseded,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	// Warnings are what the caller should check: the applies-to it got, an old input name (S3 명세 2.4·2.5).
	Warnings []string `json:"warnings,omitempty"`
}

// appliesTo turns appliesTo (or the old scope and project, accepted for one version) into the stored
// scope and the folder label, with what the caller should check (설계 · orbit-mem 2판 4.1, S3 명세 2.4·2.5).
// With neither given and def false it gives "" (no change, memory_update).
func (h handlers) appliesTo(applies, oldScope, dir string, def bool) (string, string, []string, error) {
	here := h.o.here()
	label := h.o.ProjectLabel
	if label == "" {
		label = here.Label()
	}
	var warns []string
	repo := func(d string) (string, string, error) {
		if d != "" {
			p := project.Resolve(d)
			return p.Scope, p.Label, nil
		}
		if h.o.Project == "" {
			return "", "", fmt.Errorf("이 세션의 프로젝트를 모릅니다. repo:<키>로 주거나 project에 디렉터리 절대경로를 주세요")
		}
		return h.o.Project, label, nil
	}
	var aliasScope, aliasLabel string
	switch oldScope {
	case "":
	case "global":
		aliasScope = memory.ScopeGlobal
	case "project":
		sc, lb, err := repo(dir)
		if err != nil {
			return "", "", nil, err
		}
		aliasScope, aliasLabel = sc, lb
	default:
		return "", "", nil, fmt.Errorf("scope(옛 이름)는 global 또는 project입니다. appliesTo를 쓰세요")
	}
	if oldScope != "" {
		warns = append(warns, "scope는 옛 이름입니다 — appliesTo를 쓰세요(global=user, project=repo). 0.15.0에서 뺍니다")
		if applies == "" {
			applies = place.AppliesTo(aliasScope)
		}
	}
	unknown := func() (string, string) {
		warns = append(warns, fmt.Sprintf("제품 미정: 이 폴더(%s)는 제품 지도에서 제품 하나로 정해지지 않아 product:?(이 폴더에서만 회상)로 저장했습니다. 제품의 것이면 appliesTo product:<이름>, 모든 일에 맞으면 user로 고치세요(memory_update appliesTo)", label))
		return place.ScopeUnknown, label
	}
	var scope, lb string
	switch applies {
	case "":
		if !def {
			return "", "", warns, nil
		}
		fallthrough
	case "product":
		if p := here.Product(); p != "" {
			scope = "product:" + p
		} else {
			scope, lb = unknown()
		}
	case "repo":
		sc, l, err := repo(dir)
		if err != nil {
			return "", "", nil, err
		}
		scope, lb = sc, l
	case place.ScopeUnknown:
		scope, lb = place.ScopeUnknown, label
	default:
		sc, ok := place.ScopeOf(applies)
		if !ok {
			return "", "", nil, fmt.Errorf("%w: appliesTo는 user·product·product:<이름>(소문자·숫자·.-_)·product:?·repo·repo:<키>입니다", memory.ErrInvalid)
		}
		scope = sc
		if strings.HasPrefix(sc, "project:") {
			lb = strings.TrimPrefix(sc, "project:")
			if sc == aliasScope && aliasLabel != "" {
				lb = aliasLabel
			}
		}
	}
	if aliasScope != "" && aliasScope != scope {
		return "", "", nil, fmt.Errorf("%w: scope(옛 이름 %s)와 appliesTo(%s)가 다릅니다. appliesTo 하나만 주세요", memory.ErrInvalid, oldScope, applies)
	}
	switch name := place.ProductOf(scope); {
	case scope == memory.ScopeGlobal:
		core, _ := h.s.Core(memory.ScopeGlobal)
		warns = append(warns, fmt.Sprintf("user: 모든 일(모든 저장소·제품)에 적용됩니다. 맞는지 확인하세요 — 한 제품의 일이면 appliesTo product. 지금 user 상시 핵심 %d/3,000자", memory.CoreUsed(core)))
	case name != "":
		if _, ok := h.o.Products[name]; !ok {
			warns = append(warns, fmt.Sprintf("product:%s는 제품 지도(config products)에 없어 어느 폴더에서도 회상되지 않습니다. 제품 지도에 줄을 더하거나 이름을 고치세요", name))
		}
	}
	return scope, lb, warns, nil
}

func (h handlers) save(_ context.Context, _ *mcp.CallToolRequest, in SaveIn) (*mcp.CallToolResult, MemoryOut, error) {
	if in.Source.Kind == memory.SourceLegacy {
		r, _ := toolError(fmt.Errorf("legacy 출처는 orbit-mem migrate만 씁니다"))
		return r, MemoryOut{}, nil
	}
	scope, label, warns, err := h.appliesTo(strings.TrimSpace(in.AppliesTo), in.Scope, in.Project, true)
	if err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	if in.Source.Kind == memory.SourceQuote && strings.TrimSpace(in.Source.Excerpt) == "" {
		r, _ := toolError(fmt.Errorf("quote 출처에는 사용자 말 그대로(excerpt)가 필요합니다"))
		return r, MemoryOut{}, nil
	}
	if err := h.checkLinks("", linkIDs(in.Links)...); err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	if err := h.checkLinks("", in.ConflictsWith...); err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	if err := h.checkClaim(in.Kind, in.Body); err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	if err := h.checkTopic(in, scope); err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	si := memory.SaveInput{Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Scope: scope, Project: label,
		Source: memory.Source{Kind: in.Source.Kind, Ref: in.Source.Ref, Excerpt: in.Source.Excerpt}, ConflictsWith: in.ConflictsWith, Who: h.o.who("", ""),
		Retention: in.Retention, TTLDays: in.TTLDays, ReviewAfterDays: in.ReviewAfterDays, Topic: in.Topic, Supersedes: in.Supersedes, Tools: in.Tools, Pin: in.Pin}
	// 파일 출처는 그때의 해시를 남겨 두어, 파일이 바뀌면 회고의 근거 판정이 낡음 후보를 가린다.
	if si.Source.Kind == memory.SourceFile && filepath.IsAbs(si.Source.Ref) {
		if h := memory.FileHash(si.Source.Ref); h != "absent" {
			si.Source.Hash = h
		}
	}
	// 저장 거부 목록과 상대 시간(기획 · Orbit Code K15).
	rewrites, err := memory.Screen(&si, h.s.Now())
	if err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	m, rep, err := h.s.SaveWith(si)
	if err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	if len(in.Links) > 0 {
		if m, err = h.applyLinks(m.ID, in.Links, nil); err != nil {
			r, _ := toolError(err)
			return r, MemoryOut{}, nil
		}
	}
	if rep.Unpinned != "" {
		warns = append(warns, fmt.Sprintf("상시 핵심에서 빠짐: 대체한 %s는 핵심이었으나 새 판이 한도를 넘어 핵심에 들지 못했습니다. 핵심을 줄이고 memory_update action=pin으로 승격하세요", rep.Unpinned))
	}
	return nil, MemoryOut{Memory: m, Rewrites: rewrites, Superseded: rep.Superseded, Conflicts: rep.Conflicts, Warnings: warns}, nil
}

// maxClaimRunes bounds the body of a rule or decision: one memory holds one claim, so a
// later value replaces all of it (설계 · orbit-mem 2판 4.3.1, AC-9).
const maxClaimRunes = 1000

func (h handlers) checkClaim(kind, body string) error {
	if (kind == memory.KindRule || kind == memory.KindDecision) && utf8.RuneCountInString(body) > maxClaimRunes {
		return fmt.Errorf("%w: 규칙·결정 본문은 %d자 이하입니다(지금 %d자). 한 기억에 주장 하나만 담는다: 주장마다 나눠 저장하고, 긴 경위는 문서 링크로 대신한다",
			memory.ErrInvalid, maxClaimRunes, utf8.RuneCountInString(body))
	}
	return nil
}

// checkTopic refuses a rule or decision without a topic and shows the topics of the
// scope and the three current values most like the new one, so the caller picks the
// slot (설계 · orbit-mem 2판 4.2).
func (h handlers) checkTopic(in SaveIn, scope string) error {
	if (in.Kind != memory.KindRule && in.Kind != memory.KindDecision) || memory.TopicKey(in.Topic) != "" {
		return nil
	}
	live, err := h.s.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return err
	}
	var topics []string
	type near struct {
		m     memory.Memory
		score float64
	}
	var similar []near
	here := h.o.here()
	for _, m := range live {
		if m.Scope != scope && !here.Sees(m.Scope, m.Project) {
			continue
		}
		if m.Topic != "" && m.Scope == scope && !slices.Contains(topics, m.Topic) && len(topics) < 30 {
			topics = append(topics, m.Topic)
		}
		if m.Kind != memory.KindRule && m.Kind != memory.KindDecision {
			continue
		}
		if r := memory.Relevance(m.Doc(), in.Title+" "+in.Body); r.Score > 0 {
			similar = append(similar, near{m, r.Score})
		}
	}
	sort.SliceStable(similar, func(i, j int) bool { return similar[i].score > similar[j].score })
	var b strings.Builder
	fmt.Fprintf(&b, "%v: 규칙·결정에는 topic(주제 키)이 필요합니다. 같은 주제의 옛 값은 이 저장이 대체합니다.", memory.ErrInvalid)
	if len(topics) > 0 {
		b.WriteString("\n이 범위의 기존 주제: " + strings.Join(topics, " · "))
	}
	if len(similar) > 0 {
		b.WriteString("\n비슷한 현재값(같은 주제면 그 topic을 쓰고, 바꾸는 것이면 supersedes에 id를 준다):")
		for i, n := range similar {
			if i == 3 {
				break
			}
			topic := n.m.Topic
			if topic == "" {
				topic = "주제 없음"
			}
			fmt.Fprintf(&b, "\n- %s · %s · topic %s", n.m.ID, n.m.Title, topic)
		}
	}
	return errors.New(b.String())
}

// UpdateIn is memory_update's input.
type UpdateIn struct {
	ID              string     `json:"id"`
	ExpectedVersion int        `json:"expectedVersion,omitempty" jsonschema:"읽은 판본. 주면 그사이 바뀌었을 때 거절한다"`
	Action          string     `json:"action,omitempty" jsonschema:"edit(기본)·confirm·stale·supersede·restore·reviewed(다시 봤고 아직 맞음: 재검토일을 민다)·pin(상시 핵심으로 승격: 확정된 user·제품 기억, 한도를 넘으면 거절)·unpin(핵심에서 내림)"`
	Kind            *string    `json:"kind,omitempty"`
	Title           *string    `json:"title,omitempty"`
	Body            *string    `json:"body,omitempty"`
	Tags            *[]string  `json:"tags,omitempty"`
	AppliesTo       string     `json:"appliesTo,omitempty" jsonschema:"적용 대상을 옮긴다: user·product·product:<이름>·product:?·repo·repo:<키>"`
	Scope           string     `json:"scope,omitempty" jsonschema:"옛 이름(0.15.0에서 뺀다): global=appliesTo user, project=repo"`
	Project         string     `json:"project,omitempty" jsonschema:"옛 scope project일 때 프로젝트 디렉터리 절대경로. 비우면 이 세션의 프로젝트"`
	SupersededBy    string     `json:"supersededBy,omitempty" jsonschema:"supersede일 때 대신하는 기억 id"`
	Reason          string     `json:"reason,omitempty"`
	UserQuote       string     `json:"userQuote,omitempty" jsonschema:"사용자 말을 담은 기억을 바꾸거나 후보를 확정할 때, 그렇게 하라는 사용자 말 그대로. 새 출처로 남는다"`
	Tools           *[]string  `json:"tools,omitempty" jsonschema:"도구 트리거를 바꾼다(빈 목록이면 없앤다): 도구 이름 또는 *로 끝나는 접두"`
	Link            []LinkIn   `json:"link,omitempty" jsonschema:"관련 있는 기억과 잇는다. 연결만 바꾸면 판은 그대로다"`
	Unlink          []UnlinkIn `json:"unlink,omitempty" jsonschema:"연결을 뗀다(기록은 남는다)"`
}

// edits says whether the update changes the memory itself (not only its links).
func (in UpdateIn) edits() bool {
	return in.Action != "" || in.Kind != nil || in.Title != nil || in.Body != nil || in.Tags != nil || in.Tools != nil || in.Scope != "" || in.AppliesTo != "" || in.SupersededBy != ""
}

func (h handlers) update(_ context.Context, _ *mcp.CallToolRequest, in UpdateIn) (*mcp.CallToolResult, MemoryOut, error) {
	var m memory.Memory
	var err error
	var warns []string
	if err := h.checkLinks(in.ID, linkIDs(in.Link)...); err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	if !in.edits() && (len(in.Link) > 0 || len(in.Unlink) > 0) {
		if _, err := h.s.Get(in.ID); err != nil {
			r, _ := toolError(err)
			return r, MemoryOut{}, nil
		}
		if m, err = h.applyLinks(in.ID, in.Link, in.Unlink); err != nil {
			r, _ := toolError(err)
			return r, MemoryOut{}, nil
		}
		return nil, MemoryOut{Memory: m}, nil
	}
	if in.ExpectedVersion != 0 {
		cur, gerr := h.s.Get(in.ID)
		if gerr == nil && cur.Version != in.ExpectedVersion {
			r, _ := toolError(memory.ErrVersionConflict)
			return r, MemoryOut{}, nil
		}
	}
	switch in.Action {
	case "", "edit", "confirm", "restore", "stale":
		u := memory.UpdateInput{ID: in.ID, ExpectedVersion: in.ExpectedVersion, Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Tools: in.Tools, Reason: in.Reason, Action: in.Action,
			Who: h.o.who(in.UserQuote, "")}
		if in.Body != nil || in.Kind != nil { // 고친 뒤의 종류·본문으로 본다
			if cur, gerr := h.s.Get(in.ID); gerr == nil {
				kind, body := cur.Kind, cur.Body
				if in.Kind != nil {
					kind = *in.Kind
				}
				if in.Body != nil {
					body = *in.Body
				}
				if err := h.checkClaim(kind, body); err != nil {
					r, _ := toolError(err)
					return r, MemoryOut{}, nil
				}
			}
		}
		if in.Scope != "" || in.AppliesTo != "" {
			sc, label, w, serr := h.appliesTo(strings.TrimSpace(in.AppliesTo), in.Scope, in.Project, false)
			if serr != nil {
				r, _ := toolError(serr)
				return r, MemoryOut{}, nil
			}
			u.Scope, u.Project = &sc, &label
			warns = w
		}
		state := map[string]string{"confirm": memory.StateConfirmed, "restore": memory.StateCandidate, "stale": memory.StateStale}[in.Action]
		if state != "" {
			u.State = &state
		}
		m, err = h.s.Update(u)
	case "reviewed":
		m, err = h.s.Reviewed(in.ID, h.o.who(in.UserQuote, ""))
	case "pin":
		m, err = h.s.Pin(in.ID, h.o.who(in.UserQuote, ""))
	case "unpin":
		m, err = h.s.Unpin(in.ID, h.o.who(in.UserQuote, ""))
	case "supersede":
		if in.SupersededBy == "" {
			err = fmt.Errorf("supersede에는 supersededBy가 필요합니다")
			break
		}
		m, err = h.s.SupersedeAs(in.ID, in.SupersededBy, in.Reason, h.o.who(in.UserQuote, ""))
	default:
		err = fmt.Errorf("action은 edit·confirm·stale·supersede·restore·reviewed·pin·unpin입니다")
	}
	if err == nil && (len(in.Link) > 0 || len(in.Unlink) > 0) {
		m, err = h.applyLinks(in.ID, in.Link, in.Unlink)
	}
	if err != nil {
		r, _ := toolError(err)
		return r, MemoryOut{}, nil
	}
	return nil, MemoryOut{Memory: m, Warnings: warns}, nil
}

// ProposalsIn is proposals' input.
type ProposalsIn struct {
	Status string `json:"status,omitempty" jsonschema:"audit(기본, 감사 목록)·auto(자동 변경)·pending(남은 옛 대기)·approved·rejected·settled·dropped·all"`
	Days   int    `json:"days,omitempty" jsonschema:"audit: 자동 변경을 볼 날 수(기본 14)"`
}

// ProposalView is a proposal with the memories it concerns.
type ProposalView struct {
	memory.Proposal
	Memories []Summary `json:"memories,omitempty"`
}

// ProposalsOut is proposals' result.
type ProposalsOut struct {
	Items []ProposalView `json:"items"`
	Audit *memory.Audit  `json:"audit,omitempty"` // status=audit
}

func (h handlers) proposals(_ context.Context, _ *mcp.CallToolRequest, in ProposalsIn) (*mcp.CallToolResult, ProposalsOut, error) {
	status := in.Status
	switch status {
	case "all":
		status = ""
	case "", "audit": // 기본은 감사 목록: 사람 승인 대기열은 없다(설계 · orbit-mem 2판 18.6)
		a, err := h.s.Audit(memory.AuditFilter{Days: in.Days})
		if err != nil {
			r, _ := toolError(err)
			return r, ProposalsOut{}, nil
		}
		return nil, ProposalsOut{Items: []ProposalView{}, Audit: &a}, nil
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
	ID       string   `json:"id,omitempty" jsonschema:"결정할 제안 하나"`
	IDs      []string `json:"ids,omitempty" jsonschema:"같은 답으로 한 번에 결정할 제안들(승인·거절만, 고쳐서 승인은 하나씩)"`
	Decision string   `json:"decision" jsonschema:"approve·reject(대기 제안), revert(자동 변경 되돌리기), dismiss(어긋남·반복 교정 확인함)"`
	Title    *string  `json:"title,omitempty" jsonschema:"고쳐서 승인할 제목"`
	Body     *string  `json:"body,omitempty" jsonschema:"고쳐서 승인할 본문(파일 반영이면 새 파일 내용 전체)"`
	Note     string   `json:"note,omitempty" jsonschema:"결정 이유"`
	// UserQuote is the user's words behind a decision on something the user said (A2).
	UserQuote string `json:"userQuote,omitempty" jsonschema:"사용자 말을 담은 기억에 닿는 제안의 승인·거절이나 어긋남 표시 닫기는 그렇게 하라는 사용자 말 그대로"`
}

// DecideOut is proposal_decide's result: one decision, or with ids what was
// decided and what failed with its reason.
type DecideOut struct {
	memory.DecideResult
	Decided []memory.DecideResult `json:"decided,omitempty"`
	Failed  []memory.BatchFailure `json:"failed,omitempty"`
}

func (h handlers) decide(_ context.Context, _ *mcp.CallToolRequest, in DecideIn) (*mcp.CallToolResult, DecideOut, error) {
	if in.Decision == "revert" || in.Decision == "dismiss" {
		ids := in.IDs
		if in.ID != "" {
			ids = append([]string{in.ID}, ids...)
		}
		if len(ids) == 0 || in.Title != nil || in.Body != nil {
			r, _ := toolError(fmt.Errorf("%w: %s에는 id(또는 ids)만 줍니다", memory.ErrInvalid, in.Decision))
			return r, DecideOut{}, nil
		}
		out := DecideOut{}
		for _, id := range ids {
			res, err := h.auditDecide(id, in.Decision, in.Note, in.UserQuote)
			if err != nil {
				if len(ids) == 1 {
					r, _ := toolError(err)
					return r, DecideOut{}, nil
				}
				out.Failed = append(out.Failed, memory.BatchFailure{ID: id, Error: err.Error()})
				continue
			}
			out.Decided = append(out.Decided, res)
		}
		if len(ids) == 1 {
			out.DecideResult, out.Decided = out.Decided[0], nil
		}
		return nil, out, nil
	}
	if len(in.IDs) > 0 {
		if in.Title != nil || in.Body != nil {
			r, _ := toolError(fmt.Errorf("%w: 고쳐서 승인은 하나씩 id로 합니다", memory.ErrInvalid))
			return r, DecideOut{}, nil
		}
		if in.Decision != "approve" && in.Decision != "reject" {
			r, _ := toolError(fmt.Errorf("%w: decision은 approve 또는 reject", memory.ErrInvalid))
			return r, DecideOut{}, nil
		}
		ids := in.IDs
		if in.ID != "" {
			ids = append([]string{in.ID}, ids...)
		}
		if !slices.ContainsFunc(ids, func(id string) bool { return strings.TrimSpace(id) != "" }) {
			r, _ := toolError(fmt.Errorf("%w: 결정할 제안 id가 비었습니다", memory.ErrInvalid))
			return r, DecideOut{}, nil
		}
		var allowed []string
		var refused []memory.BatchFailure
		for _, id := range ids {
			if err := h.needsWords(id, in.UserQuote); err != nil {
				refused = append(refused, memory.BatchFailure{ID: id, Error: err.Error()})
				continue
			}
			allowed = append(allowed, id)
		}
		b := h.s.DecideMany(allowed, in.Decision, in.Note)
		b.Failed = append(refused, b.Failed...)
		msg := fmt.Sprintf("%d건 결정, %d건 실패", len(b.Decided), len(b.Failed))
		for _, f := range b.Failed {
			msg += "\n- " + f.ID + ": " + f.Error
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, DecideOut{Decided: b.Decided, Failed: b.Failed}, nil
	}
	if in.ID == "" {
		r, _ := toolError(fmt.Errorf("%w: id나 ids가 필요합니다", memory.ErrInvalid))
		return r, DecideOut{}, nil
	}
	if err := h.needsWords(in.ID, in.UserQuote); err != nil {
		r, _ := toolError(err)
		return r, DecideOut{}, nil
	}
	res, err := h.s.Decide(memory.DecideInput{ID: in.ID, Decision: in.Decision, Title: in.Title, Body: in.Body, Note: in.Note})
	if err != nil {
		r, _ := toolError(err)
		return r, DecideOut{}, nil
	}
	return nil, DecideOut{DecideResult: res}, nil
}

// auditDecide settles one audit item: revert an automatic change (by its
// proposal or ledger id), or dismiss a conflict mark or a repeated correction.
func (h handlers) auditDecide(id, decision, note, quote string) (memory.DecideResult, error) {
	w := h.o.who(quote, "")
	switch {
	case decision == "revert":
		return h.s.RevertChange(id, w, note)
	case strings.HasPrefix(id, "correction:"):
		return memory.DecideResult{}, h.s.Acknowledge(id, w)
	default:
		// 어긋남을 닫으면 그 후보가 자동 확정될 수 있다: 사람이 아니면 사용자 말이 있어야 한다.
		if w.Actor != memory.ActorUser && strings.TrimSpace(quote) == "" {
			return memory.DecideResult{}, fmt.Errorf("%w: %s", memory.ErrProtected, id)
		}
		return memory.DecideResult{}, h.s.ResolveFlag(id, "dismissed", w)
	}
}

// needsWords refuses a decision by a writer other than the person on a
// proposal that touches what the user said, unless the user's words come with it.
func (h handlers) needsWords(id, quote string) error {
	if h.o.who("", "").Actor == memory.ActorUser || strings.TrimSpace(quote) != "" {
		return nil
	}
	p, err := h.s.GetProposal(id)
	if err != nil {
		return nil // 없는 제안은 결정에서 알린다
	}
	for _, mid := range p.MemoryIDs {
		if prot, err := h.s.Protected(mid); err == nil && prot {
			return fmt.Errorf("%w: %s", memory.ErrProtected, mid)
		}
	}
	return nil
}
