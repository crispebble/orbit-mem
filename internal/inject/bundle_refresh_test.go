package inject

// 명세 F-1·F-2: 같은 세션의 다시 시작(재개)은 저장된 묶음을 주되, 묶음 안 기억이 그 사이 현재 값이 아니게 됐으면 묶음을
// 다시 만든다. 이 세션에 넣은 기억이 바뀌면 정정·무효 줄을 세션에 한 번 낸다.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// pinned saves a user's rule in the global core: the session bundle (recall auto) holds the core.
func pinned(t *testing.T, s *memory.Store, in memory.SaveInput) memory.Memory {
	t.Helper()
	if in.Kind == "" {
		in.Kind = memory.KindRule
	}
	in.Source = memory.Source{Kind: memory.SourceQuote, Excerpt: in.Title}
	m, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	if m, err = s.Pin(m.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	return m
}

// change is one way a memory of a bundle stops being a current value after the bundle was made.
type change struct {
	name string
	// make saves the memory that will change (pinned, so it is in the bundle).
	make func(t *testing.T, s *memory.Store) memory.Memory
	// apply changes it; it returns the memory that stands in for it now, or "".
	apply func(t *testing.T, s *memory.Store, m memory.Memory) string
}

var changes = []change{
	{"폐기",
		func(t *testing.T, s *memory.Store) memory.Memory {
			return pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다"})
		},
		func(t *testing.T, s *memory.Store, m memory.Memory) string {
			if _, err := s.SetState(m.ID, memory.StateStale, "폐기"); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
	// 사용자 말의 휘발 기억은 날이 지나도 상태가 그대로다(보임 판정만 만료로 본다).
	{"만료",
		func(t *testing.T, s *memory.Store) memory.Memory {
			return pinned(t, s, memory.SaveInput{Title: "이번 주는 배포를 멈춘다", Retention: memory.RetentionVolatile, TTLDays: 1})
		},
		func(t *testing.T, s *memory.Store, m memory.Memory) string {
			later(s, 48*time.Hour)
			if got, _ := s.Get(m.ID); got.State != memory.StateConfirmed || got.Freshness(s.Now()) != memory.FreshExpired {
				t.Fatalf("준비가 어긋났습니다: %+v", got)
			}
			return ""
		}},
	{"대체",
		func(t *testing.T, s *memory.Store) memory.Memory {
			return pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다", Topic: "보고 언어"})
		},
		func(t *testing.T, s *memory.Store, m memory.Memory) string {
			cur, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "보고는 한국어로 쓴다", Topic: "보고 언어",
				Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "보고는 한국어로 쓴다"}})
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Get(m.ID); got.State != memory.StateStale || got.SupersededBy != cur.ID {
				t.Fatalf("준비가 어긋났습니다: %+v", got)
			}
			return cur.ID
		}},
	{"삭제",
		func(t *testing.T, s *memory.Store) memory.Memory {
			return pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다"})
		},
		func(t *testing.T, s *memory.Store, m memory.Memory) string {
			if _, err := s.Forget(memory.ForgetInput{IDs: []string{m.ID}, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "그 기억 지워"}}); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
}

func TestSessionBundle_묶음_뒤_폐기된_기억은_다음_시작_주입에_없다(t *testing.T) {
	for _, c := range changes {
		t.Run(c.name, func(t *testing.T) {
			s := store(t)
			cfg := config.Default()
			keep := pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
			m := c.make(t, s)
			first := SessionBundle(s, cfg, here, "claude", "s1")
			if !slices.Contains(first.MemoryIDs, m.ID) || !strings.Contains(first.Text, m.Title) {
				t.Fatalf("준비가 어긋났습니다: 묶음에 기억이 없습니다\n%s", first.Text)
			}
			later(s, time.Hour)
			next := c.apply(t, s, m)
			again := SessionBundle(s, cfg, here, "claude", "s1")
			if slices.Contains(again.MemoryIDs, m.ID) || strings.Contains(again.Text, m.ID) || strings.Contains(again.Text, m.Title) {
				t.Fatalf("재개한 세션 시작에 현재 값이 아닌 기억이 남았습니다:\n%s", again.Text)
			}
			if !slices.Contains(again.MemoryIDs, keep.ID) || (next != "" && !slices.Contains(again.MemoryIDs, next)) {
				t.Fatalf("다시 만든 묶음 ids %v, keep %s next %q", again.MemoryIDs, keep.ID, next)
			}
			if text, ids, ok := s.SessionBundle("claude", "s1"); !ok || text != again.Text || !slices.Equal(ids, again.MemoryIDs) {
				t.Fatalf("저장된 묶음이 돌려준 글과 다릅니다:\n%s\n---\n%s", text, again.Text)
			}
		})
	}
}

func TestSessionBundle_바뀐_기억이_없으면_같은_글을_돌려준다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	in := pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다", Body: "반말을 쓰지 않는다"})
	pinned(t, s, memory.SaveInput{Title: "보고는 한국어로 쓴다"})
	out := save(t, s, memory.KindRule, "묶음 밖 규칙", "", memory.ScopeGlobal, false)
	called := 0
	notes := func() []string { called++; return []string{fmt.Sprintf("안내 %d", called)} }
	first := SessionBundleWith(s, cfg, here, "claude", "s1", notes)
	at := s.SessionBundleAt("claude", "s1")
	later(s, time.Hour)
	// 묶음 밖 기억이 낡고, 묶음 안 기억은 본문만 고쳐지고(상태는 그대로), 새 핵심이 생겨도 묶음 안 기억의 보임은 그대로다.
	if _, err := s.SetState(out.ID, memory.StateStale, "폐기"); err != nil {
		t.Fatal(err)
	}
	body := "반말을 쓰지 않는다. 존댓말로 끝까지"
	if _, err := s.Update(memory.UpdateInput{ID: in.ID, Body: &body, Reason: "본문 보강"}); err != nil {
		t.Fatal(err)
	}
	pinned(t, s, memory.SaveInput{Title: "세션 중에 승격한 규칙"})
	again := SessionBundleWith(s, cfg, here, "claude", "s1", notes)
	if again.Text != first.Text || !slices.Equal(again.MemoryIDs, first.MemoryIDs) || called != 1 {
		t.Fatalf("바뀐 기억이 없는데 묶음이 달라졌습니다(called=%d):\n%s\n---\n%s", called, first.Text, again.Text)
	}
	if text, _, _ := s.SessionBundle("claude", "s1"); text != first.Text || s.SessionBundleAt("claude", "s1") != at {
		t.Fatal("바뀐 기억이 없는데 저장된 묶음이 고쳐졌습니다")
	}
	if marks := s.SessionMarks("claude", "s1", memory.MarkBundle); len(marks) != 0 {
		t.Fatalf("뺀 기억이 없는데 표시가 남았습니다: %v", marks)
	}
}

func TestSessionBundle_다시_만들어도_세션_시작_시각과_표시를_지킨다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
	gone := pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다"})
	told := save(t, s, memory.KindLesson, "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", "", memory.ScopeGlobal, false)
	SessionBundle(s, cfg, here, "claude", "s1")
	at := s.SessionBundleAt("claude", "s1")
	p := PromptInjectionFor(s, cfg, here, "codex exec를 백그라운드에서 돌렸더니 멈춰요", "claude", "s1", Options{})
	if !slices.Contains(p.MemoryIDs, told.ID) {
		t.Fatalf("준비가 어긋났습니다: 요청 회상이 없습니다\n%s", p.Text)
	}
	RecordShown(s, "claude", "s1", p)
	later(s, time.Hour)
	if _, err := s.SetState(gone.ID, memory.StateStale, "폐기"); err != nil {
		t.Fatal(err)
	}
	fresh := save(t, s, memory.KindRule, "세션 중에 확정된 규칙", "", memory.ScopeGlobal, true)
	later(s, time.Hour)
	if again := SessionBundle(s, cfg, here, "claude", "s1"); slices.Contains(again.MemoryIDs, gone.ID) {
		t.Fatalf("준비가 어긋났습니다: 묶음을 다시 만들지 않았습니다\n%s", again.Text)
	}
	if got := s.SessionBundleAt("claude", "s1"); got != at {
		t.Fatalf("세션 시작 시각이 %d에서 %d로 바뀌었습니다", at, got)
	}
	if !s.SessionMarks("claude", "s1", memory.MarkShown)[told.ID] || !s.SessionMarks("claude", "s1", memory.MarkBundle)[gone.ID] {
		t.Fatal("다시 만들기가 세션 표시를 잃었거나 뺀 기억을 표시하지 않았습니다")
	}
	// 시작 시각이 그대로라 그 뒤 확정된 기억은 새 기억 줄로 알린다. 이미 알린 회상은 다시 내지 않는다.
	q := PromptInjectionFor(s, cfg, here, "codex exec를 백그라운드에서 돌렸더니 멈춰요", "claude", "s1", Options{})
	if !strings.Contains(q.Text, "새 기억") || !strings.Contains(q.Text, fresh.ID) || slices.Contains(q.MemoryIDs, told.ID) {
		t.Fatalf("다시 만든 뒤 요청:\n%s", q.Text)
	}
}

func TestSessionBundle_두번_다시_만들어도_여전히_폐기된_기억은_돌아오지_않는다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
	a := pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다"})
	b := pinned(t, s, memory.SaveInput{Title: "배포는 금요일에 하지 않는다"})
	SessionBundle(s, cfg, here, "claude", "s1")
	later(s, time.Hour)
	if _, err := s.SetState(a.ID, memory.StateStale, "폐기"); err != nil {
		t.Fatal(err)
	}
	once := SessionBundle(s, cfg, here, "claude", "s1")
	if slices.Contains(once.MemoryIDs, a.ID) || !slices.Contains(once.MemoryIDs, b.ID) {
		t.Fatalf("첫 다시 만들기 %v", once.MemoryIDs)
	}
	p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
	RecordShown(s, "claude", "s1", p)
	later(s, time.Hour)
	if _, err := s.SetState(b.ID, memory.StateStale, "폐기"); err != nil {
		t.Fatal(err)
	}
	twice := SessionBundle(s, cfg, here, "claude", "s1")
	if slices.Contains(twice.MemoryIDs, a.ID) || slices.Contains(twice.MemoryIDs, b.ID) || strings.Contains(twice.Text, a.Title) {
		t.Fatalf("둘째 다시 만들기에 낡은 기억이 돌아왔습니다:\n%s", twice.Text)
	}
	q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
	if strings.Contains(q.Text, a.ID) || !strings.Contains(q.Text, b.ID) {
		t.Fatalf("둘째 다시 만들기 뒤 요청은 b만 알려야 합니다:\n%s", q.Text)
	}
	// 되돌려 다시 현재 값이 된 기억은 다음 다시 만들기에 돌아와도 된다.
	RecordShown(s, "claude", "s1", q)
	later(s, time.Hour)
	if _, err := s.SetState(a.ID, memory.StateConfirmed, "되돌림"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(memory.ForgetInput{IDs: []string{b.ID}, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "그 기억 지워"}}); err != nil {
		t.Fatal(err)
	}
	if third := SessionBundle(s, cfg, here, "claude", "s1"); slices.Contains(third.MemoryIDs, b.ID) {
		t.Fatalf("셋째 다시 만들기 %v", third.MemoryIDs)
	}
}

// 두 훅(다른 연결)이 같은 세션을 같은 때 다시 시작한다. 둘 다 묶음을 다시 만들지만 저장은 한쪽만 되고, 둘이 돌려준 글과
// 저장된 글이 같으며 시작 시각과 표시는 하나다. 경합을 실제로 일으키려고 여러 세션에서 되풀이한다.
func TestSessionBundle_동시에_두_번_다시_만들어도_결과가_같다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	clock := time.Date(2026, 10, 27, 9, 0, 0, 0, time.Local)
	now := func() time.Time { return clock }
	open := func() *memory.Store {
		s, err := memory.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Now = now
		t.Cleanup(func() { s.Close() })
		return s
	}
	s1, s2 := open(), open()
	cfg := config.Default()
	// 시험 DB에만 두는 기록 표: 묶음 줄이 실제로 고쳐진 횟수를 센다.
	if _, err := s1.DB().Exec(`CREATE TABLE test_bundle_writes(session TEXT);
		CREATE TRIGGER test_bundle_written AFTER UPDATE ON session_bundles BEGIN INSERT INTO test_bundle_writes VALUES(new.session); END;`); err != nil {
		t.Fatal(err)
	}
	pinned(t, s1, memory.SaveInput{Title: "답은 존댓말로 한다"})
	for i := range 12 {
		session := fmt.Sprintf("s%d", i)
		m := pinned(t, s1, memory.SaveInput{Title: fmt.Sprintf("임시 규칙 %d", i)})
		SessionBundle(s1, cfg, here, "claude", session)
		at := s1.SessionBundleAt("claude", session)
		if _, err := s1.SetState(m.ID, memory.StateStale, "폐기"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		got := make([]Injection, 2)
		for j, s := range []*memory.Store{s1, s2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				got[j] = SessionBundle(s, cfg, here, "claude", session)
			}()
		}
		close(start)
		wg.Wait()
		text, ids, _ := s1.SessionBundle("claude", session)
		if got[0].Text != got[1].Text || got[0].Text != text || !slices.Equal(ids, got[0].MemoryIDs) || slices.Contains(ids, m.ID) {
			t.Fatalf("%s: 두 훅의 글이나 저장된 글이 다릅니다:\n%s\n---\n%s\n---\n%s", session, got[0].Text, got[1].Text, text)
		}
		var writes int
		if err := s1.DB().QueryRow(`SELECT COUNT(*) FROM test_bundle_writes WHERE session=?`, session).Scan(&writes); err != nil {
			t.Fatal(err)
		}
		if writes != 1 || s1.SessionBundleAt("claude", session) != at {
			t.Fatalf("%s: 묶음을 %d번 고쳤거나 시작 시각이 바뀌었습니다", session, writes)
		}
		if marks := s1.SessionMarks("claude", session, memory.MarkBundle); len(marks) != 1 || !marks[m.ID] {
			t.Fatalf("%s: 뺀 기억 표시 %v", session, marks)
		}
	}
}

// voidOf is the session news lines of text that say a memory is no longer valid.
func voidOf(text string) []string {
	var out []string
	for _, l := range strings.Split(b3Of(text), "\n") {
		if strings.HasPrefix(l, voidPrefix) {
			out = append(out, l)
		}
	}
	return out
}

func TestPromptNews_후계_없는_폐기_만료_삭제는_무효_줄을_한_번_낸다(t *testing.T) {
	for _, c := range changes {
		if c.name == "대체" {
			continue // 후계가 살아 있으면 정정 줄이다(TestAC4)
		}
		// 바로 다음 요청에서 알리는 세션(s1)과, 먼저 재개해 묶음을 다시 만든 뒤 요청하는 세션(s2) 둘 다 한 번이다.
		for _, resume := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/재개%v", c.name, resume), func(t *testing.T) {
				s := store(t)
				cfg := config.Default()
				pinned(t, s, memory.SaveInput{Title: "답은 존댓말로 한다"})
				m := c.make(t, s)
				SessionBundle(s, cfg, here, "claude", "s1")
				later(s, time.Hour)
				c.apply(t, s, m)
				if resume {
					SessionBundle(s, cfg, here, "claude", "s1")
				}
				p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
				void := voidOf(p.Text)
				if len(void) != 1 || !strings.Contains(void[0], m.ID) || slices.Contains(p.MemoryIDs, m.ID) {
					t.Fatalf("무효 줄 %q, ids %v:\n%s", void, p.MemoryIDs, p.Text)
				}
				if c.name == "삭제" && strings.Contains(p.Text, m.Title) {
					t.Fatalf("지운 기억의 제목이 다시 나왔습니다:\n%s", p.Text)
				}
				RecordShown(s, "claude", "s1", p)
				if q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); strings.Contains(q.Text, m.ID) {
					t.Fatalf("무효 줄이 되풀이됐습니다:\n%s", q.Text)
				}
				SessionBundle(s, cfg, here, "claude", "s1")
				if q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); strings.Contains(q.Text, m.ID) {
					t.Fatalf("재개 뒤 무효 줄이 되풀이됐습니다:\n%s", q.Text)
				}
			})
		}
	}
}

func TestPromptNews_같은_기억의_정정은_세션에_한_번이다(t *testing.T) {
	cfg := config.Default()
	t.Run("정정한_뒤_후계가_지워져도", func(t *testing.T) {
		s := store(t)
		old := pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다", Topic: "보고 언어"})
		SessionBundle(s, cfg, here, "claude", "s1")
		later(s, time.Hour)
		cur, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "보고는 한국어로 쓴다", Topic: "보고 언어",
			Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "보고는 한국어로 쓴다"}})
		if err != nil {
			t.Fatal(err)
		}
		p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
		if !strings.Contains(p.Text, "정정") || !strings.Contains(p.Text, old.ID) {
			t.Fatalf("준비가 어긋났습니다: 정정 줄이 없습니다\n%s", p.Text)
		}
		RecordShown(s, "claude", "s1", p)
		if _, err := s.Forget(memory.ForgetInput{IDs: []string{cur.ID}, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "그 기억 지워"}}); err != nil {
			t.Fatal(err)
		}
		for _, resume := range []bool{false, true} {
			if resume {
				SessionBundle(s, cfg, here, "claude", "s1")
			}
			if q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); strings.Contains(q.Text, old.ID) {
				t.Fatalf("재개 %v: 같은 기억을 다시 알렸습니다:\n%s", resume, q.Text)
			}
		}
	})
	t.Run("무효를_알린_뒤_후계가_생겨도", func(t *testing.T) {
		s := store(t)
		old := pinned(t, s, memory.SaveInput{Title: "보고는 영어로 쓴다"})
		SessionBundle(s, cfg, here, "claude", "s1")
		later(s, time.Hour)
		if _, err := s.SetState(old.ID, memory.StateStale, "폐기"); err != nil {
			t.Fatal(err)
		}
		p := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{})
		if len(voidOf(p.Text)) != 1 {
			t.Fatalf("준비가 어긋났습니다: 무효 줄이 없습니다\n%s", p.Text)
		}
		RecordShown(s, "claude", "s1", p)
		later(s, time.Hour)
		cur := save(t, s, memory.KindRule, "보고는 한국어로 쓴다", "", memory.ScopeGlobal, true)
		if _, err := s.Supersede(old.ID, cur.ID, "새 버전"); err != nil {
			t.Fatal(err)
		}
		if q := PromptInjectionFor(s, cfg, here, "오늘 점심 뭐 먹지", "claude", "s1", Options{}); strings.Contains(q.Text, old.ID) {
			t.Fatalf("같은 기억을 다시 알렸습니다:\n%s", q.Text)
		}
	})
}
