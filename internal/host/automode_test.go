package host_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// host call의 memory_save는 부를 때마다 읽은 설정의 자동 모드(명세 C-9)로 대체를 가른다: 근거만 적힌 검증 사실은 켬이면
// 사용자 말 사실을 대체하고, 끔이면 어긋남으로 둔다.
func TestCall_memory_save는_부를_때의_자동_모드를_따른다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	a := e.adapter("c-1")
	saveStated := func(title, topic string) mcpserver.SaveOut {
		t.Helper()
		args, _ := json.Marshal(map[string]any{"kind": "fact", "appliesTo": "product:app", "title": title, "topic": topic,
			"source": map[string]any{"kind": "verification", "ref": "go test ./...", "excerpt": "ok  example.com/app  0.2s"}})
		out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(args)})
		if err != nil {
			t.Fatal(err)
		}
		return out.(host.CallOut).Result.(mcpserver.SaveOut)
	}
	userFact := func(title, topic string) memory.Memory {
		return save(t, s, memory.SaveInput{Kind: memory.KindFact, Title: title, Topic: topic, Scope: "product:app"})
	}
	on := userFact("시험은 make test", "시험 명령")
	e.cfg.AutoMode = true
	if out := saveStated("시험은 go test", "시험 명령"); !slices.Contains(out.Superseded, on.ID) {
		t.Fatalf("켬: %+v", out.MemoryOut)
	}
	off := userFact("빌드는 make all", "빌드 명령")
	e.cfg.AutoMode = false
	if out := saveStated("빌드는 go build", "빌드 명령"); !slices.Contains(out.Conflicts, off.ID) || slices.Contains(out.Superseded, off.ID) {
		t.Fatalf("끔: %+v", out.MemoryOut)
	}
}
