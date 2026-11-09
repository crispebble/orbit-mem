package bench

import (
	"math"
	"slices"
)

// Rate is a count over a count. Value is nil when the denominator is 0 (no case to
// judge by), except for the wrong recall, which is 0 then and says so with ZeroDen.
type Rate struct {
	Num     int      `json:"num"`
	Den     int      `json:"den"`
	Value   *float64 `json:"value"`
	ZeroDen bool     `json:"zeroDen,omitempty"`
}

// Dist is the spread of a count of characters.
type Dist struct {
	N      int     `json:"n"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	Max    int     `json:"max"`
}

// Outcome is what one case showed.
type Outcome struct {
	Case    Case
	Key     string // the session key the case is grouped by
	Part    string
	Start   Shown // layer A of the session (the case has none for a duplicate)
	B1      *Shown
	B2      *Shown
	Sub     *Shown
	Similar []string
	Found   []string
	Found8  bool // Find ran
}

// Group is the measure of a group of cases that show something to the model.
type Group struct {
	Cases       int  `json:"cases"`
	Hit         Rate `json:"hit"`
	Injected    int  `json:"injected"`
	Correct     int  `json:"correct"`
	Neutral     int  `json:"neutral"`
	Wrong       Rate `json:"wrong"`
	Forbid      int  `json:"forbid"`
	ForbidCases int  `json:"forbidCases"`
	Empty       Rate `json:"empty"`
	Chars       Dist `json:"chars"`
}

// Requests is the measure of the request cases. Chars is the characters of the request
// recall (B1) per request.
type Requests struct {
	Group
	HitByStart    int  `json:"hitByStart"` // hits that needed layer A
	Recover       Rate `json:"recover"`
	RecoverMissed Rate `json:"recoverMissed"`
	B2Chars       Dist `json:"b2Chars"`
	SessionChars  Dist `json:"sessionChars"`
	StartChars    Dist `json:"startChars"`
}

// Subagents is the measure of the delegation cases. A delegation bundle is the always-on title lines
// (Lead) and the recall for the task: the hit counts both, and HitByLead tells the hits the title lines
// gave. The wrong recall, the left empty and Chars measure the recall share alone; LeadChars is the
// characters of the title lines.
type Subagents struct {
	Group
	HitByLead int  `json:"hitByLead"`
	LeadChars Dist `json:"leadChars"`
}

// Duplicates is the measure of the duplicate save cases.
type Duplicates struct {
	Cases   int  `json:"cases"`
	Hit     Rate `json:"hit"`
	Offered Rate `json:"offered"` // cases with nothing similar where something was offered
	Forbid  int  `json:"forbid"`
}

// Metrics is the measure of a part of the set.
type Metrics struct {
	Requests   Requests   `json:"requests"`
	Subagent   Subagents  `json:"subagent"`
	Duplicates Duplicates `json:"duplicates"`
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func rate(num, den int) Rate {
	r := Rate{Num: num, Den: den}
	if den > 0 {
		v := round4(float64(num) / float64(den))
		r.Value = &v
	}
	return r
}

// wrongRate is a rate that is 0 (and says so) when nothing was shown.
func wrongRate(num, den int) Rate {
	r := rate(num, den)
	if den == 0 {
		zero := 0.0
		r.Value, r.ZeroDen = &zero, true
	}
	return r
}

func dist(v []int) Dist {
	if len(v) == 0 {
		return Dist{}
	}
	s := slices.Clone(v)
	slices.Sort(s)
	sum := 0
	for _, x := range s {
		sum += x
	}
	med := float64(s[len(s)/2])
	if len(s)%2 == 0 {
		med = float64(s[len(s)/2-1]+s[len(s)/2]) / 2
	}
	return Dist{N: len(s), Mean: round4(float64(sum) / float64(len(s))), Median: round4(med), Max: s[len(s)-1]}
}

// union keeps each id once, in order.
func union(lists ...[]string) []string {
	out := []string{}
	for _, l := range lists {
		for _, id := range l {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

func covers(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

func top(ids []string, n int) []string {
	if len(ids) > n {
		return ids[:n]
	}
	return ids
}

func idsOf(s *Shown) []string {
	if s == nil {
		return nil
	}
	return s.IDs
}

func leadOf(s *Shown) []string {
	if s == nil {
		return nil
	}
	return s.Lead
}

// leadCharsOf is the characters of the title lines, never more than the whole.
func leadCharsOf(s *Shown) int {
	if s == nil {
		return 0
	}
	return min(max(s.LeadChars, 0), s.Chars)
}

// intersect keeps the ids of a that b has.
func intersect(a, b []string) []string {
	out := []string{}
	for _, id := range a {
		if slices.Contains(b, id) {
			out = append(out, id)
		}
	}
	return out
}

// minus keeps the ids of a that b does not have.
func minus(a, b []string) []string {
	out := []string{}
	for _, id := range a {
		if !slices.Contains(b, id) {
			out = append(out, id)
		}
	}
	return out
}

func charsOf(s *Shown) int {
	if s == nil {
		return 0
	}
	return s.Chars
}

// groupAcc adds up the cases of a group that shows ids to the model.
type groupAcc struct {
	cases, hitNum, hitDen, injected, correct, neutral, wrong, forbid, forbidCases, emptyNum, emptyDen int
	chars                                                                                             []int
}

// add counts the ids a case put in (without layer A) against its answer.
func (g *groupAcc) add(c Case, injected []string) {
	g.cases++
	g.injected += len(injected)
	f := 0
	for _, id := range injected {
		switch {
		case slices.Contains(c.Expect, id):
			g.correct++
		case slices.Contains(c.Allow, id):
			g.neutral++
		default:
			g.wrong++
		}
		if slices.Contains(c.Forbid, id) {
			f++
		}
	}
	g.forbid += f
	if f > 0 {
		g.forbidCases++
	}
}

func (g *groupAcc) group() Group {
	return Group{Cases: g.cases, Hit: rate(g.hitNum, g.hitDen), Injected: g.injected, Correct: g.correct, Neutral: g.neutral,
		Wrong: wrongRate(g.wrong, g.injected), Forbid: g.forbid, ForbidCases: g.forbidCases, Empty: rate(g.emptyNum, g.emptyDen), Chars: dist(g.chars)}
}

// Compute measures outcomes (see the plan: shown = layer A + the first 3 of B1 + B2).
func Compute(outs []Outcome) Metrics {
	var req, sub groupAcc
	var subLead []int
	var hitByLead int
	var b2Chars, startChars, sessionChars []int
	var hitByStart, recNum, recDen, missNum, missDen int
	var dupCases, dupHitNum, dupHitDen, dupOfferNum, dupOfferDen, dupForbid int
	sessions := map[string]int{} // key → characters so far
	var order []string
	for _, o := range outs {
		c := o.Case
		switch c.CaseKind() {
		case KindRequest:
			injected := union(idsOf(o.B1), idsOf(o.B2))
			req.add(c, injected)
			shown := union(o.Start.IDs, top(idsOf(o.B1), RequestTop), idsOf(o.B2))
			if len(c.Expect) > 0 {
				req.hitDen++
				hit := covers(shown, c.Expect)
				if hit {
					req.hitNum++
					if !covers(union(top(idsOf(o.B1), RequestTop), idsOf(o.B2)), c.Expect) {
						hitByStart++
					}
				}
				if c.Prompt != "" && o.Found8 {
					found := covers(top(o.Found, FindLimit), c.Expect)
					if len(injected) == 0 && charsOf(o.B1)+charsOf(o.B2) == 0 {
						recDen++
						if found {
							recNum++
						}
					}
					if !hit {
						missDen++
						if found {
							missNum++
						}
					}
				}
			} else {
				req.emptyDen++
				if charsOf(o.B1)+charsOf(o.B2) == 0 {
					req.emptyNum++
				}
			}
			if o.B1 != nil {
				req.chars = append(req.chars, o.B1.Chars)
			}
			if o.B2 != nil {
				b2Chars = append(b2Chars, o.B2.Chars)
			}
			if _, ok := sessions[o.Key]; !ok {
				order = append(order, o.Key)
				sessions[o.Key] = o.Start.Chars
				startChars = append(startChars, o.Start.Chars)
			}
			sessions[o.Key] += charsOf(o.B1) + charsOf(o.B2)
		case KindSubagent:
			lead := intersect(idsOf(o.Sub), leadOf(o.Sub))
			recall := minus(idsOf(o.Sub), lead)
			recallChars := charsOf(o.Sub) - leadCharsOf(o.Sub)
			sub.add(c, recall)
			if len(c.Expect) > 0 {
				sub.hitDen++
				if covers(union(lead, recall), c.Expect) {
					sub.hitNum++
					if !covers(recall, c.Expect) {
						hitByLead++
					}
				}
			} else {
				sub.emptyDen++
				if len(recall) == 0 && recallChars == 0 {
					sub.emptyNum++
				}
			}
			sub.chars = append(sub.chars, recallChars)
			subLead = append(subLead, leadCharsOf(o.Sub))
		case KindDuplicate:
			dupCases++
			similar := union(top(o.Similar, SimilarLimit))
			for _, id := range similar {
				if slices.Contains(c.Forbid, id) {
					dupForbid++
				}
			}
			if len(c.Expect) > 0 {
				dupHitDen++
				if covers(similar, c.Expect) {
					dupHitNum++
				}
			} else {
				dupOfferDen++
				for _, id := range similar {
					if !slices.Contains(c.Allow, id) {
						dupOfferNum++
						break
					}
				}
			}
		}
	}
	for _, k := range order {
		sessionChars = append(sessionChars, sessions[k])
	}
	return Metrics{
		Requests: Requests{Group: req.group(), HitByStart: hitByStart, Recover: rate(recNum, recDen), RecoverMissed: rate(missNum, missDen),
			B2Chars: dist(b2Chars), SessionChars: dist(sessionChars), StartChars: dist(startChars)},
		Subagent:   Subagents{Group: sub.group(), HitByLead: hitByLead, LeadChars: dist(subLead)},
		Duplicates: Duplicates{Cases: dupCases, Hit: rate(dupHitNum, dupHitDen), Offered: rate(dupOfferNum, dupOfferDen), Forbid: dupForbid},
	}
}
