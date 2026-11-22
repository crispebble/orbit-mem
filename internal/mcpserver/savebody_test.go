package mcpserver

import (
	"strings"
	"testing"
)

// S-1: 같은 제목에 다른 본문을 다시 저장하면 본문이 바뀌지 않았다는 안내가 나온다.
func TestMemorySave_같은_제목_다른_본문은_memory_update_안내가_나온다(t *testing.T) {
	cs, _ := connect(t)
	save := func(body, ref string) MemoryOut {
		var out MemoryOut
		res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "배포 창은 화요일", "body": body,
			"source": map[string]any{"kind": "session", "ref": ref}}, &out)
		if res.IsError {
			t.Fatalf("save: %s", text(res))
		}
		return out
	}
	has := func(out MemoryOut) bool {
		for _, w := range out.Warnings {
			if strings.Contains(w, "본문은 바뀌지 않았") && strings.Contains(w, "memory_update") {
				return true
			}
		}
		return false
	}

	if first := save("화요일 오전", "s-1"); has(first) {
		t.Errorf("처음 저장에 안내가 나왔습니다: %v", first.Warnings)
	}
	second := save("화요일 오후로 바뀜", "s-2")
	if !has(second) {
		t.Fatalf("본문이 다른 재저장에 안내가 없습니다: %v", second.Warnings)
	}
	if second.Memory.Body != "화요일 오전" || len(second.Memory.Sources) != 2 {
		t.Errorf("본문은 그대로, 출처는 더해져야 합니다: body=%q sources=%d", second.Memory.Body, len(second.Memory.Sources))
	}
	if same := save("화요일 오전", "s-3"); has(same) {
		t.Errorf("본문이 같은데 안내가 나왔습니다: %v", same.Warnings)
	}
}
