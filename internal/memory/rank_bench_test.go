package memory

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 찾기 비용 어림: 기억 3,000건에서 색인 찾기와 옛 훑기(전부 읽어 부분 문자열 점수)를 견준다.
//
//	go test ./internal/memory/ -run '^$' -bench 'BenchmarkFind' -benchtime 50x

var benchWords = strings.Fields(`배포 스크립트 드라이런 확인 절차 커밋 제목 영어 명령형 문서 위치 공간 기억 저장 설계 규칙 결정
	시험 모의 데이터 화면 밀도 버튼 백업 주기 말투 존댓말 포트 어댑터 도메인 바깥 색인 검색 순위 점수 정규화 질의 토큰
	deploy smoke test commit review index search ranking schema migrate backup restore doctor`)

func benchStore(b *testing.B, n int) *Store {
	b.Helper()
	s, err := Open(filepath.Join(b.TempDir(), "memory.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	rng := rand.New(rand.NewSource(1))
	text := func(k int) string {
		ws := make([]string, k)
		for i := range ws {
			ws[i] = benchWords[rng.Intn(len(benchWords))]
		}
		return strings.Join(ws, " ")
	}
	tx, err := s.DB().Begin()
	if err != nil {
		b.Fatal(err)
	}
	for i := range n {
		ti := fmt.Sprintf("%s %d", text(5), i)
		if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at)
			VALUES(?,?,?,'',?,?,?,?,'["bench"]',1,?,?)`, NewID("m-"), KindFact, ScopeGlobal, StateConfirmed, ti, TitleKey(ti), text(40),
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
	return s
}

const benchQuery = "배포 스크립트는 드라이런으로 확인한다 deploy"

func BenchmarkFind_색인_3000건(b *testing.B) {
	s := benchStore(b, 3000)
	b.ResetTimer()
	for range b.N {
		if _, err := s.Find(FindInput{Query: benchQuery}); err != nil {
			b.Fatal(err)
		}
	}
}

// 옛 Find와 같은 일: 상태로 전부 읽고 Relevance(부분 문자열)로 점수를 매겨 정렬한다.
func BenchmarkFind_옛_훑기_3000건(b *testing.B) {
	s := benchStore(b, 3000)
	b.ResetTimer()
	for range b.N {
		all, err := s.list([]string{StateCandidate, StateConfirmed})
		if err != nil {
			b.Fatal(err)
		}
		var hits []Rank
		for _, m := range all {
			if r := Relevance(m.Doc(), benchQuery); r.Score > 0 {
				hits = append(hits, r)
			}
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	}
}

// 쓰기 끝(syncIndexTx)이 매번 도는 늦은 줄 훑기(staleSQL)의 값이다.
func BenchmarkStaleIndexCount_3000건(b *testing.B) {
	s := benchStore(b, 3000)
	b.ResetTimer()
	for range b.N {
		if n, err := s.StaleIndexCount(); err != nil || n != 0 {
			b.Fatal(n, err)
		}
	}
}
