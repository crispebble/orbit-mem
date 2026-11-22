package memory

import "testing"

// F-6: 정리 보고에 살아 있는 기억이 현재 값이 아닌 기억을 가리키는 연결 수가 나온다.
// 산 기억 둘과 죽은 기억 둘을 서로 엇갈리게 이어, 연결 양 끝의 id 순서와 무관하게 센다.
func TestTidy_살아있는_기억이_폐기된_기억을_가리키는_연결_수를_보고한다(t *testing.T) {
	s, _ := newTestStore(t)
	live1, live2 := candidate(t, s, "산 기억 하나"), candidate(t, s, "산 기억 둘")
	stale, hidden := candidate(t, s, "낡은 기억"), candidate(t, s, "숨긴 기억")
	if _, err := s.SetState(stale.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(hidden.ID, StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	link := func(x, y string) string {
		l, _, err := s.Link(LinkInput{From: x, To: y, Origin: LinkOriginUser})
		if err != nil {
			t.Fatal(err)
		}
		return l.ID
	}
	// 센다: 산 쪽과 죽은 쪽을 양방향으로 엇갈리게 넷.
	link(live1.ID, stale.ID)
	link(hidden.ID, live1.ID)
	link(live2.ID, hidden.ID)
	removed := link(stale.ID, live2.ID)
	// 세지 않는다: 양쪽 모두 산 것, 양쪽 모두 죽은 것.
	link(live1.ID, live2.ID)
	link(stale.ID, hidden.ID)
	if n, err := s.DeadLinkCount(); err != nil || n != 4 {
		t.Fatalf("DeadLinkCount = %d, %v", n, err)
	}
	if r := s.Tidy(TidyOptions{LedgerWindowDays: 7}); r.DeadLinks != 4 {
		t.Fatalf("DeadLinks = %d (%+v)", r.DeadLinks, r)
	}
	// 뗀 연결은 세지 않는다.
	if _, err := s.UnlinkID(removed, LinkOriginUser, "정리"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.DeadLinkCount(); n != 3 {
		t.Fatalf("뗀 뒤 = %d", n)
	}
}

// 사건을 기록할 때 읽은 기억 상태: 지운 기억과 빈 입력은 결과에 없다.
func TestStatesOf_id_묶음의_상태를_한_번에_읽는다(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := candidate(t, s, "가"), candidate(t, s, "나")
	if _, err := s.SetState(b.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	got, err := s.StatesOf([]string{a.ID, b.ID, "m-gone"})
	if err != nil || len(got) != 2 || got[a.ID] != StateCandidate || got[b.ID] != StateStale {
		t.Fatalf("got = %v, %v", got, err)
	}
	if got, err := s.StatesOf(nil); err != nil || len(got) != 0 {
		t.Fatalf("빈 입력 = %v, %v", got, err)
	}
}
