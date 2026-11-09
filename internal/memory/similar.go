package memory

import (
	"math"
	"strings"
)

// 두 기억 사이 비슷함(설계 6.1 L-16, T-14 · 계획 a6c, 1-6 b6d 2차). 저장 전 판정(SaveCheck)이 같은 범위의 기억마다 매긴다.
//
// 찾기 점수(rank.go)는 「질의 토큰을 기억이 얼마나 덮나」라서 한쪽만 본다. 저장할 글이 옛 기억에 본문을 더하거나 제목을
// 다시 쓰면, 옛 기억은 새 글에 다 들어 있어도 새 글 토큰의 몫이 작아 낮게 나온다. 또 IDF가 다른 기억들에 기대므로,
// 같은 내용 사본이 이미 있으면 서로의 낱말을 흔하게 만들어 다음 중복의 점수를 깎는다.
//
// 그래서 비슷함은 두 기억의 낱말 집합(제목·주제·태그·본문)만으로 정한다. 다른 기억이 몇 건이든, 무엇이 있든 같은 짝의
// 점수는 같다. 두 방향 덮음의 기하 평균(Ochiai 계수를 부분 맞음까지 넓힌 것)이다.
//
//	낱말 = 찾기 낱말(Terms → splitWordMarks)에서 군말(stopwords)을 빼고 조사·어미를 뗀 것(stemWord, 요청 회상과 같은 규칙)
//	cover(A→B) = Σ_{w∈A} m(w,B) / |A|
//	m(w,B) = 1(B에 같은 낱말) · 0.5(B에 w의 다른 꼴이 있다, sameForm) · 0
//	다른 꼴 = 한글: 두 글자 이상이고 첫 글자가 같고 둘째 글자가 받침만 다르거나 같다(「실행된다」「실행」, 「답한다」「답하기」)
//	          영어: 한쪽이 다른 쪽의 앞부분이고 짧은 쪽이 네 글자 이상(「databases」「database」)
//	similarity(A,B) = √(cover(A→B) · cover(B→A))
//
// 같은 낱말 집합이면 1, 같은 낱말이 없으면 0이다. 낱말 단위인 까닭: 바이그램은 「백업은」「백업을」처럼 조사만 다른 낱말의
// 꼬리 토큰을 다른 토큰으로 세어, 서술어·조사를 바꿔 다시 쓴 짧은 제목을 0.42~0.6으로 깎았다(1-6 b6d 1차 잰 값). 다른 꼴
// 부분 맞음은 떼는 목록에 없는 활용(「실행된다」「실행」, 「줄이지」「줄인다」, 받침으로 붙는 어미 「답한다」「답하기」)과 영어
// 복수·활용(-s·-es·-ed·-ing)을 반만 센다. 받침·영어 꼴은 1-6 b6db에 더했다: 낱말 셋 가운데 하나만 꼴이 바뀐 짧은
// 제목(「Use Docker for databases」「… the database」)이 2/3에 머물러, 같은 틀에 값 하나만 다른 새 사실(낱말 여섯 가운데
// 넷이 같다, 2/3)과 갈라지지 않았다. 기하 평균인 까닭: 산술
// 평균은 한쪽이 다른 쪽에 다 들어 있으면(덮음 1) 다른 쪽 덮음이 작아도 0.5 남짓을 주어, 짧은 기억이 긴 글에 다 들어
// 있는 짝이나 낱말 둘 가운데 머리 낱말 하나만 같은 짝이 낱말을 바꿔 다시 쓴 것과 붙었다. 기하 평균은 그 짝을 1/3 이하로
// 내리고(덮음 1과 1/9이면 1/3) 두 방향이 모두 겹친 바꿔 쓰기는 거의 그대로 둔다. 칸 무게와 IDF는 쓰지 않는다: 칸 무게는
// 같은 낱말이 다른 칸에 있는 바꿔 쓰기를 깎고, IDF는 짝 밖의 기억에 점수를 묶는다(a6c 잰 값).

// similarPrefixRunes is how many leading characters two Hangul words share at least for half a match (the
// last of them may differ in its final consonant).
const similarPrefixRunes = 2

// similarLatinPrefix is how many letters the shorter of two Latin words has at least for half a match when it
// begins the longer one.
const similarLatinPrefix = 4

// tokenSet is the distinct words of a memory, all fields together, in first-seen order.
type tokenSet struct {
	list []string
	has  map[string]bool
}

// memoryTokens is the word set of a memory's title, topic, tags and body (similarWords of each).
func memoryTokens(title, topic string, tags []string, body string) tokenSet {
	s := tokenSet{has: map[string]bool{}}
	for _, part := range []string{title, strings.Join(append([]string{topic}, tags...), " "), body} {
		for _, w := range similarWords(part) {
			if !s.has[w] {
				s.has[w] = true
				s.list = append(s.list, w)
			}
		}
	}
	return s
}

// similarWords are the words of a text for similarity: the find terms split at word marks, less filler
// words, with the closed particle or ending taken off.
func similarWords(text string) []string {
	var out []string
	for _, t := range Terms(text, math.MaxInt) {
		for _, w := range splitWordMarks(t) {
			if !stopwords[w] {
				out = append(out, stemWord(w))
			}
		}
	}
	return out
}

// cover is how much of a's words b has, 0~1 (0 when a has none).
func cover(a, b tokenSet) float64 {
	if len(a.list) == 0 {
		return 0
	}
	sum := 0.0
	for _, w := range a.list {
		switch {
		case b.has[w]:
			sum++
		case sameForm(w, b.list):
			sum += 0.5
		}
	}
	return sum / float64(len(a.list))
}

// sameForm says whether list has another form of the word w: for a Hangul word of similarPrefixRunes or more
// characters, a word with the same first character and a second one that differs at most in its final consonant;
// for a Latin word, a word that it begins or that begins it, the shorter of similarLatinPrefix letters or more.
func sameForm(w string, list []string) bool {
	if isHangulStart(w) {
		rw := []rune(w)
		if len(rw) < similarPrefixRunes {
			return false
		}
		for _, x := range list {
			rx := []rune(x)
			if len(rx) >= similarPrefixRunes && rx[0] == rw[0] && syllableBase(rx[1]) == syllableBase(rw[1]) {
				return true
			}
		}
		return false
	}
	if !isLatinWord(w) {
		return false
	}
	for _, x := range list {
		short, long := w, x
		if len(short) > len(long) {
			short, long = long, short
		}
		if len(short) >= similarLatinPrefix && isLatinWord(x) && strings.HasPrefix(long, short) {
			return true
		}
	}
	return false
}

// syllableBase is a Hangul syllable without its final consonant; any other character as it is.
func syllableBase(r rune) rune {
	if r >= 0xAC00 && r <= 0xD7A3 {
		return r - (r-0xAC00)%28
	}
	return r
}

// isLatinWord says whether w is made only of the lower-case letters a~z (words are lower-cased, Terms).
func isLatinWord(w string) bool {
	for i := 0; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return false
		}
	}
	return w != ""
}

// similarity is how alike two memories are, 0~1, from their word sets alone: the geometric mean of the two
// covers.
func similarity(a, b tokenSet) float64 {
	return math.Sqrt(cover(a, b) * cover(b, a))
}
