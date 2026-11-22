package memory

import (
	"slices"
	"testing"
)

// 되돌린 자동 대체 쌍(supersede_reverts)과 지우기: 어느 쪽을 지워도 쌍 줄은 id만 남는다. 옛 기억을 지우면 그 줄이 새 기억의
// 자동 확정을 계속 막고, 새 기억을 지우면 그 줄이 같은 옛 기억의 자동 대체를 계속 막는다(사람이 되돌린 판단은 어느 쪽이
// 없어져도 그대로다).
func TestForget_되돌린_대체_쌍은_어느_쪽을_지워도_남는다(t *testing.T) {
	pair := func(t *testing.T) (*Store, Memory, Memory) {
		s := supStore(t)
		s.Policy.AutoMode = true // 되돌릴 자동 대체를 만든다
		old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
		cur, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
		revertAuto(t, s, old.ID)
		return s, old, cur
	}
	count := func(t *testing.T, s *Store, col, id string) int {
		t.Helper()
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM supersede_reverts WHERE `+col+`=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	t.Run("새 기억을 지움", func(t *testing.T) {
		s, old, cur := pair(t)
		if _, err := s.Forget(ForgetInput{IDs: []string{cur.ID}}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, s, "new_id", cur.ID); n != 1 {
			t.Fatalf("쌍 줄 %d", n)
		}
		if got := stateOf(t, s, old.ID); got.State != StateConfirmed {
			t.Fatalf("old %+v", got)
		}
	})
	// 되돌린 옛 기억은 새 기억을 지운 뒤에도 다시 자동 대체하지 않는다: 같은 주제의 제목 다른 재확인 사실이 와도 어긋남이다.
	// 남은 줄은 관계없는 기억에는 걸리지 않는다(다른 주제의 사용자 말 사실은 그대로 자동 대체된다).
	t.Run("새 기억을 지운 뒤 같은 주제의 다른 재확인 사실", func(t *testing.T) {
		s, old, cur := pair(t)
		other, _ := mustSave(t, s, userFact("배포는 화요일", "배포 요일"))
		if _, err := s.Forget(ForgetInput{IDs: []string{cur.ID}}); err != nil {
			t.Fatal(err)
		}
		next, rep := mustSave(t, s, checkedFact("빌드 명령은 go vet", "빌드 명령"))
		if len(rep.Superseded) != 0 || !slices.Contains(rep.Conflicts, old.ID) {
			t.Fatalf("저장 결과 %+v", rep)
		}
		if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
			t.Fatalf("old %+v", got)
		}
		if why, err := s.confirmBlocked(next.ID); why == blockRevertedReplace || err != nil { // 어긋남 표시는 저장이 달았다
			t.Fatalf("남은 쌍 줄이 다른 새 기억을 막았다 %q %v", why, err)
		}
		if why, err := s.confirmBlocked(other.ID); why != "" || err != nil {
			t.Fatalf("관계없는 기억이 막혔다 %q %v", why, err)
		}
		swap, rep := mustSave(t, s, checkedFact("배포는 목요일", "배포 요일"))
		if !slices.Contains(rep.Superseded, other.ID) || stateOf(t, s, swap.ID).State != StateConfirmed {
			t.Fatalf("다른 주제는 그대로 자동 대체돼야 한다 %+v", rep)
		}
	})
	t.Run("옛 기억을 지움", func(t *testing.T) {
		s, old, cur := pair(t)
		if _, err := s.Forget(ForgetInput{IDs: []string{old.ID}}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, s, "old_id", old.ID); n != 1 {
			t.Fatalf("쌍 줄 %d", n)
		}
		if why, err := s.confirmBlocked(cur.ID); why != blockRevertedReplace || err != nil {
			t.Fatalf("막는 까닭 %q %v", why, err)
		}
	})
}
