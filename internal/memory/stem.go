package memory

import (
	"strings"
	"unicode/utf8"
)

// 요청 낱말의 조사·어미 떼기(설계 보완, 계획 1-6 b6). 요청 회상의 낱말(MeaningfulWords)과 찾기 질의(RecallQuery)에만
// 쓴다. 색인과 기억 글은 그대로다: 뗀 어간의 바이그램은 원래 어절의 바이그램 앞부분이라, 원래 어절을 가진 기억은
// 그대로 맞는다. 떼지 않으면 「꼴이」「방식이야」「pnpm으로」의 꼬리 바이그램이 어느 기억에도 없는 토큰으로 남아
// 찾기 점수를 깎고 맞은 비율을 낮춘다.
//
// 목록은 학교 문법의 닫힌 목록에서 가져왔다: 격조사·보조사·접속조사, 서술격 조사 「이다」의 활용, 체언에 붙어 용언을
// 만드는 「하다」「되다」의 흔한 활용, 연결·종결 어미. 평가 사례를 보고 늘리지 않는다.
//
//   - 짝 조사(이/가, 은/는, 을/를, 과/와, 으로/로, 이랑/랑, 이나/나, 이야/야)는 앞 글자의 받침에 맞을 때만 뗀다
//     (「사이」의 「이」, 「평가」의 「가」는 조사가 아니다).
//   - 떼고 남는 어간이 두 글자 이상일 때만 뗀다. 한 글자로 남는 것은 은·는·을·를·이만 뗀다(「꼴이」→꼴, 「키를」→키).
//     「가·도·로·에」는 「추가·속도·경로」처럼 두 글자 명사의 끝 글자와 겹쳐 한 글자 어간으로는 떼지 않는다.
//   - 라틴·숫자로 시작하는 어절은 뒤 한글이 통째로 목록에 있을 때만 그 한글을 뗀다(「pnpm으로」→pnpm).
//   - 굳은 부사(fixedAdverbs)는 떼지 않는다: 「제대로」「대체로」는 명사+조사가 아니라 한 낱말이라, 떼면 「제대」「대체(바꿈)」
//     처럼 다른 낱말이 된다. 목록은 표준국어대사전의 부사 표제어 가운데 「-로」로 끝나 조사 「로」로 잘못 떼이는 것이다.
//
// 낱말 앞뒤의 구두점·괄호·따옴표는 낱말이 아니다(splitWordMarks): 「보고하라.」「m2:」「변경(pr」을 그대로 두면 어느
// 기억에도 없는 낱말로 셈한다. 낱말 안의 점·붙임표·밑줄·빗금·더하기·샵(node.js, api-key, c++, c#)은 남긴다.

// tailRule says which syllable a tail may follow.
type tailRule int

const (
	afterAny         tailRule = iota
	afterFinal                // 받침 있는 글자 뒤(이·은·을·과·으로·이랑·이나·이야)
	afterOpen                 // 받침 없는 글자 뒤(가·는·를·와·랑·나·야)
	afterOpenOrRieul          // 받침 없거나 ㄹ 받침 뒤(로)
)

// stemTails are the closed tails, matched longest first (stemTailOrder).
var stemTails = map[string]tailRule{}

// stemTailOrder is stemTails, longest first, then by text.
var stemTailOrder []string

// oneSyllableStemTails are the tails taken off even when one syllable is left.
var oneSyllableStemTails = map[string]bool{"은": true, "는": true, "을": true, "를": true, "이": true}

// fixedAdverbs are adverbs that end in 로 without a particle (see above). Stemming leaves them whole.
var fixedAdverbs = map[string]bool{}

// functionSyllables are one-syllable words of closed classes (대명사·관형사·부사·의존명사·조사·흔한 용언 활용, 그리고
// 체언에 붙어 용언을 만드는 「하다」「되다」의 어간 하·되) that say nothing about a topic. A word that is one of them
// after stemming (「하는」→하, 「것을」→것) is left out too. Other one-syllable Hangul words (앱·키·꼴) are nouns and stay.
var functionSyllables = map[string]bool{}

func init() {
	add := func(rule tailRule, list string) {
		for _, t := range strings.Fields(list) {
			stemTails[t] = rule
		}
	}
	add(afterFinal, `이 은 을 과 으로 으로는 으로도 으로의 이랑 이나 이야 이다 이고 이면 이라 이란 이라도 이라서 이라고 이에요 이죠 이지`)
	add(afterOpen, `가 는 를 와 랑 나 야 예요`)
	add(afterOpenOrRieul, `로 로는 로도 로의`)
	add(afterAny, `
		의 에 에서 에게 한테 께 께서 에게서 한테서 에는 에서는 에도 에서도 에게는 에의 에서의
		도 만 까지 부터 조차 마저 밖에 처럼 보다 마다 라도 든지 이든지 하고 과는 와는 까지는 부터는
		인지 인데 입니다
		하다 한다 해 해요 해서 해야 해도 해줘 하면 하는 하고 하게 하지 하려면 하려고 한 할 함 했 했다 했지 했어 했나
		하나 하나요 하는지 했는지 할지 할까 합니다 했습니다
		되다 된다 돼 돼요 되는 된 될 되면 되고 됐 됐다 됐어 되나 되는지 됐는지 됩니다
		는지 은지 을지 려면 으려면 는데 은데 으면 면 어야 아야 어도 아도 어서 아서 고 지 죠 다 요 나요 을까 냐 니 서`)
	for _, w := range strings.Fields(`제대로 그대로 이대로 저대로 멋대로 제멋대로 마음대로 저절로 스스로 함부로 대체로 때때로 날로 홀로
		새로 따로 별로 서로 절로 주로`) {
		fixedAdverbs[w] = true
	}
	for t := range stemTails {
		stemTailOrder = append(stemTailOrder, t)
	}
	sortTails(stemTailOrder)
	for _, w := range strings.Fields(`이 그 저 뭐 왜 좀 더 잘 안 못 꼭 또 다 각 한 두 세 및 수 것 거 게 걸 건 데 때 뭘 곧 막 늘
		하 되 될 할 해 돼 줘 나 너 날 내 네 제 난 넌 은 는 을 를 가 에 의 도 만 와 과 로 어 아 음 응 예`) {
		functionSyllables[w] = true
	}
}

// sortTails orders tails longest first, then by text, so the same word always loses the same tail.
func sortTails(ts []string) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && tailLess(ts[j], ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func tailLess(a, b string) bool {
	if la, lb := utf8.RuneCountInString(a), utf8.RuneCountInString(b); la != lb {
		return la > lb
	}
	return a < b
}

// syllableFinal says whether a Hangul syllable has a final consonant, and whether it is ㄹ.
func syllableFinal(r rune) (final, rieul bool) {
	if r < 0xAC00 || r > 0xD7A3 {
		return false, false
	}
	f := (r - 0xAC00) % 28
	return f != 0, f == 8
}

// tailFits says whether tail may follow the syllable before it.
func tailFits(rule tailRule, before rune) bool {
	final, rieul := syllableFinal(before)
	switch rule {
	case afterFinal:
		return final
	case afterOpen:
		return !final
	case afterOpenOrRieul:
		return !final || rieul
	}
	return true
}

// stemWord takes a closed particle or ending off a word (see the rules above). Other words are unchanged.
func stemWord(w string) string {
	rs := []rune(w)
	if len(rs) < 2 || fixedAdverbs[w] {
		return w
	}
	if kindOf(rs[0]) != kindHangul {
		for i, r := range rs {
			if kindOf(r) == kindHangul {
				if _, ok := stemTails[string(rs[i:])]; ok && i > 0 {
					return string(rs[:i])
				}
				return w
			}
		}
		return w
	}
	for _, t := range stemTailOrder {
		tr := []rune(t)
		n := len(rs) - len(tr)
		if n < 1 || string(rs[n:]) != t {
			continue
		}
		if n == 1 && !oneSyllableStemTails[t] {
			continue
		}
		for _, r := range rs[:n] {
			if kindOf(r) != kindHangul {
				return w
			}
		}
		if !tailFits(stemTails[t], rs[n-1]) {
			continue
		}
		return string(rs[:n])
	}
	return w
}

// requestWords are the words of a request after stemming, in order, without duplicates, at most 40: words of
// two or more characters and one-syllable Hangul nouns, less filler words. withShort adds one-character Latin
// and digit words (the find query keeps them: 「확정 규칙 7」의 7).
func requestWords(prompt string, withShort bool) []string {
	var out []string
	seen := map[string]bool{}
	var terms []string
	for _, t := range Terms(prompt, 64) {
		terms = append(terms, splitWordMarks(t)...)
	}
	for _, t := range terms {
		if stopwords[t] {
			continue
		}
		w := t
		if utf8.RuneCountInString(t) == 1 {
			if isHangulStart(t) {
				if functionSyllables[t] {
					continue
				}
			} else if !withShort {
				continue
			}
		} else if w = stemWord(t); utf8.RuneCountInString(w) == 1 && isHangulStart(w) && functionSyllables[w] {
			continue // 뗀 뒤 한 글자 기능어
		}
		if stopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) == 40 {
			break
		}
	}
	return out
}

// wordJoiners are the marks a word keeps inside it (node.js, api-key, snake_case, a/b, c++, c#).
const wordJoiners = ".-_/+#"

// splitWordMarks splits a term at the marks that are not part of a word (brackets, quotes, colons, the
// middle dot) and trims the joiners off the ends of each part: 「변경(pr」 → 변경, pr; 「보고하라.」 → 보고하라.
func splitWordMarks(t string) []string {
	parts := strings.FieldsFunc(t, func(r rune) bool { return !isTokenRune(r) && !strings.ContainsRune(wordJoiners, r) })
	out := parts[:0]
	for _, p := range parts {
		if p = strings.Trim(p, wordJoiners); p != "" {
			out = append(out, p)
		}
	}
	return out
}
