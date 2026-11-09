// Package inject builds what the host hooks add to a session: the confirmed
// rules at session start, the few memories that strongly match a request (with
// what changed in the session), and the memories a tool triggers right before
// its call.
package inject

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

// Lengths (설계 · 지연 회상·호스트 계약·어댑터 4절 표, 수용 기준 L-4). The request hook keeps to MaxHookRunes:
// what changed in the session (B3, 3 lines and maxNewsRunes with its heading) and the work recall
// (B1, config.MaxPromptRecallChars or less). The session start, which stays for the whole session,
// keeps to MaxSessionStartRunes (D2) with the note lines saved in it (T-20). Rules that do not fit are
// not listed by title: they are counted in one line and come through the work recall when a request
// matches them.
const (
	MaxHookRunes         = 2500
	MaxSessionStartRunes = 6000
	// maxNewsLines and maxNewsRunes bound the correction and new memory lines of one request, heading included.
	maxNewsLines = 3
	maxNewsRunes = 300
	// newsTitleRunes is how much of a title a correction or new memory line shows.
	newsTitleRunes = 40
	// oldTitleRunes is how much of the superseded title a correction line shows, so the session knows what to drop.
	oldTitleRunes = 24
	// maxRecallLines is how many memory (or domain) lines the work recall of one request lists.
	maxRecallLines = 3
	// recallTitleRunes and recallSummaryRunes bound a work recall line's title and the one summary line.
	recallTitleRunes   = 80
	recallSummaryRunes = 160
	// minSummaryRunes is the shortest summary worth a line when the length runs short.
	minSummaryRunes = 20
	// minRecallWords is how many meaningful words a request needs for the work recall: one word scores
	// every memory that has it 1 on both measures, so it says too little (공개 묶음 old-weak-word).
	minRecallWords = 2
	// maxDecisionRunes bounds the recent decisions line of the session start (heading included), and
	// decisionTitleRunes one title in it: five titles of the usual length fit (1-6 b6r).
	maxDecisionRunes   = 500
	decisionTitleRunes = 60
)

// Headings of the request hook's two parts.
const (
	newsHead   = "[" + Label + " 기억 · 이 세션 중 바뀐 것] 옛 판은 따르지 않는다"
	recallHead = "[" + Label + " 관련 기억] 본문은 memory_get"
)

// Label is the name every memory context heading carries: the memory system's own name.
const Label = "orbit-mem"

// at is the place of a session working in scope, by the product map of cfg (S3 명세 2.2·2.3).
func at(cfg config.Config, scope string) place.Place { return place.Resolve(scope, cfg.Products) }

func inScope(m memory.Memory, pl place.Place) bool { return pl.Sees(m.Scope, m.Project) }

// Options are the choices of one injection that the settings do not carry. None is left: the brief
// form of the confirmed rules (Brief) is gone (설계 보완 T-27, 진행 결정 D3), and every host gets the
// full form. The type stays so the callers keep their shape.
type Options struct {
	// NewsOnly leaves the recall for the request out and keeps the session news
	// (correction and new memory lines): the request is a trivial one.
	NewsOnly bool
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
	return SessionBundleWith(s, cfg, scope, host, session, nil)
}

// SessionBundleWith is SessionBundle with the note lines of the session (the version, update and
// overlapping memory notes, 설계 보완 T-20). notes is called only when the bundle is built anew; its
// lines go at the end of the bundle and are saved with it, so a resumed session gets the same bytes.
// The bundle keeps to MaxSessionStartRunes with them. notes may be nil. With recall off no memory is
// put in, but the note lines still are (as with the old inject=off). A notes that panics leaves only
// the note lines out; the bundle is made and saved as usual (원칙 6).
func SessionBundleWith(s *memory.Store, cfg config.Config, scope, host, session string, notes func() []string) Injection {
	if session != "" {
		if text, ids, ok := s.SessionBundle(host, session); ok && runes(text) <= MaxSessionStartRunes {
			var b strings.Builder
			b.WriteString(text)
			return Injection{MemoryIDs: ids}.done(&b)
		} else if ok { // 한도 넘는 옛 묶음은 다시 만든다
			_ = s.ResetSession(host, session)
		}
	}
	note := noteText(noteLines(notes))
	limit := MaxSessionStartRunes
	if note != "" {
		limit -= runes(note) + 1 // 묶음과 안내 줄 사이 줄바꿈
	}
	inj := sessionStart(s, cfg, scope, limit)
	if note != "" {
		var b strings.Builder
		b.WriteString(inj.Text)
		for _, l := range strings.Split(note, "\n") { // 핵심이 길어 남는 자리가 모자라면 뒤 안내 줄부터 뺀다
			if runes(b.String())+1+runes(l) > MaxSessionStartRunes {
				break
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(l)
		}
		inj = inj.done(&b)
	}
	if session != "" { // 빈 묶음도 남긴다: 세션이 언제 시작했는지가 정정·새 기억 줄의 기준이다
		_ = s.SaveSessionBundle(host, session, inj.Text, inj.MemoryIDs)
	}
	return inj
}

// noteLines calls notes, if any; one that panics gives no lines.
func noteLines(notes func() []string) (lines []string) {
	if notes == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			lines = nil
		}
	}()
	return notes()
}

// noteText is the non-empty note lines, one per line.
func noteText(lines []string) string {
	var kept []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// SessionStart is the session-start context: the core layers, one line on where to look first and one
// line about harmful automatic changes undone lately.
func SessionStart(s *memory.Store, cfg config.Config, scope string) string {
	return SessionStartInjection(s, cfg, scope).Text
}

// SessionStartInjection is SessionStart with the memories it named.
func SessionStartInjection(s *memory.Store, cfg config.Config, scope string) Injection {
	return sessionStart(s, cfg, scope, MaxSessionStartRunes)
}

// SessionStartInjectionWith is SessionStartInjection; Options carries no choice any more.
func SessionStartInjectionWith(s *memory.Store, cfg config.Config, scope string, _ Options) Injection {
	return SessionStartInjection(s, cfg, scope)
}

// findLine says where to look first. It names no count, so it is the same all through a session
// (설계 · orbit-mem 지연 회상 7판 4절).
const findLine = "[" + Label + " 기억 · 먼저 찾는 곳] 규칙·결정이 일에 닿으면 memory_find로 먼저 찾는다.\n"

// sessionStart is the session start within limit runes (S3-R6, 7판 4절 A층): the core layers, the find
// line and the automatic-revert line. The other memories come through the request recall, the tool
// recall and memory_find (S3-R7). Only recall=on_demand, which has no request recall, fills the rest
// with confirmed rules in full as before. Nothing with recall=off.
func sessionStart(s *memory.Store, cfg config.Config, scope string, limit int) Injection {
	var inj Injection
	var b strings.Builder
	mode := cfg.RecallMode()
	if mode == config.RecallOff {
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
	// 묶음 핵심 층 밖에 이 폴더에서 보이는 확정 규칙·결정(여러 제품 폴더의 제품 핵심 포함)이 있는가: 찾는 곳 줄을 붙인다.
	others := false
	for _, m := range confirmed {
		// 분야 지식은 요청에 맞을 때 신호로만 넣는다. 이 묶음의 핵심 층에 든 것은 세지 않는다.
		if !inScope(m, pl) || memory.DomainOf(m) != "" || inCore[m.ID] {
			continue
		}
		if excluded(m, now, heads) != "" || (m.Kind != memory.KindRule && m.Kind != memory.KindDecision) {
			continue
		}
		others = true
		switch {
		case m.Pinned: // 핵심 층에 들지 않은 승격 기억(여러 제품 폴더의 제품 핵심)은 규칙 채우기·결정 제목 줄에 넣지 않는다
		case m.Kind == memory.KindRule:
			rules = append(rules, m)
		default:
			decisions = append(decisions, m) // 최근에 고친 것부터(List 순서)
		}
	}
	if mode == config.RecallOnDemand {
		// 요청 회상이 없는 판: 규칙이 들어올 다른 길이 없어 예전처럼 남는 칸을 확정 규칙 전문으로 채운다(명세 2.7 예외).
		head := "[" + Label + " 기억 · 확정 규칙] 이 PC에서 사용자가 확정한 일하는 방식이다.\n"
		omittedLine := func(n int) string {
			return fmt.Sprintf("- 확정 규칙 %d개 더: 길이 때문에 여기 없다. 요청과 맞으면 회상으로 들어오고, memory_find로 찾는다.\n", n)
		}
		budget := limit - runes(core.String()) - runes(head) - runes(omittedLine(len(rules)))
		if len(rules) > 0 {
			var body strings.Builder
			used := 0
			for _, m := range rules { // 최근에 고친 규칙부터
				entry := memory.CoreEntry(m)
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
	// 상시 핵심 밖 본문은 세션 시작에 넣지 않는다(S3-R6). 먼저 찾을 곳과 최근 결정의 제목만 알리고, 나머지는 요청 회상·
	// 도구 직전·memory_find로 들어온다(S3-R7). 「최근 결정」 줄은 1-6 b6r에서 제목 줄로만 다시 넣었다(87ab0fb의 고르는 법,
	// maxDecisionRunes 안). 찾는 곳 줄·결정 줄·꼬리 줄은 한도 안에 들 때만 쓴다.
	footer := "기억은 " + Label + " MCP(memory_find·memory_get·memory_save)로 찾고 남긴다."
	room := limit - runes(core.String()) - runes(footer)
	find := ""
	if core.Len() > 0 || others {
		find = findLine
	}
	if runes(find) > room {
		find = ""
	}
	room -= runes(find)
	reverts := revertLine(s)
	if reverts == "" || runes(reverts)+1 > room {
		reverts = ""
	} else {
		room -= runes(reverts) + 1
	}
	recent, recentIDs := recentDecisionsLine(decisions, cfg.RecentDecisions, room-1)
	b.WriteString(find)
	if recent != "" {
		b.WriteString(recent + "\n")
		inj.MemoryIDs = append(inj.MemoryIDs, recentIDs...)
	}
	if reverts != "" {
		b.WriteString(reverts + "\n")
	}
	if b.Len() > 0 && runes(b.String())+runes(footer) <= limit {
		b.WriteString(footer)
	}
	return inj.done(&b)
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// recentDecisionsLine is the titles and ids of up to n decisions, in the order given, on one line of at
// most min(maxDecisionRunes, room) runes, each title cut to decisionTitleRunes. No body: memory_get has it.
// It is "" when not even the first fits.
func recentDecisionsLine(decisions []memory.Memory, n, room int) (string, []string) {
	limit := min(maxDecisionRunes, room)
	line := "[" + Label + " 기억 · 최근 결정] 본문은 memory_get — "
	var ids []string
	for i := 0; i < len(decisions) && i < n; i++ {
		entry := fmt.Sprintf("%s(%s)", clipTitle(decisions[i].Title, decisionTitleRunes), decisions[i].ID)
		if i > 0 {
			entry = " · " + entry
		}
		if runes(line)+runes(entry) > limit {
			break
		}
		line += entry
		ids = append(ids, decisions[i].ID)
	}
	if len(ids) == 0 {
		return "", nil
	}
	return line, ids
}

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

// Prompt is the per-request context: the memories that pass both recall thresholds, ranked and
// packed within the work recall's length. A weak match adds nothing.
func Prompt(s *memory.Store, cfg config.Config, scope, prompt string) string {
	return PromptInjection(s, cfg, scope, prompt).Text
}

// PromptInjection is the work recall (설계 · 지연 회상·호스트 계약·어댑터 5.1) of a request outside a
// session: see PromptInjectionFor.
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
		// 일지는 대화 맥락이 아니다.
		switch why := excluded(m, now, heads); {
		case !inScope(m, pl):
			out[m.ID] = "scope"
		case m.Kind == memory.KindJournal:
			out[m.ID] = "journal"
		case why != "":
			out[m.ID] = why
		case inBundle[m.ID]: // 세션 묶음(A층)에 든 기억은 이 세션에 이미 들어갔다(설계 5.1 3단계)
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

// PromptInjectionFor is the request hook of a host session (설계 · 지연 회상·호스트 계약·어댑터 5.1, 4절 표).
// It tells the session, once, what changed since its bundle was made (B3, sessionNews): a memory of the
// session superseded meanwhile (a correction line) and memories confirmed elsewhere after the session
// started (new memory lines), 3 lines and maxNewsRunes at most. Then the work recall (B1, recallEntries):
// the memories that pass both thresholds, leaving out what the session already has (its bundle and what
// the work recall or the news told it; not what a tool call brought). The whole stays within
// MaxHookRunes. RecordShown keeps what it told; a compaction or /clear (ResetSession) forgets it.
func PromptInjectionFor(s *memory.Store, cfg config.Config, scope, prompt, host, session string, opts Options) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.RecallMode() != config.RecallAuto || (strings.TrimSpace(prompt) == "" && !opts.NewsOnly) {
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
	if !opts.NewsOnly {
		b.WriteString(recallEntries(s, cfg, pl, prompt, byID, &inj, toldInSession(s, host, session), idSet(bundleIDs), MaxHookRunes-runes(news)))
	}
	return inj.done(&b)
}

// toldInSession are the memories the session was given by an earlier work recall or session news line.
// What a tool call brought is not among them: a request that asks about it still recalls it (공개 묶음 st-3).
func toldInSession(s *memory.Store, host, session string) map[string]bool {
	told := map[string]bool{}
	for _, kind := range []string{memory.MarkShown, memory.MarkNew, memory.MarkCorrection} {
		for id := range s.SessionMarks(host, session, kind) {
			told[id] = true
		}
	}
	return told
}

// idSet is ids as a set.
func idSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// clipTitle folds a title's spaces and cuts it to n characters at most (an ellipsis included).
func clipTitle(title string, n int) string {
	r := []rune(strings.Join(strings.Fields(title), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

// sessionNews is the correction and new memory lines of a session (B3), heading included, or "".
// Lines that do not fit wait for a later request: only what was told is marked.
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
	head := newsHead + "\n"
	var lines strings.Builder
	used, n := runes(head), 0
	add := func(line string) bool {
		if n == maxNewsLines || used+runes(line) > maxNewsRunes {
			return false
		}
		lines.WriteString(line)
		used += runes(line)
		n++
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
		if !add(fmt.Sprintf("- 정정: 「%s」(%s) → %s — %s\n", clipTitle(old.Title, oldTitleRunes), old.ID, clipTitle(cur.Title, newsTitleRunes), cur.ID)) {
			break
		}
		inj.mark(memory.MarkCorrection, old.ID)
		inj.mark(memory.MarkShown, cur.ID) // 바뀐 판은 이 세션에 들어갔다: 요청 회상이 다시 내지 않는다
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
	for _, m := range fresh {
		if !add(fmt.Sprintf("- 새 기억: %s — %s · %s 확정\n", clipTitle(m.Title, newsTitleRunes), m.ID, kindName(m.Kind))) {
			break
		}
		inj.mark(memory.MarkNew, m.ID)
		inj.MemoryIDs = append(inj.MemoryIDs, m.ID)
		named[m.ID] = true
	}
	if n == 0 {
		return ""
	}
	return head + lines.String()
}

// NewsShare is the session news (B3) share of a request hook's text and ids (PromptInjectionFor): the ids its
// lines name and the characters of the block. The block leads the text and names one memory a line, in the order
// of the ids; the work recall (B1) follows it. A text with no news gives none. A measure that tells the news
// apart from the recall uses it (internal/bench, 1-6 b6r 3차).
func NewsShare(text string, ids []string) ([]string, int) {
	if !strings.HasPrefix(text, newsHead) {
		return nil, 0
	}
	block := text
	if i := strings.Index(text, "\n"+recallHead); i >= 0 {
		block = text[:i]
	}
	n := 0
	for _, line := range strings.Split(block, "\n")[1:] {
		if strings.HasPrefix(line, "- ") {
			n++
		}
	}
	return slices.Clone(ids[:min(n, len(ids))]), runes(block)
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

// thresholds are the gate of a recall (설계 5.1 4단계, 설계 보완 1-6 b6).
//
//   - 요청 회상(B1, R1): 찾기 점수 ≥ score, 맞은 비율 ≥ cover(맞은 낱말이 둘 이상) 또는 ≥ coverOne(하나뿐), 1위 점수의
//     relative배 이상. 한 낱말 문턱을 따로 두는 까닭: 맞은 비율은 낱말 수에 따라 1/3·1/4처럼 띄엄띄엄한 값이고, 「낱말 넷 중
//     흔한 낱말 하나」는 주제 밖 요청과 가를 수 없다.
//   - 위임 회상(T1): 위임 글은 길고 여러 주제라 맞은 비율을 쓰지 않는다. 시키는 말을 뺀 내용 낱말(memory.TaskWords)
//     minWords개 이상이 맞고, 찾기 점수 ≥ score, 1위 점수의 relative배 이상(5개까지 넣으니 둘째·셋째 주제를 살리게 낮다).
//     그리고 제목 몫(memory.RecallHit.TitleShare, 맞은 낱말 가운데 기억의 제목·주제에 나온 드문 낱말 몫을 내용 낱말 수로
//     나눈 값) ≥ title(설계 보완, 1-6 b7f): 긴 위임 글에서는 아무 기억이나 낱말 서넛이 맞으니, 그 기억이 무엇인지를 말하는
//     자리에 드문 낱말이 위임 글 길이만큼 맞아야 그 기억에 대한 일로 본다. 맞는 것이 없으면 넣지 않는다(역할 지침 제목 줄만).
type thresholds struct {
	score, cover, coverOne, relative, title float64
	minWords                                int
	task                                    bool
}

// requestThresholds are the thresholds of the work recall of a request (B1), the defaults where the settings carry none.
func requestThresholds(cfg config.Config) thresholds {
	score, cover := recallThresholds(cfg)
	return thresholds{score: score, cover: cover, coverOne: orDefault(cfg.RecallMinCoverageOneWord, config.DefaultRecallMinCoverageOneWord),
		relative: recallRelative(cfg)}
}

// taskThresholds are the thresholds of the recall of a delegated task (T1), the defaults where the settings carry none.
func taskThresholds(cfg config.Config) thresholds {
	words := cfg.TaskRecallMinWords
	if words <= 0 {
		words = config.DefaultTaskRecallMinWords
	}
	return thresholds{score: orDefault(cfg.TaskRecallMinScore, config.DefaultTaskRecallMinScore), minWords: words,
		relative: orDefault(cfg.TaskRecallMinRelative, config.DefaultTaskRecallMinRelative),
		title:    orDefault(cfg.TaskRecallMinTitleShare, config.DefaultTaskRecallMinTitleShare), task: true}
}

func orDefault(v, d float64) float64 {
	if v <= 0 {
		return d
	}
	return v
}

// pass says whether a hit passes the gate, before the relative floor.
func (th thresholds) pass(h memory.RecallHit) bool {
	if h.Score <= 0 || h.Score < th.score {
		return false
	}
	if th.task {
		return len(h.Matched) >= th.minWords && h.TitleShare >= th.title
	}
	return h.Coverage >= th.coverFor(len(h.Matched))
}

// coverFor is the share-of-words threshold of a request hit with n matched words.
func (th thresholds) coverFor(n int) float64 {
	if n == 1 {
		return th.coverOne
	}
	return th.cover
}

// words are the words of text the recall matches, as s's recall takes them (memory.Store.RecallWords): a
// request's meaningful words, a task's content words. When the store cannot be read they are the words
// without it (the recall fails then anyway).
func (th thresholds) words(s *memory.Store, text string) []string {
	var words []string
	var err error
	if th.task {
		if words, err = s.TaskRecallWords(text); err != nil {
			return memory.TaskWords(text)
		}
		return words
	}
	if words, err = s.RecallWords(text); err != nil {
		return memory.MeaningfulWords(text)
	}
	return words
}

// hits are the memories in states that text matches, measured as this recall measures them.
func (th thresholds) hits(s *memory.Store, text string, states ...string) ([]memory.RecallHit, error) {
	if th.task {
		return s.TaskRecallHitsIn(text, states...)
	}
	return s.RecallHitsIn(text, states...)
}

// recallThresholds are the score and share thresholds of the work recall of a request (matched words two or
// more), the defaults where the settings carry none.
func recallThresholds(cfg config.Config) (score, cover float64) {
	return orDefault(cfg.RecallMinScore, config.DefaultRecallMinScore), orDefault(cfg.RecallMinCoverage, config.DefaultRecallMinCoverage)
}

// recallRelative is the relative threshold of the work recall (설계 보완, 1-6 b6), the default where the
// settings carry none.
func recallRelative(cfg config.Config) float64 {
	return orDefault(cfg.RecallMinRelative, config.DefaultRecallMinRelative)
}

// recallHits are the memories of byID, less those the session was told, that pass both thresholds
// (설계 5.1 3·4단계) and the relative floor (recallFloor), best first: confirmed before candidates (A2),
// then find score, share of the request's words, this place's own before the user's (S3 명세 2.8), age
// (newer first), net usage (followed − irrelevant) over the ledger window, last edit, id.
func recallHits(s *memory.Store, cfg config.Config, th thresholds, pl place.Place, prompt string, byID map[string]memory.Memory, told, held map[string]bool) []memory.RecallHit {
	return recallHitsOf(s, cfg, th, pl, prompt, th.found(s, prompt), byID, told, held)
}

// recallHitsOf is recallHits over found, the live memories prompt matched (thresholds.found).
func recallHitsOf(s *memory.Store, cfg config.Config, th thresholds, pl place.Place, prompt string, found []memory.RecallHit, byID map[string]memory.Memory, told, held map[string]bool) []memory.RecallHit {
	hits, floor := floorOf(th, found, byID, held)
	kept := hits[:0]
	for _, h := range hits {
		if !told[h.Memory.ID] && h.Score >= floor {
			kept = append(kept, h)
		}
	}
	return rankHits(s, cfg, pl, prompt, kept)
}

// recallFloor is the hits of byID that pass the gate (thresholds.pass), and the relative floor (설계 보완, 1-6 b6):
// recallMinRelative times the best of their find scores, less a rounding margin. The best counts the memories
// the session was already told, and those its bundle holds (held, 1-6 b6r): a memory weak next to the best
// match of the request stays out when that one was told before or is in the bundle (L-2: the same request
// again is 0 characters).
func recallFloor(s *memory.Store, th thresholds, prompt string, byID map[string]memory.Memory, held map[string]bool) ([]memory.RecallHit, float64) {
	return floorOf(th, th.found(s, prompt), byID, held)
}

// found are the live memories prompt matches, as this recall measures them: none when prompt has fewer words
// than the recall needs or the find fails. One find serves the recall and its old-version count (oldVersions).
func (th thresholds) found(s *memory.Store, prompt string) []memory.RecallHit {
	if len(th.words(s, prompt)) < max(minRecallWords, th.minWords) {
		return nil
	}
	found, err := th.hits(s, prompt)
	if err != nil {
		return nil
	}
	return found
}

// floorOf is recallFloor over found (thresholds.found). A held memory that passes the gate counts toward the
// best but is not among the hits.
func floorOf(th thresholds, found []memory.RecallHit, byID map[string]memory.Memory, held map[string]bool) ([]memory.RecallHit, float64) {
	var hits []memory.RecallHit
	top := 0.0
	for _, h := range found {
		m, ok := byID[h.Memory.ID]
		if !ok && held[h.Memory.ID] && th.pass(h) {
			top = max(top, h.Score)
		}
		if !ok || !th.pass(h) {
			continue
		}
		h.Memory = m
		hits = append(hits, h)
		top = max(top, h.Score)
	}
	return hits, th.relative*top - 1e-9 // 같은 값의 부동소수점 오차로 빠지지 않게
}

// rankHits orders the hits of a recall, best first (see recallHits).
func rankHits(s *memory.Store, cfg config.Config, pl place.Place, prompt string, hits []memory.RecallHit) []memory.RecallHit {
	if len(hits) == 0 {
		return nil
	}
	now := s.Now()
	usage, _ := s.UsageCounts(memory.Day(now.AddDate(0, 0, -max(cfg.LedgerWindowDays, 1))), memory.Day(now))
	net := func(id string) int { u := usage[id]; return u.Followed - u.Irrelevant }
	products := pl.Named(prompt)
	local := func(m memory.Memory) bool { return pl.Local(m.Scope, m.Project, products) }
	key := func(x float64) float64 { return math.Round(x * 1e9) } // 같은 값의 부동소수점 오차가 순위를 가르지 않게
	sort.SliceStable(hits, func(i, j int) bool {
		a, c := hits[i].Memory, hits[j].Memory
		if ca, cc := a.State == memory.StateConfirmed, c.State == memory.StateConfirmed; ca != cc {
			return ca
		}
		if sa, sc := key(hits[i].Score), key(hits[j].Score); sa != sc {
			return sa > sc
		}
		if va, vc := key(hits[i].Coverage), key(hits[j].Coverage); va != vc {
			return va > vc
		}
		if la, lc := local(a), local(c); la != lc {
			return la
		}
		if wa, wc := memory.AgeWeight(a, now), memory.AgeWeight(c, now); wa != wc {
			return wa > wc
		}
		if na, nc := net(a.ID), net(c.ID); na != nc {
			return na > nc
		}
		if a.UpdatedAt != c.UpdatedAt {
			return a.UpdatedAt > c.UpdatedAt
		}
		return a.ID < c.ID
	})
	return hits
}

// recallLine is a work recall line: title (80 characters at most), id, kind and state, with the marks of
// a memory past its review day and of one whose sources are all untrusted (D7).
func recallLine(m memory.Memory, now time.Time) string {
	state := "확정"
	if m.State == memory.StateCandidate {
		state = "후보"
	}
	if m.Freshness(now) == memory.FreshReviewDue {
		state += " · 재검토일 지남"
	}
	if note := memory.TrustNoteIn(i18n.KO, m); note != "" {
		state += " · " + note
	}
	return fmt.Sprintf("- %s — %s · %s %s\n", clipTitle(m.Title, recallTitleRunes), m.ID, kindName(m.Kind), state)
}

// recallEntries is the work recall of a request (B1, 설계 5.1 5단계): the heading, then at most
// maxRecallLines lines in rank order, a domain's memories as one line that counts its signals and says
// how to find them, and under the first memory line one summary line (its body's start). All of it
// stays within cfg.PromptRecallChars and limit. Memories already named in this injection or told in
// the session are left out; what it lists is marked shown.
func recallEntries(s *memory.Store, cfg config.Config, pl place.Place, prompt string, byID map[string]memory.Memory, inj *Injection, told, held map[string]bool, limit int) string {
	skip := map[string]bool{}
	for id := range told {
		skip[id] = true
	}
	for _, id := range inj.MemoryIDs {
		skip[id] = true
	}
	hits := recallHits(s, cfg, requestThresholds(cfg), pl, prompt, byID, skip, held)
	if len(hits) == 0 {
		return ""
	}
	// 분야 지식은 본문 대신 분야 한 줄(가장 맞은 기억 하나의 제목·id)로 든다(분야 순서는 그 분야의 가장 앞선 기억 자리).
	type item struct {
		m      memory.Memory
		domain string
		ids    []string
	}
	var items []item
	domainAt := map[string]int{}
	for _, h := range hits {
		d := memory.DomainOf(h.Memory)
		if d == "" {
			items = append(items, item{m: h.Memory})
			continue
		}
		if i, ok := domainAt[d]; ok {
			items[i].ids = append(items[i].ids, h.Memory.ID)
			continue
		}
		domainAt[d] = len(items)
		items = append(items, item{domain: d, ids: []string{h.Memory.ID}})
	}
	budget := min(cfg.PromptRecallChars(), limit)
	head := recallHead + "\n"
	used := runes(head)
	now := s.Now()
	var lines []string
	first := -1 // 요약을 붙일 첫 기억 줄
	var top memory.Memory
	for _, it := range items {
		if len(lines) == maxRecallLines {
			break
		}
		var line string
		if it.domain != "" {
			n := 0
			for _, m := range byID {
				if memory.DomainOf(m) == it.domain {
					n++
				}
			}
			best := byID[it.ids[0]] // 그 분야에서 가장 앞선(가장 맞은) 기억: 제목과 id만, 본문은 넣지 않는다
			line = fmt.Sprintf("- 분야 「%s」 신호 %d개 · 가장 맞은 것 「%s」 %s — memory_find tags=%s%s\n",
				it.domain, n, clipTitle(best.Title, newsTitleRunes), best.ID, memory.DomainPrefix, it.domain)
		} else {
			line = recallLine(it.m, now)
		}
		if used+runes(line) > budget {
			break
		}
		used += runes(line)
		lines = append(lines, line)
		if it.domain != "" {
			inj.MemoryIDs = append(inj.MemoryIDs, it.ids[0]) // 줄이 제목과 id를 든 기억
			inj.mark(memory.MarkShown, it.ids...)            // 분야 줄이 가리킨 기억: 같은 세션에 같은 줄을 다시 내지 않는다
			continue
		}
		inj.MemoryIDs = append(inj.MemoryIDs, it.m.ID)
		inj.mark(memory.MarkShown, it.m.ID)
		if first < 0 {
			first, top = len(lines)-1, it.m
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if first >= 0 {
		// 가장 확실한 기억 하나만 요약 한 줄(본문 앞부분, 확정 규칙이면 규칙 자체)을 남는 글자 수 안에서 붙인다.
		room := min(recallSummaryRunes, budget-used-3)
		if text := strings.Join(strings.Fields(top.Body), " "); text != "" && room >= minSummaryRunes {
			lines[first] += "  " + clipTitle(text, room) + "\n"
		}
	}
	return head + strings.Join(lines, "")
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
