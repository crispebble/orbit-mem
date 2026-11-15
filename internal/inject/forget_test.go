package inject

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestForget_다음_세션_시작_묶음에서_지운_핵심_기억이_빠진다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	gone := save(t, s, memory.KindRule, "orbit-cc 일을 먼저 챙긴다", "폐기한 일", memory.ScopeGlobal, true)
	keep := save(t, s, memory.KindRule, "답은 한글 존댓말로", "", memory.ScopeGlobal, true)
	for _, id := range []string{gone.ID, keep.ID} {
		if _, err := s.Pin(id, memory.Who{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := SessionStart(s, cfg, here); !strings.Contains(got, gone.Title) {
		t.Fatalf("the pinned memory is not in the session start:\n%s", got)
	}
	if _, err := s.Forget(memory.ForgetInput{IDs: []string{gone.ID}, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "그 기억 지워"}}); err != nil {
		t.Fatal(err)
	}
	got := SessionStart(s, cfg, here)
	if strings.Contains(got, gone.Title) || strings.Contains(got, gone.ID) || !strings.Contains(got, keep.Title) {
		t.Fatalf("session start after forget:\n%s", got)
	}
}

func TestForget_대체한_새_판을_지워도_옛_판이_세션_시작과_프롬프트_회상에_돌아오지_않는다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	// 세션 시작에는 상시 핵심인 옛 판이, 프롬프트 회상에는 핵심이 아닌 옛 판이 나온다.
	core := save(t, s, memory.KindRule, "릴리스는 월요일에 한다", "월요일 릴리스 규칙", memory.ScopeGlobal, true)
	if _, err := s.Pin(core.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	lesson := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "옛 판 본문", memory.ScopeGlobal, false)
	const prompt = "codex exec를 백그라운드에서 돌렸더니 멈춰요"
	if !strings.Contains(SessionStart(s, cfg, here), core.Title) || !strings.Contains(Prompt(s, cfg, here, prompt), lesson.ID) {
		t.Fatal("준비가 어긋났습니다: 옛 판이 회상에 나와야 합니다")
	}
	for _, old := range []memory.Memory{core, lesson} {
		newer := save(t, s, memory.KindRule, "새 판 "+old.ID, "새 본문", memory.ScopeGlobal, true)
		if _, err := s.Supersede(old.ID, newer.ID, "새 판으로 바뀜"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Forget(memory.ForgetInput{IDs: []string{newer.ID}, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "새 판 지워"}}); err != nil {
			t.Fatal(err)
		}
	}
	for name, got := range map[string]string{"세션 시작": SessionStart(s, cfg, here), "프롬프트 회상": Prompt(s, cfg, here, prompt)} {
		for _, bad := range []string{core.Title, core.ID, lesson.ID, lesson.Title, "새 판 m-"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s에 %q가 나옵니다:\n%s", name, bad, got)
			}
		}
	}
}
