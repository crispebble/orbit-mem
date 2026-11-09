package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// 늦은 줄 표시와 한 시점 찾기(ORBIT-25 후속 6·9a): 쓰기 끝과 찾기는 기억 전부를 훑지 않고, 옛 실행 파일이 memories만
// 고친 기억도 늦은 줄로 잡으며, 찾기 하나는 한 읽기 트랜잭션(한 시점)에서 읽는다.

// recordingQuerier records every statement run through it.
type recordingQuerier struct {
	execQuerier
	stmts []recordedStmt
}

type recordedStmt struct {
	query string
	args  []any
}

func (r *recordingQuerier) Query(q string, args ...any) (*sql.Rows, error) {
	r.stmts = append(r.stmts, recordedStmt{q, args})
	return r.execQuerier.Query(q, args...)
}

func (r *recordingQuerier) QueryRow(q string, args ...any) *sql.Row {
	r.stmts = append(r.stmts, recordedStmt{q, args})
	return r.execQuerier.QueryRow(q, args...)
}

func (r *recordingQuerier) Exec(q string, args ...any) (sql.Result, error) {
	r.stmts = append(r.stmts, recordedStmt{q, args})
	return r.execQuerier.Exec(q, args...)
}

// fullScans lists the query plan lines of stmts that read a whole table whose size grows with the memories:
// memories, memory_index, or memory_fts without MATCH or rowid. Reading the late marks (memory_index_late)
// is allowed: it grows with the late rows only.
func fullScans(t *testing.T, q querier, stmts []recordedStmt) []string {
	t.Helper()
	var out []string
	for _, st := range stmts {
		rows, err := q.Query(`EXPLAIN QUERY PLAN `+st.query, st.args...)
		if err != nil {
			t.Fatalf("explain %q: %v", st.query, err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if isFullScan(detail) {
				out = append(out, detail+"  ⟵  "+strings.Join(strings.Fields(st.query), " "))
			}
		}
		rows.Close()
	}
	return out
}

func isFullScan(detail string) bool {
	if !strings.HasPrefix(detail, "SCAN ") {
		return false
	}
	f := strings.Fields(detail)
	if len(f) < 2 {
		return false
	}
	switch name := f[1]; {
	case name == "memory_index_late" || name == "l" || name == "json_each": // 늦은 표시, 찾기가 넘긴 후보 id 목록
		return false
	case strings.Contains(detail, "VIRTUAL TABLE INDEX"):
		// FTS5: 계획 글(idxStr)에 MATCH(M) 또는 rowid 조건(=)이 있으면 전체 읽기가 아니다.
		_, plan, _ := strings.Cut(detail, "VIRTUAL TABLE INDEX ")
		_, idx, _ := strings.Cut(plan, ":")
		return !strings.ContainsAny(idx, "M=")
	case strings.Contains(detail, "CONSTANT ROW"):
		return false
	}
	return true
}

// indexedStore is a store of n memories made with SQL and indexed (no late rows).
func indexedStore(t *testing.T, n int) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		ti := fmt.Sprintf("일반 기억 %d 커밋 규칙", i)
		if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at)
			VALUES(?,?,?,'',?,?,?,?,'["t"]',1,?,?)`, fmt.Sprintf("m-%06d", i), KindFact, ScopeGlobal, StateConfirmed, ti, TitleKey(ti),
			"본문 "+ti, 1700000000000+i, 1700000000000+i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FillIndex(0); err != nil {
		t.Fatal(err)
	}
	if n := staleCount(t, s); n != 0 {
		t.Fatalf("stale %d", n)
	}
	return s, path
}

func TestIndexLate_늦은_줄이_없으면_쓰기_끝은_어느_표도_통째로_읽지_않는다(t *testing.T) {
	for _, n := range []int{10, 400} {
		s, _ := indexedStore(t, n)
		tx, err := s.DB().Begin()
		if err != nil {
			t.Fatal(err)
		}
		id := "m-000003"
		if _, err := tx.Exec(`UPDATE memories SET body='고친 본문', updated_at=updated_at+1 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		rec := &recordingQuerier{execQuerier: tx}
		if err := syncIndexTx(rec, id); err != nil {
			t.Fatal(err)
		}
		if len(rec.stmts) == 0 {
			t.Fatal("no statements recorded")
		}
		if scans := fullScans(t, tx, rec.stmts); len(scans) > 0 {
			t.Fatalf("%d memories: the end of a write reads whole tables:\n%s", n, strings.Join(scans, "\n"))
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		wantIndexed(t, s, id)
		if n := staleCount(t, s); n != 0 {
			t.Fatalf("stale %d", n)
		}
	}
}

// A find's one whole read is indexCountsSQL: the count of memories (the IDF's N) and the index row counts
// of the guard (index.go). COUNT(*) walks b-tree pages without decoding rows (BenchmarkIndexFind).

func TestFindScored_질의가_있는_찾기는_기억_수에_따라_커지는_표를_통째로_읽지_않는다(t *testing.T) {
	for _, n := range []int{10, 400} {
		s, path := indexedStore(t, n)
		oldBinaryWrites(t, path, "m-000004", 2) // 늦은 표시가 있는 기억도 섞는다
		tx, err := s.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		rec := &recordingQuerier{execQuerier: tx}
		got, err := s.readFind(rec, FindInput{Query: "커밋 규칙 7 legacyedit", AllProjects: true})
		if err != nil || len(got.all) == 0 {
			t.Fatalf("find %d %v", len(got.all), err)
		}
		var stmts []recordedStmt
		for _, st := range rec.stmts {
			if st.query != indexCountsSQL {
				stmts = append(stmts, st)
			}
		}
		if len(stmts) != len(rec.stmts)-1 {
			t.Fatalf("the row counts ran %d times", len(rec.stmts)-len(stmts))
		}
		if scans := fullScans(t, tx, stmts); len(scans) > 0 {
			t.Fatalf("%d memories: a find reads whole tables:\n%s", n, strings.Join(scans, "\n"))
		}
		tx.Rollback()
	}
}

func TestIndexLate_옛_실행_파일이_memories만_고쳐도_늦은_줄로_잡혀_찾기와_다음_쓰기에_반영된다(t *testing.T) {
	s, path := indexedStore(t, 20)
	// 저장소를 연 채로 옛 실행 파일이 같은 DB에 쓴다: 자기 판의 DDL을 돌리고 memories만 고치고 넣는다.
	db := rawDB(t, path)
	if _, err := db.Exec(schema + eventsSchema + linksSchema + labSchema + activitySchema + auditSchema); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE memories SET body='옛 판이 고친 본문 legacyedit', version=version+1, updated_at=updated_at+1 WHERE id='m-000001'`,
		// updated_at을 올리지 않고 글만 바꾼 쓰기도 늦은 줄이다.
		`UPDATE memories SET title='옛 판이 바꾼 제목 legacyedit' WHERE id='m-000002'`,
		// 충돌 처리 절이 붙은 쓰기를 같은 기억에 두 번 해도 실패하지 않는다(표시는 하나).
		`UPDATE OR ABORT memories SET body='옛 판 고침 legacyedit 하나', updated_at=updated_at+1 WHERE id='m-000003'`,
		`UPDATE OR ROLLBACK memories SET body='옛 판 고침 legacyedit 둘', updated_at=updated_at+1 WHERE id='m-000003'`,
		`INSERT OR ABORT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at)
			VALUES('m-old-1','fact','global','','candidate','옛 판 새 기억','옛 판 새 기억','본문 legacyedit','["old"]',1,1800000000000,1800000000000)`,
		`PRAGMA user_version=4`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()
	want := []string{"m-000001", "m-000002", "m-000003", "m-old-1"}
	ids := func() []string {
		t.Helper()
		got, err := s.Find(FindInput{Query: "legacyedit", AllProjects: true})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range got {
			out = append(out, m.ID)
		}
		slices.Sort(out)
		return out
	}
	if got := ids(); !slices.Equal(got, want) {
		t.Fatalf("find before the next write %v, want %v", got, want)
	}
	if n := staleCount(t, s); n != len(want) {
		t.Fatalf("stale %d, want %d", n, len(want))
	}
	saveFact(t, s, "다음 쓰기") // 쓰기 끝이 늦은 줄을 채운다
	if n := staleCount(t, s); n != 0 {
		t.Fatalf("stale after a write %d", n)
	}
	for _, id := range want {
		wantIndexed(t, s, id)
	}
	if got := ids(); !slices.Equal(got, want) {
		t.Fatalf("find after the write %v, want %v", got, want)
	}
}

func TestFindScored_질의가_있으면_점수가_붙은_후보만_읽는다(t *testing.T) {
	s, _ := indexedStore(t, 30)
	// 다른 글자의 기억 하나를 읽으면 실패하게 만든다: version 칸에 정수가 아닌 글(scanMemory가 읽지 못한다).
	if _, err := s.DB().Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at)
		VALUES('m-poison','fact','global','','confirmed','상관없는 독 기억','상관없는 독 기억','zzz','[]','poison',1700000009999,1700000009999)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FillIndex(0); err != nil { // 늦은 줄이 아니게 색인해 둔다
		t.Fatal(err)
	}
	if _, err := s.Find(FindInput{AllProjects: true}); err == nil {
		t.Fatal("an empty query lists every memory and should hit the poison row") // 독 줄이 실제로 막는지 확인
	}
	got, err := s.FindScored(FindInput{Query: "커밋 규칙 7", AllProjects: true})
	if err != nil {
		t.Fatalf("a find with a query read a memory it does not score: %v", err)
	}
	if len(got) == 0 || !strings.Contains(got[0].Memory.Title, "7") {
		t.Fatalf("find %+v", got)
	}
}

func TestFindScored_읽는_도중_다른_연결이_써도_한_시점의_결과다(t *testing.T) {
	opens := append([]readOpen{{"Open", func(p string) (*Store, SchemaInfo, error) {
		s, err := Open(p)
		return s, SchemaInfo{State: SchemaOK}, err
	}}}, readOpens...)
	for _, o := range opens {
		t.Run(o.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "memory.db")
			w, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			for i := range 4 {
				saveFact(t, w, fmt.Sprintf("배포 전에 드라이런 %d", i))
				saveFact(t, w, fmt.Sprintf("상관없는 말투 기억 %d", i))
			}
			// 질의의 「스크립트」는 아무 기억에도 없어 점수가 1보다 작고, 기억 수(IDF)에 따라 바뀐다.
			in := FindInput{Query: "배포 드라이런 스크립트", AllProjects: true}
			before, err := w.FindScored(in)
			if err != nil || len(before) != 4 {
				t.Fatalf("before %+v %v", before, err)
			}
			r, info, err := o.open(path)
			if err != nil || info.State != SchemaOK {
				t.Fatalf("open %+v %v", info, err)
			}
			defer r.Close()
			var once sync.Once
			r.findStep = func() {
				once.Do(func() { // 찾기의 첫 읽기 뒤에 다른 연결이 쓰고 커밋한다
					saveFact(t, w, "배포 드라이런 새 기억")
					for i := range 6 {
						saveFact(t, w, fmt.Sprintf("새 상관없는 기억 %d", i))
					}
					body := "이제 질의와 무관한 본문"
					title := "바뀐 제목"
					if _, err := w.Update(UpdateInput{ID: before[0].Memory.ID, Title: &title, Body: &body}); err != nil {
						t.Error(err)
					}
				})
			}
			got, err := r.FindScored(in)
			if err != nil {
				t.Fatal(err)
			}
			if !sameHits(got, before) {
				t.Fatalf("a find mixed two moments:\n got %s\nwant %s", hitsText(got), hitsText(before))
			}
			after, err := r.FindScored(in)
			if err != nil {
				t.Fatal(err)
			}
			if sameHits(after, before) {
				t.Fatalf("the write is not seen by the next find: %s", hitsText(after))
			}
		})
	}
}

func sameHits(a, b []Hit) bool {
	return slices.EqualFunc(a, b, func(x, y Hit) bool {
		return x.Memory.ID == y.Memory.ID && x.Score == y.Score && x.Memory.Title == y.Memory.Title &&
			x.Memory.Body == y.Memory.Body && x.Memory.Version == y.Memory.Version
	})
}

func hitsText(hs []Hit) string {
	var parts []string
	for _, h := range hs {
		parts = append(parts, fmt.Sprintf("%s(%s v%d %.9f)", h.Memory.ID, h.Memory.Title, h.Memory.Version, h.Score))
	}
	return strings.Join(parts, ", ")
}

func TestFindScored_읽기_열기와_읽기_전용에서도_늦은_줄까지_같은_결과이고_채우지_않는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	saveFact(t, w, "옛 실행 파일 이전 기억")
	added := oldBinaryWrites(t, path, "", 3)
	in := FindInput{Query: "옛 실행 파일 기억", AllProjects: true}
	want, err := w.FindScored(in)
	if err != nil || len(want) != 4 {
		t.Fatalf("writer find %s %v", hitsText(want), err)
	}
	for _, o := range readOpens {
		r, info, err := o.open(path)
		if err != nil || info.State != SchemaOK {
			t.Fatalf("%s: %+v %v", o.name, info, err)
		}
		got, err := r.FindScored(in)
		if err != nil || !sameHits(got, want) {
			t.Fatalf("%s find %s %v, want %s", o.name, hitsText(got), err, hitsText(want))
		}
		if n := staleCount(t, r); n != len(added) {
			t.Fatalf("%s stale %d", o.name, n)
		}
		r.Close()
	}
}

// dropLate removes the late marks (table and triggers) as a schema 5 DB made before them has none.
func dropLate(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}) {
	t.Helper()
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS memory_index_late_insert; DROP TRIGGER IF EXISTS memory_index_late_update;
		DROP TABLE IF EXISTS memory_index_late`); err != nil {
		t.Fatal(err)
	}
}

func TestIndexLate_늦은_표시가_없는_판5_DB는_쓰기_열기가_사본을_남기고_표시를_채운다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, s, "그대로 둘 기억")
	saveFact(t, s, "고칠 기억")
	ms, _ := s.List()
	var changed string
	for _, m := range ms {
		if m.Title == "고칠 기억" {
			changed = m.ID
		}
	}
	s.Close()
	db := rawDB(t, path)
	dropLate(t, db)
	db.Close()
	// 표시가 없던 때 옛 실행 파일이 쓴 기억: 표시 없이 늦다.
	added := oldBinaryWrites(t, path, changed, 1)
	r, info, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != SchemaOlder {
		t.Fatalf("a schema 5 DB without the late marks is %+v, want older", info)
	}
	r.Close()
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if info, _ := w.Schema(); info.State != SchemaOK {
		t.Fatalf("after the write open %+v", info)
	}
	if copies, _ := filepath.Glob(filepath.Join(home, "backups", "*")); len(copies) == 0 {
		t.Fatal("no copy before the schema change")
	}
	var marks int
	if err := w.DB().QueryRow(`SELECT COUNT(*) FROM memory_index_late`).Scan(&marks); err != nil || marks != 2 {
		t.Fatalf("late marks %d %v, want 2", marks, err)
	}
	if n := staleCount(t, w); n != 2 {
		t.Fatalf("stale %d", n)
	}
	if got, err := w.Find(FindInput{Query: "legacyedit 실행", AllProjects: true}); err != nil || len(got) != 2 {
		t.Fatalf("find %v %v", titlesOf(got), err)
	}
	saveFact(t, w, "다음 쓰기")
	if n := staleCount(t, w); n != 0 {
		t.Fatalf("stale after a write %d", n)
	}
	wantIndexed(t, w, changed)
	wantIndexed(t, w, added[0])
}

func TestIndexLate_열려_있던_저장소의_DB에서_표시가_사라져도_쓰기와_찾기가_되고_다음_열기가_맞춘다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	saveFact(t, a, "앞선 기억")
	db := rawDB(t, path) // 표시가 없는 판 5 사본으로 복구된 것과 같다
	dropLate(t, db)
	db.Close()
	m, err := a.Save(SaveInput{Kind: KindFact, Title: "표시 없는 동안 저장 드라이런", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}})
	if err != nil {
		t.Fatalf("save without the late marks: %v", err)
	}
	body := "표시 없는 동안 고친 본문"
	if _, err := a.Update(UpdateInput{ID: m.ID, Body: &body}); err != nil {
		t.Fatalf("update without the late marks: %v", err)
	}
	if got, err := a.Find(FindInput{Query: "드라이런", AllProjects: true}); err != nil || len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("find without the late marks %v %v", titlesOf(got), err)
	}
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	saveFact(t, w, "다시 연 뒤 쓰기")
	if n := staleCount(t, w); n != 0 {
		t.Fatalf("stale after reopen and a write %d", n)
	}
	wantIndexed(t, w, m.ID)
}

func TestIndexLate_색인_맞추기가_실패한_쓰기는_기억도_표시도_남기지_않는다(t *testing.T) {
	s, _ := indexedStore(t, 5)
	before := mustGet(t, s, "m-000002")
	if _, err := s.DB().Exec(`CREATE TRIGGER index_fail BEFORE UPDATE ON memory_index BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	body := "실패할 고침"
	if _, err := s.Update(UpdateInput{ID: before.ID, Body: &body}); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("update err %v, want the injected failure", err)
	}
	if got := mustGet(t, s, before.ID); got.Body != before.Body || got.Version != before.Version || got.UpdatedAt != before.UpdatedAt {
		t.Fatalf("the failed update left %+v", got)
	}
	var marks int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_index_late`).Scan(&marks); err != nil || marks != 0 {
		t.Fatalf("late marks after the failed write %d %v", marks, err)
	}
	if _, err := s.DB().Exec(`DROP TRIGGER index_fail`); err != nil {
		t.Fatal(err)
	}
	if n := staleCount(t, s); n != 0 {
		t.Fatalf("stale %d", n)
	}
	wantIndexed(t, s, before.ID)
}
