package inject

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 요청 회상(B1)과 세션 중 바뀐 것(B3)의 규칙(설계 · 지연 회상·호스트 계약·어댑터 5.1, 4절 표, 수용 기준 L-2·L-4).

// b1Of is the work recall part of a request's text (from its heading to the end), or "".
func b1Of(text string) string {
	if i := strings.Index(text, recallHead); i >= 0 {
		return text[i:]
	}
	return ""
}

// b3Of is the session news part of a request's text, or "".
func b3Of(text string) string {
	i := strings.Index(text, newsHead)
	if i < 0 {
		return ""
	}
	out := text[i:]
	if j := strings.Index(out, recallHead); j >= 0 {
		out = out[:j]
	}
	return strings.TrimSpace(out)
}

func pnpmRule(t *testing.T, s *memory.Store) memory.Memory {
	t.Helper()
	return save(t, s, memory.KindRule, "이 앱은 pnpm으로 설치한다", "npm install을 쓰지 않는다. lock 파일은 pnpm-lock.yaml 하나만 둔다.", here, true)
}

// L-2: 같은 세션의 같은 요청 두 번째는 요청 회상이 0자이고, compact·clear(reset) 뒤에는 다시 나온다.
func TestB1_같은_세션_같은_요청_두_번째는_0자이고_reset_뒤에는_다시_나온다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	m := pnpmRule(t, s)
	SessionBundle(s, cfg, here, "claude", "A")
	first := PromptInjectionFor(s, cfg, here, "pnpm으로 설치해 줘", "claude", "A", Options{})
	if !strings.Contains(first.Text, m.ID) {
		t.Fatalf("첫 요청에 회상이 없습니다:\n%s", first.Text)
	}
	RecordShown(s, "claude", "A", first)
	if second := PromptInjectionFor(s, cfg, here, "pnpm으로 설치해 줘", "claude", "A", Options{}); second.Text != "" || second.Chars != 0 {
		t.Fatalf("두 번째 같은 요청은 0자여야 합니다:\n%s", second.Text)
	}
	// 다른 세션은 따로 센다.
	if other := PromptInjectionFor(s, cfg, here, "pnpm으로 설치해 줘", "claude", "B", Options{}); !strings.Contains(other.Text, m.ID) {
		t.Fatalf("다른 세션에서 빠졌습니다:\n%s", other.Text)
	}
	ResetSession(s, "claude", "A") // compact·clear
	SessionBundle(s, cfg, here, "claude", "A")
	if again := PromptInjectionFor(s, cfg, here, "pnpm으로 설치해 줘", "claude", "A", Options{}); !strings.Contains(again.Text, m.ID) {
		t.Fatalf("reset 뒤에 다시 나와야 합니다:\n%s", again.Text)
	}
}

// 공개 묶음 st-3: 도구 직전에 넣은 기억도 요청이 직접 물으면 요청 회상에 나온다(도구 표시는 빼지 않는다).
func TestB1_도구_직전에_넣은_기억도_요청이_물으면_나온다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "문서는 Atlas에 발행한다", Body: "로컬 md 파일을 만들지 않는다", Topic: "문서 위치",
		Tools: []string{"mcp__atlas__write_page"}, Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "문서는 Atlas에"}})
	if err != nil {
		t.Fatal(err)
	}
	SessionBundle(s, cfg, here, "claude", "A")
	tool := ToolInjection(s, cfg, here, "claude", "A", "mcp__atlas__write_page")
	if !strings.Contains(tool.Text, m.ID) {
		t.Fatalf("도구 직전에 없습니다:\n%s", tool.Text)
	}
	RecordShown(s, "claude", "A", tool)
	if got := PromptInjectionFor(s, cfg, here, "문서는 Atlas에 발행하나요", "claude", "A", Options{}); !strings.Contains(b1Of(got.Text), m.ID) {
		t.Fatalf("도구 직전에 본 기억이 요청 회상에서 빠졌습니다:\n%s", got.Text)
	}
}

// 세션 묶음(A층)에 든 기억은 요청 회상에 다시 나오지 않는다.
func TestB1_세션_묶음에_든_기억은_나오지_않는다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	core := save(t, s, memory.KindDecision, "배포 순서는 백업 다음 이전이다", "백업을 먼저 확인한다", memory.ScopeGlobal, true)
	if _, err := s.Pin(core.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	other := save(t, s, memory.KindLesson, "배포 순서를 바꾸면 알림을 다시 건다", "알림 설정", here, true)
	bundle := SessionBundle(s, cfg, here, "claude", "A")
	if !strings.Contains(bundle.Text, core.ID) {
		t.Fatalf("핵심이 묶음에 없습니다:\n%s", bundle.Text)
	}
	got := PromptInjectionFor(s, cfg, here, "배포 순서 알려 줘", "claude", "A", Options{})
	if strings.Contains(got.Text, core.ID) || !strings.Contains(got.Text, other.ID) {
		t.Fatalf("묶음 기억이 나오거나 다른 기억이 빠졌습니다:\n%s", got.Text)
	}
}

// 세션 중 바뀐 것(새 기억·정정)으로 알린 기억은 그 뒤 요청 회상에 다시 나오지 않는다.
func TestB1_새_기억_줄과_정정_줄로_알린_기억은_다시_내지_않는다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	old := saveIn(t, s, memory.KindRule, "배포 알림은 메일로 보낸다", memory.ScopeGlobal, "", "배포 알림")
	if _, err := s.Pin(old.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	SessionBundle(s, cfg, here, "claude", "A")
	later(s, time.Hour)
	fresh := save(t, s, memory.KindLesson, "배포 롤백은 태그로 한다", "이전 태그를 다시 배포한다", here, true)
	cur := saveIn(t, s, memory.KindRule, "배포 알림은 채팅으로 보낸다", memory.ScopeGlobal, "", "배포 알림")
	first := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "A", Options{})
	news := b3Of(first.Text)
	if !strings.Contains(news, fresh.ID) || !strings.Contains(news, cur.ID) || b1Of(first.Text) != "" {
		t.Fatalf("새 기억·정정 줄이 없습니다:\n%s", first.Text)
	}
	RecordShown(s, "claude", "A", first)
	for _, q := range []string{"배포 롤백은 태그로 하나", "배포 알림은 채팅으로 보내나"} {
		got := PromptInjectionFor(s, cfg, here, q, "claude", "A", Options{})
		if strings.Contains(got.Text, fresh.ID) || strings.Contains(got.Text, cur.ID) {
			t.Fatalf("%q: 알린 기억이 다시 나왔습니다:\n%s", q, got.Text)
		}
	}
}

var recallLinePat = regexp.MustCompile(`^- (.+) — (m-[0-9a-f]+) · (규칙|사실|결정|교훈) (확정|후보)`)

// 4절 표 B1: 머리말 포함 500자 이하, 기억 줄 3개까지, 가장 확실한 1개만 요약 한 줄(160자 이하), 제목은 80자까지.
// recallChars가 더 작으면 그것을 따른다.
func TestB1_꼴과_길이(t *testing.T) {
	s := store(t)
	long := strings.Repeat("긴 설명이 이어진다 ", 10)
	var ids []string
	for i := 0; i < 8; i++ {
		m := save(t, s, memory.KindRule, fmt.Sprintf("배포 순서 점검 %d번 — %s", i, long), long+long, here, true)
		ids = append(ids, m.ID)
	}
	cfg := config.Default()
	cfg.RecallMinScore, cfg.RecallMinCoverage = 0.1, 0.1 // 꼴만 본다: 여덟 기억이 모두 문턱을 넘게
	inj := PromptInjectionFor(s, cfg, here, "배포 순서 점검 알려 줘", "", "", Options{})
	lines := strings.Split(inj.Text, "\n")
	if lines[0] != recallHead {
		t.Fatalf("머리말 %q:\n%s", lines[0], inj.Text)
	}
	var entries, summaries int
	for i, l := range lines[1:] {
		switch {
		case strings.HasPrefix(l, "- "):
			mm := recallLinePat.FindStringSubmatch(l)
			if mm == nil {
				t.Fatalf("기억 줄 꼴 %q", l)
			}
			if n := utf8.RuneCountInString(mm[1]); n > 80 {
				t.Fatalf("제목 %d자 > 80: %q", n, mm[1])
			}
			entries++
		case strings.HasPrefix(l, "  "):
			if i != 1 {
				t.Fatalf("요약은 첫 기억 줄 다음에만 옵니다(%d번째 줄):\n%s", i+1, inj.Text)
			}
			if n := utf8.RuneCountInString(strings.TrimSpace(l)); n > 160 {
				t.Fatalf("요약 %d자 > 160", n)
			}
			summaries++
		default:
			t.Fatalf("모르는 줄 %q", l)
		}
	}
	if entries != 3 || summaries != 1 || inj.Chars > 500 || len(inj.MemoryIDs) != 3 {
		t.Fatalf("줄 %d 요약 %d 글자 %d ids %v:\n%s", entries, summaries, inj.Chars, inj.MemoryIDs, inj.Text)
	}
	cfg.RecallChars = 200
	small := PromptInjectionFor(s, cfg, here, "배포 순서 점검 알려 줘", "", "", Options{})
	if small.Chars == 0 || small.Chars > 200 {
		t.Fatalf("recallChars 200 → %d자:\n%s", small.Chars, small.Text)
	}
	// 같은 입력에 같은 출력.
	if again := PromptInjectionFor(s, cfg, here, "배포 순서 점검 알려 줘", "", "", Options{}); again.Text != small.Text {
		t.Fatalf("같은 입력에 다른 출력:\n%s\n---\n%s", small.Text, again.Text)
	}
}

// 4절 표 B3: 정정·새 기억 제목 3줄·300자(머리말 포함). 새 기억 줄은 제목만이다. 넘친 것은 다음 요청에 온다.
// L-4: 요청 훅 전체는 2,500자 이하다.
func TestB3_세_줄_300자이고_새_기억은_제목만이다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	SessionBundle(s, cfg, here, "claude", "A")
	later(s, time.Hour)
	var fresh []memory.Memory
	for i := 0; i < 5; i++ {
		fresh = append(fresh, save(t, s, memory.KindLesson, fmt.Sprintf("새로 확정된 교훈 %d번 %s", i, strings.Repeat("제목이 길다 ", 15)),
			"본문은 회상 줄에 나오지 않는다", memory.ScopeGlobal, true))
	}
	first := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "A", Options{})
	news := b3Of(first.Text)
	lines := strings.Split(news, "\n")
	if news == "" || len(lines)-1 > 3 || utf8.RuneCountInString(news) > 300 || strings.Contains(news, "본문은") {
		t.Fatalf("B3 %d줄 %d자:\n%s", len(lines)-1, utf8.RuneCountInString(news), news)
	}
	if first.Chars > MaxHookRunes || MaxHookRunes != 2500 {
		t.Fatalf("요청 훅 %d자 (상한 %d)", first.Chars, MaxHookRunes)
	}
	RecordShown(s, "claude", "A", first)
	second := b3Of(PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "A", Options{}).Text)
	told := 0
	for _, m := range fresh {
		if strings.Contains(news, m.ID) {
			told++
			if strings.Contains(second, m.ID) {
				t.Fatalf("이미 알린 %s를 다시 알립니다:\n%s", m.ID, second)
			}
		} else if strings.Contains(second, m.ID) {
			told++
		}
	}
	if told < 4 {
		t.Fatalf("넘친 새 기억이 다음 요청에 오지 않습니다(%d개):\n%s\n---\n%s", told, news, second)
	}
}

// 설계 5.1 4단계: 찾기 점수와 맞은 비율 둘 다 문턱 이상이어야 한다. 한쪽만 넘으면 나오지 않는다.
func TestB1_문턱_두_값의_경계(t *testing.T) {
	s := store(t)
	m := pnpmRule(t, s)
	save(t, s, memory.KindFact, "캐시는 매주 비운다", "", here, true)
	prompt := "pnpm으로 설치해 캐시 정리"
	hits, err := s.RecallHits(prompt)
	if err != nil {
		t.Fatal(err)
	}
	var h memory.RecallHit
	for _, x := range hits {
		if x.Memory.ID == m.ID {
			h = x
		}
	}
	if h.Score <= 0 || h.Score >= 1 || h.Coverage != 0.5 {
		t.Fatalf("재료가 맞지 않습니다: %+v", h)
	}
	for _, c := range []struct {
		name         string
		score, cover float64
		want         bool
	}{
		{"둘 다 같으면 든다", h.Score, h.Coverage, true},
		{"점수만 모자라면 빠진다", h.Score + 1e-6, h.Coverage, false},
		{"맞은 비율만 모자라면 빠진다", h.Score, h.Coverage + 1e-6, false},
		{"둘 다 모자라면 빠진다", h.Score + 1e-6, h.Coverage + 1e-6, false},
	} {
		cfg := config.Default()
		cfg.RecallMinScore, cfg.RecallMinCoverage = c.score, c.cover
		got := strings.Contains(PromptInjection(s, cfg, here, prompt).Text, m.ID)
		if got != c.want {
			t.Errorf("%s: 나옴 %v, want %v", c.name, got, c.want)
		}
	}
}

// 상대 문턱(설계 보완, 1-6 b6): 두 문턱을 넘어도 찾기 점수가 1위의 recallMinRelative배 미만이면 빠진다.
func TestB1_상대_문턱_밑은_빠진다(t *testing.T) {
	s := store(t)
	strong := save(t, s, memory.KindFact, "pnpm 설치 캐시 정리 순서", "store prune 다음 설치", here, false)
	weak := save(t, s, memory.KindRule, "캐시는 매주 비운다", "", here, true)
	prompt := "pnpm 설치 캐시 정리"
	var ws, ss float64
	hits, _ := s.RecallHits(prompt)
	for _, h := range hits {
		switch h.Memory.ID {
		case weak.ID:
			ws = h.Score
		case strong.ID:
			ss = h.Score
		}
	}
	if !(ws > 0 && ss > ws) {
		t.Fatalf("재료가 맞지 않습니다: 약한 %v 강한 %v", ws, ss)
	}
	for _, c := range []struct {
		name string
		rel  float64
		want bool
	}{
		{"1위의 배수가 약한 점수와 같으면 든다", ws / ss, true},
		{"1위의 배수에 모자라면 빠진다", ws/ss + 1e-6, false},
	} {
		cfg := config.Default()
		cfg.RecallMinScore, cfg.RecallMinCoverage, cfg.RecallMinRelative = 0.01, 0.01, c.rel
		text := PromptInjection(s, cfg, here, prompt).Text
		if got := strings.Contains(text, weak.ID); got != c.want || !strings.Contains(text, strong.ID) {
			t.Errorf("%s: 약한 기억 나옴 %v, want %v (강한 기억은 늘)\n%s", c.name, got, c.want, text)
		}
	}
}

// 분야 기억은 본문 대신 분야 한 줄(신호 수, 가장 맞은 기억의 제목과 id, 찾는 법)로 3줄 안에 든다.
func TestB1_분야_기억은_분야_한_줄로_든다(t *testing.T) {
	s := store(t)
	for i := 0; i < 30; i++ {
		if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: fmt.Sprintf("부가세 신고 항목 %02d번은 이렇게 적는다", i), Body: "본문",
			Tags: []string{"분야:세무"}, Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession, Ref: "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	inj := PromptInjection(s, config.Default(), here, "부가세 신고 항목 적는 법")
	if len(inj.MemoryIDs) != 1 {
		t.Fatalf("분야 줄이 가리킨 기억 하나가 MemoryIDs에 있어야 합니다: %v\n%s", inj.MemoryIDs, inj.Text)
	}
	best, err := s.Get(inj.MemoryIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	want := "- 분야 「세무」 신호 30개 · 가장 맞은 것 「" + best.Title + "」 " + best.ID + " — memory_find tags=분야:세무"
	if !strings.Contains(inj.Text, want) || strings.Count(inj.Text, "\n- ") != 1 {
		t.Fatalf("분야 줄, want %q:\n%s", want, inj.Text)
	}
	if strings.Count(inj.Text, "이렇게 적는다") != 1 || strings.Contains(inj.Text, "  본문") || inj.Chars > 500 {
		t.Fatalf("분야 기억은 한 제목만, 본문 없이 들어야 합니다:\n%s", inj.Text)
	}
}
