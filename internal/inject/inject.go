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

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
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
	// maxNewMemories is how many memories confirmed elsewhere one request announces.
	maxNewMemories = 3
	// maxSessionNewsRunes bounds the corrections and new memories of one request.
	maxSessionNewsRunes = 2000
	// moreLineRunes is kept for the line that says a domain shows only some signals.
	moreLineRunes = 60
)

// Label is the name every memory context heading carries: the memory system's own name.
const Label = "orbit-mem"

// at is the place of a session working in scope, by the product map of cfg (S3 명세 2.2·2.3).
func at(cfg config.Config, scope string) place.Place { return place.Resolve(scope, cfg.Products) }

func inScope(m memory.Memory, pl place.Place) bool { return pl.Sees(m.Scope, m.Project) }

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
// about harmful automatic changes undone lately.
func SessionStart(s *memory.Store, cfg config.Config, scope string) string {
	return SessionStartInjection(s, cfg, scope).Text
}

// SessionStartInjection is SessionStart with the memories it named.
func SessionStartInjection(s *memory.Store, cfg config.Config, scope string) Injection {
	return SessionStartInjectionWith(s, cfg, scope, Options{})
}

// SessionStartInjectionWith is SessionStartInjection in the given form (S3-R6): the core layers, one
// line on where to look first (how many confirmed rules and decisions this folder sees, memory_find
// first), the titles of recent decisions and the automatic-revert line, within MaxSessionStartRunes. The
// other memories come through the request recall, the tool recall and memory_find (S3-R7). Only
// inject=rules, which has no request recall, fills the rest with confirmed rules as before.
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
	pl := at(cfg, scope)
	heads, _ := s.TopicHeads()
	now := s.Now()
	// 상시 핵심(S3 명세 2.7): user 핵심, 그리고 한 제품 폴더면 그 제품의 핵심. 여러 제품 폴더는 제품 핵심을 넣지 않는다.
	var core strings.Builder
	layer := func(scope, heading string) {
		ms, err := s.Core(scope)
		if err != nil || len(ms) == 0 {
			return
		}
		var body strings.Builder
		used, limit := 0, memory.CoreLimit(scope)
		for _, m := range ms {
			if excluded(m, now, heads) != "" {
				continue
			}
			entry := memory.CoreEntry(m)
			if used+runes(entry) > limit { // 한도는 승격할 때 지킨다. 설정이 바뀐 옛 묶음만 여기서 멈춘다
				continue
			}
			body.WriteString(entry)
			used += runes(entry)
			inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
		}
		if used > 0 {
			core.WriteString(heading)
			core.WriteString(body.String())
		}
	}
	layer(memory.ScopeGlobal, "["+Label+" 기억 · 상시 핵심] 모든 일에 늘 지키는 사용자의 방식이다.\n")
	if p := pl.Product(); p != "" {
		layer("product:"+p, fmt.Sprintf("["+Label+" 기억 · %s 핵심] 이 제품에서 늘 지키는 결정이다.\n", p))
	}
	b.WriteString(core.String())
	inCore := map[string]bool{}
	for _, id := range inj.MemoryIDs {
		inCore[id] = true
	}
	var rules, decisions []memory.Memory
	// 묶음 핵심 층에 들지 않은 승격 기억(여러 제품 폴더의 제품 핵심): 찾는 곳 수에만 든다(검토 지적).
	pinnedRules, pinnedDecisions := 0, 0
	for _, m := range confirmed {
		// 분야 지식은 요청에 맞을 때 신호로만 넣는다. 이 묶음의 핵심 층에 든 것은 세지 않는다.
		if !inScope(m, pl) || memory.DomainOf(m) != "" || inCore[m.ID] {
			continue
		}
		if excluded(m, now, heads) != "" {
			continue
		}
		switch {
		case m.Pinned && m.Kind == memory.KindRule: // 규칙 채우기·결정 제목 줄에는 넣지 않는다(요청 회상으로 온다)
			pinnedRules++
		case m.Pinned && m.Kind == memory.KindDecision:
			pinnedDecisions++
		case m.Kind == memory.KindRule:
			rules = append(rules, m)
		case m.Kind == memory.KindDecision:
			decisions = append(decisions, m)
		}
	}
	footer := "기억은 " + Label + " MCP(memory_find·memory_get·memory_save)로 찾고 남긴다."
	head := "[" + Label + " 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다.\n"
	if opts.Brief {
		footer = "기억은 memory_find·memory_get·memory_save로 찾고 남긴다."
		head = "[" + Label + " 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다. 한 줄이 규칙 하나의 요지다. 일이 규칙에 닿으면 memory_get으로 전문을 읽고 따른다(요청과 맞는 규칙은 전문이 회상으로 온다).\n"
	}
	omittedLine := func(n int) string {
		return fmt.Sprintf("- 확정 규칙 %d개 더: 길이 때문에 여기 없다. 요청과 맞으면 회상으로 들어오고, memory_find로 찾는다.\n", n)
	}
	if cfg.Inject == config.InjectRules {
		// 요청 회상이 없는 판: 규칙이 들어올 다른 길이 없어 예전처럼 남는 칸을 확정 규칙으로 채운다(명세 2.7 예외).
		budget := MaxSessionStartRunes - sessionNoteRunes - runes(core.String()) - runes(head) - runes(omittedLine(len(rules)))
		if len(rules) > 0 {
			var body strings.Builder
			used := 0
			for _, m := range rules { // 최근에 고친 규칙부터
				entry := memory.CoreEntry(m)
				if opts.Brief {
					entry = fmt.Sprintf("- %s (%s)\n", briefTitle(m.Title), m.ID)
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
		return inj.done(&b)
	}
	// 상시 핵심 밖은 세션 시작에 넣지 않는다(S3-R6). 이 폴더에서 보이는 수와 먼저 찾을 곳만 알리고, 나머지는
	// 요청 회상·도구 직전·memory_find로 들어온다(S3-R7).
	find := ""
	if nr, nd := len(rules)+pinnedRules, len(decisions)+pinnedDecisions; nr+nd > 0 {
		find = fmt.Sprintf("["+Label+" 기억 · 먼저 찾는 곳] 이 폴더에서 보이는 확정 규칙 %d개·결정 %d개의 본문은 여기 없다(최근 결정 몇 개는 아래에 제목으로 있다). 일이 작업 방식·문서 위치·설계 선택 같은 규칙이나 결정에 닿으면 무엇보다 먼저 memory_find로 찾는다. 요청에 맞는 것은 회상으로 들어온다.\n",
			nr, nd)
		if opts.Brief {
			find = fmt.Sprintf("["+Label+" 기억 · 먼저 찾는 곳] 이 폴더의 확정 규칙 %d개·결정 %d개의 본문은 여기 없다. 일이 닿으면 먼저 memory_find로 찾는다.\n", nr, nd)
		}
	}
	// 핵심이 한도까지 차면 찾는 곳 줄은 짧은 꼴로 줄인다(합계 한도).
	if nr, nd := len(rules)+pinnedRules, len(decisions)+pinnedDecisions; find != "" && !opts.Brief &&
		runes(core.String())+runes(find)+runes(footer) > MaxSessionStartRunes-sessionNoteRunes {
		find = fmt.Sprintf("["+Label+" 기억 · 먼저 찾는 곳] 이 폴더의 확정 규칙 %d개·결정 %d개의 본문은 여기 없다. 일이 닿으면 먼저 memory_find로 찾는다.\n", nr, nd)
	}
	// 꼬리(결정 제목·자동 되돌림 줄)는 핵심과 찾는 곳 줄을 뺀 남는 자리 안에서만 쓴다(합계 한도).
	var tail strings.Builder
	room := MaxSessionStartRunes - sessionNoteRunes - runes(core.String()) - runes(find) - runes(footer)
	reverts := revertLine(s)
	if reverts != "" && runes(reverts)+1 <= room {
		room -= runes(reverts) + 1
	} else {
		reverts = ""
	}
	if n := cfg.RecentDecisions; n > 0 && len(decisions) > 0 {
		line := "[최근 결정] "
		var ids []string
		for i := 0; i < len(decisions) && i < n; i++ {
			entry := fmt.Sprintf("%s(%s)", decisions[i].Title, decisions[i].ID)
			if i > 0 {
				entry = " · " + entry
			}
			if runes(line)+runes(entry) > min(maxDecisionsRunes, room-1) {
				break
			}
			line += entry
			ids = append(ids, decisions[i].ID)
		}
		if len(ids) > 0 {
			tail.WriteString(line + "\n")
			inj.MemoryIDs = append(inj.MemoryIDs, ids...)
		}
	}
	if reverts != "" {
		tail.WriteString(reverts + "\n")
	}
	b.WriteString(find)
	b.WriteString(tail.String())
	if b.Len() > 0 {
		b.WriteString(footer)
	}
	return inj.done(&b)
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// revertLine says how many harmful automatic changes the daily tidy undid lately (효과 원장이
// 해로움으로 판정해 스스로 되돌림, A2). Nothing waits for the person, so it does not count
// proposals (설계 · orbit-mem 2판 18.6, 기획 · orbit-mem H4).
func revertLine(s *memory.Store) string {
	since := s.Now().Add(-72 * time.Hour).UnixMilli()
	if n := s.RecentAutoReverts(since); n > 0 {
		return fmt.Sprintf("해로움 판정으로 자동 변경 %d건을 되돌림 — 감사 목록(proposals status=audit)에서 확인", n)
	}
	return ""
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

// CandidatesAt is CandidatesFor at a place resolved by the product map. A memory of the session's core
// layers is in the bundle and left out like a session rule.
func CandidatesAt(s *memory.Store, pl place.Place, opts Options, bundle []string) (map[string]memory.Memory, map[string]string, error) {
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
		case !inScope(m, pl):
			out[m.ID] = "scope"
		case m.Kind == memory.KindJournal:
			out[m.ID] = "journal"
		case why != "":
			out[m.ID] = why
		case m.Kind == memory.KindRule && m.State == memory.StateConfirmed && !domain && !opts.Brief && inBundle[m.ID],
			m.Pinned && inBundle[m.ID]:
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
	pl := at(cfg, scope)
	byID, _, err := CandidatesAt(s, pl, opts, bundleIDs)
	if err != nil {
		return inj.done(&b)
	}
	news := ""
	if ok {
		news = sessionNews(s, &inj, host, session, bundleIDs, byID)
	}
	b.WriteString(news)
	recall := recallEntries(s, cfg, pl, prompt, byID, &inj, MaxHookRunes-runes(news))
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
	return "[" + Label + " 기억 · 이 세션 중 바뀐 것] 세션 시작 뒤 대체되거나 다른 세션에서 확정된 기억이다.\n" + lines.String()
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
func recallEntries(s *memory.Store, cfg config.Config, pl place.Place, prompt string, byID map[string]memory.Memory, inj *Injection, limit int) string {
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
	// 이 폴더의 것(저장소·제품·요청 글에 이름이 나온 제품)이 사용자 공통보다 앞이다(S3 명세 2.8).
	products := pl.Named(prompt)
	local := func(id string) bool { m := byID[id]; return pl.Local(m.Scope, m.Project, products) }
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
	head := "[" + Label + " 관련 기억] 필요하면 memory_get으로 본문을 읽는다.\n"
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

// SubagentStart is what a subagent gets when it starts (설계 · orbit-mem 2판 6절, S3 명세 2.9): the user
// core only, within memory.UserCoreRunes. A subagent's hook input carries no request, so nothing else is
// matched; the tools it calls bring their own memories (ToolInjection, keyed by the subagent).
func SubagentStart(s *memory.Store, cfg config.Config) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.Inject == config.InjectOff {
		return inj.done(&b)
	}
	core, err := s.Core(memory.ScopeGlobal)
	if err != nil || len(core) == 0 {
		return inj.done(&b)
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	var body strings.Builder
	used := 0
	for _, m := range core {
		if excluded(m, now, heads) != "" {
			continue
		}
		entry := memory.CoreEntry(m)
		if used+runes(entry) > memory.UserCoreRunes {
			continue
		}
		body.WriteString(entry)
		used += runes(entry)
		inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
		inj.mark(memory.MarkShown, m.ID)
	}
	if used == 0 {
		return inj.done(&b)
	}
	b.WriteString("[" + Label + " 기억 · 상시 핵심] 모든 일에 늘 지키는 사용자의 방식이다. 다른 기억은 memory_find로 찾는다.\n")
	b.WriteString(body.String())
	return inj.done(&b)
}
