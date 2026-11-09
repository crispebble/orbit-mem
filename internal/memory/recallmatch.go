package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// 요청 회상의 두 문턱 재료(설계 · 지연 회상·호스트 계약·어댑터 5.1 4단계). 문턱 값은 설정이 정하고 여기서는 재기만 한다.
//
//   - 점수(Score): 요청의 찾기 질의(RecallQuery)를 찾기(FindScored)와 같은 함수로 매긴 0~1 값이다(rank.go). 다만 어느
//     기억에도 없는 토큰은 분모에서 뺀다(SkipUnknownTokens, 설계 보완 1-6 b6). 그 토큰은 모든 기억에 같은 몫이라 순위는
//     memory_find와 거의 같고 값만 오른다. 이 빼기는 회상에만 있다: 찾기 점수는 순위용, 회상 점수는 문 여닫기용이다(rank.go). 질의는 뜻 있는 낱말(조사·어미를 뗀 것, stem.go)에 한 글자 숫자·라틴 낱말을
//     더한 것이다: 「확정 규칙 7」의 7이 빠지면 제목만 다른 「확정 규칙 N」들이 모두 같은 점수가 된다.
//   - 맞은 비율(Coverage): 뜻 있는 낱말 중 기억(제목·주제·태그·본문)에 나온 낱말 수 ÷ 뜻 있는 낱말 수다(IDF 무게 없음).
//     세 글자 이상 한글 낱말은 두 글자 이상 앞부분이 나와도 맞은 것으로 센다(조사·어미: 「설치해」는 「설치한다」에 맞는다).
//     한 글자 한글 낱말은 기억의 낱말(조사·어미를 뗀 것 포함)과 통째로 같을 때만 맞는다(1-6 b6r 2차): 「밥」이 「비빔밥」의
//     한 글자로 맞으면 짧은 요청의 맞은 비율이 우연히 오른다.

// MeaningfulWords are the words of a request worth matching, in request order, at most 40: words of two or
// more characters and one-syllable Hangul nouns, less filler words, with closed particles and endings taken
// off (stem.go), each once. They do not look at the store: the recall's words are RecallWords, which also
// takes 의 off a one-syllable noun the store knows (resolveGenitives).
func MeaningfulWords(prompt string) []string { return requestWords(prompt, false) }

// RecallQuery is the find query of a request: its meaningful words and its one-character Latin and digit
// words, in request order, at most 40. Like MeaningfulWords it does not look at the store.
func RecallQuery(prompt string) string { return strings.Join(requestWords(prompt, true), " ") }

// RecallWords are the words the recall of a request matches (RecallHitsIn): its meaningful words, with each
// two-syllable X의 taken to X when the live memories have X and not X의 as a whole word (stem.go).
func (s *Store) RecallWords(prompt string) ([]string, error) {
	words, _, err := s.resolvedWords(requestWords(prompt, false), requestWords(prompt, true))
	return words, err
}

// resolvedWords are words and the query words with the X의 the store knows as X taken off (resolveGenitives).
// The query words hold the words, so one look serves both.
func (s *Store) resolvedWords(words, query []string) ([]string, []string, error) {
	known, err := s.genitiveKnown(query)
	if err != nil {
		return nil, nil, err
	}
	return resolveGenitives(words, known), resolveGenitives(query, known), nil
}

// genitiveKnown are the whole words (wholeWords) of the live memories, any place, among the X and X의 of the
// words that may be X+의 (genitiveBase), as much as resolveGenitives needs: X의 first, and X only for the
// X의 no memory has. It reads only the memories the search index finds for the exact tokens of those whole
// words (genitiveTokens, no prefix) and the late ones, as a find does (queryScores), in one read
// transaction. Words without such a word read nothing.
func (s *Store) genitiveKnown(words []string) (map[string]bool, error) {
	var bases []string
	for _, w := range words {
		if x, ok := genitiveBase(w); ok && !slices.Contains(bases, x) {
			bases = append(bases, x)
		}
	}
	if len(bases) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	known := map[string]bool{}
	want := map[string]bool{}
	var qs []queryToken
	for _, x := range bases {
		want[x+"의"] = true
		qs = append(qs, queryToken{text: x + "의"})
	}
	if err := knownIn(tx, qs, want, known); err != nil {
		return nil, err
	}
	want, qs = map[string]bool{}, nil
	for _, x := range bases {
		if !known[x+"의"] {
			want[x] = true
			for _, t := range genitiveTokens(x) {
				qs = append(qs, queryToken{text: t})
			}
		}
	}
	if len(qs) > 0 {
		if err := knownIn(tx, qs, want, known); err != nil {
			return nil, err
		}
	}
	return known, nil
}

// knownIn adds to known the words of want that are whole words of a live memory the tokens qs find.
// It stops reading when all of want is known.
func knownIn(tx *sql.Tx, qs []queryToken, want, known map[string]bool) error {
	scores, err := queryScores(tx, qs, false, nil)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(scores))
	for id, sc := range scores {
		if sc > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	list, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	// 글만 읽는다(memoriesTx의 신뢰·출처는 쓰지 않는다). 살아 있는 기억만 본다.
	rows, err := tx.Query(`SELECT title, topic, body, tags FROM memories WHERE id IN (SELECT value FROM json_each(?)) AND state IN (?, ?)`,
		string(list), StateCandidate, StateConfirmed)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := map[string]bool{}
	for len(found) < len(want) && rows.Next() {
		var m Memory
		var tags string
		if err := rows.Scan(&m.Title, &m.Topic, &m.Body, &tags); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(tags), &m.Tags); err != nil {
			return err
		}
		wholeWordsAmong(matchText(m), want, found)
	}
	for w := range found {
		known[w] = true
	}
	return rows.Err()
}

// wholeWordsAmong adds to known the words of want that are whole words of text (wholeWords). A word or its
// stem starts with the same letter, so only words that start as a word of want does are stemmed.
func wholeWordsAmong(text string, want, known map[string]bool) {
	firsts := map[rune]bool{}
	for w := range want {
		r, _ := utf8.DecodeRuneInString(w)
		firsts[r] = true
	}
	for _, t := range Terms(text, math.MaxInt32) {
		for _, w := range splitWordMarks(t) {
			if r, _ := utf8.DecodeRuneInString(w); !firsts[r] {
				continue
			}
			if want[w] {
				known[w] = true
			}
			if x := stemWord(w); want[x] {
				known[x] = true
			}
		}
	}
}

// RecallHit is a memory that shares a token with a request's meaningful words, with the two measures.
type RecallHit struct {
	Memory   Memory
	Score    float64  // 찾기 점수(0~1)
	Coverage float64  // 뜻 있는 낱말 중 맞은 비율(0~1)
	Matched  []string // 맞은 낱말(요청 순서)
	// TitleShare is set by the recall of a delegated task only (TaskRecallHitsIn, else 0): see titleShares.
	TitleShare float64
}

// Coverage is the share of words that appear in m (title, topic, tags or body), and those words in order.
// No words give 0.
func Coverage(m Memory, words []string) (float64, []string) {
	if len(words) == 0 {
		return 0, nil
	}
	all := matchText(m)
	var matched []string
	var whole map[string]bool // 한 글자 낱말 가운데 기억의 통째 낱말(쓴 그대로 또는 뗀 꼴)인 것, 한 글자 낱말이 있을 때만 만든다
	for _, w := range words {
		if utf8.RuneCountInString(w) == 1 && isHangulStart(w) {
			if whole == nil {
				whole = map[string]bool{}
				one := map[string]bool{}
				for _, x := range words {
					if utf8.RuneCountInString(x) == 1 && isHangulStart(x) {
						one[x] = true
					}
				}
				wholeWordsAmong(all, one, whole)
			}
			if whole[w] {
				matched = append(matched, w)
			}
			continue
		}
		if termScore(w, all) > 0 {
			matched = append(matched, w)
		}
	}
	return float64(len(matched)) / float64(len(words)), matched
}

// matchText is m's title, topic, body and tags, normalized, one per line: the text Coverage matches.
func matchText(m Memory) string {
	parts := []string{Normalize(m.Title), Normalize(m.Topic), Normalize(m.Body)}
	for _, t := range m.Tags {
		parts = append(parts, Normalize(t))
	}
	return strings.Join(parts, "\n")
}

// wholeWords are the words of a normalized text, split as a request's words are (splitWordMarks), each as
// written and with its particles and endings taken off (stemWord). It defines a whole word; the recall reads
// only the ones it asks for (wholeWordsAmong, the same set: genitive_test.go).
func wholeWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, t := range Terms(text, math.MaxInt32) {
		for _, w := range splitWordMarks(t) {
			out[w] = true
			out[stemWord(w)] = true
		}
	}
	return out
}

// RecallHits are the live memories (candidate or confirmed, any place) that share a token with prompt's
// meaningful words, ordered by id. A prompt without meaningful words matches nothing. Which memories a
// session may be given, and the thresholds, are the caller's.
func (s *Store) RecallHits(prompt string) ([]RecallHit, error) { return s.RecallHitsIn(prompt) }

// RecallHitsIn is RecallHits over the memories in states (none: candidate and confirmed), so a caller can
// see the stale versions a request also matches. The words are RecallWords: whether an X의 is X is the live
// memories' in any states, so the stale versions are matched by the same words.
func (s *Store) RecallHitsIn(prompt string, states ...string) ([]RecallHit, error) {
	words, query, err := s.resolvedWords(requestWords(prompt, false), requestWords(prompt, true))
	if err != nil {
		return nil, err
	}
	return s.recallHitsOf(words, strings.Join(query, " "), states)
}

// recallHitsOf are the memories in states that the find query matches, with the share of words each has.
func (s *Store) recallHitsOf(words []string, query string, states []string) ([]RecallHit, error) {
	if len(words) == 0 {
		return nil, nil
	}
	found, err := s.FindScored(FindInput{Query: query, States: states, AllProjects: true, Limit: math.MaxInt32, SkipUnknownTokens: true})
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
