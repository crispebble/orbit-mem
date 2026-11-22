package inject

import (
	"math"
	"sort"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

// rankPolicy is the rank rule of every recall (명세 R-1·R-3): the request, the delegated task and the tool
// recall all order their hits by it. byScore is the settings' rankByScore, margin its rankScoreMargin.
type rankPolicy struct {
	byScore bool
	margin  float64
}

// rankPolicyOf reads the rank rule from the settings, the default margin where they carry none.
func rankPolicyOf(cfg config.Config) rankPolicy {
	return rankPolicy{byScore: cfg.RankByScore, margin: orDefault(cfg.RankScoreMargin, config.DefaultRankScoreMargin)}
}

// rankKey is what the first three steps of the rank rule read of one hit. score is 0 for a hit with none
// (a tool call with no input).
type rankKey struct {
	score     float64
	confirmed bool
	reviewDue bool
}

// scoreEps keeps the rounding error of two equal floating-point scores from deciding an order or a margin.
const scoreEps = 1e-9

// before is the first three steps of the order, best first: (1) with byScore, a score gap larger than the
// margin puts the higher score first; (2) a confirmed memory comes before a candidate; (3) with equal scores
// and equal confirmation a memory past its review day comes after (명세 R-3). decided is false when none of
// them tells a from b; the caller's own tail breaks that tie.
//
// With byScore off step 1 is skipped: that is the order before the rank rule existed (confirmed first, then
// score in the tail). The score gap is measured against the margin with scoreEps, so a gap equal to the margin
// is no larger than it.
func (p rankPolicy) before(a, b rankKey) (first, decided bool) {
	if d := a.score - b.score; p.byScore && math.Abs(d) > p.margin+scoreEps {
		return d > 0, true
	}
	if a.confirmed != b.confirmed {
		return a.confirmed, true
	}
	if math.Abs(a.score-b.score) <= scoreEps && a.reviewDue != b.reviewDue {
		return !a.reviewDue, true
	}
	return false, false
}

// rankHits orders the hits of a recall, best first (see recallHits): the rank rule (rankPolicy.before), then
// find score, share of the request's words, this place's own before the user's (S3 명세 2.8), age (newer
// first), net usage (followed − irrelevant) over the ledger window, last edit, id. v judges which memories
// are past their review day.
func rankHits(s *memory.Store, cfg config.Config, v memory.Viewer, pl place.Place, prompt string, hits []memory.RecallHit) []memory.RecallHit {
	if len(hits) == 0 {
		return nil
	}
	now := s.Now()
	usage, _ := s.UsageCounts(memory.Day(now.AddDate(0, 0, -max(cfg.LedgerWindowDays, 1))), memory.Day(now))
	net := func(id string) int { u := usage[id]; return u.Followed - u.Irrelevant }
	products := pl.Named(prompt)
	local := func(m memory.Memory) bool { return pl.Local(m.Scope, m.Project, products) }
	key := func(x float64) float64 { return math.Round(x * 1e9) } // 같은 값의 부동소수점 오차가 순위를 가르지 않게
	policy := rankPolicyOf(cfg)
	due := make(map[string]bool, len(hits))
	for _, h := range hits {
		due[h.Memory.ID] = v.Of(h.Memory).ReviewDue
	}
	rk := func(h memory.RecallHit) rankKey {
		return rankKey{score: h.Score, confirmed: h.Memory.State == memory.StateConfirmed, reviewDue: due[h.Memory.ID]}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, c := hits[i].Memory, hits[j].Memory
		if first, decided := policy.before(rk(hits[i]), rk(hits[j])); decided {
			return first
		}
		if sa, sc := key(hits[i].Score), key(hits[j].Score); sa != sc {
			return sa > sc
		}
		if va, vc := key(hits[i].Coverage), key(hits[j].Coverage); va != vc {
			return va > vc
		}
		if la, lc := local(a), local(c); la != lc {
			return la
		}
		if wa, wc := memory.AgeWeight(a, now), memory.AgeWeight(c, now); wa != wc {
			return wa > wc
		}
		if na, nc := net(a.ID), net(c.ID); na != nc {
			return na > nc
		}
		if a.UpdatedAt != c.UpdatedAt {
			return a.UpdatedAt > c.UpdatedAt
		}
		return a.ID < c.ID
	})
	return hits
}
