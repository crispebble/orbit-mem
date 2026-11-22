package memory

// 어긋남 표시의 저장 쪽(명세 C-5·C-6·C-2): 열린 어긋남 쌍을 옛 기억 기준으로 한 번에 묶어 읽고, 사용자 말이 어느 쪽이든 닿으면
// 쌍이 풀리고, 자동 대체는 알린 뒤 다시 나오지 않는다.

import (
	"slices"
	"testing"
)

// conflictPair makes an old user fact and a session-learned candidate on the same topic: the save leaves them as a conflict.
func conflictPair(t *testing.T, s *Store, topic string) (old, nw Memory) {
	t.Helper()
	old, _ = mustSave(t, s, userFact("배포는 금요일에 한다 "+topic, topic))
	nw, rep := mustSave(t, s, SaveInput{Kind: KindFact, Title: "배포는 화요일에 한다 " + topic, Topic: topic, Scope: replScope,
		Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	if !slices.Contains(rep.Conflicts, old.ID) || !hasConflictFlag(t, s, nw.ID, old.ID) {
		t.Fatalf("어긋남이 남지 않았습니다: %+v", rep)
	}
	return old, nw
}

func TestOpenConflictsOf_옛_기억_기준으로_새_쪽을_한_번에_묶어_준다(t *testing.T) {
	s := supStore(t)
	old1, new1 := conflictPair(t, s, "배포 요일")
	old2, new2 := conflictPair(t, s, "점검 요일")
	other, _ := mustSave(t, s, userFact("무관한 사실", "무관"))
	got, err := s.OpenConflictsOf([]string{old1.ID, old2.ID, other.ID, "m-없음"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !slices.Equal(got[old1.ID], []string{new1.ID}) || !slices.Equal(got[old2.ID], []string{new2.ID}) {
		t.Fatalf("got %v", got)
	}
	if got, _ := s.OpenConflictsOf(nil); len(got) != 0 {
		t.Fatalf("빈 입력에 %v", got)
	}
	all, err := s.OpenConflicts()
	if err != nil || len(all) != 2 {
		t.Fatalf("전체 %v %v", all, err)
	}
	if n, err := s.OpenConflictCount(); err != nil || n != 2 {
		t.Fatalf("개수 %d %v", n, err)
	}
}

func TestOpenConflictsOf_새_쪽이_더_이상_살아_있지_않으면_빠진다(t *testing.T) {
	s := supStore(t)
	old, nw := conflictPair(t, s, "배포 요일")
	if _, err := s.SetState(nw.ID, StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.OpenConflictsOf([]string{old.ID}); len(got) != 0 {
		t.Fatalf("거절한 새 쪽이 남았습니다: %v", got)
	}
	if n, _ := s.OpenConflictCount(); n != 0 {
		t.Fatalf("개수 %d", n)
	}
}

func TestViewer_WithConflicts는_현재_값의_Conflicts만_채우고_DB를_더_읽지_않는다(t *testing.T) {
	s := supStore(t)
	old, nw := conflictPair(t, s, "배포 요일")
	vw, err := s.Viewer()
	if err != nil {
		t.Fatal(err)
	}
	if v := vw.Of(old); len(v.Conflicts) != 0 {
		t.Fatalf("WithConflicts 전에는 채우지 않는다: %v", v.Conflicts)
	}
	vw, err = vw.WithConflicts([]string{old.ID, nw.ID})
	if err != nil {
		t.Fatal(err)
	}
	if v := vw.Of(old); !slices.Equal(v.Conflicts, []string{nw.ID}) || !v.Current() {
		t.Fatalf("old %+v", v)
	}
	if v := vw.Of(nw); len(v.Conflicts) != 0 {
		t.Fatalf("새 쪽에는 붙지 않는다: %+v", v)
	}
}

func TestConflict_사용자_말로_저장하면_어긋남이_풀린다(t *testing.T) {
	// 옛 쪽을 사용자 말로 다시 저장·고치면 풀린다.
	for _, side := range []string{"옛 쪽을 고침", "새 쪽을 고침"} {
		t.Run(side, func(t *testing.T) {
			s := supStore(t)
			old, nw := conflictPair(t, s, "배포 요일")
			target := old
			if side == "새 쪽을 고침" {
				target = nw
			}
			body := "사용자가 직접 정정한 본문"
			if _, err := s.Update(UpdateInput{ID: target.ID, Body: &body, Who: Who{Actor: ActorAI, UserQuote: "그건 이렇게 고쳐 줘"},
				Sources: []Source{{Kind: SourceQuote, Excerpt: "그건 이렇게 고쳐 줘"}}}); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.OpenConflictsOf([]string{old.ID}); len(got) != 0 {
				t.Fatalf("풀리지 않았습니다: %v", got)
			}
			if hasConflictFlag(t, s, nw.ID, old.ID) {
				t.Fatal("열린 표시가 남았습니다")
			}
		})
	}
	// 사용자 말이 아닌 고침(AI의 세션 출처)은 풀지 않는다.
	s := supStore(t)
	old, nw := conflictPair(t, s, "배포 요일")
	body := "AI가 다듬은 본문"
	if _, err := s.Update(UpdateInput{ID: nw.ID, Body: &body, Who: Who{Actor: ActorAI}, Sources: []Source{{Kind: SourceSession, Ref: "s2"}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.OpenConflictsOf([]string{old.ID}); !slices.Equal(got[old.ID], []string{nw.ID}) {
		t.Fatalf("AI의 고침이 풀었습니다: %v", got)
	}
}

func TestAutoSupersedesSince는_알림_표시_뒤의_자동_대체만_주고_되돌린_것은_뺀다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드는 make all", "빌드 명령"))
	cur, _ := mustSave(t, s, checkedFact("빌드는 go build", "빌드 명령"))
	if got, err := s.AutoSupersedesSince(0); err != nil || len(got) != 1 || got[0].OldID != old.ID || got[0].NewID != cur.ID ||
		got[0].OldTitle != "빌드는 make all" || got[0].NewTitle != "빌드는 go build" {
		t.Fatalf("got %+v %v", got, err)
	}
	if s.AutoNoticeAt() != 0 {
		t.Fatalf("처음 표시는 0이어야 합니다: %d", s.AutoNoticeAt())
	}
	at := s.nowMs()
	if err := s.SetAutoNoticeAt(at); err != nil {
		t.Fatal(err)
	}
	if s.AutoNoticeAt() != at {
		t.Fatalf("표시 %d", s.AutoNoticeAt())
	}
	if got, _ := s.AutoSupersedesSince(s.AutoNoticeAt()); len(got) != 0 {
		t.Fatalf("알린 것이 또 나옵니다: %+v", got)
	}
	// 알리기 전에 사람이 되돌린 대체는 알릴 것이 아니다.
	old2, _ := mustSave(t, s, userFact("시험은 make test", "시험 명령"))
	mustSave(t, s, checkedFact("시험은 go test", "시험 명령"))
	lines := autoReplaceLines(t, s, old2.ID)
	if len(lines) != 1 {
		t.Fatalf("원장 %d", len(lines))
	}
	if _, err := s.RevertChange(lines[0].ID, Who{}, "되돌림"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AutoSupersedesSince(0); len(got) != 1 || got[0].OldID != old.ID {
		t.Fatalf("되돌린 대체가 남았습니다: %+v", got)
	}
}
