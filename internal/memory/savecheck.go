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
// 막지 않는 새 기억 판정에는 같은 후보 가운데 비슷함이 가까운 하한(CheckMins.Near) 이상인 것을 Near로 준다(1-6 b6dn):
// 막는 문턱은 엄격하게 두고, 바꿔 쓴 같은 주장처럼 문턱 밑에 머무는 중복은 저장한 뒤 부른 쪽이 보고 합치게 한다. 글 꼴은
// 부르는 쪽(어댑터)이 정한다.

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
// 1-6 b6d 2차에 낱말 단위 기하 평균(similar.go)과 함께 정했다. 개발 자료(internal/bench/testdata의 public·holdout·
// holdout2 중복 사례)에서 0.42~0.45가 같은 적중을 내고 0.48부터 holdout2의 낱말 바꾼 짧은 제목을 놓친다: 그 가운데
// 높은 쪽이다. 시험 쪽 값: 바꿔 쓴 것 최저 0.463(similar_test.go 낱말 둘 바꿈), L-16 변형 최저 0.667, 다른 기억 최고
// 0.333(머리 낱말 하나만 겹침, 짧은 기억이 긴 글에 다 들어 있음 0.324).
const DefaultSimilarMin = 0.45

// DefaultNearMin is the score a memory needs to be shown as close beside a save that goes on, when the caller
// gives no floor. 1-6 b6dn에 정했다: 개발 자료 넷(internal/bench/testdata의 public·holdout·holdout2·holdout3_dup 중복
// 사례)에서 (막음 ∪ 가까운) 상위 3의 적중이 묶음마다 옛 판(87ab0fb find-proxy) 이상인 가장 높은 하한이다. 0.24부터
// holdout3_dup의 바꿔 쓴 같은 주장(0.239)을 놓쳐 옛 판 아래로 내려간다.
const DefaultNearMin = 0.22

// CheckMins are the scores of SaveCheck. Similar is the score that stops a save (0 or less: DefaultSimilarMin),
// Near the floor of the close memories shown beside a save that goes on (0 or less: DefaultNearMin).
type CheckMins struct {
	Similar float64
	Near    float64
}

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
	Near     []Similar // new: 막지 않고 저장할 때 보일 가까운 기억, 점수 순
}

// SaveCheck says, only reading, what saving in would meet: the same title (Save adds the source to that
// memory), the current value of the same scope and topic (Save supersedes it), or else the candidate and
// confirmed memories of the same scope whose similarity to the new one (similar.go) is mins.Similar or more, best
// SimilarLimit first. When none is, the save goes on and Near holds, the same way, the memories of mins.Near or
// more: close ones the caller may merge or fix after it. An input Save refuses is refused with the same error.
func (s *Store) SaveCheck(in SaveInput, mins CheckMins) (SaveCheckResult, error) {
	if err := in.validate(); err != nil { // in은 값 복사본이다: 부르는 쪽 입력은 그대로
		return SaveCheckResult{}, err
	}
	min := mins.Similar
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
	near := mins.Near
	if near <= 0 {
		near = DefaultNearMin
	}
	n := memoryTokens(in.Title, in.Topic, in.Tags, in.Body)
	r := SaveCheckResult{Verdict: CheckNew}
	for _, h := range hits {
		m := h.Memory
		sc := similarity(n, memoryTokens(m.Title, m.Topic, m.Tags, m.Body))
		if sc < min && sc < near {
			continue
		}
		sim := Similar{ID: m.ID, Kind: m.Kind, Title: m.Title, Summary: summaryOf(m.Body), Topic: m.Topic, Score: sc, Trust: m.Trust}
		if sc >= min {
			r.Similar = append(r.Similar, sim)
		} else {
			r.Near = append(r.Near, sim)
		}
	}
	if len(r.Similar) > 0 { // 막는 저장에는 가까운 기억을 붙이지 않는다: 고르기(Similar)가 그 자리다
		r.Verdict, r.Similar, r.Near = CheckSimilar, bestSimilar(r.Similar), nil
		return r, nil
	}
	r.Near = bestSimilar(r.Near)
	return r, nil
}

// bestSimilar is list ranked by score, the first SimilarLimit. Equal scores keep the find order.
func bestSimilar(list []Similar) []Similar {
	// 비교용으로 반올림한다: 같은 값의 부동소수점 오차가 순서를 가르지 않게(FindScored와 같다).
	slices.SortStableFunc(list, func(a, b Similar) int { return cmp.Compare(math.Round(b.Score*1e9), math.Round(a.Score*1e9)) })
	if len(list) > SimilarLimit {
		list = list[:SimilarLimit]
	}
	return list
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
