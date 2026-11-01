package mcpserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	memcontract "github.com/onidot-labs/orbit-mem/internal/memory/contract"
)

func (e workEnv) memDB() string { return filepath.Join(e.dir, "m.db") }

// seedMemory는 연결·대체·태그·세션·파일 묶음과 숨김을 하나씩 만든다. 이름 → id를 돌려준다.
func seedMemory(t *testing.T, e workEnv) map[string]string {
	t.Helper()
	s, err := memory.Open(e.memDB())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ids := map[string]string{}
	save := func(name string, tags []string, src memory.Source) {
		m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: name + " 기억 token=sk-abcdefghijklmnop1234", Scope: memory.ScopeGlobal, Tags: tags, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = m.ID
	}
	sess := memory.Source{Kind: memory.SourceSession, Ref: "codex:01a18284-799c-76f1-9f84-8b65e509ca8d"}
	save("a", []string{"orbit"}, sess)
	save("b", []string{"orbit"}, sess)
	save("c", nil, memory.Source{Kind: memory.SourceFile, Ref: "/repo/AGENTS.md"})
	save("d", nil, memory.Source{Kind: memory.SourceFile, Ref: "/repo/AGENTS.md"})
	save("e", nil, memory.Source{Kind: memory.SourceQuote, Excerpt: "e"})
	if _, _, err := s.Link(memory.LinkInput{From: ids["a"], To: ids["c"], Note: "근거", Origin: memory.LinkOriginRetro}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Supersede(ids["d"], ids["e"], "새것"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(ids["b"], memory.StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	return ids
}

func validateMemory(t *testing.T, def string, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	b, _ := json.Marshal(res.StructuredContent)
	v, err := memcontract.ResponseValidator(1, def)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(b); err != nil {
		t.Errorf("%s 응답이 orbit.memory 1판 Schema에 맞지 않습니다: %v\n%s", def, err, b)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// 끝 기준: 실제 응답이 모두 orbit.memory 1판 Schema를 통과하고, 글 칸의 비밀값은 가려진다.
func TestMemoryGraphMatchesContract(t *testing.T) {
	e := newWorkEnv(t)
	ids := seedMemory(t, e)
	cs := readerHost(t, e)
	all := validateMemory(t, "memory_graph", callRaw(t, cs, "memory_graph", nil))
	if len(all["nodes"].([]any)) != 4 || len(all["edges"].([]any)) != 2 || all["contract"].(map[string]any)["name"] != "orbit.memory" {
		t.Errorf("전체: %v", all)
	}
	kinds := map[string]bool{}
	for _, g := range all["groups"].([]any) {
		kinds[g.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["file"] || kinds["session"] || kinds["tag"] { // b가 숨김이라 세션·태그 묶음은 소속이 하나뿐이다
		t.Errorf("묶음 종류: %v", kinds)
	}
	b, _ := json.Marshal(all)
	if strings.Contains(string(b), "sk-abcdefghijklmnop1234") {
		t.Errorf("비밀값을 가리지 않았습니다: %s", b)
	}
	hidden := validateMemory(t, "memory_graph", callRaw(t, cs, "memory_graph", map[string]any{"states": []string{"candidate", "confirmed", "stale", "hidden"}, "relations": []string{"session", "tag"}}))
	for _, g := range hidden["groups"].([]any) {
		if g := g.(map[string]any); g["kind"] == "session" && (g["host"] != "codex" || g["label"] != "Codex 세션 01a18284") {
			t.Errorf("세션 묶음: %v", g)
		}
	}
	if len(hidden["groups"].([]any)) != 2 || len(hidden["edges"].([]any)) != 0 {
		t.Errorf("relations: %v", hidden)
	}
	focus := validateMemory(t, "memory_graph", callRaw(t, cs, "memory_graph", map[string]any{"focus": ids["c"], "depth": 2}))
	if focus["focus"] != ids["c"] || len(focus["nodes"].([]any)) != 4 { // c → link a, file d → 대체 e
		t.Errorf("주변: %v", focus)
	}
	cut := validateMemory(t, "memory_graph", callRaw(t, cs, "memory_graph", map[string]any{"limit": 1}))
	if cut["truncated"] != true || cut["total"].(float64) != 4 || len(cut["nodes"].([]any)) != 1 {
		t.Errorf("자르기: %v", cut)
	}
}

// 끝 기준: major 2는 UNSUPPORTED_MAJOR, 모르는 칸은 ignoredArgs, 형이 틀리면 INVALID_ARGUMENT, 없는 focus는 NOT_FOUND.
func TestMemoryGraphErrors(t *testing.T) {
	e := newWorkEnv(t)
	seedMemory(t, e)
	cs := readerHost(t, e)
	res := callRaw(t, cs, "memory_graph", map[string]any{"major": 2})
	m := validateMemory(t, "error", res)
	if er := m["error"].(map[string]any); !res.IsError || er["code"] != "UNSUPPORTED_MAJOR" || !strings.Contains(er["message"].(string), "orbit.memory") {
		t.Errorf("major 2: %v", m)
	}
	m = validateMemory(t, "memory_graph", callRaw(t, cs, "memory_graph", map[string]any{"fliter": "x", "major": 1}))
	if strings.Join(toStrings(m["ignoredArgs"]), ",") != "fliter" {
		t.Errorf("ignoredArgs: %v", m["ignoredArgs"])
	}
	for _, c := range []struct {
		args  map[string]any
		code  string
		field string
	}{
		{map[string]any{"depth": 0}, "INVALID_ARGUMENT", "depth"},
		{map[string]any{"limit": "많이"}, "INVALID_ARGUMENT", "limit"},
		{map[string]any{"kinds": []string{"memo"}}, "INVALID_ARGUMENT", "kinds"},
		{map[string]any{"via": []string{"similar"}}, "INVALID_ARGUMENT", "via"},
		{map[string]any{"scopes": []string{"project:"}}, "INVALID_ARGUMENT", "scopes"},
		{map[string]any{"scopes": []string{"repo"}}, "INVALID_ARGUMENT", "scopes"},
		{map[string]any{"updatedFrom": "어제"}, "INVALID_ARGUMENT", "updatedFrom"},
		{map[string]any{"updatedTo": "2026-10-29"}, "INVALID_ARGUMENT", "updatedTo"},
		{map[string]any{"focus": "m-없음"}, "NOT_FOUND", "focus"},
	} {
		res := callRaw(t, cs, "memory_graph", c.args)
		m := validateMemory(t, "error", res)
		er, _ := m["error"].(map[string]any)
		if !res.IsError || er["code"] != c.code || er["field"] != c.field {
			t.Errorf("%v: %v", c.args, m)
		}
	}
}

// 끝 기준: memory_graph는 읽는 쪽에만 있고 AI 호스트에는 없다. 읽기 전용 주석이 붙는다.
func TestMemoryGraphOnlyForReaders(t *testing.T) {
	e := newWorkEnv(t)
	if ai := strings.Join(toolNames(t, aiHost(t, e)), ","); strings.Contains(ai, "memory_graph") {
		t.Errorf("AI 호스트에 memory_graph가 있습니다: %s", ai)
	}
	cs := readerHost(t, e)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tl := range res.Tools {
		if tl.Name == "memory_graph" {
			found = true
			if a := tl.Annotations; a == nil || !a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
				t.Errorf("memory_graph 주석: %+v", a)
			}
		}
	}
	if !found {
		t.Error("읽는 쪽에 memory_graph가 없습니다")
	}
}

// 끝 기준: 기억 DB를 열거나 읽지 못하면 인자 오류가 아니라 UNAVAILABLE이고 Schema를 통과한다.
func TestMemoryGraphUnavailable(t *testing.T) {
	cs := connectServer(t, NewReader(t.TempDir(), "test")) // 디렉터리는 DB로 열 수 없다
	res := callRaw(t, cs, "memory_graph", nil)
	m := validateMemory(t, "error", res)
	if er, _ := m["error"].(map[string]any); !res.IsError || er["code"] != "UNAVAILABLE" {
		t.Errorf("열지 못한 DB: %v", m)
	}
}

// 끝 기준: reader 서버 이름은 그대로 orbit-work다(바깥 식별자).
func TestReaderServerName(t *testing.T) {
	e := newWorkEnv(t)
	if got := readerHost(t, e).InitializeResult().ServerInfo.Name; got != "orbit-work" {
		t.Errorf("서버 이름: %s", got)
	}
}
