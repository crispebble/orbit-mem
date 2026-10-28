package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/work"
	"github.com/onidot-labs/orbit/internal/work/contract"
)

type workEnv struct {
	dir   string
	db    string
	spool string
	cwd   string
}

func newWorkEnv(t *testing.T) workEnv {
	t.Helper()
	dir := t.TempDir()
	return workEnv{dir: dir, db: filepath.Join(dir, "work.db"), spool: filepath.Join(dir, "spool"), cwd: filepath.Join(dir, "repo")}
}

func (e workEnv) opts() WorkOptions {
	return WorkOptions{DB: e.db, Spool: e.spool, Cwd: e.cwd, Project: "project:github.com/me/app"}
}

func connectServer(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func aiHost(t *testing.T, e workEnv) *mcp.ClientSession {
	t.Helper()
	s, err := memory.Open(filepath.Join(e.dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	o := e.opts()
	return connectServer(t, New(s, Options{Project: o.Project, Version: "test", Work: &o}))
}

func readerHost(t *testing.T, e workEnv) *mcp.ClientSession {
	return connectServer(t, NewReader(e.opts(), "test"))
}

// seedWork fills work.db with a graph that touches most states.
func seedWork(t *testing.T, e workEnv) {
	t.Helper()
	s, err := work.Open(e.db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 10, 28, 5, 0, 0, 0, time.UTC).UnixMilli()
	ev := func(kind, host, sess string, p any) {
		at += 1000
		x := work.NewEvent(kind, host, sess, at, p)
		x.Cwd = e.cwd
		if _, err := s.Append(x); err != nil {
			t.Fatal(err)
		}
	}
	str := func(s string) *string { return &s }
	ev(work.KindSessionStart, "claude", "S1", work.SessionStart{Source: "startup", Surface: "cli", Project: work.Project{Scope: "project:github.com/me/app", Label: "github.com/me/app"}, Repo: e.cwd, Branch: "main"})
	ev(work.KindTurnStart, "claude", "S1", work.Turn{Prompt: "P1"})
	for i, sub := range []string{"계약", "원장", "그래프", "훅"} {
		ev(work.KindTaskCreate, "claude", "S1", work.TaskCreate{List: "S1", ID: string(rune('1' + i)), Subject: sub})
	}
	ev(work.KindTaskUpdate, "claude", "S1", work.TaskUpdate{List: "S1", ID: "3", AddBlockedBy: []string{"1", "2"}})
	ev(work.KindTaskUpdate, "claude", "S1", work.TaskUpdate{List: "S1", ID: "1", Status: str("in_progress")})
	ev(work.KindRunStart, "claude", "S1", work.RunStart{Agent: "a1", Type: "orbit:worker", Repo: e.cwd, Branch: "main"})
	ev(work.KindCommand, "claude", "S1", work.Command{Text: "go test ./...", OK: true, Agent: "a1"})
	ev(work.KindCommit, "claude", "S1", work.Commit{Repo: e.cwd, SHA: "3b9e0c1f7a2d4e6b8c0a1f3e5d7b9c2a4e6f8a0b", Subject: "feat: X", Files: []string{"a.go"}, Agent: "a1"})
	ev(work.KindRunStop, "claude", "S1", work.RunStop{Agent: "a1", Type: "orbit:worker", Transcript: "/t/a1.jsonl"})
	ev(work.KindTaskUpdate, "claude", "S1", work.TaskUpdate{List: "S1", ID: "1", Status: str("completed")})
	ev(work.KindTaskUpdate, "claude", "S1", work.TaskUpdate{List: "S1", ID: "2", Status: str("in_progress")})
	ev(work.KindWaitOpen, "claude", "S1", work.Wait{Key: "k1", Prompt: "P1", Tool: "Bash"})
	ev(work.KindTaskUpdate, "claude", "S1", work.TaskUpdate{List: "S1", ID: "4", Status: str("deleted")})
	ev(work.KindPlanSnapshot, "codex", "C1", work.PlanSnapshot{List: work.ListCodexPlan, ID: "C1", Items: []work.PlanItem{{Text: "A", Status: "completed"}, {Text: "B", Status: "in_progress"}}})
	ev(work.KindSessionEnd, "codex", "C1", work.SessionEnd{Reason: "other"})
}

func validate(t *testing.T, def string, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	b, _ := json.Marshal(res.StructuredContent)
	v, err := contract.ResponseValidator(1, def)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(b); err != nil {
		t.Errorf("%s 응답이 orbit.work 1판 Schema에 맞지 않습니다: %v\n%s", def, err, b)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func callRaw(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

// 끝 기준: AI 호스트에는 work_list·work_get·work_title만, --reader에는 work_title이 없다.
func TestWorkToolLists(t *testing.T) {
	e := newWorkEnv(t)
	ai := strings.Join(toolNames(t, aiHost(t, e)), ",")
	for _, want := range []string{"work_get", "work_list", "work_title", "memory_find"} {
		if !strings.Contains(ai, want) {
			t.Errorf("AI 호스트 도구에 %s가 없습니다: %s", want, ai)
		}
	}
	if strings.Contains(ai, "work_sessions") || strings.Contains(ai, "work_changes") {
		t.Errorf("AI 호스트에 읽는 쪽 도구가 섞였습니다: %s", ai)
	}
	if got := strings.Join(toolNames(t, readerHost(t, e)), ","); got != "work_changes,work_get,work_list,work_sessions" {
		t.Errorf("읽는 쪽 도구: %s", got)
	}
}

// 끝 기준: 실제 응답이 모두 1판 Schema를 통과하고 시각은 RFC 3339 UTC 밀리초다.
func TestWorkResponsesMatchContract(t *testing.T) {
	e := newWorkEnv(t)
	seedWork(t, e)
	cs := readerHost(t, e)
	list := validate(t, "work_list", callRaw(t, cs, "work_list", nil))
	items := list["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("작업 %d", len(items))
	}
	for _, it := range items {
		id := it.(map[string]any)["id"].(string)
		validate(t, "work_get", callRaw(t, cs, "work_get", map[string]any{"workId": id, "includeChildren": true}))
	}
	sess := validate(t, "work_sessions", callRaw(t, cs, "work_sessions", map[string]any{"limit": 1}))
	if sess["nextCursor"] == nil {
		t.Error("작은 limit에 nextCursor가 없습니다")
	}
	validate(t, "work_sessions", callRaw(t, cs, "work_sessions", map[string]any{"limit": 1, "cursor": sess["nextCursor"]}))
	one := validate(t, "work_sessions", callRaw(t, cs, "work_sessions", map[string]any{"sessionId": "S1", "limit": 3}))
	if one["runs"] == nil || one["evidence"] == nil || one["nextCursor"] == nil {
		t.Errorf("세션 첫 쪽: %v", one)
	}
	two := validate(t, "work_sessions", callRaw(t, cs, "work_sessions", map[string]any{"sessionId": "S1", "cursor": one["nextCursor"]}))
	if two["runs"] != nil {
		t.Error("뒤 쪽에 runs가 있습니다")
	}
	ch := validate(t, "work_changes", callRaw(t, cs, "work_changes", nil))
	if ch["reset"] != true || ch["waitMax"].(float64) != float64(work.WaitMax.Milliseconds()) {
		t.Errorf("처음 붙을 때: %v", ch)
	}
	ch = validate(t, "work_changes", callRaw(t, cs, "work_changes", map[string]any{"after": ch["seq"]}))
	if ch["reset"] != false || len(ch["workIds"].([]any)) != 0 {
		t.Errorf("바뀐 것 없음: %v", ch)
	}
	// 시각 모양은 Schema가 검사한다. 한 번 더 직접 본다
	b, _ := json.Marshal(callRaw(t, cs, "work_get", map[string]any{"workId": items[0].(map[string]any)["id"]}).StructuredContent)
	if !strings.Contains(string(b), `.000Z"`) || strings.Contains(string(b), "+09:00") {
		t.Errorf("시각 형식: %s", b)
	}
}

// 끝 기준: major를 넘기지 않으면 1판, major 2는 UNSUPPORTED_MAJOR와 supportedMajors [1], 모르는 칸은 ignoredArgs.
func TestWorkMajorAndArgs(t *testing.T) {
	e := newWorkEnv(t)
	seedWork(t, e)
	cs := readerHost(t, e)
	for _, tool := range []string{"work_list", "work_get", "work_sessions", "work_changes"} {
		res := callRaw(t, cs, tool, map[string]any{"major": 2, "workId": "x"})
		m := validate(t, "error", res)
		er := m["error"].(map[string]any)
		if !res.IsError || er["code"] != "UNSUPPORTED_MAJOR" || len(er["supportedMajors"].([]any)) != 1 || !strings.HasPrefix(text(res), "UNSUPPORTED_MAJOR:") {
			t.Errorf("%s major 2: %v", tool, m)
		}
	}
	m := validate(t, "work_list", callRaw(t, cs, "work_list", map[string]any{"fliter": "open", "major": 1, "zzz": nil}))
	if strings.Join(toStrings(m["ignoredArgs"]), ",") != "fliter,zzz" || m["contract"].(map[string]any)["major"].(float64) != 1 {
		t.Errorf("ignoredArgs: %v", m["ignoredArgs"])
	}
	for _, c := range []struct {
		tool  string
		args  map[string]any
		code  string
		field string
	}{
		{"work_changes", map[string]any{"wait": "long"}, "INVALID_ARGUMENT", "wait"},
		{"work_changes", map[string]any{"wait": -1}, "INVALID_ARGUMENT", "wait"},
		{"work_list", map[string]any{"state": "closed"}, "INVALID_ARGUMENT", "state"},
		{"work_list", map[string]any{"limit": 1.5}, "INVALID_ARGUMENT", "limit"},
		{"work_list", map[string]any{"cwdPrefix": "/a"}, "INVALID_ARGUMENT", "cwdPrefix"},
		{"work_list", map[string]any{"cursor": "망가짐"}, "INVALID_ARGUMENT", "cursor"},
		{"work_list", map[string]any{"scope": "project"}, "INVALID_ARGUMENT", "project"},
		{"work_get", map[string]any{}, "INVALID_ARGUMENT", "workId"},
		{"work_get", map[string]any{"workId": "w-없음"}, "NOT_FOUND", "workId"},
		{"work_sessions", map[string]any{"sessionId": "없음"}, "NOT_FOUND", "sessionId"},
	} {
		res := callRaw(t, cs, c.tool, c.args)
		er := validate(t, "error", res)["error"].(map[string]any)
		if !res.IsError || er["code"] != c.code || er["field"] != c.field {
			t.Errorf("%s %v: %v", c.tool, c.args, er)
		}
	}
	// 읽는 쪽에서도 scope=project에 project를 주면 거른다. 세션 시작을 보지 못한 Codex 세션의 작업은 프로젝트를 모른다
	m = validate(t, "work_list", callRaw(t, cs, "work_list", map[string]any{"scope": "project", "project": "project:github.com/me/app"}))
	if len(m["items"].([]any)) != 1 {
		t.Errorf("프로젝트 거르기: %d", len(m["items"].([]any)))
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// 끝 기준: work_title에 줄바꿈과 비밀값을 넣으면 한 줄로 바뀌고 가려진다. 같은 cwd에 열린 작업이 둘이면 AMBIGUOUS_WORK와 후보.
func TestWorkTitle(t *testing.T) {
	e := newWorkEnv(t)
	seedWork(t, e)
	cs := aiHost(t, e)
	var list map[string]any
	list = validate(t, "work_list", callRaw(t, cs, "work_list", map[string]any{"scope": "project"})) // AI 호스트는 세션 프로젝트
	var claudeWork string
	for _, it := range list["items"].([]any) {
		if w := it.(map[string]any); w["origin"].(map[string]any)["host"] == "claude" {
			claudeWork = w["id"].(string)
			if w["titleSource"] != "step" || w["title"] != "계약" {
				t.Errorf("제목 전: %v", w)
			}
		}
	}
	res := callRaw(t, cs, "work_title", map[string]any{"workId": claudeWork, "title": "orbit.work 계약\n그리고 password=hunter2 가림"})
	m := validate(t, "work_title", res)
	w := m["work"].(map[string]any)
	if w["title"] != "orbit.work 계약 그리고 password=[가림] 가림" || w["titleSource"] != "ai" || m["titleMax"].(float64) != work.TitleMax {
		t.Errorf("제목: %v", w)
	}
	got := validate(t, "work_get", callRaw(t, cs, "work_get", map[string]any{"workId": claudeWork}))["work"].(map[string]any)
	if got["title"] != w["title"] || got["titleSource"] != "ai" {
		t.Errorf("work_get 제목: %v", got["title"])
	}
	// workId 없이: 같은 cwd에 열린 작업이 둘(claude·codex)
	res = callRaw(t, cs, "work_title", map[string]any{"title": "x"})
	er := validate(t, "error", res)["error"].(map[string]any)
	if er["code"] != "AMBIGUOUS_WORK" || len(er["candidates"].([]any)) != 2 {
		t.Errorf("둘일 때: %v", er)
	}
	res = callRaw(t, cs, "work_title", map[string]any{"title": "   "})
	if er := validate(t, "error", res)["error"].(map[string]any); er["field"] != "title" {
		t.Errorf("빈 제목: %v", er)
	}
	// 작업 디렉토리에 열린 작업이 없으면 후보 없는 AMBIGUOUS_WORK
	o := e.opts()
	o.Cwd = t.TempDir()
	s, _ := memory.Open(filepath.Join(e.dir, "m2.db"))
	defer s.Close()
	other := connectServer(t, New(s, Options{Version: "t", Work: &o}))
	er = validate(t, "error", callRaw(t, other, "work_title", map[string]any{"title": "x"}))["error"].(map[string]any)
	if er["code"] != "AMBIGUOUS_WORK" || len(er["candidates"].([]any)) != 0 {
		t.Errorf("없을 때: %v", er)
	}
}

// 끝 기준: DB를 잠근 동안 스풀에 남은 사건은 잠금을 푼 뒤 첫 조회에서 반영된다.
func TestWorkReadDrainsSpool(t *testing.T) {
	e := newWorkEnv(t)
	seedWork(t, e)
	if err := work.SpoolEvent(e.spool, work.NewEvent(work.KindTaskCreate, "claude", "S9", 1, work.TaskCreate{List: "S9", ID: "1", Subject: "스풀 단계"})); err != nil {
		t.Fatal(err)
	}
	m := validate(t, "work_list", callRaw(t, readerHost(t, e), "work_list", nil))
	found := false
	for _, it := range m["items"].([]any) {
		if it.(map[string]any)["title"] == "스풀 단계" {
			found = true
		}
	}
	if !found {
		t.Error("스풀 사건이 첫 조회에 없습니다")
	}
}

// 끝 기준: work_changes에 waitMax보다 큰 wait를 넘기면 waitMax 안에 답하고, wait를 넘기지 않으면 바로 답한다.
func TestWorkChangesWaitCap(t *testing.T) {
	old := work.WaitMax
	work.WaitMax = 400 * time.Millisecond
	t.Cleanup(func() { work.WaitMax = old })
	e := newWorkEnv(t)
	seedWork(t, e)
	cs := readerHost(t, e)
	first := validate(t, "work_changes", callRaw(t, cs, "work_changes", nil))
	start := time.Now()
	m := validate(t, "work_changes", callRaw(t, cs, "work_changes", map[string]any{"after": first["seq"], "wait": 60000}))
	if el := time.Since(start); el > 2*time.Second || el < 400*time.Millisecond {
		t.Errorf("wait 상한: %v", el)
	}
	if m["waitMax"].(float64) != 400 || m["seq"] != first["seq"] {
		t.Errorf("응답: %v", m)
	}
	start = time.Now()
	validate(t, "work_changes", callRaw(t, cs, "work_changes", map[string]any{"after": first["seq"]}))
	if el := time.Since(start); el > 300*time.Millisecond {
		t.Errorf("wait 없이 %v 걸렸습니다", el)
	}
}
