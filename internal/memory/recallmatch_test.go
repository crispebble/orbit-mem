package memory

import (
	"math"
	"slices"
	"testing"
)

// 요청 회상의 두 문턱 재료(설계 · 지연 회상 5.1 4단계): 점수는 찾기와 같은 점수이고, 맞은 비율은 뜻 있는 낱말 중
// 기억에 나온 낱말의 비율이다.

func TestMeaningfulWords_불용어와_기능어_한_글자를_빼고_조사_어미를_뗀다(t *testing.T) {
	got := MeaningfulWords("이거 pnpm으로 설치해 줘 please 7")
	if want := []string{"pnpm", "설치"}; !slices.Equal(got, want) {
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

// 1-6 b6r 2차 규칙 e: 한 글자 한글 낱말은 기억의 낱말(조사·어미를 뗀 것 포함)과 통째로 같을 때만 맞는다. 「밥」이 「밥값」·
// 「비빔밥」의 한 글자로 맞으면 짧은 요청의 맞은 비율이 우연히 오른다.
func TestCoverage_한_글자_한글_낱말은_기억의_낱말과_통째로_같을_때만_맞는다(t *testing.T) {
	whole := Memory{Title: "점심 밥은 12시에 먹는다", Body: "문(門)은 닫는다"}
	if _, matched := Coverage(whole, []string{"밥", "문", "점심"}); !slices.Equal(matched, []string{"밥", "문", "점심"}) {
		t.Fatalf("통째 낱말·조사 뗀 낱말·괄호 앞 낱말이 맞아야 함: %v", matched)
	}
	inside := Memory{Title: "비빔밥 밥값은 문서로 남긴다", Tags: []string{"문서함"}}
	if _, matched := Coverage(inside, []string{"밥", "문", "문서"}); !slices.Equal(matched, []string{"문서"}) {
		t.Fatalf("낱말 안의 한 글자는 맞지 않아야 함: %v", matched)
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
	if q := RecallQuery(prompt + " 7 v"); q != "pnpm 설치 7 v" {
		t.Fatalf("찾기 질의 %q", q)
	}
	if want := scoreOf(t, s, RecallQuery(prompt), a.ID); math.Abs(ha.Score-want) > 1e-12 {
		t.Fatalf("회상 점수 %v, 같은 낱말의 찾기 점수 %v", ha.Score, want)
	}
	if ha.Coverage != 1 || !slices.Equal(ha.Matched, []string{"pnpm", "설치"}) {
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

// 어느 기억에도 없는 질의 토큰은 어느 기억인지를 가르지 못한다: 회상 점수의 분모에 넣지 않는다(설계 보완, 1-6 b6).
// memory_find의 점수는 그대로다(기억 수에 덜 흔들리고, 낱말 하나 겹친 무관한 기억이 낮다: rank_test.go).
func TestRecallHits_어느_기억에도_없는_낱말은_점수를_깎지_않고_찾기는_그대로다(t *testing.T) {
	s, _ := newTestStore(t)
	a := saveDoc(t, s, "운영 배포는 main 병합으로 일어난다", "감시기가 main을 본다", "")
	saveDoc(t, s, "로그 꼴은 JSON 한 줄", "", "")
	for i := 0; i < 6; i++ {
		filler(t, s, i)
	}
	recallScore := func(prompt string) float64 {
		hits, err := s.RecallHits(prompt)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			if h.Memory.ID == a.ID {
				return h.Score
			}
		}
		return 0
	}
	without, with := recallScore("배포 main 감시기"), recallScore("배포 main 감시기 zzqxv 콰뚫")
	if without <= 0 || math.Abs(with-without) > 1e-12 {
		t.Fatalf("없는 낱말을 더한 회상 점수 %v, 없이 %v", with, without)
	}
	if f, g := scoreOf(t, s, "배포 main 감시기", a.ID), scoreOf(t, s, "배포 main 감시기 zzqxv 콰뚫", a.ID); g >= f {
		t.Fatalf("찾기 점수는 없는 낱말도 분모에 넣어야 함: %v ≥ %v", g, f)
	}
}

// 낱말 앞뒤의 구두점·괄호는 낱말이 아니다: 「보고하라.」「m2:」「변경(pr」이 그대로 남으면 어느 기억에도 맞지 않는 낱말로
// 셈해 맞은 비율을 낮춘다. 낱말 안의 점·붙임표(node.js, api-key)는 남긴다.
func TestMeaningfulWords_낱말_앞뒤의_구두점과_괄호를_뗀다(t *testing.T) {
	got := MeaningfulWords("m2: 변경(pr 초안)을 「설계안」에 node.js·api-key와 함께 보고하라.")
	if want := []string{"m2", "변경", "pr", "초안", "설계안", "node.js", "api-key", "함께", "보고하라"}; !slices.Equal(got, want) {
		t.Fatalf("뜻 있는 낱말 %v, want %v", got, want)
	}
	if q := RecallQuery("(a) 7. 배포!"); q != "a 7 배포" {
		t.Fatalf("찾기 질의 %q", q)
	}
}
