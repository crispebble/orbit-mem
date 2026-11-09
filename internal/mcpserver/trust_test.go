package mcpserver

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectOpts(t *testing.T, o Options) (*mcp.ClientSession, *memory.Store) {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	o.Project, o.ProjectLabel, o.Version = "project:github.com/me/app", "github.com/me/app", "test"
	srv := New(s, o)
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
	return cs, s
}

func saveWith(t *testing.T, cs *mcp.ClientSession, title string, source map[string]any) (*mcp.CallToolResult, MemoryOut) {
	t.Helper()
	var out MemoryOut
	res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": title, "body": "본문 " + title, "appliesTo": "user", "source": source}, &out)
	return res, out
}

func trustOf(t *testing.T, s *memory.Store, id string) string {
	t.Helper()
	m, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Trust
}

// 통로 상한: MCP 저장은 agent, 요청이 높아도 상한, ORBIT_ACTOR=user(Actor user)로 띄워도 agent.
func TestSave_통로_상한은_agent이고_요청이_올리지_못한다(t *testing.T) {
	for _, actor := range []string{"", memory.ActorUser} {
		cs, s := connectOpts(t, Options{Actor: actor})
		res, out := saveWith(t, cs, "상한 기본", map[string]any{"kind": "session", "ref": "s1"})
		if res.IsError {
			t.Fatal(text(res))
		}
		if got := trustOf(t, s, out.Memory.ID); got != memory.TrustAgent {
			t.Errorf("actor %q 기본 등급 = %q", actor, got)
		}
		res, out = saveWith(t, cs, "owner 요청", map[string]any{"kind": "session", "ref": "s2", "trust": "owner"})
		if res.IsError {
			t.Fatal(text(res))
		}
		if got := trustOf(t, s, out.Memory.ID); got != memory.TrustAgent {
			t.Errorf("actor %q owner 요청 등급 = %q", actor, got)
		}
	}
}

func TestSave_AI는_등급을_낮출_수_있고_모르는_값은_도구_오류다(t *testing.T) {
	cs, s := connectOpts(t, Options{})
	for _, lvl := range []string{memory.TrustUntrusted, memory.TrustSystem} {
		res, out := saveWith(t, cs, "낮춤 "+lvl, map[string]any{"kind": "file", "ref": "web", "trust": lvl})
		if res.IsError {
			t.Fatal(text(res))
		}
		if got := trustOf(t, s, out.Memory.ID); got != lvl {
			t.Errorf("%s 요청 등급 = %q", lvl, got)
		}
	}
	res, _ := saveWith(t, cs, "모르는 값", map[string]any{"kind": "file", "ref": "web", "trust": "boss"})
	if !res.IsError || !strings.Contains(text(res), "owner|agent|untrusted|system") {
		t.Fatalf("모르는 값 = %v %q", res.IsError, text(res))
	}
	// 사용자 말(quote)은 요청과 무관하게 owner다.
	res, out := saveWith(t, cs, "사용자 말", map[string]any{"kind": "quote", "excerpt": "이렇게 해", "trust": "untrusted"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if got := trustOf(t, s, out.Memory.ID); got != memory.TrustOwner {
		t.Errorf("quote 등급 = %q", got)
	}
}

// 사람 CLI 통로(Tools)는 상한 owner를 조립 지점이 넣는다. Lower는 이번 호출 상한을 낮추기만 한다.
func TestTools_상한을_넣으면_owner이고_Lower는_낮추기만_한다(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	save := func(tl Tools, title string) string {
		out, err := tl.Save(SaveIn{Kind: "fact", Title: title, Body: "본문 " + title, AppliesTo: "user", Source: SourceIn{Kind: "session", Ref: "s"}})
		if err != nil {
			t.Fatal(err)
		}
		return trustOf(t, s, out.Memory.ID)
	}
	human := NewTools(s, Options{TrustLimit: memory.TrustOwner})
	if got := save(human, "사람"); got != memory.TrustOwner {
		t.Errorf("사람 통로 = %q", got)
	}
	if got := save(NewTools(s, Options{}), "비어 있음"); got != memory.TrustAgent {
		t.Errorf("상한 비면 = %q", got)
	}
	lower := func(tl Tools, trust string) Tools {
		l, err := tl.Lower(trust)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if got := save(lower(human, memory.TrustUntrusted), "낮춤"); got != memory.TrustUntrusted {
		t.Errorf("Lower untrusted = %q", got)
	}
	if got := save(lower(NewTools(s, Options{}), memory.TrustOwner), "못 올림"); got != memory.TrustAgent {
		t.Errorf("Lower owner on agent = %q", got)
	}
	if _, err := human.Lower("boss"); !errors.Is(err, memory.ErrInvalid) {
		t.Errorf("Lower 모르는 값 = %v", err)
	}
}

// find·get 보기: 믿을 수 없는 기억에만 trust 칸이 있다.
func TestView_믿을_수_없는_기억만_trust를_싣는다(t *testing.T) {
	cs, _ := connectOpts(t, Options{})
	saveWith(t, cs, "믿을만한 기억 알파", map[string]any{"kind": "session", "ref": "s1"})
	saveWith(t, cs, "웹에서 온 기억 알파", map[string]any{"kind": "file", "ref": "web", "trust": "untrusted"})
	var found FindOut
	call(t, cs, "memory_find", map[string]any{"query": "알파", "scope": "all"}, &found)
	if len(found.Items) != 2 {
		t.Fatalf("find = %+v", found.Items)
	}
	ids := []string{}
	for _, it := range found.Items {
		ids = append(ids, it.ID)
		want := ""
		if strings.HasPrefix(it.Title, "웹") {
			want = memory.TrustUntrusted
		}
		if it.Trust != want {
			t.Errorf("find %s trust = %q, want %q", it.Title, it.Trust, want)
		}
	}
	var got GetOut
	call(t, cs, "memory_get", map[string]any{"ids": ids}, &got)
	for _, v := range got.Items {
		want := ""
		if strings.HasPrefix(v.Title, "웹") {
			want = memory.TrustUntrusted
		}
		if v.Trust != want {
			t.Errorf("get %s trust = %q, want %q", v.Title, v.Trust, want)
		}
	}
}
