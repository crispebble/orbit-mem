package memory

// 자동 모드가 꺼진 기본 상태에서는 서버가 다시 확인한 검증 사실도 사용자 말 사실을 대체하지 않는다: 어긋남으로 남기고
// 사용자가 옛 말을 유지할지 새 사실로 바꿀지 고른다. 자동 모드를 켜면 같은 범위·주제의 사용자 말 사실을 자동 대체한다.

import (
	"slices"
	"testing"
)

// 사용자 말 사실 + 같은 범위·주제의 다시 확인된 검증 사실. 저장 한 번 안에서 어긋남이 열린 뒤에 즉시 확정을 판정하므로(store.go
// 의 FlagConflict → ConfirmByEvidence) 새 사실은 후보로 남고 옛 사실이 주제의 현재 값으로 남는다.
func TestSave_자동_모드_끔이면_다시_확인된_검증_사실은_사용자_말_사실을_대체하지_않고_어긋남으로_남는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	cur, rep := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	got := stateOf(t, s, old.ID)
	if got.State != StateConfirmed || got.Version != old.Version || got.SupersededBy != "" {
		t.Fatalf("옛 사실이 바뀌었다: %+v", got)
	}
	if !slices.Contains(rep.Conflicts, old.ID) || len(rep.Superseded) != 0 {
		t.Fatalf("저장 보고 %+v", rep)
	}
	if c := stateOf(t, s, cur.ID); c.State != StateCandidate {
		t.Fatalf("새 사실이 스스로 확정돼 현재 값을 빼앗았다: %s", c.State)
	}
	if !hasConflictFlag(t, s, cur.ID, old.ID) {
		t.Fatal("어긋남 표시가 열리지 않았다")
	}
	if n := len(autoReplaceLines(t, s, old.ID)); n != 0 {
		t.Fatalf("자동 대체 원장 %d줄", n)
	}
	// 저장 하나의 자동 모드 끔은 저장소의 켬보다 앞선다.
	s.Policy.AutoMode = true
	in := checkedFact("빌드 명령은 go build -race", "빌드 명령")
	in.AutoMode = ptr(false)
	if _, rep := mustSave(t, s, in); !slices.Contains(rep.Conflicts, old.ID) || stateOf(t, s, old.ID).State != StateConfirmed {
		t.Fatalf("저장의 끔이 무시됨 %+v", rep)
	}
}

// 어긋남 뒤 사용자의 두 갈래를 한 흐름으로 잇는다: (가) 옛 말을 다시 하면 옛 값이 남고 새 후보는 스스로 확정되지 않는다.
// (나) 새 값을 말하면 새 사실이 확정되고 옛 사실이 대체된다.
func TestSettle_다시_확인된_사실과_어긋난_뒤_사용자가_고른_쪽이_이긴다(t *testing.T) {
	t.Run("가_사용자가_옛_말을_유지한다", func(t *testing.T) {
		s := supStore(t)
		old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
		cur, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
		if stateOf(t, s, cur.ID).State != StateCandidate || !hasConflictFlag(t, s, cur.ID, old.ID) {
			t.Fatal("어긋남이 열리지 않았다")
		}
		quoteUpdate(t, s, old.ID)
		if at, res, ok := flagOf(t, s, cur.ID, old.ID); !ok || at == 0 || res != resolutionKeepOld {
			t.Fatalf("닫힌 표시 %d %q %v", at, res, ok)
		}
		if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
			t.Fatalf("옛 값이 바뀌었다: %+v", got)
		}
		// 같은 새 사실이 다시 확인돼 저장돼도 스스로 확정해 옛 값을 가리지 않는다.
		again := checkedFact("빌드 명령은 go build", "빌드 명령")
		again.Session = "s-2"
		if m, _ := mustSave(t, s, again); m.ID != cur.ID || m.State != StateCandidate {
			t.Fatalf("재저장 %+v", m)
		}
		if why, err := s.confirmBlocked(cur.ID); err != nil || why != blockKeptOld {
			t.Fatalf("confirmBlocked %q %v", why, err)
		}
		if n := len(autoReplaceLines(t, s, old.ID)); n != 0 {
			t.Fatalf("자동 대체 원장 %d줄", n)
		}
	})
	t.Run("나_사용자가_새_값으로_바꾼다", func(t *testing.T) {
		s := supStore(t)
		old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
		cur, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
		quoteUpdate(t, s, cur.ID)
		if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != cur.ID {
			t.Fatalf("옛 사실 %+v", got)
		}
		if got := stateOf(t, s, cur.ID); got.State != StateConfirmed {
			t.Fatalf("새 사실 %+v", got)
		}
		h, _ := s.History(old.ID)
		if last := h[len(h)-1]; last.Action != "supersede" {
			t.Fatalf("옛 사실의 마지막 이력 %+v", last)
		}
		if hasConflictFlag(t, s, cur.ID, old.ID) {
			t.Fatal("어긋남 표시가 닫히지 않았다")
		}
		// 사용자가 고른 대체는 자동 변경이 아니다.
		if n := len(autoReplaceLines(t, s, old.ID)); n != 0 {
			t.Fatalf("자동 대체 원장 %d줄", n)
		}
	})
}

// 자동 모드를 켜면 다시 확인된 검증 사실과 근거만 적힌 검증 사실 모두 같은 범위·주제의 사용자 말 사실을 자동 대체하고 원장에 남는다.
func TestSave_자동_모드_켬이면_다시_확인된_사실도_근거_적힌_사실도_자동_대체하고_원장에_남는다(t *testing.T) {
	for _, c := range []struct {
		name string
		in   func(string, string) SaveInput
	}{{"다시 확인된 사실", checkedFact}, {"근거 적힌 사실", statedFact}} {
		t.Run(c.name, func(t *testing.T) {
			s := supStore(t)
			s.Policy.AutoMode = true
			old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
			cur, rep := mustSave(t, s, c.in("빌드 명령은 go build", "빌드 명령"))
			if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != cur.ID || !slices.Contains(rep.Superseded, old.ID) || len(rep.Conflicts) != 0 {
				t.Fatalf("old %+v rep %+v", got, rep)
			}
			if n := len(autoReplaceLines(t, s, old.ID)); n != 1 {
				t.Fatalf("자동 대체 원장 %d줄", n)
			}
			if hasConflictFlag(t, s, cur.ID, old.ID) {
				t.Fatal("자동 대체에 어긋남 표시가 붙었다")
			}
		})
	}
}

// 자동 모드를 켜도 user 범위 사실, 규칙·결정, 다른 범위·주제의 사실은 자동 대체하지 않는다.
func TestSave_자동_모드_켬이어도_user_범위_규칙_결정_다른_자리는_자동_대체하지_않는다(t *testing.T) {
	for _, src := range []func(string, string) SaveInput{checkedFact, statedFact} {
		s := supStore(t)
		s.Policy.AutoMode = true
		// user 범위 사실
		userOld, _ := mustSave(t, s, userSays(KindFact, "주말에는 일하지 않는다", "주말 일정"))
		g := src("주말에도 일한다", "주말 일정")
		g.Scope = ScopeGlobal
		mustSave(t, s, g)
		// 규칙·결정
		ruleIn := userSays(KindRule, "배포 전에 점검표를 본다", "배포 점검")
		ruleIn.Scope = replScope
		rule, _ := mustSave(t, s, ruleIn)
		nr := src("배포 점검표는 없다", "배포 점검")
		mustSave(t, s, nr)
		// 다른 주제의 사실을 supersedes로 지목
		other, _ := mustSave(t, s, userFact("점검은 월요일에 한다", "점검 요일"))
		ex := src("배포는 화요일에 한다", "배포 요일")
		ex.Supersedes = []string{other.ID}
		mustSave(t, s, ex)
		for _, id := range []string{userOld.ID, rule.ID, other.ID} {
			if got := stateOf(t, s, id); got.State != StateConfirmed || got.SupersededBy != "" {
				t.Fatalf("자동 모드 켬에서 %s가 대체됐다: %+v", got.Title, got)
			}
			if n := len(autoReplaceLines(t, s, id)); n != 0 {
				t.Fatalf("%s: 자동 대체 원장 %d줄", id, n)
			}
		}
	}
}

// 어긋남 표시를 쓰지 못해도(쓰기 실패) 새 사실은 즉시 확정돼 주제의 현재 값을 빼앗지 않는다: 이 저장에서 어긋남이 생겼으면
// 즉시 확정을 시도하지 않는다.
func TestSave_어긋남_표시를_쓰지_못해도_새_사실이_즉시_확정되지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	if _, err := s.DB().Exec(`CREATE TRIGGER audit_flags_block BEFORE INSERT ON audit_flags BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	cur, rep := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	if !slices.Contains(rep.Conflicts, old.ID) {
		t.Fatalf("어긋남이 보고되지 않았다 %+v", rep)
	}
	if c := stateOf(t, s, cur.ID); c.State != StateCandidate {
		t.Fatalf("새 사실이 확정돼 현재 값을 빼앗았다: %s", c.State)
	}
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("옛 사실이 바뀌었다: %+v", got)
	}
}

// 사용자가 옛 사실을 유지하기로 한 뒤에는 자동 모드를 켜고 다시 확인된 검증 사실을 저장해도 그 옛 사실은 자동 대체되지 않는다.
func TestSave_옛_사실을_유지하기로_한_뒤에는_자동_모드_켬이어도_대체하지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	cur, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	quoteUpdate(t, s, old.ID)
	if _, res, ok := flagOf(t, s, cur.ID, old.ID); !ok || res != resolutionKeepOld {
		t.Fatalf("유지 표시가 없다 %q %v", res, ok)
	}
	s.Policy.AutoMode = true
	next, rep := mustSave(t, s, checkedFact("빌드 명령은 go build -race", "빌드 명령"))
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("유지하기로 한 옛 사실이 대체됐다: %+v (저장 %+v, 새 %s)", got, rep, next.ID)
	}
	if n := len(autoReplaceLines(t, s, old.ID)); n != 0 {
		t.Fatalf("자동 대체 원장 %d줄", n)
	}
}
