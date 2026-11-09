package memory

import (
	"slices"
	"testing"
)

// 요청 낱말의 조사·어미 떼기(stem.go): 닫힌 목록만 떼고, 받침에 맞는 조사만 떼며, 한 글자로 남는 것은 은·는·을·를·이만 뗀다.

func TestStemWord_닫힌_조사와_어미만_뗀다(t *testing.T) {
	for in, want := range map[string]string{
		"꼴이":     "꼴",  // 받침 뒤 주격 조사
		"키를":     "키",  // 받침 없는 말 뒤 목적격 조사
		"방식이야":   "방식", // 서술격 조사 활용
		"데이터가":   "데이터",
		"규칙으로":   "규칙",
		"서버로":    "서버",
		"어디서":    "어디",
		"설치해":    "설치", // 하다 활용
		"배포했는지":  "배포",
		"쓰기로":    "쓰기",
		"페이징은":   "페이징",
		"pnpm으로": "pnpm", // 라틴 낱말 뒤 조사
		"api를":   "api",
		"사이":     "사이", // 받침 없는 말 뒤 「이」는 조사가 아니다
		"추가":     "추가", // 한 글자로 남는 「가」는 떼지 않는다
		"경로":     "경로",
		"속도":     "속도",
		"평가":     "평가", // 받침 뒤 「가」는 조사가 아니다
		"설치":     "설치",
		"v2버전":   "v2버전", // 라틴 뒤 한글이 조사·어미가 아니면 그대로
		"deploy": "deploy",
	} {
		if got := stemWord(in); got != want {
			t.Errorf("stemWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMeaningfulWords_한_글자_명사는_남기고_기능어는_뺀다(t *testing.T) {
	got := MeaningfulWords("앱 로그 꼴이 좀 뭐 더 이거를 키를 바꿔")
	if want := []string{"앱", "로그", "꼴", "키", "바꿔"}; !slices.Equal(got, want) {
		t.Fatalf("뜻 있는 낱말 %v, want %v", got, want)
	}
}

func TestMeaningfulWords_뗀_뒤_같은_낱말은_한_번만(t *testing.T) {
	got := MeaningfulWords("배포를 배포는 배포")
	if want := []string{"배포"}; !slices.Equal(got, want) {
		t.Fatalf("뜻 있는 낱말 %v, want %v", got, want)
	}
}
