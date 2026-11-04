package inject

// 설계 · orbit-mem 2판 S1·S2의 수용 기준(AC-1·AC-2·AC-4·AC-5·AC-8)을 주입 쪽에서 잰다.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// thousand fills a store with 1,000 memories shaped like this PC's DB (2026-11-03): many
// confirmed global rules with bodies around 574 characters, decisions, facts and lessons.
func thousand(t *testing.T, s *memory.Store) []memory.Memory {
	t.Helper()
	var rules []memory.Memory
	body := strings.Repeat("규칙 본문은 이렇게 길다. ", 40) // 약 600자
	for i := 0; i < 1000; i++ {
		var kind, scope string
		quote := true
		switch {
		case i < 300:
			kind, scope = memory.KindRule, memory.ScopeGlobal
		case i < 400:
			kind, scope = memory.KindRule, here
		case i < 600:
			kind, scope = memory.KindDecision, memory.ScopeGlobal
		case i < 800:
			kind, scope, quote = memory.KindFact, memory.ScopeGlobal, false
		default:
			kind, scope, quote = memory.KindLesson, here, false
		}
		word := fmt.Sprintf("%c%c%c", 0xAC00+(i*37)%11172, 0xAC00+(i*91+5)%11172, 0xAC00+(i*53+11)%11172) // 기억마다 다른 낱말
		m := save(t, s, kind, fmt.Sprintf("%s %s 배포 상단바 시안 메모", kind, word), body, scope, quote)
		if kind == memory.KindRule {
			rules = append(rules, m)
		}
	}
	return rules
}

func TestAC1_어떤_주입도_한도를_넘지_않고_제목만_목록이_없다(t *testing.T) {
	s := store(t)
	thousand(t, s)
	for _, opts := range []Options{{}, {Brief: true}} {
		inj := SessionStartInjectionWith(s, config.Default(), here, opts)
		if inj.Chars > MaxSessionStartRunes || utf8.RuneCountInString(inj.Text) > MaxSessionStartRunes {
			t.Fatalf("brief=%v: session start %d > %d", opts.Brief, inj.Chars, MaxSessionStartRunes)
		}
		if strings.Contains(inj.Text, "제목만") {
			t.Fatalf("brief=%v: the title-only list is back:\n%s", opts.Brief, inj.Text)
		}
		// 상시 핵심 밖 규칙은 넣지 않고 이 폴더에서 보이는 수와 먼저 찾을 곳을 알린다(S3-R6): 전역 300 + 이 앱 100.
		if inj.Omitted != 0 || !strings.Contains(inj.Text, "확정 규칙 400개") || !strings.Contains(inj.Text, "memory_find") {
			t.Fatalf("brief=%v: omitted=%d, find line:\n%s", opts.Brief, inj.Omitted, inj.Text)
		}
	}
	cfg := config.Default()
	cfg.RecallChars = 50000 // 설정이 커도 훅 문맥 한도는 넘지 않는다
	p := PromptInjectionFor(s, cfg, here, "배포 상단바 시안 메모", "claude", "", Options{})
	if p.Chars == 0 || p.Chars > MaxHookRunes {
		t.Fatalf("prompt chars %d (max %d)", p.Chars, MaxHookRunes)
	}
}

func TestAC5_세션_시작에_못_들어간_확정_규칙은_요청_회상_후보다(t *testing.T) {
	s := store(t)
	rules := thousand(t, s)
	if _, err := s.Pin(rules[0].ID, memory.Who{}); err != nil { // 묶음에는 상시 핵심만 든다(S3-R6)
		t.Fatal(err)
	}
	bundle := SessionBundle(s, config.Default(), here, "claude", "s1")
	byID, why, err := CandidatesFor(s, here, Options{}, bundle.MemoryIDs)
	if err != nil {
		t.Fatal(err)
	}
	nowhere := 0
	var outside memory.Memory
	for _, r := range rules {
		in := slices.Contains(bundle.MemoryIDs, r.ID)
		_, eligible := byID[r.ID]
		if !in && !eligible {
			nowhere++
			t.Errorf("rule %s is neither in the bundle nor a recall candidate (%s)", r.ID, why[r.ID])
		}
		if !in && outside.ID == "" {
			outside = r
		}
	}
	if nowhere != 0 || outside.ID == "" {
		t.Fatalf("nowhere=%d outside=%q", nowhere, outside.ID)
	}
	// 묶음 밖 규칙은 요청이 맞으면 회상으로 온다.
	got := PromptInjectionFor(s, config.Default(), here, outside.Title, "claude", "s1", Options{})
	if !slices.Contains(got.MemoryIDs, outside.ID) {
		t.Fatalf("rule outside the bundle not recalled:\n%s", got.Text)
	}
	// 묶음 안 규칙은 다시 넣지 않는다.
	inside := rules[0].ID
	m, _ := s.Get(inside)
	if got := PromptInjectionFor(s, config.Default(), here, m.Title, "claude", "s1", Options{}); slices.Contains(got.MemoryIDs, inside) {
		t.Fatalf("rule in the bundle came again:\n%s", got.Text)
	}
}

func later(s *memory.Store, d time.Duration) {
	at := s.Now().Add(d)
	s.Now = func() time.Time { return at }
}

func TestAC4_세션_도중_대체된_기억은_정정으로_알리고_compact는_묶음을_다시_만든다(t *testing.T) {
	s := store(t)
	old, _ := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "보고는 영어로", Body: "영어로 쓴다", Topic: "보고 언어",
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "보고는 영어로"}})
	if _, err := s.Pin(old.ID, memory.Who{}); err != nil { // 묶음에 드는 것은 상시 핵심이다(S3-R6). 새 판이 승격을 잇는다
		t.Fatal(err)
	}
	first := SessionBundle(s, config.Default(), here, "claude", "s1")
	if !slices.Contains(first.MemoryIDs, old.ID) {
		t.Fatalf("bundle lacks the rule:\n%s", first.Text)
	}
	later(s, time.Hour)
	cur, _ := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "보고는 한국어로", Body: "한국어로 쓴다", Topic: "보고 언어",
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "보고는 한국어로"}})
	if got, _ := s.Get(old.ID); got.State != memory.StateStale || got.SupersededBy != cur.ID {
		t.Fatalf("old = %+v", got)
	}
	p := PromptInjectionFor(s, config.Default(), here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
	if !strings.Contains(p.Text, "정정") || !strings.Contains(p.Text, "보고는 영어로") || !strings.Contains(p.Text, cur.ID) {
		t.Fatalf("no correction:\n%s", p.Text)
	}
	RecordShown(s, "claude", "s1", p)
	if again := PromptInjectionFor(s, config.Default(), here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); strings.Contains(again.Text, "정정") {
		t.Fatalf("correction repeated:\n%s", again.Text)
	}
	// resume은 같은 묶음, compact·clear는 새 묶음.
	if again := SessionBundle(s, config.Default(), here, "claude", "s1"); again.Text != first.Text {
		t.Fatal("resume must keep the bundle")
	}
	ResetSession(s, "claude", "s1")
	next := SessionBundle(s, config.Default(), here, "claude", "s1")
	if slices.Contains(next.MemoryIDs, old.ID) || !slices.Contains(next.MemoryIDs, cur.ID) {
		t.Fatalf("compact bundle:\n%s", next.Text)
	}
}

func TestAC8_세션_시작_뒤_확정된_기억은_새_기억으로_도구_직전에도_들어간다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	SessionBundle(s, config.Default(), here, "claude", "A")
	later(s, time.Minute)
	// 세션 B가 문서 위치 규칙을 저장한다.
	doc, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "Orbit 문서는 Orbit Space에 쓴다", Body: "onidot Space에 쓰지 않는다",
		Topic: "atlas 문서 위치", Tools: []string{"mcp__atlas__write_page"}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "Orbit 문서는 Orbit Space에"}})
	if err != nil {
		t.Fatal(err)
	}
	other := save(t, s, memory.KindRule, "다른 앱 규칙", "", "project:github.com/me/other", true)
	later(s, time.Minute)
	p := PromptInjectionFor(s, config.Default(), here, "설계 문서를 발행해 줘", "claude", "A", Options{})
	if !strings.Contains(p.Text, "새 기억") || !strings.Contains(p.Text, doc.ID) || strings.Contains(p.Text, other.ID) {
		t.Fatalf("new memory line:\n%s", p.Text)
	}
	RecordShown(s, "claude", "A", p)
	if again := PromptInjectionFor(s, config.Default(), here, "설계 문서를 발행해 줘", "claude", "A", Options{}); strings.Contains(again.Text, "새 기억") {
		t.Fatalf("new memory repeated:\n%s", again.Text)
	}
	// 새로 시작한 세션의 묶음에 이미 있으면 새 기억이 아니다.
	SessionBundle(s, config.Default(), here, "claude", "C")
	if c := PromptInjectionFor(s, config.Default(), here, "설계 문서를 발행해 줘", "claude", "C", Options{}); strings.Contains(c.Text, "새 기억") {
		t.Fatalf("session C started after the save:\n%s", c.Text)
	}

	tool := ToolInjection(s, config.Default(), here, "claude", "A", "mcp__atlas__write_page")
	if !strings.Contains(tool.Text, doc.ID) || !strings.Contains(tool.Text, "onidot Space에 쓰지 않는다") || tool.Chars > MaxToolRunes {
		t.Fatalf("tool context:\n%s", tool.Text)
	}
	RecordShown(s, "claude", "A", tool)
	if again := ToolInjection(s, config.Default(), here, "claude", "A", "mcp__atlas__write_page"); again.Text != "" {
		t.Fatalf("tool context repeated in the session:\n%s", again.Text)
	}
	if b := ToolInjection(s, config.Default(), here, "claude", "B", "mcp__atlas__read"); b.Text != "" {
		t.Fatalf("another tool:\n%s", b.Text)
	}
}

func TestToolInjection_접두_트리거와_개수_글자_한도(t *testing.T) {
	s := store(t)
	for i := 0; i < 5; i++ {
		_, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: fmt.Sprintf("atlas 규칙 %d", i), Body: strings.Repeat("길다 ", 200),
			Topic: fmt.Sprintf("atlas %d", i), Tools: []string{"mcp__atlas__*"}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	got := ToolInjection(s, config.Default(), here, "claude", "s", "mcp__atlas__write_page")
	if len(got.MemoryIDs) == 0 || len(got.MemoryIDs) > MaxToolMemories || got.Chars > MaxToolRunes {
		t.Fatalf("ids=%d chars=%d:\n%s", len(got.MemoryIDs), got.Chars, got.Text)
	}
	if ToolInjection(s, config.Default(), here, "claude", "s", "mcp__atlassian__x").Text != "" {
		t.Fatal("a prefix must stop at the server name")
	}
}

// AC-2 알약 재현: 옛 결정 둘(전역·확정, 주제 없음)을 새 결정 하나가 supersedes로 대체하면, 고정 질의 6개 × 폴더 4곳의
// 회상·세션 시작 어디에도 옛 판이 나오지 않는다.
func TestAC2_알약_재현_옛_결정은_어디에도_나오지_않는다(t *testing.T) {
	s := store(t)
	old1 := save(t, s, memory.KindDecision, "상단바 상태는 아일랜드 알약으로 한다", "제목 막대 에이전트 현황을 알약으로", memory.ScopeGlobal, true)
	old2 := save(t, s, memory.KindDecision, "제목 막대 에이전트 현황은 알약 아일랜드", "상단바 시안 v3 승인", memory.ScopeGlobal, true)
	later(s, time.Hour)
	cur, _, err := s.SaveWith(memory.SaveInput{Kind: memory.KindDecision, Title: "상단바 상태는 알약으로 하지 않는다", Body: "제목 막대 에이전트 현황은 레일에 둔다",
		Topic: "상단바 상태 표시", Supersedes: []string{old1.ID, old2.ID}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "알약은 안 해"}})
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{"상단바 시안", "제목 막대 에이전트 현황", "알약 아일랜드", "상단 상태 표시", "상단바 알약 다시", "에이전트 현황 시안 v3"}
	scopes := []string{"project:github.com/onidot-labs/orbit-studio", "project:/Users/me/_workspace/onidot",
		"project:/Users/me/_workspace/onidot/prototypes/orbit-desktop-v4-2026-11-03", "project:github.com/me/unrelated"}
	seen := 0
	for _, sc := range scopes {
		start := SessionStartInjection(s, config.Default(), sc)
		for _, q := range queries {
			p := PromptInjectionFor(s, config.Default(), sc, q, "claude", "", Options{})
			for _, id := range append(p.MemoryIDs, start.MemoryIDs...) {
				if id == old1.ID || id == old2.ID {
					seen++
				}
			}
		}
	}
	found, _ := s.Find(memory.FindInput{Query: "상단바 알약", AllProjects: true})
	for _, m := range found {
		if m.ID == old1.ID || m.ID == old2.ID {
			seen++
		}
	}
	if seen != 0 {
		t.Fatalf("old decisions came back %d times", seen)
	}
	if p := PromptInjectionFor(s, config.Default(), scopes[0], "상단바 상태는 알약으로 하지 않는다", "claude", "", Options{}); !slices.Contains(p.MemoryIDs, cur.ID) {
		t.Fatalf("the new decision is not recalled:\n%s", p.Text)
	}
}

func TestNews_다시_저장하거나_고친_옛_확정_기억은_새_기억이_아니다(t *testing.T) {
	s := store(t)
	old := save(t, s, memory.KindLesson, "codex exec는 stdin을 닫는다", "본문", memory.ScopeGlobal, true)
	SessionBundle(s, config.Default(), here, "claude", "A")
	later(s, time.Minute)
	save(t, s, memory.KindLesson, "codex exec는 stdin을 닫는다", "본문", memory.ScopeGlobal, true) // 같은 제목 다시 저장
	tags := []string{"codex"}
	s.Update(memory.UpdateInput{ID: old.ID, Tags: &tags})
	if p := PromptInjectionFor(s, config.Default(), here, "아무 요청", "claude", "A", Options{}); strings.Contains(p.Text, "새 기억") {
		t.Fatalf("an old confirmed memory came as new:\n%s", p.Text)
	}
}

func TestNews_시작_문맥이_빈_세션도_새_기억을_받는다(t *testing.T) {
	s := store(t)
	if b := SessionBundle(s, config.Default(), here, "claude", "A"); b.Text != "" {
		t.Fatalf("bundle %q", b.Text)
	}
	later(s, time.Minute)
	r := save(t, s, memory.KindRule, "보고는 한국어로", "", memory.ScopeGlobal, true)
	if p := PromptInjectionFor(s, config.Default(), here, "아무 요청", "claude", "A", Options{}); !strings.Contains(p.Text, r.ID) {
		t.Fatalf("no new memory line:\n%s", p.Text)
	}
}

func TestNews_연속_대체는_살아_있는_끝_판으로_정정한다(t *testing.T) {
	s := store(t)
	quote := func(title string) memory.SaveInput {
		return memory.SaveInput{Kind: memory.KindRule, Title: title, Topic: "보고 언어", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}}
	}
	a, _ := s.Save(quote("보고는 영어로"))
	if _, err := s.Pin(a.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	SessionBundle(s, config.Default(), here, "claude", "A")
	later(s, time.Minute)
	b, _ := s.Save(quote("보고는 일본어로"))
	later(s, time.Minute)
	c, _ := s.Save(quote("보고는 한국어로"))
	p := PromptInjectionFor(s, config.Default(), here, "아무 요청", "claude", "A", Options{})
	if !strings.Contains(p.Text, a.ID) || !strings.Contains(p.Text, c.ID) || strings.Contains(p.Text, b.ID) {
		t.Fatalf("correction must point at the live value:\n%s", p.Text)
	}
}

func TestSessionBundle_한도를_넘는_옛_묶음은_다시_만든다(t *testing.T) {
	s := store(t)
	r := save(t, s, memory.KindRule, "답은 존댓말", "", memory.ScopeGlobal, true)
	if _, err := s.Pin(r.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSessionBundle("claude", "old", strings.Repeat("가", 12540), []string{"m-x"}); err != nil {
		t.Fatal(err)
	}
	if b := SessionBundle(s, config.Default(), here, "claude", "old"); b.Chars > MaxSessionStartRunes || !strings.Contains(b.Text, "답은 존댓말") {
		t.Fatalf("old oversized bundle replayed: %d", b.Chars)
	}
}
