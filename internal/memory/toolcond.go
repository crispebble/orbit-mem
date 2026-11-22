package memory

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 입력 조건(명세 T-2, 계획 「작성자가 정한 것」 T-2): 도구 트리거 「이름(낱말 …)」은 그 도구를 부를 때 도구 입력(host.ToolInputText의
// 글: Bash면 명령, Edit·Read·Write면 경로)에 그 낱말들이 이어서 나올 때만 걸린다. 예: Bash(git commit), Edit(package.json).
// 조건은 열을 늘리지 않으려고 기존 tools 칸의 글에 둔다. 조건 없는 이름과 접두(mcp__atlas__*)는 입력과 관계없이 지금처럼 걸린다.
//
// 낱말 맞추기(inputWords):
//   - 빈칸, 따옴표, 셸 기호(; & | < > ( ) = ,), 경로 구분자(/ \)에서 나눈다. 「git  commit」도 「cd w && git commit」도 맞는다.
//     경로는 조각으로 나뉘어 Edit(package.json)이 /w/app/package.json에 맞고, Edit(web/src)는 이어진 폴더 조각에 맞는다.
//   - 하이픈·점·밑줄은 낱말 안의 글자다: git commit-tree, package.json.bak은 commit, package.json이 아니다.
//   - 조건 낱말은 입력에 같은 차례로 바로 이어서 나와야 한다. 떨어져 나온 낱말(git -C /w commit)은 맞지 않는다: 아무 데나 흩어진
//     낱말까지 맞추면 「git status; ls commit」처럼 무관한 명령에 걸린다. 그런 명령을 잡으려면 Bash(commit)처럼 조건을 넓힌다.
//   - 셸 문법은 읽지 않는다: echo "git commit"의 따옴표 안 낱말도 낱말이다. 틀리게 붙어도 세션에 한 번 보이는 규칙 줄이고,
//     빠뜨리면 지켜야 할 규칙이 안 보이므로 넓게 맞춘다.
//   - 대소문자를 가리지 않고 NFKC로 맞춘다(Normalize). 경로가 대소문자를 가리지 않는 OS가 있고 명령은 대개 소문자다.
//
// 옛 실행 파일과의 호환: 옛 TriggeredBy는 「Bash(git commit)」를 이름도 접두도 아니라고 보고 걸지 않는다(그 기억은 도구 직전에만
// 안 나오고 다른 회상은 그대로다). 옛 cleanTools는 이 꼴을 거절하므로, 옛 실행 파일로 tools를 다시 쓰거나 이 꼴이 든 내보내기를
// 들이면 거절되거나(memory_save·memory_update) 트리거 없이 들어온다(가져오기, transfer.go).

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

// cleanCondTrigger is the stored form of a trigger with a condition: the condition's spaces collapsed and
// trimmed. ok is false when t is not of the "Name(words)" form or its condition is blank, too long or holds a
// control character.
func cleanCondTrigger(t string) (string, bool) {
	name, cond, has := splitTrigger(t)
	if !has {
		return "", false
	}
	if strings.ContainsFunc(cond, unicode.IsControl) {
		return "", false
	}
	cond = strings.Join(strings.Fields(cond), " ")
	if cond == "" || utf8.RuneCountInString(cond) > maxToolCondRunes {
		return "", false
	}
	return name + "(" + cond + ")", true
}

// isInputSep says whether r splits the words of a tool input or a condition (see the comment above).
func isInputSep(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	switch r {
	case '"', '\'', '`', ';', '&', '|', '<', '>', '(', ')', '=', ',', '/', '\\':
		return true
	}
	return false
}

// inputWords are the words of a tool input or a condition, normalized.
func inputWords(s string) []string { return strings.FieldsFunc(Normalize(s), isInputSep) }

// hasRun says whether want is a run of words, in order and next to each other, of words.
func hasRun(words, want []string) bool {
	if len(want) == 0 {
		return false
	}
	for i := 0; i+len(want) <= len(words); i++ {
		match := true
		for j, w := range want {
			if words[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// ToolInputScores are the recall scores (0~1) of ms against a tool input, by memory id, for the tool input gate
// (명세 T-1): the score function of the recall of a request (tokens scored as FindScored scores them with
// SkipUnknownTokens, rank.go) over the title, topic, tags and body of ms, with the input's meaningful words
// (MeaningfulWords) as the query. It differs from the request's score in three ways:
//   - The query leaves out one-character Latin and digit words (RecallQuery keeps them). In a command they are
//     flags and counts (-m, -n, =1), and as prefix tokens they matched any memory with a word that starts so: on a
//     sample, 「go test ./x -count=1」 scored an unrelated lesson 0.45 by its 1 and 「16」 in a date.
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
// condition, or with a condition whose words come in input in order and next to each other, matches exactly; a
// prefix ending in * matches the tool as TriggeredBy does. Several triggers of one tool are alternatives: one is
// enough. An input that is empty or only spaces is no input (the host did not give it, 명세 T-4): then a trigger
// with a condition matches by its tool name alone. It reads only m and its arguments.
func (m Memory) TriggeredByInput(tool, input string) (exact, ok bool) {
	given := strings.TrimSpace(input) != ""
	var words []string // 조건이 있는 트리거를 처음 만날 때 한 번만 나눈다
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
		if words == nil {
			words = inputWords(input)
		}
		if hasRun(words, inputWords(cond)) {
			return true, true
		}
	}
	return false, ok
}
