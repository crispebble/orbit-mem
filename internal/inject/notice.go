package inject

// 세션 시작의 알림 줄(명세 C-2, 4.3): 자동 대체를 한 번 알리는 줄과, 자동 모드에서 묻지 않고 넘어간 어긋남 개수 줄. 둘 다
// 0건이면 줄이 없다. 알림은 새 세션을 만드는 세션 시작(buildBundle)에서만 쓰고 표시한다.

import (
	"fmt"
	"strings"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const (
	// maxAutoNoticePairs is how many replacements the notice line names; the rest are counted.
	maxAutoNoticePairs = 2
	// autoNoticeTitleRunes is how much of each title the notice line shows.
	autoNoticeTitleRunes = 30
)

// noticeTold is the automatic replacements a session start put in its notice: the time of the newest one, and
// the line that names them.
type noticeTold struct {
	at   int64
	line string
}

// autoNoticeLines are the notice lines of a session start that is making a bundle, and what to keep once the
// bundle is made. A line that did not fit in the bundle is not kept as told: the next session start tells it.
//
//   - 자동 대체: 「사용자 말 사실 n건을 검증된 사실로 바꿨다: 「옛」 → 「새」」. 알린 시각 뒤의 대체만 센다.
//   - 넘어간 어긋남: 자동 모드가 켜져 있고 열린 어긋남 쌍이 있으면 개수 한 줄. 끄면 어긋남은 작업에 걸릴 때 묻는다.
//
// With the recall off no memory is put in, and no notice is told.
func autoNoticeLines(s *memory.Store, cfg config.Config) ([]string, noticeTold) {
	var lines []string
	var told noticeTold
	if cfg.RecallMode() == config.RecallOff {
		return nil, told
	}
	if got, err := s.AutoSupersedesSince(s.AutoNoticeAt()); err == nil && len(got) > 0 {
		var pairs []string
		for i, a := range got {
			if i < maxAutoNoticePairs {
				pairs = append(pairs, fmt.Sprintf("「%s」 → 「%s」", clipTitle(a.OldTitle, autoNoticeTitleRunes), clipTitle(a.NewTitle, autoNoticeTitleRunes)))
			}
			told.at = max(told.at, a.At)
		}
		told.line = fmt.Sprintf("사용자 말 사실 %d건을 검증된 사실로 바꿨다: %s", len(got), strings.Join(pairs, ", "))
		if n := len(got) - len(pairs); n > 0 {
			told.line += fmt.Sprintf(" 외 %d건", n)
		}
		told.line += " — 되돌리려면 감사 목록(proposals status=audit)"
		lines = append(lines, told.line)
	}
	if cfg.AutoMode {
		if n, err := s.OpenConflictCount(); err == nil && n > 0 {
			lines = append(lines, fmt.Sprintf("묻지 않고 넘어간 어긋남 %d건 — 감사 목록(proposals status=audit)에서 확인", n))
		}
	}
	return lines, told
}

// keep records that the automatic replacements were told when the bundle's text holds the notice line.
func (t noticeTold) keep(s *memory.Store, text string) {
	if t.at > 0 && strings.Contains(text, t.line) {
		_ = s.SetAutoNoticeAt(t.at)
	}
}
