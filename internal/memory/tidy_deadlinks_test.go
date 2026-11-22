package memory

import "testing"

// F-6: 정리 보고에 살아 있는 기억이 현재 값이 아닌 기억을 가리키는 연결 수가 나온다.
func TestTidy_살아있는_기억이_폐기된_기억을_가리키는_연결_수를_보고한다(t *testing.T) {
	s, _ := newTestStore(t)
	live, stale, hidden, other := candidate(t, s, "산 기억"), candidate(t, s, "낡은 기억"), candidate(t, s, "숨긴 기억"), candidate(t, s, "다른 산 기억")
	for _, to := range []string{stale.ID, hidden.ID, other.ID} {
		if _, _, err := s.Link(LinkInput{From: live.ID, To: to, Origin: LinkOriginUser}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetState(stale.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(hidden.ID, StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeadLinkCount(); err != nil || n != 2 {
		t.Fatalf("DeadLinkCount = %d, %v", n, err)
	}
	if r := s.Tidy(TidyOptions{LedgerWindowDays: 7}); r.DeadLinks != 2 {
		t.Fatalf("DeadLinks = %d (%+v)", r.DeadLinks, r)
	}
}
