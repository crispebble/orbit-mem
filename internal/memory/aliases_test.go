package memory

// 별칭(명세 Q-1): 저장·수정할 때 받아 색인한다. 별칭에만 있는 낱말로도 찾아지고, 본문 글자 한도에 세지 않고,
// 개수·길이를 넘으면 조용히 버리지 않고 거절하고, 내보내기·가져오기·버전 스냅숏·지우기에서도 한 몸으로 다룬다.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func saveAliased(t *testing.T, s *Store, title string, aliases []string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindFact, Title: title, Body: "본문은 별칭과 무관하다", Scope: ScopeGlobal, Aliases: aliases,
		Source: Source{Kind: SourceSession, Ref: "claude:abc"}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func idsOf(hits []Memory) []string {
	var out []string
	for _, m := range hits {
		out = append(out, m.ID)
	}
	return out
}

func TestFind_별칭에만_있는_낱말로_찾으면_나온다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveAliased(t, s, "배포 점검표는 먼저 본다", []string{"릴리스", "release checklist"})
	other := saveAliased(t, s, "백업은 매일 밤에 돈다", nil)
	for _, q := range []string{"릴리스", "release", "checklist"} {
		got, err := s.Find(FindInput{Query: q, AllProjects: true})
		if err != nil {
			t.Fatal(err)
		}
		if ids := idsOf(got); len(ids) != 1 || ids[0] != m.ID {
			t.Errorf("%q → %v, want only %s (not %s)", q, ids, m.ID, other.ID)
		}
	}
	if got := getAliased(t, s, m.ID); len(got.Aliases) != 2 || got.Aliases[0] != "릴리스" {
		t.Errorf("aliases read back = %v", got.Aliases)
	}
}

func getAliased(t *testing.T, s *Store, id string) Memory {
	t.Helper()
	m, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpdate_별칭을_고치면_새_별칭으로_찾아지고_옛_별칭으로는_안_찾아진다(t *testing.T) {
	s, c := newTestStore(t)
	m := saveAliased(t, s, "배포 점검표는 먼저 본다", []string{"릴리스"})
	c.t = c.t.Add(time.Minute)
	next := []string{"출시", "go-live"}
	if _, err := s.Update(UpdateInput{ID: m.ID, Aliases: &next}); err != nil {
		t.Fatal(err)
	}
	find := func(q string) int {
		got, err := s.Find(FindInput{Query: q, AllProjects: true})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if find("출시") != 1 || find("go-live") != 1 {
		t.Error("the new aliases do not find the memory")
	}
	if find("릴리스") != 0 {
		t.Error("the old alias still finds the memory")
	}
	// 빈 목록은 별칭을 비운다. 주지 않은 것(nil)은 그대로 둔다.
	c.t = c.t.Add(time.Minute)
	if _, err := s.Update(UpdateInput{ID: m.ID, Reason: "제목만"}); err != nil {
		t.Fatal(err)
	}
	if got := getAliased(t, s, m.ID); len(got.Aliases) != 2 {
		t.Fatalf("an update without aliases changed them: %v", got.Aliases)
	}
	empty := []string{}
	if _, err := s.Update(UpdateInput{ID: m.ID, Aliases: &empty}); err != nil {
		t.Fatal(err)
	}
	if got := getAliased(t, s, m.ID); len(got.Aliases) != 0 || find("출시") != 0 {
		t.Fatalf("an empty list did not clear: %v", got.Aliases)
	}
}

func TestSave_별칭은_본문_한도에_세지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	body := strings.Repeat("가", 32<<10/3) // 본문 한도(32KiB)에 가까운 한글 본문
	var aliases []string
	for i := 0; i < MaxAliases; i++ {
		aliases = append(aliases, strings.Repeat("나", MaxAliasRunes-2)+fmt.Sprintf("%02d", i)) // 꽉 찬 길이, 서로 다른 별칭
	}
	m, err := s.Save(SaveInput{Kind: KindFact, Title: "한도 시험", Body: body, Scope: ScopeGlobal, Aliases: aliases,
		Source: Source{Kind: SourceSession, Ref: "claude:abc"}})
	if err != nil {
		t.Fatalf("body near the limit with full aliases: %v", err)
	}
	if len(m.Aliases) != MaxAliases {
		t.Fatalf("aliases kept = %d, want %d", len(m.Aliases), MaxAliases)
	}
	if len(m.Body) != len(body) {
		t.Fatalf("body changed: %d → %d", len(body), len(m.Body))
	}
}

func TestSave_별칭이_한도를_넘으면_버리지_않고_거절한다(t *testing.T) {
	s, _ := newTestStore(t)
	var many []string
	for i := 0; i <= MaxAliases; i++ {
		many = append(many, "별칭"+string(rune('가'+i)))
	}
	for name, aliases := range map[string][]string{
		"개수":  many,
		"길이":  {strings.Repeat("가", MaxAliasRunes+1)},
		"비밀값": {"sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"},
	} {
		_, err := s.Save(SaveInput{Kind: KindFact, Title: "거절 " + name, Scope: ScopeGlobal, Aliases: aliases,
			Source: Source{Kind: SourceSession, Ref: "x"}})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got, _ := s.List(StateCandidate); len(got) != 0 {
		t.Errorf("rejected saves left memories: %d", len(got))
	}
}

func TestCleanAliases_공백을_접고_대소문자_무시로_중복을_없앤다(t *testing.T) {
	got, err := cleanAliases([]string{"  Release   Checklist ", "release checklist", "", "  ", "RELEASE", "출시\t\n준비"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Release Checklist", "RELEASE", "출시 준비"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, err := cleanAliases(nil); err != nil || len(got) != 0 {
		t.Fatalf("nil → %v, %v", got, err)
	}
}

func TestSave_같은_제목으로_다른_별칭을_주면_적용하지_않고_알린다(t *testing.T) {
	s, _ := newTestStore(t)
	saveAliased(t, s, "배포 점검표는 먼저 본다", []string{"릴리스"})
	_, rep, err := s.SaveWith(SaveInput{Kind: KindFact, Title: "배포 점검표는 먼저 본다", Scope: ScopeGlobal, Aliases: []string{"출시"},
		Source: Source{Kind: SourceSession, Ref: "claude:def"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range rep.FieldsKept {
		found = found || f == "aliases"
	}
	if !found {
		t.Fatalf("FieldsKept = %v, want aliases", rep.FieldsKept)
	}
}

func TestExport_별칭은_내보내고_가져오면_그대로_찾아진다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveAliased(t, s, "배포 점검표는 먼저 본다", []string{"릴리스", "release checklist"})
	plain := saveAliased(t, s, "백업은 매일 밤에 돈다", nil)
	list, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]string{}
	for _, e := range list {
		b, _ := json.Marshal(e)
		lines[e.ID] = string(b)
	}
	// 별칭이 없는 기억의 줄은 옛 버전이 내보낸 줄과 같은 꼴이다(키가 없다). 옛 버전은 모르는 키를 무시하고 읽는다.
	if strings.Contains(lines[plain.ID], `"aliases"`) {
		t.Errorf("a memory without aliases writes the key: %s", lines[plain.ID])
	}
	if !strings.Contains(lines[m.ID], `"aliases":["릴리스","release checklist"]`) {
		t.Errorf("export line lacks the aliases: %s", lines[m.ID])
	}
	var old struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(lines[m.ID]), &old); err != nil || old.ID != m.ID {
		t.Fatalf("a reader without the field fails: %v", err)
	}

	dst, _ := newTestStore(t)
	for _, e := range list {
		var back Memory
		if err := json.Unmarshal([]byte(lines[e.ID]), &back); err != nil {
			t.Fatal(err)
		}
		if res, err := dst.Import(back, "옮김", false); err != nil || res != ImportDone {
			t.Fatalf("import %s = %q, %v", e.ID, res, err)
		}
	}
	got, err := dst.Find(FindInput{Query: "릴리스", AllProjects: true})
	if err != nil || len(got) != 1 || got[0].ID != m.ID || len(got[0].Aliases) != 2 {
		t.Fatalf("after import: %v %v", got, err)
	}
}

func TestVersions_스냅숏은_그_버전의_별칭을_담는다(t *testing.T) {
	s, c := newTestStore(t)
	m := saveAliased(t, s, "배포 점검표는 먼저 본다", []string{"릴리스"})
	c.t = c.t.Add(time.Minute)
	next := []string{"출시"}
	if _, err := s.Update(UpdateInput{ID: m.ID, Aliases: &next}); err != nil {
		t.Fatal(err)
	}
	vs, err := s.History(m.ID)
	if err != nil || len(vs) != 2 {
		t.Fatalf("history = %v, %v", vs, err)
	}
	if vs[0].Memory.Aliases[0] != "릴리스" || vs[1].Memory.Aliases[0] != "출시" {
		t.Fatalf("snapshots = %v, %v", vs[0].Memory.Aliases, vs[1].Memory.Aliases)
	}
}

func TestForget_지우면_별칭의_색인과_버전_흔적도_없다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveAliased(t, s, "배포 점검표는 먼저 본다", []string{"유일한별칭어"})
	if _, err := s.Forget(ForgetInput{IDs: []string{m.ID}, Reason: "시험"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Find(FindInput{Query: "유일한별칭어", AllProjects: true}); len(got) != 0 {
		t.Fatalf("a forgotten memory is still found by its alias: %v", idsOf(got))
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM memory_versions WHERE snapshot LIKE '%유일한별칭어%'`,
		`SELECT COUNT(*) FROM memory_fts WHERE memory_fts MATCH '"유일한별칭어"'`,
		`SELECT COUNT(*) FROM memories WHERE aliases LIKE '%유일한별칭어%'`,
	} {
		if n := countOf(t, s, q); n != 0 {
			t.Errorf("%s = %d, want 0", q, n)
		}
	}
}

// 별칭이 든 기억을 늦은 줄(색인 전)로 훑을 때도 같은 별칭으로 맞는다: 색인과 훑기가 같은 글을 본다.
func TestFind_색인이_없어도_늦은_줄_훑기가_별칭을_본다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveAliased(t, s, "배포 점검표는 먼저 본다", []string{"릴리스"})
	if _, err := s.DB().Exec(`DELETE FROM memory_index WHERE memory_id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT OR IGNORE INTO memory_index_late(memory_id) VALUES(?)`, m.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Find(FindInput{Query: "릴리스", AllProjects: true})
	if err != nil || len(got) != 1 {
		t.Fatalf("late scan: %v %v", idsOf(got), err)
	}
}

func TestToolInputScores_별칭도_기억_글로_센다(t *testing.T) {
	ms := []Memory{
		{ID: "a", Title: "백업은 밤에 돈다", Body: "본문", Aliases: []string{"rsync"}},
		{ID: "b", Title: "배포 점검표", Body: "본문"},
	}
	got := ToolInputScores(ms, "rsync -a src/ dst")
	if got["a"] < 0.5 || got["b"] != 0 {
		t.Fatalf("scores = %v", got)
	}
}

func TestCoverage_요청의_낱말이_별칭에만_있어도_맞은_것으로_센다(t *testing.T) {
	m := Memory{Title: "배포 점검표", Body: "본문", Aliases: []string{"릴리스 체크리스트"}}
	cover, matched := Coverage(m, []string{"릴리스", "체크리스트"})
	if cover != 1 || len(matched) != 2 {
		t.Fatalf("cover %v matched %v", cover, matched)
	}
}
