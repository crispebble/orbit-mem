package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func candidate(t *testing.T, s *Store, title string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindLesson, Title: title, Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestProposal_후보_확정_승인과_거절(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "가")
	b := candidate(t, s, "나")
	pa, err := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "가 확정", MemoryIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "나 확정", MemoryIDs: []string{b.ID}})

	if _, err := s.Decide(DecideInput{ID: pa.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Get(a.ID); m.State != StateConfirmed {
		t.Fatalf("approved candidate state = %s", m.State)
	}
	if _, err := s.Decide(DecideInput{ID: pb.ID, Decision: "reject", Note: "틀림"}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Get(b.ID); m.State != StateHidden {
		t.Fatalf("rejected candidate state = %s", m.State)
	}
	pending, _ := s.Proposals(StatusPending)
	if len(pending) != 0 {
		t.Fatalf("pending = %d", len(pending))
	}
}

func TestProposal_고쳐서_승인하면_제목과_본문이_바뀐다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "초안 제목")
	p, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정", MemoryIDs: []string{a.ID}})
	title, body := "고친 제목", "고친 본문"
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Title: &title, Body: &body}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get(a.ID)
	if m.Title != title || m.Body != body || m.State != StateConfirmed {
		t.Fatalf("got %+v", m)
	}
}

func TestProposal_거절한_제안은_다시_올라오지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	in := ProposalInput{Kind: ProposalDirection, Title: "방향: 시험을 먼저", Body: "..."}
	p, _ := s.Propose(in)
	s.Decide(DecideInput{ID: p.ID, Decision: "reject"})
	again, err := s.Propose(in)
	if !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("err = %v, proposal = %+v", err, again)
	}
	// 대기 중인 같은 제안도 겹쳐 쌓지 않는다.
	in2 := ProposalInput{Kind: ProposalDirection, Title: "다른 방향"}
	s.Propose(in2)
	if _, err := s.Propose(in2); !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("pending duplicate err = %v", err)
	}
}

func TestProposal_낡음_정리와_합치기(t *testing.T) {
	s, _ := newTestStore(t)
	q := Source{Kind: SourceQuote, Excerpt: "q"}
	a, _ := s.Save(SaveInput{Kind: KindRule, Title: "규칙 하나", Scope: ScopeGlobal, Source: q})
	b, _ := s.Save(SaveInput{Kind: KindRule, Title: "규칙 둘", Scope: ScopeGlobal, Source: q})
	c, _ := s.Save(SaveInput{Kind: KindRule, Title: "규칙 셋", Scope: ScopeGlobal, Source: q})

	p1, _ := s.Propose(ProposalInput{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{a.ID}})
	s.Decide(DecideInput{ID: p1.ID, Decision: "approve"})
	if m, _ := s.Get(a.ID); m.State != StateStale {
		t.Fatalf("stale: %s", m.State)
	}

	p2, _ := s.Propose(ProposalInput{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{b.ID, c.ID},
		Payload: Payload{Kind: KindRule, MergedTitle: "규칙 둘과 셋", MergedBody: "합친 본문"}})
	res, err := s.Decide(DecideInput{ID: p2.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created == "" {
		t.Fatal("merge must create a memory")
	}
	merged, _ := s.Get(res.Created)
	if merged.State != StateConfirmed || merged.Title != "규칙 둘과 셋" {
		t.Fatalf("merged = %+v", merged)
	}
	for _, id := range []string{b.ID, c.ID} {
		if m, _ := s.Get(id); m.State != StateStale || m.SupersededBy != res.Created {
			t.Fatalf("old %s = %+v", id, m)
		}
	}
}

func TestProposal_재분류는_종류를_바꾼다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "옛 ISSUE")
	p, _ := s.Propose(ProposalInput{Kind: ProposalReclassify, Title: "재분류", MemoryIDs: []string{a.ID}, Payload: Payload{Kind: KindDecision}})
	s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if m, _ := s.Get(a.ID); m.Kind != KindDecision {
		t.Fatalf("kind = %s", m.Kind)
	}
}

func TestProposal_파일_반영은_파일만_바꾸고_그사이_바뀌었으면_멈춘다(t *testing.T) {
	s, _ := newTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	os.WriteFile(path, []byte("옛 내용\n"), 0o644)
	p, err := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "스킬에 반영", Payload: Payload{Path: path, Content: "새 내용\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body, "-옛 내용") || !strings.Contains(p.Body, "+새 내용") {
		t.Fatalf("diff missing in body: %q", p.Body)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "새 내용\n" {
		t.Fatalf("file = %q", got)
	}

	p2, _ := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "또 반영", Payload: Payload{Path: path, Content: "세번째\n"}})
	os.WriteFile(path, []byte("사람이 고침\n"), 0o644)
	if _, err := s.Decide(DecideInput{ID: p2.ID, Decision: "approve"}); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("err = %v", err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "사람이 고침\n" {
		t.Fatalf("file overwritten: %q", got)
	}
}

func TestProposal_방향_제안을_승인하면_결정으로_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	p, _ := s.Propose(ProposalInput{Kind: ProposalDirection, Title: "Atlas보다 Orbit 먼저", Body: "이유"})
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get(res.Created)
	if m.Kind != KindDecision || m.State != StateConfirmed {
		t.Fatalf("got %+v", m)
	}
}

func TestProposal_후보가_스스로_확정되면_제안은_자동으로_닫힌다(t *testing.T) {
	s, c := newTestStore(t)
	in := SaveInput{Kind: KindLesson, Title: "반복 교훈", Scope: ScopeGlobal, Source: Source{Kind: SourceSession}}
	m, _ := s.Save(in)
	s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정?", MemoryIDs: []string{m.ID}})
	c.t = c.t.AddDate(0, 0, 1)
	s.Save(in)
	if n, _ := s.CloseSettled(); n != 1 {
		t.Fatalf("closed = %d", n)
	}
	pending, _ := s.Proposals(StatusPending)
	if len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestProposal_파일_반영은_심볼릭_링크의_원본을_고친다(t *testing.T) {
	s, _ := newTestStore(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.md")
	link := filepath.Join(dir, "link.md")
	os.WriteFile(real, []byte("옛\n"), 0o644)
	os.Symlink(real, link)
	p, err := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "링크", Payload: Payload{Path: link, Content: "새\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	if b, _ := os.ReadFile(real); string(b) != "새\n" {
		t.Fatalf("real = %q", b)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*tmp*")); len(m) != 0 {
		t.Fatalf("temp left: %v", m)
	}
}

func TestProposal_파일_반영에는_본문_덮어쓰기를_받지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	path := filepath.Join(t.TempDir(), "a.md")
	os.WriteFile(path, []byte("a\n"), 0o644)
	p, _ := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "x", Payload: Payload{Path: path, Content: "b\n"}})
	body := "메모"
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Body: &body}); err == nil {
		t.Fatal("body override accepted for apply_file")
	}
}

// 설계 · Orbit 3차 O3-3: 합친 기억은 합치는 기억들의 태그를 잇되 옛 표식(imported:*)은 뺀다. 합친 제목이 합치는 기억 하나의
// 제목과 같으면 그 기억에 합친 본문·태그를 새 판으로 쌓고, 원장 되돌리기로 옛 판으로 돌아온다.
func TestProposal_합치기는_태그를_잇고_같은_제목이면_그_기억에_쌓는다(t *testing.T) {
	s, _ := newTestStore(t)
	save := func(title string, tags ...string) Memory {
		m, err := s.Save(SaveInput{Kind: KindRule, Title: title, Body: title + " 본문", Tags: tags, Scope: ScopeGlobal, State: StateConfirmed,
			Source: Source{Kind: SourceSession, Ref: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	cur := save("커밋은 경로를 명시한다", "git")
	old := save("커밋할 때 경로를 쓴다", "Git", "커밋", "imported:server-knowledge", "space:onidot")
	other := save("화면은 키트로 만든다", "studio")
	older := save("화면은 옛 부품으로 만든다", "studio", "imported:claude-memory")

	p, _ := s.Propose(ProposalInput{Kind: ProposalMerge, Title: "새 제목으로 합치기", MemoryIDs: []string{other.ID, older.ID},
		Payload: Payload{MergedTitle: "화면은 Studio 키트로 만든다", MergedBody: "합친 본문"}})
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Get(res.Created); strings.Join(m.Tags, ",") != "studio" {
		t.Errorf("새로 만든 합친 기억 태그 %v", m.Tags)
	}

	p, _ = s.Propose(ProposalInput{Kind: ProposalMerge, Title: "같은 제목으로 합치기", MemoryIDs: []string{cur.ID, old.ID},
		Payload: Payload{MergedTitle: "커밋은 경로를 명시한다", MergedBody: "git add -A 금지"}})
	res, err = s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get(cur.ID)
	if res.Created != "" || m.Body != "git add -A 금지" || strings.Join(m.Tags, ",") != "git,커밋,space:onidot" || m.State != StateConfirmed {
		t.Fatalf("같은 제목 합치기 created=%q %+v", res.Created, m)
	}
	if o, _ := s.Get(old.ID); o.State != StateStale || o.SupersededBy != cur.ID {
		t.Fatalf("합쳐진 옛 기억 %+v", o)
	}
	rv, err := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: res.LedgerID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: rv.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Get(cur.ID); m.Body != cur.Body || strings.Join(m.Tags, ",") != "git" {
		t.Errorf("되돌린 지금 기억 %+v", m)
	}
	if o, _ := s.Get(old.ID); o.State != StateConfirmed || o.SupersededBy != "" {
		t.Errorf("되돌린 옛 기억 %+v", o)
	}
}
