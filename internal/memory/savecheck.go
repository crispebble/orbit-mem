package memory

import (
	"cmp"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
)

// 저장 전 판정(설계 6.1, T-26 · ORBIT-25 G10, 계획 D8). 저장을 받는 쪽(MCP·host call 처리기)이 저장하기 전에
// 부르는 읽기 전용 판정이다. Save가 하는 순서 그대로 같은 제목 단축 → 같은 주제 대체를 먼저 보고, 둘 다 아니면
// 같은 범위(scope)의 후보·확정 기억 가운데 새 기억과의 비슷함(similar.go)이 문턱 이상인 것을 위에서 SimilarLimit개 고른다.
// 내부 Save(정리·나누기·들여오기·계획 적용이 부름)는 이 판정을 하지 않는다: 넣으면 그 흐름이 모두 바뀐다(T-26).
// 비슷한 기억은 종류(kind)를 가리지 않는다: 같은 내용이 규칙·결정·사실로 갈라지는 중복도 보이게 한다.

// Verdicts of SaveCheck.
const (
	CheckNew       = "new"        // 비슷한 것이 없다: 바로 저장한다
	CheckSameTitle = "same-title" // Save가 Existing[0]에 출처와 날을 더한다(같은 종류·범위·제목)
	CheckSameTopic = "same-topic" // Save가 Existing(같은 범위·주제의 현재값)을 대체한다
	CheckSimilar   = "similar"    // 같은 범위에 비슷한 기억(Similar)이 있다: 저장하지 않고 고르게 한다
)

// SimilarLimit is how many similar memories SaveCheck returns at most.
const SimilarLimit = 3

// DefaultSimilarMin is the score a memory needs to count as similar when the caller gives no threshold.
// Measured on rewordings and on different memories (savecheck_test.go scoreCases, the L-16 duplicate set
// in internal/mcpserver/testdata): the evaluation set of 1-6 tunes it again.
const DefaultSimilarMin = 0.61

// similarSummaryRunes is the most characters of a similar memory's summary.
const similarSummaryRunes = 200

// Similar is a memory like the one about to be saved.
type Similar struct {
	ID      string
	Kind    string
	Title   string
	Summary string // 본문 한 줄, similarSummaryRunes자 이하
	Topic   string
	Score   float64 // 0~1
	Trust   string  // 기억의 등급(출처 가운데 가장 높은 것, trust.go)
}

// SaveCheckResult is what SaveCheck found.
type SaveCheckResult struct {
	Verdict  string
	Existing []string  // same-title: 출처를 더할 기억 하나, same-topic: 대체할 현재값
	Similar  []Similar // similar: 점수 순
}

// SaveCheck says, only reading, what saving in would meet: the same title (Save adds the source to that
// memory), the current value of the same scope and topic (Save supersedes it), or else the candidate and
// confirmed memories of the same scope whose similarity to the new one (similar.go) is min or more, best SimilarLimit
// first. min <= 0 is DefaultSimilarMin. An input Save refuses is refused with the same error.
func (s *Store) SaveCheck(in SaveInput, min float64) (SaveCheckResult, error) {
	if err := in.validate(); err != nil { // in은 값 복사본이다: 부르는 쪽 입력은 그대로
		return SaveCheckResult{}, err
	}
	if min <= 0 {
		min = DefaultSimilarMin
	}
	if in.LegacyID == "" { // 들여오기는 Save가 단축하지 않는다
		var id string
		err := s.db.QueryRow(`SELECT id FROM memories WHERE kind=? AND scope=? AND title_key=? AND state IN ('candidate','confirmed','hidden') ORDER BY created_at LIMIT 1`,
			in.Kind, in.Scope, TitleKey(in.Title)).Scan(&id)
		switch {
		case err == nil:
			return SaveCheckResult{Verdict: CheckSameTitle, Existing: []string{id}}, nil
		case !errors.Is(err, sql.ErrNoRows):
			return SaveCheckResult{}, err
		}
	}
	if in.Topic != "" && in.Retention != RetentionVolatile { // supersedeTx와 같은 조건
		ids, err := s.topicValues(in.Scope, in.Topic)
		if err != nil {
			return SaveCheckResult{}, err
		}
		if len(ids) > 0 {
			return SaveCheckResult{Verdict: CheckSameTopic, Existing: ids}, nil
		}
	}
	// 후보는 찾기(rank.go)가 정한다: 같은 범위의 후보·확정 기억 가운데 새 글의 토큰(앞 findTokenLimit개)이 하나라도 닿는
	// 것 모두. 점수는 두 기억 사이 비슷함(similar.go)이고, 같은 점수면 찾기 순서(찾기 점수 → 확정 → 최신)를 지킨다.
	hits, err := s.FindScored(FindInput{Query: checkQuery(in), Scope: in.Scope, Limit: math.MaxInt})
	if err != nil {
		return SaveCheckResult{}, err
	}
	n := memoryTokens(in.Title, in.Topic, in.Tags, in.Body)
	r := SaveCheckResult{Verdict: CheckNew}
	for _, h := range hits {
		m := h.Memory
		if sc := similarity(n, memoryTokens(m.Title, m.Topic, m.Tags, m.Body)); sc >= min {
			r.Similar = append(r.Similar, Similar{ID: m.ID, Kind: m.Kind, Title: m.Title, Summary: summaryOf(m.Body),
				Topic: m.Topic, Score: sc, Trust: m.Trust})
		}
	}
	// 비교용으로 반올림한다: 같은 값의 부동소수점 오차가 순서를 가르지 않게(FindScored와 같다).
	slices.SortStableFunc(r.Similar, func(a, b Similar) int { return cmp.Compare(math.Round(b.Score*1e9), math.Round(a.Score*1e9)) })
	if len(r.Similar) > SimilarLimit {
		r.Similar = r.Similar[:SimilarLimit]
	}
	if len(r.Similar) > 0 {
		r.Verdict = CheckSimilar
	}
	return r, nil
}

// topicValues are the current (candidate, confirmed) memories of scope and topic, oldest first.
func (s *Store) topicValues(scope, topic string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM memories WHERE scope=? AND topic=? AND state IN ('candidate','confirmed') ORDER BY created_at, id`, scope, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// checkQuery is the find query of a memory about to be saved: title, topic, tags and body, in that order
// (the query keeps its first tokens when it is long, rank.go).
func checkQuery(in SaveInput) string {
	parts := append([]string{in.Title, in.Topic}, in.Tags...)
	return strings.Join(append(parts, in.Body), " ")
}

// summaryOf is a body on one line, at most similarSummaryRunes characters with the ellipsis.
func summaryOf(body string) string {
	r := []rune(strings.Join(strings.Fields(body), " "))
	if len(r) <= similarSummaryRunes {
		return string(r)
	}
	return string(r[:similarSummaryRunes-1]) + "…"
}
