package memory

import (
	"fmt"
	"slices"
	"sync"
	"testing"
)

func take(t *testing.T, s *Store, session, kind, taker string) string {
	t.Helper()
	text, ok, err := s.TakeDelegation("h", session, kind, taker)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return text
}

func delegationRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM session_bundles`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 설계 4.1: 같은 세션·같은 에이전트 종류에서 먼저 온 위임 글부터 짝을 맞추고, 가져간 글은 다른 서브에이전트가 다시 못 쓴다.
func TestDelegation_같은_세션_같은_종류에서_먼저_온_차례로_짝을_맞춘다(t *testing.T) {
	s, _ := newTestStore(t)
	for _, d := range []struct{ session, kind, task string }{
		{"S", "worker", "첫 위임"}, {"S", "reviewer", "검토 위임"}, {"S", "worker", "둘째 위임"}, {"T", "worker", "다른 세션 위임"},
	} {
		if err := s.SaveDelegation("h", d.session, d.kind, d.task); err != nil {
			t.Fatal(err)
		}
	}
	if got := take(t, s, "S", "worker", "a1"); got != "첫 위임" {
		t.Fatalf("a1 = %q", got)
	}
	if got := take(t, s, "S", "worker", "a2"); got != "둘째 위임" {
		t.Fatalf("a2 = %q", got)
	}
	if got := take(t, s, "S", "worker", "a3"); got != "" {
		t.Fatalf("a3 = %q (남은 worker 위임이 없다)", got)
	}
	if got := take(t, s, "S", "explorer", "a4"); got != "" {
		t.Fatalf("종류가 다르면 짝이 없다: %q", got)
	}
	if got := take(t, s, "S", "reviewer", "a5"); got != "검토 위임" {
		t.Fatalf("a5 = %q", got)
	}
	// 같은 서브에이전트가 다시 물으면 가져간 글을 그대로 준다(다음 위임을 빼앗지 않는다).
	if got := take(t, s, "S", "worker", "a1"); got != "첫 위임" {
		t.Fatalf("a1 again = %q", got)
	}
	if got := take(t, s, "T", "worker", "b1"); got != "다른 세션 위임" {
		t.Fatalf("b1 = %q", got)
	}
}

// 위임 글은 세션 묶음이 아니다: 같은 표에 두지만 세션 묶음 읽기에 섞이지 않고, 세션 묶음도 위임으로 잡히지 않는다.
func TestDelegation_세션_묶음과_섞이지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SaveDelegation("h", "S", "worker", "위임 글"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.SessionBundle("h", "S"); ok {
		t.Fatal("위임 글이 세션 묶음으로 읽혔다")
	}
	if err := s.SaveSessionBundle("h", "S", "묶음", []string{"m-1"}); err != nil {
		t.Fatal(err)
	}
	if text, _, ok := s.SessionBundle("h", "S"); !ok || text != "묶음" {
		t.Fatalf("bundle = %q %v", text, ok)
	}
	if at := s.SessionBundleAt("h", "S"); at == 0 {
		t.Fatal("bundle at")
	}
	if got := take(t, s, "S", "worker", "a1"); got != "위임 글" {
		t.Fatalf("take = %q", got)
	}
}

// 위임 글(가져간 것 포함)은 reset(compact·clear)·세션 끝·오래된 세션 상태 정리와 함께 지워진다. 다른 세션 것은 남는다.
func TestDelegation_reset_세션_끝_정리와_함께_지워진다(t *testing.T) {
	s, clk := newTestStore(t)
	seed := func() {
		t.Helper()
		for _, session := range []string{"S", "T"} {
			if err := s.SaveDelegation("h", session, "worker", session+" 가져갈 위임"); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveDelegation("h", session, "worker", session+" 남는 위임"); err != nil {
				t.Fatal(err)
			}
		}
		take(t, s, "S", "worker", "a1")
	}
	seed()
	if err := s.ResetSession("h", "S"); err != nil {
		t.Fatal(err)
	}
	if got := take(t, s, "S", "worker", "a1"); got != "" {
		t.Fatalf("reset 뒤 = %q", got)
	}
	if n := delegationRows(t, s); n != 2 {
		t.Fatalf("rows after reset = %d (T의 둘만 남는다)", n)
	}
	if err := s.DropDelegations("h", "T"); err != nil {
		t.Fatal(err)
	}
	if n := delegationRows(t, s); n != 0 {
		t.Fatalf("rows after drop = %d", n)
	}
	seed()
	clk.t = clk.t.AddDate(0, 0, SessionStateDays+1)
	if err := s.PruneSessionState(SessionStateDays); err != nil {
		t.Fatal(err)
	}
	if n := delegationRows(t, s); n != 0 {
		t.Fatalf("rows after prune = %d", n)
	}
	if err := s.SaveDelegation("h", "", "worker", "세션 없음"); err != nil || delegationRows(t, s) != 0 {
		t.Fatalf("세션 없는 위임은 남기지 않는다: %v", err)
	}
}

// 같은 종류 여럿을 나란히 띄워도 서브에이전트마다 서로 다른 위임 하나씩이다(경합: 여러 연결에서 동시에 가져간다).
func TestDelegation_동시에_가져가도_하나씩_짝이_맞는다(t *testing.T) {
	s, _ := newTestStore(t)
	const n = 12
	for i := range n {
		if err := s.SaveDelegation("h", "S", "worker", fmt.Sprintf("위임 %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	got := make([]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			text, _, err := s.TakeDelegation("h", "S", "worker", fmt.Sprintf("a%02d", i))
			got[i], errs[i] = text, err
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("take %d: %v", i, err)
		}
	}
	slices.Sort(got)
	for i := range n {
		if want := fmt.Sprintf("위임 %02d", i); got[i] != want {
			t.Fatalf("got %v", got)
		}
	}
}
