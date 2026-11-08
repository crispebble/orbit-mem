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
// the relative time in its title and body to absolute dates. It returns the
// rewrites made, or a *RejectError (ErrRejected) for what is not kept: secrets,
// raw logs, transcripts, instructions aimed at the model and, unless the
// user's own words, temporary progress. The source excerpt stays as given.
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
	in.Title = absoluteDates(in.Title, now, &notes)
	in.Body = absoluteDates(in.Body, now, &notes)
	return notes, nil
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
		return now.AddDate(0, n, 0)
	}
	return now.AddDate(n, 0, 0)
}

func monday(t time.Time) time.Time {
	wd := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -wd)
}

// absoluteDates rewrites the relative time of s (written at now) to dates.
func absoluteDates(s string, now time.Time, notes *[]string) string {
	date := func(t time.Time) string { return t.Format("2006-01-02") }
	note := func(from, to string) { *notes = append(*notes, fmt.Sprintf("%s → %s", from, to)) }
	s = koSpan.ReplaceAllStringFunc(s, func(m string) string {
		p := koSpan.FindStringSubmatch(m)
		n, _ := strconv.Atoi(p[1])
		sign := 1
		if p[3] == "전" {
			sign = -1
		}
		to := date(span(now, n, p[2], sign))
		note(m, to)
		return to
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
			return now.AddDate(0, 1, 0).Format("2006-01")
		case "지난달":
			return now.AddDate(0, -1, 0).Format("2006-01")
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
			return "in " + now.AddDate(0, 1, 0).Format("2006-01")
		case "last month":
			return "in " + now.AddDate(0, -1, 0).Format("2006-01")
		case "this month":
			return "in " + now.Format("2006-01")
		}
		return w
	}, note)
	return s
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
