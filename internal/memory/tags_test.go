package memory

import (
	"reflect"
	"testing"
)

func taggedMemory(t *testing.T, s *Store, title string, tags ...string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindFact, Title: title, Scope: ScopeGlobal, Tags: tags, Source: Source{Kind: SourceSession, Ref: "claude:s1"}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 설계 · Orbit 3차 O3-2 「읽을 때 맞추기」: 대소문자·앞뒤 공백·-와 _만 다른 태그는 같은 태그다.
func TestTagKey_표기만_다른_태그는_같은_열쇠(t *testing.T) {
	same := [][]string{
		{"UI", "ui", " Ui "},
		{"claude_code", "Claude-Code", "CLAUDE-CODE"},
		{"ＵＩ", "ui"}, // 전각
	}
	for _, group := range same {
		for _, t2 := range group[1:] {
			if TagKey(group[0]) != TagKey(t2) {
				t.Errorf("%q와 %q는 같은 태그여야 합니다: %q / %q", group[0], t2, TagKey(group[0]), TagKey(t2))
			}
		}
	}
	for _, pair := range [][2]string{{"onidot-studio", "studio"}, {"claude code", "claude-code"}} {
		if TagKey(pair[0]) == TagKey(pair[1]) {
			t.Errorf("%q와 %q는 다른 태그입니다", pair[0], pair[1])
		}
	}
}

// 끝 기준 1: 표기만 다른 태그가 memory_graph 묶음에서 한 태그로 보인다. 저장값은 그대로다.
func TestGraph_표기만_다른_태그는_한_묶음이고_저장값은_그대로(t *testing.T) {
	s, _ := newTestStore(t)
	a := taggedMemory(t, s, "A 기억", "UI", "claude_code")
	b := taggedMemory(t, s, "B 기억", "ui", "Claude-Code")
	c := taggedMemory(t, s, "C 기억", "ui")
	g, err := s.Graph(GraphInput{Relations: []string{RelTag}})
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]GraphGroup{}
	for _, x := range g.Groups {
		groups[x.Key] = x
	}
	if len(g.Groups) != 2 {
		t.Fatalf("묶음은 ui·claude-code 둘이어야 합니다: %+v", g.Groups)
	}
	ui := groups["ui"]
	if len(ui.Members) != 3 || ui.Label != "ui" {
		t.Errorf("ui 묶음(라벨은 많이 쓴 표기): %+v", ui)
	}
	cc := groups["claude-code"]
	if len(cc.Members) != 2 || cc.Label != "Claude-Code" && cc.Label != "claude_code" {
		t.Errorf("claude-code 묶음: %+v", cc)
	}
	if got, _ := s.Get(a.ID); !reflect.DeepEqual(got.Tags, []string{"UI", "claude_code"}) {
		t.Errorf("저장값이 바뀌었습니다: %v", got.Tags)
	}
	for _, n := range g.Nodes {
		if n.ID == b.ID && !reflect.DeepEqual(n.Tags, []string{"ui", "Claude-Code"}) {
			t.Errorf("노드 태그는 저장값 그대로: %v", n.Tags)
		}
	}
	_ = c
}

// 끝 기준 1: memory_graph tags 거르기도 표기 차이를 무시한다.
func TestGraph_태그_거르기는_표기_차이를_무시한다(t *testing.T) {
	s, _ := newTestStore(t)
	a := taggedMemory(t, s, "A 기억", "claude_code")
	b := taggedMemory(t, s, "B 기억", "Claude-Code")
	taggedMemory(t, s, "C 기억", "codex")
	g, err := s.Graph(GraphInput{Tags: []string{" CLAUDE-code "}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		ids[n.ID] = true
	}
	if len(ids) != 2 || !ids[a.ID] || !ids[b.ID] {
		t.Errorf("거른 기억: %v", ids)
	}
}

// 끝 기준 1: memory_find 태그 거르기도 표기 차이를 무시한다.
func TestFind_태그_거르기는_표기_차이를_무시한다(t *testing.T) {
	s, _ := newTestStore(t)
	a := taggedMemory(t, s, "A 기억", "UI")
	b := taggedMemory(t, s, "B 기억", "ui", "orbit")
	taggedMemory(t, s, "C 기억", "orbit")
	got, err := s.Find(FindInput{Tags: []string{"Ui"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if len(ids) != 2 || !ids[a.ID] || !ids[b.ID] {
		t.Errorf("찾은 기억: %v", ids)
	}
	if got, _ := s.Find(FindInput{Query: "B", Tags: []string{"UI"}}); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("질의와 태그를 함께: %+v", got)
	}
}

// 주제 태그: 옛 Knowledge 표식과 종류 이름은 클러스터 기준에서 뺀다(Studio 보기와 같은 규칙).
func TestTopicTags_표식과_종류_이름은_주제가_아니다(t *testing.T) {
	got := TopicKeys([]string{"imported:server-knowledge", "space:onidot", "since:2026-01", "type:project", "옛:ISSUE", "superseded-by:x", "결정", "Lesson", "UI", "ui", "claude_code"})
	want := []string{"ui", "claude-code"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("주제 태그: %v", got)
	}
}

// 대표 태그 클러스터: 덮기 순서로 고르고, 주제 태그가 없으면 그 밖이다(Studio 보기 network.ts와 같은 규칙).
// 다음 태그가 새로 덮는 기억이 minNew보다 적으면 멈춘다(개수 상한 없음, 2026-10-30 사용자 결정).
func TestClusters_덮기_순서(t *testing.T) {
	ms := []Memory{
		{ID: "m1", Tags: []string{"orbit", "ui"}},
		{ID: "m2", Tags: []string{"Orbit"}},
		{ID: "m3", Tags: []string{"UI"}},
		{ID: "m4", Tags: []string{"studio", "ui"}},
		{ID: "m5", Tags: []string{"imported:server-knowledge"}},
	}
	c := Clusters(ms, 1)
	if !reflect.DeepEqual(c.Picked, []string{"ui", "orbit"}) {
		t.Errorf("고른 순서: %v", c.Picked)
	}
	want := map[string]string{"m1": "ui", "m2": "orbit", "m3": "ui", "m4": "ui", "m5": ""}
	if !reflect.DeepEqual(c.Of, want) {
		t.Errorf("클러스터: %v", c.Of)
	}
	if c.Other != 1 || c.Untagged != 1 {
		t.Errorf("그 밖 %d, 주제 태그 없음 %d", c.Other, c.Untagged)
	}
	if two := Clusters(ms, 2); !reflect.DeepEqual(two.Picked, []string{"ui"}) || two.Other != 2 {
		t.Errorf("새로 덮는 기억이 둘 이상인 태그만: %v, 그 밖 %d", two.Picked, two.Other)
	}
}

// 클러스터가 되려면 새로 묶는 기억이 전체의 1% 이상(최소 2개)이어야 한다.
func TestClusterMinNew_전체의_1퍼센트_최소_2(t *testing.T) {
	for n, want := range map[int]int{0: 2, 10: 2, 249: 2, 250: 3, 558: 6, 1000: 10} {
		if got := ClusterMinNew(n); got != want {
			t.Errorf("기억 %d개: %d, 바라는 것 %d", n, got, want)
		}
	}
}
