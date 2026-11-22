package memory

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of a memory's aliases (Memory.Aliases). They are the tags' limit: a few short words of another way to
// say the same thing, not a second body.
const (
	MaxAliases    = 10
	MaxAliasRunes = 50
)

// NormalizeAliases is the aliases as a memory keeps them: each one with its white space folded to single
// spaces, empty ones dropped, and the ones that equal an earlier one once case and width are ignored
// (Normalize) dropped, in the order given. kept stops at MaxAliases and trimmed says whether anything past the
// limits (a count above MaxAliases, a length above MaxAliasRunes, a control character) was left out, so a caller that must not
// lose a word silently can refuse (cleanAliases, or a tool's own check with a message) and one that reads a file
// written elsewhere can take what fits.
func NormalizeAliases(aliases []string) (kept []string, trimmed bool) {
	kept = []string{}
	seen := map[string]bool{}
	for _, a := range aliases {
		a = strings.Join(strings.Fields(a), " ")
		if a == "" {
			continue
		}
		if utf8.RuneCountInString(a) > MaxAliasRunes || strings.ContainsFunc(a, unicode.IsControl) {
			// 제어 문자(공백류는 위에서 접혔다)는 색인과 응답에 그대로 실리므로 길이 초과처럼 버리고 알린다.
			trimmed = true
			continue
		}
		k := Normalize(a)
		if seen[k] {
			continue
		}
		if len(kept) == MaxAliases {
			trimmed = true
			continue
		}
		seen[k] = true
		kept = append(kept, a)
	}
	return kept, trimmed
}

// cleanAliases is NormalizeAliases for a save or an update: a list over the limits, or an alias that looks like
// a secret, is ErrInvalid and nothing is written. An alias is indexed and shown back as written, so a
// secret in one would spread like one in the body. The tools say which of the two it was before they call
// (mcpserver checkAliases); this is the domain's own refusal.
func cleanAliases(aliases []string) ([]string, error) {
	kept, trimmed := NormalizeAliases(aliases)
	if trimmed {
		return nil, ErrInvalid
	}
	for _, a := range kept {
		if MaskSecrets(a) != a {
			return nil, ErrInvalid
		}
	}
	return kept, nil
}

// mergedAliases are the aliases of the memories ids, one after another, as one list NormalizeAliases keeps
// (no repeat once case and width are ignored, at most MaxAliases, the first ones first).
func mergedAliases(s *Store, ids []string) []string {
	var all []string
	for _, id := range ids {
		if m, err := getTx(s.db, id); err == nil {
			all = append(all, m.Aliases...)
		}
	}
	kept, _ := NormalizeAliases(all)
	return kept
}

// aliasesJSON is the aliases column: a JSON array, "[]" for none.
func aliasesJSON(aliases []string) string {
	if len(aliases) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(aliases)
	return string(b)
}

// parseAliases reads the aliases column; anything but a JSON array of texts reads as none.
func parseAliases(col string) []string {
	var out []string
	if json.Unmarshal([]byte(col), &out) != nil || len(out) == 0 {
		return nil
	}
	return out
}
