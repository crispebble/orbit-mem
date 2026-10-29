package memory

import (
	"errors"
	"path/filepath"
	"testing"
)

func saved(t *testing.T, s *Store, title string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindFact, Title: title, Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "claude:s1"}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 끝 기준 1: 이으면 양쪽에서 보이고, 같은 연결을 다시 달면 하나뿐이다.
func TestLink_양쪽에서_보이고_같은_쌍은_하나(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	l, created, err := s.Link(LinkInput{From: b.ID, To: a.ID, Note: "같은 결정 token=sk-abcdefghijklmnop1234", Origin: LinkOriginAI, OriginRef: "claude:s1"})
	if err != nil || !created {
		t.Fatalf("잇기: %v %v", created, err)
	}
	if l.A >= l.B {
		t.Errorf("양 끝을 id 순으로 맞추지 않았습니다: %s %s", l.A, l.B)
	}
	if l.Note == "같은 결정 token=sk-abcdefghijklmnop1234" {
		t.Error("근거 글의 비밀값을 가리지 않았습니다")
	}
	again, created, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginAI})
	if err != nil || created || again.ID != l.ID {
		t.Fatalf("같은 쌍을 다시 이으면 있던 연결이어야 합니다: %v %v %v", again.ID, created, err)
	}
	for _, m := range []Memory{a, b} {
		ls, err := s.LinksOf(m.ID)
		if err != nil || len(ls) != 1 || ls[0].ID != l.ID {
			t.Fatalf("%s의 연결: %+v %v", m.Title, ls, err)
		}
		other := a.ID
		if m.ID == a.ID {
			other = b.ID
		}
		if ls[0].MemoryID != other || ls[0].Origin != LinkOriginAI {
			t.Errorf("상대 기억·만든 쪽: %+v", ls[0])
		}
	}
	// 연결을 바꿔도 기억의 판은 그대로다
	if got, _ := s.Get(a.ID); got.Version != a.Version {
		t.Errorf("판이 바뀌었습니다: %d → %d", a.Version, got.Version)
	}
}

// 끝 기준 2: 떼면 빠지고, 표에는 뗀 때·뗀 쪽이 남는다. 판은 그대로다. 다시 이을 수 있다.
func TestUnlink_지우지_않고_뗀_기록을_남긴다(t *testing.T) {
	s, c := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	l, _, _ := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginAI})
	c.t = c.t.Add(60e9)
	removed, err := s.Unlink(b.ID, a.ID, LinkOriginAI, "관련 없음")
	if err != nil || !removed {
		t.Fatalf("떼기: %v %v", removed, err)
	}
	if ls, _ := s.LinksOf(a.ID); len(ls) != 0 {
		t.Errorf("뗀 연결이 보입니다: %+v", ls)
	}
	var at int64
	var origin, reason string
	if err := s.db.QueryRow(`SELECT removed_at, removed_origin, removed_reason FROM memory_links WHERE id=?`, l.ID).Scan(&at, &origin, &reason); err != nil {
		t.Fatal(err)
	}
	if at != c.t.UnixMilli() || origin != LinkOriginAI || reason != "관련 없음" {
		t.Errorf("뗀 기록: %d %s %s", at, origin, reason)
	}
	if removed, err := s.Unlink(a.ID, b.ID, LinkOriginAI, ""); err != nil || removed {
		t.Errorf("없는 연결 떼기는 아무것도 하지 않습니다: %v %v", removed, err)
	}
	l2, created, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginRetro, OriginRef: "p-1"})
	if err != nil || !created || l2.ID == l.ID {
		t.Fatalf("뗀 뒤 다시 잇기: %v %v", created, err)
	}
	if got, _ := s.Get(a.ID); got.Version != a.Version {
		t.Error("판이 바뀌었습니다")
	}
}

// 끝 기준 3: 없는 기억·자기 자신·모르는 만든 쪽은 거절한다.
func TestLink_거절(t *testing.T) {
	s, _ := newTestStore(t)
	a := saved(t, s, "A 기억")
	if _, _, err := s.Link(LinkInput{From: a.ID, To: "m-없음", Origin: LinkOriginAI}); !errors.Is(err, ErrNotFound) {
		t.Errorf("없는 기억: %v", err)
	}
	if _, _, err := s.Link(LinkInput{From: a.ID, To: a.ID, Origin: LinkOriginAI}); !errors.Is(err, ErrInvalid) {
		t.Errorf("자기 자신: %v", err)
	}
	b := saved(t, s, "B 기억")
	if _, _, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: "import"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("모르는 만든 쪽: %v", err)
	}
}

func TestLinks_살아_있는_연결과_사건(t *testing.T) {
	s, _ := newTestStore(t)
	a, b, c := saved(t, s, "A 기억"), saved(t, s, "B 기억"), saved(t, s, "C 기억")
	_, _, _ = s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginAI})
	_, _, _ = s.Link(LinkInput{From: b.ID, To: c.ID, Origin: LinkOriginUser})
	_, _ = s.Unlink(a.ID, b.ID, LinkOriginUser, "")
	ls, err := s.Links()
	if err != nil || len(ls) != 1 || ls[0].Origin != LinkOriginUser {
		t.Fatalf("살아 있는 연결: %+v %v", ls, err)
	}
	day := Day(s.Now())
	linked, _ := s.Events(EventFilter{FromDay: day, ToDay: day, KindPrefix: EventMemoryLinked})
	unlinked, _ := s.Events(EventFilter{FromDay: day, ToDay: day, KindPrefix: EventMemoryUnlinked})
	if len(linked) != 2 || len(unlinked) != 1 {
		t.Errorf("사건: 이음 %d개, 뗌 %d개", len(linked), len(unlinked))
	}
}

// 끝 기준 10: 연결 표가 없던 옛 DB를 열면 표만 더해지고 기존 기억은 그대로다.
func TestOpen_옛_DB에_연결_표만_더한다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := saved(t, s, "옛 기억")
	if _, err := s.db.Exec(`DROP TABLE memory_links`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(m.ID)
	if err != nil || got.Title != "옛 기억" || got.Version != m.Version {
		t.Fatalf("기존 기억: %+v %v", got, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_links`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("연결 표: %d %v", n, err)
	}
}
