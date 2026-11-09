// Package metrics turns the recorded events, ledger and proposals
// into the numbers that say whether orbit-mem makes the AI's work better
// (설계 「측정」). A new metric reads a new event kind; nothing else changes.
package metrics

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// Count is a thing and how often it came up.
type Count struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	Query string `json:"query,omitempty"`
	Count int    `json:"count"`
}

// Corrections are the user's corrections the daily retrospectives found.
type Corrections struct {
	Repeated    int     `json:"repeated"`    // 같은 내용의 기억이 이미 있었다
	Injected    int     `json:"injected"`    // 그 기억을 넣었는데 어겼다
	NotInjected int     `json:"notInjected"` // 그 기억을 그 세션에 넣지 못했다
	Unknown     int     `json:"unknown"`     // 그 세션의 주입 기록이 없다
	New         int     `json:"new"`         // 기억이 없던 교정
	Top         []Count `json:"top,omitempty"`
}

// Prompts are the per-request injections (the work recall).
type Prompts struct {
	Total    int     `json:"total"`
	Hits     int     `json:"hits"`
	HitRate  float64 `json:"hitRate"`
	AvgChars int     `json:"avgChars"` // 기억을 넣은 요청의 평균 글자 수
}

// Usage are the session-end retrospectives' verdicts on injected memories.
type Usage struct {
	Followed      int     `json:"followed"`
	Violated      int     `json:"violated"`
	Irrelevant    int     `json:"irrelevant"`
	TopViolated   []Count `json:"topViolated,omitempty"`
	TopIrrelevant []Count `json:"topIrrelevant,omitempty"`
}

// Writes are the retrospectives' write verdicts and the user's answers.
type Writes struct {
	Keep         int `json:"keep"`
	Hold         int `json:"hold"`
	Reject       int `json:"reject"`
	UserRejected int `json:"userRejected"`
	UserEdited   int `json:"userEdited"`
}

// Find is memory_find use.
type Find struct {
	Total        int     `json:"total"`
	Empty        int     `json:"empty"`
	EmptyRate    float64 `json:"emptyRate"`
	EmptyQueries []Count `json:"emptyQueries,omitempty"`
}

// Saves is memory_save use.
type Saves struct {
	Total int `json:"total"`
	Quote int `json:"quote"`
	Other int `json:"other"`
}

// SessionStart is the session-start injection.
type SessionStart struct {
	Sessions     int `json:"sessions"` // 서로 다른 세션 수
	Starts       int `json:"starts"`   // 시작 훅 호출 수(재개·압축 포함)
	AvgChars     int `json:"avgChars"`
	TitleOnlyMax int `json:"titleOnlyMax"` // 0.10.0까지: 제목만 넣은 규칙 수
	OmittedMax   int `json:"omittedMax"`   // 0.11.0부터: 길이 때문에 세션 시작에 넣지 못한 규칙 수
}

// Proposals are decisions in the period and what still waits now.
type Proposals struct {
	Created           int `json:"created"`
	Approved          int `json:"approved"`
	Edited            int `json:"edited"`
	Rejected          int `json:"rejected"`
	Pending           int `json:"pending"`
	OldestPendingDays int `json:"oldestPendingDays"`
}

// Sources are the checks of memories whose source file changed: how many
// memory and file pairs were caught, how each was settled, and how many
// proposals came of it.
type Sources struct {
	Detected  int `json:"detected"`
	Proposals int `json:"proposals"`
	Kept      int `json:"kept"`   // 발췌가 그대로
	Absent    int `json:"absent"` // 파일이 없어짐
	Gone      int `json:"gone"`   // 발췌가 사라짐
	OK        int `json:"ok"`     // 모델 판정: 맞음
	Edit      int `json:"edit"`   // 모델 판정: 고쳐야 함
	Stale     int `json:"stale"`  // 모델 판정: 낡음
}

// Report is the metrics of a period of days (inclusive).
type Report struct {
	From         string               `json:"from"`
	To           string               `json:"to"`
	Corrections  Corrections          `json:"corrections"`
	Prompts      Prompts              `json:"prompts"`
	Find         Find                 `json:"find"`
	Saves        Saves                `json:"saves"`
	SessionStart SessionStart         `json:"sessionStart"`
	Proposals    Proposals            `json:"proposals"`
	Sources      Sources              `json:"sources"`
	Tools        map[string]int       `json:"tools"`
	Usage        Usage                `json:"usage"`
	Ledger       memory.LedgerSummary `json:"ledger"`
	Writes       Writes               `json:"writes"`
	Graph        Graph                `json:"graph"`
	States       map[string]int       `json:"states"` // 지금 기억 수(상태별)
	Legacy       memory.LegacyCount   `json:"legacy"` // 옮겨 온 옛 기억 판정(설계 · Orbit 3차 O3-3)
	// 옛 기억 정리를 시작하기 전 상태별 기억 수(끝 기준 3의 「전」). 시작하지 않았으면 nil
	LegacyBaseline *memory.LegacyBaseline `json:"legacyBaseline,omitempty"`
}

// Graph is the memory graph as it stands now (설계 · Orbit 3차 O3-2 「클러스터 다시 재기」):
// the graph's default states split by representative tags, each newly
// covering at least 1% of them (memory.ClusterMinNew).
type Graph struct {
	Memories  int     `json:"memories"`
	Clusters  int     `json:"clusters"` // 「그 밖」 빼고
	Other     int     `json:"other"`
	OtherRate float64 `json:"otherRate"`
	Untagged  int     `json:"untagged"` // 주제 태그가 하나도 없는 기억
	Links     int     `json:"links"`    // 그래프 안 기억끼리의 살아 있는 명시 연결
	TopicTags int     `json:"topicTags"`
}

func graphNow(s *memory.Store) (Graph, error) {
	ms, err := s.List(memory.StateCandidate, memory.StateConfirmed, memory.StateStale) // 숨김(거절한 후보)은 뺀다
	if err != nil {
		return Graph{}, err
	}
	links, err := s.Links()
	if err != nil {
		return Graph{}, err
	}
	c := memory.Clusters(ms, memory.ClusterMinNew(len(ms)))
	g := Graph{Memories: len(ms), Clusters: len(c.Picked), Other: c.Other, OtherRate: rate(c.Other, len(ms)), Untagged: c.Untagged,
		TopicTags: len(memory.TopicTagCounts(ms))}
	in := make(map[string]bool, len(ms))
	for _, m := range ms {
		in[m.ID] = true
	}
	for _, l := range links {
		if in[l.A] && in[l.B] {
			g.Links++
		}
	}
	return g, nil
}

const topN = 5

func rate(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}

func dayStart(day string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", day, time.Local)
	return t
}

func top(m map[string]int, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// Compute reads the store for the days from..to (inclusive, local time).
func Compute(s *memory.Store, from, to string) (Report, error) {
	r := Report{From: from, To: to, Tools: map[string]int{}}
	evs, err := s.Events(memory.EventFilter{FromDay: from, ToDay: to})
	if err != nil {
		return r, err
	}
	corrected := map[string]int{}
	sessions := map[string]bool{}
	emptyQ := map[string]int{}
	violated, irrelevant := map[string]int{}, map[string]int{}
	chars, promptChars := 0, 0
	for _, e := range evs {
		var d struct {
			MemoryIDs  []string `json:"memoryIds"`
			Chars      int      `json:"chars"`
			TitleOnly  int      `json:"titleOnly"`
			Omitted    int      `json:"omitted"`
			OK         *bool    `json:"ok"`
			Results    int      `json:"results"`
			Query      string   `json:"query"`
			SourceKind string   `json:"sourceKind"`
			MemoryID   string   `json:"memoryId"`
			Injected   string   `json:"injected"`
			Decision   string   `json:"decision"`
			Verdict    string   `json:"verdict"`
			Outcome    string   `json:"outcome"`
			Memories   int      `json:"memories"`
			Proposed   bool     `json:"proposed"`
		}
		_ = json.Unmarshal(e.Data, &d)
		ok := d.OK == nil || *d.OK
		switch {
		case e.Kind == memory.EventInjectSessionStart:
			r.SessionStart.Starts++
			if e.Session == "" || !sessions[e.Session] {
				r.SessionStart.Sessions++
				sessions[e.Session] = true
			}
			chars += d.Chars
			r.SessionStart.TitleOnlyMax = max(r.SessionStart.TitleOnlyMax, d.TitleOnly)
			r.SessionStart.OmittedMax = max(r.SessionStart.OmittedMax, d.Omitted)
		case e.Kind == memory.EventInjectPrompt:
			r.Prompts.Total++
			if len(d.MemoryIDs) > 0 {
				r.Prompts.Hits++
				promptChars += d.Chars
			}
		case e.Kind == memory.EventMemoryUsed:
			switch d.Verdict {
			case memory.UsageFollowed:
				r.Usage.Followed++
			case memory.UsageViolated:
				r.Usage.Violated++
				violated[d.MemoryID]++
			case memory.UsageIrrelevant:
				r.Usage.Irrelevant++
				irrelevant[d.MemoryID]++
			}
		case strings.HasPrefix(e.Kind, memory.EventToolPrefix):
			name := strings.TrimPrefix(e.Kind, memory.EventToolPrefix)
			r.Tools[name]++
			if !ok {
				continue
			}
			switch name {
			case "memory_find":
				r.Find.Total++
				if d.Results == 0 {
					r.Find.Empty++
					emptyQ[d.Query]++
				}
			case "memory_save":
				r.Saves.Total++
				if d.SourceKind == memory.SourceQuote {
					r.Saves.Quote++
				} else {
					r.Saves.Other++
				}
			}
		case e.Kind == memory.EventSourceCheck:
			src := &r.Sources
			src.Detected += d.Memories
			if d.Proposed {
				src.Proposals++
			}
			switch d.Outcome {
			case memory.SourceKept:
				src.Kept += d.Memories
			case memory.SourceAbsent:
				src.Absent += d.Memories
			case memory.SourceGone:
				src.Gone += d.Memories
			case memory.SourceOK:
				src.OK += d.Memories
			case memory.SourceEdit:
				src.Edit += d.Memories
			case memory.SourceStale:
				src.Stale += d.Memories
			}
		case e.Kind == memory.EventProposalDecided:
			switch d.Decision {
			case "approve":
				r.Proposals.Approved++
			case "edit":
				r.Proposals.Edited++
			case "reject":
				r.Proposals.Rejected++
			}
		case e.Kind == memory.EventCorrection:
			if d.MemoryID == "" {
				r.Corrections.New++
				continue
			}
			r.Corrections.Repeated++
			corrected[d.MemoryID]++
			switch d.Injected {
			case "yes":
				r.Corrections.Injected++
			case "no":
				r.Corrections.NotInjected++
			default:
				r.Corrections.Unknown++
			}
		}
	}
	if r.SessionStart.Starts > 0 {
		r.SessionStart.AvgChars = chars / r.SessionStart.Starts
	}
	r.Prompts.HitRate = rate(r.Prompts.Hits, r.Prompts.Total)
	if r.Prompts.Hits > 0 {
		r.Prompts.AvgChars = promptChars / r.Prompts.Hits
	}
	r.Usage.TopViolated = topMemories(s, violated)
	r.Usage.TopIrrelevant = topMemories(s, irrelevant)
	r.Find.EmptyRate = rate(r.Find.Empty, r.Find.Total)
	for _, q := range top(emptyQ, topN) {
		r.Find.EmptyQueries = append(r.Find.EmptyQueries, Count{Query: q, Count: emptyQ[q]})
	}
	r.Corrections.Top = topMemories(s, corrected)

	startMs := dayStart(from).UnixMilli()
	endMs := dayStart(to).AddDate(0, 0, 1).UnixMilli()
	db := s.DB()
	if err := db.QueryRow(`SELECT COUNT(*) FROM proposals WHERE created_at>=? AND created_at<?`, startMs, endMs).Scan(&r.Proposals.Created); err != nil {
		return r, err
	}
	var oldest int64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(created_at),0) FROM proposals WHERE status=?`, memory.StatusPending).Scan(&r.Proposals.Pending, &oldest); err != nil {
		return r, err
	}
	if oldest > 0 {
		r.Proposals.OldestPendingDays = int(s.Now().Sub(time.UnixMilli(oldest)).Hours() / 24)
	}
	if r.Ledger, err = s.SummarizeLedger(startMs, endMs); err != nil {
		return r, err
	}
	if r.Graph, err = graphNow(s); err != nil {
		return r, err
	}
	if r.States, err = s.StateCounts(); err != nil {
		return r, err
	}
	if r.Legacy, err = s.CountLegacy(); err != nil {
		return r, err
	}
	if b, ok := s.LegacyBaseline(); ok {
		r.LegacyBaseline = &b
	}
	jc, err := s.JudgmentCounts(startMs, endMs)
	if err != nil {
		return r, err
	}
	r.Writes = Writes{Keep: jc[memory.VerdictKeep], Hold: jc[memory.VerdictHold], Reject: jc[memory.VerdictReject], UserRejected: jc["user_reject"], UserEdited: jc["user_edit"]}
	return r, nil
}

func topMemories(s *memory.Store, counts map[string]int) []Count {
	var out []Count
	for _, id := range top(counts, topN) {
		c := Count{ID: id, Count: counts[id]}
		if m, err := s.Get(id); err == nil {
			c.Title = m.Title
		}
		out = append(out, c)
	}
	return out
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func titled(lg i18n.Lang, cs []Count) string {
	var parts []string
	for _, c := range cs {
		title := c.Title
		if title == "" {
			title = i18n.TIn(lg, "metrics.no_memory")
		}
		parts = append(parts, i18n.TIn(lg, "metrics.title_count", title, c.ID, c.Count))
	}
	return strings.Join(parts, ", ")
}

// Render is the report as short text in the current language.
func Render(r Report) string { return RenderIn(r, i18n.Current()) }

// RenderIn is the report in a fixed language, for text the AI reads (the
// weekly retrospective material stays Korean whatever the user picked).
func RenderIn(r Report, lg i18n.Lang) string {
	var b strings.Builder
	b.WriteString(i18n.TIn(lg, "metrics.period", r.From, r.To) + "\n\n")
	c := r.Corrections
	b.WriteString(i18n.TIn(lg, "metrics.corrections", c.Repeated, c.Injected, c.NotInjected, c.Unknown, c.New) + "\n")
	for _, t := range c.Top {
		title := t.Title
		if title == "" {
			title = i18n.TIn(lg, "metrics.no_memory")
		}
		b.WriteString("  " + i18n.TIn(lg, "metrics.top_item", title, t.ID, t.Count) + "\n")
	}
	b.WriteString(i18n.TIn(lg, "metrics.prompts", r.Prompts.Hits, r.Prompts.Total, pct(r.Prompts.HitRate), r.Prompts.AvgChars) + "\n")
	u := r.Usage
	b.WriteString(i18n.TIn(lg, "metrics.usage", u.Followed, u.Violated, u.Irrelevant) + "\n")
	if len(u.TopViolated) > 0 {
		b.WriteString("  " + i18n.TIn(lg, "metrics.usage_violated", titled(lg, u.TopViolated)) + "\n")
	}
	if len(u.TopIrrelevant) > 0 {
		b.WriteString("  " + i18n.TIn(lg, "metrics.usage_irrelevant", titled(lg, u.TopIrrelevant)) + "\n")
	}
	b.WriteString(i18n.TIn(lg, "metrics.find", r.Find.Empty, r.Find.Total, pct(r.Find.EmptyRate)))
	if len(r.Find.EmptyQueries) > 0 {
		var qs []string
		for _, q := range r.Find.EmptyQueries {
			qs = append(qs, i18n.TIn(lg, "metrics.query_count", q.Query, q.Count))
		}
		b.WriteString(": " + strings.Join(qs, ", "))
	}
	b.WriteString("\n")
	b.WriteString(i18n.TIn(lg, "metrics.saves", r.Saves.Total, r.Saves.Quote, r.Saves.Other) + "\n")
	b.WriteString(i18n.TIn(lg, "metrics.session_start", r.SessionStart.Sessions, r.SessionStart.Starts, r.SessionStart.AvgChars, r.SessionStart.TitleOnlyMax, r.SessionStart.OmittedMax) + "\n")
	p := r.Proposals
	b.WriteString(i18n.TIn(lg, "metrics.proposals", p.Created, p.Approved, p.Edited, p.Rejected, p.Pending, p.OldestPendingDays) + "\n")
	sc := r.Sources
	b.WriteString(i18n.TIn(lg, "metrics.sources", sc.Detected, sc.Proposals, sc.Kept, sc.Absent, sc.Gone, sc.OK, sc.Edit, sc.Stale) + "\n")
	w := r.Writes
	b.WriteString(i18n.TIn(lg, "metrics.writes", w.Keep, w.Hold, w.Reject, w.UserRejected, w.UserEdited) + "\n")
	l := r.Ledger
	b.WriteString(i18n.TIn(lg, "metrics.ledger", l.Changes,
		l.Judged[memory.VerdictHelped], l.Judged[memory.VerdictHarmful], l.Judged[memory.VerdictNoEffect], l.Judged[memory.VerdictMixed], l.Evidence, l.Pending) + "\n")
	g := r.Graph
	b.WriteString(i18n.TIn(lg, "metrics.graph", g.Memories, g.Clusters, g.Other, pct(g.OtherRate), g.Untagged, g.Links, g.TopicTags) + "\n")
	states := func(m map[string]int) string {
		return i18n.TIn(lg, "metrics.states", m[memory.StateCandidate], m[memory.StateConfirmed], m[memory.StateStale], m[memory.StateHidden])
	}
	b.WriteString(i18n.TIn(lg, "metrics.states_line", states(r.States)))
	if lb := r.LegacyBaseline; lb != nil {
		b.WriteString(" " + i18n.TIn(lg, "metrics.legacy_baseline", lb.Day, states(lb.States)))
	}
	b.WriteString("\n")
	if l := r.Legacy; l.Total > 0 {
		var by []string
		for _, v := range []struct{ key, name string }{
			{"keep", i18n.TIn(lg, "metrics.legacy_keep")}, {"confirm", i18n.TIn(lg, "metrics.legacy_confirm")},
			{"merge", i18n.TIn(lg, "metrics.legacy_merge")}, {"stale", i18n.TIn(lg, "metrics.legacy_stale")}} {
			if n := l.By[v.key]; n > 0 {
				by = append(by, i18n.TIn(lg, "metrics.legacy_by", v.name, n))
			}
		}
		b.WriteString(i18n.TIn(lg, "metrics.legacy", l.Total, l.Judged))
		if len(by) > 0 {
			b.WriteString("(" + strings.Join(by, " · ") + ")")
		}
		b.WriteString(i18n.TIn(lg, "metrics.legacy_left", l.Left) + "\n")
	}
	return b.String()
}
