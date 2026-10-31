package mcpserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/openorbit/internal/memory"
	memcontract "github.com/onidot-labs/openorbit/internal/memory/contract"
)

var memoryContract = contractInfo{Name: memcontract.Name, Major: memcontract.Major, Minor: memcontract.Minor, Supported: memcontract.SupportedMajors}

// unavailable is orbit.memory 1.1's UNAVAILABLE: the memory store could not be
// opened or read (a lock held too long, a broken file). 인자 탓이 아니므로 다시 부를 수 있다.
func unavailable(err error) *callError {
	return &callError{Code: "UNAVAILABLE", Message: "기억 기록을 읽지 못했습니다: " + err.Error()}
}

// memoryGraphTool is the reader's memory_graph (orbit.memory 1판). It opens the
// memory store on the first call so a reader that only reads work never
// touches memory.db.
type memoryGraphTool struct {
	path string
	mu   sync.Mutex
	s    *memory.Store
}

func (g *memoryGraphTool) store() (*memory.Store, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.s == nil {
		s, err := memory.Open(g.path)
		if err != nil {
			return nil, err
		}
		g.s = s
	}
	return g.s, nil
}

func addMemoryGraphTool(srv *mcp.Server, g *memoryGraphTool) {
	schema, err := memcontract.InputSchema(memcontract.Major, "memory_graph")
	if err != nil {
		panic(err)
	}
	srv.AddTool(&mcp.Tool{
		Name:        "memory_graph",
		Description: "이 PC의 기억 그래프(orbit.memory). 기억 노드(본문 없음)·연결과 대체 선·같은 태그·세션·파일 묶음. kinds·states·scopes·tags·기간으로 거르고, focus를 주면 via로 depth 단계 안의 주변만.",
		InputSchema: schema,
		Annotations: readHints(),
	}, contractHandler(memoryContract, schema, g.graph))
}

func (g *memoryGraphTool) graph(_ context.Context, a args) (any, error) {
	in := memory.GraphInput{Focus: a.str("focus"), Depth: a.int("depth"), Via: a.strs("via"), Kinds: a.strs("kinds"),
		States: a.strs("states"), Scopes: a.strs("scopes"), Tags: a.strs("tags"), Relations: a.strs("relations"), Limit: a.int("limit")}
	for _, sc := range in.Scopes {
		if sc != memory.ScopeGlobal && !(strings.HasPrefix(sc, "project:") && len(sc) > len("project:")) {
			return nil, invalid("scopes", "scopes는 global 또는 project:<키>여야 합니다.")
		}
	}
	for _, f := range []struct {
		name string
		to   **int64
	}{{"updatedFrom", &in.UpdatedFrom}, {"updatedTo", &in.UpdatedTo}} {
		if v := a.str(f.name); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return nil, invalid(f.name, f.name+"는 RFC 3339 시각이어야 합니다.")
			}
			ms := t.UnixMilli()
			*f.to = &ms
		}
	}
	s, err := g.store()
	if err != nil {
		return nil, unavailable(err)
	}
	out, err := s.Graph(in)
	if errors.Is(err, memory.ErrNotFound) {
		return nil, &callError{Code: "NOT_FOUND", Message: "focus 기억이 없습니다.", Field: "focus"}
	}
	if err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}
