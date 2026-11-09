package legacy

// A copy of the recall path of internal/inject/inject.go at 87ab0fb in the form the hooks of that day
// used it: inject=full and the full (not brief) form, so the branches of the other levels and of the
// brief form are not copied. Each function names the lines it was copied from.

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

// The budgets and the policy of 87ab0fb, frozen.
const (
	label                = "orbit-mem"    // internal/inject/inject.go:47 at 87ab0fb (Label)
	maxHookRunes         = 9000           // internal/inject/inject.go:28 at 87ab0fb
	maxSessionStartRunes = 6000           // internal/inject/inject.go:29 at 87ab0fb
	sessionNoteRunes     = 600            // internal/inject/inject.go:32 at 87ab0fb
	maxDecisionsRunes    = 1000           // internal/inject/inject.go:34 at 87ab0fb
	maxToolRunes         = 1000           // internal/inject/inject.go:36 at 87ab0fb
	maxToolMemories      = 3              // internal/inject/inject.go:37 at 87ab0fb
	maxNewMemories       = 3              // internal/inject/inject.go:39 at 87ab0fb
	maxSessionNewsRunes  = 2000           // internal/inject/inject.go:41 at 87ab0fb
	moreLineRunes        = 60             // internal/inject/inject.go:43 at 87ab0fb
	ruleBodyRunes        = 600            // internal/inject/inject.go:352 at 87ab0fb
	recallBodyRunes      = 200            // internal/inject/inject.go:356 at 87ab0fb
	revertWindow         = 72 * time.Hour // internal/inject/inject.go:337 at 87ab0fb
	recentDecisions      = 5              // internal/config/config.go:109 at 87ab0fb (Default RecentDecisions)
	recallChars          = 1000           // internal/config/config.go:110 at 87ab0fb (Default RecallChars)
	ledgerWindowDays     = 7              // internal/config/config.go:110 at 87ab0fb (Default LedgerWindowDays)
	userCoreRunes        = 3000           // internal/memory/core.go:20 at 87ab0fb (UserCoreRunes)
	productCoreRunes     = 2000           // internal/memory/core.go:21 at 87ab0fb (ProductCoreRunes)
)

// Why a live memory is kept out of recall: internal/inject/inject.go:66-69 at 87ab0fb.
const (
	excludedExpired    = "expired"
	excludedSuperseded = "superseded_by_topic"
)

// Freshness of a memory: internal/memory/lifecycle.go:33-35 at 87ab0fb (FreshOK·FreshExpired·FreshReviewDue).
const (
	freshOK        = ""
	freshExpired   = "expired"
	freshReviewDue = "review_due"
)

// placeAt is internal/inject/inject.go:50 at 87ab0fb (at): the place of a session, by the product map.
func placeAt(products place.Map, scope string) place.Place { return place.Resolve(scope, products) }

// inScope is internal/inject/inject.go:52 at 87ab0fb.
func inScope(m memory.Memory, pl place.Place) bool { return pl.Sees(m.Scope, m.Project) }

// freshness is internal/memory/lifecycle.go:98-108 at 87ab0fb (Memory.Freshness).
func freshness(m memory.Memory, now time.Time) string {
	t := now.UnixMilli()
	switch {
	case m.ExpiresAt > 0 && t >= m.ExpiresAt:
		return freshExpired
	case m.ReviewAt > 0 && t >= m.ReviewAt:
		return freshReviewDue
	}
	return freshOK
}

// triggeredBy is internal/memory/supersede.go:105-115 at 87ab0fb (Memory.TriggeredBy).
func triggeredBy(m memory.Memory, tool string) (exact, ok bool) {
	for _, t := range m.Tools {
		if t == tool {
			return true, true
		}
		if p, isPrefix := strings.CutSuffix(t, "*"); isPrefix && strings.HasPrefix(tool, p) {
			ok = true
		}
	}
	return false, ok
}

// coreLimit is internal/memory/core.go:26-34 at 87ab0fb (CoreLimit).
func coreLimit(scope string) int {
	switch {
	case scope == memory.ScopeGlobal:
		return userCoreRunes
	case place.ProductOf(scope) != "":
		return productCoreRunes
	}
	return 0
}

// excluded is internal/inject/inject.go:72-80 at 87ab0fb.
func excluded(m memory.Memory, now time.Time, heads memory.TopicHeads) string {
	if freshness(m, now) == freshExpired {
		return excludedExpired
	}
	if heads.Superseding(m) != "" {
		return excludedSuperseded
	}
	return ""
}

// injection is internal/inject/inject.go:88-95 at 87ab0fb (Injection, without Omitted, which only the
// rules level filled).
type injection struct {
	text      string
	memoryIDs []string
	chars     int
	marks     map[string][]string
	// news and newsChars are the session news share of a request hook (the first news ids, its characters):
	// not in 87ab0fb, kept for the measure that tells the news apart from the recall (1-6 b6r 3차).
	news      int
	newsChars int
}

// mark is internal/inject/inject.go:97-102 at 87ab0fb.
func (in *injection) mark(kind string, ids ...string) {
	if in.marks == nil {
		in.marks = map[string][]string{}
	}
	in.marks[kind] = append(in.marks[kind], ids...)
}

// recordShown is internal/inject/inject.go:106-110 at 87ab0fb (RecordShown).
func recordShown(s *memory.Store, host, session string, in injection) {
	for kind, ids := range in.marks {
		_ = s.MarkSession(host, session, kind, ids...)
	}
}

// done is internal/inject/inject.go:116-123 at 87ab0fb.
func (in injection) done(b *strings.Builder) injection {
	in.text = strings.TrimSpace(b.String())
	in.chars = utf8.RuneCountInString(in.text)
	if in.memoryIDs == nil {
		in.memoryIDs = []string{}
	}
	return in
}

// sessionBundle is internal/inject/inject.go:135-150 at 87ab0fb (SessionBundleWith, full form).
func sessionBundle(s *memory.Store, products place.Map, scope, host, session string) injection {
	if session != "" {
		if text, ids, ok := s.SessionBundle(host, session); ok && runes(text) <= maxSessionStartRunes-sessionNoteRunes {
			var b strings.Builder
			b.WriteString(text)
			return injection{memoryIDs: ids}.done(&b)
		} else if ok { // 0.10.0이 남긴 한도 넘는 묶음은 다시 만든다
			_ = s.ResetSession(host, session)
		}
	}
	inj := sessionStart(s, products, scope)
	if session != "" { // 빈 묶음도 남긴다: 세션이 언제 시작했는지가 정정·새 기억 줄의 기준이다
		_ = s.SaveSessionBundle(host, session, inj.text, inj.memoryIDs)
	}
	return inj
}

// sessionStart is internal/inject/inject.go:169-329 at 87ab0fb (SessionStartInjectionWith with
// inject=full and the full form): the core layers, the line on where to look first, the titles of recent
// decisions and the automatic-revert line. The inject=rules branch (inject.go:248-276) and the brief
// texts (inject.go:241-244, 283-285) are not copied.
func sessionStart(s *memory.Store, products place.Map, scope string) injection {
	var inj injection
	var b strings.Builder
	confirmed, err := s.List(memory.StateConfirmed)
	if err != nil {
		return inj.done(&b)
	}
	pl := placeAt(products, scope)
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
		used, limit := 0, coreLimit(scope)
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
			inj.memoryIDs = append(inj.memoryIDs, m.ID)
		}
		if used > 0 {
			core.WriteString(heading)
			core.WriteString(body.String())
		}
	}
	layer(memory.ScopeGlobal, "["+label+" 기억 · 상시 핵심] 모든 일에 늘 지키는 사용자의 방식이다.\n")
	if p := pl.Product(); p != "" {
		layer("product:"+p, fmt.Sprintf("["+label+" 기억 · %s 핵심] 이 제품에서 늘 지키는 결정이다.\n", p))
	}
	b.WriteString(core.String())
	inCore := map[string]bool{}
	for _, id := range inj.memoryIDs {
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
	footer := "기억은 " + label + " MCP(memory_find·memory_get·memory_save)로 찾고 남긴다."
	// 상시 핵심 밖은 세션 시작에 넣지 않는다(S3-R6). 이 폴더에서 보이는 수와 먼저 찾을 곳만 알리고, 나머지는
	// 요청 회상·도구 직전·memory_find로 들어온다(S3-R7).
	find := ""
	if nr, nd := len(rules)+pinnedRules, len(decisions)+pinnedDecisions; nr+nd > 0 {
		find = fmt.Sprintf("["+label+" 기억 · 먼저 찾는 곳] 이 폴더에서 보이는 확정 규칙 %d개·결정 %d개의 본문은 여기 없다(최근 결정 몇 개는 아래에 제목으로 있다). 일이 작업 방식·문서 위치·설계 선택 같은 규칙이나 결정에 닿으면 무엇보다 먼저 memory_find로 찾는다. 요청에 맞는 것은 회상으로 들어온다.\n",
			nr, nd)
	}
	// 핵심이 한도까지 차면 찾는 곳 줄은 짧은 꼴로 줄인다(합계 한도).
	if nr, nd := len(rules)+pinnedRules, len(decisions)+pinnedDecisions; find != "" &&
		runes(core.String())+runes(find)+runes(footer) > maxSessionStartRunes-sessionNoteRunes {
		find = fmt.Sprintf("["+label+" 기억 · 먼저 찾는 곳] 이 폴더의 확정 규칙 %d개·결정 %d개의 본문은 여기 없다. 일이 닿으면 먼저 memory_find로 찾는다.\n", nr, nd)
	}
	// 꼬리(결정 제목·자동 되돌림 줄)는 핵심과 찾는 곳 줄을 뺀 남는 자리 안에서만 쓴다(합계 한도).
	var tail strings.Builder
	room := maxSessionStartRunes - sessionNoteRunes - runes(core.String()) - runes(find) - runes(footer)
	reverts := revertLine(s)
	if reverts != "" && runes(reverts)+1 <= room {
		room -= runes(reverts) + 1
	} else {
		reverts = ""
	}
	if n := recentDecisions; n > 0 && len(decisions) > 0 {
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
			inj.memoryIDs = append(inj.memoryIDs, ids...)
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

// runes is internal/inject/inject.go:331 at 87ab0fb.
func runes(s string) int { return utf8.RuneCountInString(s) }

// revertLine is internal/inject/inject.go:336-342 at 87ab0fb.
func revertLine(s *memory.Store) string {
	since := s.Now().Add(-revertWindow).UnixMilli()
	if n := s.RecentAutoReverts(since); n > 0 {
		return fmt.Sprintf("해로움 판정으로 자동 변경 %d건을 되돌림 — 감사 목록(proposals status=audit)에서 확인", n)
	}
	return ""
}

// candidatesAt is internal/inject/inject.go:370-401 at 87ab0fb (CandidatesAt, full form).
func candidatesAt(s *memory.Store, pl place.Place, bundle []string) (map[string]memory.Memory, map[string]string, error) {
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
		case m.Kind == memory.KindRule && m.State == memory.StateConfirmed && !domain && inBundle[m.ID],
			m.Pinned && inBundle[m.ID]:
			out[m.ID] = "session_rule"
		default:
			byID[m.ID] = m
		}
	}
	return byID, out, nil
}

// promptInjectionFor is internal/inject/inject.go:414-437 at 87ab0fb (PromptInjectionFor with
// inject=full and the full form): the session's news, then the work recall, within maxHookRunes.
func promptInjectionFor(s *memory.Store, products place.Map, scope, prompt, host, session string) injection {
	var inj injection
	var b strings.Builder
	if strings.TrimSpace(prompt) == "" {
		return inj.done(&b)
	}
	_, bundleIDs, ok := s.SessionBundle(host, session)
	if !ok { // 묶음을 모르면 지금 세션 시작이 넣을 것으로 본다
		bundleIDs = sessionStart(s, products, scope).memoryIDs
	}
	pl := placeAt(products, scope)
	byID, _, err := candidatesAt(s, pl, bundleIDs)
	if err != nil {
		return inj.done(&b)
	}
	news := ""
	if ok {
		news = sessionNews(s, &inj, host, session, bundleIDs, byID)
	}
	b.WriteString(news)
	inj.news, inj.newsChars = len(inj.memoryIDs), runes(strings.TrimSpace(news))
	recall := recallEntries(s, pl, prompt, byID, &inj, maxHookRunes-runes(news))
	b.WriteString(recall)
	return inj.done(&b)
}

// sessionNews is internal/inject/inject.go:440-521 at 87ab0fb.
func sessionNews(s *memory.Store, inj *injection, host, session string, bundleIDs []string, byID map[string]memory.Memory) string {
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
		inj.memoryIDs = append(inj.memoryIDs, cur.ID)
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
		inj.memoryIDs = append(inj.memoryIDs, m.ID)
		named[m.ID] = true
	}
	if used == 0 {
		return ""
	}
	return "[" + label + " 기억 · 이 세션 중 바뀐 것] 세션 시작 뒤 대체되거나 다른 세션에서 확정된 기억이다.\n" + lines.String()
}

// liveSuccessor is internal/inject/inject.go:525-541 at 87ab0fb.
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

// recallEntries is internal/inject/inject.go:545-680 at 87ab0fb. One change: the members of a field
// pack are ordered by id when their UpdatedAt ties (inject.go:648 sorted only by UpdatedAt over a map,
// so ties came in a random order); the order kept is one 87ab0fb could give, and the baseline stays fixed.
func recallEntries(s *memory.Store, pl place.Place, prompt string, byID map[string]memory.Memory, inj *injection, limit int) string {
	named := map[string]bool{}
	for _, id := range inj.memoryIDs {
		named[id] = true
	}
	docs := make([]doc, 0, len(byID))
	for _, m := range byID {
		if !named[m.ID] {
			docs = append(docs, docOf(m))
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	hits := strong(docs, prompt)
	if len(hits) == 0 {
		return ""
	}
	now := s.Now()
	usage, _ := s.UsageCounts(memory.Day(now.AddDate(0, 0, -max(ledgerWindowDays, 1))), memory.Day(now))
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
	budget := recallChars
	budget = min(budget, limit-moreLineRunes) // 분야 신호의 「N개 중 M개만 보임」 줄 몫
	head := "[" + label + " 관련 기억] 필요하면 memory_get으로 본문을 읽는다.\n"
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
		if freshness(m, now) == freshReviewDue {
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
		inj.memoryIDs = append(inj.memoryIDs, m.ID)
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
		sort.Slice(members, func(i, j int) bool {
			if members[i].UpdatedAt != members[j].UpdatedAt {
				return members[i].UpdatedAt > members[j].UpdatedAt
			}
			return members[i].ID < members[j].ID // 사본만: 같은 시각은 id 순(원본은 map 순서)
		})
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
			inj.memoryIDs = append(inj.memoryIDs, m.ID)
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

// toolInjection is internal/inject/inject.go:686-765 at 87ab0fb (ToolInjection with inject=full).
func toolInjection(s *memory.Store, products place.Map, scope, host, session, tool string) injection {
	var inj injection
	var b strings.Builder
	if strings.TrimSpace(tool) == "" {
		return inj.done(&b)
	}
	active, err := s.Triggered()
	if err != nil || len(active) == 0 {
		return inj.done(&b)
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	given := s.SessionMarks(host, session, memory.MarkTool)
	pl := placeAt(products, scope)
	type hit struct {
		m     memory.Memory
		exact bool
	}
	var hits []hit
	for _, m := range active {
		if len(m.Tools) == 0 || given[m.ID] || !inScope(m, pl) || m.Kind == memory.KindJournal || excluded(m, now, heads) != "" {
			continue
		}
		if exact, ok := triggeredBy(m, tool); ok {
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
		if la, lc := pl.Local(a.m.Scope, a.m.Project, nil), pl.Local(c.m.Scope, c.m.Project, nil); la != lc {
			return la
		}
		return a.m.UpdatedAt > c.m.UpdatedAt
	})
	head := fmt.Sprintf("["+label+" 기억 · %s 호출 직전] 이 도구에 걸린 기억이다. 부르기 전에 확인하고 따른다.\n", tool)
	used := runes(head)
	var entries strings.Builder
	for _, h := range hits {
		if len(inj.memoryIDs) == maxToolMemories {
			break
		}
		m := h.m
		state := "확정"
		if m.State == memory.StateCandidate {
			state = "후보"
		}
		line := fmt.Sprintf("- %s (%s · %s %s)\n", m.Title, m.ID, kindName(m.Kind), state)
		if used+runes(line) > maxToolRunes {
			break
		}
		if text := strings.Join(strings.Fields(m.Body), " "); text != "" {
			room := min(ruleBodyRunes, maxToolRunes-used-runes(line)-3)
			if room > 20 {
				line += "  " + memory.ClipRunes(text, room) + "\n"
			}
		}
		if used+runes(line) > maxToolRunes {
			continue
		}
		entries.WriteString(line)
		used += runes(line)
		inj.memoryIDs = append(inj.memoryIDs, m.ID)
		inj.mark(memory.MarkTool, m.ID)
	}
	if len(inj.memoryIDs) == 0 {
		return inj.done(&b)
	}
	b.WriteString(head)
	b.WriteString(entries.String())
	return inj.done(&b)
}

// kindName is internal/inject/inject.go:767-769 at 87ab0fb.
func kindName(k string) string {
	return map[string]string{memory.KindRule: "규칙", memory.KindFact: "사실", memory.KindDecision: "결정", memory.KindLesson: "교훈", memory.KindJournal: "일지"}[k]
}

// subagentStart is internal/inject/inject.go:784-817 at 87ab0fb (SubagentStart with inject=full): the
// user core only, within the user core limit. The task is not read.
func subagentStart(s *memory.Store) injection {
	var inj injection
	var b strings.Builder
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
		if used+runes(entry) > userCoreRunes {
			continue
		}
		body.WriteString(entry)
		used += runes(entry)
		inj.memoryIDs = append(inj.memoryIDs, m.ID)
		inj.mark(memory.MarkShown, m.ID)
	}
	if used == 0 {
		return inj.done(&b)
	}
	b.WriteString("[" + label + " 기억 · 상시 핵심] 모든 일에 늘 지키는 사용자의 방식이다. 다른 기억은 memory_find로 찾는다.\n")
	b.WriteString(body.String())
	return inj.done(&b)
}
