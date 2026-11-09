package memory

import (
	"strings"
	"unicode"
)

// FTS5 색인용 가공 글과 찾기 질의 토큰(설계 7절, ORBIT-25). MATCH 글은 rank.go의 candidateMatch가 만든다.
//
// 한국어는 낱말이 두 글자인 경우가 많아 trigram만으로는 못 찾는다. 그래서 어절을 한글·한자·가나
// 조각으로 나누고, 두 글자 이상 조각은 겹치는 두 글자(바이그램), 한 글자는 그대로 색인 칸에 넣어
// unicode61로 색인한다. 질의도 같은 방식으로 쪼갠다. 바이그램은 한 조각 안에서만 만들고 어절이나
// 글자 종류 경계를 넘지 않는다. NFC 정규화는 하지 않는다.

type runKind int

const (
	kindOther runKind = iota // 라틴·숫자·그 밖 글자: 소문자 낱말 하나로 둔다.
	kindHangul
	kindHan
	kindKana
)

// isTokenRune는 unicode61의 토큰 글자 기준(L*, N*, Co)이다. 결합 기호(M*)는 앞 글자에 붙은 채 둔다.
func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r) || unicode.IsMark(r)
}

func kindOf(r rune) runKind {
	switch {
	case r >= 0xAC00 && r <= 0xD7A3, r >= 0x1100 && r <= 0x11FF, r >= 0x3130 && r <= 0x318F:
		return kindHangul
	case unicode.Is(unicode.Han, r):
		return kindHan
	case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r), r == 0x30FC:
		return kindKana
	}
	return kindOther
}

// piece는 어절 안의 같은 종류 글자 조각이다. toks는 그 조각의 색인 토큰이다.
type piece struct {
	toks []string
}

func makePiece(kind runKind, rs []rune) piece {
	if kind == kindOther {
		return piece{[]string{strings.ToLower(string(rs))}}
	}
	if len(rs) == 1 {
		return piece{[]string{string(rs)}}
	}
	toks := make([]string, 0, len(rs)-1)
	for i := 0; i+1 < len(rs); i++ {
		toks = append(toks, string(rs[i:i+2]))
	}
	return piece{toks}
}

// splitWords는 글을 어절로, 어절을 같은 종류 조각으로 나눈다.
func splitWords(s string) [][]piece {
	var words [][]piece
	var cur []piece
	var run []rune
	var kind runKind
	flushRun := func() {
		if len(run) > 0 {
			cur = append(cur, makePiece(kind, run))
			run = run[:0]
		}
	}
	flushWord := func() {
		flushRun()
		if len(cur) > 0 {
			words = append(words, cur)
			cur = nil
		}
	}
	for _, r := range s {
		if !isTokenRune(r) {
			flushWord()
			continue
		}
		k := kindOf(r)
		if unicode.IsMark(r) && len(run) > 0 {
			k = kind // 결합 기호는 앞 조각에 붙인다.
		}
		if len(run) > 0 && k != kind {
			flushRun()
		}
		kind = k
		run = append(run, r)
	}
	flushWord()
	return words
}

// indexTokens는 색인·점수용 토큰 목록이다. 순서를 지키고 중복을 남긴다.
func indexTokens(s string) []string {
	var out []string
	for _, w := range splitWords(s) {
		for _, p := range w {
			out = append(out, p.toks...)
		}
	}
	return out
}

// indexText는 FTS5 칸에 넣을 가공 글이다. unicode61이 다시 나눠도 indexTokens와 같은 토큰이 나온다.
func indexText(s string) string {
	return strings.Join(indexTokens(s), " ")
}

// quoteFTS는 토큰 하나를 FTS5 따옴표 글로 감싼다. 그래서 문법 글자(AND·OR·NEAR·( ) : * - ^ ")가 질의로 해석되지 않는다.
func quoteFTS(tok string) string {
	return `"` + strings.ReplaceAll(tok, `"`, `""`) + `"`
}

// queryTokens는 찾기 질의의 토큰이다(후보 찾기 candidateMatch와 점수 rank.go가 함께 쓴다). 순서를 지키고 중복을 없앤다.
// 어절의 토큰 수 합계가 limit(0 이하면 제한 없음)를 넘으면 뒤 어절을 버린다. 첫 어절 하나만으로 넘으면 그 어절을 limit개
// 토큰으로 자른다. 합계는 중복을 없애기 전 토큰 수로 센다.
func queryTokens(q string, limit int) []string {
	var out []string
	seen := map[string]bool{}
	total := 0
	for _, w := range splitWords(q) {
		var toks []string
		for _, p := range w {
			toks = append(toks, p.toks...)
		}
		if limit > 0 {
			room := limit - total
			if len(toks) > room {
				if total > 0 {
					break
				}
				toks = toks[:room]
			}
		}
		for _, tk := range toks {
			if !seen[tk] {
				seen[tk] = true
				out = append(out, tk)
			}
		}
		total += len(toks)
		if limit > 0 && total >= limit {
			break
		}
	}
	return out
}
