package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// L-8: memory_find의 기본 8건은 2,000룬, memory_get의 한 건은 2,500룬 안이다. 룬 수는 SDK가 내는
// 결과 JSON(TextContent 글 = structuredContent 직렬화)으로 잰다. `<`는 6룬, `"`는 2룬이 된다.
const (
	findBudget = 2000
	getBudget  = 2500
)

// 어떤 글자로도 제목이 200룬, `<`와 `"`가 섞인 가장 나쁜 제목이다.
func worstTitle(i int) string {
	r := []rune(strings.Repeat(`<"가`, 70))[:200]
	copy(r, []rune(fmt.Sprintf("%02d", i)))
	return string(r)
}

// 10,000룬에 줄바꿈과 `<`·`"`가 많은 본문이다.
func worstBody() string {
	line := `가나다 <b>"라마"</b> 바사아자차카타파하` + "\n"
	return string([]rune(strings.Repeat(line, 600))[:10000])
}

// 5,000룬 발췌다.
func worstExcerpt(i int) string {
	return string([]rune(fmt.Sprintf("%02d ", i) + strings.Repeat(`<"인용"> `, 800))[:5000])
}

// 126룬 가까운 긴 경로 키의 저장소 범위다.
func worstScope() string {
	return "project:github.com/" + strings.Repeat("deep-path/", 11) + "repo-x"
}

// 이 PC의 실데이터 최대치를 닮은 자료를 만든다: 최악 find 8건 이상과 최악 한 건(출처 27개·links 10개).
func seedWorst(t *testing.T, s *memory.Store) (worstID string) {
	t.Helper()
	var others []string
	for i := 0; i < 10; i++ {
		m, err := s.Save(memory.SaveInput{Kind: "fact", Title: worstTitle(i), Scope: worstScope(), Tags: []string{"t" + strings.Repeat("가", 49)},
			Source: memory.Source{Kind: "file", Ref: "/x/" + fmt.Sprint(i)}})
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, m.ID)
	}
	var extra []memory.Source
	for i := 1; i < 27; i++ {
		extra = append(extra, memory.Source{Kind: "file", Ref: fmt.Sprintf("/%02d/", i) + strings.Repeat("긴경로/", 70), Excerpt: worstExcerpt(i)})
	}
	var tags []string
	for i := 0; i < 10; i++ {
		tags = append(tags, fmt.Sprintf("tag%d", i)+strings.Repeat("나", 40))
	}
	w, err := s.Save(memory.SaveInput{Kind: "fact", Title: worstTitle(99), Body: worstBody(), Scope: worstScope(), Tags: tags,
		Topic: strings.Repeat("<주제>", 20), Tools: []string{"mcp__atlas__write_page", "mcp__atlas__*"},
		Source: memory.Source{Kind: "file", Ref: "/0/" + strings.Repeat("긴경로/", 70), Excerpt: worstExcerpt(0)}, Extra: extra})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range others {
		if _, _, err := s.Link(memory.LinkInput{From: w.ID, To: o, Origin: memory.LinkOriginAI, Note: strings.Repeat(`<"메모"> `, 40)}); err != nil {
			t.Fatal(err)
		}
	}
	return w.ID
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// generic은 structuredContent를 map으로 푼다. 새 꼴의 타입 없이도 모양을 본다.
func generic(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	b, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return m
}

func itemsOf(t *testing.T, res *mcp.CallToolResult) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, it := range generic(t, res)["items"].([]any) {
		out = append(out, it.(map[string]any))
	}
	return out
}

func TestSurface_최악_자료_find_기본_8건은_2000룬_이하(t *testing.T) {
	cs, s := connect(t)
	seedWorst(t, s)
	res := call(t, cs, "memory_find", map[string]any{"scope": "all"}, nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	got := runes(text(res))
	t.Logf("find 기본 %d건: TextContent %d룬", len(itemsOf(t, res)), got)
	if n := len(itemsOf(t, res)); n != 8 {
		t.Errorf("기본 건수 = %d, 8이어야 합니다", n)
	}
	if got > findBudget {
		t.Errorf("find 결과 %d룬 > %d", got, findBudget)
	}
	if sc, _ := json.Marshal(res.StructuredContent); runes(string(sc)) != got {
		t.Errorf("TextContent와 structuredContent의 길이가 다릅니다: %d, %d", got, runes(string(sc)))
	}
}

func TestSurface_최악_자료_get_한_건은_2500룬_이하(t *testing.T) {
	cs, s := connect(t)
	id := seedWorst(t, s)
	res := call(t, cs, "memory_get", map[string]any{"ids": []string{id}}, nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	got := runes(text(res))
	t.Logf("get 한 건: TextContent %d룬", got)
	if got > getBudget {
		t.Errorf("get 결과 %d룬 > %d", got, getBudget)
	}
	if sc, _ := json.Marshal(res.StructuredContent); runes(string(sc)) != got {
		t.Errorf("TextContent와 structuredContent의 길이가 다릅니다: %d, %d", got, runes(string(sc)))
	}
	it := itemsOf(t, res)[0]
	if it["id"] != id || !strings.HasSuffix(it["body"].(string), "… (전문은 full: true)") {
		t.Errorf("본문이 잘려 있어야 합니다: id=%v 끝=%q", it["id"], tail(it["body"].(string), 30))
	}
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}

func TestSurface_대표_자료_get_본문_2000룬과_자르기_표시(t *testing.T) {
	cs, s := connect(t)
	other, err := s.Save(memory.SaveInput{Kind: "fact", Title: "함께 읽을 기억", Source: memory.Source{Kind: "file", Ref: "/a"}})
	if err != nil {
		t.Fatal(err)
	}
	other2, err := s.Save(memory.SaveInput{Kind: "lesson", Title: "또 하나의 관련 기억", Source: memory.Source{Kind: "file", Ref: "/b"}})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for b.Len() < 40000 && runes(b.String()) < 10000 {
		b.WriteString("이 결정은 배포 전에 점검표를 먼저 확인하고, 문제가 없을 때만 다음 단계로 넘어가도록 정한 것이다. 근거는 지난 장애 회고에 있다.\n")
	}
	body := string([]rune(b.String())[:10000])
	m, err := s.Save(memory.SaveInput{Kind: "decision", Title: string([]rune(strings.Repeat("배포 점검표는 먼저 본다 ", 10))[:70]), Body: body, Topic: "배포 점검",
		Scope: "project:github.com/me/app", Tags: []string{"배포", "점검"},
		Source: memory.Source{Kind: "session", Ref: "claude:abc", Excerpt: "배포 전에 점검표부터"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{other.ID, other2.ID} {
		if _, _, err := s.Link(memory.LinkInput{From: m.ID, To: o, Origin: memory.LinkOriginAI}); err != nil {
			t.Fatal(err)
		}
	}
	res := call(t, cs, "memory_get", map[string]any{"ids": []string{m.ID}}, nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	it := itemsOf(t, res)[0]
	got := it["body"].(string)
	const mark = "… (전문은 full: true)"
	if !strings.HasSuffix(got, mark) {
		t.Fatalf("자르기 표시가 없습니다: %q", tail(got, 40))
	}
	if n := runes(strings.TrimSuffix(got, mark)); n != 2000 {
		t.Errorf("본문 = %d룬, 2000이어야 합니다", n)
	}
	if !strings.HasPrefix(body, strings.TrimSuffix(got, mark)) {
		t.Error("잘린 본문은 전문의 앞부분이어야 합니다")
	}
	if n := runes(text(res)); n > getBudget {
		t.Errorf("get 결과 %d룬 > %d", n, getBudget)
	}
	if src, _ := it["sources"].([]any); len(src) != 1 {
		t.Errorf("출처 = %v", it["sources"])
	}
	if ls, _ := it["links"].([]any); len(ls) != 2 {
		t.Errorf("links = %v", it["links"])
	}
}

func TestSurface_limit_기본_8_0이하는_8_30초과는_30(t *testing.T) {
	cs, s := connect(t)
	for i := 0; i < 40; i++ {
		if _, err := s.Save(memory.SaveInput{Kind: "fact", Title: fmt.Sprintf("기억 %02d", i), Source: memory.Source{Kind: "file", Ref: fmt.Sprint("/", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		args map[string]any
		want int
	}{
		{map[string]any{"scope": "all"}, 8},
		{map[string]any{"scope": "all", "limit": 0}, 8},
		{map[string]any{"scope": "all", "limit": -3}, 8},
		{map[string]any{"scope": "all", "limit": 5}, 5},
		{map[string]any{"scope": "all", "limit": 30}, 30},
		{map[string]any{"scope": "all", "limit": 31}, 30},
		{map[string]any{"scope": "all", "limit": 100}, 30},
	} {
		res := call(t, cs, "memory_find", c.args, nil)
		if n := len(itemsOf(t, res)); n != c.want {
			t.Errorf("%v: %d건, %d건이어야 합니다", c.args, n, c.want)
		}
	}
}

func TestSurface_find_detail_index_summary_그_밖은_도구_오류(t *testing.T) {
	cs, s := connect(t)
	if _, err := s.Save(memory.SaveInput{Kind: "fact", Title: "발췌를 볼 기억", Body: "본문 한 줄\n둘째 줄", Tags: []string{"x"}, Source: memory.Source{Kind: "file", Ref: "/a"}}); err != nil {
		t.Fatal(err)
	}
	res := call(t, cs, "memory_find", map[string]any{"scope": "all"}, nil)
	it := itemsOf(t, res)[0]
	keys := map[string]bool{}
	for k := range it {
		keys[k] = true
	}
	for _, k := range []string{"id", "kind", "state", "appliesTo", "title"} {
		if !keys[k] {
			t.Errorf("색인 꼴에 %s가 없습니다: %v", k, it)
		}
		delete(keys, k)
	}
	if len(keys) != 0 {
		t.Errorf("색인 꼴에 다섯 칸 밖의 칸이 있습니다: %v", it)
	}

	for _, d := range []string{"index", ""} {
		if r := call(t, cs, "memory_find", map[string]any{"scope": "all", "detail": d}, nil); r.IsError || len(itemsOf(t, r)[0]) != 5 {
			t.Errorf("detail %q: %s", d, text(r))
		}
	}
	res = call(t, cs, "memory_find", map[string]any{"scope": "all", "detail": "summary"}, nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	it = itemsOf(t, res)[0]
	if it["summary"] != "본문 한 줄 둘째 줄" || it["tags"] == nil {
		t.Errorf("summary 꼴: %v", it)
	}
	if _, has := it["pinned"]; has {
		t.Errorf("빈 pinned는 생략해야 합니다: %v", it)
	}
	if r := call(t, cs, "memory_find", map[string]any{"scope": "all", "detail": "bogus"}, nil); !r.IsError || !strings.Contains(text(r), "detail") {
		t.Errorf("bogus: IsError=%v %q", r.IsError, text(r))
	}
}

func TestSurface_get_links는_제목_id_줄이고_5개를_넘으면_외_N개(t *testing.T) {
	cs, s := connect(t)
	m, err := s.Save(memory.SaveInput{Kind: "fact", Title: "기준", Body: "본문", Source: memory.Source{Kind: "file", Ref: "/a"}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 7; i++ {
		o, err := s.Save(memory.SaveInput{Kind: "fact", Title: fmt.Sprintf("상대 %d ", i) + strings.Repeat("길", 100), Source: memory.Source{Kind: "file", Ref: fmt.Sprint("/o", i)}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, o.ID)
		if _, _, err := s.Link(memory.LinkInput{From: m.ID, To: o.ID, Origin: memory.LinkOriginAI, Note: "이유" + fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	lines := func(args map[string]any) []string {
		var out []string
		for _, l := range itemsOf(t, call(t, cs, "memory_get", args, nil))[0]["links"].([]any) {
			out = append(out, l.(string))
		}
		return out
	}
	def := lines(map[string]any{"ids": []string{m.ID}})
	if len(def) != 6 || def[5] != "외 2개" {
		t.Fatalf("기본 links: %q", def)
	}
	for _, l := range def[:5] {
		title := strings.TrimSuffix(l[:strings.LastIndex(l, " — ")], "")
		if !strings.HasPrefix(title, "상대 ") || runes(title) > 40 || strings.Contains(l, "이유") {
			t.Errorf("기본 links는 제목(≤40룬) — id뿐입니다: %q", l)
		}
	}
	full := lines(map[string]any{"ids": []string{m.ID}, "full": true})
	if len(full) != 7 {
		t.Fatalf("full links: %q", full)
	}
	for i, id := range ids {
		var line string
		for _, l := range full {
			if strings.Contains(l, " — "+id) {
				line = l
			}
		}
		if !strings.Contains(line, strings.Repeat("길", 100)+" — "+id) || !strings.HasSuffix(line, "· 이유"+fmt.Sprint(i)) {
			t.Errorf("full 줄 %d = %q", i, line)
		}
	}
}

func TestSurface_full_true는_본문_출처_links_전부(t *testing.T) {
	cs, s := connect(t)
	id := seedWorst(t, s)
	res := call(t, cs, "memory_get", map[string]any{"ids": []string{id}, "full": true}, nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	it := itemsOf(t, res)[0]
	if it["body"].(string) != worstBody() {
		t.Errorf("본문 전문이 아닙니다: %d룬", runes(it["body"].(string)))
	}
	srcs := it["sources"].([]any)
	if len(srcs) != 27 {
		t.Fatalf("출처 = %d개", len(srcs))
	}
	for _, x := range srcs {
		if n := runes(x.(map[string]any)["excerpt"].(string)); n != 5000 {
			t.Errorf("발췌 = %d룬, 자르지 않아야 합니다", n)
		}
	}
	if ls := it["links"].([]any); len(ls) != 10 {
		t.Errorf("links = %d개", len(ls))
	}
	// 기본 호출은 출처를 2개로 줄이고 수를 알린다.
	def := itemsOf(t, call(t, cs, "memory_get", map[string]any{"ids": []string{id}}, nil))[0]
	shown, _ := def["sources"].([]any)
	more, _ := def["sourcesMore"].(float64)
	if len(shown) > 2 || len(shown)+int(more) != 27 {
		t.Errorf("기본 출처 = %d개 + 더 있음 %v, 합쳐 27이고 보이는 것은 2개 이하여야 합니다", len(shown), more)
	}
}

func TestSurface_Tools_Call과_MCP_structuredContent는_같은_값(t *testing.T) {
	cs, s := connect(t)
	id := seedWorst(t, s)
	tl := NewTools(s, Options{Project: "project:github.com/me/app", ProjectLabel: "github.com/me/app", Version: "test"})
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"memory_find", map[string]any{"scope": "all"}},
		{"memory_find", map[string]any{"scope": "all", "detail": "summary", "limit": 3}},
		{"memory_get", map[string]any{"ids": []string{id}}},
		{"memory_get", map[string]any{"ids": []string{id}, "full": true, "history": true}},
	} {
		in, _ := json.Marshal(c.args)
		got, err := tl.Call(c.name, in, "claude", "s1")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var a, b any
		raw, _ := json.Marshal(got)
		_ = json.Unmarshal(raw, &a)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s %v: Tools.Call과 MCP 결과가 다릅니다", c.name, c.args)
		}
	}
}

// 어떤 글자로도 색인 한 줄은 247룬, get 한 건은 2,488룬(결과 2,500룬) 안이다.
func TestSurface_글자가_모두_이스케이프돼도_상한을_지킨다(t *testing.T) {
	esc := strings.Repeat("<", 400)
	m := memory.Memory{ID: "m-0123456789ab", Kind: "decision", State: "confirmed", AppliesTo: "repo:" + esc, Title: esc[:200], Topic: esc, StaleReason: esc,
		Body: strings.Repeat("<", 5000), Tags: []string{esc[:50], esc[:50]}, Tools: []string{esc}, Version: 3, UpdatedAt: 1760000000000,
		Sources: []memory.Source{{Kind: "file", Ref: esc, Excerpt: esc}, {Kind: "file", Ref: esc, Excerpt: esc}, {Kind: "file", Ref: esc, Excerpt: esc}},
		Links:   []memory.MemoryLink{{MemoryID: "m-aaaaaaaaaaaa"}, {MemoryID: "m-bbbbbbbbbbbb"}}}
	for _, d := range []string{DetailIndex} {
		it := findItem(m, d)
		if n := jsonLen(it); n > findItemBudget {
			t.Errorf("색인 한 줄 %d룬 > %d: %+v", n, findItemBudget, it)
		}
		if it.ID != m.ID || it.Title == "" {
			t.Errorf("id와 제목은 남아야 합니다: %+v", it)
		}
	}
	v := memoryView(m, func(string) string { return esc }, false)
	if n := jsonLen(GetOut{Items: []MemoryView{v}}); n > getBudget {
		t.Errorf("get 한 건 %d룬 > %d", n, getBudget)
	}
	if !strings.HasSuffix(v.AppliesTo, "…") {
		t.Errorf("비정상 자료에서는 appliesTo가 …로 잘려야 합니다: %q", tail(v.AppliesTo, 10))
	}
	if !strings.HasSuffix(v.Body, "… (전문은 full: true)") || v.ID != m.ID {
		t.Errorf("본문 끝 = %q", tail(v.Body, 30))
	}
	// 짧은 기억은 아무것도 줄이지 않는다.
	short := memory.Memory{ID: "m-0123456789ab", Kind: "fact", State: "candidate", AppliesTo: "user", Title: "짧은 제목", Body: "짧은 본문", Tags: []string{"a"}, Version: 1}
	sv := memoryView(short, func(string) string { return "" }, false)
	if sv.Body != "짧은 본문" || sv.Title != "짧은 제목" || len(sv.Tags) != 1 {
		t.Errorf("짧은 기억이 줄었습니다: %+v", sv)
	}
}

// L-8: tools/list 전체는 8,000룬, MCP 안내는 1,200룬 안이고 어느 도구도 출력 스키마를 내지 않는다.
func TestSurface_tools_list는_8000룬_이하이고_출력_스키마가_없다(t *testing.T) {
	cs, _ := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 7 {
		t.Fatalf("도구 %d개", len(res.Tools))
	}
	b, _ := json.Marshal(res)
	n := runes(string(b))
	t.Logf("tools/list 전체 %d룬", n)
	if n > 8000 {
		t.Errorf("tools/list %d룬, 상한 8,000", n)
	}
	for _, tl := range res.Tools {
		if tl.OutputSchema != nil {
			t.Errorf("%s: 출력 스키마가 있습니다", tl.Name)
		}
		in, _ := json.Marshal(tl.InputSchema)
		t.Logf("%s: 입력 스키마 %d룬, description %d룬", tl.Name, runes(string(in)), runes(tl.Description))
	}
}

func TestSurface_안내는_1200룬_이하이고_앞_512룬에_언제_memory_find를_부르나가_있다(t *testing.T) {
	cs, _ := connect(t)
	ins := cs.InitializeResult().Instructions
	t.Logf("안내 %d룬", runes(ins))
	if runes(ins) > 1200 {
		t.Errorf("안내 %d룬, 상한 1,200", runes(ins))
	}
	head := string([]rune(ins)[:512])
	for _, want := range []string{"memory_find", "설계", "결정", "규칙", "문서 위치", "작업 방식", "전에"} {
		if !strings.Contains(head, want) {
			t.Errorf("안내 앞 512룬에 %q가 없습니다:\n%s", want, head)
		}
	}
}

// 기억을 거절하는 규칙은 스킬이 없는 호스트도 안내나 입력 설명으로 안다.
func TestSurface_거절_규칙이_안내와_입력_설명에_남아_있다(t *testing.T) {
	cs, _ := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	all := cs.InitializeResult().Instructions
	for _, tl := range res.Tools {
		in, _ := json.Marshal(tl.InputSchema)
		all += string(in) + tl.Description
	}
	for _, want := range []string{"topic", "appliesTo", "1,000자", "pin"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q가 없습니다", want)
		}
	}
}

// 도구 오류 결과에는 structuredContent가 없고 오류 글이 Content에 그대로 있다.
func TestSurface_도구_오류에는_structuredContent가_없다(t *testing.T) {
	cs, _ := connect(t)
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"memory_find", map[string]any{"scope": "bogus"}, "scope는 here·global·all입니다"},
		{"memory_get", map[string]any{"ids": []string{}}, "ids는 1~20개입니다"},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("%s: 오류여야 합니다", c.name)
		}
		if res.StructuredContent != nil {
			t.Errorf("%s: 오류인데 structuredContent가 있습니다: %v", c.name, res.StructuredContent)
		}
		if len(res.Content) != 1 || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, c.want) {
			t.Errorf("%s: 오류 글 %+v", c.name, res.Content)
		}
	}
}

// 줄바꿈·`<`가 많은 긴 본문이어도 제목·links·출처는 그대로 두고 본문만 자리에 맞게 줄인다.
func TestSurface_get_본문이_길어도_제목_links_출처를_먼저_지우지_않는다(t *testing.T) {
	cs, s := connect(t)
	var links []string
	for i := 0; i < 3; i++ {
		o, err := s.Save(memory.SaveInput{Kind: "fact", Title: fmt.Sprintf("함께 읽을 기억 %d", i), Source: memory.Source{Kind: "file", Ref: fmt.Sprintf("/o/%d", i)}})
		if err != nil {
			t.Fatal(err)
		}
		links = append(links, o.ID)
	}
	body := string([]rune(strings.Repeat("이 결정은 배포 전에 점검표를 먼저 확인하고 문제가 없을 때만 넘어가도록 정한 것이다 <근거>\n", 200))[:10000])
	title := strings.TrimSpace(string([]rune(strings.Repeat("배포 점검표는 먼저 본다 ", 10))[:70]))
	m, err := s.Save(memory.SaveInput{Kind: "decision", Title: title, Body: body, Topic: "배포 점검", Scope: "project:github.com/me/app",
		Tags: []string{"배포"}, Source: memory.Source{Kind: "session", Ref: "claude:abc", Excerpt: "배포 전에 점검표부터"},
		Extra: []memory.Source{{Kind: "file", Ref: "/b", Excerpt: "둘째 출처"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range links {
		if _, _, err := s.Link(memory.LinkInput{From: m.ID, To: o, Origin: memory.LinkOriginAI}); err != nil {
			t.Fatal(err)
		}
	}
	res := call(t, cs, "memory_get", map[string]any{"ids": []string{m.ID}}, nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	it := itemsOf(t, res)[0]
	got := it["body"].(string)
	const mark = "… (전문은 full: true)"
	t.Logf("한 건 %d룬, 본문 %d룬", runes(text(res)), runes(got))
	if it["title"] != title {
		t.Errorf("제목이 줄었습니다: %q", it["title"])
	}
	if ls, _ := it["links"].([]any); len(ls) != 3 || strings.HasPrefix(fmt.Sprint(ls[0]), "외 ") {
		t.Errorf("links = %v", it["links"])
	}
	if src, _ := it["sources"].([]any); len(src) != 2 {
		t.Errorf("출처 = %v", it["sources"])
	}
	if !strings.HasSuffix(got, mark) || !strings.HasPrefix(body, strings.TrimSuffix(got, mark)) {
		t.Errorf("본문은 표시가 붙은 앞부분이어야 합니다: %q", tail(got, 40))
	}
	if runes(text(res)) > getBudget {
		t.Errorf("get 결과 %d룬 > %d", runes(text(res)), getBudget)
	}
	if runes(strings.TrimSuffix(got, mark)) < 1000 {
		t.Errorf("본문이 1,000룬보다 짧습니다")
	}
}

func TestSurface_안내_첫_문장은_모든_답_앞에_찾기를_시키지_않는다(t *testing.T) {
	cs, _ := connect(t)
	ins := cs.InitializeResult().Instructions
	if strings.Contains(ins, "답하거나 일하기 전에") {
		t.Errorf("안내가 모든 답 앞에 찾기를 시킵니다:\n%s", ins)
	}
}

func TestSurface_get_기본_보기는_긴_topic과_appliesTo를_자르지_않는다(t *testing.T) {
	cs, s := connect(t)
	topic := strings.Repeat("주제", 80) // 160룬
	scope := worstScope()
	m, err := s.Save(memory.SaveInput{Kind: "fact", Title: "긴 키", Body: "본문", Scope: scope, Topic: topic,
		Source: memory.Source{Kind: "file", Ref: "/x"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, full := range []bool{false, true} {
		res := call(t, cs, "memory_get", map[string]any{"ids": []string{m.ID}, "full": full}, nil)
		if res.IsError {
			t.Fatal(text(res))
		}
		it := itemsOf(t, res)[0]
		if it["topic"] != m.Topic || it["appliesTo"] != m.AppliesTo {
			t.Errorf("full=%v topic=%q appliesTo=%q, 원문 %q %q", full, it["topic"], it["appliesTo"], m.Topic, m.AppliesTo)
		}
		if runes(m.Topic) <= 120 || runes(m.AppliesTo) <= 100 {
			t.Fatalf("자료가 짧습니다: %d %d", runes(m.Topic), runes(m.AppliesTo))
		}
	}
}

func TestSurface_get_극단_자료는_topic을_비우고_잘린_topic을_만들지_않는다(t *testing.T) {
	cs, s := connect(t)
	id := seedWorst(t, s)
	res := call(t, cs, "memory_get", map[string]any{"ids": []string{id}}, nil)
	it := itemsOf(t, res)[0]
	if got, _ := it["topic"].(string); got != "" {
		t.Errorf("topic = %q, 빈 값이어야 합니다", got)
	}
	if a, _ := it["appliesTo"].(string); strings.HasSuffix(a, "…") || a == "" {
		t.Errorf("appliesTo가 바뀌었습니다: %q", a)
	}
}
