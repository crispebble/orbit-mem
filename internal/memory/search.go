package memory

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Doc is the searchable part of a memory.
type Doc struct {
	ID    string
	Title string
	Body  string
	Tags  []string
}

// Rank is a relevance score; Title is the part earned in the title alone.
type Rank struct{ Score, Title float64 }

var termSplit = regexp.MustCompile(`[\s,;|]+`)

// Normalize folds width and case the way the old Knowledge search did (NFKC, lower case).
func Normalize(s string) string { return strings.ToLower(norm.NFKC.String(s)) }

// Terms splits q on whitespace, comma, semicolon and pipe, drops duplicates and keeps at most max.
func Terms(q string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range termSplit.Split(Normalize(q), -1) {
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

func isHangulStart(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return (r >= 0x1100 && r <= 0x11ff) || (r >= 0x3130 && r <= 0x318f) || (r >= 0xac00 && r <= 0xd7a3)
}

// termScore is 1 when term appears in haystack. A Hangul term of three or more
// characters earns 0.5 when a leading part of at least two characters appears,
// so particles such as 은/를/으로 do not hide a memory.
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

func haystack(d Doc) (title, all string) {
	title = Normalize(d.Title)
	parts := []string{title, Normalize(d.Body)}
	for _, t := range d.Tags {
		parts = append(parts, Normalize(t))
	}
	return title, strings.Join(parts, "\n")
}

// Relevance scores d against q (port of the Knowledge relevance rule). An empty
// query matches everything with score 1.
func Relevance(d Doc, q string) Rank {
	list := Terms(q, 16)
	if len(list) == 0 {
		return Rank{Score: 1}
	}
	title, all := haystack(d)
	var r Rank
	for _, t := range list {
		r.Score += termScore(t, all)
		r.Title += termScore(t, title)
	}
	if r.Score > 0 && len(list) > 1 && strings.Contains(all, strings.Join(list, " ")) {
		r.Score++
	}
	return r
}

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`다시 그리고 그냥 이거 저거 그거 이건 그건 해줘 해주세요 주세요 지금 이번 오늘 어떻게
		뭐야 그래서 근데 하나 있어 없어 있는 없는 하고 해서 the and for to of in is it this that please can you`) {
		stopwords[w] = true
	}
}

// promptTerms keeps the words of a user prompt worth matching: two or more
// characters and not a filler word.
func promptTerms(q string) []string {
	var out []string
	for _, t := range Terms(q, 64) {
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

// Match is a strongly matching doc and its scores.
type Match struct {
	Doc          Doc
	Score, Title float64
}

// StrongMatches returns at most n docs that match prompt q strongly (see Strong).
func StrongMatches(docs []Doc, q string, n int) []Doc {
	var out []Doc
	for _, m := range Strong(docs, q) {
		if len(out) == n {
			break
		}
		out = append(out, m.Doc)
	}
	return out
}

// Strong returns the docs that match prompt q strongly, best first: at least
// two prompt words match, one of them fully, at least one in the title, and
// either two in the title or three overall. A weak overlap returns nothing.
func Strong(docs []Doc, q string) []Match {
	terms := promptTerms(q)
	if len(terms) < 2 {
		return nil
	}
	phrase := " " + strings.Join(Terms(q, 64), " ") + " " // 낱말 경계: 「규칙 2」는 「규칙 27」에 맞지 않는다
	var hits []Match
	for _, d := range docs {
		title, all := haystack(d)
		var matched, full, inTitle int
		var score, titleScore float64
		for _, t := range terms {
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
			if t := " " + strings.Join(Terms(d.Title, 64), " ") + " "; utf8.RuneCountInString(t) >= 6 && (strings.Contains(phrase, t) || strings.Contains(t, phrase)) {
				score++
				titleScore++
			}
			hits = append(hits, Match{d, score, titleScore})
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

// PromptTerms are the words of a user prompt the work recall matches.
func PromptTerms(q string) []string { return promptTerms(q) }
