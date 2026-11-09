package inject

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// Lengths of the tool recall (B2, 설계 · orbit-mem 5.2절·11절 L-4). A memory line is its "- " title
// line with a short body line under it (the title first; memory_get has the body, 1-6 b6r). The call limits
// count the memory lines; the session sum counts the whole text the hooks gave (head, memory lines and
// pointer line, 1-6 b6r 2차), and MaxToolRunes bounds the whole text of one call.
const (
	// MaxToolRunes bounds the whole text a tool hook adds (L-4).
	MaxToolRunes = 2500
	// MaxToolMemories and MaxToolCallRunes bound the memory lines of one call.
	MaxToolMemories  = 2
	MaxToolCallRunes = 800
	// MaxToolMemoryRunes bounds one memory line; the body is cut to fit. It is short so the session sum holds
	// more memories (1-6 b6r): the line says what the memory is, memory_get gives the rest.
	MaxToolMemoryRunes = 160
	// MaxToolSessionRunes bounds the whole text one session key gets from tool calls (설계 5.2절 「세션 합계
	// 2,000자에서 멈춘다」): every call's head, memory lines and pointer line.
	MaxToolSessionRunes = 2000
	// maxToolNameRunes is how much of the tool name the head line shows: the head is paid from the session
	// sum on every call that says something.
	maxToolNameRunes = 60
	// minToolBodyRunes is the least body worth a line.
	minToolBodyRunes = 20
	// maxToolPointers, maxToolPointerRunes and toolPointerTitleRunes bound the pointer line: the memories a
	// call could not bring, by title and id, so the model can memory_get them (1-6 b6r).
	maxToolPointers       = 5
	maxToolPointerRunes   = 400
	toolPointerTitleRunes = 40
)

// toolPointerHead starts the pointer line.
const toolPointerHead = "- 이 도구에 걸린 기억 더 — "

// markToolMore marks, per session key, the tool memories the pointer line has named: the session is told
// about the same memories once (설계 · orbit-mem 5.2절, 원칙 3). They stay candidates: a later call within the
// limits can still bring them as lines. The ones only counted ("외 N개") are not marked, so a later call
// can name them. ResetSession drops these marks with the others.
const markToolMore = "tool.more"

// markToolCall marks, per session key, every call that said something, with what it cost: the mark is
// "<runes>:<first id it marked, b for brought or p for pointed>", unique per call because a memory is
// brought once and pointed once a session. The session sum is the sum of these.
const markToolCall = "tool.call"

// ToolInjection is what a tool call brings right before it runs (PreToolUse, 설계 · orbit-mem 5.2절):
// the live current values in scope whose tool triggers name the tool or a prefix of it, each once per
// session key (RecordShown). Exact names come first, then confirmed, this product, newest. One call
// brings at most MaxToolMemories memory lines within MaxToolCallRunes; the ones it could not bring and
// the session has not been told about are pointed at by title and id on one line, maxToolPointers at
// most and the rest counted. The whole text (head, lines, pointer line) of the session key's calls stops at
// MaxToolSessionRunes: a line that does not fit stops the lines (no shorter one is taken out of order),
// and the pointer line names what still fits. Memories it could not bring or name stay candidates for a
// later call. MemoryIDs are the memories brought, then the ones named. With nothing to bring and nothing
// new to name within the session sum it adds nothing.
func ToolInjection(s *memory.Store, cfg config.Config, scope, host, session, tool string) Injection {
	var inj Injection
	var b strings.Builder
	if cfg.RecallMode() == config.RecallOff || strings.TrimSpace(tool) == "" {
		return inj.done(&b)
	}
	active, err := s.Triggered()
	if err != nil || len(active) == 0 {
		return inj.done(&b)
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	given := s.SessionMarks(host, session, memory.MarkTool)
	told := s.SessionMarks(host, session, markToolMore)
	pl := at(cfg, scope)
	type hit struct {
		m     memory.Memory
		exact bool
	}
	var hits []hit
	for _, m := range active {
		if len(m.Tools) == 0 || given[m.ID] || !inScope(m, pl) || m.Kind == memory.KindJournal || excluded(m, now, heads) != "" {
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
		if la, lc := pl.Local(a.m.Scope, a.m.Project, nil), pl.Local(c.m.Scope, c.m.Project, nil); la != lc {
			return la
		}
		if a.m.UpdatedAt != c.m.UpdatedAt {
			return a.m.UpdatedAt > c.m.UpdatedAt
		}
		return a.m.ID < c.m.ID
	})
	head := toolHead(tool)
	// room is what the session sum leaves for this call's whole text (TrimSpace drops the last newline).
	room := MaxToolSessionRunes - toolSessionUsed(s, host, session, active, given) + 1
	used := runes(head)
	if used >= room {
		return inj.done(&b)
	}
	callUsed, brought := 0, 0
	var entries strings.Builder
	for _, h := range hits {
		if brought == MaxToolMemories {
			break
		}
		line := toolLine(h.m)
		n := runes(line)
		// 세션 합계에서 멈춘다(설계 5.2절): 순서를 건너뛰어 더 짧은 기억을 넣지 않는다.
		if callUsed+n > MaxToolCallRunes || used+n > room {
			break
		}
		entries.WriteString(line)
		callUsed += n
		used += n
		brought++
		inj.MemoryIDs = append(inj.MemoryIDs, h.m.ID)
		inj.mark(memory.MarkTool, h.m.ID)
	}
	var rest []memory.Memory
	for _, h := range hits[brought:] { // 넣은 것은 hits 앞에서부터다
		if !told[h.m.ID] {
			rest = append(rest, h.m)
		}
	}
	pointer, pointed := "", []string(nil)
	if len(rest) > 0 {
		// 가리키기 줄도 세션 합계 안에서만: 남은 자리(줄바꿈 하나 빼고)가 줄 상한보다 작으면 그만큼만 쓴다.
		pointer, pointed = toolPointerLine(rest, min(maxToolPointerRunes, room-used-1))
	}
	if brought == 0 && len(pointed) == 0 {
		inj.marks = nil
		return inj.done(&b)
	}
	b.WriteString(head)
	b.WriteString(entries.String())
	if len(pointed) > 0 {
		b.WriteString(pointer + "\n")
		inj.MemoryIDs = append(inj.MemoryIDs, pointed...)
		inj.mark(markToolMore, pointed...)
	}
	inj = inj.done(&b)
	first := "b" + inj.MemoryIDs[0]
	if brought == 0 {
		first = "p" + inj.MemoryIDs[0]
	}
	inj.mark(markToolCall, fmt.Sprintf("%d:%s", inj.Chars, first))
	return inj
}

// toolHead is the head line of a tool recall, the tool name cut to maxToolNameRunes.
func toolHead(tool string) string {
	return fmt.Sprintf("["+Label+" 기억 · %s 호출 직전] 걸린 기억을 확인하고 따른다. 본문은 memory_get\n", memory.ClipRunes(tool, maxToolNameRunes))
}

// toolPointerLine points at the memories a call could not bring: title (cut to toolPointerTitleRunes) and
// id, maxToolPointers at most within limit runes, then how many more. It gives the ids it named; with none
// named the line is "" (a count alone tells nothing the model can get).
func toolPointerLine(rest []memory.Memory, limit int) (string, []string) {
	line := toolPointerHead
	var ids []string
	for i, m := range rest {
		entry := fmt.Sprintf("%s(%s)", clipTitle(m.Title, toolPointerTitleRunes), m.ID)
		if i > 0 {
			entry = " · " + entry
		}
		more := ""
		if left := len(rest) - i - 1; left > 0 {
			more = fmt.Sprintf(" 외 %d개 — memory_find", left)
		}
		// 이 항목과(남는 것이 있으면) 「외 N개」 꼬리까지 한도 안에 들 때만 넣는다.
		if i == maxToolPointers || runes(line)+runes(entry)+runes(more) > limit {
			break
		}
		line += entry
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		return "", nil
	}
	if left := len(rest) - len(ids); left > 0 {
		line += fmt.Sprintf(" 외 %d개 — memory_find", left)
	}
	return line, ids
}

// toolLine is how a tool call shows one memory: its title line and, under it, as much of the body as
// fits in MaxToolMemoryRunes. The session sum redraws the memories it was given with it.
func toolLine(m memory.Memory) string {
	tail := fmt.Sprintf(" (%s · %s %s)\n", m.ID, kindName(m.Kind), toolState(m))
	title := m.Title
	if room := MaxToolMemoryRunes - runes(tail) - 2; runes(title) > room {
		title = memory.ClipRunes(title, room-1) // ClipRunes adds "…"
	}
	line := "- " + title + tail
	if text := strings.Join(strings.Fields(m.Body), " "); text != "" {
		// "  " + body + "\n", where a cut body ends in "…".
		if room := MaxToolMemoryRunes - runes(line) - 4; runes(text) <= room+1 {
			line += "  " + text + "\n"
		} else if room > minToolBodyRunes {
			line += "  " + memory.ClipRunes(text, room) + "\n"
		}
	}
	return line
}

// toolState is the state word of a tool memory line, with the untrusted note (D7) when the memory's
// sources are all untrusted.
func toolState(m memory.Memory) string {
	s := "확정"
	if m.State == memory.StateCandidate {
		s = "후보"
	}
	if note := memory.TrustNoteIn(i18n.KO, m); note != "" {
		s += " · " + note
	}
	return s
}

// toolSessionUsed is how much of MaxToolSessionRunes the session key has used: the runes of every call
// that said something (markToolCall). A session marked by an older binary has memory marks with no call
// marks: then the memories it was given, drawn as they are now, are the floor (a memory that is gone or
// hidden counts as a full line).
func toolSessionUsed(s *memory.Store, host, session string, active []memory.Memory, given map[string]bool) int {
	calls := 0
	for key := range s.SessionMarks(host, session, markToolCall) {
		n, _, _ := strings.Cut(key, ":")
		if v, err := strconv.Atoi(n); err == nil && v > 0 {
			calls += v
		} else {
			calls += MaxToolRunes // 알 수 없는 표시는 한 호출 최대로 센다
		}
	}
	if len(given) == 0 {
		return calls
	}
	byID := make(map[string]memory.Memory, len(active))
	for _, m := range active {
		byID[m.ID] = m
	}
	lines := 0
	for id := range given {
		m, ok := byID[id]
		if !ok {
			got, err := s.Get(id)
			if err != nil || got.State == memory.StateHidden {
				lines += MaxToolMemoryRunes
				continue
			}
			m = got
		}
		lines += runes(toolLine(m))
	}
	return max(calls, lines)
}
