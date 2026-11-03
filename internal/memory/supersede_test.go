package memory

// 설계 · orbit-mem 2판 4.2·4.3: 같은 범위·주제로 저장하면 옛 현재값을 대체하고, supersedes로 다른 자리의 옛 판도
// 같은 트랜잭션에서 대체한다. 대체 여부는 출처별 규칙을 따른다(D1).

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func supStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local)
	s.Now = func() time.Time { at = at.Add(time.Minute); return at }
	t.Cleanup(func() { s.Close() })
	return s
}

func userSays(kind, title, topic string) SaveInput {
	return SaveInput{Kind: kind, Title: title, Topic: topic, Source: Source{Kind: SourceQuote, Excerpt: title}, Who: Who{Actor: ActorAI}}
}

func aiLearns(kind, title, topic string) SaveInput {
	return SaveInput{Kind: kind, Title: title, Topic: topic, Source: Source{Kind: SourceSession, Ref: "s"}, Who: Who{Actor: ActorAI}}
}

func mustSave(t *testing.T, s *Store, in SaveInput) (Memory, SaveReport) {
	t.Helper()
	m, rep, err := s.SaveWith(in)
	if err != nil {
		t.Fatal(err)
	}
	return m, rep
}

func stateOf(t *testing.T, s *Store, id string) Memory {
	t.Helper()
	m, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSave_새_사용자_말은_같은_주제의_옛_사용자_말을_userQuote_없이_대체한다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userSays(KindDecision, "상단바는 알약으로", "상단바 상태"))
	cur, rep := mustSave(t, s, userSays(KindDecision, "상단바는 알약으로 하지 않는다", "상단바 상태"))
	got := stateOf(t, s, old.ID)
	if got.State != StateStale || got.SupersededBy != cur.ID || !slices.Contains(rep.Superseded, old.ID) {
		t.Fatalf("old=%+v rep=%+v", got, rep)
	}
	h, _ := s.History(old.ID)
	if last := h[len(h)-1]; last.Action != "supersede" || last.Reason == "" {
		t.Fatalf("history %+v", last)
	}
	// 옛 기억에 새 말을 출처로 붙이지 않는다(내용을 고친 것이 아니다).
	for _, src := range got.Sources {
		if src.Excerpt == cur.Title {
			t.Fatalf("new words attached to the old memory: %+v", got.Sources)
		}
	}
}

func TestSave_AI가_배운_것은_사용자_말을_대체하지_않고_어긋남으로_표시한다(t *testing.T) {
	s := supStore(t)
	user, _ := mustSave(t, s, userSays(KindRule, "보고는 한국어로", "보고 언어"))
	ai, rep := mustSave(t, s, aiLearns(KindRule, "보고는 영어로", "보고 언어"))
	if stateOf(t, s, user.ID).State != StateConfirmed || len(rep.Superseded) != 0 || !slices.Contains(rep.Conflicts, user.ID) {
		t.Fatalf("user rule replaced or no conflict: rep=%+v", rep)
	}
	flags, _ := s.OpenFlags()
	if len(flags) != 1 || flags[0].MemoryID != ai.ID || flags[0].OtherID != user.ID {
		t.Fatalf("flags = %+v", flags)
	}
	// supersedes로 지목해도 같다.
	_, rep = mustSave(t, s, SaveInput{Kind: KindRule, Title: "보고는 일본어로", Topic: "다른 주제", Supersedes: []string{user.ID},
		Source: Source{Kind: SourceSession, Ref: "s"}, Who: Who{Actor: ActorAI}})
	if stateOf(t, s, user.ID).State != StateConfirmed || !slices.Contains(rep.Conflicts, user.ID) {
		t.Fatalf("explicit supersedes replaced the user's words: %+v", rep)
	}
}

func TestSave_AI가_배운_것끼리는_대체한다(t *testing.T) {
	s := supStore(t)
	a, _ := mustSave(t, s, aiLearns(KindFact, "빌드는 make", "빌드 명령"))
	b, _ := mustSave(t, s, aiLearns(KindFact, "빌드는 go build", "빌드 명령"))
	if got := stateOf(t, s, a.ID); got.State != StateStale || got.SupersededBy != b.ID {
		t.Fatalf("got %+v", got)
	}
}

func TestSave_임시인_말은_대체하지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userSays(KindRule, "보고는 한국어로", "보고 언어"))
	in := userSays(KindRule, "이번 주만 보고는 영어로", "보고 언어")
	in.Retention = RetentionVolatile
	_, rep := mustSave(t, s, in)
	if stateOf(t, s, old.ID).State != StateConfirmed || len(rep.Superseded) != 0 {
		t.Fatalf("volatile replaced: %+v", rep)
	}
}

func TestSave_다른_범위의_같은_주제는_대체하지_않고_supersedes는_범위를_넘는다(t *testing.T) {
	s := supStore(t)
	global, _ := mustSave(t, s, userSays(KindDecision, "기본 모델은 A", "기본 모델"))
	in := userSays(KindDecision, "이 앱 기본 모델은 B", "기본 모델")
	in.Scope = "project:github.com/me/app"
	local, _ := mustSave(t, s, in)
	if stateOf(t, s, global.ID).State != StateConfirmed {
		t.Fatal("another scope replaced")
	}
	in = userSays(KindDecision, "기본 모델은 C", "모델 선택")
	in.Scope = "project:github.com/me/app"
	in.Supersedes = []string{global.ID, local.ID}
	c, rep := mustSave(t, s, in)
	if stateOf(t, s, global.ID).SupersededBy != c.ID || stateOf(t, s, local.ID).SupersededBy != c.ID || len(rep.Superseded) != 2 {
		t.Fatalf("rep %+v", rep)
	}
}

func TestSave_없는_supersedes는_저장째_거절한다(t *testing.T) {
	s := supStore(t)
	in := userSays(KindRule, "x", "x")
	in.Supersedes = []string{"m-없음"}
	if _, _, err := s.SaveWith(in); err == nil {
		t.Fatal("want error")
	}
	if n, _ := s.Count(); n != 0 {
		t.Fatalf("saved %d", n)
	}
}

func TestSave_도구_트리거를_남기고_고친다(t *testing.T) {
	s := supStore(t)
	in := userSays(KindRule, "Orbit 문서는 Orbit Space에", "atlas 문서 위치")
	in.Tools = []string{"mcp__atlas__write_page", " mcp__atlas__* ", "mcp__atlas__write_page"}
	m, _ := mustSave(t, s, in)
	if !slices.Equal(m.Tools, []string{"mcp__atlas__write_page", "mcp__atlas__*"}) {
		t.Fatalf("tools %v", m.Tools)
	}
	tools := []string{"Bash"}
	got, err := s.Update(UpdateInput{ID: m.ID, Tools: &tools})
	if err != nil || !slices.Equal(got.Tools, tools) {
		t.Fatalf("update %v %v", got.Tools, err)
	}
	bad := userSays(KindRule, "y", "y")
	bad.Tools = []string{"mcp atlas"}
	if _, _, err := s.SaveWith(bad); err == nil {
		t.Fatal("a tool name with a space must be refused")
	}
}

func TestTopicHeads_날이_지난_임시_기억은_현재값이_아니다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userSays(KindRule, "보고는 한국어로", "보고 언어"))
	in := userSays(KindRule, "이번 주만 영어로", "보고 언어")
	in.Retention, in.TTLDays = RetentionVolatile, 1
	tmp, _ := mustSave(t, s, in)
	heads, _ := s.TopicHeads()
	if heads.Superseding(old) != tmp.ID {
		t.Fatal("while it lasts the temporary value leads")
	}
	at := s.Now().Add(48 * time.Hour)
	s.Now = func() time.Time { return at }
	heads, _ = s.TopicHeads()
	if heads.Superseding(old) != "" {
		t.Fatal("an expired temporary value hides the old one")
	}
}

func TestSave_거절된_같은_제목으로_돌아오면_대체하지_않는다(t *testing.T) {
	s := supStore(t)
	x, _ := mustSave(t, s, aiLearns(KindFact, "빌드는 make", "빌드"))
	h, _ := mustSave(t, s, aiLearns(KindFact, "빌드는 bazel", "다른"))
	hidden := StateHidden
	if _, err := s.Update(UpdateInput{ID: h.ID, State: &hidden}); err != nil {
		t.Fatal(err)
	}
	in := aiLearns(KindFact, "빌드는 bazel", "다른")
	in.Supersedes = []string{x.ID}
	if _, rep := mustSave(t, s, in); len(rep.Superseded) != 0 || stateOf(t, s, x.ID).State != StateCandidate {
		t.Fatalf("a rejected memory replaced a live one: %+v", rep)
	}
}
