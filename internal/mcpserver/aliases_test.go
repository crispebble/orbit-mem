package mcpserver

// 별칭과 찾기 질의(명세 Q-1·Q-2·Q-4): memory_save·memory_update가 별칭을 받고, memory_find가 별칭으로도 찾고 질의의 조사를
// 떼고(스위치 FindStripParticles), 별칭은 본문 글자 한도와 find·get 응답 글자 한도를 늘리지 않는다.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func saveFact(t *testing.T, s *memory.Store, title, body string, aliases []string) memory.Memory {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Body: body, Scope: memory.ScopeGlobal, Aliases: aliases,
		Source: memory.Source{Kind: memory.SourceFile, Ref: "/x/" + title}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// connectWith is connect with the options o over a store at path that seed fills first.
func connectSeeded(t *testing.T, path string, o Options, seed func(*memory.Store)) *mcp.ClientSession {
	t.Helper()
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	seed(s)
	o.Project, o.ProjectLabel, o.Version = "project:github.com/me/app", "github.com/me/app", "test"
	return connectServer(t, New(s, o))
}

func foundBy(t *testing.T, cs *mcp.ClientSession, query string) []string {
	t.Helper()
	var out FindOut
	res := call(t, cs, "memory_find", map[string]any{"query": query, "scope": "all"}, &out)
	if res.IsError {
		t.Fatal(text(res))
	}
	var ids []string
	for _, it := range out.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestMemoryFind_배포를로_찾으면_배포가_든_기억이_나온다(t *testing.T) {
	cs, s := connect(t)
	m := saveFact(t, s, "배포 전에 점검표를 본다", "점검표 항목은 문서에 있다", nil)
	saveFact(t, s, "백업은 매일 밤에 돈다", "", nil)
	if ids := foundBy(t, cs, "배포를"); len(ids) != 1 || ids[0] != m.ID {
		t.Fatalf("배포를 → %v, want [%s]", ids, m.ID)
	}
}

func TestMemoryFind_붙여_쓴_한글_낱말의_일부로_찾아진다(t *testing.T) {
	cs, s := connect(t)
	m := saveFact(t, s, "배포점검표는 먼저 본다", "", nil)
	saveFact(t, s, "백업은 매일 밤에 돈다", "", nil)
	for _, q := range []string{"점검", "점검을", "점검표를"} {
		if ids := foundBy(t, cs, q); len(ids) != 1 || ids[0] != m.ID {
			t.Errorf("%q → %v, want [%s]", q, ids, m.ID)
		}
	}
}

// strip is a connection whose memory_find stems the query's words (on) or leaves the query as written (off) over
// a store that seed fills.
func strip(t *testing.T, on bool, seed func(*memory.Store)) *mcp.ClientSession {
	t.Helper()
	return connectSeeded(t, filepath.Join(t.TempDir(), "m.db"), Options{LoadFindStripParticles: func() bool { return on }}, seed)
}

// 기본(옵션이 없으면)은 끔이다: 질의를 쓴 그대로 찾는다.
func TestMemoryFind_조사_떼기는_기본이_꺼짐이다(t *testing.T) {
	cs, s := connect(t)
	saveFact(t, s, "꼴은 이상한 점검", "", nil)
	if ids := foundBy(t, cs, "꼴이"); len(ids) != 0 {
		t.Fatalf("default: 꼴이 → %v, want none (the query is left as written)", ids)
	}
}

// 한 글자 명사 뒤의 조사(「꼴이」)는 떼지 않으면 어느 기억에도 없는 두 글자 토큰이 된다. 켜면 「꼴」로도 찾지만 「꼴이」가 정확히 든
// 기억이 「꼴은」만 든 기억보다 앞이다. 끄면 정확히 든 기억만 나온다.
func TestMemoryFind_켜면_꼴이로_꼴은_기억도_찾고_정확히_맞은_기억이_앞이다(t *testing.T) {
	var exact, loose string
	on := strip(t, true, func(s *memory.Store) {
		exact = saveFact(t, s, "꼴이 이상한 점검", "", nil).ID
		loose = saveFact(t, s, "꼴은 이상한 점검", "", nil).ID
	})
	if ids := foundBy(t, on, "꼴이"); len(ids) != 2 || ids[0] != exact || ids[1] != loose {
		t.Fatalf("on: 꼴이 → %v, want [%s %s]", ids, exact, loose)
	}
	off := strip(t, false, func(s *memory.Store) {
		exact = saveFact(t, s, "꼴이 이상한 점검", "", nil).ID
		saveFact(t, s, "꼴은 이상한 점검", "", nil)
	})
	if ids := foundBy(t, off, "꼴이"); len(ids) != 1 || ids[0] != exact {
		t.Fatalf("off: 꼴이 → %v, want [%s]", ids, exact)
	}
}

// 「지」를 어미로 잘못 떼도 원래 낱말이 남아, 「페이지」 기억이 같은 앞 두 글자의 「페이로드」 기억보다 앞이다.
func TestMemoryFind_켜도_페이지_기억이_페이로드_기억보다_앞이다(t *testing.T) {
	var page, payload string
	on := strip(t, true, func(s *memory.Store) {
		payload = saveFact(t, s, "페이로드 크기 제한", "", nil).ID
		page = saveFact(t, s, "페이지 크기 제한", "", nil).ID
	})
	ids := foundBy(t, on, "페이지 크기 제한")
	if len(ids) < 1 || ids[0] != page {
		t.Fatalf("on: %v, want %s first (payload %s)", ids, page, payload)
	}
}

func TestMemoryFind_별칭으로_찾고_find_응답에는_별칭이_없다(t *testing.T) {
	cs, s := connect(t)
	m := saveFact(t, s, "배포 점검표는 먼저 본다", "", []string{"유일한별칭어"})
	if ids := foundBy(t, cs, "유일한별칭어"); len(ids) != 1 || ids[0] != m.ID {
		t.Fatalf("alias → %v, want [%s]", ids, m.ID)
	}
	for _, detail := range []string{"index", "summary"} {
		res := call(t, cs, "memory_find", map[string]any{"query": "유일한별칭어", "scope": "all", "detail": detail}, nil)
		if strings.Contains(text(res), "유일한별칭어") {
			t.Errorf("detail %s: the find response carries the alias: %s", detail, text(res))
		}
	}
}

func TestMemoryGet_별칭은_full에서만_보인다(t *testing.T) {
	cs, s := connect(t)
	m := saveFact(t, s, "배포 점검표는 먼저 본다", "", []string{"유일한별칭어", "go-live"})
	plain := call(t, cs, "memory_get", map[string]any{"ids": []string{m.ID}}, nil)
	if strings.Contains(text(plain), "유일한별칭어") {
		t.Errorf("the default get carries the alias (its budget is full): %s", text(plain))
	}
	var out struct {
		Aliases map[string][]string `json:"aliases"`
	}
	call(t, cs, "memory_get", map[string]any{"ids": []string{m.ID}, "full": true}, &out)
	if got := out.Aliases[m.ID]; len(got) != 2 || got[0] != "유일한별칭어" {
		t.Fatalf("full get aliases = %v", out.Aliases)
	}
}

func TestMemorySave_별칭을_받고_수정으로_바꾸고_비운다(t *testing.T) {
	cs, _ := connect(t)
	var saved SaveOut
	res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "배포 점검표는 먼저 본다", "appliesTo": "user",
		"aliases": []string{"릴리스", "Release Checklist"}, "source": map[string]any{"kind": "file", "ref": "/x/a"}}, &saved)
	if res.IsError || !saved.Saved || len(saved.Memory.Aliases) != 2 {
		t.Fatalf("save: %s %+v", text(res), saved.Memory.Aliases)
	}
	id := saved.Memory.ID
	if ids := foundBy(t, cs, "릴리스"); len(ids) != 1 || ids[0] != id {
		t.Fatalf("saved alias → %v", ids)
	}
	var upd MemoryOut
	res = call(t, cs, "memory_update", map[string]any{"id": id, "aliases": []string{"출시"}}, &upd)
	if res.IsError || len(upd.Memory.Aliases) != 1 {
		t.Fatalf("update: %s", text(res))
	}
	if len(foundBy(t, cs, "릴리스")) != 0 || len(foundBy(t, cs, "출시")) != 1 {
		t.Fatal("the update did not swap the alias")
	}
	var cleared MemoryOut // 새 값: 별칭이 없으면 키가 없어 이미 채운 값 위에 풀면 옛 별칭이 남는다
	res = call(t, cs, "memory_update", map[string]any{"id": id, "aliases": []string{}}, &cleared)
	if res.IsError || len(cleared.Memory.Aliases) != 0 || len(foundBy(t, cs, "출시")) != 0 {
		t.Fatalf("an empty list did not clear: %s", text(res))
	}
	// 한도를 넘으면 버리지 않고 오류로 알린다.
	over := make([]string, 0, 11)
	for i := 0; i < 11; i++ {
		over = append(over, "별칭"+string(rune('가'+i)))
	}
	res = call(t, cs, "memory_update", map[string]any{"id": id, "aliases": over}, nil)
	if !res.IsError || !strings.Contains(text(res), "aliases") {
		t.Fatalf("11 aliases: error = %v %s", res.IsError, text(res))
	}
}

// 규칙의 본문은 1,000자 이하다. 별칭은 그 한도에 세지 않는다.
func TestMemorySave_별칭은_규칙_본문_1000자_한도에_세지_않는다(t *testing.T) {
	cs, _ := connect(t)
	body := strings.Repeat("가", 1000)
	aliases := make([]string, 0, memory.MaxAliases)
	for i := 0; i < memory.MaxAliases; i++ {
		aliases = append(aliases, strings.Repeat("나", memory.MaxAliasRunes-1)+string(rune('a'+i)))
	}
	var saved SaveOut
	res := call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "별칭 한도 시험", "topic": "별칭 한도", "appliesTo": "user",
		"body": body, "aliases": aliases, "source": map[string]any{"kind": "quote", "excerpt": "별칭 한도 시험"}}, &saved)
	if res.IsError || !saved.Saved {
		t.Fatalf("1,000-rune body with full aliases was refused: %s", text(res))
	}
}

// 별칭은 find·get 응답의 글자 한도를 늘리지 않는다: 가장 나쁜 자료의 한 건에 별칭을 가득 달아도 같은 한도 안이다.
func TestSurface_최악_자료에_별칭을_가득_달아도_find_get_한도_안이다(t *testing.T) {
	cs, s := connect(t)
	id := seedWorst(t, s)
	full := make([]string, 0, memory.MaxAliases)
	for i := 0; i < memory.MaxAliases; i++ {
		full = append(full, strings.Repeat("가", memory.MaxAliasRunes-1)+string(rune('a'+i)))
	}
	if _, err := s.Update(memory.UpdateInput{ID: id, Aliases: &full}); err != nil {
		t.Fatal(err)
	}
	f := call(t, cs, "memory_find", map[string]any{"scope": "all"}, nil)
	g := call(t, cs, "memory_get", map[string]any{"ids": []string{id}}, nil)
	t.Logf("find 기본 %d룬, get 한 건 %d룬", runes(text(f)), runes(text(g)))
	if runes(text(f)) > findBudget || runes(text(g)) > getBudget {
		t.Errorf("find %d룬(한도 %d), get %d룬(한도 %d)", runes(text(f)), findBudget, runes(text(g)), getBudget)
	}
}
