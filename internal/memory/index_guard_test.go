package memory

import (
	"slices"
	"testing"
)

// 개수 비교 보호 장치(ORBIT-25 후속 검토 (b)): 늦은 표시 없이 색인이 깨진 기억(memory_fts 줄이나 memory_index 줄이
// 지워짐)도 찾기가 놓치지 않는다. 찾기는 한 읽기 트랜잭션에서 기억·색인·FTS 줄 수를 견주고, 어긋나면 그 찾기만
// 전체 검사(staleSQL)로 늦은 줄을 훑는다. doctor는 늦은 줄(옛 실행 파일이 쓴 것)과 깨진 색인을 나눠 센다.

// guardStore is an indexed store of 10 memories where an older binary changed m-000004 and added 2 (3 late
// rows, all marked), and the healthy find of query on it.
func guardStore(t *testing.T, query string) (*Store, []Hit) {
	t.Helper()
	s, path := indexedStore(t, 10)
	oldBinaryWrites(t, path, "m-000004", 2)
	want, err := s.FindScored(FindInput{Query: query, AllProjects: true})
	if err != nil || len(want) == 0 {
		t.Fatalf("healthy find %s %v", hitsText(want), err)
	}
	return s, want
}

func TestIndexGuard_표시_없이_memory_index_줄이_지워진_기억도_고치기_전에_찾는다(t *testing.T) {
	for _, c := range []struct{ name, sql string }{
		// FTS 줄은 남는다(색인 줄 없는 FTS 줄).
		{"색인 줄만", `DELETE FROM memory_index WHERE memory_id='m-000007'`},
		{"색인 줄과 FTS 줄", `DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='m-000007');
			DELETE FROM memory_index WHERE memory_id='m-000007'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, want := guardStore(t, "커밋 규칙 7 legacyedit")
			if !slices.ContainsFunc(want, func(h Hit) bool { return h.Memory.ID == "m-000007" }) {
				t.Fatalf("the healthy find lacks m-000007: %s", hitsText(want))
			}
			if _, err := s.DB().Exec(c.sql); err != nil {
				t.Fatal(err)
			}
			if got, err := s.FindScored(FindInput{Query: "커밋 규칙 7 legacyedit", AllProjects: true}); err != nil || !sameHits(got, want) {
				t.Fatalf("find on the broken index %s %v\nwant %s", hitsText(got), err, hitsText(want))
			}
			if _, err := s.Reindex(); err != nil {
				t.Fatal(err)
			}
			if got, err := s.FindScored(FindInput{Query: "커밋 규칙 7 legacyedit", AllProjects: true}); err != nil || !sameHits(got, want) {
				t.Fatalf("find after reindex %s %v\nwant %s", hitsText(got), err, hitsText(want))
			}
		})
	}
}

func TestIndexGuard_개수가_맞으면_통과하고_깨지면_어긋난다(t *testing.T) {
	s, _ := guardStore(t, "커밋 규칙")
	agree := func(t *testing.T) bool {
		t.Helper()
		tx, err := s.DB().Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		total, ok, err := indexCounts(tx)
		if err != nil {
			t.Fatal(err)
		}
		var n int // 저장소의 연결은 하나라 같은 트랜잭션에서 센다
		if err := tx.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n); err != nil || total != n {
			t.Fatalf("total %d, want %d (%v)", total, n, err)
		}
		return ok
	}
	// 옛 실행 파일이 넣어 색인 줄이 없는 기억(표시가 있다)은 어긋남이 아니다: 늦은 줄이 있어도 전체 검사로 가지 않는다.
	if !agree(t) {
		t.Fatal("counts disagree with late rows only")
	}
	for _, c := range []struct{ name, sql string }{
		{"FTS 줄 지움", `DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='m-000001')`},
		{"색인 줄 지움", `DELETE FROM memory_index WHERE memory_id='m-000002'`},
		{"색인 줄과 FTS 줄 지움", `DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='m-000003');
			DELETE FROM memory_index WHERE memory_id='m-000003'`},
		{"기억 없는 색인 줄", `INSERT INTO memory_index(memory_id, indexed_at) VALUES('m-gone','1')`},
		{"색인 줄 없는 FTS 줄", `INSERT INTO memory_fts(rowid, title, keys, body) VALUES(99999, 'x', 'x', 'x')`},
		// 표시가 있는 기억의 FTS 줄이 지워진 것도 어긋남이다(찾기는 맞지만 doctor가 깨짐으로 알린다).
		// (다시 만들기가 표시를 지웠으니 옛 실행 파일처럼 고쳐 다시 표시한다.)
		{"표시가 있는 기억의 FTS 줄 지움", `UPDATE memories SET updated_at=updated_at+1 WHERE id='m-000004';
			DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='m-000004')`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.Reindex(); err != nil {
				t.Fatal(err)
			}
			if !agree(t) {
				t.Fatal("counts disagree after reindex")
			}
			if _, err := s.DB().Exec(c.sql); err != nil {
				t.Fatal(err)
			}
			if agree(t) {
				t.Fatal("counts agree on a broken index")
			}
		})
	}
}

func TestIndexHealth_늦은_줄과_깨진_색인을_나눠_센다(t *testing.T) {
	s, _ := guardStore(t, "커밋 규칙")
	want := func(late, broken int) {
		t.Helper()
		h, err := s.IndexHealth()
		if err != nil || h != (IndexHealth{Late: late, Broken: broken}) {
			t.Fatalf("health %+v %v, want late %d broken %d", h, err, late, broken)
		}
	}
	want(3, 0) // 옛 실행 파일이 쓴 기억 3건: 늦은 줄(검색은 맞다)
	steps := []struct {
		sql    string
		broken int
	}{
		{`DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='m-000001')`, 1},
		// 색인 줄이 없는 기억 하나와 색인 줄 없이 남은 FTS 줄 하나
		{`DELETE FROM memory_index WHERE memory_id='m-000002'`, 3},
		{`INSERT INTO memory_index(memory_id, indexed_at) VALUES('m-gone','1')`, 4},
		// 표시 없이 indexed_at만 어긋남: 개수 비교는 못 잡지만 doctor는 센다
		{`UPDATE memory_index SET indexed_at='1' WHERE memory_id='m-000003'`, 5},
		// 표시가 있는 기억의 FTS 줄: 늦은 줄이기도 하다
		{`DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='m-000004')`, 6},
	}
	for _, st := range steps {
		if _, err := s.DB().Exec(st.sql); err != nil {
			t.Fatal(err)
		}
		want(3, st.broken)
	}
	if _, err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	want(0, 0)
}

func TestIndexGuard_개수_비교가_못_잡는_깨짐도_doctor는_센다(t *testing.T) {
	s, _ := guardStore(t, "커밋 규칙")
	// 기억 하나의 색인 줄과 FTS 줄이 지워지고, 기억 없는 색인 줄과 FTS 줄이 하나씩 생겨 수가 맞는다.
	if _, err := s.DB().Exec(`DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='m-000005');
		DELETE FROM memory_index WHERE memory_id='m-000005';
		INSERT INTO memory_index(memory_id, indexed_at) VALUES('m-gone','1');
		INSERT INTO memory_fts(rowid, title, keys, body) VALUES((SELECT rowid FROM memory_index WHERE memory_id='m-gone'), 'x', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := indexCounts(tx)
	tx.Rollback()
	if err != nil || !ok {
		t.Fatalf("this break is meant to keep the counts equal: %v %v", ok, err)
	}
	if h, err := s.IndexHealth(); err != nil || h.Broken != 2 {
		t.Fatalf("health %+v %v, want broken 2 (a memory without its index row, an index row without its memory)", h, err)
	}
}

// FTS5 공식 문서(fts5.html 9.3)는 FTS 줄마다 같은 rowid의 %_docsize 줄이 있다고 한다. 보호 장치는 그 줄 수를 FTS
// 줄 수로 센다. 이 시험은 묶어 쓰는 SQLite(modernc)가 그대로인지 본다: 올리면 여기서 먼저 깨진다.
func TestIndexGuard_docsize_줄은_FTS_줄과_rowid가_같다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, aiLearns(KindFact, "배포 전에 드라이런을 돌린다", ""))
	mustSave(t, s, aiLearns(KindFact, "커밋 전에 시험을 돌린다", ""))
	body := "고친 본문"
	if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body}); err != nil {
		t.Fatal(err)
	}
	same := func(t *testing.T) {
		t.Helper()
		var fts, docs, both int
		if err := s.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM memory_fts), (SELECT COUNT(*) FROM memory_fts_docsize),
			(SELECT COUNT(*) FROM memory_fts f JOIN memory_fts_docsize d ON d.id = f.rowid)`).Scan(&fts, &docs, &both); err != nil {
			t.Fatal(err)
		}
		if fts != docs || fts != both || fts == 0 {
			t.Fatalf("fts %d docsize %d both %d", fts, docs, both)
		}
	}
	same(t)
	if _, err := s.DB().Exec(`DELETE FROM memory_fts WHERE rowid=(SELECT MIN(rowid) FROM memory_index)`); err != nil {
		t.Fatal(err)
	}
	same(t)
	if _, err := s.DB().Exec(`UPDATE memory_fts SET body='엉뚱 zzz'`); err != nil {
		t.Fatal(err)
	}
	same(t)
	if _, err := s.Reindex(); err != nil {
		t.Fatal(err)
	}
	same(t)
}

func TestFindScored_읽기를_마치면_거르기와_정렬_전에_트랜잭션을_닫는다(t *testing.T) {
	s, _ := indexedStore(t, 20)
	for _, in := range []FindInput{{Query: "커밋 규칙 7", AllProjects: true}, {AllProjects: true}} {
		called := false
		s.rankStep = func() {
			called = true
			if n := s.DB().Stats().InUse; n != 0 {
				t.Errorf("query %q: %d connections in use while ranking (the read transaction is still open)", in.Query, n)
			}
		}
		if got, err := s.FindScored(in); err != nil || len(got) == 0 {
			t.Fatalf("find %d %v", len(got), err)
		}
		if !called {
			t.Fatal("rankStep not called")
		}
	}
}
