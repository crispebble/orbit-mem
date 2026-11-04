// Package inject builds what the host hooks add to a session: the confirmed
// rules at session start, the few memories that strongly match a request (with
// what changed in the session), and the memories a tool triggers right before
// its call.
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

// Lengths (설계 · orbit-mem 2판 AC-1). Claude Code saves a hook context over 10,000
// characters to a file and shows only its first 2,000, so no hook context passes
// MaxHookRunes, and the session start, which stays for the whole session, keeps to
// MaxSessionStartRunes (D2). Rules that do not fit are not listed by title: they are
// counted in one line and come through the work recall when a request matches them.
const (
	MaxHookRunes         = 9000
	MaxSessionStartRunes = 6000
	// sessionNoteRunes is kept free for the lines a hook adds after the bundle (the
	// plugin version note and the overlapping memory note, 17절).
	sessionNoteRunes = 600
	// maxDecisionsRunes bounds the line of recent decision titles.
	maxDecisionsRunes = 1000
	// MaxToolRunes and MaxToolMemories bound what a tool call brings (6절).
	MaxToolRunes    = 1000
	MaxToolMemories = 3
	// maxNewMemories is how many memories confirmed elsewhere one request announces.
	maxNewMemories = 3
	// maxSessionNewsRunes bounds the corrections and new memories of one request.
	maxSessionNewsRunes = 2000
	// moreLineRunes is kept for the line that says a domain shows only some signals.
	moreLineRunes = 60
)

func inScope(m memory.Memory, scope string) bool {
	return m.Scope == memory.ScopeGlobal || (scope != "" && m.Scope == scope)
}

// Options picks the form of what is injected. The hooks of Claude Code and
// Codex use the zero value (the full form); Orbit Code asks for the brief one.
type Options struct {
	// Brief lists each confirmed rule on one line (title and id) at session
	// start instead of its full text, and lets the work recall bring the full
	// text of the confirmed rules a request strongly matches. It keeps to the
	// same length as the full form; memory_get reads the rest.
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
	Omitted   int // 길이 때문에 세션 시작에 넣지 못한 확정 규칙 수(요청 회상 후보로 남는다)
	// marks are what the session is told once (memory.Mark*), kept by RecordShown.
	marks map[string][]string
}

func (in *Injection) mark(kind string, ids ...string) {
	if in.marks == nil {
		in.marks = map[string][]string{}
	}
	in.marks[kind] = append(in.marks[kind], ids...)
}

// RecordShown keeps what the injection told the session, so the corrections, the new
// memories and the tool recall are given once per session.
func RecordShown(s *memory.Store, host, session string, in Injection) {
	for kind, ids := range in.marks {
		_ = s.MarkSession(host, session, kind, ids...)
	}
}

// ResetSession drops the session's bundle and marks: a compaction or /clear starts
// the session over with a bundle made anew (설계 · orbit-mem 2판 AC-4).
func ResetSession(s *memory.Store, host, session string) { _ = s.ResetSession(host, session) }

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
// session resumes, so the host's prompt cache holds. A compaction or /clear calls
// ResetSession first. Without a session id it is built afresh.
func SessionBundle(s *memory.Store, cfg config.Config, scope, host, session string) Injection {
	return SessionBundleWith(s, cfg, scope, host, session, Options{})
}

// SessionBundleWith is SessionBundle in the given form. A session keeps the
// form it was built in; the bundle is kept per host and session.
func SessionBundleWith(s *memory.Store, cfg config.Config, scope, host, session string, opts Options) Injection {
	if session != "" {
		if text, ids, ok := s.SessionBundle(host, session); ok && runes(text) <= MaxSessionStartRunes-sessionNoteRunes {
			var b strings.Builder
			b.WriteString(text)
			return Injection{MemoryIDs: ids}.done(&b)
		} else if ok { // 0.10.0이 남긴 한도 넘는 묶음은 다시 만든다
			_ = s.ResetSession(host, session)
		}
	}
	inj := SessionStartInjectionWith(s, cfg, scope, opts)
	if session != "" { // 빈 묶음도 남긴다: 세션이 언제 시작했는지가 정정·새 기억 줄의 기준이다
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

// SessionStartInjectionWith is SessionStartInjection in the given form. The whole
// text keeps within MaxSessionStartRunes: the decision titles, the retrospective
// line and the closing line first, then the rules, most recently changed first, as
// many as fit; the rest are counted in one line.
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
	var tail strings.Builder // 결정 제목·회고 줄: 규칙 뒤에 붙는다
	var tailIDs []string
	if cfg.Inject != config.InjectRules {
		if n := cfg.RecentDecisions; n > 0 && len(decisions) > 0 {
			line := "[최근 결정] "
			for i := 0; i < len(decisions) && i < n; i++ {
				entry := fmt.Sprintf("%s(%s)", decisions[i].Title, decisions[i].ID)
				if i > 0 {
					entry = " · " + entry
				}
				if runes(line)+runes(entry) > maxDecisionsRunes {
					break
				}
				line += entry
				tailIDs = append(tailIDs, decisions[i].ID)
			}
			if len(tailIDs) > 0 {
				tail.WriteString(line + "\n")
			}
		}
		if line := retroLine(s); line != "" {
			tail.WriteString(line + "\n")
		}
	}
	footer := "기억은 OpenOrbit MCP(memory_find·memory_get·memory_save)로 찾고 남긴다."
	head := "[OpenOrbit 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다.\n"
	if opts.Brief {
		footer = "기억은 memory_find·memory_get·memory_save로 찾고 남긴다."
		head = "[OpenOrbit 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다. 한 줄이 규칙 하나의 요지다. 일이 규칙에 닿으면 memory_get으로 전문을 읽고 따른다(요청과 맞는 규칙은 전문이 회상으로 온다).\n"
	}
	omittedLine := func(n int) string {
		return fmt.Sprintf("- 확정 규칙 %d개 더: 길이 때문에 여기 없다. 요청과 맞으면 회상으로 들어오고, memory_find로 찾는다.\n", n)
	}
	// 규칙 몫: 전체 한도에서 꼬리·맺음 줄·생략 줄 자리를 먼저 뺀다.
	budget := MaxSessionStartRunes - sessionNoteRunes - runes(tail.String()) - runes(footer) - runes(head) - runes(omittedLine(len(rules)))
	if len(rules) > 0 {
		var body strings.Builder
		used := 0
		for _, m := range rules { // 최근에 고친 규칙부터
			entry := fmt.Sprintf("- %s (%s)\n", m.Title, m.ID)
			if opts.Brief {
				entry = fmt.Sprintf("- %s (%s)\n", briefTitle(m.Title), m.ID)
			} else if text := strings.TrimSpace(m.Body); text != "" {
				entry += "  " + strings.ReplaceAll(text, "\n", "\n  ") + "\n"
			}
			if n := runes(entry); used+n <= budget {
				body.WriteString(entry)
				used += n
				inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
				continue
			}
			inj.Omitted++
		}
		if used > 0 || inj.Omitted > 0 {
			b.WriteString(head)
			b.WriteString(body.String())
		}
		if inj.Omitted > 0 {
			b.WriteString(omittedLine(inj.Omitted))
		}
	}
	if cfg.Inject == config.InjectRules {
		return inj.done(&b)
	}
	b.WriteString(tail.String())
	inj.MemoryIDs = append(inj.MemoryIDs, tailIDs...)
	if b.Len() > 0 {
		b.WriteString(footer)
	}
	return inj.done(&b)
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// retroLine says whether a recent retrospective left results and how many
// harmful automatic changes were undone lately. Nothing waits for the person,
// so it does not count proposals (설계 · orbit-mem 2판 18.6).
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
// why the rest of the live ones in scope are left out (memory id → reason). The
// confirmed rules the session start would carry now are left out
// ("session_rule"); the ones it leaves out for length stay candidates.
func Candidates(s *memory.Store, cfg config.Config, scope string, opts Options) (map[string]memory.Memory, map[string]string, error) {
	return CandidatesFor(s, scope, opts, SessionStartInjectionWith(s, cfg, scope, opts).MemoryIDs)
}

// CandidatesFor is Candidates for a session whose bundle named bundle: a confirmed
// rule is left out of the work recall only when the bundle carries its full text.
func CandidatesFor(s *memory.Store, scope string, opts Options, bundle []string) (map[string]memory.Memory, map[string]string, error) {
	active, err := s.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return nil, nil, err
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	inBundle := map[string]bool{}
	for _, id := range bundle {
		inBundle[id] = true
	}
	byID := map[string]memory.Memory{}
	out := map[string]string{}
	for _, m := range active {
		// 세션 묶음에 전문으로 든 확정 규칙은 다시 넣지 않는다(분야 규칙과 짧은 꼴은 빼고). 일지는 대화 맥락이 아니다.
		domain := memory.DomainOf(m) != ""
		switch why := excluded(m, now, heads); {
		case !inScope(m, scope):
			out[m.ID] = "scope"
		case m.Kind == memory.KindJournal:
			out[m.ID] = "journal"
		case why != "":
			out[m.ID] = why
		case m.Kind == memory.KindRule && m.State == memory.StateConfirmed && !domain && !opts.Brief && inBundle[m.ID]:
			out[m.ID] = "session_rule"
		default:
			byID[m.ID] = m
		}
	}
	return byID, out, nil
}

// PromptInjectionWith is PromptInjection in the given form.
func PromptInjectionWith(s *memory.Store, cfg config.Config, scope, prompt string, opts Options) Injection {
	return PromptInjectionFor(s, cfg, scope, prompt, "", "", opts)
}

// PromptInjectionFor is the work recall of a request in a host session. Besides the
// matching memories it tells the session, once, what changed since its bundle was
// made (설계 · orbit-mem 2판 6절): a memory of the session superseded meanwhile (a
// correction line) and memories confirmed elsewhere after the session started (new
// memory lines, up to maxNewMemories). The whole stays within MaxHookRunes.
// RecordShown keeps what it told.
func PromptInjectionFor(s *memory.Store, cfg config.Config, scope, prompt, host, session string, opts Options) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.Inject != config.InjectFull || strings.TrimSpace(prompt) == "" {
		return inj.done(&b)
	}
	_, bundleIDs, ok := s.SessionBundle(host, session)
	if !ok { // 묶음을 모르면 지금 세션 시작이 넣을 것으로 본다
		bundleIDs = SessionStartInjectionWith(s, cfg, scope, opts).MemoryIDs
	}
	byID, _, err := CandidatesFor(s, scope, opts, bundleIDs)
	if err != nil {
		return inj.done(&b)
	}
	news := ""
	if ok {
		news = sessionNews(s, &inj, host, session, bundleIDs, byID)
	}
	b.WriteString(news)
	recall := recallEntries(s, cfg, scope, prompt, byID, &inj, MaxHookRunes-runes(news))
	b.WriteString(recall)
	return inj.done(&b)
}

// sessionNews is the correction and new memory lines of a session (설계 · orbit-mem 2판 6절).
func sessionNews(s *memory.Store, inj *Injection, host, session string, bundleIDs []string, byID map[string]memory.Memory) string {
	at := s.SessionBundleAt(host, session)
	told := map[string]bool{}
	for _, id := range bundleIDs {
		told[id] = true
	}
	for _, kind := range []string{memory.MarkShown, memory.MarkNew, memory.MarkTool} {
		for id := range s.SessionMarks(host, session, kind) {
			told[id] = true
		}
	}
	corrected := s.SessionMarks(host, session, memory.MarkCorrection)
	var lines strings.Builder
	used := 0
	add := func(line string) bool {
		if used+runes(line) > maxSessionNewsRunes {
			return false
		}
		lines.WriteString(line)
		used += runes(line)
		return true
	}
	ids := make([]string, 0, len(told))
	for id := range told {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	named := map[string]bool{}
	for _, id := range ids {
		if corrected[id] {
			continue
		}
		old, err := s.Get(id)
		if err != nil || old.State != memory.StateStale || old.SupersededBy == "" || old.UpdatedAt <= at {
			continue
		}
		cur, live := liveSuccessor(s, old)
		if !live {
			continue
		}
		if !add(fmt.Sprintf("- 정정: 「%s」(%s)는 「%s」(%s)로 바뀌었다. 옛 판을 따르지 않는다.\n", old.Title, old.ID, cur.Title, cur.ID)) {
			break
		}
		inj.mark(memory.MarkCorrection, old.ID)
		inj.MemoryIDs = append(inj.MemoryIDs, cur.ID)
		named[cur.ID] = true
	}
	since, _ := s.ConfirmedSince(at)
	var fresh []memory.Memory
	for id := range since {
		m, live := byID[id]
		if !live || m.State != memory.StateConfirmed || told[id] || named[id] || memory.DomainOf(m) != "" {
			continue
		}
		fresh = append(fresh, m)
	}
	sort.Slice(fresh, func(i, j int) bool {
		if since[fresh[i].ID] != since[fresh[j].ID] {
			return since[fresh[i].ID] > since[fresh[j].ID]
		}
		return fresh[i].ID < fresh[j].ID
	})
	for i, m := range fresh {
		if i == maxNewMemories {
			break
		}
		line := fmt.Sprintf("- 새 기억: %s (%s · %s 확정)\n", m.Title, m.ID, kindName(m.Kind))
		if text := strings.Join(strings.Fields(m.Body), " "); text != "" {
			line += "  " + memory.ClipRunes(text, recallBodyRunes) + "\n"
		}
		if !add(line) {
			break
		}
		inj.mark(memory.MarkNew, m.ID)
		inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
		named[m.ID] = true
	}
	if used == 0 {
		return ""
	}
	return "[OpenOrbit 기억 · 이 세션 중 바뀐 것] 세션 시작 뒤 대체되거나 다른 세션에서 확정된 기억이다.\n" + lines.String()
}

// liveSuccessor follows superseded_by from old to the live (candidate or confirmed)
// value that replaces it now, a few steps at most.
func liveSuccessor(s *memory.Store, old memory.Memory) (memory.Memory, bool) {
	next := old.SupersededBy
	for range 8 {
		if next == "" {
			return memory.Memory{}, false
		}
		m, err := s.Get(next)
		if err != nil {
			return memory.Memory{}, false
		}
		if m.State == memory.StateCandidate || m.State == memory.StateConfirmed {
			return m, true
		}
		next = m.SupersededBy
	}
	return memory.Memory{}, false
}

// recallEntries is the work recall list of the request within limit characters (and
// cfg.RecallChars). Memories already named in this injection are not listed again.
func recallEntries(s *memory.Store, cfg config.Config, scope, prompt string, byID map[string]memory.Memory, inj *Injection, limit int) string {
	named := map[string]bool{}
	for _, id := range inj.MemoryIDs {
		named[id] = true
	}
	docs := make([]memory.Doc, 0, len(byID))
	for _, m := range byID {
		if !named[m.ID] {
			docs = append(docs, m.Doc())
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	hits := memory.Strong(docs, prompt)
	if len(hits) == 0 {
		return ""
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
	budget = min(budget, limit-moreLineRunes) // 분야 신호의 「N개 중 M개만 보임」 줄 몫
	head := "[OpenOrbit 관련 기억] 필요하면 memory_get으로 본문을 읽는다.\n"
	used := utf8.RuneCountInString(head)
	var entries strings.Builder
	shown := 0
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
			body = "  " + memory.ClipRunes(text, clip)
			if clip == ruleBodyRunes && utf8.RuneCountInString(text) > clip {
				body += " (전문은 memory_get)"
			}
			body += "\n"
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
		inj.mark(memory.MarkShown, m.ID)
		shown++
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
			shown++
		}
		if shownN < len(members) { // 길이 때문에 다 보이지 못하면 그 사실을 알린다(남은 것은 memory_find로)
			line := fmt.Sprintf("- …신호 %d개 중 %d개만 보임. 나머지는 memory_find로 찾는다\n", len(members), shownN)
			if used+runes(line) > budget+moreLineRunes {
				break
			}
			entries.WriteString(line)
			used += runes(line)
		}
	}
	if shown == 0 {
		return ""
	}
	return head + entries.String()
}

// ToolInjection is what a tool call brings right before it runs (PreToolUse, 설계 ·
// orbit-mem 2판 6절): the live current values in scope whose tool triggers name the
// tool or a prefix of it, exact names first, then confirmed, this project, newest, at
// most MaxToolMemories within MaxToolRunes, each once per session (RecordShown).
func ToolInjection(s *memory.Store, cfg config.Config, scope, host, session, tool string) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.Inject == config.InjectOff || strings.TrimSpace(tool) == "" {
		return inj.done(&b)
	}
	active, err := s.Triggered()
	if err != nil || len(active) == 0 {
		return inj.done(&b)
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	given := s.SessionMarks(host, session, memory.MarkTool)
	type hit struct {
		m     memory.Memory
		exact bool
	}
	var hits []hit
	for _, m := range active {
		if len(m.Tools) == 0 || given[m.ID] || !inScope(m, scope) || m.Kind == memory.KindJournal || excluded(m, now, heads) != "" {
			continue
		}
		if exact, ok := m.TriggeredBy(tool); ok {
			hits = append(hits, hit{m, exact})
		}
	}
	if len(hits) == 0 {
		return inj.done(&b)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, c := hits[i], hits[j]
		if a.exact != c.exact {
			return a.exact
		}
		if ca, cc := a.m.State == memory.StateConfirmed, c.m.State == memory.StateConfirmed; ca != cc {
			return ca
		}
		if la, lc := a.m.Scope == scope, c.m.Scope == scope; la != lc {
			return la
		}
		return a.m.UpdatedAt > c.m.UpdatedAt
	})
	head := fmt.Sprintf("[OpenOrbit 기억 · %s 호출 직전] 이 도구에 걸린 기억이다. 부르기 전에 확인하고 따른다.\n", tool)
	used := runes(head)
	var entries strings.Builder
	for _, h := range hits {
		if len(inj.MemoryIDs) == MaxToolMemories {
			break
		}
		m := h.m
		state := "확정"
		if m.State == memory.StateCandidate {
			state = "후보"
		}
		line := fmt.Sprintf("- %s (%s · %s %s)\n", m.Title, m.ID, kindName(m.Kind), state)
		if used+runes(line) > MaxToolRunes {
			break
		}
		if text := strings.Join(strings.Fields(m.Body), " "); text != "" {
			room := min(ruleBodyRunes, MaxToolRunes-used-runes(line)-3)
			if room > 20 {
				line += "  " + memory.ClipRunes(text, room) + "\n"
			}
		}
		if used+runes(line) > MaxToolRunes {
			continue
		}
		entries.WriteString(line)
		used += runes(line)
		inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
		inj.mark(memory.MarkTool, m.ID)
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
