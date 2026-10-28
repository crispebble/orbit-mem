package work

import (
	"strings"

	"github.com/onidot-labs/orbit/internal/memory"
)

// Text limits. Titles and summaries are plain one-line text with secrets masked.
const (
	TitleMax     = 120 // work_title 제목(계약 titleMax). 근거는 계약 README 「oni가 정한 값」
	stepTitleMax = 200
	summaryMax   = 200
	commandMax   = 200
)

// OneLine joins lines with spaces, masks secret-looking strings and trims.
func OneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(memory.MaskSecrets(s))
}

// clip cuts to n characters (runes) without breaking a character.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Clean is the text rule for anything a hook stores: one line, masked, clipped.
func Clean(s string, n int) string { return clip(OneLine(s), n) }
