package mcpserver

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

type graphEnv struct{ dir string }

func newGraphEnv(t *testing.T) graphEnv { return graphEnv{dir: t.TempDir()} }

func aiHost(t *testing.T, e graphEnv) *mcp.ClientSession {
	t.Helper()
	s, err := memory.Open(filepath.Join(e.dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return connectServer(t, New(s, Options{Project: "project:x", Version: "test"}))
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

func readerHost(t *testing.T, e graphEnv) *mcp.ClientSession {
	return connectServer(t, NewReader(e.memDB(), "test"))
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

func callRaw(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
