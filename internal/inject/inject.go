// Package inject builds what the host hooks add to a session: the confirmed
// rules at session start, and the few memories that strongly match a request.
package inject

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
)

// maxRulesRunes bounds the rule text added at session start, in characters,
// so the whole context stays under the hosts' hook context limit (about
// 10,000 characters in Claude Code). Rules beyond it are listed by title only.
const maxRulesRunes = 6000

func inScope(m memory.Memory, scope string) bool {
	return m.Scope == memory.ScopeGlobal || (scope != "" && m.Scope == scope)
}

// Injection is what a hook adds and which memories it named, for the metrics.
type Injection struct {
	Text      string
	MemoryIDs []string // 전문이든 제목이든 문맥에 넣은 기억
	Chars     int
	TitleOnly int // 길이 때문에 제목만 넣은 규칙 수
}

func (in Injection) done(b *strings.Builder) Injection {
	in.Text = strings.TrimSpace(b.String())
	in.Chars = utf8.RuneCountInString(in.Text)
	if in.MemoryIDs == nil {
		in.MemoryIDs = []string{}
	}
	return in
}

// SessionBundle is the fixed session-start bundle (설계 「작업 기준 회상」의
// 고정 묶음): built once per session and handed back unchanged when the same
// session starts again (resume, compaction), so the host's prompt cache holds.
// Without a session id it is built afresh.
func SessionBundle(s *memory.Store, cfg config.Config, scope, host, session string) Injection {
	if session != "" {
		if text, ids, ok := s.SessionBundle(host, session); ok {
			var b strings.Builder
			b.WriteString(text)
			return Injection{MemoryIDs: ids}.done(&b)
		}
	}
	inj := SessionStartInjection(s, cfg, scope)
	if session != "" && inj.Text != "" {
		_ = s.SaveSessionBundle(host, session, inj.Text, inj.MemoryIDs)
	}
	return inj
}

// SessionStart is the session-start context: confirmed rules of the global
// scope and this project in full, the titles of recent decisions, and one line
// about retrospectives and waiting proposals.
func SessionStart(s *memory.Store, cfg config.Config, scope string) string {
	return SessionStartInjection(s, cfg, scope).Text
}

// SessionStartInjection is SessionStart with the memories it named.
func SessionStartInjection(s *memory.Store, cfg config.Config, scope string) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.Inject == config.InjectOff {
		return inj.done(&b)
	}
	confirmed, err := s.List(memory.StateConfirmed)
	if err != nil {
		return inj.done(&b)
	}
	var rules, decisions []memory.Memory
	for _, m := range confirmed {
		if !inScope(m, scope) {
			continue
		}
		switch m.Kind {
		case memory.KindRule:
			rules = append(rules, m)
		case memory.KindDecision:
			decisions = append(decisions, m)
		}
	}
	if len(rules) > 0 {
		b.WriteString("[Orbit 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다.\n")
		var titlesOnly []memory.Memory
		for _, m := range rules { // 최근에 고친 규칙부터 전문으로
			entry := fmt.Sprintf("- %s (%s)\n", m.Title, m.ID)
			if body := strings.TrimSpace(m.Body); body != "" {
				entry += "  " + strings.ReplaceAll(body, "\n", "\n  ") + "\n"
			}
			if utf8.RuneCountInString(b.String())+utf8.RuneCountInString(entry) > maxRulesRunes {
				titlesOnly = append(titlesOnly, m)
				continue
			}
			b.WriteString(entry)
			inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
		}
		if len(titlesOnly) > 0 {
			b.WriteString("- 길이 때문에 제목만: ")
			for i, m := range titlesOnly {
				if i > 0 {
					b.WriteString(" · ")
				}
				fmt.Fprintf(&b, "%s(%s)", m.Title, m.ID)
				inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
			}
			b.WriteString("\n")
			inj.TitleOnly = len(titlesOnly)
		}
	}
	if cfg.Inject == config.InjectRules {
		return inj.done(&b)
	}
	if n := cfg.RecentDecisions; n > 0 && len(decisions) > 0 {
		b.WriteString("[최근 결정] ")
		for i := 0; i < len(decisions) && i < n; i++ {
			if i > 0 {
				b.WriteString(" · ")
			}
			fmt.Fprintf(&b, "%s(%s)", decisions[i].Title, decisions[i].ID)
			inj.MemoryIDs = append(inj.MemoryIDs, decisions[i].ID)
		}
		b.WriteString("\n")
	}
	if line := retroLine(s); line != "" {
		b.WriteString(line + "\n")
	}
	if b.Len() > 0 {
		b.WriteString("기억은 Orbit MCP(memory_find·memory_get·memory_save)로 찾고 남긴다.")
	}
	return inj.done(&b)
}

// retroLine says whether a recent retrospective left results and how many proposals wait.
func retroLine(s *memory.Store) string {
	var parts []string
	var period, journal string
	since := s.Now().Add(-72 * time.Hour).UnixMilli()
	_ = s.DB().QueryRow(`SELECT period, journal_id FROM retros WHERE status='done' AND finished_at>=? ORDER BY finished_at DESC LIMIT 1`, since).Scan(&period, &journal)
	if period != "" {
		p := "회고 결과 있음(" + period
		if journal != "" {
			p += " 일지 " + journal
		}
		parts = append(parts, p+")")
	}
	if n := s.PendingCount(); n > 0 {
		parts = append(parts, fmt.Sprintf("제안 %d건 대기 — \"제안 보여줘\"로 확인", n))
	}
	return strings.Join(parts, " · ")
}

// Prompt is the per-request context: the memories that match the request
// strongly, ranked and packed within the configured length. A weak match adds nothing.
func Prompt(s *memory.Store, cfg config.Config, scope, prompt string) string {
	return PromptInjection(s, cfg, scope, prompt).Text
}

// recallBodyRunes is how much of a memory's body the work recall shows, the
// same length as a memory_find summary.
const recallBodyRunes = 200

// PromptInjection is the work recall (설계 「작업 기준 회상」): memories that
// match the request strongly, ordered by match score, this project's scope
// first, then net usage (followed − irrelevant) over the ledger window, then
// the most recently edited, packed as title and body excerpt within
// cfg.RecallChars. A body that does not fit is left out; a title that does
// not fit ends the list.
func PromptInjection(s *memory.Store, cfg config.Config, scope, prompt string) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.Inject != config.InjectFull || strings.TrimSpace(prompt) == "" {
		return inj.done(&b)
	}
	active, err := s.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return inj.done(&b)
	}
	byID := map[string]memory.Memory{}
	var docs []memory.Doc
	for _, m := range active {
		// 확정 규칙은 세션 시작에 이미 들어갔다. 일지는 대화 맥락이 아니다.
		if !inScope(m, scope) || m.Kind == memory.KindJournal || (m.Kind == memory.KindRule && m.State == memory.StateConfirmed) {
			continue
		}
		byID[m.ID] = m
		docs = append(docs, m.Doc())
	}
	hits := memory.Strong(docs, prompt)
	if len(hits) == 0 {
		return inj.done(&b)
	}
	now := s.Now()
	usage, _ := s.UsageCounts(memory.Day(now.AddDate(0, 0, -max(cfg.LedgerWindowDays, 1))), memory.Day(now))
	net := func(id string) int { u := usage[id]; return u.Followed - u.Irrelevant }
	local := func(id string) bool { return scope != "" && byID[id].Scope == scope }
	sort.SliceStable(hits, func(i, j int) bool {
		a, c := hits[i], hits[j]
		if a.Score != c.Score {
			return a.Score > c.Score
		}
		if la, lc := local(a.Doc.ID), local(c.Doc.ID); la != lc {
			return la
		}
		if na, nc := net(a.Doc.ID), net(c.Doc.ID); na != nc {
			return na > nc
		}
		return byID[a.Doc.ID].UpdatedAt > byID[c.Doc.ID].UpdatedAt
	})
	budget := cfg.RecallChars
	if budget <= 0 {
		budget = config.Default().RecallChars
	}
	head := "[Orbit 관련 기억] 필요하면 memory_get으로 본문을 읽는다.\n"
	used := utf8.RuneCountInString(head)
	var entries strings.Builder
	for _, h := range hits {
		m := byID[h.Doc.ID]
		state := "확정"
		if m.State == memory.StateCandidate {
			state = "후보"
		}
		line := fmt.Sprintf("- %s (%s · %s %s)\n", m.Title, m.ID, kindName(m.Kind), state)
		body := ""
		if text := strings.Join(strings.Fields(m.Body), " "); text != "" {
			body = "  " + memory.ClipRunes(text, recallBodyRunes) + "\n"
		}
		n := utf8.RuneCountInString(line)
		if used+n > budget {
			break
		}
		if bn := utf8.RuneCountInString(body); body != "" && used+n+bn <= budget {
			line += body
			n += bn
		}
		entries.WriteString(line)
		used += n
		inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
	}
	if len(inj.MemoryIDs) == 0 {
		return inj.done(&b)
	}
	b.WriteString(head)
	b.WriteString(entries.String())
	return inj.done(&b)
}

func kindName(k string) string {
	return map[string]string{memory.KindRule: "규칙", memory.KindFact: "사실", memory.KindDecision: "결정", memory.KindLesson: "교훈", memory.KindJournal: "일지"}[k]
}

// HookOutput is the JSON both Claude Code and Codex read from a hook's stdout.
// An empty context prints nothing.
func HookOutput(event, context string) string {
	if context == "" {
		return ""
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": context}})
	return string(b)
}
