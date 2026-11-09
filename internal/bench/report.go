package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Report is the result of a run. It holds only structs and slices, so the JSON is the
// same bytes every time for the same input.
type Report struct {
	About string      `json:"about,omitempty"`
	Now   string      `json:"now"`
	Seed  string      `json:"seed"`
	Cases int         `json:"cases"`
	Split SplitInfo   `json:"split"`
	Runs  []RunReport `json:"runs"`
}

// SplitInfo says how big the test part is and how much one case moves a rate.
type SplitInfo struct {
	Units         int     `json:"units"`
	TrainUnits    int     `json:"trainUnits"`
	TestUnits     int     `json:"testUnits"`
	Test          Bundles `json:"test"`
	PointsPerCase Points  `json:"pointsPerCase"` // percentage points one test case is worth in its rate (0: no such case)
}

// Points is the percentage points of one case for each bundle of the test part.
type Points struct {
	Answered  float64 `json:"answered"`
	Empty     float64 `json:"empty"`
	Subagent  float64 `json:"subagent"`
	Duplicate float64 `json:"duplicate"`
}

func points(b Bundles) Points {
	p := func(n int) float64 {
		if n == 0 {
			return 0
		}
		return round4(100 / float64(n))
	}
	return Points{Answered: p(b.Answered), Empty: p(b.Empty), Subagent: p(b.Subagent), Duplicate: p(b.Duplicate)}
}

// RunReport is the result of one recall path.
type RunReport struct {
	Name     string          `json:"name"`
	Train    Metrics         `json:"train"`
	Test     Metrics         `json:"test"`
	All      Metrics         `json:"all"`
	Sessions []SessionResult `json:"sessions"`
	Cases    []CaseResult    `json:"cases"`
}

// Seen is what a step showed: the ids in order and the characters.
type Seen struct {
	IDs   []string `json:"ids"`
	Chars int      `json:"chars"`
}

func seen(s Shown) Seen { return Seen{IDs: nonNil(s.IDs), Chars: s.Chars} }

func seenPtr(s *Shown) *Seen {
	if s == nil {
		return nil
	}
	v := seen(*s)
	return &v
}

// SessionResult is the session start (layer A) of one session.
type SessionResult struct {
	Session string `json:"session"`
	Seen
}

// CaseResult is what one case was shown.
type CaseResult struct {
	ID      string   `json:"id"`
	Session string   `json:"session"`
	Kind    string   `json:"kind"`
	Part    string   `json:"part"`
	Request *Seen    `json:"request,omitempty"`
	Tool    *Seen    `json:"tool,omitempty"`
	Sub     *Seen    `json:"subagent,omitempty"`
	Similar []string `json:"similar,omitempty"`
	Found   []string `json:"found,omitempty"`
}

func caseResult(o Outcome) CaseResult {
	return CaseResult{ID: o.Case.ID, Session: o.Key, Kind: o.Case.CaseKind(), Part: o.Part,
		Request: seenPtr(o.B1), Tool: seenPtr(o.B2), Sub: seenPtr(o.Sub), Similar: o.Similar, Found: o.Found}
}

// JSON is the report as indented JSON with a final newline.
func (r *Report) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Wording is the text of the report in the reader's language: one method a line, the numbers
// (rates already as "num/den value%") handed in. The caller implements it with its message
// catalog; the domain holds only the layout and the numbers.
type Wording interface {
	Part(part string) string // name of PartTrain, PartTest or PartAll
	Header(about, now, seed string) string
	Units(cases, units, train, test int) string
	TestSize(b Bundles, p Points) string
	Requests(n int) string
	Hit(rate string, byStart int) string
	Wrong(rate string, injected, correct, neutral, wrong, forbid, forbidCases int) string
	Recover(rate, everyMissed string) string
	Empty(rate string) string
	CharsRequest(dist string) string
	CharsTool(dist string) string
	CharsSession(dist string) string
	CharsStart(dist string) string
	Subagent(cases int, hit, wrong string, forbid int, empty, chars string) string
	Duplicate(cases int, hit, offered string, forbid int) string
	ZeroDen() string
	Dist(mean, median float64, max, n int) string
}

// partOrder is the order of the metric blocks.
var partOrder = []string{PartTrain, PartTest, PartAll}

func pct(w Wording, r Rate) string {
	if r.Value == nil {
		return fmt.Sprintf("%d/%d -", r.Num, r.Den)
	}
	s := fmt.Sprintf("%d/%d %.1f%%", r.Num, r.Den, *r.Value*100)
	if r.ZeroDen {
		s += w.ZeroDen()
	}
	return s
}

func distText(w Wording, d Dist) string {
	if d.N == 0 {
		return "-"
	}
	return w.Dist(d.Mean, d.Median, d.Max, d.N)
}

// Text is the report for a person, numbers first. part picks the metric block of every recall
// path: PartTrain or PartTest shows that part alone, PartAll shows the train, test and all blocks.
func (r *Report) Text(part string, w Wording) string {
	var b strings.Builder
	s := r.Split
	for _, line := range []string{w.Header(r.About, r.Now, r.Seed), w.Units(r.Cases, s.Units, s.TrainUnits, s.TestUnits), w.TestSize(s.Test, s.PointsPerCase)} {
		b.WriteString(line + "\n")
	}
	for _, run := range r.Runs {
		fmt.Fprintf(&b, "\n== %s ==\n", run.Name)
		m := map[string]Metrics{PartTrain: run.Train, PartTest: run.Test, PartAll: run.All}
		for _, p := range partOrder {
			if part == PartAll || part == p {
				metricsText(&b, w, p, m[p])
			}
		}
	}
	return b.String()
}

func metricsText(b *strings.Builder, w Wording, part string, m Metrics) {
	q, g, d := m.Requests, m.Subagent, m.Duplicates
	fmt.Fprintf(b, "[%s] %s\n", w.Part(part), w.Requests(q.Cases))
	for _, line := range []string{
		w.Hit(pct(w, q.Hit), q.HitByStart),
		w.Wrong(pct(w, q.Wrong), q.Injected, q.Correct, q.Neutral, q.Injected-q.Correct-q.Neutral, q.Forbid, q.ForbidCases),
		w.Recover(pct(w, q.Recover), pct(w, q.RecoverMissed)),
		w.Empty(pct(w, q.Empty)),
		w.CharsRequest(distText(w, q.Chars)),
		w.CharsTool(distText(w, q.B2Chars)),
		w.CharsSession(distText(w, q.SessionChars)),
		w.CharsStart(distText(w, q.StartChars)),
		w.Subagent(g.Cases, pct(w, g.Hit), pct(w, g.Wrong), g.Forbid, pct(w, g.Empty), distText(w, g.Chars)),
		w.Duplicate(d.Cases, pct(w, d.Hit), pct(w, d.Offered), d.Forbid),
	} {
		b.WriteString("  " + line + "\n")
	}
}
