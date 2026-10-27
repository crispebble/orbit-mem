package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/memory"
)

func connect(t *testing.T) (*mcp.ClientSession, *memory.Store) {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New(s, Options{Project: "project:github.com/me/app", ProjectLabel: "github.com/me/app", Version: "test"})
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

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s decode: %v (%s)", name, err, b)
		}
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestTools_여섯개(t *testing.T) {
	cs, _ := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := "memory_find,memory_get,memory_save,memory_update,proposal_decide,proposals"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
}

func TestSave_찾기_읽기_고치기(t *testing.T) {
	cs, _ := connect(t)
	var saved MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "커밋은 경로를 명시한다", "body": "git add -A 금지",
		"scope": "project", "source": map[string]any{"kind": "quote", "excerpt": "커밋은 경로 명시해"}}, &saved)
	if saved.Memory.State != "confirmed" || saved.Memory.Scope != "project:github.com/me/app" {
		t.Fatalf("saved = %+v", saved.Memory)
	}
	var cand MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "시험은 단독으로 돌린다",
		"source": map[string]any{"kind": "session", "ref": "claude:abc"}}, &cand)
	if cand.Memory.State != "candidate" || cand.Memory.Scope != "global" {
		t.Fatalf("cand = %+v", cand.Memory)
	}

	var found FindOut
	call(t, cs, "memory_find", map[string]any{"query": "커밋"}, &found)
	if len(found.Items) != 1 || found.Items[0].ID != saved.Memory.ID {
		t.Fatalf("found = %+v", found)
	}

	var got GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{saved.Memory.ID, cand.Memory.ID}}, &got)
	if len(got.Items) != 2 || len(got.Items[0].Sources) != 1 {
		t.Fatalf("get = %+v", got)
	}

	var upd MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": saved.Memory.ID, "expectedVersion": 1, "body": "git add -A·git commit -a 금지", "reason": "보강"}, &upd)
	if upd.Memory.Version != 2 {
		t.Fatalf("upd = %+v", upd.Memory)
	}
	res := call(t, cs, "memory_update", map[string]any{"id": saved.Memory.ID, "expectedVersion": 1, "body": "x"}, nil)
	if !res.IsError || !strings.Contains(text(res), "먼저 고쳤습니다") {
		t.Fatalf("conflict not reported: %s", text(res))
	}
	var stale MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": cand.Memory.ID, "action": "stale", "reason": "더 이상 맞지 않음"}, &stale)
	if stale.Memory.State != "stale" {
		t.Fatalf("stale = %+v", stale.Memory)
	}
}

func TestSave_잘못된_입력은_도구_오류로_알린다(t *testing.T) {
	cs, _ := connect(t)
	res := call(t, cs, "memory_save", map[string]any{"kind": "todo", "title": "x", "source": map[string]any{"kind": "quote"}}, nil)
	if !res.IsError {
		t.Fatal("bad kind accepted")
	}
}

func TestProposals_보기와_결정(t *testing.T) {
	cs, s := connect(t)
	m, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "후보", Scope: "global", Source: memory.Source{Kind: "session"}})
	p, _ := s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "후보 확정", MemoryIDs: []string{m.ID}})
	var list ProposalsOut
	call(t, cs, "proposals", map[string]any{}, &list)
	if len(list.Items) != 1 || list.Items[0].ID != p.ID || len(list.Items[0].Memories) != 1 {
		t.Fatalf("list = %+v", list)
	}
	var dec DecideOut
	call(t, cs, "proposal_decide", map[string]any{"id": p.ID, "decision": "approve", "title": "고친 후보"}, &dec)
	if dec.Proposal.Status != "approved" {
		t.Fatalf("dec = %+v", dec)
	}
	got, _ := s.Get(m.ID)
	if got.State != "confirmed" || got.Title != "고친 후보" {
		t.Fatalf("memory = %+v", got)
	}
}
