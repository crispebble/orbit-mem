package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/activity"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

func connectWith(t *testing.T, o Options) (*mcp.ClientSession, *memory.Store) {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := New(s, o).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, s
}

// 끝 기준(M4): activity_find는 기본으로 이 프로젝트, project=all이면 전부, 질의·호스트·since·limit로 거른다.
// 출처(source)를 붙여 최신순으로 준다. 서버 instructions는 작업 기록 도구가 아니라 활동 기록을 말한다.
func TestActivityFind(t *testing.T) {
	cs, s := connectWith(t, Options{Project: "project:a", ProjectLabel: "a", Version: "t"})
	for i, e := range []memory.ActivityEvent{
		{Kind: memory.ActivityPrompt, Host: "claude", Session: "S1", ProjectScope: "project:a", ProjectLabel: "a", Prompt: "로그인 고치기", At: 1000},
		{Kind: memory.ActivityPrompt, Host: "codex", Session: "S2", ProjectScope: "project:b", ProjectLabel: "b", Prompt: "문서", At: 2000},
		{Kind: memory.ActivityPrompt, Host: "codex", Session: "S3", ProjectScope: "project:a", ProjectLabel: "a", Prompt: "배포", At: 3000},
	} {
		if err := s.RecordActivity(e); err != nil {
			t.Fatal(i, err)
		}
	}
	var out activity.Result
	call(t, cs, "activity_find", map[string]any{}, &out)
	if len(out.Items) != 2 || out.Items[0].Session != "S3" || out.Items[1].Session != "S1" || out.Items[0].Source != activity.SourceActivity {
		t.Fatalf("이 프로젝트: %+v", out.Items)
	}
	call(t, cs, "activity_find", map[string]any{"project": "all"}, &out)
	if len(out.Items) != 3 {
		t.Fatalf("all: %+v", out.Items)
	}
	call(t, cs, "activity_find", map[string]any{"project": "all", "host": "codex", "query": "문서"}, &out)
	if len(out.Items) != 1 || out.Items[0].Session != "S2" {
		t.Fatalf("거르기: %+v", out.Items)
	}
	call(t, cs, "activity_find", map[string]any{"project": "all", "limit": 1}, &out)
	if len(out.Items) != 1 || out.Items[0].Session != "S3" {
		t.Fatalf("limit: %+v", out.Items)
	}
	if res := call(t, cs, "activity_find", map[string]any{"since": "어제"}, nil); !res.IsError || !strings.Contains(text(res), "since") {
		t.Fatalf("틀린 since: %+v", res)
	}
	ins := cs.InitializeResult().Instructions
	if !strings.Contains(ins, "activity_find") || strings.Contains(ins, "work_list") || strings.Contains(ins, "work_title") {
		t.Fatalf("instructions: %s", ins)
	}
	if cs.InitializeResult().ServerInfo.Name != "orbit-mem" {
		t.Fatalf("서버 이름 %q", cs.InitializeResult().ServerInfo.Name)
	}
}

// 끝 기준(S6): 얇은 CLI가 부르는 Tools는 MCP 도구와 같은 함수다(같은 결과, 도구 오류는 Go 오류).
func TestTools_얇은_CLI용(t *testing.T) {
	_, s := connectWith(t, Options{Project: "project:a", Version: "t"})
	tools := NewTools(s, Options{Project: "project:a"})
	m, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말"}})
	found, err := tools.Find(FindIn{Query: "존댓말"})
	if err != nil || len(found.Items) != 1 || found.Items[0].ID != m.ID {
		t.Fatalf("Find %+v %v", found, err)
	}
	if _, err := tools.Find(FindIn{Scope: "bogus"}); err == nil {
		t.Fatal("도구 오류는 Go 오류")
	}
	if _, err := tools.Get(GetIn{IDs: []string{"m-none"}}); err == nil {
		t.Fatal("없는 id")
	}
	stale := "stale"
	if out, err := tools.Update(UpdateIn{ID: m.ID, Action: stale, Reason: "옮김"}); err != nil || out.Memory.State != memory.StateStale {
		t.Fatalf("Update %+v %v", out, err)
	}
	if _, err := tools.Activity(ActivityIn{Project: "all"}); err != nil {
		t.Fatal(err)
	}
	// CLI 호출은 AI 도구 사용 사건(tool.*)으로 세지 않는다
	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE kind LIKE 'tool.%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("도구 사용 사건 %d", n)
	}
}
