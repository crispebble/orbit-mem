package memory

import (
	"regexp"
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
