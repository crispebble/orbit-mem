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
	Similar []string // the memories the save stopped at (SaveMeets.Block)
	Near    []string // the close memories beside a save that went on (SaveMeets.Near)
	Found   []string
	Found8  bool     // Find ran
	NotYet  []string // expected memories made after the case's time (timed sets)
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
	// NotYet is how many answered cases expect a memory made after the case's time (sets whose cases carry
	// their times): no recall of that time could show it. They stay in the hit's denominator.
	NotYet int `json:"notYet,omitempty"`
}

// Requests is the measure of the request cases. Chars is the characters of the request
// recall (B1) per request.
type Requests struct {
	Group
	HitByStart    int  `json:"hitByStart"`   // hits that needed layer A
	HitByEarlier  int  `json:"hitByEarlier"` // hits that needed what an earlier request of the session put in (B1)
	HitByNews     int  `json:"hitByNews"`    // hits that needed this request's session news (B3)
	News          News `json:"news"`
	Recover       Rate `json:"recover"`
	RecoverMissed Rate `json:"recoverMissed"`
	B2Chars       Dist `json:"b2Chars"`
	SessionChars  Dist `json:"sessionChars"`
	StartChars    Dist `json:"startChars"`
}

// News is the session news (B3) of the request hooks: the lines that tell a session what changed since its
// bundle. It is not a recall of the request, so it stays out of the wrong recall and the characters per request:
// Requests counts the requests that had news, IDs the ids it named, Expected and Allowed how many of them the case
// expected or allowed, Chars its characters per request that had news.
type News struct {
	Requests int  `json:"requests"`
	IDs      int  `json:"ids"`
	Expected int  `json:"expected"`
	Allowed  int  `json:"allowed"`
	Chars    Dist `json:"chars"`
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
	Cases int `json:"cases"`
	// Hit is the answered cases whose answers are all in the first SimilarLimit of the block and the near ids
	// together (block first).
	Hit Rate `json:"hit"`
	// WrongBlock is the cases with no answer where the block named a memory outside allow: a save stopped for
	// nothing.
	WrongBlock Rate `json:"wrongBlock"`
	// NearOffered is the cases with no answer where the near ids named a memory outside allow: a save that went on
	// with a needless line beside it.
	NearOffered Rate `json:"nearOffered"`
	Forbid      int  `json:"forbid"`     // forbidden ids in the block (top SimilarLimit)
	NearForbid  int  `json:"nearForbid"` // forbidden ids in the near ids (top SimilarLimit)
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

// newsOf is the session news ids of a request step (those of IDs that News names).
func newsOf(s *Shown) []string {
	if s == nil || len(s.News) == 0 {
		return nil
	}
	return intersect(s.IDs, s.News)
}

// newsCharsOf is the characters of the session news, never more than the whole.
func newsCharsOf(s *Shown) int {
	if s == nil {
		return 0
	}
	return min(max(s.NewsChars, 0), s.Chars)
}

func charsOf(s *Shown) int {
	if s == nil {
		return 0
	}
	return s.Chars
}

// groupAcc adds up the cases of a group that shows ids to the model.
type groupAcc struct {
	cases, hitNum, hitDen, injected, correct, neutral, wrong, forbid, forbidCases, emptyNum, emptyDen, notYet int
	chars                                                                                                     []int
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
		Wrong: wrongRate(g.wrong, g.injected), Forbid: g.forbid, ForbidCases: g.forbidCases, Empty: rate(g.emptyNum, g.emptyDen), Chars: dist(g.chars), NotYet: g.notYet}
}

// Compute measures outcomes. Shown is layer A of the session + what the request hooks (B1, B3) of the
// session's earlier requests put in + this request's session news (B3) + the first 3 of its work recall (B1)
// + its B2: a memory is told a session once (설계 L-2), so one put in earlier is still in front of the model,
// and a news line names the memory with its id like a recall line. The wrong recall, the put in, the left
// empty and the characters per request count only what this request recalled (B1 without the news, B2): the
// news is measured apart (News, 1-6 b6r 3차).
func Compute(outs []Outcome) Metrics {
	var req, sub groupAcc
	var subLead []int
	var hitByLead int
	var b2Chars, startChars, sessionChars []int
	var hitByStart, recNum, recDen, missNum, missDen int
	var dupCases, dupHitNum, dupHitDen, dupBlockNum, dupNearNum, dupNoneDen, dupForbid, dupNearForbid int
	sessions := map[string]int{}     // key → characters so far
	earlier := map[string][]string{} // key → what the work recall (B1) of the session's earlier requests put in
	var hitByEarlier, hitByNews int
	var news News
	var newsChars []int
	var order []string
	for _, o := range outs {
		c := o.Case
		switch c.CaseKind() {
		case KindRequest:
			told := newsOf(o.B1)
			recall := minus(idsOf(o.B1), told)
			recallChars := charsOf(o.B1) - newsCharsOf(o.B1)
			injected := union(recall, idsOf(o.B2))
			req.add(c, injected)
			if len(told) > 0 || newsCharsOf(o.B1) > 0 {
				news.Requests++
				news.IDs += len(told)
				for _, id := range told {
					switch {
					case slices.Contains(c.Expect, id):
						news.Expected++
					case slices.Contains(c.Allow, id):
						news.Allowed++
					}
				}
				newsChars = append(newsChars, newsCharsOf(o.B1))
			}
			recalled := union(top(recall, RequestTop), idsOf(o.B2))
			own := union(told, recalled)
			shown := union(o.Start.IDs, earlier[o.Key], own)
			if len(c.Expect) > 0 {
				req.hitDen++
				if len(o.NotYet) > 0 {
					req.notYet++
				}
				hit := covers(shown, c.Expect)
				if hit {
					req.hitNum++
					if !covers(union(earlier[o.Key], own), c.Expect) {
						hitByStart++
					}
					if !covers(union(o.Start.IDs, own), c.Expect) {
						hitByEarlier++
					}
					if !covers(union(o.Start.IDs, earlier[o.Key], recalled), c.Expect) {
						hitByNews++
					}
				}
				if c.Prompt != "" && o.Found8 {
					found := covers(top(o.Found, FindLimit), c.Expect)
					if len(injected) == 0 && recallChars+charsOf(o.B2) == 0 {
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
				if len(injected) == 0 && recallChars+charsOf(o.B2) == 0 {
					req.emptyNum++
				}
			}
			if o.B1 != nil {
				req.chars = append(req.chars, recallChars)
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
			earlier[o.Key] = union(earlier[o.Key], idsOf(o.B1))
		case KindSubagent:
			lead := intersect(idsOf(o.Sub), leadOf(o.Sub))
			recall := minus(idsOf(o.Sub), lead)
			recallChars := charsOf(o.Sub) - leadCharsOf(o.Sub)
			sub.add(c, recall)
			if len(c.Expect) > 0 {
				sub.hitDen++
				if len(o.NotYet) > 0 {
					sub.notYet++
				}
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
			block, near := union(top(o.Similar, SimilarLimit)), union(top(o.Near, SimilarLimit))
			dupForbid += countIn(block, c.Forbid)
			dupNearForbid += countIn(near, c.Forbid)
			if len(c.Expect) > 0 {
				dupHitDen++
				if covers(top(union(block, near), SimilarLimit), c.Expect) {
					dupHitNum++
				}
			} else {
				dupNoneDen++
				if namesOutside(block, c.Allow) {
					dupBlockNum++
				}
				if namesOutside(near, c.Allow) {
					dupNearNum++
				}
			}
		}
	}
	news.Chars = dist(newsChars)
	for _, k := range order {
		sessionChars = append(sessionChars, sessions[k])
	}
	return Metrics{
		Requests: Requests{Group: req.group(), HitByStart: hitByStart, HitByEarlier: hitByEarlier, HitByNews: hitByNews, News: news, Recover: rate(recNum, recDen), RecoverMissed: rate(missNum, missDen),
			B2Chars: dist(b2Chars), SessionChars: dist(sessionChars), StartChars: dist(startChars)},
		Subagent: Subagents{Group: sub.group(), HitByLead: hitByLead, LeadChars: dist(subLead)},
		Duplicates: Duplicates{Cases: dupCases, Hit: rate(dupHitNum, dupHitDen), WrongBlock: rate(dupBlockNum, dupNoneDen),
			NearOffered: rate(dupNearNum, dupNoneDen), Forbid: dupForbid, NearForbid: dupNearForbid},
	}
}

// countIn is how many of ids are in set.
func countIn(ids, set []string) int {
	n := 0
	for _, id := range ids {
		if slices.Contains(set, id) {
			n++
		}
	}
	return n
}

// namesOutside says whether ids has an id not in allow.
func namesOutside(ids, allow []string) bool {
	return slices.ContainsFunc(ids, func(id string) bool { return !slices.Contains(allow, id) })
}
