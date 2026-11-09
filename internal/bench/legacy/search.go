package legacy

// A copy of the strong match of internal/memory/search.go at 87ab0fb: how a request picked the
// memories of the work recall.

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// doc is internal/memory/search.go:13-18 at 87ab0fb (Doc).
type doc struct {
	ID    string
	Title string
	Body  string
	Tags  []string
}

// docOf is internal/memory/model.go:98 at 87ab0fb (Memory.Doc).
func docOf(m memory.Memory) doc { return doc{ID: m.ID, Title: m.Title, Body: m.Body, Tags: m.Tags} }

// termSplit is internal/memory/search.go:23 at 87ab0fb.
var termSplit = regexp.MustCompile(`[\s,;|]+`)

// normalize is internal/memory/search.go:26 at 87ab0fb (Normalize).
func normalize(s string) string { return strings.ToLower(norm.NFKC.String(s)) }

// terms is internal/memory/search.go:29-43 at 87ab0fb (Terms).
func terms(q string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range termSplit.Split(normalize(q), -1) {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == max {
			break
		}
	}
	return out
}

// isHangulStart is internal/memory/search.go:45-48 at 87ab0fb.
func isHangulStart(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return (r >= 0x1100 && r <= 0x11ff) || (r >= 0x3130 && r <= 0x318f) || (r >= 0xac00 && r <= 0xd7a3)
}

// termScore is internal/memory/search.go:53-67 at 87ab0fb.
func termScore(term, haystack string) float64 {
	if strings.Contains(haystack, term) {
		return 1
	}
	runes := []rune(term)
	if len(runes) < 3 || !isHangulStart(term) {
		return 0
	}
	for n := len(runes) - 1; n >= 2; n-- {
		if strings.Contains(haystack, string(runes[:n])) {
			return 0.5
		}
	}
	return 0
}

// haystack is internal/memory/search.go:69-76 at 87ab0fb.
func haystack(d doc) (title, all string) {
	title = normalize(d.Title)
	parts := []string{title, normalize(d.Body)}
	for _, t := range d.Tags {
		parts = append(parts, normalize(t))
	}
	return title, strings.Join(parts, "\n")
}

// stopwords is internal/memory/search.go:97-104 at 87ab0fb.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`다시 그리고 그냥 이거 저거 그거 이건 그건 해줘 해주세요 주세요 지금 이번 오늘 어떻게
		뭐야 그래서 근데 하나 있어 없어 있는 없는 하고 해서 the and for to of in is it this that please can you`) {
		stopwords[w] = true
	}
}

// promptTerms is internal/memory/search.go:108-120 at 87ab0fb.
func promptTerms(q string) []string {
	var out []string
	for _, t := range terms(q, 64) {
		if utf8.RuneCountInString(t) < 2 || stopwords[t] {
			continue
		}
		out = append(out, t)
		if len(out) == 40 {
			break
		}
	}
	return out
}

// match is internal/memory/search.go:123-126 at 87ab0fb (Match).
type match struct {
	Doc          doc
	Score, Title float64
}

// strong is internal/memory/search.go:143-194 at 87ab0fb (Strong): the docs that match prompt q strongly,
// best first: at least two prompt words match, one of them fully, at least one in the title, and either
// two in the title or three overall.
func strong(docs []doc, q string) []match {
	list := promptTerms(q)
	if len(list) < 2 {
		return nil
	}
	phrase := " " + strings.Join(terms(q, 64), " ") + " " // 낱말 경계: 「규칙 2」는 「규칙 27」에 맞지 않는다
	var hits []match
	for _, d := range docs {
		title, all := haystack(d)
		var matched, full, inTitle int
		var score, titleScore float64
		for _, t := range list {
			s := termScore(t, all)
			if s == 0 {
				continue
			}
			matched++
			score += s
			if s == 1 {
				full++
			}
			if ts := termScore(t, title); ts > 0 {
				inTitle++
				titleScore += ts
			}
		}
		if full >= 1 && matched >= 2 && inTitle >= 1 && (inTitle >= 2 || matched >= 3) {
			// 요청에 제목이 그대로 나오거나 요청 전체가 제목에 나오면 한 점을 더한다. 한 글자 낱말(「규칙 7」의 7)은
			// 낱말 점수에서 빠지므로, 이것이 없으면 제목만 다른 기억들이 같은 점수가 된다.
			if t := " " + strings.Join(terms(d.Title, 64), " ") + " "; utf8.RuneCountInString(t) >= 6 && (strings.Contains(phrase, t) || strings.Contains(t, phrase)) {
				score++
				titleScore++
			}
			hits = append(hits, match{d, score, titleScore})
		}
	}
	// 같은 점수끼리는 제목 점수, 제목, id 순이다. 입력 순서(무작위 id)에 결과가 걸리지 않게 끝까지 정한다.
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Title != b.Title {
			return a.Title > b.Title
		}
		if a.Doc.Title != b.Doc.Title {
			return a.Doc.Title < b.Doc.Title
		}
		return a.Doc.ID < b.Doc.ID
	})
	return hits
}
