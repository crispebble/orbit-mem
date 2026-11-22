package inject

// 어긋남 표시(명세 C-5·C-6·C-8): 사용자 말 기억과 어긋나는 새 사실이 있으면, 그 옛 기억이 실린 줄 밑에 새 사실을 나란히
// 붙인다. 자동 모드가 꺼져 있으면 「기대는 일이면 사용자에게 한 번 확인한다」는 지시도 붙는다. 지시는 세션마다 쌍마다 한 번이라
// 세션 표시(memory.MarkConflictAsk)로 센다. 표시는 줄이 실제로 실릴 때만 남긴다: 길이 때문에 빠진 줄은 지시도 낸 것이 아니다.

import (
	"fmt"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
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
// the recall is about to show), and what each note says. A newer value is named only when the session may see it:
// it is a current value (or only the older value of a topic the old memory heads) and inside the session's place
// (memory.Viewer.ShownSides), so a note never carries the title of a memory that is stale, expired, or from
// another project. A pair that fails that gets no line and no instruction. The zero value says nothing.
type conflictReader struct {
	vw   memory.Viewer
	pl   place.Place
	ask  bool            // 지시를 붙인다: 자동 모드가 꺼져 있고 세션을 안다
	told map[string]bool // 이 세션이 이미 받은 쌍
}

// newConflictReader reads the open conflicts of ids. The instruction is given while the auto mode is off (C-8
// asks nothing with it on) and only for a session that can count it once; with no session id (a task handed to a
// subagent) the note is the side-by-side value alone. A failed read gives a reader that says nothing: a recall
// line is never held back by it. It reads the pairs and their newer values (two queries) and, only when there is
// a pair, the topic heads for the verdict.
func newConflictReader(s *memory.Store, cfg config.Config, host, session string, ids []string, pl place.Place) conflictReader {
	if len(ids) == 0 {
		return conflictReader{}
	}
	sides, err := s.OpenConflictSides(ids)
	return readerOf(s, cfg, host, session, sides, err, pl)
}

// newConflictReaderAll is newConflictReader over every open conflict: the session start looks at the whole core
// layer and the recent decisions, so it takes all of them instead of binding their ids.
func newConflictReaderAll(s *memory.Store, cfg config.Config, host, session string, pl place.Place) conflictReader {
	sides, err := s.OpenConflictSidesAll()
	return readerOf(s, cfg, host, session, sides, err, pl)
}

func readerOf(s *memory.Store, cfg config.Config, host, session string, sides map[string][]memory.Memory, err error, pl place.Place) conflictReader {
	if err != nil || len(sides) == 0 {
		return conflictReader{}
	}
	vw, _ := s.Viewer()
	c := conflictReader{vw: vw.WithConflictSides(sides), pl: pl, ask: !cfg.AutoMode && session != ""}
	if c.ask {
		c.told = s.SessionMarks(host, session, memory.MarkConflictAsk)
	}
	return c
}

// of is the note for memory id, false when it has no open conflict whose newer value may be shown.
func (c conflictReader) of(id string) (conflictInfo, bool) {
	shown := c.vw.ShownSides(id, &c.pl)
	if len(shown) == 0 {
		return conflictInfo{}, false
	}
	first := shown[0]
	text := fmt.Sprintf("어긋나는 새 사실: 「%s」(%s)", clipTitle(first.Title, conflictTitleRunes), first.ID)
	if n := len(shown) - 1; n > 0 {
		text += fmt.Sprintf(" 외 %d건", n)
	}
	var asked []string
	for _, m := range shown {
		if key := memory.ConflictPairKey(id, m.ID); c.ask && !c.told[key] {
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
