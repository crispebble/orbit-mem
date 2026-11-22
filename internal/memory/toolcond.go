package memory

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// 입력 조건(명세 T-2, 계획 「작성자가 정한 것」 T-2): 도구 트리거 「이름(낱말 …)」은 그 도구를 부를 때 도구 입력(host.ToolInputText의
// 글: Bash면 명령, Edit·Read·Write면 경로)에 그 낱말들이 있을 때만 걸린다. 예: Bash(git commit), Edit(package.json).
// 조건은 열을 늘리지 않으려고 기존 tools 칸의 글에 둔다. 조건 없는 이름과 접두(mcp__docs__*)는 입력과 관계없이 지금처럼 걸린다.
//
// 맞추는 법(condMatches):
//   - 입력을 ; & | 줄바꿈에서 명령 조각으로 자른다. 조건은 한 조각 안에서만 맞춘다: 「git status; ls commit」은 git과 commit이
//     다른 명령에 있어 맞지 않는다.
//   - 조각과 조건을 낱말로 나눈다: 빈칸, 따옴표, 셸 기호(< > ( ) = ,), 경로 구분자(/ \)에서 나누고, 하이픈·점·밑줄·콜론은 낱말 안의
//     글자다(git commit-tree, package.json.bak, .env.local은 commit, package.json, .env가 아니다). 대소문자를 가리지 않고 NFKC로
//     맞추며(Normalize), 제로폭 문자 같은 서식 글자(Cf)는 지운다.
//   - 조건을 빈칸으로 나눈 덩이들은 한 조각 안에 같은 차례로 나오면 맞고, 사이에 다른 낱말이 있어도 된다: git -C /w commit,
//     git -c user.name=x commit, git push origin main --force(조건 git push --force)가 맞는다. 명령의 선택지는 이름과 하위 명령
//     사이에도 끼므로 바로 이어진 낱말만 맞추면 지켜야 할 확정 규칙이 조용히 안 보인다. 그쪽이 더 비싸서 넓게 맞춘다.
//   - 한 덩이 안의 경로 조각(src/app)은 입력에서도 바로 이어져야 한다: 폴더의 차례가 곧 경로라 /w/src/lib/../app은 src/app이
//     아니다. 명령 속 경로(Bash(rm src/app))도 같다. 그래서 경로 입력(file_path)에 대한 경로 조건은 이어짐 규칙 그대로다.
//   - 셸 문법은 더 읽지 않는다: echo "git commit"의 따옴표 안 낱말도 낱말이다. 틀리게 붙어도 세션에 한 번 보이는 줄이다.
//
// 저장 때 거절(cleanCondTrigger): 낱말이 없는 조건(Bash(;)), *가 든 조건(덩이 사이를 건너뛰어 맞으므로 *는 필요 없고, 낱말로
// 맞추면 영영 맞지 않는다), ; & |가 든 조건(명령 조각을 건너 맞추지 않는다), 서식 글자(제로폭 문자 등)가 든 조건은 까닭과 올바른
// 꼴을 알려 거절한다. 줄바꿈 없는 빈칸 같은 공백은 빈칸 하나로 접는다.
//
// 옛 실행 파일과의 호환: 옛 판정(도구 이름이나 접두만 보는 것)은 「Bash(git commit)」를 이름도 접두도 아니라고 보고 걸지 않는다
// (그 기억은 도구 직전에만 안 나오고 다른 회상은 그대로다). 옛 cleanTools는 이 꼴을 거절하므로, 옛 실행 파일로 tools를 다시
// 쓰거나 이 꼴이 든 내보내기를 들이면 거절되거나(memory_save·memory_update) 트리거 없이 들어온다(가져오기, transfer.go).

const (
	// maxToolCondRunes bounds one condition: a few words of a command or a path, not a script.
	maxToolCondRunes = 120
)

// toolCondRE is a trigger with an input condition: a full tool name (no prefix *) and the condition in parentheses,
// with no parenthesis inside so the end of the condition is the end of the trigger.
var toolCondRE = regexp.MustCompile(`^([A-Za-z0-9_.:\-]{1,120})\(([^()]*)\)$`)

// splitTrigger splits a trigger "Name(words)" into its tool name and its condition words. A trigger without a
// condition gives cond "" and hasCond false.
func splitTrigger(t string) (name, cond string, hasCond bool) {
	if !strings.HasSuffix(t, ")") {
		return t, "", false
	}
	sub := toolCondRE.FindStringSubmatch(t)
	if sub == nil {
		return t, "", false
	}
	return sub[1], sub[2], true
}

// cleanCondTrigger is the stored form of a trigger with a condition: the condition's spaces (any Unicode space)
// collapsed to one and trimmed. isCond is false when t is not of the "Name(words)" form: the caller then checks
// it as a plain name. A condition that could never match (no words, a *, ; & |, an invisible format character) is
// refused with its reason; one with a control character or over maxToolCondRunes as a malformed trigger.
func cleanCondTrigger(t string) (clean string, isCond bool, err error) {
	name, cond, has := splitTrigger(t)
	if !has {
		return "", false, nil
	}
	if strings.ContainsFunc(cond, unicode.IsControl) {
		return "", true, i18n.Errorf("memory.err.tool_name", ErrInvalid, t)
	}
	if strings.ContainsFunc(cond, isFormat) {
		return "", true, i18n.Errorf("memory.err.tool_cond_invisible", ErrInvalid, t)
	}
	cond = strings.Join(strings.Fields(cond), " ")
	switch {
	case utf8.RuneCountInString(cond) > maxToolCondRunes:
		return "", true, i18n.Errorf("memory.err.tool_name", ErrInvalid, t)
	case len(condChunks(strings.Map(dropCommandSep, cond))) == 0: // 「;」만 있는 조건은 낱말이 없는 것이다
		return "", true, i18n.Errorf("memory.err.tool_cond_empty", ErrInvalid, t)
	case strings.Contains(cond, "*"):
		return "", true, i18n.Errorf("memory.err.tool_cond_wildcard", ErrInvalid, t)
	case strings.ContainsFunc(cond, isCommandSep):
		return "", true, i18n.Errorf("memory.err.tool_cond_chain", ErrInvalid, t)
	}
	return name + "(" + cond + ")", true, nil
}

// isFormat says whether r is an invisible format character (zero-width space, joiner, byte order mark …).
func isFormat(r rune) bool { return unicode.Is(unicode.Cf, r) }

// isCommandSep says whether r ends one command of a shell input: ; & | and line breaks.
func isCommandSep(r rune) bool {
	switch r {
	case ';', '&', '|', '\n', '\r':
		return true
	}
	return false
}

// dropCommandSep maps a command separator to a space (strings.Map), to see whether the rest of a condition has words.
func dropCommandSep(r rune) rune {
	if isCommandSep(r) {
		return ' '
	}
	return r
}

// isInputSep says whether r splits the words of one command or of a condition (see the comment above).
func isInputSep(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	switch r {
	case '"', '\'', '`', '<', '>', '(', ')', '=', ',', '/', '\\':
		return true
	}
	return false
}

// inputWords are the words of one command or of one chunk of a condition: normalized, format characters taken
// out, split at isInputSep.
func inputWords(s string) []string {
	s = strings.Map(func(r rune) rune {
		if isFormat(r) {
			return -1
		}
		return r
	}, Normalize(s))
	return strings.FieldsFunc(s, isInputSep)
}

// condChunks are the chunks of a condition, the parts its spaces split: each a run of words that must stand next
// to each other in the input (a path's parts), the chunks in order with any words between them. A chunk with no
// words (a lone / or quote) is left out.
func condChunks(cond string) [][]string {
	var out [][]string
	for _, f := range strings.Fields(cond) {
		if ws := inputWords(f); len(ws) > 0 {
			out = append(out, ws)
		}
	}
	return out
}

// commands are the word lists of the commands of an input (split at isCommandSep), empty ones left out.
func commands(input string) [][]string {
	var out [][]string
	for _, c := range strings.FieldsFunc(input, isCommandSep) {
		if ws := inputWords(c); len(ws) > 0 {
			out = append(out, ws)
		}
	}
	return out
}

// condMatches says whether one of cmds holds chunks in order, each chunk's words next to each other.
func condMatches(cmds, chunks [][]string) bool {
	if len(chunks) == 0 {
		return false
	}
	for _, words := range cmds {
		at, ok := 0, true
		// 덩이마다 가장 앞자리를 잡는다: 앞 덩이가 일찍 끝날수록 뒤 덩이가 들어갈 자리가 넓으므로 이 탐색은 맞는 것을 빠뜨리지 않는다.
		for _, ch := range chunks {
			i := runAt(words, ch, at)
			if i < 0 {
				ok = false
				break
			}
			at = i + len(ch)
		}
		if ok {
			return true
		}
	}
	return false
}

// runAt is the first index from start where want stands in words next to each other, or -1.
func runAt(words, want []string, start int) int {
	for i := start; i+len(want) <= len(words); i++ {
		if slices.Equal(words[i:i+len(want)], want) {
			return i
		}
	}
	return -1
}

// ToolInputScores are the recall scores (0~1) of ms against a tool input, by memory id, for the tool input gate
// (명세 T-1): the score function of the recall of a request (tokens scored as FindScored scores them with
// SkipUnknownTokens, rank.go) over the title, topic, tags and body of ms, with the input's meaningful words
// (MeaningfulWords) as the query. It differs from the request's score in three ways:
//   - The query leaves out one-character Latin and digit words (RecallQuery keeps them). In a command they are
//     flags and counts (-m, -n, =1), and as prefix tokens they match any memory with a word that starts so: on a
//     sample, 「go test ./x -count=1」 scored an unrelated lesson 0.45 by its 1 and a number of a date in its body.
//   - The token weights (IDF) are counted over ms, not over the store, and 의 is not taken off a one-syllable noun
//     the store knows (resolveGenitives, which a command or a path hardly holds). Both would need a read of the
//     store, and the tool recall runs before every tool call with only the triggered memories read.
//
// A memory that shares no token with the input is left out (score 0); an input with no words gives none.
func ToolInputScores(ms []Memory, input string) map[string]float64 {
	qs := scoreTokens(strings.Join(MeaningfulWords(input), " "))
	if len(qs) == 0 || len(ms) == 0 {
		return nil
	}
	r := newRanker(qs)
	r.skipUnknown = true
	for _, m := range ms {
		tags := toolsJSON(m.Tags) // 색인과 같은 꼴: 태그 JSON 배열
		r.add(m.ID, tokensOf(m.Title, m.Topic, tags, m.Body))
	}
	r.total = len(ms)
	return r.scores()
}

// TriggeredByInput says whether calling tool with input should recall m. A trigger that names the tool with no
// condition, or with a condition that matches input (condMatches: its words in order within one command), matches
// exactly; a prefix ending in * matches the tool, not exactly. Several triggers of one tool are alternatives: one
// is enough, so a plain name beside a conditioned one wins. An input that is empty or only spaces is no input (the
// host did not give it, 명세 T-4): then a trigger with a condition matches by its tool name alone. It reads only m
// and its arguments.
func (m Memory) TriggeredByInput(tool, input string) (exact, ok bool) {
	given := strings.TrimSpace(input) != ""
	var cmds [][]string // 조건이 있는 트리거를 처음 만날 때 한 번만 나눈다
	for _, t := range m.Tools {
		name, cond, has := splitTrigger(t)
		if !has {
			if t == tool {
				return true, true
			}
			if p, isPrefix := strings.CutSuffix(t, "*"); isPrefix && strings.HasPrefix(tool, p) {
				ok = true
			}
			continue
		}
		if name != tool {
			continue
		}
		if !given {
			return true, true
		}
		if cmds == nil {
			cmds = commands(input)
		}
		if condMatches(cmds, condChunks(cond)) {
			return true, true
		}
	}
	return false, ok
}
