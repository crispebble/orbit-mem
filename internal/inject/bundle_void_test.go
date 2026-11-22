package inject

// 명세 F-2의 무효 줄: 이 세션에 넣은 기억이 지워지거나, 세션 중에 낡거나 만료되면 다음 요청에 한 번 알린다. 세션 시작
// 전에 이미 바뀐 기억과, 후계를 읽지 못한 기억에는 내지 않는다.

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// forget forgets m as a person who asked for it.
func forget(t *testing.T, s *memory.Store, m memory.Memory) {
	t.Helper()
	if _, err := s.Forget(memory.ForgetInput{IDs: []string{m.ID}, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "그 기억 지워"}}); err != nil {
		t.Fatal(err)
	}
}

// voidOnce checks that the next request of session s1 tells it once, with a void line of why, that m no longer
// holds, and that the request after it (and after a resume) does not tell it again.
func voidOnce(t *testing.T, s *memory.Store, m memory.Memory, why string) {
	t.Helper()
	cfg := config.Default()
	p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
	void := voidOf(p.Text)
	if len(void) != 1 || !strings.Contains(void[0], m.ID) || !strings.Contains(void[0], why) || slices.Contains(p.MemoryIDs, m.ID) {
		t.Fatalf("무효(%s) 줄 %q:\n%s", why, void, p.Text)
	}
	if why == voidGone && strings.Contains(p.Text, m.Title) {
		t.Fatalf("지운 기억의 제목이 다시 나왔습니다:\n%s", p.Text)
	}
	RecordShown(s, "claude", "s1", p)
	for _, resume := range []bool{false, true} {
		if resume {
			SessionBundle(s, cfg, here, "claude", "s1")
		}
		if q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); strings.Contains(q.Text, m.ID) {
			t.Fatalf("재개 %v: 같은 기억을 다시 알렸습니다:\n%s", resume, q.Text)
		}
	}
}

const recallPrompt = "codex exec를 백그라운드에서 돌렸더니 멈춰요"

// recalled starts session s1 and puts m in it by the work recall of recallPrompt alone (m is not in the bundle).
func recalled(t *testing.T, s *memory.Store, m memory.Memory) {
	t.Helper()
	cfg := config.Default()
	pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
	if b := SessionBundle(s, cfg, here, "claude", "s1"); slices.Contains(b.MemoryIDs, m.ID) {
		t.Fatalf("준비가 어긋났습니다: 묶음에 든 기억입니다\n%s", b.Text)
	}
	p := PromptInjectionFor(s, cfg, here, recallPrompt, "claude", "s1", Options{})
	if !slices.Contains(p.MemoryIDs, m.ID) {
		t.Fatalf("준비가 어긋났습니다: 요청 회상이 없습니다\n%s", p.Text)
	}
	RecordShown(s, "claude", "s1", p)
}

func TestPromptNews_요청_회상으로만_넣은_기억을_지우면_지움_무효_줄이_한_번_나온다(t *testing.T) {
	s := store(t)
	m := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "", memory.ScopeGlobal, false)
	recalled(t, s, m)
	later(s, time.Hour)
	forget(t, s, m)
	voidOnce(t, s, m, voidGone)
}

func TestPromptNews_도구_직전으로만_넣은_기억을_지우면_지움_무효_줄이_한_번_나온다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "git push 전에 브랜치를 확인한다", Tools: []string{"Bash"},
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "git push 전에 브랜치를 확인한다"}})
	if err != nil {
		t.Fatal(err)
	}
	pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
	if b := SessionBundle(s, cfg, here, "claude", "s1"); slices.Contains(b.MemoryIDs, m.ID) {
		t.Fatalf("준비가 어긋났습니다: 묶음에 든 기억입니다\n%s", b.Text)
	}
	tool := ToolInjection(s, cfg, here, "claude", "s1", "Bash")
	if !slices.Contains(tool.MemoryIDs, m.ID) {
		t.Fatalf("준비가 어긋났습니다: 도구 직전 회상이 없습니다\n%s", tool.Text)
	}
	RecordShown(s, "claude", "s1", tool)
	later(s, time.Hour)
	forget(t, s, m)
	voidOnce(t, s, m, voidGone)
}

func TestPromptNews_낡음_무효_줄을_낸_뒤_지워도_다시_나오지_않는다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
	m := pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다"})
	SessionBundle(s, cfg, here, "claude", "s1")
	later(s, time.Hour)
	if _, err := s.SetState(m.ID, memory.StateStale, "폐기"); err != nil {
		t.Fatal(err)
	}
	p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
	if void := voidOf(p.Text); len(void) != 1 || !strings.Contains(void[0], voidStale) {
		t.Fatalf("준비가 어긋났습니다: 낡음 무효 줄 %q", void)
	}
	RecordShown(s, "claude", "s1", p)
	forget(t, s, m)
	for _, resume := range []bool{false, true} {
		if resume {
			SessionBundle(s, cfg, here, "claude", "s1")
		}
		if q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); strings.Contains(q.Text, m.ID) {
			t.Fatalf("재개 %v: 지운 뒤 무효 줄이 한 번 더 나왔습니다:\n%s", resume, q.Text)
		}
	}
}

// 세션 시작 전에 이미 낡았거나 만료된 기억은 이 세션에 현재 값으로 들어간 적이 없다. 그 id의 표시가 있어도(앞선 기록 등)
// 이 세션 중에 바뀐 것이 아니므로 무효 줄을 내지 않는다.
func TestPromptNews_세션_시작_전에_낡거나_만료된_기억에는_무효_줄이_없다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	stale := save(t, s, memory.KindLesson, "옛 교훈은 이렇다", "", memory.ScopeGlobal, false)
	if _, err := s.SetState(stale.ID, memory.StateStale, "폐기"); err != nil {
		t.Fatal(err)
	}
	expired, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "이번 주 배포 창은 화요일", Retention: memory.RetentionVolatile, TTLDays: 1,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "이번 주 배포 창은 화요일"}})
	if err != nil {
		t.Fatal(err)
	}
	later(s, 48*time.Hour)
	pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
	SessionBundle(s, cfg, here, "claude", "s1")
	if err := s.MarkSession("claude", "s1", memory.MarkShown, stale.ID, expired.ID); err != nil {
		t.Fatal(err)
	}
	later(s, time.Hour)
	if p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); len(voidOf(p.Text)) != 0 {
		t.Fatalf("세션 전에 바뀐 기억에 무효 줄이 나왔습니다:\n%s", p.Text)
	}
}

func TestPromptNews_요청_회상으로_넣은_기억이_세션_중_낡거나_만료되면_무효_줄을_한_번_낸다(t *testing.T) {
	t.Run("낡음", func(t *testing.T) {
		s := store(t)
		m := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "", memory.ScopeGlobal, false)
		recalled(t, s, m)
		later(s, time.Hour)
		if _, err := s.SetState(m.ID, memory.StateStale, "폐기"); err != nil {
			t.Fatal(err)
		}
		voidOnce(t, s, m, voidStale)
	})
	t.Run("만료", func(t *testing.T) {
		s := store(t)
		m, err := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", Scope: memory.ScopeGlobal,
			Retention: memory.RetentionVolatile, TTLDays: 1, Source: memory.Source{Kind: memory.SourceSession, Ref: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		recalled(t, s, m)
		later(s, 48*time.Hour)
		voidOnce(t, s, m, voidExpired)
	})
}

// 후계를 읽다 오류가 나면 「후계 없음」이 아니다: 무효 줄도 표시도 남기지 않아, 읽을 수 있게 된 다음 요청에 정정 줄이
// 나온다.
func TestPromptNews_후계를_읽지_못하면_건너뛰고_다음_요청에_다시_본다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	old := pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다", Topic: "보고 언어"})
	SessionBundle(s, cfg, here, "claude", "s1")
	later(s, time.Hour)
	cur, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "보고는 한국어로 쓴다", Topic: "보고 언어",
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "보고는 한국어로 쓴다"}})
	if err != nil {
		t.Fatal(err)
	}
	// 후계의 출처 줄 시각을 읽을 수 없게 해 그 기억의 Get만 실패하게 한다(목록 읽기는 출처 시각을 읽지 않는다).
	var at int64
	if err := s.DB().QueryRow(`SELECT at FROM sources WHERE memory_id=?`, cur.ID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE sources SET at='읽을 수 없음' WHERE memory_id=?`, cur.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(cur.ID); err == nil || errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("준비가 어긋났습니다: 후계 읽기 오류 %v", err)
	}
	p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
	if strings.Contains(p.Text, old.ID) {
		t.Fatalf("후계를 못 읽었는데 줄을 냈습니다:\n%s", p.Text)
	}
	RecordShown(s, "claude", "s1", p)
	if _, err := s.DB().Exec(`UPDATE sources SET at=? WHERE memory_id=?`, at, cur.ID); err != nil {
		t.Fatal(err)
	}
	q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
	if !strings.Contains(q.Text, "정정") || !strings.Contains(q.Text, old.ID) || !strings.Contains(q.Text, cur.ID) || len(voidOf(q.Text)) != 0 {
		t.Fatalf("읽을 수 있게 된 뒤 정정 줄이 없습니다:\n%s", q.Text)
	}
}
