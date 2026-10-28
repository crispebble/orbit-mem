package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/work"
	"github.com/onidot-labs/orbit/internal/work/contract"
)

// WorkOptions is where the work tools read (설계 · Orbit 2차 「기록 형식 계약」).
type WorkOptions struct {
	DB      string           // work.db
	Spool   string           // 훅이 DB에 넣지 못한 사건. 도구를 부를 때마다 먼저 비운다
	Cwd     string           // MCP 연결의 작업 디렉토리(work_title에 workId가 없을 때)
	Project string           // MCP 연결의 프로젝트 범위(AI 호스트의 scope=project)
	Read    work.ReadOptions // 응답 없음 시간 등
}

const readerInstructions = `Orbit 작업 기록(orbit.work 1판)을 읽는다. work_list·work_get·work_sessions로 읽고 work_changes로 바뀐 것을 기다린다.
응답의 contract 판을 확인하고, 모르는 칸과 값은 견딘다. id·seq·커서는 받은 그대로 돌려준다.`

// NewReader is oni mcp --reader: the four read tools for readers such as
// Studio. It has no memory tools and no work_title.
func NewReader(wo WorkOptions, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "orbit-work", Version: version}, &mcp.ServerOptions{Instructions: readerInstructions})
	addWorkTools(srv, newWorkTools(wo, true))
	return srv
}

type workTools struct {
	o      WorkOptions
	reader bool
	mu     sync.Mutex
	s      *work.Store
}

func newWorkTools(o WorkOptions, reader bool) *workTools { return &workTools{o: o, reader: reader} }

// store opens work.db once and drains the spool before each call, so a hook
// event spooled while the database was locked shows in the next read.
func (w *workTools) store() (*work.Store, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.s == nil {
		s, err := work.Open(w.o.DB)
		if err != nil {
			return nil, err
		}
		s.Spool = w.o.Spool
		w.s = s
	}
	_, _ = w.s.Drain()
	return w.s, nil
}

// Close closes the work database.
func (w *workTools) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.s != nil {
		w.s.Close()
		w.s = nil
	}
}

func addWorkTools(srv *mcp.Server, w *workTools) {
	// Every work tool stays inside this PC's work.db; work_title only sets a title.
	add := func(name, desc string, h func(context.Context, args) (any, error)) {
		schema, err := contract.InputSchema(contract.Major, name)
		if err != nil {
			panic(err)
		}
		a := readHints()
		if name == "work_title" {
			a = writeHints(false, true)
		}
		srv.AddTool(&mcp.Tool{Name: name, Description: desc, InputSchema: schema, Annotations: a}, w.handler(name, schema, h))
	}
	add("work_list", "이 PC의 작업 목록 쪽(orbit.work). 범위(all·project)·cwdPrefix·상태(open·done·any)·updatedSince로 거르고 cursor로 넘긴다. 최근에 바뀐 것부터.", w.list)
	add("work_get", "작업 하나의 전체 그래프(단계·선행·실행·증거·자식 작업 id). includeChildren이면 자식 작업 전체.", w.get)
	if !w.reader {
		add("work_title", "작업 제목을 정하거나 고친다. 작업 목록 도구로 작업을 열면 무엇을 하는 작업인지 한 줄로 정한다. 덧붙임 문맥이 준 workId를 넘긴다.", w.title)
		return
	}
	add("work_sessions", "세션 쪽, 또는 sessionId를 넘기면 그 세션 한 건과 사건 쪽(오래된 것부터, 첫 쪽에 실행·증거 전부).", w.sessions)
	add("work_changes", "after(앞 응답의 seq) 뒤에 바뀐 작업·세션 id. 바뀐 것이 없으면 wait 밀리초까지 기다린다(상한 waitMax). reset이면 처음부터 다시 읽는다.", w.changes)
}

// args are a call's arguments after the contract checks.
type args map[string]json.RawMessage

func (a args) str(k string) string {
	var s string
	_ = json.Unmarshal(a[k], &s)
	return s
}

func (a args) strs(k string) []string {
	var s []string
	_ = json.Unmarshal(a[k], &s)
	return s
}

func (a args) int(k string) int {
	var n int
	_ = json.Unmarshal(a[k], &n)
	return n
}

func (a args) bool(k string) bool {
	var b bool
	_ = json.Unmarshal(a[k], &b)
	return b
}

func (a args) has(k string) bool { _, ok := a[k]; return ok }

// callError is a contract error (계약 README 「오류」).
type callError struct {
	Code       string    `json:"code"`
	Message    string    `json:"message"`
	Field      string    `json:"field,omitempty"`
	Supported  []int     `json:"supportedMajors,omitempty"`
	Candidates *[]string `json:"candidates,omitempty"` // AMBIGUOUS_WORK에는 빈 배열이라도 둔다
}

func (e *callError) Error() string { return e.Code + ": " + e.Message }

func invalid(field, msg string) *callError {
	return &callError{Code: "INVALID_ARGUMENT", Message: msg, Field: field}
}

func contractHead() map[string]any {
	return map[string]any{"name": contract.Name, "major": contract.Major, "minor": contract.Minor, "supportedMajors": contract.SupportedMajors}
}

// handler checks the arguments against the contract input schema, then calls h.
// Unknown arguments are ignored and named in ignoredArgs; a wrong type is
// INVALID_ARGUMENT; an unsupported major is UNSUPPORTED_MAJOR.
func (w *workTools) handler(name string, schema map[string]any, h func(context.Context, args) (any, error)) mcp.ToolHandler {
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var raw map[string]json.RawMessage
		if len(req.Params.Arguments) > 0 && string(req.Params.Arguments) != "null" {
			if err := json.Unmarshal(req.Params.Arguments, &raw); err != nil {
				return errorResult(invalid("", "인자는 JSON 객체여야 합니다")), nil
			}
		}
		a := args{}
		var ignored []string
		for k, v := range raw {
			ps, ok := props[k].(map[string]any)
			if !ok {
				ignored = append(ignored, k)
				continue
			}
			if string(v) == "null" {
				continue // null은 넘기지 않은 것으로 본다
			}
			if err := checkValue(k, ps, v); err != nil {
				return errorResult(err), nil
			}
			a[k] = v
		}
		if a.has("major") && !supported(a.int("major")) {
			return errorResult(&callError{Code: "UNSUPPORTED_MAJOR", Message: fmt.Sprintf("orbit.work %d판은 지원하지 않습니다.", a.int("major")), Supported: contract.SupportedMajors}), nil
		}
		for _, r := range required {
			if k, _ := r.(string); !a.has(k) {
				return errorResult(invalid(k, k+"가 필요합니다.")), nil
			}
		}
		out, err := h(ctx, a)
		if err != nil {
			var ce *callError
			if errors.As(err, &ce) {
				return errorResult(ce), nil
			}
			return errorResult(&callError{Code: "INVALID_ARGUMENT", Message: err.Error()}), nil
		}
		doc := map[string]any{}
		b, _ := json.Marshal(out)
		_ = json.Unmarshal(b, &doc)
		doc["contract"] = contractHead()
		if len(ignored) > 0 {
			sort.Strings(ignored)
			doc["ignoredArgs"] = ignored
		}
		body, _ := json.Marshal(doc)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}, StructuredContent: json.RawMessage(body)}, nil
	}
}

func supported(major int) bool {
	for _, m := range contract.SupportedMajors {
		if m == major {
			return true
		}
	}
	return false
}

func errorResult(e *callError) *mcp.CallToolResult {
	body, _ := json.Marshal(map[string]any{"contract": contractHead(), "error": e})
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: e.Error()}}, StructuredContent: json.RawMessage(body)}
}

// checkValue is the type part of the input schema: type, enum, minimum, items.
func checkValue(field string, ps map[string]any, v json.RawMessage) *callError {
	var x any
	if err := json.Unmarshal(v, &x); err != nil {
		return invalid(field, field+" 값을 읽을 수 없습니다.")
	}
	return checkAny(field, ps, x)
}

func checkAny(field string, ps map[string]any, x any) *callError {
	want, _ := ps["type"].(string)
	if want == "" && ps["enum"] != nil {
		want = "string"
	}
	ok := true
	switch want {
	case "string":
		_, ok = x.(string)
	case "boolean":
		_, ok = x.(bool)
	case "integer":
		f, isNum := x.(float64)
		ok = isNum && f == float64(int64(f))
		if ok {
			if min, has := ps["minimum"].(float64); has && f < min {
				return invalid(field, fmt.Sprintf("%s는 %v 이상이어야 합니다.", field, min))
			}
		}
	case "array":
		list, isList := x.([]any)
		ok = isList
		if items, has := ps["items"].(map[string]any); ok && has {
			for _, it := range list {
				if err := checkAny(field, items, it); err != nil {
					return err
				}
			}
		}
	}
	if !ok {
		return invalid(field, fmt.Sprintf("%s는 %s여야 합니다.", field, typeName[want]))
	}
	if enum, has := ps["enum"].([]any); has {
		for _, e := range enum {
			if e == x {
				return nil
			}
		}
		var names []string
		for _, e := range enum {
			names = append(names, fmt.Sprint(e))
		}
		return invalid(field, fmt.Sprintf("%s는 %s 중 하나여야 합니다.", field, strings.Join(names, "·")))
	}
	return nil
}

var typeName = map[string]string{"string": "문자열", "boolean": "불", "integer": "정수(밀리초·개수)", "array": "배열"}

func (w *workTools) filter(a args) (work.Filter, error) {
	f := work.Filter{CwdPrefix: a.strs("cwdPrefix"), Cursor: a.str("cursor"), Limit: a.int("limit")}
	for _, p := range f.CwdPrefix {
		if !filepath.IsAbs(p) {
			return f, invalid("cwdPrefix", "cwdPrefix는 절대경로여야 합니다.")
		}
	}
	if a.str("scope") == "project" {
		f.Project = a.str("project")
		if f.Project == "" && !w.reader {
			f.Project = w.o.Project // AI 호스트는 그 세션의 프로젝트
		}
		if f.Project == "" {
			return f, invalid("project", "scope=project에는 project(project:…)가 필요합니다.")
		}
	}
	if since := a.str("updatedSince"); since != "" {
		t, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			return f, invalid("updatedSince", "updatedSince는 RFC 3339 시각이어야 합니다.")
		}
		f.Since = t.UnixMilli()
	}
	return f, nil
}

func cursorErr(err error) error {
	if errors.Is(err, work.ErrBadCursor) {
		return invalid("cursor", "cursor를 읽을 수 없습니다. 앞 응답의 nextCursor를 그대로 넘기거나 처음부터 읽어 주세요.")
	}
	return err
}

func (w *workTools) list(_ context.Context, a args) (any, error) {
	s, err := w.store()
	if err != nil {
		return nil, err
	}
	f, err := w.filter(a)
	if err != nil {
		return nil, err
	}
	if st := a.str("state"); st != "any" {
		f.State = st
	}
	p, err := s.ListWorks(f, w.o.Read)
	if err != nil {
		return nil, cursorErr(err)
	}
	out := map[string]any{"seq": s.EncodeSeq(p.Seq), "items": p.Items, "limitMax": work.ListLimitMax}
	if p.NextCursor != "" {
		out["nextCursor"] = p.NextCursor
	}
	return out, nil
}

func notFound(field, what string) error {
	return &callError{Code: "NOT_FOUND", Message: what + "이 없습니다.", Field: field}
}

func (w *workTools) get(_ context.Context, a args) (any, error) {
	s, err := w.store()
	if err != nil {
		return nil, err
	}
	doc, seq, err := s.GetWork(a.str("workId"), a.bool("includeChildren"), w.o.Read)
	if errors.Is(err, work.ErrNotFound) {
		return nil, notFound("workId", "작업 "+a.str("workId"))
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"seq": s.EncodeSeq(seq), "work": doc}, nil
}

func (w *workTools) title(_ context.Context, a args) (any, error) {
	s, err := w.store()
	if err != nil {
		return nil, err
	}
	t := work.Clean(a.str("title"), work.TitleMax)
	if t == "" {
		return nil, invalid("title", "title은 빈 글일 수 없습니다.")
	}
	id := a.str("workId")
	if id == "" {
		cands := []string{}
		seen := map[string]bool{}
		for _, dir := range sameDirs(w.o.Cwd) {
			ids, err := s.OpenWorksAt(dir)
			if err != nil {
				return nil, err
			}
			for _, x := range ids {
				if !seen[x] {
					seen[x] = true
					cands = append(cands, x)
				}
			}
		}
		if len(cands) != 1 {
			return nil, &callError{Code: "AMBIGUOUS_WORK", Message: "이 작업 디렉토리에 열린 작업이 하나가 아닙니다. workId를 넘겨 주세요.", Candidates: &cands}
		}
		id = cands[0]
	}
	if _, err := s.SetTitle(id, t); errors.Is(err, work.ErrNotFound) {
		return nil, notFound("workId", "작업 "+id)
	} else if err != nil {
		return nil, err
	}
	sum, seq, err := s.Summary(id, w.o.Read)
	if err != nil {
		return nil, err
	}
	return map[string]any{"seq": s.EncodeSeq(seq), "work": sum, "titleMax": work.TitleMax}, nil
}

// sameDirs gives the directory and its real path (훅의 cwd는 실제 경로일 수 있다).
func sameDirs(dir string) []string {
	if dir == "" {
		return nil
	}
	out := []string{filepath.Clean(dir)}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != out[0] {
		out = append(out, real)
	}
	return out
}

func (w *workTools) sessions(_ context.Context, a args) (any, error) {
	s, err := w.store()
	if err != nil {
		return nil, err
	}
	if id := a.str("sessionId"); id != "" {
		d, err := s.SessionEvents(id, a.str("cursor"), a.int("limit"), w.o.Read)
		if errors.Is(err, work.ErrNotFound) {
			return nil, notFound("sessionId", "세션 "+id)
		}
		if err != nil {
			return nil, cursorErr(err)
		}
		out := map[string]any{"seq": s.EncodeSeq(d.Seq), "session": d.Session, "events": d.Events, "limitMax": work.EventLimitMax}
		if d.First {
			out["runs"], out["evidence"] = d.Runs, d.Evidence
		}
		if d.NextCursor != "" {
			out["nextCursor"] = d.NextCursor
		}
		return out, nil
	}
	f, err := w.filter(a)
	if err != nil {
		return nil, err
	}
	f.States = a.strs("state")
	p, err := s.ListSessions(f, w.o.Read)
	if err != nil {
		return nil, cursorErr(err)
	}
	out := map[string]any{"seq": s.EncodeSeq(p.Seq), "sessions": p.Sessions, "limitMax": work.ListLimitMax}
	if p.NextCursor != "" {
		out["nextCursor"] = p.NextCursor
	}
	return out, nil
}

func (w *workTools) changes(ctx context.Context, a args) (any, error) {
	s, err := w.store()
	if err != nil {
		return nil, err
	}
	c, err := s.Changes(ctx, a.str("after"), time.Duration(a.int("wait"))*time.Millisecond)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"seq": s.EncodeSeq(c.Seq), "reset": c.Reset, "more": c.More, "waitMax": work.WaitMax.Milliseconds()}
	if !c.Reset {
		out["workIds"], out["sessionIds"] = c.WorkIDs, c.Sessions
	}
	return out, nil
}
