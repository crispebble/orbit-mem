// Package mcpserver is orbit-mem's MCP server: six tools to find, read, save and fix
// memories and to see and decide proposals, and activity_find over the activity log.
package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/activity"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/project"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options says where the server runs.
type Options struct {
	Project      string // 이 세션의 프로젝트 범위("project:<키>")
	ProjectLabel string
	Version      string
	// Actor is who writes through this server (A2): ai for a host's AI (the
	// default), user for a person at a terminal, automation for a job. A tool
	// argument never changes it.
	Actor string
	// TrustLimit is the highest trust a source saved through this connection may get (the channel it came
	// by): the assembly point sets it, never the actor or a tool argument. Empty is agent; the thin CLI of
	// a person sets owner.
	TrustLimit string
	// Dir is the session folder (a real slash path), whose parents' memories the session sees (S3-R2),
	// Git says it is in a git checkout and Temp under a temporary folder: they pick the default appliesTo
	// (S3-R1·R4). Empty, the folder of a folder scope is used.
	Dir       string
	Git, Temp bool
	// Products is the product map (config products): which product this session's folder belongs to,
	// the default of appliesTo and what memory_find sees here (설계 · orbit-mem 2판 S3).
	Products place.Map
	// LoadProducts, when set, reads the product map on every call instead of Products: a long-lived server
	// follows a config.json changed after it started, like the hooks do.
	LoadProducts func() (place.Map, error)
	// SaveSimilarMin is the similarity score (0~1, two-way token cover between the two memories) a memory of the same scope needs for memory_save to show it as
	// similar and save nothing until the caller picks (config saveSimilarMin, 설계 6.1). 0 or less is
	// memory.DefaultSimilarMin. LoadSaveSimilarMin, when set, reads it on every save instead (a long-lived
	// server follows config.json like LoadProducts).
	SaveSimilarMin     float64
	LoadSaveSimilarMin func() float64
	// SaveNearMin is the floor (0~1, the same score) of the close memories memory_save shows beside a save it
	// does not stop (config saveNearMin, 1-6 b6dn). 0 or less is memory.DefaultNearMin. LoadSaveNearMin, when
	// set, reads it on every save instead.
	SaveNearMin     float64
	LoadSaveNearMin func() float64
}

// checkMins are the scores of memory_save's judgment now: the threshold of its similar memories and the floor
// of its close ones.
func (o Options) checkMins() memory.CheckMins {
	m := memory.CheckMins{Similar: o.SaveSimilarMin, Near: o.SaveNearMin}
	if o.LoadSaveSimilarMin != nil {
		m.Similar = o.LoadSaveSimilarMin()
	}
	if o.LoadSaveNearMin != nil {
		m.Near = o.LoadSaveNearMin()
	}
	return m
}

// products is the product map now, and why it could not be read.
func (o Options) products() (place.Map, error) {
	if o.LoadProducts != nil {
		return o.LoadProducts()
	}
	return o.Products, nil
}

// here is the place of this connection's folder.
func (o Options) here() place.Place {
	m, _ := o.products()
	return place.At(o.Project, o.Dir, m)
}

// ResolveActor picks the writer of a server or command: the flag, else
// ORBIT_ACTOR, else def. Unknown values fall back.
func ResolveActor(flag, env, def string) string {
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

const instructions = `orbit-mem은 이 PC의 공유 기억이다. 일이 규칙·결정·교훈에 닿을 법하면 memory_find로 먼저 찾는다(결과는 id·제목 색인). 쓸 기억은 memory_get으로 본문을 읽는다(memory_get의 links는 함께 읽을 관련 기억). 부르는 때: 설계·결정·규칙을 확인할 때, 문서 위치·작업 방식이 애매할 때, 사용자가 "전에"를 말할 때.
사용자가 "기억해"라고 하거나 다시 쓸 만한 사실·결정·교훈이 생기면 memory_save로 출처와 함께 남긴다. 사용자 말 그대로의 출처(kind=quote)면 바로 확정, 아니면 후보이고 증거가 쌓이면 스스로 확정된다. 함께 읽어야 할 기존 기억은 links로 잇는다. 틀린 기억은 memory_update로 고치고, 사용자 말을 담은 기억은 사용자가 바꾸라고 한 말을 userQuote로 줄 때만 고친다.
규칙·결정은 topic(주제 키)이 있어야 하고, 규칙은 appliesTo(사람의 일하는 방식은 대개 user)가 있어야 하며, 본문은 한 주장에 1,000자 이하다. 아니면 거절된다. 주제가 다른 옛 판을 바꾸는 결정이면 supersedes에 그 id를 준다. 「이번엔」처럼 임시라는 말은 retention volatile로 남기고, 옛 결정을 바꾸는 말인지 애매하면 저장 전에 사용자에게 묻는다.
pin은 사용자가 늘 지키라고 한 확정된 user·제품 기억만 상시 핵심에 올린다(한도 user 3,000자·제품마다 2,000자, 넘으면 거절). 도구를 부르기 직전에 떠올릴 규칙은 tools에 도구 이름이나 접두(mcp__atlas__*)를 단다.
proposals·proposal_decide는 감사용이라 사용자가 감사 목록을 요청할 때만 쓴다. 이전 세션·이어 할 일은 activity_find로 찾는다. 자세한 규칙은 memory·proposals 스킬에 있다.`

// New builds the server over the store.
func New(s *memory.Store, o Options) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "orbit-mem", Version: o.Version}, &mcp.ServerOptions{Instructions: instructions})
	h := handlers{s: s, o: o}
	// memory_save and memory_update only add: an edit, stale or supersede keeps
	// every earlier version. proposal_decide can overwrite or remove a user's
	// file (apply_file, revert); a decided proposal is not decided again.
	addPlain(srv, &mcp.Tool{Name: "memory_find", Description: "기억을 찾는다. 설계·결정·규칙 확인, 문서 위치·작업 방식이 애매할 때 먼저 부른다. 결과는 색인이라 쓰기 전에 memory_get으로 읽는다.", Annotations: readHints()},
		track(h, "memory_find", h.find, func(in FindIn, out FindOut) map[string]any {
			ids := make([]string, 0, len(out.Items))
			for _, m := range out.Items {
				ids = append(ids, m.ID)
			}
			return map[string]any{"query": memory.ClipRunes(memory.MaskSecrets(in.Query), 200), "results": len(out.Items), "scope": in.Scope, "ids": ids, "tags": len(in.Tags)}
		}))
	addPlain(srv, &mcp.Tool{Name: "memory_get", Description: "id로 기억을 읽는다(여러 개). 본문·출처·links(함께 읽을 관련 기억).", Annotations: readHints()},
		track(h, "memory_get", h.get, func(in GetIn, out GetOut) map[string]any {
			ids := make([]string, 0, len(out.Items))
			for _, m := range out.Items {
				ids = append(ids, m.ID)
			}
			return map[string]any{"requested": len(in.IDs), "results": len(out.Items), "ids": ids}
		}))
	addPlain(srv, &mcp.Tool{Name: "memory_save", Description: "기억을 출처와 함께 남긴다. 출처가 사용자 말(quote)이면 확정, 아니면 후보. 규칙·결정은 topic 필수, 규칙은 appliesTo 필수, 본문 1,000자 이하. 비밀값·로그·전사·지시문·진행 상태는 거절된다. saved:false면 similar를 보고 choices 하나를 resolve로 주어 다시 부른다. 저장됐는데 near가 있으면 같은 주장인지 보고 하나만 남긴다.", Annotations: writeHints(false, false)},
		track(h, "memory_save", h.save, func(in SaveIn, out SaveOut) map[string]any {
			return saveData(in, out)
		}))
	addPlain(srv, &mcp.Tool{Name: "memory_update", Description: "기억을 고친다. action: edit(기본)·confirm·stale·supersede·restore·reviewed·pin·unpin. 사용자 말을 담은 기억(quote)을 바꾸거나 후보를 확정하면 사용자 말을 userQuote로 준다.", Annotations: writeHints(false, false)},
		track(h, "memory_update", h.update, func(in UpdateIn, out MemoryOut) map[string]any {
			return map[string]any{"action": in.Action, "memoryId": in.ID}
		}))
	addPlain(srv, &mcp.Tool{Name: "proposals", Description: "감사용 목록. 기본(status=audit)은 최근 자동 변경·어긋남·반복 교정. 사용자가 요청할 때만 부른다.", Annotations: readHints()},
		track(h, "proposals", h.proposals, func(in ProposalsIn, out ProposalsOut) map[string]any {
			return map[string]any{"status": in.Status, "results": len(out.Items)}
		}))
	addPlain(srv, &mcp.Tool{Name: "proposal_decide", Description: "감사 항목 결정: revert(자동 변경 되돌리기)·dismiss(어긋남·반복 교정 닫기). 사용자 답을 받은 뒤에만 부른다. approve·reject는 남은 옛 대기 제안만.", Annotations: writeHints(true, true)},
		track(h, "proposal_decide", h.decide, func(in DecideIn, out DecideOut) map[string]any {
			return map[string]any{"decision": in.Decision, "proposalId": in.ID, "ids": len(in.IDs), "failed": len(out.Failed)}
		}))
	addPlain(srv, &mcp.Tool{Name: "activity_find", Description: "세션 활동 기록(언제·어느 호스트·프로젝트·요청)을 최신순으로 찾는다. 기본은 이 프로젝트, project=all이면 전부.", Annotations: readHints()},
		track(h, "activity_find", h.activity, func(in ActivityIn, out activity.Result) map[string]any {
			return map[string]any{"query": memory.ClipRunes(memory.MaskSecrets(in.Query), 200), "project": in.Project, "results": len(out.Items)}
		}))
	return srv
}

// readHints and writeHints are the MCP annotations of a tool. Every orbit-mem tool
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
	Kinds  []string `json:"kinds,omitempty" jsonschema:"rule·fact·decision·lesson·journal"`
	Scope  string   `json:"scope,omitempty" jsonschema:"here(기본, 이 폴더에서 보이는 것)·global(user만)·all"`
	States []string `json:"states,omitempty" jsonschema:"candidate·confirmed·stale(기본 앞 둘)"`
	Tags   []string `json:"tags,omitempty" jsonschema:"하나라도 가진 기억만"`
	Limit  int      `json:"limit,omitempty" jsonschema:"기본 8, 최대 30"`
	Detail string   `json:"detail,omitempty" jsonschema:"index(기본)·summary(본문 발췌·태그 포함)"`
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

// FindOut is memory_find's result: index lines (view.go).
type FindOut struct {
	Items []FindItem `json:"items"`
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
	detail, err := findDetail(in.Detail)
	if err != nil {
		r, _ := toolError(err)
		return r, FindOut{}, nil
	}
	f := memory.FindInput{Query: in.Query, Kinds: in.Kinds, States: in.States, Tags: in.Tags, Project: h.o.Project, Limit: findLimit(in.Limit)}
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
	out := FindOut{Items: []FindItem{}}
	for _, m := range ms {
		out.Items = append(out.Items, findItem(m, detail))
	}
	return nil, out, nil
}

// GetIn is memory_get's input.
type GetIn struct {
	IDs     []string `json:"ids" jsonschema:"읽을 기억 id(1~20개)"`
	History bool     `json:"history,omitempty" jsonschema:"true면 버전 이력도"`
	Full    bool     `json:"full,omitempty" jsonschema:"true면 자르지 않고 전부"`
}

// GetOut is memory_get's result.
type GetOut struct {
	Items   []MemoryView                `json:"items"`
	History map[string][]memory.Version `json:"history,omitempty"`
}

func (h handlers) get(_ context.Context, _ *mcp.CallToolRequest, in GetIn) (*mcp.CallToolResult, GetOut, error) {
	out := GetOut{Items: []MemoryView{}}
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
		out.Items = append(out.Items, memoryView(m, h.linkTitle, in.Full))
		if in.History {
			if out.History == nil {
				out.History = map[string][]memory.Version{}
			}
			out.History[id], _ = h.s.History(id)
		}
	}
	return nil, out, nil
}

// linkTitle is the title of the memory at the other end of a link, "" when it cannot be read.
func (h handlers) linkTitle(id string) string {
	m, err := h.s.Get(id)
	if err != nil {
		return ""
	}
	return m.Title
}

// SourceIn is where a memory came from.
type SourceIn struct {
	Kind    string `json:"kind" jsonschema:"quote(사용자 말)·file·command·session·host_memory·verification·work"`
	Ref     string `json:"ref,omitempty" jsonschema:"절대경로·명령·세션 id"`
	Excerpt string `json:"excerpt,omitempty" jsonschema:"quote면 사용자 말 그대로"`
	Trust   string `json:"trust,omitempty" jsonschema:"untrusted(웹·도구 출력)·system으로만 낮춤"`
}

// SaveIn is memory_save's input.
type SaveIn struct {
	Kind  string   `json:"kind" jsonschema:"rule·fact·decision·lesson"`
	Title string   `json:"title" jsonschema:"200자 이하"`
	Body  string   `json:"body,omitempty" jsonschema:"규칙·결정은 1,000자 이하"`
	Tags  []string `json:"tags,omitempty"`
	// AppliesTo is 설계 · orbit-mem 2판 4.1 (S3 명세 2.4).
	AppliesTo string `json:"appliesTo,omitempty" jsonschema:"규칙은 필수(사람의 일하는 방식은 대개 user). user·product[:<이름>|:?]·repo[:<키>]. 비우면 폴더 기준"`
	// Scope and Project are the old names, accepted for one version (0.14.x, S3 명세 2.5).
	Scope   string   `json:"scope,omitempty" jsonschema:"옛 이름(global=user, project=repo). appliesTo를 쓴다"`
	Project string   `json:"project,omitempty" jsonschema:"옛 이름: scope project의 디렉터리"`
	Pin     bool     `json:"pin,omitempty" jsonschema:"상시 핵심으로 올린다. 사용자가 늘 지키라고 한 확정 기억만, 한도(user 3,000자·제품 2,000자) 넘으면 거절"`
	Source  SourceIn `json:"source"`
	Links   []LinkIn `json:"links,omitempty" jsonschema:"함께 읽을 기억"`
	// ConflictsWith marks the new candidate as going against these rules of the user (A2).
	ConflictsWith []string `json:"conflictsWith,omitempty" jsonschema:"어긋나는 사용자 규칙 id"`
	// Retention, TTLDays, ReviewAfterDays and Topic are the lifecycle (기획 · Orbit Code K15).
	Retention       string `json:"retention,omitempty" jsonschema:"volatile(이번 주만)·normal(기본)·durable"`
	TTLDays         int    `json:"ttlDays,omitempty" jsonschema:"volatile 유지일(1~7)"`
	ReviewAfterDays int    `json:"reviewAfterDays,omitempty" jsonschema:"재검토까지 일수"`
	Topic           string `json:"topic,omitempty" jsonschema:"주제 키. 규칙·결정은 필수"`
	// Supersedes and Tools are 설계 · orbit-mem 2판 4.2·6절.
	Supersedes []string `json:"supersedes,omitempty" jsonschema:"대체하는 옛 기억 id(주제가 달라도)"`
	Tools      []string `json:"tools,omitempty" jsonschema:"부르기 직전에 회상할 도구 이름·접두"`
	// Resolve and IdempotencyKey are the save-time tidy of 설계 6.1 (handlers.tidy).
	Resolve        string `json:"resolve,omitempty" jsonschema:"saved:false 뒤 고른 것: add·update:<id>·supersede:<id>·skip"`
	IdempotencyKey string `json:"idempotencyKey,omitempty" jsonschema:"같은 키로 다시 불러도 한 번만 적용"`
}

// LinkIn links to another memory.
type LinkIn struct {
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
}

// UnlinkIn removes a link to another memory.
type UnlinkIn struct {
	ID     string `json:"id"`
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
	Memory memory.Memory `json:"memory,omitzero"` // memory_save가 고르기를 돌려줄 때만 없다
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
// text is the title and body of a memory being saved: in a folder of several products, the one product it
// names is the default (S3-R2b).
func (h handlers) appliesTo(applies, oldScope, dir string, def bool, text string) (string, string, []string, error) {
	var warns []string
	products, perr := h.o.products()
	if perr != nil {
		warns = append(warns, fmt.Sprintf("제품 지도(config.json products)를 읽지 못해 제품이 없는 것으로 봤습니다: %v. 설정을 고친 뒤 적용 대상을 확인하세요", perr))
	}
	here := place.At(h.o.Project, h.o.Dir, products)
	label := h.o.ProjectLabel
	if label == "" {
		label = here.Label()
	}
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
	// 폴더 키: 세션 폴더(git 밖이면 범위 키와 같다).
	folder := here.Dir
	if folder == "" {
		folder = label
	}
	unknown := func() (string, string) {
		switch {
		case h.o.Temp:
			warns = append(warns, fmt.Sprintf("임시 폴더(%s)라 product:?(이 폴더와 그 아래에서만 회상)로 저장했습니다. 다른 곳에서도 쓸 것이면 appliesTo user·product:<이름>·repo:<키>로 고치세요(memory_update appliesTo)", folder))
		case h.o.Git:
			warns = append(warns, fmt.Sprintf("폴더에 묶음: 이 폴더(%s)는 제품 지도에서 제품 하나로 정해지지 않아 product:?(이 폴더와 그 아래에서만 회상)로 저장했습니다. 제품의 것이면 appliesTo product:<이름>, 이 저장소의 것이면 repo, 모든 일에 맞으면 user로 고치세요(memory_update appliesTo)", folder))
		default:
			warns = append(warns, fmt.Sprintf("폴더에 묶음: 이 폴더(%s)는 제품 지도에서 제품 하나로 정해지지 않고 git 저장소도 아니라 product:?(이 폴더와 그 아래에서만 회상)로 저장했습니다. 제품의 것이면 appliesTo product:<이름>, 모든 일에 맞으면 user로 고치세요(memory_update appliesTo)", folder))
		}
		return place.ScopeUnknown, folder
	}
	// 기본값(S3-R1): 그 폴더의 제품 → 여러 제품 폴더면 글에 이름이 하나 나온 제품(S3-R2b) → git 안이면 그 저장소 → 그 폴더.
	// 임시 폴더는 늘 그 폴더다(S3-R4).
	byDefault := func() (string, string) {
		switch sc := here.Default(h.o.Git, h.o.Temp, text); {
		case sc == place.ScopeUnknown:
			return unknown()
		case strings.HasPrefix(sc, "project:"):
			return sc, label
		default:
			return sc, ""
		}
	}
	var scope, lb string
	switch applies {
	case "":
		if !def {
			return "", "", warns, nil
		}
		scope, lb = byDefault()
	case "product":
		switch p := here.Product(); {
		case p != "":
			scope = "product:" + p
		case here.Multi() && here.Narrow(text) != "":
			scope = "product:" + here.Narrow(text)
		default:
			scope, lb = unknown()
		}
	case "repo":
		sc, l, err := repo(dir)
		if err != nil {
			return "", "", nil, err
		}
		scope, lb = sc, l
	case place.ScopeUnknown:
		scope, lb = place.ScopeUnknown, folder
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
		if _, ok := products[name]; !ok && perr == nil {
			warns = append(warns, fmt.Sprintf("product:%s는 제품 지도(config products)에 없어 어느 폴더에서도 회상되지 않습니다. 제품 지도에 줄을 더하거나 이름을 고치세요", name))
		}
	}
	return scope, lb, warns, nil
}

// checkRuleApplies refuses a rule saved without saying whom it applies to (S3-R3): a person's way of
// working is usually user, and a rule bound to this folder by default would be missing everywhere else.
// The refusal names the choices and this folder's products.
func (h handlers) checkRuleApplies(in SaveIn) error {
	if in.Kind != memory.KindRule || strings.TrimSpace(in.AppliesTo) != "" || in.Scope != "" {
		return nil
	}
	here := h.o.here()
	products := "없음(제품 지도에 이 폴더가 없다)"
	if len(here.Products) > 0 {
		products = "product:" + strings.Join(here.Products, ", product:")
	}
	return fmt.Errorf("%w: 규칙(kind=rule)은 appliesTo가 있어야 저장합니다. 고를 것: user(모든 일 — 사람의 일하는 방식은 대개 이것), product:<이름>(한 제품), repo(이 저장소), product:?(이 폴더와 그 아래). 이 폴더(%s)의 제품: %s",
		memory.ErrInvalid, folderOf(here), products)
}

func (h handlers) save(_ context.Context, _ *mcp.CallToolRequest, in SaveIn) (*mcp.CallToolResult, SaveOut, error) {
	if in.Source.Kind == memory.SourceLegacy {
		r, _ := toolError(fmt.Errorf("legacy 출처는 orbit-mem migrate만 씁니다"))
		return r, SaveOut{}, nil
	}
	if err := h.checkRuleApplies(in); err != nil {
		r, _ := toolError(err)
		return r, SaveOut{}, nil
	}
	scope, label, warns, err := h.appliesTo(strings.TrimSpace(in.AppliesTo), in.Scope, in.Project, true, in.Title+"\n"+in.Body)
	if err != nil {
		r, _ := toolError(err)
		return r, SaveOut{}, nil
	}
	if in.Source.Kind == memory.SourceQuote && strings.TrimSpace(in.Source.Excerpt) == "" {
		r, _ := toolError(fmt.Errorf("quote 출처에는 사용자 말 그대로(excerpt)가 필요합니다"))
		return r, SaveOut{}, nil
	}
	if err := h.checkLinks("", linkIDs(in.Links)...); err != nil {
		r, _ := toolError(err)
		return r, SaveOut{}, nil
	}
	if err := h.checkLinks("", in.ConflictsWith...); err != nil {
		r, _ := toolError(err)
		return r, SaveOut{}, nil
	}
	if err := h.checkClaim(in.Kind, in.Body); err != nil {
		r, _ := toolError(err)
		return r, SaveOut{}, nil
	}
	if err := h.checkTopic(in, scope); err != nil {
		r, _ := toolError(err)
		return r, SaveOut{}, nil
	}
	si := memory.SaveInput{Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Scope: scope, Project: label,
		Source: memory.Source{Kind: in.Source.Kind, Ref: in.Source.Ref, Excerpt: in.Source.Excerpt, Trust: in.Source.Trust}, TrustLimit: h.o.TrustLimit, ConflictsWith: in.ConflictsWith, Who: h.o.who("", ""),
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
		return r, SaveOut{}, nil
	}
	out, err := h.tidy(in, si)
	if err != nil {
		r, _ := toolError(err)
		return r, SaveOut{}, nil
	}
	out.Rewrites, out.Warnings = rewrites, append(warns, out.Warnings...)
	return nil, out, nil
}

// 저장 때 정리(설계 6.1, ORBIT-25 G10, 계획 D8·D9·M7). memory_save(MCP·host call·tool이 모두 이 처리기를 지난다)는
// 저장하기 전에 도메인 판정(memory.Store.SaveCheck)을 본다: 같은 제목·같은 주제면 지금처럼 저장하고(단축·대체),
// 같은 범위에 비슷한 기억만 있으면 저장하지 않고 고르기를 돌려준다. 부르는 쪽이 resolve로 고르면 그대로 적용한다.
// idempotencyKey가 있으면 인자 해시와 함께 도메인에 넘겨 적용된 쓰기를 한 번만 하고, 같은 키가 다시 오면 기록한
// 기억을 지금 상태로 다시 그려 돌려준다(링크는 멱등으로 다시 건다). 내부 Save는 이 판정을 하지 않는다.

// The choices of resolve.
const (
	resolveAdd       = "add"
	resolveUpdate    = "update"
	resolveSupersede = "supersede"
	resolveSkip      = "skip"
)

// SaveOut is memory_save's result. Saved is always there: true when the memory was saved, updated or
// superseded (Memory), false when nothing was written — similar memories to pick from (Similar, Choices),
// or the caller picked skip (Resolve, Memory the one kept).
type SaveOut struct {
	Saved bool `json:"saved"`
	MemoryOut
	Resolve string       `json:"resolve,omitempty"`
	Similar []SimilarOut `json:"similar,omitempty"`
	Choices []string     `json:"choices,omitempty"`
	// Near is one line of the close memories (id and title, at most memory.SimilarLimit) beside a save that was
	// not stopped, so the caller may merge or fix them (1-6 b6dn). Empty when there are none.
	Near string `json:"near,omitempty"`
}

// SimilarOut is a memory like the one about to be saved. Its score stays inside (M5).
type SimilarOut struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"` // 본문 한 줄, 200자 이하
	Topic   string `json:"topic,omitempty"`
	Trust   string `json:"trust,omitempty"` // 믿을 수 없는 기억(untrusted·system)만
}

// saveData is the use event data of a memory_save call.
func saveData(in SaveIn, out SaveOut) map[string]any {
	action, _, _ := parseResolve(in.Resolve)
	return map[string]any{"kind": in.Kind, "sourceKind": in.Source.Kind, "state": out.Memory.State, "memoryId": out.Memory.ID,
		"saved": out.Saved, "similar": len(out.Similar), "near": out.Near != "", "resolve": action}
}

// parseResolve splits resolve into its choice and the memory it names: add, update:<id>, supersede:<id>,
// skip or skip:<id>. Empty is no choice yet.
func parseResolve(r string) (string, string, error) {
	r = strings.TrimSpace(r)
	if r == "" {
		return "", "", nil
	}
	choice, id, named := strings.Cut(r, ":")
	choice, id = strings.TrimSpace(choice), strings.TrimSpace(id)
	switch {
	case choice == resolveAdd && !named,
		choice == resolveSkip && (!named || id != ""),
		(choice == resolveUpdate || choice == resolveSupersede) && id != "":
		return choice, id, nil
	}
	return "", "", i18n.Errorf("memory.save.resolve", memory.ErrInvalid, r)
}

// saveHash is the hash of a memory_save call's arguments for its idempotency key: the decoded input
// written again as JSON (so key order and spacing do not matter), resolve in, the key itself out. The
// host and session are not in it: a key is not bound to the caller (M7).
func saveHash(in SaveIn) string {
	in.IdempotencyKey, in.Resolve = "", strings.TrimSpace(in.Resolve)
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tidy saves si (the checked input of in) by the save-time tidy.
func (h handlers) tidy(in SaveIn, si memory.SaveInput) (SaveOut, error) {
	choice, target, err := parseResolve(in.Resolve)
	if err != nil {
		return SaveOut{}, err
	}
	if key := in.IdempotencyKey; key != "" {
		si.Idem = &memory.Idem{Key: key, Hash: saveHash(in)}
		// 같은 키의 재시도는 고르기를 다시 받지 않는다. 실제 적용은 도메인이 쓰기 트랜잭션 안에서 다시 확인한다.
		r, ok, err := h.s.SaveRequest(key)
		if err != nil {
			return SaveOut{}, err
		}
		if ok {
			if r.Hash != si.Idem.Hash {
				return SaveOut{}, memory.ErrIdempotencyConflict
			}
			return h.replay(r, in.Links)
		}
	}
	switch choice {
	case "":
		chk, err := h.s.SaveCheck(si, h.o.checkMins())
		if err != nil {
			return SaveOut{}, err
		}
		if chk.Verdict == memory.CheckSimilar {
			return similarOut(chk.Similar), nil
		}
		out, err := h.saveNow(si, in.Links)
		if err != nil {
			return SaveOut{}, err
		}
		// 막지 않은 저장에만 가까운 기억을 붙인다: 같은 내용이면 부른 AI가 합치거나 고친다(1-6 b6dn).
		out.Near = nearLine(nearOf(chk.Near, out))
		return out, nil
	case resolveUpdate:
		return h.saveAsUpdate(target, in, si)
	case resolveSupersede:
		si.Replaces = target
	case resolveSkip:
		return h.skip(target, si)
	}
	return h.saveNow(si, in.Links)
}

// similarOut is the answer that saves nothing: the similar memories and the choices of resolve.
func similarOut(sim []memory.Similar) SaveOut {
	out := SaveOut{Choices: []string{resolveAdd}}
	for _, m := range sim {
		out.Similar = append(out.Similar, SimilarOut{ID: m.ID, Title: m.Title, Summary: m.Summary, Topic: m.Topic, Trust: untrustedOf(memory.Memory{Trust: m.Trust})})
		out.Choices = append(out.Choices, resolveUpdate+":"+m.ID)
	}
	for _, m := range sim {
		out.Choices = append(out.Choices, resolveSupersede+":"+m.ID)
	}
	out.Choices = append(out.Choices, resolveSkip)
	return out
}

// replay draws again, as it is now, the memory an earlier call with the same key applied to, with the
// links of this call (linking is idempotent, so the result never comes without them).
func (h handlers) replay(r memory.SaveRequest, links []LinkIn) (SaveOut, error) {
	if r.Action == memory.ApplySkip {
		m, err := h.s.Get(r.MemoryID)
		return SaveOut{Resolve: resolveSkip, MemoryOut: MemoryOut{Memory: m}}, err
	}
	m, err := h.applyLinks(r.MemoryID, links, nil)
	return SaveOut{Saved: true, MemoryOut: MemoryOut{Memory: m}}, err
}

// saveNow saves si (with its idempotency key and the memory it replaces, if any) and links the result.
func (h handlers) saveNow(si memory.SaveInput, links []LinkIn) (SaveOut, error) {
	m, rep, err := h.s.SaveWith(si)
	if err != nil {
		return SaveOut{}, err
	}
	if len(links) > 0 || rep.Replayed != nil {
		if m, err = h.applyLinks(m.ID, links, nil); err != nil {
			return SaveOut{}, err
		}
	}
	out := SaveOut{Saved: true, MemoryOut: MemoryOut{Memory: m, Superseded: rep.Superseded, Conflicts: rep.Conflicts}}
	if rep.Unpinned != "" {
		out.Warnings = append(out.Warnings, fmt.Sprintf("상시 핵심에서 빠짐: 대체한 %s는 핵심이었으나 새 판이 한도를 넘어 핵심에 들지 못했습니다. 핵심을 줄이고 memory_update action=pin으로 승격하세요", rep.Unpinned))
	}
	if si.Replaces != "" && rep.Replayed == nil && !slices.Contains(rep.Superseded, si.Replaces) {
		out.Warnings = append(out.Warnings, i18n.T("memory.save.not_replaced", si.Replaces))
	}
	return out, nil
}

// saveAsUpdate applies resolve update:<id>: the memory id takes the title, body, tags, topic and tools of
// the save (what the save leaves empty stays) by the Update path, so the user's words are kept by the same
// guard; the save's source is added to it, in the same transaction, as one more ground. A source that is the user's own words is
// the userQuote of the change (D1).
func (h handlers) saveAsUpdate(id string, in SaveIn, si memory.SaveInput) (SaveOut, error) {
	if !slices.Contains(memory.SourceKinds, si.Source.Kind) { // 일반 저장과 같은 안내
		return SaveOut{}, i18n.Errorf("memory.err.source_kind", memory.ErrInvalid, strings.Join(memory.SourceKinds, "|"))
	}
	cur, err := h.s.Get(id)
	if err != nil {
		return SaveOut{}, err
	}
	quote := ""
	if si.Source.Kind == memory.SourceQuote {
		quote = si.Source.Excerpt
	}
	who := h.o.who(quote, "")
	title, body := si.Title, cur.Body
	// 저장의 출처는 고침과 같은 트랜잭션에 근거로 붙는다: 등급은 통로 상한 아래에서 정하고, 믿을 수 없는 출처뿐이면
	// 도메인이 owner·agent 기억의 고침을 거절한다(D7).
	u := memory.UpdateInput{ID: id, Title: &title, Reason: "memory_save resolve update", Who: who, Idem: si.Idem,
		Sources: []memory.Source{si.Source}, TrustLimit: h.o.TrustLimit}
	if si.Body != "" {
		body = si.Body
		u.Body = &body
	}
	if si.Tags != nil {
		u.Tags = &si.Tags
	}
	if si.Tools != nil {
		u.Tools = &si.Tools
	}
	if si.Topic != "" {
		u.Topic = &si.Topic
	}
	if err := h.checkClaim(cur.Kind, body); err != nil {
		return SaveOut{}, err
	}
	m, replay, err := h.s.UpdateWith(u)
	if err != nil {
		return SaveOut{}, err
	}
	var warns []string
	if replay == nil && in.Pin && !m.Pinned { // 고침은 이미 적용·기록됐다: 승격하지 못한 것은 오류가 아니라 알림이다
		if _, err := h.s.Pin(id, who); err != nil {
			warns = append(warns, i18n.T("memory.save.not_pinned", id, err))
		}
	}
	if m, err = h.applyLinks(id, in.Links, nil); err != nil {
		return SaveOut{}, err
	}
	return SaveOut{Saved: true, MemoryOut: MemoryOut{Memory: m, Warnings: warns}}, nil
}

// skip applies resolve skip: nothing is written and the memory kept comes back. Plain skip keeps the most
// similar memory (or the one of the same title or topic); skip:<id> names it. With a key the choice is
// recorded so the same call comes back the same.
func (h handlers) skip(id string, si memory.SaveInput) (SaveOut, error) {
	if id == "" {
		chk, err := h.s.SaveCheck(si, h.o.checkMins())
		if err != nil {
			return SaveOut{}, err
		}
		switch {
		case len(chk.Similar) > 0:
			id = chk.Similar[0].ID
		case len(chk.Existing) > 0:
			id = chk.Existing[0]
		default:
			return SaveOut{Resolve: resolveSkip}, nil // 남겨 둘 비슷한 기억이 이제 없다: 쓰지 않는다
		}
	}
	var m memory.Memory
	var err error
	if si.Idem != nil {
		m, _, err = h.s.RecordSkip(*si.Idem, id)
	} else {
		m, err = h.s.Get(id)
	}
	if err != nil {
		return SaveOut{}, err
	}
	return SaveOut{Resolve: resolveSkip, MemoryOut: MemoryOut{Memory: m}}, nil
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
	ExpectedVersion int        `json:"expectedVersion,omitempty" jsonschema:"읽은 판본(바뀌었으면 거절)"`
	Action          string     `json:"action,omitempty" jsonschema:"edit(기본)·confirm·stale·supersede·restore·reviewed·pin·unpin"`
	Kind            *string    `json:"kind,omitempty"`
	Title           *string    `json:"title,omitempty"`
	Body            *string    `json:"body,omitempty"`
	Tags            *[]string  `json:"tags,omitempty"`
	AppliesTo       string     `json:"appliesTo,omitempty" jsonschema:"옮길 적용 대상"`
	Scope           string     `json:"scope,omitempty" jsonschema:"옛 이름(global=user, project=repo)"`
	Project         string     `json:"project,omitempty" jsonschema:"옛 이름: scope project의 디렉터리"`
	SupersededBy    string     `json:"supersededBy,omitempty" jsonschema:"supersede의 새 기억 id"`
	Reason          string     `json:"reason,omitempty"`
	UserQuote       string     `json:"userQuote,omitempty" jsonschema:"사용자 말을 담은 기억을 바꾸거나 후보를 확정할 때 그 사용자 말"`
	Tools           *[]string  `json:"tools,omitempty" jsonschema:"도구 트리거(빈 목록이면 없앰)"`
	Link            []LinkIn   `json:"link,omitempty" jsonschema:"함께 읽을 기억"`
	Unlink          []UnlinkIn `json:"unlink,omitempty" jsonschema:"연결을 뗀다"`
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
	before, _ := h.s.Get(in.ID)
	if in.ExpectedVersion != 0 && before.ID != "" && before.Version != in.ExpectedVersion {
		r, _ := toolError(memory.ErrVersionConflict)
		return r, MemoryOut{}, nil
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
			text := before.Title + "\n" + before.Body // 여러 제품 폴더에서 product는 이 기억의 글로 좁힌다(S3-R2b)
			if in.Title != nil {
				text = *in.Title + "\n" + before.Body
			}
			if in.Body != nil {
				text = strings.SplitN(text, "\n", 2)[0] + "\n" + *in.Body
			}
			sc, label, w, serr := h.appliesTo(strings.TrimSpace(in.AppliesTo), in.Scope, in.Project, false, text)
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
	moved := before.ID != "" && (m.Scope != before.Scope || m.Project != before.Project)
	if before.Pinned && !m.Pinned && in.Action != "unpin" {
		why := "이 변경으로 확정된 user·제품 기억이 아니게 됐거나 핵심 한도(user 3,000자·제품마다 2,000자)를 넘었습니다"
		if moved {
			why = fmt.Sprintf("적용 대상을 %s에서 %s로 옮겨 상시 핵심이 없는 층이 됐거나 그 층의 한도를 넘었습니다", place.AppliesTo(before.Scope), place.AppliesTo(m.Scope))
		}
		warns = append(warns, "상시 핵심에서 빠짐: "+why+". 되돌리려면 orbit-mem audit revert, 다시 올리려면 줄인 뒤 memory_update action=pin")
	}
	// 적용 대상 옮기기는 원장에 한 줄을 남긴다(S4 명세 1.1): 감사 목록에 보이고 audit revert로 되돌린다.
	if moved {
		reason := strings.TrimSpace(in.Reason)
		if reason == "" {
			reason = "memory_update로 적용 대상 옮김"
		}
		if lid, lerr := h.s.RecordEdit(before, fmt.Sprintf("적용 대상 옮김: %s → %s", place.AppliesTo(before.Scope), place.AppliesTo(m.Scope)), reason, h.o.who(in.UserQuote, "")); lerr != nil {
			warns = append(warns, "원장 기록 실패: "+lerr.Error())
		} else {
			warns = append(warns, "원장 "+lid+"에 남김(되돌리기: orbit-mem audit revert "+lid+")")
		}
	}
	return nil, MemoryOut{Memory: m, Warnings: warns}, nil
}

// ProposalsIn is proposals' input.
type ProposalsIn struct {
	Status string `json:"status,omitempty" jsonschema:"audit(기본)·auto·pending·approved·rejected·settled·dropped·all"`
	Days   int    `json:"days,omitempty" jsonschema:"audit에서 볼 날 수(기본 14)"`
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
		var names []string // 「A — B」 검사의 제품 이름(S4-12): 이 PC 설정의 제품 지도
		if m, perr := h.o.products(); perr == nil {
			for n := range m {
				names = append(names, n)
			}
		}
		a, err := h.s.Audit(memory.AuditFilter{Days: in.Days, Products: names})
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
	ID       string   `json:"id,omitempty" jsonschema:"결정할 항목 하나"`
	IDs      []string `json:"ids,omitempty" jsonschema:"같은 답으로 여럿을 결정"`
	Decision string   `json:"decision" jsonschema:"revert·dismiss·approve·reject"`
	Title    *string  `json:"title,omitempty" jsonschema:"고쳐서 승인할 제목"`
	Body     *string  `json:"body,omitempty" jsonschema:"고쳐서 승인할 본문"`
	Note     string   `json:"note,omitempty" jsonschema:"결정 이유"`
	// UserQuote is the user's words behind a decision on something the user said (A2).
	UserQuote string `json:"userQuote,omitempty" jsonschema:"사용자 말을 담은 기억에 닿는 결정이면 그 사용자 말"`
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

// folderOf is the session folder a person reads in a message: the folder, else the scope key.
func folderOf(p place.Place) string {
	if p.Dir != "" {
		return p.Dir
	}
	return p.Label()
}
