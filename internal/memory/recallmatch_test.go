package memory

import (
	"math"
	"slices"
	"testing"
)

// 요청 회상의 두 문턱 재료(설계 · 지연 회상 5.1 4단계): 점수는 찾기와 같은 점수이고, 맞은 비율은 뜻 있는 낱말 중
// 기억에 나온 낱말의 비율이다.

func TestMeaningfulWords_불용어와_한_글자를_뺀다(t *testing.T) {
	got := MeaningfulWords("이거 pnpm으로 설치해 줘 please 7")
	if want := []string{"pnpm으로", "설치해"}; !slices.Equal(got, want) {
		t.Fatalf("뜻 있는 낱말 %v, want %v", got, want)
	}
}

func TestCoverage_맞은_낱말의_비율이고_한글은_앞부분이_맞아도_센다(t *testing.T) {
	m := Memory{Title: "이 앱은 pnpm으로 설치한다", Body: "lock 파일은 하나만 둔다", Topic: "패키지 관리자", Tags: []string{"node"}}
	cover, matched := Coverage(m, []string{"pnpm으로", "설치해", "패키지", "node", "배포"})
	if want := []string{"pnpm으로", "설치해", "패키지", "node"}; !slices.Equal(matched, want) {
		t.Fatalf("맞은 낱말 %v, want %v", matched, want)
	}
	if cover != 0.8 {
		t.Fatalf("맞은 비율 %v, want 0.8", cover)
	}
	if c, _ := Coverage(m, nil); c != 0 {
		t.Fatalf("낱말이 없으면 0이어야 함: %v", c)
	}
}

func TestRecallHits_점수는_뜻_있는_낱말로_찾은_찾기_점수와_같다(t *testing.T) {
	s, _ := newTestStore(t)
	a := saveDoc(t, s, "이 앱은 pnpm으로 설치한다", "npm install을 쓰지 않는다", "패키지 관리자")
	b := saveDoc(t, s, "운영 배포는 main 병합으로 일어난다", "감시기가 main을 본다", "")
	for i := 0; i < 6; i++ {
		filler(t, s, i)
	}
	prompt := "이거 pnpm으로 설치해 줘"
	hits, err := s.RecallHits(prompt)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RecallHit{}
	for _, h := range hits {
		byID[h.Memory.ID] = h
	}
	ha, ok := byID[a.ID]
	if !ok {
		t.Fatalf("pnpm 기억이 없습니다: %+v", hits)
	}
	if q := RecallQuery(prompt + " 7 v"); q != "pnpm으로 설치해 7 v" {
		t.Fatalf("찾기 질의 %q", q)
	}
	if want := scoreOf(t, s, RecallQuery(prompt), a.ID); math.Abs(ha.Score-want) > 1e-12 {
		t.Fatalf("회상 점수 %v, 같은 낱말의 찾기 점수 %v", ha.Score, want)
	}
	if ha.Coverage != 1 || !slices.Equal(ha.Matched, []string{"pnpm으로", "설치해"}) {
		t.Fatalf("맞은 비율 %v 낱말 %v", ha.Coverage, ha.Matched)
	}
	if hb, ok := byID[b.ID]; ok && hb.Score >= ha.Score {
		t.Fatalf("무관한 기억 점수 %v ≥ %v", hb.Score, ha.Score)
	}
	// 같은 입력에 같은 순서(id 순).
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Memory.ID >= hits[i].Memory.ID {
			t.Fatalf("id 순이 아닙니다: %s, %s", hits[i-1].Memory.ID, hits[i].Memory.ID)
		}
	}
}

func TestRecallHits_뜻_있는_낱말이_없으면_아무것도_없다(t *testing.T) {
	s, _ := newTestStore(t)
	saveDoc(t, s, "그냥 지금 해줘", "", "")
	for _, p := range []string{"", "  ", "그냥 지금 해줘", "7 a ㅇ"} {
		hits, err := s.RecallHits(p)
		if err != nil || len(hits) != 0 {
			t.Fatalf("%q → %d건 (err %v)", p, len(hits), err)
		}
	}
}
