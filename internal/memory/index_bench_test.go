package memory

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

// 쓰기 한 건과 찾기 한 건의 값이 기억 수에 따라 어떻게 늘어나는지 잰다(늦은 줄이 없는 저장소).
//
//	go test ./internal/memory -run '^$' -bench 'IndexWrite|IndexFind' -benchmem -count=5
//
// 찾기 질의(benchQuery)는 기억 수와 무관하게 늘 같은 20건(benchTargets)에만 닿는다. 나머지 기억은 질의의
// 글자를 하나도 쓰지 않는 가짜 낱말로 만든다. 그래서 찾기가 후보만 읽으면 값이 기억 수와 거의 무관하다.

var benchSizes = []int{1000, 5000, 20000}

const benchTargets = 20

// benchVocab is a fixed list of pseudo words made of Hangul syllables that share no syllable with benchQuery.
var benchVocab = func() []string {
	skip := map[rune]bool{}
	for _, r := range benchQuery {
		skip[r] = true
	}
	rng := rand.New(rand.NewSource(7))
	syllable := func() rune {
		for {
			if r := rune(0xAC00 + rng.Intn(11172)); !skip[r] {
				return r
			}
		}
	}
	out := make([]string, 3000)
	for i := range out {
		w := make([]rune, 2+rng.Intn(2))
		for k := range w {
			w[k] = syllable()
		}
		out[i] = string(w)
	}
	return out
}()

// sizedStore is a store of n memories, all indexed: benchTargets of them match benchQuery, the rest do not.
func sizedStore(b *testing.B, n int) (*Store, []string) {
	b.Helper()
	s, err := Open(filepath.Join(b.TempDir(), "memory.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	rng := rand.New(rand.NewSource(int64(n)))
	words := func(k int) string {
		ws := make([]string, k)
		for i := range ws {
			ws[i] = benchVocab[rng.Intn(len(benchVocab))]
		}
		return strings.Join(ws, " ")
	}
	tx, err := s.DB().Begin()
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]string, n)
	for i := range n {
		ti, body := fmt.Sprintf("%s %d", words(5), i), words(40)
		if i%(n/benchTargets) == 0 { // 질의에 닿는 기억: 기억 수와 무관하게 benchTargets건
			ti, body = fmt.Sprintf("배포 스크립트 드라이런 %s %d", words(2), i), "deploy 전에 확인한다 "+words(30)
		}
		ids[i] = NewID("m-")
		if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at)
			VALUES(?,?,?,'',?,?,?,?,'["bench"]',1,?,?)`, ids[i], KindFact, ScopeGlobal, StateConfirmed, ti, TitleKey(ti), body,
			1700000000000+i, 1700000000000+i); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := s.FillIndex(0); err != nil {
		b.Fatal(err)
	}
	if n, err := s.StaleIndexCount(); err != nil || n != 0 {
		b.Fatalf("stale %d %v", n, err)
	}
	return s, ids
}

// BenchmarkIndexWrite is one write (an update of one memory's body) in a store with no late rows.
func BenchmarkIndexWrite(b *testing.B) {
	for _, n := range benchSizes {
		s, ids := sizedStore(b, n)
		b.Run(fmt.Sprintf("기억_%d건", n), func(b *testing.B) {
			for i := range b.N {
				body := fmt.Sprintf("고친 본문 %d %s", i, benchVocab[i%len(benchVocab)])
				if _, err := s.Update(UpdateInput{ID: ids[(i*7919)%len(ids)], Body: &body}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkIndexFind is one find with a query that reaches benchTargets memories.
func BenchmarkIndexFind(b *testing.B) {
	for _, n := range benchSizes {
		s, _ := sizedStore(b, n)
		if got, err := s.Find(FindInput{Query: benchQuery}); err != nil || len(got) != benchTargets {
			b.Fatalf("find %d %v", len(got), err)
		}
		b.Run(fmt.Sprintf("기억_%d건", n), func(b *testing.B) {
			for range b.N {
				if _, err := s.Find(FindInput{Query: benchQuery}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkIndexCounts is the guard's row counts (indexCountsSQL, index.go) beside the count of memories
// alone, which is what a find ran before the guard: the difference is what the guard adds to every find.
func BenchmarkIndexCounts(b *testing.B) {
	for _, n := range benchSizes {
		s, _ := sizedStore(b, n)
		b.Run(fmt.Sprintf("기억_%d건/기억_수만", n), func(b *testing.B) {
			var total int
			for range b.N {
				if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&total); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("기억_%d건/보호_장치", n), func(b *testing.B) {
			for range b.N {
				if _, ok, err := indexCounts(s.DB()); err != nil || !ok {
					b.Fatalf("%v %v", ok, err)
				}
			}
		})
	}
}

// BenchmarkFindOnBrokenIndex is a find while the row counts disagree (one memory lost its FTS row without a
// mark): it scans the late rows of the full check, so it grows with the memories until --reindex.
func BenchmarkFindOnBrokenIndex(b *testing.B) {
	for _, n := range benchSizes {
		s, ids := sizedStore(b, n)
		if _, err := s.DB().Exec(`DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id=?)`, ids[n/2]); err != nil {
			b.Fatal(err)
		}
		if got, err := s.Find(FindInput{Query: benchQuery}); err != nil || len(got) != benchTargets {
			b.Fatalf("find %d %v", len(got), err)
		}
		b.Run(fmt.Sprintf("기억_%d건", n), func(b *testing.B) {
			for range b.N {
				if _, err := s.Find(FindInput{Query: benchQuery}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
