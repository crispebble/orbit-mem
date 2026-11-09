package memory

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// 1-6 b6r 2차: 재생 사례는 나중에 뜬 DB 사본 위에서 돈다. Rewinder는 사본을 사례 시각의 상태로 맞춘다: 그 뒤에 만든
// 기억·출처·연결·사건은 없고, 그 뒤의 고침·낡음은 그 전 판이다. 다시 지금으로 맞추면 원래 사본과 같다.

// historyFixture saves, an hour apart: a (t1), a edited (t2), b with a link a–b and an event (t3), a stale (t4).
func historyFixture(t *testing.T) (path string, a, b Memory, at []int64) {
	t.Helper()
	s, c := newTestStore(t)
	tick := func() int64 { c.t = c.t.Add(time.Hour); return c.t.UnixMilli() }
	at = append(at, tick())
	a = saveDoc(t, s, "배포 창 규칙 첫 판", "금요일 배포 금지", "배포 창")
	at = append(at, tick())
	title := "배포 창 규칙 고친 판"
	if _, err := s.Update(UpdateInput{ID: a.ID, Title: &title, Reason: "고침"}); err != nil {
		t.Fatal(err)
	}
	at = append(at, tick())
	b = saveDoc(t, s, "롤백 순서 기록", "롤백은 이전 태그로", "롤백")
	if _, _, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginAI}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEvent(Event{Kind: EventMemoryUsed, Day: Day(c.t)}, map[string]any{"memoryId": b.ID}); err != nil {
		t.Fatal(err)
	}
	at = append(at, tick())
	if _, err := s.SetState(a.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	return s.Path(), a, b, at
}

// dump is every row of the tables the history keeps, as text, sorted.
func dump(t *testing.T, path string) map[string][]string {
	t.Helper()
	db, err := sqlitedb.OpenWith(path, sqlitedb.Options{Existing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string][]string{}
	for table := range historyTables {
		_, rows, err := readTable(db, table, "1")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			out[table] = append(out[table], fmt.Sprint(r...))
		}
		slices.Sort(out[table])
	}
	return out
}

func rewound(t *testing.T, path string) (*History, *Rewinder) {
	t.Helper()
	h, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.Rewind(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return h, r
}

func readAt(t *testing.T, path string) *Store {
	t.Helper()
	s, _, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRewind_사례_시각의_기억만_그_시각의_판으로_있다(t *testing.T) {
	path, a, b, at := historyFixture(t)
	h, r := rewound(t, path)
	if !h.Existed(a.ID, at[0]) || h.Existed(b.ID, at[1]) || !h.Existed(b.ID, at[2]) {
		t.Fatal("Existed")
	}

	// t2와 t3 사이: a는 고친 판이고 아직 낡지 않았다. b·연결·사건은 아직 없다. 찾기 색인도 그 상태다.
	if err := r.To(at[1] + 1); err != nil {
		t.Fatal(err)
	}
	s := readAt(t, path)
	got, err := s.Get(a.ID)
	if err != nil || got.Title != "배포 창 규칙 고친 판" || got.State == StateStale || got.Version != 2 {
		t.Fatalf("a at t2: %+v %v", got, err)
	}
	if _, err := s.Get(b.ID); err == nil {
		t.Fatal("b must not be there before t3")
	}
	if found, _ := s.Find(FindInput{Query: "롤백 순서", AllProjects: true}); len(found) != 0 {
		t.Fatalf("index still finds b: %v", found)
	}
	if found, _ := s.Find(FindInput{Query: "고친 판", AllProjects: true}); len(found) != 1 || found[0].ID != a.ID {
		t.Fatalf("index does not find a's title of t2: %v", found)
	}
	d := dump(t, path)
	if len(d["memory_links"]) != 0 || len(d["events"]) != 0 || len(d["memory_versions"]) != 2 {
		t.Fatalf("rows at t2: links %d events %d versions %d", len(d["memory_links"]), len(d["events"]), len(d["memory_versions"]))
	}

	// t1 바로 뒤: 첫 판 제목.
	if err := r.To(at[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := readAt(t, path).Get(a.ID); got.Title != "배포 창 규칙 첫 판" {
		t.Fatalf("a at t1: %q", got.Title)
	}
	// 모든 기억보다 앞: 기억이 없다.
	if err := r.To(at[0] - 1); err != nil {
		t.Fatal(err)
	}
	if d := dump(t, path); len(d["memories"])+len(d["sources"])+len(d["memory_versions"]) != 0 {
		t.Fatalf("rows before everything: %v", d)
	}
}

func TestRewind_앞뒤로_오가도_지금으로_맞추면_원래_사본과_같다(t *testing.T) {
	path, _, _, at := historyFixture(t)
	want := dump(t, path)
	_, r := rewound(t, path)
	for _, step := range []int64{at[3] + 1, at[0] - 1, at[2] + 1, at[0], at[1] + 1, at[3] + 1} {
		if err := r.To(step); err != nil {
			t.Fatal(err)
		}
	}
	got := dump(t, path)
	for table := range historyTables {
		if !slices.Equal(got[table], want[table]) {
			t.Fatalf("%s differs after going back and forth:\n got %v\nwant %v", table, got[table], want[table])
		}
	}
	// 실행이 쓴 사건(틀에 없던 줄)은 건드리지 않는다.
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO events(at,day,kind) VALUES(?,?,?)`, at[3]+10, "x", "bench.run"); err != nil {
		t.Fatal(err)
	}
	if err := r.To(at[0]); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='bench.run'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("run's own event: %d %v", n, err)
	}
}
