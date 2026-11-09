package inject

import (
	"fmt"
	"sort"
	"strings"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// Lengths of the tool recall (B2, 설계 · orbit-mem 5.2절·11절 L-4). A memory line is its "- " title
// line with the body line under it. Only memory lines count toward the call and session sums; the head
// line and the "N개 더" line do not, and MaxToolRunes bounds the whole hook text.
const (
	// MaxToolRunes bounds the whole text a tool hook adds (L-4).
	MaxToolRunes = 2500
	// MaxToolMemories and MaxToolCallRunes bound the memory lines of one call.
	MaxToolMemories  = 2
	MaxToolCallRunes = 800
	// MaxToolMemoryRunes bounds one memory line; the body is cut to fit.
	MaxToolMemoryRunes = 400
	// MaxToolSessionRunes bounds the memory lines one session key gets from tool calls.
	MaxToolSessionRunes = 2000
	// maxToolNameRunes is how much of the tool name the head line shows, so a very long name
	// keeps the hook text within MaxToolRunes.
	maxToolNameRunes = 200
	// minToolBodyRunes is the least body worth a line.
	minToolBodyRunes = 20
)

// markToolMore marks, per session key, the tool memories the "N개 더" line has counted: the session is
// told about the same memories once (설계 · orbit-mem 5.2절, 원칙 3). They stay candidates: a later call
// within the call limits, or one after a reset once the session sum is full, can still bring them.
// ResetSession drops these marks with the others.
const markToolMore = "tool.more"

// ToolInjection is what a tool call brings right before it runs (PreToolUse, 설계 · orbit-mem 5.2절):
// the live current values in scope whose tool triggers name the tool or a prefix of it, each once per
// session key (RecordShown). Exact names come first, then confirmed, this product, newest. One call
// brings at most MaxToolMemories memory lines within MaxToolCallRunes, and the session key stops at
// MaxToolSessionRunes, counted over the memories it was given, drawn as they are now. Memories it
// could not bring stay candidates for a later call; the ones the session has not been told about are
// counted in one line, once (markToolMore). With nothing to bring and nothing new to count it adds nothing.
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
	sessionUsed := toolSessionUsed(s, active, given)
	callUsed := 0
	var entries strings.Builder
	for _, h := range hits {
		if len(inj.MemoryIDs) == MaxToolMemories {
			break
		}
		line := toolLine(h.m)
		n := runes(line)
		// 세션 합계에서 멈춘다(설계 5.2절): 순서를 건너뛰어 더 짧은 기억을 넣지 않는다.
		if callUsed+n > MaxToolCallRunes || sessionUsed+n > MaxToolSessionRunes {
			break
		}
		entries.WriteString(line)
		callUsed += n
		sessionUsed += n
		inj.MemoryIDs = append(inj.MemoryIDs, h.m.ID)
		inj.mark(memory.MarkTool, h.m.ID)
	}
	left := 0
	for _, h := range hits[len(inj.MemoryIDs):] { // 넣은 것은 hits 앞에서부터다
		if !told[h.m.ID] {
			left++
			inj.mark(markToolMore, h.m.ID)
		}
	}
	if len(inj.MemoryIDs) == 0 && left == 0 {
		return inj.done(&b)
	}
	b.WriteString(fmt.Sprintf("["+Label+" 기억 · %s 호출 직전] 이 도구에 걸린 기억이다. 부르기 전에 확인하고 따른다.\n", memory.ClipRunes(tool, maxToolNameRunes)))
	b.WriteString(entries.String())
	if left > 0 {
		b.WriteString(fmt.Sprintf("- 이 도구에 걸린 기억 %d개 더 — memory_find\n", left))
	}
	return inj.done(&b)
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

// toolSessionUsed is how much of MaxToolSessionRunes the session key has used: the memories it was
// given, drawn as they are now. A memory that is gone or hidden counts as a full line.
func toolSessionUsed(s *memory.Store, active []memory.Memory, given map[string]bool) int {
	if len(given) == 0 {
		return 0
	}
	byID := make(map[string]memory.Memory, len(active))
	for _, m := range active {
		byID[m.ID] = m
	}
	used := 0
	for id := range given {
		m, ok := byID[id]
		if !ok {
			got, err := s.Get(id)
			if err != nil || got.State == memory.StateHidden {
				used += MaxToolMemoryRunes
				continue
			}
			m = got
		}
		used += runes(toolLine(m))
	}
	return used
}
