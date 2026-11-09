package host_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 공통 칸 idempotencyKey(설계 6.1, 계획 D9): call memory_save에서 args에 키가 없을 때만 args에 합친다(args가 이긴다).
// 같은 키로 다시 부르면 한 번만 적용하고, 결과는 MCP memory_save와 같은 값이다(링크 포함).
func TestCall_공통_칸_idempotencyKey는_memory_save의_args에_합친다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	a := e.adapter("c-1")
	result := func(fields map[string]any) map[string]any {
		t.Helper()
		out, err := a.do(host.OpCall, fields)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out.(host.CallOut).Result)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		return v
	}
	other := result(map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"fact","appliesTo":"user","title":"시험 DB는 컨테이너다","source":{"kind":"session","ref":"x:s"}}`)})
	otherID := other["memory"].(map[string]any)["id"].(string)
	args := `{"kind":"lesson","appliesTo":"user","title":"배포 스크립트는 드라이런을 먼저 돌린다","body":"운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 본다","source":{"kind":"session","ref":"x:s"},"links":[{"id":"` + otherID + `"}]}`
	first := result(map[string]any{"name": "memory_save", "args": json.RawMessage(args), "idempotencyKey": "k-host"})
	id := first["memory"].(map[string]any)["id"].(string)
	if r, ok, _ := s.SaveRequest("k-host"); !ok || r.MemoryID != id || r.Action != memory.ApplySave {
		t.Fatalf("공통 칸 키가 기록되지 않았다: %+v %v", r, ok)
	}
	if _, err := s.Unlink(id, otherID, memory.LinkOriginAI, "시험"); err != nil {
		t.Fatal(err)
	}
	second := result(map[string]any{"name": "memory_save", "args": json.RawMessage(args), "idempotencyKey": "k-host"})
	m, _ := s.Get(id)
	if second["saved"] != true || second["memory"].(map[string]any)["id"] != id || m.Version != 1 || len(m.Sources) != 1 {
		t.Fatalf("다시 적용했다: %v (version %d, sources %d)", second, m.Version, len(m.Sources))
	}
	if links, _ := second["memory"].(map[string]any)["links"].([]any); len(links) != 1 {
		t.Errorf("링크가 빠진 결과: %v", second["memory"])
	}
	// MCP memory_save로 같은 키·같은 인자를 부르면 같은 값이다.
	srv := mcpserver.New(s, mcpserver.Options{Actor: memory.ActorAI})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	var in map[string]any
	_ = json.Unmarshal([]byte(args), &in)
	in["idempotencyKey"] = "k-host"
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "memory_save", Arguments: in})
	if err != nil || res.IsError {
		t.Fatalf("mcp = %v %v", err, res)
	}
	var viaMCP map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(raw, &viaMCP)
	if !reflect.DeepEqual(viaMCP, second) {
		t.Errorf("MCP ≠ host call:\n%s\n%s", raw, mustJSON(second))
	}
	// args의 키가 공통 칸보다 앞선다.
	withKey := `{"kind":"fact","appliesTo":"user","title":"커밋 메시지는 한글로 쓴다","source":{"kind":"session","ref":"x:s"},"idempotencyKey":"k-args"}`
	result(map[string]any{"name": "memory_save", "args": json.RawMessage(withKey), "idempotencyKey": "k-common"})
	if _, ok, _ := s.SaveRequest("k-args"); !ok {
		t.Error("args의 키가 기록되지 않았다")
	}
	if _, ok, _ := s.SaveRequest("k-common"); ok {
		t.Error("공통 칸이 args를 이겼다")
	}
	// 같은 키에 다른 인자는 도구 오류다.
	if _, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(withKey[:len(withKey)-1] + `,"body":"다른 인자"}`)}); err == nil || err.Code != host.CodeTool {
		t.Errorf("conflict = %v", err)
	}
	// 다른 도구에는 합치지 않는다(memory_find는 키를 모른다).
	if _, err := a.do(host.OpCall, map[string]any{"name": "memory_find", "args": json.RawMessage(`{"query":"배포"}`), "idempotencyKey": "k-find"}); err != nil {
		t.Errorf("find = %v", err)
	}
}

// 비슷한 기억이 있으면 host call도 저장하지 않고 고르기를 돌려준다(MCP와 같은 결과, 종료 0).
func TestCall_memory_save_비슷한_기억은_고르기를_돌려준다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	a := e.adapter("c-1")
	save := func(args string) map[string]any {
		t.Helper()
		out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(args)})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out.(host.CallOut).Result)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		return v
	}
	save(`{"kind":"lesson","appliesTo":"user","title":"배포 스크립트는 드라이런을 먼저 돌린다","body":"운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다","source":{"kind":"session","ref":"x:s"}}`)
	got := save(`{"kind":"lesson","appliesTo":"user","title":"배포 스크립트는 드라이런이 먼저다","body":"운영 배포 스크립트는 처음 돌릴 때 드라이런으로 바뀌는 것을 먼저 본 뒤에 실제로 돌린다","source":{"kind":"session","ref":"x:s"}}`)
	if got["saved"] != false || got["similar"] == nil || got["choices"] == nil {
		t.Fatalf("got %v", got)
	}
	if ms, _ := s.List(memory.StateCandidate); len(ms) != 1 {
		t.Errorf("기억 %d개", len(ms))
	}
}

// 설정 saveSimilarMin이 host call의 문턱이다(조립 지점이 설정 값을 넘긴다).
func TestCall_memory_save_문턱은_설정_saveSimilarMin이다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	e.cfg.SaveSimilarMin = 0.99
	a := e.adapter("c-1")
	for _, args := range []string{
		`{"kind":"lesson","appliesTo":"user","title":"배포 스크립트는 드라이런을 먼저 돌린다","body":"운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다","source":{"kind":"session","ref":"x:s"}}`,
		`{"kind":"lesson","appliesTo":"user","title":"배포 스크립트는 드라이런이 먼저다","body":"운영 배포 스크립트는 처음 돌릴 때 드라이런으로 바뀌는 것을 먼저 본 뒤에 실제로 돌린다","source":{"kind":"session","ref":"x:s"}}`,
	} {
		out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(args)})
		if err != nil || !out.(host.CallOut).Result.(mcpserver.SaveOut).Saved {
			t.Fatalf("문턱 0.99에서 저장하지 않았다: %v %v", out, err)
		}
	}
	if ms, _ := s.List(memory.StateCandidate); len(ms) != 2 {
		t.Errorf("기억 %d개", len(ms))
	}
}

// 가까운 기억(1-6 b6dn): host call memory_save도 막지 않은 저장에 near 한 줄을 붙이고, 하한은 설정 saveNearMin이다
// (조립 지점이 설정 값을 넘긴다). 두 제목의 비슷함은 0.365다(mcpserver savenear_test.go).
func TestCall_memory_save_막지_않은_저장에_가까운_기억을_붙이고_하한은_설정_saveNearMin이다(t *testing.T) {
	for _, c := range []struct {
		min  float64
		near bool
	}{{0.3, true}, {0.4, false}} {
		e := newEnv(t)
		e.cfg.SaveNearMin = c.min
		a := e.adapter("c-1")
		var first string
		for i, title := range []string{"배포 공지는 하루 전에 올린다", "배포 공지는 팀 채널에 미리 알린다"} {
			out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(`{"kind":"lesson","appliesTo":"user","title":"` + title + `","source":{"kind":"session","ref":"x:s"}}`)})
			if err != nil {
				t.Fatal(err)
			}
			so := out.(host.CallOut).Result.(mcpserver.SaveOut)
			if !so.Saved {
				t.Fatalf("not saved: %+v", so)
			}
			if i == 0 {
				first = so.Memory.ID
				continue
			}
			if got := so.Near != "" && strings.Contains(so.Near, first+" 배포 공지는 하루 전에 올린다"); got != c.near {
				t.Errorf("saveNearMin %v: near %q, want near %v", c.min, so.Near, c.near)
			}
		}
	}
}
