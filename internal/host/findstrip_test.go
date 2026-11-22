package host_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
)

// 찾기 질의의 조사 떼기 스위치(config findStripParticles, 명세 Q-2)는 host call의 memory_find에 닿는다: 켜면 「꼴이」로
// 「꼴」이 든 기억이 나오고, 끄면(기본) 질의를 쓴 그대로 찾아 나오지 않는다. 설정은 호출마다 다시 읽는다.
func TestCall_findStripParticles_스위치가_host_call의_찾기에_닿는다(t *testing.T) {
	e := newEnv(t)
	a := e.adapter("c-strip")
	call := func(name, args string) string {
		t.Helper()
		out, err := a.do(host.OpCall, map[string]any{"name": name, "args": json.RawMessage(args), "idempotencyKey": "k-" + name})
		if err != nil {
			t.Fatal(err)
		}
		return mustJSON(out)
	}
	saved := call("memory_save", `{"kind":"fact","appliesTo":"user","title":"꼴 이상 점검","source":{"kind":"session"}}`)
	var env struct {
		Result struct{ Memory struct{ ID string } }
	}
	if err := json.Unmarshal([]byte(saved), &env); err != nil || env.Result.Memory.ID == "" {
		t.Fatalf("save = %s %v", saved, err)
	}
	id := env.Result.Memory.ID
	if got := call("memory_find", `{"query":"꼴이"}`); strings.Contains(got, id) {
		t.Fatalf("default: 꼴이 → %s", got)
	}
	e.cfg.FindStripParticles = true
	if got := call("memory_find", `{"query":"꼴이"}`); !strings.Contains(got, id) {
		t.Fatalf("on: 꼴이 → %s", got)
	}
}
