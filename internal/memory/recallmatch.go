package memory

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// 요청 회상의 두 문턱 재료(설계 · 지연 회상·호스트 계약·어댑터 5.1 4단계). 문턱 값은 설정이 정하고 여기서는 재기만 한다.
//
//   - 점수(Score): 요청의 찾기 질의(RecallQuery)를 찾기(FindScored)와 같은 함수로 매긴 0~1 값이다(rank.go). 같은 질의로
//     memory_find를 부르면 같은 점수가 나와, 찾기와 회상이 같은 순위를 본다. 질의는 뜻 있는 낱말에 한 글자 숫자·라틴
//     낱말을 더한 것이다: 「확정 규칙 7」의 7이 빠지면 제목만 다른 「확정 규칙 N」들이 모두 같은 점수가 된다.
//   - 맞은 비율(Coverage): 뜻 있는 낱말 중 기억(제목·주제·태그·본문)에 나온 낱말 수 ÷ 뜻 있는 낱말 수다(IDF 무게 없음).
//     세 글자 이상 한글 낱말은 두 글자 이상 앞부분이 나와도 맞은 것으로 센다(조사·어미: 「설치해」는 「설치한다」에 맞는다).

// MeaningfulWords are the words of a request worth matching: two or more characters and not a filler word,
// in request order, at most 40.
func MeaningfulWords(prompt string) []string { return promptTerms(prompt) }

// RecallQuery is the find query of a request: its words without filler words and one-character Hangul
// words (particles), in request order, at most 40.
func RecallQuery(prompt string) string {
	var out []string
	for _, t := range Terms(prompt, 64) {
		if stopwords[t] || (utf8.RuneCountInString(t) == 1 && isHangulStart(t)) {
			continue
		}
		out = append(out, t)
		if len(out) == 40 {
			break
		}
	}
	return strings.Join(out, " ")
}

// RecallHit is a memory that shares a token with a request's meaningful words, with the two measures.
type RecallHit struct {
	Memory   Memory
	Score    float64  // 찾기 점수(0~1)
	Coverage float64  // 뜻 있는 낱말 중 맞은 비율(0~1)
	Matched  []string // 맞은 낱말(요청 순서)
}

// Coverage is the share of words that appear in m (title, topic, tags or body), and those words in order.
// No words give 0.
func Coverage(m Memory, words []string) (float64, []string) {
	if len(words) == 0 {
		return 0, nil
	}
	parts := []string{Normalize(m.Title), Normalize(m.Topic), Normalize(m.Body)}
	for _, t := range m.Tags {
		parts = append(parts, Normalize(t))
	}
	all := strings.Join(parts, "\n")
	var matched []string
	for _, w := range words {
		if termScore(w, all) > 0 {
			matched = append(matched, w)
		}
	}
	return float64(len(matched)) / float64(len(words)), matched
}

// RecallHits are the live memories (candidate or confirmed, any place) that share a token with prompt's
// meaningful words, ordered by id. A prompt without meaningful words matches nothing. Which memories a
// session may be given, and the thresholds, are the caller's.
func (s *Store) RecallHits(prompt string) ([]RecallHit, error) { return s.RecallHitsIn(prompt) }

// RecallHitsIn is RecallHits over the memories in states (none: candidate and confirmed), so a caller can
// see the stale versions a request also matches.
func (s *Store) RecallHitsIn(prompt string, states ...string) ([]RecallHit, error) {
	words := MeaningfulWords(prompt)
	if len(words) == 0 {
		return nil, nil
	}
	found, err := s.FindScored(FindInput{Query: RecallQuery(prompt), States: states, AllProjects: true, Limit: math.MaxInt32})
	if err != nil {
		return nil, err
	}
	out := make([]RecallHit, 0, len(found))
	for _, h := range found {
		cover, matched := Coverage(h.Memory, words)
		out = append(out, RecallHit{Memory: h.Memory, Score: h.Score, Coverage: cover, Matched: matched})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Memory.ID < out[j].Memory.ID })
	return out, nil
}
