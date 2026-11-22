package inject

// 어긋남 표시(명세 C-5·C-6·C-8): 사용자 말 기억과 어긋나는 새 사실이 있으면, 그 옛 기억이 실린 줄 밑에 새 사실을 나란히
// 붙인다. 자동 모드가 꺼져 있으면 「기대는 일이면 사용자에게 한 번 확인한다」는 지시도 붙는다. 지시는 세션마다 쌍마다 한 번이라
// 세션 표시(memory.MarkConflictAsk)로 센다. 표시는 줄이 실제로 실릴 때만 남긴다: 길이 때문에 빠진 줄은 지시도 낸 것이 아니다.

import (
	"fmt"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const (
	// conflictTitleRunes is how much of the newer value's title a conflict note shows.
	conflictTitleRunes = 40
	// conflictAskText is the instruction of a conflict note while the auto mode is off.
	conflictAskText = " — 이 기억에 기대는 일이면 사용자에게 한 번 확인한다"
)

// conflictInfo is what a recall line says about a memory that has newer, disagreeing values.
type conflictInfo struct {
	// text is the note without its frame: 「어긋나는 새 사실: 「제목」(id)」, with 「 외 N건」 and, when this line is
	// the one that asks, the instruction.
	text string
	// asked are the session mark ids (memory.ConflictPairKey) of the pairs this line asks about: marked only once
	// the line is in the injection.
	asked []string
}

// line is the note as its own line under a recall line.
func (c conflictInfo) line() string {
	if c.text == "" {
		return ""
	}
	return "  ※ " + c.text + "\n"
}

// conflictReader tells which of the memories a recall shows have open conflicts, from one read of them (the ids
// the recall is about to show), and what each note says. The zero value says nothing.
type conflictReader struct {
	s     *memory.Store
	byOld map[string][]string
	ask   bool            // 지시를 붙인다: 자동 모드가 꺼져 있고 세션을 안다
	told  map[string]bool // 이 세션이 이미 받은 쌍
}

// newConflictReader reads the open conflicts of ids in one query. The instruction is given while the auto mode
// is off (C-8 asks nothing with it on) and only for a session that can count it once; with no session id (a task
// handed to a subagent) the note is the side-by-side value alone. A failed read gives a reader that says nothing:
// a recall line is never held back by it.
func newConflictReader(s *memory.Store, cfg config.Config, host, session string, ids []string) conflictReader {
	if len(ids) == 0 {
		return conflictReader{}
	}
	byOld, err := s.OpenConflictsOf(ids)
	if err != nil || len(byOld) == 0 {
		return conflictReader{}
	}
	c := conflictReader{s: s, byOld: byOld, ask: !cfg.AutoMode && session != ""}
	if c.ask {
		c.told = s.SessionMarks(host, session, memory.MarkConflictAsk)
	}
	return c
}

// newConflictReaderAll is newConflictReader over every open conflict (one query): the session start looks at
// the whole core layer and the recent decisions, so it takes all of them instead of binding their ids.
func newConflictReaderAll(s *memory.Store, cfg config.Config, host, session string) conflictReader {
	byOld, err := s.OpenConflicts()
	if err != nil || len(byOld) == 0 {
		return conflictReader{}
	}
	c := conflictReader{s: s, byOld: byOld, ask: !cfg.AutoMode && session != ""}
	if c.ask {
		c.told = s.SessionMarks(host, session, memory.MarkConflictAsk)
	}
	return c
}

// of is the note for memory id, false when it has no open conflict whose newer value can be read.
func (c conflictReader) of(id string) (conflictInfo, bool) {
	var shown *memory.Memory
	var pairs []string
	for _, newID := range c.byOld[id] {
		m, err := c.s.Get(newID)
		if err != nil {
			continue
		}
		if shown == nil {
			shown = &m
		}
		pairs = append(pairs, newID)
	}
	if shown == nil {
		return conflictInfo{}, false
	}
	text := fmt.Sprintf("어긋나는 새 사실: 「%s」(%s)", clipTitle(shown.Title, conflictTitleRunes), shown.ID)
	if n := len(pairs) - 1; n > 0 {
		text += fmt.Sprintf(" 외 %d건", n)
	}
	var asked []string
	for _, newID := range pairs {
		if key := memory.ConflictPairKey(id, newID); c.ask && !c.told[key] {
			asked = append(asked, key)
		}
	}
	if len(asked) > 0 {
		text += conflictAskText
	}
	return conflictInfo{text: text, asked: asked}, true
}

// note is of(id).line(), with the info for the caller that marks the pairs it asked about.
func (c conflictReader) note(id string) (string, conflictInfo) {
	info, ok := c.of(id)
	if !ok {
		return "", conflictInfo{}
	}
	return info.line(), info
}

// markAsked records that the line named these pairs' instruction.
func (in *Injection) markAsked(c conflictInfo) {
	if len(c.asked) > 0 {
		in.mark(memory.MarkConflictAsk, c.asked...)
	}
}
