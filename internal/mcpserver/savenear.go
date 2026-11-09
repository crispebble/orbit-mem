package mcpserver

import (
	"slices"
	"strings"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 가까운 기억 줄(1-6 b6dn). 도메인(memory.SaveCheck)은 막지 않는 저장에 가까운 기억을 값(Near)으로 주고, 그 글 꼴은
// 여기서 만든다: 「<머리>: <id> <제목> · <id> <제목> · <id> <제목>」 한 줄. 제목은 공백을 한 칸으로 줄이고 nearTitleRunes자에서
// 자른다(믿을 수 없는 기억은 등급 표시가 그 몫을 쓴다). 그래서 줄은 셋이어도 nearLineRunes자 이하이고, 저장 한 건에 200자
// 안팎이 는다. 점수는 싣지 않는다(SimilarOut과 같다, M5).

const (
	nearTitleRunes = 30  // 가까운 기억 하나의 제목(과 등급 표시) 글자 상한
	nearLineRunes  = 230 // 줄 전체 글자 상한: 머리(en 70자 남짓) + 셋 × (id + 제목 30자) + 나눔표
)

// nearLine is the near line of a save in the language of this process ("" when there is nothing close).
func nearLine(near []memory.Similar) string { return nearLineIn(i18n.Current(), near) }

// nearLineIn is the near line in language l.
func nearLineIn(l i18n.Lang, near []memory.Similar) string {
	if len(near) == 0 {
		return ""
	}
	items := make([]string, 0, len(near))
	for _, m := range near {
		mark := ""
		if t := untrustedOf(memory.Memory{Trust: m.Trust}); t != "" {
			mark = " (" + t + ")"
		}
		items = append(items, m.ID+" "+cutRunes(strings.Join(strings.Fields(m.Title), " "), nearTitleRunes-len([]rune(mark)))+mark)
	}
	return i18n.TIn(l, "memory.save.near", strings.Join(items, " · "))
}

// cutRunes is s at most n characters, the last one an ellipsis when it is cut.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// nearOf is the close memories of a save that went on, less the memory it wrote and the ones it replaced.
func nearOf(near []memory.Similar, out SaveOut) []memory.Similar {
	return slices.DeleteFunc(slices.Clone(near), func(m memory.Similar) bool {
		return m.ID == out.Memory.ID || slices.Contains(out.Superseded, m.ID)
	})
}
