package memory

import (
	"testing"
)

// 설계 · Studio 4차 화면 3: 점 크기 = importance(0~1). 가져다 쓴 횟수·지킨 기록·연결 수·출처 수·확정 여부로 계산한다.
func TestGraphImportanceAndSourceKinds(t *testing.T) {
	g := newGraphSeed(t)
	s := g.s
	// a를 여러 번 넣고 읽고, 따름 판정을 남긴다
	for i := 0; i < 5; i++ {
		_ = s.RecordEvent(Event{Kind: EventInjectPrompt}, map[string]any{"memoryIds": []string{g.ids["a"]}})
	}
	_ = s.RecordEvent(Event{Kind: EventToolPrefix + "memory_get"}, map[string]any{"ids": []string{g.ids["a"], g.ids["b"]}})
	for i := 0; i < 3; i++ {
		_ = s.RecordEvent(Event{Kind: EventMemoryUsed}, map[string]any{"memoryId": g.ids["a"], "verdict": UsageFollowed})
	}
	if err := s.AddSource(g.ids["a"], Source{Kind: SourceQuote, Excerpt: "그렇게 해"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Graph(GraphInput{})
	if err != nil {
		t.Fatal(err)
	}
	node := map[string]GraphNode{}
	for _, n := range out.Nodes {
		if n.Importance < 0 || n.Importance > 1 {
			t.Errorf("%s importance %v는 0~1이어야 합니다", n.ID, n.Importance)
		}
		node[n.ID] = n
	}
	a, b, f := node[g.ids["a"]], node[g.ids["b"]], node[g.ids["f"]]
	if !(a.Importance > b.Importance && b.Importance > f.Importance) {
		t.Errorf("많이 쓰고 지킨 기억이 더 커야 합니다: a=%v b=%v f=%v", a.Importance, b.Importance, f.Importance)
	}
	if len(a.SourceKinds) != 2 || a.SourceKinds[0] != SourceQuote || a.SourceKinds[1] != SourceSession {
		t.Errorf("a 원본 종류 = %v", a.SourceKinds)
	}
	if len(f.SourceKinds) != 1 || f.SourceKinds[0] != SourceFile {
		t.Errorf("f 원본 종류 = %v", f.SourceKinds)
	}
	// 확정 기억은 같은 쓰임의 후보보다 크다(e는 인용 출처라 확정)
	if d := node[g.ids["e"]]; d.State != StateConfirmed || d.Importance <= f.Importance {
		t.Errorf("확정 d=%v(%s) f=%v", d.Importance, d.State, f.Importance)
	}
}
