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

// Prompts are the per-request injections.
type Prompts struct {
	Total   int     `json:"total"`
	Hits    int     `json:"hits"`
	HitRate float64 `json:"hitRate"`
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

// Retros are the retrospectives started in the period.
type Retros struct {
	Runs       int `json:"runs"`
	Done       int `json:"done"`
	Empty      int `json:"empty"`
	Failed     int `json:"failed"`
	AvgSeconds int `json:"avgSeconds"`
}

// Report is the metrics of a period of days (inclusive).
type Report struct {
	From         string         `json:"from"`
	To           string         `json:"to"`
	Corrections  Corrections    `json:"corrections"`
	Prompts      Prompts        `json:"prompts"`
	Find         Find           `json:"find"`
	Saves        Saves          `json:"saves"`
	SessionStart SessionStart   `json:"sessionStart"`
	Proposals    Proposals      `json:"proposals"`
	Retros       Retros         `json:"retros"`
	Tools        map[string]int `json:"tools"`
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
	chars := 0
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
	r.Find.EmptyRate = rate(r.Find.Empty, r.Find.Total)
	for _, q := range top(emptyQ, topN) {
		r.Find.EmptyQueries = append(r.Find.EmptyQueries, Count{Query: q, Count: emptyQ[q]})
	}
	for _, id := range top(corrected, topN) {
		c := Count{ID: id, Count: corrected[id]}
		if m, err := s.Get(id); err == nil {
			c.Title = m.Title
		}
		r.Corrections.Top = append(r.Corrections.Top, c)
	}

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
	rows, err := db.Query(`SELECT status, started_at, finished_at FROM retros WHERE started_at>=? AND started_at<?`, startMs, endMs)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	var took int64
	for rows.Next() {
		var status string
		var started, finished int64
		if err := rows.Scan(&status, &started, &finished); err != nil {
			return r, err
		}
		r.Retros.Runs++
		switch status {
		case "done":
			r.Retros.Done++
			if finished > started {
				took += finished - started
			}
		case "empty":
			r.Retros.Empty++
		case "failed":
			r.Retros.Failed++
		}
	}
	if r.Retros.Done > 0 {
		r.Retros.AvgSeconds = int(took / int64(r.Retros.Done) / 1000)
	}
	return r, rows.Err()
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

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
	fmt.Fprintf(&b, "- 요청 적중 %d/%d (%s)\n", r.Prompts.Hits, r.Prompts.Total, pct(r.Prompts.HitRate))
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
	t := r.Retros
	fmt.Fprintf(&b, "- 회고 %d번: 완료 %d · 빈 회고 %d · 실패 %d, 평균 %d초\n", t.Runs, t.Done, t.Empty, t.Failed, t.AvgSeconds)
	return b.String()
}
