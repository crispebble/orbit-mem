package inject

// 세션 시작의 알림 줄(명세 C-2, 4.3): 자동 대체를 한 번 알리는 줄과, 자동 모드에서 묻지 않고 넘어간 어긋남 개수 줄. 둘 다
// 0건이면 줄이 없다. 알림은 새 세션을 만드는 세션 시작(buildBundle)에서만 쓰고, 묶음이 확정된 뒤에 표시한다.

import (
	"fmt"
	"strings"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

const (
	// maxAutoNoticePairs is how many replacements the notice line names; the rest are counted.
	maxAutoNoticePairs = 2
	// autoNoticeTitleRunes is how much of each title the notice line shows.
	autoNoticeTitleRunes = 30
)

// noticeTold is the automatic replacements a session start put in its notice: the time of the newest one per
// scope, and the line that names them.
type noticeTold struct {
	at   map[string]int64
	line string
}

// autoNoticeLines are the notice lines of a session start that is making a bundle, and what to keep once the
// bundle is settled. A line that did not fit in the bundle, or a bundle that was thrown away, keeps nothing: the
// next session start tells it.
//
//   - 자동 대체: 「사용자 말 사실 n건을 검증된 사실로 바꿨다: 「옛」 → 「새」」. 옛 기억이 이 자리에서 보이는 범위의 대체만이고,
//     알린 시각은 범위마다 따로 센다: 한 프로젝트의 세션이 받았다고 다른 프로젝트의 세션이 못 받는 일이 없다.
//   - 넘어간 어긋남: 자동 모드가 켜져 있고 열린 어긋남 쌍이 있으면 개수 한 줄. 끄면 어긋남은 작업에 걸릴 때 묻는다. 개수는
//     자동 모드를 켜기 전에 생긴 쌍도 센다(쌍이 언제 생겼는지는 남지 않는다).
//
// With the recall off no memory is put in, and no notice is told.
func autoNoticeLines(s *memory.Store, cfg config.Config, pl place.Place) ([]string, noticeTold) {
	var lines []string
	var told noticeTold
	if cfg.RecallMode() == config.RecallOff {
		return nil, told
	}
	if got, err := s.AutoSupersedesUnnoticed(pl.Sees); err == nil && len(got) > 0 {
		told.at = map[string]int64{}
		var pairs []string
		for i, a := range got {
			if i < maxAutoNoticePairs {
				pairs = append(pairs, fmt.Sprintf("「%s」 → 「%s」", clipTitle(a.OldTitle, autoNoticeTitleRunes), clipTitle(a.NewTitle, autoNoticeTitleRunes)))
			}
			told.at[a.Scope] = max(told.at[a.Scope], a.At)
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
	if len(t.at) == 0 || !strings.Contains(text, t.line) {
		return
	}
	for scope, at := range t.at {
		_ = s.SetAutoNoticeAt(scope, at)
	}
}
