package memory

import (
	"regexp"
	"strings"
)

// 위임 회상의 내용 낱말(설계 보완, 계획 1-6 b6 T1). 위임 글은 요청과 달리 일을 시키는 말이 섞여 있다: 명령형 어미로 끝나는
// 낱말(「보고하라」「고쳐라」「넣지 마라」), 위임 머리 낱말(「계획:」「항목:」「검토 요청」), 항목 번호(「m2:」「1.」). 그 낱말은
// 어느 일에나 붙어 어떤 기억인지를 가르지 못하고, 긴 위임 글에서 맞은 비율과 찾기 점수의 분모만 키운다. 그래서 위임 회상은
// 요청 낱말(MeaningfulWords)에서 그 낱말을 뺀 내용 낱말로 찾고 맞은 낱말을 센다. 요청 회상(B1)의 낱말은 그대로다.
//
// 목록은 사례를 보고 늘리지 않는다.
//   - 명령형 어미: 학교 문법의 명령형 종결 어미, 하라체 「-(으)라」와 해라체 「-아라/-어라/-여라」(「하다」는 「-하라」
//     「-해라」, 「말다」는 「마라」). 어간이 모음으로 끝나 「-아/-어」가 줄어든 꼴(봐라·줘라·가라·내라·켜라·바꿔라)은 앞
//     글자가 받침 없이 ㅏ·ㅐ·ㅓ·ㅕ·ㅘ·ㅙ·ㅝ로 끝나는 「-라」로 본다. 그 밖의 모음 뒤 「-라」(「보라」「고치라」)는 명사
//     (인프라·카메라·오로라)와 가를 수 없어 빼지 않는다.
//   - 위임 머리 낱말: 위임 글의 흔한 머리(계획·항목·요청·검토·조사).
//   - 항목 번호: 번호 꼴(라틴 글자 두 개까지와 숫자, 끝에 「:」「.」「)」 — 「m2:」「1.」「a7)」)이거나 「항목」 바로 뒤에 온
//     번호(「항목 b6」)다. 그냥 쓴 「s3」「h2」는 저장소·규약 이름일 수 있어 남긴다.

// taskHeadWords are the heading words of a delegation.
var taskHeadWords = map[string]bool{"계획": true, "항목": true, "요청": true, "검토": true, "조사": true}

// itemNumber is an item number: up to two Latin letters, digits, maybe one letter after (b6, b6h, 12).
var itemNumber = regexp.MustCompile(`^[a-z]{0,2}\d{1,3}[a-z]?$`)

// TaskWords are the content words of a delegated task: its meaningful words (MeaningfulWords) less words
// that end in an imperative ending, the heading words of a delegation and item numbers (see above).
func TaskWords(task string) []string { return taskWords(task, false) }

// TaskQuery is the find query of a delegated task: its content words and its one-character Latin and
// digit words, in order.
func TaskQuery(task string) string { return strings.Join(taskWords(task, true), " ") }

func taskWords(task string, withShort bool) []string {
	var out []string
	for _, w := range requestWords(withoutItemNumbers(task), withShort) {
		if !taskHeadWords[w] && !imperative(w) {
			out = append(out, w)
		}
	}
	return out
}

// withoutItemNumbers is text less its item numbers: a number with a mark after it, or the term after 항목.
func withoutItemNumbers(text string) string {
	var kept []string
	afterItem := false
	for _, t := range termSplit.Split(Normalize(text), -1) {
		core := strings.TrimRight(t, ":.)")
		numbered := core != t && itemNumber.MatchString(core)
		if numbered || (afterItem && itemNumber.MatchString(core)) {
			afterItem = false
			continue
		}
		afterItem = core == "항목"
		kept = append(kept, t)
	}
	return strings.Join(kept, " ")
}

// imperative says whether a Hangul word of two or more syllables ends in an imperative ending (see above).
func imperative(w string) bool {
	rs := []rune(w)
	n := len(rs)
	if n < 2 || rs[n-1] != '라' {
		return false
	}
	before := rs[n-2]
	if before == '으' {
		return true
	}
	if before < 0xAC00 || before > 0xD7A3 {
		return false
	}
	if final, _ := syllableFinal(before); final {
		return false
	}
	switch (before - 0xAC00) / 28 % 21 { // 가운뎃소리
	case 0, 1, 4, 6, 9, 10, 14: // ㅏ ㅐ ㅓ ㅕ ㅘ ㅙ ㅝ
		return true
	}
	return false
}

// TaskRecallHitsIn is RecallHitsIn for a delegated task: the find score of its content words (TaskQuery) and
// the share of its content words (TaskWords). A task without content words matches nothing.
func (s *Store) TaskRecallHitsIn(task string, states ...string) ([]RecallHit, error) {
	return s.recallHitsOf(TaskWords(task), TaskQuery(task), states)
}
