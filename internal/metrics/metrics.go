// Package metrics turns the recorded events, retrospectives and proposals
// into the numbers that say whether Orbit makes the AI's work better
// (설계 「측정」). A new metric reads a new event kind; nothing else changes.
package metrics

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onidot-labs/orbit/internal/memory"
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
	TitleOnlyMax int `json:"titleOnlyMax"`
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

// Retros are the retrospectives started in the period.
type Retros struct {
	Runs       int                  `json:"runs"`
	Done       int                  `json:"done"`
	Empty      int                  `json:"empty"`
	Failed     int                  `json:"failed"`
	AvgSeconds int                  `json:"avgSeconds"`
	Kinds      map[string]RetroKind `json:"kinds"`
	Cost       Cost                 `json:"cost"`
}

// RetroKind counts the retrospectives of one kind (session, daily, weekly).
type RetroKind struct {
	Runs   int `json:"runs"`
	Done   int `json:"done"`
	Empty  int `json:"empty"`
	Failed int `json:"failed"`
}

// Cost is what the retrospectives' host runs reported. USD is the host's own
// estimate (Claude Code); runs that report nothing count as unknown.
type Cost struct {
	USD          float64 `json:"usd"`
	SessionUSD   float64 `json:"sessionUsd"`
	Known        int     `json:"known"`
	Unknown      int     `json:"unknown"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
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
	Retros       Retros               `json:"retros"`
	Tools        map[string]int       `json:"tools"`
	Usage        Usage                `json:"usage"`
	Ledger       memory.LedgerSummary `json:"ledger"`
	Writes       Writes               `json:"writes"`
	Graph        Graph                `json:"graph"`
	States       map[string]int       `json:"states"` // 지금 기억 수(상태별)
	Legacy       memory.LegacyCount   `json:"legacy"` // 옮겨 온 옛 기억 판정(설계 · Orbit 3차 O3-3)
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
	ms, err := s.List(memory.DefaultGraphStates...)
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
	r := Report{From: from, To: to, Tools: map[string]int{}, Retros: Retros{Kinds: map[string]RetroKind{}}}
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
			OK         *bool    `json:"ok"`
			Results    int      `json:"results"`
			Query      string   `json:"query"`
			SourceKind string   `json:"sourceKind"`
			MemoryID   string   `json:"memoryId"`
			Injected   string   `json:"injected"`
			Decision   string   `json:"decision"`
			Verdict    string   `json:"verdict"`
			RetroKind  string   `json:"retroKind"`
			CostKnown  bool     `json:"costKnown"`
			CostUSD    float64  `json:"costUsd"`
			Input      int      `json:"inputTokens"`
			Output     int      `json:"outputTokens"`
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
			if d.TitleOnly > r.SessionStart.TitleOnlyMax {
				r.SessionStart.TitleOnlyMax = d.TitleOnly
			}
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
		case e.Kind == memory.EventRetroRun:
			c := &r.Retros.Cost
			if !d.CostKnown {
				c.Unknown++
				continue
			}
			c.Known++
			c.USD += d.CostUSD
			c.InputTokens += d.Input
			c.OutputTokens += d.Output
			if d.RetroKind == "session" {
				c.SessionUSD += d.CostUSD
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
	jc, err := s.JudgmentCounts(startMs, endMs)
	if err != nil {
		return r, err
	}
	r.Writes = Writes{Keep: jc[memory.VerdictKeep], Hold: jc[memory.VerdictHold], Reject: jc[memory.VerdictReject], UserRejected: jc["user_reject"], UserEdited: jc["user_edit"]}
	rows, err := db.Query(`SELECT kind, status, started_at, finished_at FROM retros WHERE started_at>=? AND started_at<?`, startMs, endMs)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	var took int64
	for rows.Next() {
		var kind, status string
		var started, finished int64
		if err := rows.Scan(&kind, &status, &started, &finished); err != nil {
			return r, err
		}
		k := r.Retros.Kinds[kind]
		r.Retros.Runs++
		k.Runs++
		switch status {
		case "done":
			r.Retros.Done++
			k.Done++
			if finished > started {
				took += finished - started
			}
		case "empty":
			r.Retros.Empty++
			k.Empty++
		case "failed":
			r.Retros.Failed++
			k.Failed++
		}
		r.Retros.Kinds[kind] = k
	}
	if r.Retros.Done > 0 {
		r.Retros.AvgSeconds = int(took / int64(r.Retros.Done) / 1000)
	}
	return r, rows.Err()
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

func titled(cs []Count) string {
	var parts []string
	for _, c := range cs {
		title := c.Title
		if title == "" {
			title = "(없는 기억)"
		}
		parts = append(parts, fmt.Sprintf("%s(%s) %d회", title, c.ID, c.Count))
	}
	return strings.Join(parts, ", ")
}

var retroKindNames = []struct{ kind, name string }{{"session", "세션 끝"}, {"daily", "하루"}, {"weekly", "주간"}, {"tidy", "그래프 정리"}, {"legacy", "옛 기억 판정"}}

// Render is the report as short Korean text for the terminal and the weekly retrospective.
func Render(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "기간 %s ~ %s\n\n", r.From, r.To)
	c := r.Corrections
	fmt.Fprintf(&b, "- 반복 교정 %d건 (넣었는데 어김 %d · 넣지 못함 %d · 주입 여부 모름 %d), 새 교정 %d건\n", c.Repeated, c.Injected, c.NotInjected, c.Unknown, c.New)
	for _, t := range c.Top {
		title := t.Title
		if title == "" {
			title = "(없는 기억)"
		}
		fmt.Fprintf(&b, "  - %s (%s) %d회\n", title, t.ID, t.Count)
	}
	fmt.Fprintf(&b, "- 요청 적중 %d/%d (%s), 넣은 요청 평균 %d자\n", r.Prompts.Hits, r.Prompts.Total, pct(r.Prompts.HitRate), r.Prompts.AvgChars)
	u := r.Usage
	fmt.Fprintf(&b, "- 사용 판정: 따름 %d · 어김 %d · 상관없음 %d\n", u.Followed, u.Violated, u.Irrelevant)
	if len(u.TopViolated) > 0 {
		fmt.Fprintf(&b, "  - 어김: %s\n", titled(u.TopViolated))
	}
	if len(u.TopIrrelevant) > 0 {
		fmt.Fprintf(&b, "  - 상관없음: %s\n", titled(u.TopIrrelevant))
	}
	fmt.Fprintf(&b, "- 빈 검색 %d/%d (%s)", r.Find.Empty, r.Find.Total, pct(r.Find.EmptyRate))
	if len(r.Find.EmptyQueries) > 0 {
		var qs []string
		for _, q := range r.Find.EmptyQueries {
			qs = append(qs, fmt.Sprintf("%q %d회", q.Query, q.Count))
		}
		b.WriteString(": " + strings.Join(qs, ", "))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- 저장 %d건 (인용 %d · 그 밖 %d)\n", r.Saves.Total, r.Saves.Quote, r.Saves.Other)
	fmt.Fprintf(&b, "- 세션 %d개(시작 훅 %d번), 평균 %d자, 제목만 넣은 규칙 최대 %d개\n", r.SessionStart.Sessions, r.SessionStart.Starts, r.SessionStart.AvgChars, r.SessionStart.TitleOnlyMax)
	p := r.Proposals
	fmt.Fprintf(&b, "- 제안: 새로 %d건, 승인 %d · 고쳐서 승인 %d · 거절 %d, 지금 대기 %d건(가장 오래된 것 %d일)\n", p.Created, p.Approved, p.Edited, p.Rejected, p.Pending, p.OldestPendingDays)
	sc := r.Sources
	fmt.Fprintf(&b, "- 근거 파일 변경: 기억 %d건 감지 → 제안 %d건 (발췌 그대로 %d · 파일 없어짐 %d · 발췌 사라짐 %d · 판정 맞음 %d · 고칠 것 %d · 낡음 %d)\n",
		sc.Detected, sc.Proposals, sc.Kept, sc.Absent, sc.Gone, sc.OK, sc.Edit, sc.Stale)
	w := r.Writes
	fmt.Fprintf(&b, "- 쓰기 판정: 유지 %d · 보류 %d · 거절 %d, 사용자 거절 %d · 고쳐서 승인 %d\n", w.Keep, w.Hold, w.Reject, w.UserRejected, w.UserEdited)
	l := r.Ledger
	fmt.Fprintf(&b, "- 효과 원장: 변경 %d건, 판정 도움됨 %d · 해로움 %d · 효과 없음 %d · 섞임 %d(증거 있음 %d), 지금 관찰 중 %d\n", l.Changes,
		l.Judged[memory.VerdictHelped], l.Judged[memory.VerdictHarmful], l.Judged[memory.VerdictNoEffect], l.Judged[memory.VerdictMixed], l.Evidence, l.Pending)
	t := r.Retros
	fmt.Fprintf(&b, "- 회고 %d번: 완료 %d · 빈 회고 %d · 실패 %d, 평균 %d초", t.Runs, t.Done, t.Empty, t.Failed, t.AvgSeconds)
	var kinds []string
	for _, k := range retroKindNames {
		if c, ok := t.Kinds[k.kind]; ok && c.Runs > 0 {
			kinds = append(kinds, fmt.Sprintf("%s %d번", k.name, c.Runs))
		}
	}
	if len(kinds) > 0 {
		b.WriteString(" (" + strings.Join(kinds, " · ") + ")")
	}
	b.WriteString("\n")
	cost := t.Cost
	fmt.Fprintf(&b, "- 회고 비용(호스트 추정) $%.4f(세션 끝 $%.4f), 토큰 입력 %d · 출력 %d, 비용 모름 %d번\n", cost.USD, cost.SessionUSD, cost.InputTokens, cost.OutputTokens, cost.Unknown)
	g := r.Graph
	fmt.Fprintf(&b, "- 기억 그래프: 기억 %d개, 클러스터 %d개, 그 밖 %d개(%s), 주제 태그 없음 %d개, 연결 %d개, 주제 태그 %d종\n",
		g.Memories, g.Clusters, g.Other, pct(g.OtherRate), g.Untagged, g.Links, g.TopicTags)
	fmt.Fprintf(&b, "- 기억 상태: 후보 %d · 확정 %d · 낡음 %d · 숨김 %d\n", r.States[memory.StateCandidate], r.States[memory.StateConfirmed],
		r.States[memory.StateStale], r.States[memory.StateHidden])
	if l := r.Legacy; l.Total > 0 {
		var by []string
		for _, v := range []struct{ key, name string }{{"keep", "그대로 둠"}, {"confirm", "확정"}, {"merge", "합치기"}, {"stale", "낡음"}} {
			if n := l.By[v.key]; n > 0 {
				by = append(by, fmt.Sprintf("%s %d", v.name, n))
			}
		}
		fmt.Fprintf(&b, "- 옛 기억 정리: 대상 %d개 중 판정 %d개", l.Total, l.Judged)
		if len(by) > 0 {
			b.WriteString("(" + strings.Join(by, " · ") + ")")
		}
		fmt.Fprintf(&b, ", 남음 %d개\n", l.Left)
	}
	return b.String()
}
