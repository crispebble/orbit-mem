package memory

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

type graphSeed struct {
	s   *Store
	c   *clock
	ids map[string]string // 이름 → id
}

// newGraphSeed는 그래프를 거의 다 건드리는 기억 여덟 개를 만든다.
//
//	a ─link─ b ─link─ c        a·b·c: 같은 세션 claude:s1, 태그 orbit
//	d ─supersede→ e            d·e: 태그 브랜드
//	f·g: 같은 파일 /repo/AGENTS.md, g는 숨김
//	h: 프로젝트 범위, 태그 orbit, 같은 세션이지만 출처가 retro:·글 ref라 묶이지 않는다
func newGraphSeed(t *testing.T) graphSeed {
	t.Helper()
	s, c := newTestStore(t)
	g := graphSeed{s: s, c: c, ids: map[string]string{}}
	save := func(name, kind, scope string, tags []string, src Source) {
		c.t = c.t.Add(time.Minute)
		m, err := s.Save(SaveInput{Kind: kind, Title: name + " 기억", Scope: scope, Project: map[bool]string{true: "/repo"}[scope != ScopeGlobal], Tags: tags, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		g.ids[name] = m.ID
	}
	sess := Source{Kind: SourceSession, Ref: "claude:s1-0000-aaaa"}
	save("a", KindDecision, ScopeGlobal, []string{"orbit"}, sess)
	save("b", KindFact, ScopeGlobal, []string{"orbit", "측정"}, sess)
	save("c", KindLesson, ScopeGlobal, []string{"orbit", "측정"}, sess)
	save("d", KindRule, ScopeGlobal, []string{"브랜드"}, Source{Kind: SourceQuote, Excerpt: "d"})
	save("e", KindRule, ScopeGlobal, []string{"브랜드"}, Source{Kind: SourceQuote, Excerpt: "e"})
	save("f", KindFact, ScopeGlobal, nil, Source{Kind: SourceFile, Ref: "/repo/AGENTS.md"})
	save("g", KindFact, ScopeGlobal, nil, Source{Kind: SourceFile, Ref: "/repo/AGENTS.md"})
	save("h", KindFact, "project:/repo", []string{"orbit"}, Source{Kind: SourceSession, Ref: "retro:r-1"})
	if err := s.AddSource(g.ids["h"], Source{Kind: SourceSession, Ref: "s1 세션 2026-10-29"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSource(g.ids["f"], Source{Kind: SourceFile, Ref: "AGENTS.md"}); err != nil { // 상대경로는 묶지 않는다
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"a", "b"}, {"b", "c"}} {
		if _, _, err := s.Link(LinkInput{From: g.ids[p[0]], To: g.ids[p[1]], Note: p[0] + p[1], Origin: LinkOriginAI}); err != nil {
			t.Fatal(err)
		}
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.Supersede(g.ids["d"], g.ids["e"], "새 결정"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Minute)
	if _, err := s.SetState(g.ids["g"], StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g graphSeed) graph(t *testing.T, in GraphInput) Graph {
	t.Helper()
	out, err := g.s.Graph(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// names는 id를 이름으로 바꿔 정렬한다.
func (g graphSeed) names(ids ...string) []string {
	back := map[string]string{}
	for n, id := range g.ids {
		back[id] = n
	}
	var out []string
	for _, id := range ids {
		out = append(out, back[id])
	}
	sort.Strings(out)
	return out
}

func (g graphSeed) nodeNames(gr Graph) []string {
	var ids []string
	for _, n := range gr.Nodes {
		ids = append(ids, n.ID)
	}
	return g.names(ids...)
}

func groupMap(g graphSeed, gr Graph) map[string][]string {
	out := map[string][]string{}
	for _, x := range gr.Groups {
		out[x.Kind+":"+x.Key] = g.names(x.Members...)
	}
	return out
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: %v, 원하는 것 %v", what, got, want)
	}
}

// 끝 기준: 거르기 없이 부르면 숨김을 뺀 기억 전부, 선 둘 종류, 묶음 셋 종류(소속 둘 이상)를 준다.
func TestGraph_전체(t *testing.T) {
	g := newGraphSeed(t)
	gr := g.graph(t, GraphInput{})
	eq(t, "노드", g.nodeNames(gr), []string{"a", "b", "c", "d", "e", "f", "h"})
	if gr.Total != 7 || gr.Truncated || gr.LimitMax != GraphLimitMax || gr.DepthMax != GraphDepthMax {
		t.Errorf("머리: %+v", gr)
	}
	for i := 1; i < len(gr.Nodes); i++ {
		p, n := gr.Nodes[i-1], gr.Nodes[i]
		if p.UpdatedAt < n.UpdatedAt || (p.UpdatedAt == n.UpdatedAt && p.ID > n.ID) {
			t.Errorf("최근에 고친 순이 아닙니다: %s %s", p.UpdatedAt, n.UpdatedAt)
		}
	}
	var links, sups int
	for _, e := range gr.Edges {
		switch e.Kind {
		case RelLink:
			links++
			if e.From >= e.To || e.Directed || e.Relation != LinkRelated || e.Origin != LinkOriginAI || e.At == "" || e.Note == "" {
				t.Errorf("연결 선: %+v", e)
			}
		case RelSupersede:
			sups++
			if e.ID != "sup-"+g.ids["d"] || e.From != g.ids["d"] || e.To != g.ids["e"] || !e.Directed || e.Origin != "" || e.At != "" {
				t.Errorf("대체 선: %+v", e)
			}
		}
	}
	if links != 2 || sups != 1 {
		t.Errorf("선: 연결 %d 대체 %d", links, sups)
	}
	eq(t, "묶음", groupMap(g, gr), map[string][]string{
		"tag:orbit":                   {"a", "b", "c", "h"},
		"tag:측정":                      {"b", "c"},
		"tag:브랜드":                     {"d", "e"},
		"session:claude:s1-0000-aaaa": {"a", "b", "c"},
	})
	for _, x := range gr.Groups {
		if x.Kind == RelSession && (x.Host != "claude" || x.Label != "Claude Code 세션 s1-0000-") {
			t.Errorf("세션 묶음: %+v", x)
		}
		if x.Kind != RelSession && x.Host != "" {
			t.Errorf("세션 아닌 묶음에 host: %+v", x)
		}
	}
	for _, n := range gr.Nodes {
		if n.ID == g.ids["h"] && (n.Scope != "project:/repo" || n.Project != "/repo") {
			t.Errorf("프로젝트 범위: %+v", n)
		}
		if n.ID == g.ids["d"] && n.SupersededBy != g.ids["e"] {
			t.Errorf("supersededBy: %+v", n)
		}
	}
}

// 끝 기준: 숨김을 넣으면 같은 파일 묶음이 생기고, 묶음 소속은 응답 노드 안에서만 센다.
func TestGraph_숨김과_파일_묶음(t *testing.T) {
	g := newGraphSeed(t)
	gr := g.graph(t, GraphInput{States: States})
	eq(t, "파일 묶음", groupMap(g, gr)["file:/repo/AGENTS.md"], []string{"f", "g"})
	for _, x := range gr.Groups {
		if x.Kind == RelFile && x.Label != "AGENTS.md" {
			t.Errorf("파일 묶음 이름: %+v", x)
		}
	}
}

// 끝 기준: kinds·scopes·tags·기간으로 거르고, relations는 응답에 담을 선·묶음만 고른다.
func TestGraph_거르기와_relations(t *testing.T) {
	g := newGraphSeed(t)
	gr := g.graph(t, GraphInput{Kinds: []string{KindRule, KindFact}, Scopes: []string{ScopeGlobal}})
	eq(t, "종류·범위", g.nodeNames(gr), []string{"b", "d", "e", "f"})
	gr = g.graph(t, GraphInput{Tags: []string{"측정", "브랜드"}, Relations: []string{RelSupersede, RelTag}})
	eq(t, "태그", g.nodeNames(gr), []string{"b", "c", "d", "e"})
	if len(gr.Edges) != 1 || gr.Edges[0].Kind != RelSupersede {
		t.Errorf("relations에 link가 없으면 연결 선을 빼야 합니다: %+v", gr.Edges)
	}
	for _, x := range gr.Groups {
		if x.Kind != RelTag {
			t.Errorf("relations에 없는 묶음: %+v", x)
		}
	}
	a, _ := g.s.Get(g.ids["a"])
	c, _ := g.s.Get(g.ids["c"])
	gr = g.graph(t, GraphInput{UpdatedFrom: a.UpdatedAt, UpdatedTo: c.UpdatedAt})
	eq(t, "기간(양 끝 포함)", g.nodeNames(gr), []string{"a", "b", "c"})
}

// 끝 기준: focus는 via로 depth 단계까지 따라간다. 기본 via는 tag를 뺀다. depth는 depthMax에서 멈춘다.
func TestGraph_주변(t *testing.T) {
	g := newGraphSeed(t)
	gr := g.graph(t, GraphInput{Focus: g.ids["a"]})
	// a → link b, session b·c (tag는 기본에서 빠져 h로 건너지 않는다)
	eq(t, "기본 1단계", g.nodeNames(gr), []string{"a", "b", "c"})
	if gr.Focus != g.ids["a"] {
		t.Errorf("focus를 돌려주지 않았습니다: %q", gr.Focus)
	}
	gr = g.graph(t, GraphInput{Focus: g.ids["a"], Via: []string{RelLink}})
	eq(t, "link 1단계", g.nodeNames(gr), []string{"a", "b"})
	gr = g.graph(t, GraphInput{Focus: g.ids["a"], Via: []string{RelLink}, Depth: 2})
	eq(t, "link 2단계", g.nodeNames(gr), []string{"a", "b", "c"})
	gr = g.graph(t, GraphInput{Focus: g.ids["a"], Via: []string{RelTag}})
	eq(t, "tag", g.nodeNames(gr), []string{"a", "b", "c", "h"})
	gr = g.graph(t, GraphInput{Focus: g.ids["e"]})
	eq(t, "대체는 양쪽으로", g.nodeNames(gr), []string{"d", "e"})
	gr = g.graph(t, GraphInput{Focus: g.ids["a"], Via: []string{RelLink}, Depth: 99})
	eq(t, "depthMax", g.nodeNames(gr), []string{"a", "b", "c"})
}

// 끝 기준: 거르기는 따라가며 만난 기억에도 적용해 걸러진 기억은 건너지 않는다. focus는 거르기와 상관없이 들어간다.
func TestGraph_주변_거르기(t *testing.T) {
	g := newGraphSeed(t)
	gr := g.graph(t, GraphInput{Focus: g.ids["a"], Via: []string{RelLink}, Depth: 3, Kinds: []string{KindDecision, KindLesson}})
	eq(t, "걸러진 b를 건너지 않는다", g.nodeNames(gr), []string{"a"})
	gr = g.graph(t, GraphInput{Focus: g.ids["f"]})
	eq(t, "숨긴 g로 건너지 않는다", g.nodeNames(gr), []string{"f"})
	gr = g.graph(t, GraphInput{Focus: g.ids["f"], States: States})
	eq(t, "숨김 포함", g.nodeNames(gr), []string{"f", "g"})
	gr = g.graph(t, GraphInput{Focus: g.ids["g"]})
	eq(t, "숨긴 focus도 들어간다", g.nodeNames(gr), []string{"f", "g"})
	if _, err := g.s.Graph(GraphInput{Focus: "m-없음"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 focus: %v", err)
	}
}

// 끝 기준: limit을 넘으면 최근 것부터 남기고 truncated. focus는 잘리지 않는다. 선·묶음은 남은 노드끼리만.
func TestGraph_자르기(t *testing.T) {
	g := newGraphSeed(t)
	gr := g.graph(t, GraphInput{Limit: 2})
	if gr.Total != 7 || !gr.Truncated || len(gr.Nodes) != 2 {
		t.Fatalf("자르기: total %d truncated %v nodes %d", gr.Total, gr.Truncated, len(gr.Nodes))
	}
	all := g.graph(t, GraphInput{})
	eq(t, "최근 둘", g.nodeNames(gr), g.names(all.Nodes[0].ID, all.Nodes[1].ID))
	in := map[string]bool{}
	for _, n := range gr.Nodes {
		in[n.ID] = true
	}
	for _, e := range gr.Edges {
		if !in[e.From] || !in[e.To] {
			t.Errorf("남지 않은 노드로 가는 선: %+v", e)
		}
	}
	for _, x := range gr.Groups {
		if len(x.Members) < 2 {
			t.Errorf("소속 하나인 묶음: %+v", x)
		}
	}
	gr = g.graph(t, GraphInput{Focus: g.ids["a"], Via: []string{RelTag}, Limit: 2})
	if !gr.Truncated || gr.Total != 4 || len(gr.Nodes) != 2 || (gr.Nodes[0].ID != g.ids["a"] && gr.Nodes[1].ID != g.ids["a"]) {
		t.Errorf("focus가 잘렸습니다: %+v", gr.Nodes)
	}
	gr = g.graph(t, GraphInput{Limit: GraphLimitMax + 5})
	if gr.Truncated || len(gr.Nodes) != 7 {
		t.Errorf("limitMax보다 큰 limit: %+v", gr)
	}
}

// 끝 기준: 응답 배열은 비어도 null이 아니다.
func TestGraph_빈_기록(t *testing.T) {
	s, _ := newTestStore(t)
	gr, err := s.Graph(GraphInput{})
	if err != nil {
		t.Fatal(err)
	}
	if gr.Nodes == nil || gr.Edges == nil || gr.Groups == nil || gr.Total != 0 {
		t.Errorf("빈 기록: %+v", gr)
	}
}
