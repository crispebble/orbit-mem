package mcpserver

import (
	"strings"
	"testing"
)

// 끝 기준 1~3: 저장할 때 잇고, 고칠 때 잇고 떼며, memory_get이 양쪽에서 연결을 보인다.
func TestLinks_저장_고치기_읽기(t *testing.T) {
	cs, s := connect(t)
	src := map[string]any{"kind": "session", "ref": "claude:abc"}
	var a, b, c MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "decision", "title": "기억 그래프는 명시 연결과 파생으로", "source": src}, &a)
	call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "같은 태그를 쌍으로 이으면 선이 수만 개", "source": src,
		"links": []map[string]any{{"id": a.Memory.ID, "note": "같은 결정의 측정 근거"}}}, &b)
	if len(b.Memory.Links) != 1 || b.Memory.Links[0].MemoryID != a.Memory.ID || b.Memory.Links[0].Origin != "ai" {
		t.Fatalf("저장 응답의 연결: %+v", b.Memory.Links)
	}
	var got GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{a.Memory.ID}}, &got)
	if len(got.Items[0].Links) != 1 || got.Items[0].Links[0].MemoryID != b.Memory.ID || got.Items[0].Links[0].Note != "같은 결정의 측정 근거" {
		t.Fatalf("반대쪽에서 본 연결: %+v", got.Items[0].Links)
	}

	// 같은 제목으로 다시 저장하면 있던 기억에 합쳐지고, 그 기억에 잇는다
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "reader는 기억 DB를 열지 않았다", "source": src}, &c)
	var again MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "reader는 기억 DB를 열지 않았다", "source": src,
		"links": []map[string]any{{"id": a.Memory.ID}}}, &again)
	if again.Memory.ID != c.Memory.ID || len(again.Memory.Links) != 1 {
		t.Fatalf("합쳐진 기억에 잇기: %s %+v", again.Memory.ID, again.Memory.Links)
	}

	// 연결만 바꾸면 판이 오르지 않는다
	var upd MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": c.Memory.ID, "link": []map[string]any{{"id": b.Memory.ID, "note": "측정"}},
		"unlink": []map[string]any{{"id": a.Memory.ID, "reason": "직접 관련 없음"}}}, &upd)
	if upd.Memory.Version != again.Memory.Version || len(upd.Memory.Links) != 1 || upd.Memory.Links[0].MemoryID != b.Memory.ID {
		t.Fatalf("연결만 바꾸기: 판 %d→%d, %+v", again.Memory.Version, upd.Memory.Version, upd.Memory.Links)
	}
	// 고치기와 함께 쓰면 고치기는 판을 올리고 연결도 바뀐다
	var both MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": c.Memory.ID, "expectedVersion": upd.Memory.Version, "body": "work.db만 연다",
		"unlink": []map[string]any{{"id": b.Memory.ID}}}, &both)
	if both.Memory.Version != upd.Memory.Version+1 || len(both.Memory.Links) != 0 {
		t.Fatalf("고치기와 떼기: %+v", both.Memory)
	}
	if ls, _ := s.LinksOf(b.Memory.ID); len(ls) != 1 {
		t.Errorf("b의 남은 연결: %+v", ls)
	}
}

func TestLinks_잘못된_연결은_아무것도_바꾸지_않고_거절(t *testing.T) {
	cs, s := connect(t)
	src := map[string]any{"kind": "session", "ref": "claude:abc"}
	var a MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "A", "source": src}, &a)
	res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "B", "source": src, "links": []map[string]any{{"id": "m-없음"}}}, nil)
	if !res.IsError || !strings.Contains(text(res), "m-없음") {
		t.Fatalf("없는 기억: %s", text(res))
	}
	if n, _ := s.Count(); n != 1 {
		t.Errorf("거절했는데 기억이 저장됐습니다: %d", n)
	}
	res = call(t, cs, "memory_update", map[string]any{"id": a.Memory.ID, "body": "고침", "link": []map[string]any{{"id": a.Memory.ID}}}, nil)
	if !res.IsError {
		t.Fatal("자기 자신과 잇기를 받았습니다")
	}
	if m, _ := s.Get(a.Memory.ID); m.Version != a.Memory.Version {
		t.Error("거절했는데 고치기가 들어갔습니다")
	}
}
