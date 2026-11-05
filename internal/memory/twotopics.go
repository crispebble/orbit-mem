package memory

// 두 주제 제목 검사(S4-12): 한 기억에 주장 하나(설계 · orbit-mem 2판 4.3.1). 제목이 「A — B」 꼴이고 앞뒤가 다른 제품을
// 말하거나 앞뒤에 같은 낱말이 하나도 없으면 두 주장이 한 기억에 붙은 것으로 보고 감사 목록에 보인다. 고치기(나누기)는
// AI가 계획의 split으로 한다. 같은 주제를 풀어 쓴 꼬리(뒤가 한 낱말이거나 앞과 낱말을 나눔)는 보이지 않는다.

import (
	"regexp"
	"strings"
)

// Why a title looks like two topics.
const (
	TwoTopicsProducts     = "products"       // 앞뒤가 다른 제품을 말한다
	TwoTopicsNoSharedWord = "no shared word" // 앞뒤에 같은 낱말이 없다
)

// TwoTopicTitle is a rule or decision whose 「A — B」 title glues two topics.
type TwoTopicTitle struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Scope string `json:"scope"`
	Why   string `json:"why"`
	A     string `json:"a"`
	B     string `json:"b"`
}

var trailingParen = regexp.MustCompile(`\s*\([^()]*\)\s*$`)

// TwoTopicTitles lists the live rules and decisions whose title glues two topics; products are the
// names of the product map.
func (s *Store) TwoTopicTitles(products []string) ([]TwoTopicTitle, error) {
	rows, err := s.db.Query(`SELECT id,kind,title,scope FROM memories WHERE kind IN (?,?) AND state IN ('candidate','confirmed') AND title LIKE '% — %' ORDER BY created_at, id`,
		KindRule, KindDecision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TwoTopicTitle{}
	for rows.Next() {
		var t TwoTopicTitle
		if err := rows.Scan(&t.ID, &t.Kind, &t.Title, &t.Scope); err != nil {
			return nil, err
		}
		a, b, _ := strings.Cut(t.Title, " — ")
		t.A, t.B = strings.TrimSpace(a), strings.TrimSpace(trailingParen.ReplaceAllString(b, ""))
		if t.Why = twoTopics(t.A, t.B, products); t.Why != "" {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

func twoTopics(a, b string, products []string) string {
	ta, tb := promptTerms(a), promptTerms(b)
	if len(ta) == 0 || len(tb) < 2 {
		return "" // 뒤가 한 낱말이면 앞을 꾸미는 꼬리다
	}
	na, nb := Normalize(a), Normalize(b)
	pa, pb := namedProducts(na, products), namedProducts(nb, products)
	if len(pa) > 0 && len(pb) > 0 {
		for p := range pb {
			if pa[p] {
				return ""
			}
		}
		return TwoTopicsProducts
	}
	for _, t := range ta {
		if termScore(t, nb) > 0 {
			return ""
		}
	}
	for _, t := range tb {
		if termScore(t, na) > 0 {
			return ""
		}
	}
	return TwoTopicsNoSharedWord
}

var wordEdge = regexp.MustCompile(`[\p{L}\p{N}-]`)

// namedProducts are the product names that appear in text as whole words.
func namedProducts(text string, products []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range products {
		p = Normalize(p)
		for i := 0; p != ""; {
			j := strings.Index(text[i:], p)
			if j < 0 {
				break
			}
			j += i
			before, after := "", ""
			if j > 0 {
				before = text[j-1 : j]
			}
			if k := j + len(p); k < len(text) {
				after = text[k : k+1]
			}
			if !wordEdge.MatchString(before) && !wordEdge.MatchString(after) {
				out[p] = true
				break
			}
			i = j + len(p)
		}
	}
	return out
}
