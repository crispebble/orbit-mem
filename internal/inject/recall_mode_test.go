package inject

// 설계 · orbit-mem 지연 회상 7판 4절 A층·T-3·T-20·T-27, ORBIT-26 G3: 세션 시작 묶음은 상시 핵심과 개수 없는 찾기 안내
// 한 줄이고 「최근 결정」 줄은 없다. 판·업데이트·호스트 기억 안내 줄은 묶음을 새로 만들 때 한 번 넣어 함께 저장한다. 주입은
// 새 설정 recall(auto·on_demand·off)로 돌고 옛 키 inject는 같은 뜻으로 읽는다.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const wantFindLine = "[" + Label + " 기억 · 먼저 찾는 곳] 규칙·결정이 일에 닿으면 memory_find로 먼저 찾는다."

func recallCfg(recall string) config.Config {
	cfg := config.Default()
	cfg.Recall = recall
	return cfg
}

func TestA층_auto_묶음에_최근_결정_줄이_없고_찾는_곳_줄은_개수_없는_고정_글이다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	dec := save(t, s, memory.KindDecision, "Orbit 먼저 만든다", "본문", memory.ScopeGlobal, true)
	cfg := config.Default()
	if cfg.RecentDecisions == 0 {
		t.Fatal("the default must still ask for recent decisions: the bundle ignores the key")
	}
	first := SessionStartInjection(s, cfg, here)
	for _, bad := range []string{"[최근 결정]", "최근 결정", dec.ID, dec.Title, "1개", "2개"} {
		if strings.Contains(first.Text, bad) {
			t.Fatalf("bundle carries %q:\n%s", bad, first.Text)
		}
	}
	if !strings.Contains(first.Text, wantFindLine+"\n") {
		t.Fatalf("find line missing:\n%s", first.Text)
	}
	// 기억 수가 바뀌어도 같은 글이다(세션 안에서 바이트가 바뀌지 않는다).
	for i := 0; i < 4; i++ {
		save(t, s, memory.KindRule, fmt.Sprintf("규칙 %d", i), "", memory.ScopeGlobal, true)
		save(t, s, memory.KindDecision, fmt.Sprintf("결정 %d", i), "", here, true)
	}
	if again := SessionStartInjection(s, cfg, here); again.Text != first.Text || len(again.MemoryIDs) != 0 {
		t.Fatalf("bundle changed with the counts:\n%s\n---\n%s\nids=%v", first.Text, again.Text, again.MemoryIDs)
	}
}

func TestA층_핵심만_있어도_찾는_곳_줄이_붙는다(t *testing.T) {
	s := store(t)
	m := save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	got := SessionStart(s, config.Default(), here)
	if !strings.Contains(got, "반말을 쓰지 않는다") || !strings.Contains(got, wantFindLine) {
		t.Fatalf("core-only bundle:\n%s", got)
	}
}

func TestRecall_on_demand는_세션_시작을_규칙으로_채우고_요청_회상은_없고_도구_직전은_있다(t *testing.T) {
	for name, cfg := range map[string]config.Config{"recall=on_demand": recallCfg(config.RecallOnDemand), "inject=rules": rulesCfg()} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			rule := save(t, s, memory.KindRule, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "본문 전문이 여기 있다", memory.ScopeGlobal, true)
			saveTool(t, s, "문서는 개인 공간에 발행한다", "발행 위치 본문", memory.ScopeGlobal, []string{docsTool}, true)
			start := SessionStartInjection(s, cfg, here)
			if !strings.Contains(start.Text, "본문 전문이 여기 있다") || !strings.Contains(start.Text, rule.ID) {
				t.Fatalf("on_demand fill lost:\n%s", start.Text)
			}
			if p := PromptInjectionFor(s, cfg, here, "codex exec를 백그라운드에서 돌렸더니 멈춰요", "claude", "s1", Options{}); p.Chars != 0 || p.Text != "" {
				t.Fatalf("on_demand request recall: %q", p.Text)
			}
			if tool := ToolInjection(s, cfg, here, "claude", "s1", docsTool); tool.Chars == 0 || !strings.Contains(tool.Text, "발행 위치 본문") {
				t.Fatalf("on_demand tool recall: %q", tool.Text)
			}
		})
	}
}

func TestRecall_off는_어디에도_넣지_않는다(t *testing.T) {
	off := config.Default()
	off.Inject = config.InjectOff
	for name, cfg := range map[string]config.Config{"recall=off": recallCfg(config.RecallOff), "inject=off": off} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			m := save(t, s, memory.KindRule, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "본문", memory.ScopeGlobal, true)
			if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
				t.Fatal(err)
			}
			saveTool(t, s, "문서는 개인 공간에 발행한다", "발행 위치 본문", memory.ScopeGlobal, []string{docsTool}, true)
			called := 0
			notes := func() []string { called++; return []string{"판 안내"} }
			for what, n := range map[string]int{
				"session start": SessionBundleWith(s, cfg, here, "claude", "s1", notes).Chars,
				"prompt":        PromptInjectionFor(s, cfg, here, "codex exec를 백그라운드에서 돌렸더니 멈춰요", "claude", "s1", Options{}).Chars,
				"tool":          ToolInjection(s, cfg, here, "claude", "s1", docsTool).Chars,
				"subagent":      SubagentStart(s, cfg).Chars,
			} {
				if n != 0 {
					t.Errorf("%s: %d chars", what, n)
				}
			}
			if called != 0 {
				t.Errorf("notes asked %d times while recall is off", called)
			}
		})
	}
}

func TestBundle_안내_줄은_새_묶음을_만들_때만_부르고_묶음과_함께_저장한다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindRule, "답은 한글 존댓말", "반말을 쓰지 않는다", memory.ScopeGlobal, true)
	cfg := config.Default()
	called := 0
	lines := []string{"판 안내 한 줄", "업데이트 안내 한 줄"}
	notes := func() []string { called++; return lines }

	a := SessionBundleWith(s, cfg, here, "claude", "s1", notes)
	if called != 1 || !strings.HasSuffix(a.Text, "\n판 안내 한 줄\n업데이트 안내 한 줄") || !strings.Contains(a.Text, wantFindLine) {
		t.Fatalf("called=%d bundle:\n%s", called, a.Text)
	}
	if saved, _, ok := s.SessionBundle("claude", "s1"); !ok || saved != a.Text {
		t.Fatalf("saved bundle differs:\n%s\n---\n%s", saved, a.Text)
	}
	// resume: 저장된 묶음을 그대로 주고 안내 줄 함수는 부르지 않는다. 안내 줄이 바뀌어도 바이트가 같다.
	lines = []string{"바뀐 안내"}
	b := SessionBundleWith(s, cfg, here, "claude", "s1", notes)
	if called != 1 || b.Text != a.Text || b.Chars != a.Chars {
		t.Fatalf("resume called=%d:\n%s", called, b.Text)
	}
	// 압축·clear: 묶음을 새로 만들며 다시 부른다.
	ResetSession(s, "claude", "s1")
	c := SessionBundleWith(s, cfg, here, "claude", "s1", notes)
	if called != 2 || !strings.HasSuffix(c.Text, "\n바뀐 안내") || strings.Contains(c.Text, "판 안내 한 줄") {
		t.Fatalf("after reset called=%d:\n%s", called, c.Text)
	}
	// nil 함수도 된다.
	if d := SessionBundleWith(s, cfg, here, "claude", "s2", nil); d.Text != SessionStart(s, cfg, here) {
		t.Fatalf("nil notes:\n%s", d.Text)
	}
}

func TestBundle_기억이_없어도_안내_줄은_묶음이_되어_저장된다(t *testing.T) {
	s := store(t)
	in := SessionBundleWith(s, config.Default(), here, "codex", "s1", func() []string { return []string{"", "판 안내"} })
	if in.Text != "판 안내" {
		t.Fatalf("got %q", in.Text)
	}
	if saved, _, ok := s.SessionBundle("codex", "s1"); !ok || saved != "판 안내" {
		t.Fatalf("saved %q ok=%v", saved, ok)
	}
}

// D12: 고정 예약 몫 없이 6,000자까지 쓴 저장 묶음은 그대로 다시 준다.
func TestBundle_6000자_안의_저장_묶음은_다시_만들지_않는다(t *testing.T) {
	s := store(t)
	text := strings.Repeat("가", MaxSessionStartRunes-100)
	if err := s.SaveSessionBundle("claude", "s1", text, []string{}); err != nil {
		t.Fatal(err)
	}
	called := 0
	if got := SessionBundleWith(s, config.Default(), here, "claude", "s1", func() []string { called++; return nil }); got.Text != text || called != 0 {
		t.Fatalf("stored bundle rebuilt: %d chars, called=%d", got.Chars, called)
	}
}

func TestBundle_핵심이_한도까지_차도_묶음과_안내_줄은_6000자_안이다(t *testing.T) {
	s := store(t)
	f := s3Folders(t)
	fill := func(scope, word string) {
		for i := 0; i < 30; i++ {
			m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: fmt.Sprintf("%s 핵심 %d", word, i), Topic: fmt.Sprintf("%s %d", word, i), Scope: scope,
				Body: strings.Repeat(word, 470), Source: memory.Source{Kind: memory.SourceQuote, Excerpt: word}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
				return
			}
		}
		t.Fatal("never full")
	}
	fill(memory.ScopeGlobal, "가")
	fill("product:orbit-code-desktop", "나")
	for i := 0; i < 15; i++ {
		saveIn(t, s, memory.KindDecision, fmt.Sprintf("긴 결정 제목 %d %s", i, strings.Repeat("다", 60)), memory.ScopeGlobal, "", fmt.Sprintf("결정 %d", i))
		saveIn(t, s, memory.KindRule, fmt.Sprintf("규칙 %d %s", i, strings.Repeat("라", 150)), memory.ScopeGlobal, "", fmt.Sprintf("규칙 %d", i))
	}
	line := func(n int) string { return strings.Repeat("마", n) }
	for _, recall := range []string{config.RecallAuto, config.RecallOnDemand} {
		for _, notes := range [][]string{{line(150), line(200), line(250)}, {line(1500), line(1500)}} {
			cfg := s3Config()
			cfg.Recall = recall
			session := fmt.Sprintf("%s-%d", recall, len(notes[0]))
			in := SessionBundleWith(s, cfg, f["시안 폴더"], "claude", session, func() []string { return notes })
			if in.Chars > MaxSessionStartRunes {
				t.Fatalf("%s: bundle with notes %d > %d", session, in.Chars, MaxSessionStartRunes)
			}
			if saved, _, _ := s.SessionBundle("claude", session); saved != in.Text {
				t.Fatalf("%s: saved bundle differs", session)
			}
			for _, scope := range []string{memory.ScopeGlobal, "product:orbit-code-desktop"} {
				for _, m := range mustCore(t, s, scope) {
					if !strings.Contains(in.Text, m.ID) {
						t.Fatalf("%s: core %s left out", session, m.ID)
					}
				}
			}
			if len(notes[0]) < 1000 && !strings.HasSuffix(in.Text, notes[2]) { // 짧은 안내 줄은 다 들어간다
				t.Fatalf("%s: short notes left out:\n%s", session, in.Text[max(0, len(in.Text)-800):])
			}
		}
	}
}

// T-27: 옛 키 inject만 있는 설정은 새 키 recall의 같은 뜻 값과 같은 글을 낸다.
func TestRecall_옛_inject_키만_있는_설정은_같은_뜻으로_돈다(t *testing.T) {
	pairs := map[string][2]config.Config{}
	for old, recall := range map[string]string{config.InjectFull: config.RecallAuto, config.InjectRules: config.RecallOnDemand, config.InjectOff: config.RecallOff} {
		legacy := config.Default()
		legacy.Inject, legacy.Recall = old, ""
		pairs[old] = [2]config.Config{legacy, recallCfg(recall)}
	}
	for old, p := range pairs {
		t.Run(old, func(t *testing.T) {
			s := store(t)
			save(t, s, memory.KindRule, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "본문 전문", memory.ScopeGlobal, true)
			save(t, s, memory.KindLesson, "codex exec 멈춤 교훈", "stdin을 닫는다", memory.ScopeGlobal, false)
			saveTool(t, s, "문서는 개인 공간에 발행한다", "발행 위치 본문", memory.ScopeGlobal, []string{docsTool}, true)
			var got [2][3]string
			for i, cfg := range p {
				got[i] = [3]string{
					SessionStart(s, cfg, here),
					PromptInjectionFor(s, cfg, here, "codex exec를 백그라운드에서 돌렸더니 멈춰요", "claude", "", Options{}).Text,
					ToolInjection(s, cfg, here, "claude", fmt.Sprintf("t%d", i), docsTool).Text,
				}
			}
			if got[0] != got[1] {
				t.Fatalf("inject=%s differs from its recall value:\n%q\n---\n%q", old, got[0], got[1])
			}
		})
	}
}
