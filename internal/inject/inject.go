// Package inject builds what the host hooks add to a session: the confirmed
// rules at session start, and the few memories that strongly match a request.
package inject

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// maxRulesRunes bounds the rule text added at session start, in characters,
// so the whole context stays under the hosts' hook context limit (about
// 10,000 characters in Claude Code). Rules beyond it are listed by title only.
const maxRulesRunes = 6000

func inScope(m memory.Memory, scope string) bool {
	return m.Scope == memory.ScopeGlobal || (scope != "" && m.Scope == scope)
}

// Options picks the form of what is injected. The hooks of Claude Code and
// Codex use the zero value (the full form); Orbit Code asks for the brief one.
type Options struct {
	// Brief lists each confirmed rule on one line (title and id) at session
	// start instead of its full text, and lets the work recall bring the full
	// text of the confirmed rules a request strongly matches. Nothing is left
	// out: every rule keeps its line, and memory_get reads the rest.
	Brief bool
}

// Not eligible for recall (subject): a volatile
// memory past its days, or an older value of a topic.
const (
	ExcludedExpired    = "expired"
	ExcludedSuperseded = "superseded_by_topic"
)

// excluded says why a live memory is kept out of recall, or "".
func excluded(m memory.Memory, now time.Time, heads memory.TopicHeads) string {
	if m.Freshness(now) == memory.FreshExpired {
		return ExcludedExpired
	}
	if heads.Superseding(m) != "" {
		return ExcludedSuperseded
	}
	return ""
}

// briefTitle drops a trailing date in parentheses, which the brief form does not need.
var trailingDate = regexp.MustCompile(`\s*\(\d{4}-\d{2}-\d{2}\)\s*$`)

func briefTitle(t string) string { return trailingDate.ReplaceAllString(t, "") }

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
	return SessionBundleWith(s, cfg, scope, host, session, Options{})
}

// SessionBundleWith is SessionBundle in the given form. A session keeps the
// form it was built in; the bundle is kept per host and session.
func SessionBundleWith(s *memory.Store, cfg config.Config, scope, host, session string, opts Options) Injection {
	if session != "" {
		if text, ids, ok := s.SessionBundle(host, session); ok {
			var b strings.Builder
			b.WriteString(text)
			return Injection{MemoryIDs: ids}.done(&b)
		}
	}
	inj := SessionStartInjectionWith(s, cfg, scope, opts)
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
	return SessionStartInjectionWith(s, cfg, scope, Options{})
}

// SessionStartInjectionWith is SessionStartInjection in the given form.
func SessionStartInjectionWith(s *memory.Store, cfg config.Config, scope string, opts Options) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.Inject == config.InjectOff {
		return inj.done(&b)
	}
	confirmed, err := s.List(memory.StateConfirmed)
	if err != nil {
		return inj.done(&b)
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	var rules, decisions []memory.Memory
	for _, m := range confirmed {
		if !inScope(m, scope) || memory.DomainOf(m) != "" { // 분야 지식은 요청에 맞을 때 신호로만 넣는다
			continue
		}
		if excluded(m, now, heads) != "" {
			continue
		}
		switch m.Kind {
		case memory.KindRule:
			rules = append(rules, m)
		case memory.KindDecision:
			decisions = append(decisions, m)
		}
	}
	if len(rules) > 0 && opts.Brief {
		b.WriteString("[OpenOrbit 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다. 한 줄이 규칙 하나의 요지다. 일이 규칙에 닿으면 memory_get으로 전문을 읽고 따른다(요청과 맞는 규칙은 전문이 회상으로 온다).\n")
		for _, m := range rules {
			fmt.Fprintf(&b, "- %s (%s)\n", briefTitle(m.Title), m.ID)
			inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
		}
	} else if len(rules) > 0 {
		b.WriteString("[OpenOrbit 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다.\n")
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
	if b.Len() > 0 && opts.Brief {
		b.WriteString("기억은 memory_find·memory_get·memory_save로 찾고 남긴다.")
	} else if b.Len() > 0 {
		b.WriteString("기억은 OpenOrbit MCP(memory_find·memory_get·memory_save)로 찾고 남긴다.")
	}
	return inj.done(&b)
}

// retroLine says whether a recent retrospective left results, how many proposals wait
// and how many harmful automatic changes were undone lately.
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
	// 효과 원장이 해로움으로 판정해 스스로 되돌린 자동 변경을 알린다(A2).
	if n := s.RecentAutoReverts(since); n > 0 {
		parts = append(parts, fmt.Sprintf("해로움 판정으로 자동 변경 %d건을 되돌림 — 감사 목록(proposals status=audit)에서 확인", n))
	}
	return strings.Join(parts, " · ")
}

// Prompt is the per-request context: the memories that match the request
// strongly, ranked and packed within the configured length. A weak match adds nothing.
func Prompt(s *memory.Store, cfg config.Config, scope, prompt string) string {
	return PromptInjection(s, cfg, scope, prompt).Text
}

// ruleBodyRunes is how much of a confirmed rule's body the brief form's work
// recall shows: the rule itself, not a summary of it.
const ruleBodyRunes = 600

// recallBodyRunes is how much of a memory's body the work recall shows, the
// same length as a memory_find summary.
const recallBodyRunes = 200

// PromptInjection is the work recall (설계 「작업 기준 회상」): memories that
// match the request strongly, confirmed before candidates (A2), then by match score, this project's scope
// first, then net usage (followed − irrelevant) over the ledger window, then
// the most recently edited, packed as title and body excerpt within
// cfg.RecallChars. A body that does not fit is left out; a title that does
// not fit ends the list.
func PromptInjection(s *memory.Store, cfg config.Config, scope, prompt string) Injection {
	return PromptInjectionWith(s, cfg, scope, prompt, Options{})
}

// Candidates are the live memories the work recall looks at for scope, and
// why the rest of the live ones in scope are left out (memory id → reason).
func Candidates(s *memory.Store, scope string, opts Options) (map[string]memory.Memory, map[string]string, error) {
	active, err := s.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return nil, nil, err
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	byID := map[string]memory.Memory{}
	out := map[string]string{}
	for _, m := range active {
		// 확정 규칙은 세션 시작에 이미 들어갔다(분야 규칙과 짧은 꼴은 빼고). 일지는 대화 맥락이 아니다.
		domain := memory.DomainOf(m) != ""
		switch {
		case !inScope(m, scope):
			out[m.ID] = "scope"
		case m.Kind == memory.KindJournal:
			out[m.ID] = "journal"
		case m.Kind == memory.KindRule && m.State == memory.StateConfirmed && !domain && !opts.Brief:
			out[m.ID] = "session_rule"
		default:
			if why := excluded(m, now, heads); why != "" {
				out[m.ID] = why
				continue
			}
			byID[m.ID] = m
		}
	}
	return byID, out, nil
}

// PromptInjectionWith is PromptInjection in the given form.
func PromptInjectionWith(s *memory.Store, cfg config.Config, scope, prompt string, opts Options) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.Inject != config.InjectFull || strings.TrimSpace(prompt) == "" {
		return inj.done(&b)
	}
	byID, _, err := Candidates(s, scope, opts)
	if err != nil {
		return inj.done(&b)
	}
	docs := make([]memory.Doc, 0, len(byID))
	for _, m := range byID {
		docs = append(docs, m.Doc())
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
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
		// 후보(AI가 배운 것)는 회상에 쓰되 확정보다 뒤에 둔다(A2 확정 규칙).
		if ca, cc := byID[a.Doc.ID].State == memory.StateConfirmed, byID[c.Doc.ID].State == memory.StateConfirmed; ca != cc {
			return ca
		}
		if a.Score != c.Score {
			return a.Score > c.Score
		}
		if la, lc := local(a.Doc.ID), local(c.Doc.ID); la != lc {
			return la
		}
		// 나이 감쇠: 관련도가 같으면 최근에 바뀌거나 다시 나온 기억이 앞이다.
		if wa, wc := memory.AgeWeight(byID[a.Doc.ID], now), memory.AgeWeight(byID[c.Doc.ID], now); wa != wc {
			return wa > wc
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
	head := "[OpenOrbit 관련 기억] 필요하면 memory_get으로 본문을 읽는다.\n"
	used := utf8.RuneCountInString(head)
	var entries strings.Builder
	var domains []string // 요청과 맞은 분야, 맞은 순서
	for _, h := range hits {
		m := byID[h.Doc.ID]
		if d := memory.DomainOf(m); d != "" {
			if !slices.Contains(domains, d) {
				domains = append(domains, d)
			}
			continue
		}
		state := "확정"
		if m.State == memory.StateCandidate {
			state = "후보"
		}
		if m.Freshness(now) == memory.FreshReviewDue {
			state += " · 재검토일 지남"
		}
		line := fmt.Sprintf("- %s (%s · %s %s)\n", m.Title, m.ID, kindName(m.Kind), state)
		body := ""
		clip := recallBodyRunes
		if m.Kind == memory.KindRule && m.State == memory.StateConfirmed {
			clip = ruleBodyRunes
		}
		if text := strings.Join(strings.Fields(m.Body), " "); text != "" {
			body = "  " + memory.ClipRunes(text, clip) + "\n"
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
	// 분야 지식은 본문 대신 그 분야의 신호(제목·id)를 글자 수 안에서 보이고, AI가 필요한 항목만 연다(설계 · Orbit 개선 3차).
	for _, d := range domains {
		var members []memory.Memory
		for _, m := range byID {
			if memory.DomainOf(m) == d {
				members = append(members, m)
			}
		}
		sort.Slice(members, func(i, j int) bool { return members[i].UpdatedAt > members[j].UpdatedAt })
		sec := fmt.Sprintf("[분야 「%s」 신호] 필요한 항목만 memory_get으로 연다.\n", d)
		if used+utf8.RuneCountInString(sec) > budget {
			break
		}
		entries.WriteString(sec)
		used += utf8.RuneCountInString(sec)
		shownN := 0
		for _, m := range members {
			line := fmt.Sprintf("- %s (%s)\n", m.Title, m.ID)
			if used+utf8.RuneCountInString(line) > budget {
				break
			}
			entries.WriteString(line)
			used += utf8.RuneCountInString(line)
			inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
			shownN++
		}
		if shownN < len(members) { // 길이 때문에 다 보이지 못하면 그 사실을 알린다(남은 것은 memory_find로)
			entries.WriteString(fmt.Sprintf("- …신호 %d개 중 %d개만 보임. 나머지는 memory_find로 찾는다\n", len(members), shownN))
		}
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
