package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/version"
)

// 세션 시작 안내 줄(판·새 판·겹치는 호스트 기억)은 묶음을 만들 때 한 번 들어가 묶음과 함께 저장된다(설계 보완 T-20).

func startHook(t *testing.T, session, source string, extra ...string) string {
	t.Helper()
	in := `{"cwd":"/tmp","session_id":"` + session + `","hook_event_name":"SessionStart","source":"` + source + `"}`
	out, _, code := run(t, in, append([]string{"hook", "session-start", "--host", "claude"}, extra...)...)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	return out
}

func versionLine(plugin string) string {
	return fmt.Sprintf("orbit-mem 판(%s)과 플러그인 판(%s)이 다릅니다: `orbit-mem install`로 맞추세요.", version.Version, plugin)
}

// (1) 판이 다른 세션 시작: 안내 줄이 DB의 세션 묶음 글에 들어간다. (2) 같은 세션 resume은 다른 판·같은 판으로 불러도
// 첫 출력과 바이트 같다. (3) compact·clear는 reset 뒤 새로 만들어 그때 안내 줄을 다시 계산한다.
func TestHook_안내_줄은_묶음과_함께_저장되고_resume은_바이트_같다(t *testing.T) {
	home(t)
	s := seed(t)
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	first := startHook(t, "n1", "startup", "--plugin-version", "0.3.9")
	ctx := eventContext(t, first, "SessionStart")
	if !strings.Contains(ctx, "답은 존댓말") || !strings.HasSuffix(ctx, "\n"+versionLine("0.3.9")) {
		t.Fatalf("first %q", ctx)
	}
	if text, _, ok := s.SessionBundle("claude", "n1"); !ok || text != ctx {
		t.Fatalf("saved bundle %v %q", ok, text)
	}
	for _, args := range [][]string{{"--plugin-version", "0.4.0"}, {"--plugin-version", "0.3.9"}, {"--plugin-version", version.Version}, nil} {
		for _, src := range []string{"resume", "startup"} {
			if again := startHook(t, "n1", src, args...); again != first {
				t.Fatalf("%s %v changed the bundle:\n%s", src, args, again)
			}
		}
	}
	renewed := eventContext(t, startHook(t, "n1", "compact", "--plugin-version", "0.4.0"), "SessionStart")
	if !strings.HasSuffix(renewed, "\n"+versionLine("0.4.0")) || strings.Contains(renewed, "0.3.9") {
		t.Fatalf("compact %q", renewed)
	}
	cleared := eventContext(t, startHook(t, "n1", "clear"), "SessionStart")
	if strings.Contains(cleared, "orbit-mem install") || !strings.Contains(cleared, "답은 존댓말") {
		t.Fatalf("clear %q", cleared)
	}
	if text, _, _ := s.SessionBundle("claude", "n1"); text != cleared {
		t.Fatalf("saved after clear %q", text)
	}
}

// (4) 핵심이 한도까지 찬 자료에 세 안내 줄(판 → 새 판 → 겹치는 호스트 기억, 이 차례)이 붙어도 세션 시작 문맥은
// 6,000자 이하이고 resume도 같다.
func TestHook_세_안내_줄은_차례대로_묶음에_들고_6000자_이하다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"enabledPlugins": {"claude-mem@thedotmack": true}}`)
	writeCache(t, os.Getenv("ORBIT_HOME"), time.Now(), nextVersion())
	fakeUpdateBG(t)
	s := seed(t)
	for i := 0; i < 200; i++ {
		m, err := s.Save(memory.SaveInput{Kind: "rule", Title: fmt.Sprintf("규칙 %d", i), Topic: fmt.Sprintf("t%d", i), Body: strings.Repeat("본문 ", 200), Source: memory.Source{Kind: "quote", Excerpt: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = s.Pin(m.ID, memory.Who{}) // 한도를 넘으면 거절된다: 핵심이 한도까지 찬다
	}
	out := startHook(t, "f1", "startup", "--plugin-version", "0.0.1")
	ctx := eventContext(t, out, "SessionStart")
	v, u, m := strings.Index(ctx, versionLine("0.0.1")), strings.Index(ctx, "`orbit-mem update`"), strings.Index(ctx, "host-memory off")
	if n := utf8.RuneCountInString(ctx); n > 6000 || !strings.Contains(ctx, "규칙 0") || v < 0 || u < v || m < u {
		t.Fatalf("session start %d chars (version %d update %d host memory %d):\n%s", n, v, u, m, ctx)
	}
	if again := startHook(t, "f1", "resume", "--plugin-version", "0.0.2"); again != out {
		t.Fatalf("resume changed:\n%s", again)
	}
}

// (5) 새 판 확인 띄우기는 묶음을 다시 쓰는 resume에서도 한다(줄만 묶음을 만들 때 들어간다).
func TestHook_새_판_확인은_resume에서도_띄우고_줄은_묶음의_것이다(t *testing.T) {
	h := home(t)
	seedUpdateRule(t)
	calls := fakeUpdateBG(t)
	writeCache(t, h, time.Now().Add(-25*time.Hour), nextVersion())
	first := startHook(t, "r1", "startup")
	if refreshCalls(*calls) != 1 || !strings.Contains(first, "`orbit-mem update`") {
		t.Fatalf("startup calls=%v %q", *calls, first)
	}
	writeCache(t, h, time.Now().Add(-25*time.Hour), version.Version) // 캐시가 바뀌어도 같은 세션의 묶음은 그대로다
	if again := startHook(t, "r1", "resume"); again != first || refreshCalls(*calls) != 2 {
		t.Fatalf("resume calls=%v\n%s", *calls, again)
	}
	if renewed := startHook(t, "r1", "compact"); strings.Contains(renewed, "`orbit-mem update`") || refreshCalls(*calls) != 3 {
		t.Fatalf("compact calls=%v\n%s", *calls, renewed)
	}
}

// (5) recall=off여도 판 안내 줄은 시작 문맥으로 나간다(예전 inject=off와 같다). 기억은 넣지 않는다.
func TestHook_recall_off여도_판_안내_줄은_나가고_기억은_없다(t *testing.T) {
	home(t)
	s := seed(t)
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	if _, errOut, code := run(t, "", "config", "set", "recall", "off"); code != 0 {
		t.Fatalf("config set: %s", errOut)
	}
	ctx := eventContext(t, startHook(t, "off-1", "startup", "--plugin-version", "0.3.9"), "SessionStart")
	if ctx != versionLine("0.3.9") {
		t.Fatalf("recall off start %q", ctx)
	}
}
