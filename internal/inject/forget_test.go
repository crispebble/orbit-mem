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
