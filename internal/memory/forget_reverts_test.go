package memory

import "testing"

// 되돌린 자동 대체 쌍(supersede_reverts)과 지우기: 새 기억을 지우면 그 쌍 줄도 지운다(막을 기억이 없다). 옛 기억을 지우면 쌍
// 줄은 id만 남아 새 기억의 자동 확정을 계속 막는다(사람이 되돌린 판단은 옛 기억이 없어져도 그대로다).
func TestForget_되돌린_대체_쌍은_새_기억을_지우면_빠지고_옛_기억을_지우면_남는다(t *testing.T) {
	pair := func(t *testing.T) (*Store, Memory, Memory) {
		s := supStore(t)
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
		if n := count(t, s, "new_id", cur.ID); n != 0 {
			t.Fatalf("쌍 줄 %d", n)
		}
		if got := stateOf(t, s, old.ID); got.State != StateConfirmed {
			t.Fatalf("old %+v", got)
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
