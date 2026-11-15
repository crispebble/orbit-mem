package memory

import (
	"errors"
	"testing"
)

// 지운 기억이 들여오기·저장 재시도로 되살아나지 않는다(명세 5.4).

func reviveSave(t *testing.T, s *Store, title, legacyID string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindFact, Title: title, Body: "본문 " + title, Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "s"}, LegacyID: legacyID})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func reviveForget(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	if _, err := s.Forget(ForgetInput{IDs: ids, Who: Who{Actor: ActorUser}}); err != nil {
		t.Fatal(err)
	}
}

func TestImport_지운_기억은_forgotten으로_건너뛴다(t *testing.T) {
	s, _ := newTestStore(t)
	m := reviveSave(t, s, "지울 기억", "")
	exported, err := s.Export()
	if err != nil || len(exported) != 1 {
		t.Fatalf("export %v %v", exported, err)
	}
	reviveForget(t, s, m.ID)
	if res, err := s.Import(exported[0], "옮김", false); err != nil || res != ImportForgotten {
		t.Fatalf("import = %q %v", res, err)
	}
	if _, err := s.Get(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forgotten memory came back: %v", err)
	}
	// 미리보기도 같은 답을 준다.
	if res, err := s.Import(exported[0], "옮김", true); err != nil || res != ImportForgotten {
		t.Fatalf("dry import = %q %v", res, err)
	}
}

func TestImport_지운_legacy_id만_같아도_forgotten으로_건너뛴다(t *testing.T) {
	s, _ := newTestStore(t)
	m := reviveSave(t, s, "옛 기억", "legacy-revive-1")
	reviveForget(t, s, m.ID)
	other := Memory{ID: "m-other0001", Kind: KindFact, Scope: ScopeGlobal, State: StateConfirmed, Title: "옛 기억 다른 id",
		Body: "본문", LegacyID: "legacy-revive-1"}
	if res, err := s.Import(other, "옮김", false); err != nil || res != ImportForgotten {
		t.Fatalf("import = %q %v", res, err)
	}
	if _, err := s.Get(other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("memory with a forgotten legacy id came back: %v", err)
	}
}

func TestImport_지운_기억을_가리키는_대체_표시는_비우고_들여온다(t *testing.T) {
	s, _ := newTestStore(t)
	gone := reviveSave(t, s, "지울 새 판", "")
	reviveForget(t, s, gone.ID)
	old := Memory{ID: "m-oldver001", Kind: KindFact, Scope: ScopeGlobal, State: StateStale, Title: "옛 판", Body: "본문",
		SupersededBy: gone.ID, StaleReason: "새 판이 대체함"}
	if res, err := s.Import(old, "옮김", false); err != nil || res != ImportDone {
		t.Fatalf("import = %q %v", res, err)
	}
	got, err := s.Get(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SupersededBy != "" || got.State != StateStale || got.StaleReason != forgetSuccessorGone {
		t.Fatalf("got superseded_by=%q state=%q reason=%q", got.SupersededBy, got.State, got.StaleReason)
	}
}

func TestIdem_지운_기억을_만든_저장_요청을_재시도하면_거절한다(t *testing.T) {
	// save·supersede 키는 Save로, update 키는 Update로 다시 보낸다.
	s, _ := newTestStore(t)
	saved, _, err := s.SaveWith(idemSave("키로 저장", "k1", "h1"))
	if err != nil {
		t.Fatal(err)
	}
	base := reviveSave(t, s, "대체될 기억", "")
	sup := idemSave("대체하는 기억", "k2", "h2")
	sup.Replaces = base.ID
	sup.Who = Who{Actor: ActorUser}
	superseding, _, err := s.SaveWith(sup)
	if err != nil {
		t.Fatal(err)
	}
	target := reviveSave(t, s, "고칠 기억", "")
	body := "고친 본문"
	if _, err := s.Update(UpdateInput{ID: target.ID, Body: &body, Idem: &Idem{Key: "k3", Hash: "h3"}}); err != nil {
		t.Fatal(err)
	}
	reviveForget(t, s, saved.ID, superseding.ID, target.ID)
	before := countOf(t, s, `SELECT COUNT(*) FROM memories`)

	if _, _, err := s.SaveWith(idemSave("키로 저장", "k1", "h1")); !errors.Is(err, ErrForgotten) {
		t.Fatalf("save retry err = %v", err)
	}
	if _, _, err := s.SaveWith(sup); !errors.Is(err, ErrForgotten) {
		t.Fatalf("supersede retry err = %v", err)
	}
	if _, err := s.Update(UpdateInput{ID: target.ID, Body: &body, Idem: &Idem{Key: "k3", Hash: "h3"}}); !errors.Is(err, ErrForgotten) {
		t.Fatalf("update retry err = %v", err)
	}
	if after := countOf(t, s, `SELECT COUNT(*) FROM memories`); after != before {
		t.Fatalf("memories %d -> %d", before, after)
	}
}

func TestIdem_같은_내용도_새_키로는_저장된다(t *testing.T) {
	s, _ := newTestStore(t)
	m, _, err := s.SaveWith(idemSave("다시 쓸 기억", "k1", "h1"))
	if err != nil {
		t.Fatal(err)
	}
	reviveForget(t, s, m.ID)
	again, rep, err := s.SaveWith(idemSave("다시 쓸 기억", "k-new", "h1"))
	if err != nil || rep.Replayed != nil || again.ID == m.ID {
		t.Fatalf("new key save = %+v %+v %v", again, rep, err)
	}
}

func TestIdem_forgotten_표가_없는_DB에서도_재시도가_돈다(t *testing.T) {
	s, _ := newTestStore(t)
	m, _, err := s.SaveWith(idemSave("표 없는 DB", "k1", "h1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP TABLE forgotten`); err != nil {
		t.Fatal(err)
	}
	again, rep, err := s.SaveWith(idemSave("표 없는 DB", "k1", "h1"))
	if err != nil || rep.Replayed == nil || again.ID != m.ID {
		t.Fatalf("retry = %+v %+v %v", again, rep, err)
	}
}

func TestImport_forgotten_표가_없는_DB에서도_들여온다(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.DB().Exec(`DROP TABLE forgotten`); err != nil {
		t.Fatal(err)
	}
	m := Memory{ID: "m-notable001", Kind: KindFact, Scope: ScopeGlobal, State: StateConfirmed, Title: "표 없는 DB 들여오기", Body: "본문"}
	if res, err := s.Import(m, "옮김", false); err != nil || res != ImportDone {
		t.Fatalf("import = %q %v", res, err)
	}
	if _, err := s.Get(m.ID); err != nil {
		t.Fatalf("memory not imported: %v", err)
	}
}
