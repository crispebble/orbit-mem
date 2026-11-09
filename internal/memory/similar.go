package memory

import "strings"

// 두 기억 사이 비슷함(설계 6.1 L-16, T-14 · 계획 a6c). 저장 전 판정(SaveCheck)이 같은 범위의 기억마다 매긴다.
//
// 찾기 점수(rank.go)는 「질의 토큰을 기억이 얼마나 덮나」라서 한쪽만 본다. 저장할 글이 옛 기억에 본문을 더하거나 제목을
// 다시 쓰면, 옛 기억은 새 글에 다 들어 있어도 새 글 토큰의 몫이 작아 낮게 나온다. 또 IDF가 다른 기억들에 기대므로,
// 같은 내용 사본이 이미 있으면 서로의 낱말을 흔하게 만들어 다음 중복의 점수를 깎는다.
//
// 그래서 비슷함은 두 기억의 색인 토큰(제목·주제·태그·본문, 색인과 같은 가공) 집합만으로 정한다. 다른 기억이 몇 건이든,
// 무엇이 있든 같은 짝의 점수는 같다. 양쪽 덮음의 산술 평균(Kulczynski 계수, 문서 중복 탐지의 containment를 양쪽으로
// 본 것)이다.
//
//	cover(A→B) = Σ_{t∈A} m(t,B) / |A|
//	m(t,B) = 1(B에 같은 토큰) · prefixMatch(t가 라틴 낱말이나 한 글자이고 B의 토큰이 t로 시작) · 0
//	similarity(A,B) = (cover(A→B) + cover(B→A)) / 2
//
// 같은 토큰 집합이면 1, 겹침이 없으면 0이다. 한쪽이 다른 쪽에 다 들어 있어도(짧은 기억이 긴 글에 다 들어감) 다른 쪽
// 덮음이 작으면 0.5 남짓이라, 문턱(0.5보다 큼)을 넘으려면 두 방향이 모두 어느 정도 겹쳐야 한다. 칸 무게와 IDF를 쓰지
// 않는 까닭: 칸 무게는 같은 낱말이 다른 칸에 있는 바꿔 쓰기를 깎고, IDF는 짝 밖의 기억에 점수를 묶는다(a6c 잰 값).

// tokenSet is the distinct index tokens of a memory, all fields together, in first-seen order.
type tokenSet struct {
	list []string
	has  map[string]bool
}

// memoryTokens is the token set of a memory's title, topic, tags and body (the index's processing).
func memoryTokens(title, topic string, tags []string, body string) tokenSet {
	s := tokenSet{has: map[string]bool{}}
	for _, part := range []string{title, strings.Join(append([]string{topic}, tags...), " "), body} {
		for _, t := range indexTokens(part) {
			if !s.has[t] {
				s.has[t] = true
				s.list = append(s.list, t)
			}
		}
	}
	return s
}

// cover is how much of a's tokens b has, 0~1 (0 when a has none).
func cover(a, b tokenSet) float64 {
	if len(a.list) == 0 {
		return 0
	}
	sum := 0.0
	for _, t := range a.list {
		switch {
		case b.has[t]:
			sum++
		case prefixable(t):
			sum += tokenMatch(queryToken{t, true}, b.list)
		}
	}
	return sum / float64(len(a.list))
}

// similarity is how alike two memories are, 0~1, from their token sets alone: the mean of the two covers.
func similarity(a, b tokenSet) float64 {
	return (cover(a, b) + cover(b, a)) / 2
}
