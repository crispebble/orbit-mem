package memory

import (
	"math"
	"slices"
	"testing"
)

// 위임 회상의 낱말(설계 보완, 1-6 b6 T1): 위임 글은 일을 시키는 말(명령형 어미, 위임 머리 낱말, 항목 번호)이 섞여 있다.
// 그 낱말은 어느 일에나 붙어 어떤 기억인지를 가르지 못하니 내용 낱말에서 뺀다.

func TestTaskWords_명령형_어미와_머리_낱말과_항목_번호를_뺀다(t *testing.T) {
	got := TaskWords("계획: orbit 항목: m2. 요청 검토 조사. 결제 동시성 시험을 고쳐라. 경합 원인을 보고하라. 로그는 지우지 마라. 1. 2) 인프라 카메라 a7) node.js 읽으라 정리해라 바꿔라 s3 버킷")
	want := []string{"orbit", "결제", "동시성", "시험", "경합", "원인", "로그", "지우", "인프라", "카메라", "node.js", "s3", "버킷"}
	if !slices.Equal(got, want) {
		t.Fatalf("내용 낱말 %v, want %v", got, want)
	}
	if got := MeaningfulWords("결제 시험을 고쳐라"); !slices.Contains(got, "고쳐라") {
		t.Fatalf("요청 낱말은 그대로여야 함: %v", got)
	}
}

// 항목 번호는 번호 꼴(「m2:」「1.」「a7)」)이거나 「항목」 바로 뒤에 온 것이다. 그냥 쓴 「s3」「h2」는 내용 낱말이다.
func TestTaskWords_항목_번호는_번호_꼴이나_항목_뒤에_온_것만이다(t *testing.T) {
	got := TaskWords("항목 b6h s3 버킷과 h2 연결을 m2. 단계에서 고친다")
	if want := []string{"s3", "버킷", "h2", "연결", "단계", "고친"}; !slices.Equal(got, want) {
		t.Fatalf("내용 낱말 %v, want %v", got, want)
	}
}

func TestTaskRecallHits_점수와_맞은_낱말은_내용_낱말로_잰다(t *testing.T) {
	s, _ := newTestStore(t)
	a := saveDoc(t, s, "결제 모듈 동시성 시험은 경합을 일으킨다", "고루틴 여럿으로 경합을 일으킨다", "결제 동시성")
	saveDoc(t, s, "보고는 짧게 한다", "검토 요청은 한 줄로", "")
	for i := 0; i < 6; i++ {
		filler(t, s, i)
	}
	task := "계획: x 항목: m2. 결제 모듈 동시성 경합을 조사하라. 검토 요청을 보고하라."
	hits, err := s.TaskRecallHitsIn(task)
	if err != nil {
		t.Fatal(err)
	}
	var ha *RecallHit
	for i := range hits {
		if hits[i].Memory.ID == a.ID {
			ha = &hits[i]
		}
	}
	if ha == nil {
		t.Fatalf("결제 기억이 없습니다: %+v", hits)
	}
	if !slices.Equal(ha.Matched, []string{"결제", "모듈", "동시성", "경합"}) {
		t.Fatalf("맞은 낱말 %v", ha.Matched)
	}
	if want := scoreOfSkip(t, s, "x 결제 모듈 동시성 경합", a.ID); math.Abs(ha.Score-want) > 1e-12 {
		t.Fatalf("위임 회상 점수 %v, 내용 낱말의 회상 점수 %v", ha.Score, want)
	}
	for _, h := range hits {
		if h.Memory.ID != a.ID && len(h.Matched) > 0 {
			t.Fatalf("시키는 말로만 맞은 기억: %s %v", h.Memory.Title, h.Matched)
		}
	}
}

// scoreOfSkip is the recall score of id for query: the find score without the tokens no memory has.
func scoreOfSkip(t *testing.T, s *Store, query, id string) float64 {
	t.Helper()
	found, err := s.FindScored(FindInput{Query: query, AllProjects: true, Limit: math.MaxInt32, SkipUnknownTokens: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range found {
		if h.Memory.ID == id {
			return h.Score
		}
	}
	return 0
}
