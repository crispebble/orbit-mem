package inject

import (
	"fmt"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

// 서브에이전트 시작 묶음(설계 · orbit-mem 4.1, 수용 기준 L-4). 두 몫이다.
//
//   - 위임 회상: 위임 글을 질의로 요청 회상(B1)과 같은 찾기·두 문턱을 돌려 고른 기억 maxTaskRecall개까지. 항목마다 제목·id·
//     고른 까닭(맞은 낱말·주제)·요약(본문 앞부분, taskSummaryRunes자까지). 위임 글에 맞은 낡은·대체된 옛 판이 있으면 그 수만
//     한 줄로 알린다(옛 판 id는 넣지 않는다). 머리말 포함 maxTaskRecallRunes자 이하.
//   - 역할 지침: 사용자 상시 핵심의 제목 줄(id와 함께, 본문은 memory_get). 머리말 포함 maxRoleRunes자 이하.
//
// 합계는 MaxSubagentRunes자 이하이고, 맞는 기억이 없으면 역할 지침만이라 maxRoleRunes자 이하다. 읽기 범위는 부모 세션의
// 폴더(scope)를 따른다.
const (
	MaxSubagentRunes   = 3000
	maxTaskRecallRunes = 2400
	maxRoleRunes       = 600
	maxTaskRecall      = 5
	taskSummaryRunes   = 500
	// taskReasonWords is how many matched words a reason line names.
	taskReasonWords = 6
)

// Headings of the subagent bundle's two shares. RoleHead starts the role share, so a reader can tell the
// two apart (the bench splits the characters with it).
const (
	taskRecallHead = "[" + Label + " 기억 · 맡은 일 관련] 위임 글에 닿는 기억이다. 본문은 memory_get"
	RoleHead       = "[" + Label + " 기억 · 상시 핵심] 사용자가 모든 일에 늘 지키는 방식이다. 본문은 memory_get"
)

// SubagentStart is what a subagent gets when it starts: the recall for its task (none without a task or
// a match) and then the title lines of the user core. The ids are in the order of the text; all of them
// are marked shown for the subagent's own session key.
func SubagentStart(s *memory.Store, cfg config.Config, scope, task string) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.RecallMode() == config.RecallOff {
		return inj.done(&b)
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	role, coreIDs := roleLines(s, now, heads)
	if cfg.RecallMode() == config.RecallAuto && strings.TrimSpace(task) != "" {
		b.WriteString(taskRecall(s, cfg, at(cfg, scope), task, coreIDs, &inj))
	}
	if role != "" {
		b.WriteString(role)
		inj.MemoryIDs = append(inj.MemoryIDs, coreIDs...)
		inj.mark(memory.MarkShown, coreIDs...)
	}
	return inj.done(&b)
}

// roleLines are the role share: the heading and one title line per user core memory in core order, within
// maxRoleRunes. Lines that do not fit are counted in one line at the end. "" without a core.
func roleLines(s *memory.Store, now time.Time, heads memory.TopicHeads) (string, []string) {
	core, err := s.Core(memory.ScopeGlobal)
	if err != nil {
		return "", nil
	}
	var lines, ids []string
	for _, m := range core {
		if excluded(m, now, heads) != "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s — %s\n", clipTitle(m.Title, recallTitleRunes), m.ID))
		ids = append(ids, m.ID)
	}
	if len(lines) == 0 {
		return "", nil
	}
	head := RoleHead + "\n"
	more := func(n int) string { return fmt.Sprintf("- 상시 핵심 %d개 더 — memory_find\n", n) }
	used := runes(head)
	total := used
	for _, l := range lines {
		total += runes(l)
	}
	room := maxRoleRunes
	if total > room {
		room -= runes(more(len(lines)))
	}
	var b strings.Builder
	b.WriteString(head)
	n := 0
	for _, l := range lines {
		if used+runes(l) > room {
			break
		}
		b.WriteString(l)
		used += runes(l)
		n++
	}
	if n == 0 {
		return "", nil
	}
	if n < len(lines) {
		b.WriteString(more(len(lines) - n))
	}
	return b.String(), ids[:n]
}

// taskRecall is the recall share for a task, "" without a match. The memories of the user core (in the
// role share) are left out; the old versions it saw are counted, never named.
func taskRecall(s *memory.Store, cfg config.Config, pl place.Place, task string, coreIDs []string, inj *Injection) string {
	byID, _, err := CandidatesAt(s, pl, Options{}, coreIDs)
	if err != nil {
		return ""
	}
	th := taskThresholds(cfg)
	found := th.found(s, task) // 위임 회상과 옛 판 셈이 같이 쓴다(같은 찾기를 두 번 돌리지 않는다)
	hits := recallHitsOf(s, cfg, th, pl, task, found, byID, nil, nil)
	if len(hits) > maxTaskRecall {
		hits = hits[:maxTaskRecall]
	}
	if len(hits) == 0 {
		return ""
	}
	now := s.Now()
	head := taskRecallHead + "\n"
	oldLine := ""
	if n := oldVersions(s, th, pl, task, found, now); n > 0 {
		oldLine = fmt.Sprintf("- 옛 판 %d개 뺌 — 낡거나 대체된 판이라 따르지 않는다\n", n)
	}
	type entry struct {
		m       memory.Memory
		top     string // 제목 줄과 까닭 줄
		summary string
	}
	entries := make([]entry, 0, len(hits))
	fixed := runes(head) + runes(oldLine)
	for _, h := range hits {
		top := recallLine(h.Memory, now) + reasonLine(h)
		if fixed+runes(top) > maxTaskRecallRunes {
			break
		}
		fixed += runes(top)
		entries = append(entries, entry{m: h.Memory, top: top, summary: strings.Join(strings.Fields(h.Memory.Body), " ")})
	}
	// 요약은 남은 글자를 아직 쓰지 않은 항목끼리 나눈다: 짧은 요약이 남긴 몫은 뒤 항목이 쓴다.
	left := maxTaskRecallRunes - fixed
	var b strings.Builder
	b.WriteString(head)
	for i, e := range entries {
		b.WriteString(e.top)
		room := min(taskSummaryRunes, left/(len(entries)-i)-3) // 앞 공백 둘과 줄바꿈
		if e.summary != "" && room >= minSummaryRunes {
			line := "  " + clipTitle(e.summary, room) + "\n"
			b.WriteString(line)
			left -= runes(line)
		}
		inj.MemoryIDs = append(inj.MemoryIDs, e.m.ID)
		inj.mark(memory.MarkShown, e.m.ID)
	}
	b.WriteString(oldLine)
	return b.String()
}

// reasonLine says why a memory was picked: the task's words it matched and its topic.
func reasonLine(h memory.RecallHit) string {
	var words []string
	for _, w := range h.Matched {
		if w = strings.Trim(w, ".,;:!?…\"'()[]「」"); w != "" && len(words) < taskReasonWords {
			words = append(words, w)
		}
	}
	parts := []string{"맞은 낱말 " + strings.Join(words, "·")}
	if h.Memory.Topic != "" {
		parts = append(parts, "주제 "+clipTitle(h.Memory.Topic, newsTitleRunes))
	}
	return "  고른 까닭: " + strings.Join(parts, " · ") + "\n"
}

// oldVersions counts the old versions in reach that a task matches by the task gate (taskThresholds.pass):
// stale memories, and live ones recall leaves out as an older value of their topic or past their days. live
// are the live memories the task matched (thresholds.found), so only the stale ones are looked up here.
func oldVersions(s *memory.Store, th thresholds, pl place.Place, task string, live []memory.RecallHit, now time.Time) int {
	heads, _ := s.TopicHeads()
	n := 0
	count := func(found []memory.RecallHit, stale bool) {
		for _, h := range found {
			m := h.Memory
			if !th.pass(h) || !inScope(m, pl) || m.Kind == memory.KindJournal {
				continue
			}
			if stale || excluded(m, now, heads) != "" {
				n++
			}
		}
	}
	count(live, false)
	if old, err := th.hits(s, task, memory.StateStale); err == nil {
		count(old, true)
	}
	return n
}
