package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
)

func TestRetroMode_재료읽기와_제출만_열린다(t *testing.T) {
	paths := config.Paths{Home: t.TempDir()}
	s, err := memory.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := "r-test"
	os.MkdirAll(filepath.Join(paths.RetroDir(), id), 0o700)
	os.WriteFile(filepath.Join(paths.RetroDir(), id, "material.json"), []byte(`{"id":"r-test","kind":"daily","period":"2026-10-26","sessions":[{"host":"claude","id":"s","cwd":"/w","turns":[{"role":"user","text":"안녕","at":"2026-10-26T01:00:00Z"}]}]}`), 0o600)
	s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES(?,?,?,?,1)`, id, "daily", "2026-10-26", "running")

	srv := NewRetro(s, paths, id, "test")
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	srv.Connect(ctx, st, nil)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, _ := cs.ListTools(ctx, nil)
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "memory_find,memory_get,retro_material,retro_read_file,retro_submit" {
		t.Fatalf("tools = %v", names)
	}
	res := call(t, cs, "retro_material", map[string]any{"page": 1}, nil)
	if res.IsError || !strings.Contains(text(res), "안녕") {
		t.Fatalf("material: %s", text(res))
	}
	res = call(t, cs, "retro_submit", map[string]any{"summary": "인사", "journal": map[string]any{"title": "인사", "body": "-"}}, nil)
	if res.IsError || !strings.Contains(text(res), "회고를 마쳤습니다") {
		t.Fatalf("submit: %s", text(res))
	}
}
