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
	// rule is set for a confirmed rule: the tool recall (beforeTool) keeps it ahead of every memory that is not one.
	rule bool
}

// scoreEps keeps the rounding error of two equal floating-point scores from deciding an order or a margin.
const scoreEps = 1e-9

// before is the first three steps of the order, best first: (1) with byScore, a score gap larger than the
// margin puts the higher score first; (2) a confirmed memory comes before a candidate; (3) with equal scores
// and equal confirmation a memory past its review day comes after (명세 R-3). decided is false when none of
// them tells a from b; the caller's own tail breaks that tie.
//
// With byScore off step 1 is skipped, so the order is confirmed first and then score (in the tail), as before
// the rank rule existed, except that step 3 always applies: a memory past its review day comes after an
// otherwise equal one whatever the settings say.
//
// The order is total: with byScore it is the descending order of "score, plus the margin when confirmed" (a
// score gap above the margin decides, inside it the confirmed one is ahead, and a gap of exactly the margin
// counts as inside), then step 3 on equal scores. The gap is measured with scoreEps while the callers' tails
// compare scores rounded to 1e-9, so two scores that straddle a rounding boundary 1e-9 apart may be ordered by
// step 3 rather than by the tail; that is below any score a find gives.
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

// beforeTool is before for the tool recall: a confirmed rule comes before every memory that is not one, whatever
// their scores. A confirmed rule is brought by the tool's name alone (명세 T-2), so a memory that merely bears
// on the input (a lesson, fact or decision, or a candidate) must not push it out of the two lines a call has;
// among confirmed rules, and among the others, the order is before's. The request and the delegated recall
// have no such class: step 1 applies to a confirmed memory there too. Total, as the classes are ordered first.
func (p rankPolicy) beforeTool(a, b rankKey) (first, decided bool) {
	if a.rule != b.rule {
		return a.rule, true
	}
	return p.before(a, b)
}

// rankHits orders the hits of a recall, best first (see recallHits): the rank rule (rankPolicy.before), then
// find score, share of the request's words, this place's own before the user's (S3 명세 2.8), age (newer
// first), net usage (followed − irrelevant) over the ledger window, last edit, id. A memory is past its review
// day by its own review date and the clock (Memory.Freshness), which is a single comparison, so rankHits makes
// no Viewer of its own; v is the caller's judge, kept so the visibility rules can join the rank without a new
// signature (the request recall, which has none at hand, passes the zero Viewer, which this does not read).
func rankHits(s *memory.Store, cfg config.Config, _ memory.Viewer, pl place.Place, prompt string, hits []memory.RecallHit) []memory.RecallHit {
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
	rk := func(h memory.RecallHit) rankKey {
		return rankKey{score: h.Score, confirmed: h.Memory.State == memory.StateConfirmed, reviewDue: h.Memory.Freshness(now) == memory.FreshReviewDue}
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
