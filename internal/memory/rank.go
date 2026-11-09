package memory

import (
	"math"
	"strings"
	"unicode/utf8"
)

// 찾기 점수(설계 7절, ORBIT-25, 결정 D4).
//
// FTS5 색인은 후보를 찾는 데만 쓴다. 점수는 Go가 후보 전체(색인된 기억과 훑은 늦은 기억)에 같은 함수로 매긴다.
// FTS5의 bm25()는 쓰지 않는다: IDF 바닥(1e-6) 때문에 기억이 한두 건이면 모든 점수가 한 값으로 무너지고,
// 결과 안에서 최소·최대로 맞추면 1건은 늘 1, 2건은 1과 0이 된다.
//
// 질의 토큰 t마다 기억 d의 일치 q(t,d)를 칸별로 보고 가장 큰 값을 쓴다.
//
//	q(t,d) = max_칸 m(t,칸) × (B + (1−B) × w(칸))
//	m = 1(같은 토큰) · prefixMatch(접두로만 맞음, 라틴 낱말과 한 글자) · 0
//	w = 제목 1, 주제·태그 2/3, 본문 1/3 (제목 3 : 주제·태그 2 : 본문 1), B = exactFloor
//
// 질의 토큰의 무게는 BM25의 음수 없는 IDF 꼴로, d 자신은 세지 않는다(leave-one-out). 그래야 기억이 1건일 때도 「다른 기억
// 대비 얼마나 드문가」가 정의되고, 무관한 기억이 늘어도 같은 기억의 점수가 흔들리지 않는다.
//
//	N′ = (기억 수 − 1) + idfPrior,  n′ = (t에 맞는 기억 수) − [d가 t에 맞음]
//	idf(t,d) = ln(1 + (N′ − n′ + 0.5) / (n′ + 0.5))  (n′ ≤ N′라 음수가 되지 않는다)
//
// 점수는 이상적인 기억(모든 질의 토큰이 제목에 그대로 있는 기억, q = 1)의 점수로 나눈 0~1 값이다.
//
//	score(d) = Σ_t idf(t,d) × q(t,d) / Σ_t idf(t,d)
//
// 같은 내용을 다시 질의하면 모든 토큰이 어느 칸에든 그대로 있어 B + (1−B)/3 이상(0.9 이상)이 된다. 낱말 하나만
// 겹치면 그 토큰의 무게 몫만 받는다. 기억이 많아 흔한 토큰은 idf가 작아 몫이 줄어든다.

const (
	// findTokenLimit은 질의 토큰 상한이다(넘는 뒤 어절은 버린다). OR 토큰 수백 개면 FTS가 느려진다.
	findTokenLimit = 60
	// exactFloor(B)는 어느 칸에서든 그대로 맞은 토큰이 받는 바닥 몫이다. 남은 몫이 칸 무게를 가른다.
	exactFloor = 0.86
	// prefixMatch는 접두로만 맞은 토큰(deploy → deployment)의 몫이다.
	prefixMatch = 0.5
	// idfPrior는 IDF에 더하는 가상 기억 수다. 기억이 몇 건뿐일 때 「다른 기억 하나에 있다」만으로 토큰이
	// 흔한 낱말처럼 취급되지 않게 한다. 기억이 많아지면 영향이 사라진다.
	idfPrior = 10
)

// fieldWeight는 칸(제목, 주제·태그, 본문)의 무게를 제목 1로 맞춘 값이다.
var fieldWeight = [3]float64{1, 2.0 / 3, 1.0 / 3}

// queryToken은 점수용 질의 토큰이다. prefix면 그 글로 시작하는 토큰에도 낮게 맞는다.
type queryToken struct {
	text   string
	prefix bool
}

// scoreTokens는 질의를 점수용 토큰으로 바꾼다(중복 없음, 상한 findTokenLimit).
func scoreTokens(q string) []queryToken {
	toks := queryTokens(q, findTokenLimit)
	out := make([]queryToken, len(toks))
	for i, t := range toks {
		out[i] = queryToken{t, prefixable(t)}
	}
	return out
}

// prefixable은 접두로도 잡는 토큰인지다: 라틴·숫자 낱말과 한 글자. 한·중·일 바이그램은 그대로만 맞는다.
func prefixable(t string) bool {
	r, _ := utf8.DecodeRuneInString(t)
	return kindOf(r) == kindOther || utf8.RuneCountInString(t) == 1
}

// candidateMatch는 후보를 찾는 FTS5 MATCH 글이다. 토큰 하나라도 맞는 기억이 모두 후보다. 어절의 바이그램을
// 구로 묶지 않는다: 「배포」로 저장하고 「배포를」로 찾을 때 「포를」이 없어도 후보가 돼야 한다.
func candidateMatch(qs []queryToken) string {
	parts := make([]string, len(qs))
	for i, t := range qs {
		parts[i] = quoteFTS(t.text)
		if t.prefix {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, " OR ")
}

// fieldTokens는 기억의 칸별 색인 토큰이다: 제목, 주제·태그, 본문.
type fieldTokens [3][]string

// tokensOf는 기억 글을 색인과 같은 가공으로 칸별 토큰으로 만든다.
func tokensOf(title, topic, tags, body string) fieldTokens {
	ti, ke, bo := indexFields(title, topic, tags, body)
	return fieldTokens{strings.Fields(ti), strings.Fields(ke), strings.Fields(bo)}
}

func tokenMatch(t queryToken, toks []string) float64 {
	best := 0.0
	for _, x := range toks {
		if x == t.text {
			return 1
		}
		if t.prefix && strings.HasPrefix(x, t.text) {
			best = prefixMatch
		}
	}
	return best
}

// matches는 질의 토큰마다 q(t,d)다. 하나도 맞지 않으면 false.
func matches(qs []queryToken, f fieldTokens) ([]float64, bool) {
	v := make([]float64, len(qs))
	hit := false
	for i, t := range qs {
		for k := range f {
			if m := tokenMatch(t, f[k]); m > 0 {
				v[i] = math.Max(v[i], m*(exactFloor+(1-exactFloor)*fieldWeight[k]))
			}
		}
		hit = hit || v[i] > 0
	}
	return v, hit
}

// ranker는 한 질의의 후보와 그 통계(기억 수, 토큰마다 맞는 기억 수)다.
type ranker struct {
	qs    []queryToken
	total int
	df    []int
	docs  map[string][]float64
}

func newRanker(qs []queryToken) *ranker {
	return &ranker{qs: qs, df: make([]int, len(qs)), docs: map[string][]float64{}}
}

// add는 기억 하나를 본다. 한 토큰도 맞지 않으면 후보가 아니다. 같은 기억은 한 번만 센다.
func (r *ranker) add(id string, f fieldTokens) {
	if _, ok := r.docs[id]; ok {
		return
	}
	v, ok := matches(r.qs, f)
	if !ok {
		return
	}
	r.docs[id] = v
	for i, x := range v {
		if x > 0 {
			r.df[i]++
		}
	}
}

// score는 후보 하나의 0~1 점수다.
func (r *ranker) score(v []float64) float64 {
	total := max(r.total, len(r.docs))
	others := float64(total-1) + idfPrior
	var num, den float64
	for i, q := range v {
		n := r.df[i]
		if q > 0 {
			n--
		}
		idf := math.Log(1 + (others-float64(n)+0.5)/(float64(n)+0.5))
		num += idf * q
		den += idf
	}
	if den <= 0 {
		return 0
	}
	return math.Min(1, num/den)
}

// scores는 후보마다 점수다.
func (r *ranker) scores() map[string]float64 {
	out := make(map[string]float64, len(r.docs))
	for id, v := range r.docs {
		out[id] = r.score(v)
	}
	return out
}

// ftsCandidatesSQL은 색인이 지금 글과 맞는 후보다. 늦은 기억(표시가 있거나 색인 글이 낡은 기억)은 빼고 따로 훑는다.
// MATCH가 바깥 고리이고 나머지는 rowid·기본 키로 찾으므로 후보 수에 비례한다.
const ftsCandidatesSQL = `SELECT i.memory_id, f.title, f.keys, f.body FROM memory_fts f
	JOIN memory_index i ON i.rowid = f.rowid JOIN memories m ON m.id = i.memory_id
	WHERE memory_fts MATCH ? AND i.indexed_at = CAST(m.updated_at AS TEXT)
	AND NOT EXISTS (SELECT 1 FROM memory_index_late l WHERE l.memory_id = i.memory_id)`

// lateScanSQL은 늦은 표시가 있는 기억의 지금 글이다(CROSS JOIN: 표시가 바깥 고리라 늦은 줄 수에 비례한다).
const lateScanSQL = `SELECT m.id, m.title, m.topic, m.tags, m.body FROM memory_index_late l CROSS JOIN memories m ON m.id = l.memory_id`

// staleScanSQL은 전체 검사(staleSQL)가 늦다고 본 기억의 지금 글이다: 색인 줄 수가 어긋날 때(index.go 개수 비교 보호
// 장치)만 쓴다. 표시가 있는 기억을 포함하고, ftsCandidatesSQL의 후보(색인 줄·FTS 줄이 있고 글이 맞고 표시가 없음)와
// 겹치지 않는다. 기억 전부를 읽는다.
const staleScanSQL = `SELECT id, title, topic, tags, body FROM memories WHERE id IN (` + staleSQL + `)`

// queryScores는 질의 토큰이 닿는 모든 기억(상태·범위와 무관)의 점수다. 후보는 FTS 색인과, 늦은 표시가 있는 기억을
// 지금 글로 훑은 것을 합친다. 색인 줄 수가 어긋나면(표시 없이 깨진 색인) 늦은 표시 대신 전체 검사의 늦은 줄을 훑는다.
// 색인 표나 표시 표가 없는 DB(판 4 사본이나 표시 전 판 5 사본으로 복구한 직후)는 모두 훑는다. q의 한 읽기
// 트랜잭션에서 읽기만 한다. step은 첫 읽기 뒤에 부른다(시험용, nil이면 없음).
func queryScores(q querier, qs []queryToken, step func()) (map[string]float64, error) {
	r := newRanker(qs)
	scan := lateScanSQL
	// 줄 수 비교가 기억 수(IDF의 N)도 센다. COUNT(*)는 b-tree 쪽만 걷고 줄을 풀지 않는다. 트리거로 센 값을 두지 않는
	// 것은 INSERT OR REPLACE가 지우기 트리거 없이 줄을 바꿔 값이 어긋날 수 있어서다.
	total, agree, err := indexCounts(q)
	switch {
	case noIndexTable(err):
		scan = `SELECT id, title, topic, tags, body FROM memories`
		if err := q.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&total); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if !agree {
			scan = staleScanSQL
		}
		rows, err := q.Query(ftsCandidatesSQL, candidateMatch(qs))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, ti, ke, bo string
			if err := rows.Scan(&id, &ti, &ke, &bo); err != nil {
				rows.Close()
				return nil, err
			}
			r.add(id, fieldTokens{strings.Fields(ti), strings.Fields(ke), strings.Fields(bo)})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	r.total = total
	if step != nil {
		step()
	}
	rows, err := q.Query(scan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, topic, tags, body string
		if err := rows.Scan(&id, &title, &topic, &tags, &body); err != nil {
			return nil, err
		}
		r.add(id, tokensOf(title, topic, tags, body))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return r.scores(), nil
}
