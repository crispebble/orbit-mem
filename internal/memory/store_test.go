package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)}
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Now = c.now
	t.Cleanup(func() { s.Close() })
	return s, c
}

func TestSave_내말_인용이면_바로_확정(t *testing.T) {
	s, _ := newTestStore(t)
	m, err := s.Save(SaveInput{Kind: KindRule, Title: "답은 한글 존댓말로", Body: "항상", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "답은 항상 한글 존댓말로 해"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != StateConfirmed || m.Version != 1 || len(m.Sources) != 1 {
		t.Fatalf("got %+v", m)
	}
}

func TestSave_인용이_아니면_후보로_시작한다(t *testing.T) {
	s, _ := newTestStore(t)
	m, err := s.Save(SaveInput{Kind: KindLesson, Title: "codex exec는 stdin을 닫는다", Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "claude:abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != StateCandidate {
		t.Fatalf("state = %s", m.State)
	}
}

func TestSave_후보는_서로_다른_날_두번_나오면_확정된다(t *testing.T) {
	s, c := newTestStore(t)
	in := SaveInput{Kind: KindLesson, Title: "codex exec는 stdin을 닫는다", Scope: ScopeGlobal, Source: Source{Kind: SourceSession}}
	first, _ := s.Save(in)
	again, err := s.Save(in) // 같은 날
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.State != StateCandidate {
		t.Fatalf("same day: %+v", again)
	}
	c.t = c.t.Add(24 * time.Hour)
	third, _ := s.Save(in)
	if third.ID != first.ID || third.State != StateConfirmed {
		t.Fatalf("next day: %+v", third)
	}
	if len(third.Days) != 2 {
		t.Fatalf("days = %v", third.Days)
	}
}

func TestSave_숨긴_후보와_같은_제목은_다시_올라오지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	in := SaveInput{Kind: KindFact, Title: "쓸모없는 사실", Scope: ScopeGlobal, Source: Source{Kind: SourceSession}}
	m, _ := s.Save(in)
	if _, err := s.SetState(m.ID, StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	again, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != m.ID || again.State != StateHidden {
		t.Fatalf("hidden came back: %+v", again)
	}
	found, _ := s.Find(FindInput{Query: "쓸모없는"})
	if len(found) != 0 {
		t.Fatalf("hidden must not be found: %+v", found)
	}
}

func TestUpdate_버전이력을_남기고_낡은_판본으로는_못_고친다(t *testing.T) {
	s, _ := newTestStore(t)
	m, _ := s.Save(SaveInput{Kind: KindFact, Title: "A", Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: "A"}})
	title := "B"
	u, err := s.Update(UpdateInput{ID: m.ID, ExpectedVersion: 1, Title: &title, Reason: "고침"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Version != 2 || u.Title != "B" {
		t.Fatalf("got %+v", u)
	}
	if _, err := s.Update(UpdateInput{ID: m.ID, ExpectedVersion: 1, Title: &title}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v", err)
	}
	h, err := s.History(m.ID)
	if err != nil || len(h) != 2 || h[0].Memory.Title != "A" || h[1].Action != "update" {
		t.Fatalf("history = %+v, %v", h, err)
	}
}

func TestSupersede_옛_기억은_낡음이_되고_새_기억을_가리킨다(t *testing.T) {
	s, _ := newTestStore(t)
	old, _ := s.Save(SaveInput{Kind: KindRule, Title: "옛 규칙", Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: "x"}})
	nw, _ := s.Save(SaveInput{Kind: KindRule, Title: "새 규칙", Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: "y"}})
	got, err := s.Supersede(old.ID, nw.ID, "바뀜")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateStale || got.SupersededBy != nw.ID {
		t.Fatalf("got %+v", got)
	}
}

func TestFind_범위와_종류와_상태로_거른다(t *testing.T) {
	s, _ := newTestStore(t)
	q := Source{Kind: SourceQuote, Excerpt: "q"}
	s.Save(SaveInput{Kind: KindRule, Title: "전역 커밋 규칙", Scope: ScopeGlobal, Source: q})
	s.Save(SaveInput{Kind: KindDecision, Title: "이 저장소 커밋 결정", Scope: "project:github.com/a/b", Source: q})
	s.Save(SaveInput{Kind: KindDecision, Title: "다른 저장소 커밋 결정", Scope: "project:github.com/c/d", Source: q})

	got, err := s.Find(FindInput{Query: "커밋", Project: "project:github.com/a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("global+this project expected, got %d: %+v", len(got), got)
	}
	got, _ = s.Find(FindInput{Query: "커밋", Kinds: []string{KindRule}, AllProjects: true})
	if len(got) != 1 || got[0].Kind != KindRule {
		t.Fatalf("kind filter: %+v", got)
	}
	got, _ = s.Find(FindInput{Query: "커밋", AllProjects: true})
	if len(got) != 3 {
		t.Fatalf("all projects: %d", len(got))
	}
}

func TestOpen_권한은_본인만(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sub", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	st, err := os.Stat(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("dir perm = %v", st.Mode().Perm())
	}
}

func TestBackup_사본을_만들고_오래된_것은_지운다(t *testing.T) {
	s, c := newTestStore(t)
	s.Save(SaveInput{Kind: KindFact, Title: "x", Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: "x"}})
	dir := t.TempDir()
	for i := 0; i < 16; i++ {
		if _, err := s.Backup(dir, 14); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(24 * time.Hour)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 14 {
		t.Fatalf("backups = %d", len(entries))
	}
	b, err := Open(filepath.Join(dir, entries[len(entries)-1].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if n, _ := b.Count(); n != 1 {
		t.Fatalf("backup rows = %d", n)
	}
}
