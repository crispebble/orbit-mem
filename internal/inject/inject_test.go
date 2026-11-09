package inject

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func store(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.Local) }
	t.Cleanup(func() { s.Close() })
	return s
}

func save(t *testing.T, s *memory.Store, kind, title, body, scope string, quote bool) memory.Memory {
	t.Helper()
	src := memory.Source{Kind: memory.SourceSession, Ref: "x"}
	if quote {
		src = memory.Source{Kind: memory.SourceQuote, Excerpt: title}
	}
	m, err := s.Save(memory.SaveInput{Kind: kind, Title: title, Body: body, Scope: scope, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const here = "project:github.com/me/app"

func TestSessionStart_확정_규칙_전문과_최근_결정_제목_대기_제안_줄은_없다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	save(t, s, memory.KindRule, "이 앱은 pnpm", "npm을 쓰지 않는다", here, true)
	save(t, s, memory.KindRule, "다른 앱 규칙", "보이면 안 됨", "project:github.com/me/other", true)
	save(t, s, memory.KindRule, "후보 규칙", "확정 전이라 안 보임", memory.ScopeGlobal, false)
	save(t, s, memory.KindDecision, "Orbit 먼저 만든다", "본문은 안 넣음", memory.ScopeGlobal, true)
	c := save(t, s, memory.KindFact, "후보 사실", "", memory.ScopeGlobal, false)
	s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "확정?", MemoryIDs: []string{c.ID}})

	got := SessionStart(s, config.Default(), here)
	// 상시 핵심 밖 확정 규칙은 넣지 않고 수만 알린다(S3-R6): 전역과 이 앱 규칙 둘, 다른 앱·후보는 세지 않는다.
	for _, want := range []string{"먼저 찾는 곳", "확정 규칙 2개", "결정 1개", "memory_find", "Orbit 먼저 만든다"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// 사람 승인 대기열은 없다(설계 · orbit-mem 2판 18.6): 옛 대기 제안이 남아 있어도 줄을 붙이지 않는다.
	for _, bad := range []string{"답은 한글 존댓말", "이 앱은 pnpm", "다른 앱 규칙", "후보 규칙", "본문은 안 넣음", "제안 1건", "대기"} {
		if strings.Contains(got, bad) {
			t.Errorf("must not contain %q", bad)
		}
	}
}

func TestSessionStart_주입_수준(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "규칙", "본문", memory.ScopeGlobal, true)
	save(t, s, memory.KindDecision, "결정", "", memory.ScopeGlobal, true)
	cfg := config.Default()
	cfg.Inject = config.InjectRules
	got := SessionStart(s, cfg, here)
	if !strings.Contains(got, "규칙") || strings.Contains(got, "결정") {
		t.Fatalf("rules level: %s", got)
	}
	cfg.Inject = config.InjectOff
	if got := SessionStart(s, cfg, here); got != "" {
		t.Fatalf("off level: %q", got)
	}
}

func TestSessionStart_기억이_없으면_빈_문자열(t *testing.T) {
	if got := SessionStart(store(t), config.Default(), here); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestPrompt_강하게_맞는_기억의_제목과_id와_본문_앞부분(t *testing.T) {
	s := store(t)
	a := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "본문 앞부분"+strings.Repeat("가", 300)+"끝부분", memory.ScopeGlobal, false)
	save(t, s, memory.KindLesson, "launchd 회고 예약", "", "project:github.com/me/other", true)
	got := Prompt(s, config.Default(), here, "codex exec를 백그라운드에서 돌렸더니 멈춰요")
	if !strings.Contains(got, a.ID) || !strings.Contains(got, a.Title) || !strings.Contains(got, "본문 앞부분") || strings.Contains(got, "끝부분") {
		t.Fatalf("got %q", got)
	}
	if got := Prompt(s, config.Default(), here, "오늘 점심 뭐 먹지"); got != "" {
		t.Fatalf("weak prompt: %q", got)
	}
	if got := Prompt(s, config.Default(), here, "launchd 회고 예약 다시"); got != "" {
		t.Fatalf("other project leaked: %q", got)
	}
	cfg := config.Default()
	cfg.Inject = config.InjectRules
	if got := Prompt(s, cfg, here, "codex exec를 백그라운드에서 돌렸더니 멈춰요"); got != "" {
		t.Fatalf("rules level must not add per prompt: %q", got)
	}
}

func TestHookOutput_호스트가_읽는_JSON(t *testing.T) {
	out := HookOutput("SessionStart", "문맥")
	if !strings.Contains(out, `"hookEventName":"SessionStart"`) || !strings.Contains(out, `"additionalContext":"문맥"`) {
		t.Fatal(out)
	}
	if HookOutput("UserPromptSubmit", "") != "" {
		t.Fatal("empty context must print nothing")
	}
}

func TestSessionStartInjection_넣은_기억_id와_넣지_못한_규칙_수(t *testing.T) {
	s := store(t)
	long := strings.Repeat("가", 2500)
	var rules []memory.Memory
	for i := 0; i < 4; i++ {
		rules = append(rules, save(t, s, memory.KindRule, "긴 규칙 "+string(rune('A'+i)), long, memory.ScopeGlobal, true))
	}
	d := save(t, s, memory.KindDecision, "결정 하나", "", memory.ScopeGlobal, true)
	other := save(t, s, memory.KindRule, "다른 앱 규칙", "", "project:github.com/me/other", true)

	inj := SessionStartInjection(s, rulesCfg(), here) // 규칙을 채우는 판(inject=rules)
	if inj.Text != SessionStart(s, rulesCfg(), here) || inj.Chars == 0 || inj.Chars > MaxSessionStartRunes {
		t.Fatalf("text mismatch or too long: %d", inj.Chars)
	}
	ids := strings.Join(inj.MemoryIDs, ",")
	in := 0
	for _, m := range rules {
		if strings.Contains(ids, m.ID) {
			in++
		}
	}
	_ = d // inject=rules는 결정 제목 줄을 넣지 않는다
	if strings.Contains(ids, other.ID) {
		t.Errorf("ids %s", ids)
	}
	// 넣은 규칙은 전문, 못 넣은 규칙은 id도 넣지 않고 수만 센다.
	if in+inj.Omitted != len(rules) || inj.Omitted < 1 || in < 1 {
		t.Errorf("in=%d omitted=%d", in, inj.Omitted)
	}
}

func TestPromptInjection_넣은_기억_id(t *testing.T) {
	s := store(t)
	a := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "", memory.ScopeGlobal, false)
	inj := PromptInjection(s, config.Default(), here, "codex exec를 백그라운드에서 돌렸더니 멈춰요")
	if len(inj.MemoryIDs) != 1 || inj.MemoryIDs[0] != a.ID || inj.Chars == 0 {
		t.Fatalf("inj = %+v", inj)
	}
	if inj := PromptInjection(s, config.Default(), here, "오늘 점심 뭐 먹지"); len(inj.MemoryIDs) != 0 || inj.Text != "" {
		t.Fatalf("weak = %+v", inj)
	}
}

func TestSessionBundle_같은_세션은_처음_묶음을_그대로_쓴다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindDecision, "첫 결정", "본문", memory.ScopeGlobal, true)
	first := SessionBundle(s, config.Default(), here, "claude", "s1")
	save(t, s, memory.KindDecision, "세션 중에 확정된 규칙", "본문", memory.ScopeGlobal, true)
	again := SessionBundle(s, config.Default(), here, "claude", "s1")
	if again.Text != first.Text || strings.Contains(again.Text, "세션 중에") || strings.Join(again.MemoryIDs, ",") != strings.Join(first.MemoryIDs, ",") {
		t.Fatalf("bundle changed within the session:\n%s\n---\n%s", first.Text, again.Text)
	}
	if next := SessionBundle(s, config.Default(), here, "claude", "s2"); !strings.Contains(next.Text, "세션 중에 확정된 규칙") {
		t.Fatalf("next session must see the new rule:\n%s", next.Text)
	}
	if none := SessionBundle(s, config.Default(), here, "claude", ""); !strings.Contains(none.Text, "세션 중에 확정된 규칙") {
		t.Fatalf("no session id builds afresh")
	}
}

func TestPromptInjection_작업_회상은_순위대로_글자_수_안에서_묶는다(t *testing.T) {
	s := store(t)
	g := save(t, s, memory.KindLesson, "launchd 예약 회고 다시 걸기", "전역 본문", memory.ScopeGlobal, false)
	p := save(t, s, memory.KindLesson, "launchd 예약 회고 다시 걸기 (이 앱)", "프로젝트 본문", here, false)
	cfg := config.Default()
	inj := PromptInjection(s, cfg, here, "launchd 예약 회고를 다시 걸어야 해")
	if len(inj.MemoryIDs) != 2 || inj.MemoryIDs[0] != p.ID || inj.MemoryIDs[1] != g.ID {
		t.Fatalf("project first: %v (p=%s g=%s)\n%s", inj.MemoryIDs, p.ID, g.ID, inj.Text)
	}
	// 같은 점수·같은 범위면 사용 판정(따름 − 상관없음)이 높은 것이 먼저다.
	s2 := store(t)
	a := save(t, s2, memory.KindLesson, "codex exec 백그라운드 stdin 닫기", "가", memory.ScopeGlobal, false)
	b := save(t, s2, memory.KindLesson, "codex exec 백그라운드 stdin 닫기 두번째", "나", memory.ScopeGlobal, false)
	for i, v := range []string{memory.UsageIrrelevant, memory.UsageIrrelevant} {
		s2.RecordEvent(memory.Event{Kind: memory.EventMemoryUsed, Session: "s" + string(rune('0'+i))}, map[string]any{"memoryId": b.ID, "verdict": v})
	}
	s2.RecordEvent(memory.Event{Kind: memory.EventMemoryUsed, Session: "s9"}, map[string]any{"memoryId": a.ID, "verdict": memory.UsageFollowed})
	inj = PromptInjection(s2, cfg, here, "codex exec 백그라운드 stdin")
	if len(inj.MemoryIDs) != 2 || inj.MemoryIDs[0] != a.ID {
		t.Fatalf("usage order: %v (a=%s)", inj.MemoryIDs, a.ID)
	}
	// 글자 수 상한: 넘으면 본문을 빼고, 제목도 안 들어가면 멈춘다.
	s3 := store(t)
	var ids []string
	for i := 0; i < 8; i++ {
		m := save(t, s3, memory.KindFact, "회고 예약 launchd 설정 "+string(rune('A'+i)), strings.Repeat("본", 180), memory.ScopeGlobal, false)
		ids = append(ids, m.ID)
	}
	cfg.RecallChars = 400
	inj = PromptInjection(s3, cfg, here, "회고 예약 launchd 설정")
	if inj.Chars > 400 || len(inj.MemoryIDs) == 0 || len(inj.MemoryIDs) == 8 {
		t.Fatalf("budget: %d chars, %d memories\n%s", inj.Chars, len(inj.MemoryIDs), inj.Text)
	}
}

// 명세 · O3-5 「PC 기억 분야 묶음」(수용 기준 8): 분야 표식(분야:<이름>) 기억은 세션 시작 묶음과 작업 회상에 본문으로 들어가지 않는다.
// 요청이 한 분야 기억과 강하게 맞으면 그 분야의 신호(제목과 id)만 모두 넣고, 맞지 않는 분야는 넣지 않는다.
func TestPrompt_분야_기억은_신호로만_넣는다(t *testing.T) {
	s := store(t)
	domain := func(kind, title, body, name string) memory.Memory {
		m, err := s.Save(memory.SaveInput{Kind: kind, Title: title, Body: body, Tags: []string{"분야:" + name}, Scope: memory.ScopeGlobal,
			State: memory.StateConfirmed, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a := domain(memory.KindFact, "부가세 신고 기한은 분기 다음 달 25일", "1기 확정은 7월 25일까지 신고한다", "세무")
	b := domain(memory.KindRule, "세금계산서 발행은 공급 시기 다음 달 10일까지", "지연 발행은 가산세", "세무")
	c := domain(memory.KindFact, "출고 마감은 매일 오후 3시", "그 뒤 주문은 다음 날 출고", "물류")
	plain := save(t, s, memory.KindLesson, "신고 기한 질문에는 날짜를 먼저 답한다", "근거 조항은 뒤에", memory.ScopeGlobal, false)

	start := SessionStart(s, config.Default(), here)
	if strings.Contains(start, b.Title) {
		t.Errorf("분야 규칙은 세션 시작 묶음에 넣지 않습니다:\n%s", start)
	}
	inj := PromptInjection(s, config.Default(), here, "부가세 신고 기한 확인해줘")
	for _, want := range []string{"분야 「세무」", a.Title, a.ID, b.Title, b.ID, plain.Title} {
		if !strings.Contains(inj.Text, want) {
			t.Errorf("%q가 없습니다:\n%s", want, inj.Text)
		}
	}
	for _, not := range []string{"1기 확정은", "지연 발행은", c.Title, "물류"} {
		if strings.Contains(inj.Text, not) {
			t.Errorf("%q는 들어가면 안 됩니다:\n%s", not, inj.Text)
		}
	}
}

// 검토 지적 10: 분야 신호가 글자 수를 넘으면 보인 수와 전체 수를 알린다.
func TestPrompt_분야_신호가_넘치면_알린다(t *testing.T) {
	s := store(t)
	for i := 0; i < 30; i++ {
		if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: fmt.Sprintf("부가세 신고 항목 %02d번은 이렇게 적는다", i), Tags: []string{"분야:세무"},
			Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession, Ref: "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.RecallChars = 300
	inj := PromptInjection(s, cfg, here, "부가세 신고 항목 적는 법")
	if !strings.Contains(inj.Text, "신호 30개 중") || len(inj.MemoryIDs) >= 30 {
		t.Fatalf("넘친 신호 알림:\n%s", inj.Text)
	}
}

// AU-6 AC-6(A2): 후보는 회상에 쓰되 확정보다 뒤에 두고 「후보」로 표시한다.
func TestPromptInjection_후보는_확정_뒤에_후보로_표시한다(t *testing.T) {
	s := store(t)
	cand := save(t, s, memory.KindLesson, "launchd 예약 회고 다시 걸기 전에 plist 지우기", "후보 본문", memory.ScopeGlobal, false)
	conf := save(t, s, memory.KindLesson, "launchd 예약 회고", "확정 본문", memory.ScopeGlobal, true)
	inj := PromptInjection(s, config.Default(), here, "launchd 예약 회고 다시 걸기 전에 plist 지우기")
	if len(inj.MemoryIDs) != 2 || inj.MemoryIDs[0] != conf.ID || inj.MemoryIDs[1] != cand.ID {
		t.Fatalf("확정이 먼저가 아닙니다: %v (conf=%s cand=%s)\n%s", inj.MemoryIDs, conf.ID, cand.ID, inj.Text)
	}
	if !strings.Contains(inj.Text, cand.ID+" · 교훈 후보") {
		t.Fatalf("후보 표시가 없습니다:\n%s", inj.Text)
	}
}

// AU-6 AC-4: 해로움 판정으로 자동 되돌린 변경을 세션 시작에 알린다.
func TestSessionStart_자동_되돌림을_알린다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	if err := s.RecordEvent(memory.Event{Kind: memory.EventAutoReverted}, map[string]any{"ledgerId": "l-1", "revertId": "l-2", "error": ""}); err != nil {
		t.Fatal(err)
	}
	got := SessionStart(s, config.Default(), here)
	if !strings.Contains(got, "자동 변경 1건을 되돌림") {
		t.Fatalf("알림이 없습니다:\n%s", got)
	}
}

// 출처가 믿을 수 없는 것뿐인 기억은 회상 줄에 「믿을 수 없음」이 붙는다(D7). 회상 글은 고정 한국어라 언어 설정과 무관하다.
func TestPrompt_믿을_수_없는_기억은_회상_줄에_표시가_붙는다(t *testing.T) {
	s := store(t)
	ok := save(t, s, memory.KindLesson, "stdin을 닫고 codex exec를 백그라운드로 돌린다", "본문", memory.ScopeGlobal, false)
	web, err := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "codex exec 백그라운드 실행은 stdin을 닫으면 된다는 웹 글", Body: "본문", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceFile, Ref: "web", Trust: memory.TrustUntrusted}})
	if err != nil {
		t.Fatal(err)
	}
	got := Prompt(s, config.Default(), here, "codex exec를 백그라운드에서 stdin 닫기")
	lineOf := func(id string) string {
		for _, l := range strings.Split(got, "\n") {
			if strings.Contains(l, id) {
				return l
			}
		}
		return ""
	}
	if l := lineOf(web.ID); !strings.Contains(l, "믿을 수 없음") {
		t.Fatalf("웹 글 줄에 표시가 없다: %q\n%s", l, got)
	}
	if l := lineOf(ok.ID); l == "" || strings.Contains(l, "믿을 수 없음") {
		t.Fatalf("믿을 만한 기억 줄: %q", l)
	}
}
