package memory

// 저장 거부 목록과 상대 시간(기획 · Orbit Code K15). 대화 중 AI가 남기는 기억(memory_save)에서 비밀값·원본
// 로그·대화 전사·지시문·임시 진행 상태는 받지 않고, 「3주 뒤」처럼 쓴 때에 기대는 말은 절대 날짜로 바꿔 남긴다.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// Why a memory is not saved.
const (
	RejectSecret      = "secret"      // 비밀값
	RejectLog         = "raw_log"     // 원본 로그·스택 추적
	RejectTranscript  = "transcript"  // 대화 전사
	RejectInstruction = "instruction" // 모델을 향한 지시문(주입 공격 꼴)
	RejectProgress    = "progress"    // 임시 진행 상태
)

// ErrRejected is a memory the save screen does not take.
var ErrRejected = errors.New("rejected")

// RejectError says why a memory was not saved.
type RejectError struct{ Reason string }

func (e *RejectError) Error() string {
	switch e.Reason {
	case RejectSecret:
		return i18n.T("memory.err.rejected.secret")
	case RejectLog:
		return i18n.T("memory.err.rejected.raw_log")
	case RejectTranscript:
		return i18n.T("memory.err.rejected.transcript")
	case RejectInstruction:
		return i18n.T("memory.err.rejected.instruction")
	}
	return i18n.T("memory.err.rejected.progress")
}
func (e *RejectError) Unwrap() error { return ErrRejected }

var (
	logLine = []*regexp.Regexp{
		regexp.MustCompile(`^\s*\[?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2})?`),
		regexp.MustCompile(`^\s*\[?(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\]?[\s:]`),
		regexp.MustCompile(`^\s+at \S+\(.*\)\s*$`),
		regexp.MustCompile(`^\s*Traceback \(most recent call last\)`),
		regexp.MustCompile(`^\s*File ".*", line \d+`),
		regexp.MustCompile(`^\s*goroutine \d+ \[`),
		regexp.MustCompile(`^\s+/\S+\.go:\d+`),
	}
	roleLine     = regexp.MustCompile(`(?i)^\s*(user|assistant|human|ai|system|사용자|어시스턴트|에이전트)\s*[:：]`)
	instructions = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(ignore|disregard|forget)\b.{0,20}\b(previous|prior|above|earlier)\b.{0,20}\b(instructions?|messages?|rules?|prompts?)\b`),
		regexp.MustCompile(`(?i)\byou are now\b`),
		regexp.MustCompile(`(?i)</?\s*system\s*>`),
		regexp.MustCompile(`(?i)\breveal\b.{0,20}\bsystem prompt\b`),
		regexp.MustCompile(`(이전|앞의|위의|기존)\s*(지시|지침|명령|규칙)\S*\s*(을|를|은|는)?\s*(모두|전부)?\s*무시`),
		regexp.MustCompile(`시스템\s*프롬프트\S*\s*(무시|덮어|공개|보여)`),
	}
	progress = []*regexp.Regexp{
		regexp.MustCompile(`진행률\s*\d+|\d+\s*%\s*(진행|완료)`),
		regexp.MustCompile(`(?i)^\s*(WIP|TODO)\s*[:：]|\bin progress\b`),
		regexp.MustCompile(`\d+\s*/\s*\d+\s*(완료|단계|끝남|done)`),
		regexp.MustCompile(`(?i)\bstep \d+ of \d+\b`),
		regexp.MustCompile(`^\s*(지금|현재|방금)\s.*\S+는\s*중(이다|임|입니다)?\s*$`),
	}
)

// Screen checks a memory a host's AI saves during a conversation and rewrites
// the relative time in its title and body to absolute dates. Only time counted
// from now is rewritten ("3주 뒤"): an expression counted from an event ("배포 3일 전")
// stays and is listed in the returned notes as not changed. Months move without
// overflowing (one month after January 31 is February 28). It returns the
// notes, or a *RejectError (ErrRejected) for what is not kept: secrets,
// raw logs, transcripts, instructions aimed at the model and, unless the
// user's own words, temporary progress. The source excerpt stays as given, and
// so does a copy of it inside the title or body.
func Screen(in *SaveInput, now time.Time) ([]string, error) {
	text := in.Title + "\n" + in.Body
	reject := func(reason string) ([]string, error) { return nil, &RejectError{Reason: reason} }
	if MaskSecrets(text) != text || MaskSecrets(in.Source.Excerpt) != in.Source.Excerpt {
		return reject(RejectSecret)
	}
	lines := strings.Split(text, "\n")
	logs := 0
	roles := map[string]bool{}
	roleLines := 0
	for _, l := range lines {
		for _, re := range logLine {
			if re.MatchString(l) {
				logs++
				break
			}
		}
		if m := roleLine.FindStringSubmatch(l); m != nil {
			roleLines++
			roles[strings.ToLower(m[1])] = true
		}
	}
	if logs >= 3 {
		return reject(RejectLog)
	}
	if roleLines >= 2 && len(roles) >= 2 {
		return reject(RejectTranscript)
	}
	for _, re := range instructions {
		if re.MatchString(text) {
			return reject(RejectInstruction)
		}
	}
	if in.Source.Kind != SourceQuote {
		for _, re := range progress {
			if re.MatchString(in.Title) || re.MatchString(in.Body) {
				return reject(RejectProgress)
			}
		}
	}
	// 규칙은 언제나 맞는 일하는 방식이라 바꾸지 않는다: 「배포 3일 전」은 사건 기준이고, 제목이 날마다 바뀌면 다른 날
	// 다시 저장한 같은 규칙이 한 제목으로 모이지 않는다(증거 ① 「서로 다른 날 다시 나옴」).
	var notes []string
	if in.Kind == KindRule {
		return notes, nil
	}
	// 사용자 말(quote) 출처의 발췌는 그 말 그대로 남기는 근거다. 제목·본문이 발췌를 그대로 품고 있어도 그 부분은 바꾸지 않는다.
	keep := ""
	if ex := strings.TrimSpace(in.Source.Excerpt); in.Source.Kind == SourceQuote && utf8.RuneCountInString(ex) >= 2 {
		keep = ex
	}
	in.Title = absoluteDatesKeeping(in.Title, keep, now, &notes)
	in.Body = absoluteDatesKeeping(in.Body, keep, now, &notes)
	return notes, nil
}

// absoluteDatesKeeping is absoluteDates that leaves every occurrence of keep as it is.
func absoluteDatesKeeping(s, keep string, now time.Time, notes *[]string) string {
	if keep == "" || !strings.Contains(s, keep) {
		return absoluteDates(s, now, notes)
	}
	parts := strings.Split(s, keep)
	for i, p := range parts {
		parts[i] = absoluteDates(p, now, notes)
	}
	return strings.Join(parts, keep)
}

var (
	koSpan  = regexp.MustCompile(`(\d+)\s*(일|주|주일|개월|달|년)\s*(뒤|후|전)`)
	enIn    = regexp.MustCompile(`(?i)\bin (\d+) (day|week|month|year)s?\b`)
	enAgo   = regexp.MustCompile(`(?i)\b(\d+) (day|week|month|year)s? ago\b`)
	deictic = regexp.MustCompile(`(그저께|그제|어제|오늘|내일|모레|(?:다음|지난|이번)\s?(?:주|달)|다음달|지난달|내년|작년|올해)`)
	enWord  = regexp.MustCompile(`(?i)\b(today|tomorrow|yesterday|(?:next|last|this) (?:week|month))\b`)
)

func span(now time.Time, n int, unit string, sign int) time.Time {
	n *= sign
	switch unit {
	case "일", "day":
		return now.AddDate(0, 0, n)
	case "주", "주일", "week":
		return now.AddDate(0, 0, 7*n)
	case "개월", "달", "month":
		return addMonths(now, n)
	}
	return addMonths(now, 12*n)
}

// addMonths moves t by n months and keeps the day inside the month it lands in: one month after
// January 31 is the end of February, not the start of March (time.AddDate rolls the day over).
func addMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, n, 0)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(t.Day(), last)-1)
}

// 상대 시간이 지금(저장하는 때)을 기준으로 쓴 말인지 가르는 규칙. 「3주 뒤」「2일 전」은 지금 기준일 때만 날짜로 바꾼다.
// 판정은 낱말 사전 없이 글자 모양만 보는 휴리스틱이라 양쪽에 오류가 남는다: 「약 3일 전」(조사 없는 부사 뒤)은
// 지금 기준인데 사건 기준으로 보아 안 바뀌고, 「휴가 3일 전까지」(조사 글자로 끝나는 명사 뒤)는 사건 기준인데 바뀐다.
var (
	// compoundRest maps the last character of a matched span to what, right after it, makes the span part of a
	// longer word ("3개월 전체", "2주 후반"). Anything else that follows (어미·조사: "3주 뒤다", "3일 전부터") ends
	// the expression.
	compoundRest = map[rune][]string{
		'전': {"체", "반", "후", "날", "혀", "용", "환", "략", "원"},
		'후': {"반", "보", "기", "속", "회", "원", "배", "유"},
		'뒤': {"쪽", "늦", "처", "집", "끝", "따"},
	}
	// notEvent are words that never name an event: a connective or demonstrative before a span says nothing
	// about what the span is counted from ("그건 3일 전 일이다").
	notEvent = map[string]bool{"그": true, "이": true, "저": true, "그건": true, "이건": true, "저건": true, "그것": true, "이것": true,
		"그리고": true, "그러나": true, "그런데": true, "그래서": true, "하지만": true, "또": true, "또는": true, "또한": true,
		"이후": true, "이전": true, "그후": true, "그뒤": true, "다만": true, "단": true, "즉": true, "그때": true, "이때": true,
		"벌써": true, "이미": true, "방금": true, "아직": true, "바로": true}
	// beforeWord is the word right before an expression: a bare noun ("배포 3일 전") makes the expression
	// relative to that event; a word that ends in a particle ("만료는 3주 뒤") says when the thing happens, now-relative.
	beforeWord = regexp.MustCompile(`([\p{L}\p{N}_]+) ?$`)
)

// spanIsWordPart says whether the span m, followed by rest, is the start of a longer word and no time expression.
func spanIsWordPart(m, rest string) bool {
	last, _ := utf8.DecodeLastRuneInString(m)
	for _, c := range compoundRest[last] {
		if strings.HasPrefix(rest, c) {
			return true
		}
	}
	return false
}

// eventWord is the noun the span at the end of before is counted from ("배포" in "배포 3일 전"), or "" when the
// span is counted from now: nothing before it, punctuation, a connective, or a word that carries a particle.
func eventWord(before string) string {
	m := beforeWord.FindStringSubmatch(before)
	if m == nil {
		return ""
	}
	w := m[1]
	if notEvent[w] {
		return ""
	}
	if last, _ := utf8.DecodeLastRuneInString(w); strings.ContainsRune("는은이가에도를을만", last) {
		return ""
	}
	return w
}

func monday(t time.Time) time.Time {
	wd := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -wd)
}

// absoluteDates rewrites the relative time of s (written at now) to dates.
func absoluteDates(s string, now time.Time, notes *[]string) string {
	date := func(t time.Time) string { return t.Format("2006-01-02") }
	note := func(from, to string) { *notes = append(*notes, fmt.Sprintf("%s → %s", from, to)) }
	s = replaceKoSpans(s, func(p []string) string {
		n, _ := strconv.Atoi(p[1])
		sign := 1
		if p[3] == "전" {
			sign = -1
		}
		to := date(span(now, n, p[2], sign))
		note(p[0], to)
		return to
	}, func(event, m string) {
		// rewrites는 언어와 무관하게 「원문 → 결과」 꼴이라 이 알림도 고정 문구다(바꾼 것은 날짜, 안 바꾼 것은 「바꾸지 않음」).
		*notes = append(*notes, event+" "+m+" → 바꾸지 않음(사건 기준)")
	})
	s = enIn.ReplaceAllStringFunc(s, func(m string) string {
		p := enIn.FindStringSubmatch(m)
		n, _ := strconv.Atoi(p[1])
		to := "on " + date(span(now, n, strings.ToLower(p[2]), 1))
		note(m, to)
		return to
	})
	s = enAgo.ReplaceAllStringFunc(s, func(m string) string {
		p := enAgo.FindStringSubmatch(m)
		n, _ := strconv.Atoi(p[1])
		to := "on " + date(span(now, n, strings.ToLower(p[2]), -1))
		note(m, to)
		return to
	})
	s = replaceDeictic(s, deictic, func(w string) string {
		switch strings.ReplaceAll(w, " ", "") {
		case "그저께", "그제":
			return date(now.AddDate(0, 0, -2))
		case "어제":
			return date(now.AddDate(0, 0, -1))
		case "오늘":
			return date(now)
		case "내일":
			return date(now.AddDate(0, 0, 1))
		case "모레":
			return date(now.AddDate(0, 0, 2))
		case "다음주":
			return date(monday(now).AddDate(0, 0, 7)) + " 주"
		case "지난주":
			return date(monday(now).AddDate(0, 0, -7)) + " 주"
		case "이번주":
			return date(monday(now)) + " 주"
		case "다음달":
			return addMonths(now, 1).Format("2006-01")
		case "지난달":
			return addMonths(now, -1).Format("2006-01")
		case "이번달":
			return now.Format("2006-01")
		case "내년":
			return strconv.Itoa(now.Year()+1) + "년"
		case "작년":
			return strconv.Itoa(now.Year()-1) + "년"
		case "올해":
			return strconv.Itoa(now.Year()) + "년"
		}
		return w
	}, note)
	s = replaceDeictic(s, enWord, func(w string) string {
		switch strings.ToLower(w) {
		case "today":
			return "on " + date(now)
		case "tomorrow":
			return "on " + date(now.AddDate(0, 0, 1))
		case "yesterday":
			return "on " + date(now.AddDate(0, 0, -1))
		case "next week":
			return "in the week of " + date(monday(now).AddDate(0, 0, 7))
		case "last week":
			return "in the week of " + date(monday(now).AddDate(0, 0, -7))
		case "this week":
			return "in the week of " + date(monday(now))
		case "next month":
			return "in " + addMonths(now, 1).Format("2006-01")
		case "last month":
			return "in " + addMonths(now, -1).Format("2006-01")
		case "this month":
			return "in " + now.Format("2006-01")
		}
		return w
	}, note)
	return s
}

// replaceKoSpans rewrites the Korean spans ("3주 뒤") that are counted from now: to gives the date of a span
// (p is the match and its groups). A span counted from an event ("배포 3일 전") stays and is told to kept;
// a span that is part of a longer word ("3개월 전체") stays without a word.
func replaceKoSpans(s string, to func(p []string) string, kept func(event, span string)) string {
	var b strings.Builder
	last, prevEnd := 0, -1
	for _, loc := range koSpan.FindAllStringSubmatchIndex(s, -1) {
		m := s[loc[0]:loc[1]]
		if spanIsWordPart(m, s[loc[1]:]) {
			continue
		}
		// 바로 앞이 다른 표현이면 그 표현의 단위어(「3일 전 3일 전」의 「전」)는 사건 낱말이 아니다.
		follows := prevEnd >= 0 && strings.TrimSpace(s[prevEnd:loc[0]]) == ""
		prevEnd = loc[1]
		if ev := eventWord(s[:loc[0]]); ev != "" && !follows {
			kept(ev, m)
			continue
		}
		p := make([]string, 0, 4)
		for i := 0; i < len(loc); i += 2 {
			p = append(p, s[loc[i]:loc[i+1]])
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(to(p))
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// replaceDeictic replaces the words re finds, except where a Korean word
// carries on (오늘날 is "nowadays", not a day).
func replaceDeictic(s string, re *regexp.Regexp, to func(string) string, note func(string, string)) string {
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringIndex(s, -1) {
		w := s[loc[0]:loc[1]]
		rest := s[loc[1]:]
		if strings.HasPrefix(rest, "날") {
			continue
		}
		r := to(w)
		b.WriteString(s[last:loc[0]])
		b.WriteString(r)
		if r != w {
			note(w, r)
		}
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}
