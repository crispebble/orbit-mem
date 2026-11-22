package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
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
	if err := MarkThrowawayCopy(path); err != nil {
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

// Rewind는 기억·출처·연결을 지우고 다시 넣는다: 임시 사본 표지(MarkThrowawayCopy)가 없는 DB(사용자의 기억 DB)는
// 열지 않고 거절하고 아무 줄도 바꾸지 않는다(1-6 b6d 2차, 재검토 지적).
func TestRewind_임시_사본_표지가_없는_DB는_거절하고_아무것도_바꾸지_않는다(t *testing.T) {
	path, _, _, at := historyFixture(t)
	want := dump(t, path)
	h, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := h.Rewind(path); err == nil {
		r.To(at[0] - 1)
		r.Close()
		t.Fatal("Rewind opened a DB without the throwaway copy mark")
	} else if !errors.Is(err, ErrNotThrowawayCopy) {
		t.Fatalf("err = %v, want ErrNotThrowawayCopy", err)
	}
	got := dump(t, path)
	for table := range historyTables {
		if !slices.Equal(got[table], want[table]) {
			t.Fatalf("%s changed by a refused Rewind", table)
		}
	}
	if err := MarkThrowawayCopy(path); err != nil {
		t.Fatal(err)
	}
	r, err := h.Rewind(path)
	if err != nil {
		t.Fatalf("marked copy: %v", err)
	}
	r.Close()
	if err := MarkThrowawayCopy(filepath.Join(t.TempDir(), "none.db")); err == nil {
		t.Fatal("marked a DB that does not exist")
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

// StateAt은 기록된 주입이 그 시각에 낡은 기억이었는지 가리는 감사(bench.AuditInjections)가 쓴다: 그 시각의 버전과 상태를
// 돌려주고, 그 시각에 없던 기억은 false다.
func TestHistory_StateAt은_그_시각의_버전과_상태를_돌려준다(t *testing.T) {
	path, a, b, at := historyFixture(t)
	h, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := h.StateAt(a.ID, at[0]); !ok || m.Title != "배포 창 규칙 첫 판" || m.State == StateStale || m.Version != 1 {
		t.Fatalf("a at t1: %+v %v", m, ok)
	}
	if m, ok := h.StateAt(a.ID, at[2]); !ok || m.Title != "배포 창 규칙 고친 판" || m.State == StateStale || m.Topic != "배포 창" {
		t.Fatalf("a at t3: %+v %v", m, ok)
	}
	if m, ok := h.StateAt(a.ID, at[3]); !ok || m.State != StateStale || m.StaleReason != "낡음" || m.ID != a.ID {
		t.Fatalf("a at t4: %+v %v", m, ok)
	}
	if _, ok := h.StateAt(b.ID, at[1]); ok {
		t.Fatal("b is not there before t3")
	}
	if m, ok := h.StateAt(b.ID, at[3]); !ok || m.Title != "롤백 순서 기록" || m.Tags == nil {
		t.Fatalf("b at t4: %+v %v", m, ok)
	}
	if _, ok := h.StateAt("m-none", at[3]); ok {
		t.Fatal("a memory the DB never had")
	}
}

// 재생 사본의 세션 상태(세션 묶음·세션 표시·위임 글)는 실제 세션이 남긴 것이라 재생 세션과 섞이면 안 된다. 비우기는
// 임시 사본 표지가 있는 DB만 하고, 기억 줄은 건드리지 않는다.
func TestHistory_ClearSessionState는_임시_사본만_세션_표를_비운다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveDoc(t, s, "배포 창 규칙", "금요일 배포 금지", "")
	if err := s.SaveSessionBundle("h", "s1", "묶음", []string{m.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSession("h", "s1", MarkShown, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDelegation("h", "s1", "worker", "일"); err != nil {
		t.Fatal(err)
	}
	path := s.Path()
	count := func() (bundles, marks, mems int) {
		db, err := sqlitedb.OpenWith(path, sqlitedb.Options{Existing: true})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		db.QueryRow(`SELECT COUNT(*) FROM session_bundles`).Scan(&bundles)
		db.QueryRow(`SELECT COUNT(*) FROM session_marks`).Scan(&marks)
		db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&mems)
		return
	}
	if err := ClearSessionState(path); !errors.Is(err, ErrNotThrowawayCopy) {
		t.Fatalf("a DB without the mark: %v", err)
	}
	if b, k, _ := count(); b != 2 || k != 1 {
		t.Fatalf("refused clear changed rows: bundles %d marks %d", b, k)
	}
	if err := MarkThrowawayCopy(path); err != nil {
		t.Fatal(err)
	}
	if err := ClearSessionState(path); err != nil {
		t.Fatal(err)
	}
	if b, k, n := count(); b != 0 || k != 0 || n != 1 {
		t.Fatalf("after clear: bundles %d marks %d memories %d", b, k, n)
	}
}
