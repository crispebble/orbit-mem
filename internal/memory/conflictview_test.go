package memory

// 어긋남 표시의 저장 쪽(명세 C-5·C-6·C-2): 열린 어긋남 쌍을 옛 기억 기준으로 한 번에 묶어 읽고, 사용자 말이 어느 쪽이든 닿으면
// 쌍이 풀리고, 자동 대체는 알린 뒤 다시 나오지 않는다.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/place"
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
	if s.AutoNoticeAt(replScope) != 0 {
		t.Fatalf("처음 표시는 0이어야 합니다: %d", s.AutoNoticeAt(replScope))
	}
	at := s.nowMs()
	if err := s.SetAutoNoticeAt(replScope, at); err != nil {
		t.Fatal(err)
	}
	if s.AutoNoticeAt(replScope) != at || s.AutoNoticeAt(otherScope) != 0 {
		t.Fatalf("표시 %d %d", s.AutoNoticeAt(replScope), s.AutoNoticeAt(otherScope))
	}
	if got, _ := s.AutoSupersedesSince(s.AutoNoticeAt(replScope)); len(got) != 0 {
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

const otherScope = "project:github.com/o/other"

func inScope(in SaveInput, scope string) SaveInput { in.Scope = scope; return in }

func TestAutoSupersedesUnnoticed_범위마다_표시점이_따로여서_한_범위가_받아도_다른_범위는_받는다(t *testing.T) {
	s := supStore(t)
	oldA, _ := mustSave(t, s, userFact("빌드는 make all", "빌드 명령"))
	mustSave(t, s, checkedFact("빌드는 go build", "빌드 명령"))
	oldB, _ := mustSave(t, s, inScope(userFact("시험은 make test", "시험 명령"), otherScope))
	mustSave(t, s, inScope(checkedFact("시험은 go test", "시험 명령"), otherScope))
	seesA := func(scope, _ string) bool { return scope == replScope }
	seesB := func(scope, _ string) bool { return scope == otherScope }
	got, err := s.AutoSupersedesUnnoticed(seesA)
	if err != nil || len(got) != 1 || got[0].OldID != oldA.ID || got[0].Scope != replScope {
		t.Fatalf("A 자리: %+v %v", got, err)
	}
	if err := s.SetAutoNoticeAt(replScope, got[0].At); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.AutoSupersedesUnnoticed(seesA); len(again) != 0 {
		t.Fatalf("A가 받은 것이 또 나옵니다: %+v", again)
	}
	// A 자리가 받았어도 B 범위의 대체는 B 자리가 받는다.
	if got, _ := s.AutoSupersedesUnnoticed(seesB); len(got) != 1 || got[0].OldID != oldB.ID {
		t.Fatalf("B 자리: %+v", got)
	}
	// 둘 다 보이는 자리는 아직 받지 않은 B 것만 받는다.
	if got, _ := s.AutoSupersedesUnnoticed(func(string, string) bool { return true }); len(got) != 1 || got[0].OldID != oldB.ID {
		t.Fatalf("둘 다 보이는 자리: %+v", got)
	}
}

func TestAutoSupersedesUnnoticed_자동_대체가_하나도_없으면_표시점을_앞으로_옮겨_원장을_다시_훑지_않는다(t *testing.T) {
	s := supStore(t)
	mustSave(t, s, userFact("빌드는 make all", "빌드 명령"))
	if got, err := s.AutoSupersedesUnnoticed(func(string, string) bool { return true }); err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	if s.Meta(metaAutoNoticeFloor) == "" {
		t.Fatal("읽은 자리 표시가 없습니다")
	}
	// 그 뒤 생긴 자동 대체는 그대로 받는다.
	mustSave(t, s, checkedFact("빌드는 go build", "빌드 명령"))
	if got, _ := s.AutoSupersedesUnnoticed(func(string, string) bool { return true }); len(got) != 1 {
		t.Fatalf("표시점 뒤의 대체를 놓쳤습니다: %+v", got)
	}
}

func TestOpenConflictCount_옛_쪽이_낡음이_되면_세지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := conflictPair(t, s, "배포 요일")
	if n, _ := s.OpenConflictCount(); n != 1 {
		t.Fatalf("개수 %d", n)
	}
	if _, err := s.SetState(old.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.OpenConflictCount(); n != 0 {
		t.Fatalf("옛 쪽이 낡은 뒤에도 셉니다: %d", n)
	}
	if got, _ := s.OpenConflictsOf([]string{old.ID}); len(got) != 0 {
		t.Fatalf("옛 쪽이 낡은 뒤에도 열려 있습니다: %v", got)
	}
}

// 어긋남 줄에 실을 수 있는 새 쪽: 현재 값이거나 옛 쪽이 머리인 같은 주제의 옛 버전뿐이다. 만료·다른 머리에 가려진 것·보이지 않는
// 범위는 싣지 않는다.
func TestShownSides_새_쪽이_보일_수_있을_때만_준다(t *testing.T) {
	s := supStore(t)
	base := s.Now()
	s.Now = func() time.Time { return base }
	old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
	flag := func(nw Memory) {
		t.Helper()
		if _, err := s.FlagConflict(nw.ID, old.ID, "시험", Who{}); err != nil {
			t.Fatal(err)
		}
	}
	ai := func(in SaveInput) Memory {
		t.Helper()
		in.Who = Who{Actor: ActorAI}
		in.Source = Source{Kind: SourceSession, Ref: "s"}
		m, _, err := s.SaveWith(in)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	same := ai(SaveInput{Kind: KindFact, Title: "같은 주제의 새 사실", Topic: "배포 요일", Scope: replScope}) // 옛 쪽이 머리인 주제의 후보
	flag(same)
	volatile := ai(SaveInput{Kind: KindFact, Title: "하루짜리 사실", Scope: replScope, Retention: RetentionVolatile, TTLDays: 1})
	flag(volatile)
	hiddenElsewhere := ai(SaveInput{Kind: KindFact, Title: "다른 머리에 가려진 사실", Topic: "다른 주제", Scope: replScope})
	flag(hiddenElsewhere)
	ai2 := SaveInput{Kind: KindFact, Title: "다른 주제의 확정 사실", Topic: "다른 주제", Scope: replScope, Who: Who{Actor: ActorUser},
		Source: Source{Kind: SourceQuote, Excerpt: "다른 주제의 확정 사실"}}
	mustSave(t, s, ai2) // 「다른 주제」의 머리: hiddenElsewhere를 가린다
	foreign := ai(SaveInput{Kind: KindFact, Title: "다른 프로젝트의 후보", Scope: otherScope})
	flag(foreign)
	ids := []string{old.ID}
	title := func(ms []Memory) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Title)
		}
		return out
	}
	vw, err := s.Viewer()
	if err != nil {
		t.Fatal(err)
	}
	vw, err = vw.WithConflicts(ids)
	if err != nil {
		t.Fatal(err)
	}
	here := place.Resolve(replScope, nil)
	got := title(vw.ShownSides(old.ID, &here))
	slices.Sort(got)
	want := []string{"같은 주제의 새 사실", "하루짜리 사실"}
	if !slices.Equal(got, want) {
		t.Fatalf("지금 자리에서 보이는 새 쪽 %v, want %v", got, want)
	}
	// 열흘 뒤: 하루짜리 사실은 만료돼 빠진다.
	s.Now = func() time.Time { return base.Add(10 * 24 * time.Hour) }
	vw, _ = s.Viewer()
	vw, _ = vw.WithConflicts(ids)
	if got := title(vw.ShownSides(old.ID, &here)); !slices.Equal(got, []string{"같은 주제의 새 사실"}) {
		t.Fatalf("열흘 뒤 %v", got)
	}
	// 범위를 가리지 않는 읽기(memory_get)는 다른 프로젝트 후보도 현재 값이면 준다.
	if got := title(vw.ShownSides(old.ID, nil)); !slices.Contains(got, "다른 프로젝트의 후보") {
		t.Fatalf("범위 없는 읽기 %v", got)
	}
}

// 사용자 말이 새 쪽에 붙으면 사용자가 고른 값이 현재 값이 된다: 새 쪽은 확정되고 옛 쪽은 대체로 기록된다.
func TestConflict_새_쪽에_사용자_말이_붙으면_새_쪽이_확정되고_옛_쪽은_대체로_기록된다(t *testing.T) {
	s := supStore(t)
	old, nw := conflictPair(t, s, "배포 요일")
	body := "사용자가 정한 본문"
	if _, err := s.Update(UpdateInput{ID: nw.ID, Body: &body, Who: Who{Actor: ActorAI, UserQuote: "화요일이 맞아"},
		Sources: []Source{{Kind: SourceQuote, Excerpt: "화요일이 맞아"}}}); err != nil {
		t.Fatal(err)
	}
	o, n := stateOf(t, s, old.ID), stateOf(t, s, nw.ID)
	if o.State != StateStale || o.SupersededBy != nw.ID || n.State != StateConfirmed {
		t.Fatalf("old %s→%q, new %s", o.State, o.SupersededBy, n.State)
	}
	h, err := s.History(old.ID)
	if err != nil || !strings.Contains(h[len(h)-1].Memory.StaleReason, "사용자 말") {
		t.Fatalf("버전 사유 %+v %v", h, err)
	}
	var by string
	var at int64
	if err := s.db.QueryRow(`SELECT resolved_by, resolved_at FROM audit_flags WHERE kind=? AND memory_id=? AND other_id=?`, FlagConflict, nw.ID, old.ID).Scan(&by, &at); err != nil || by != ActorUser || at == 0 {
		t.Fatalf("닫은 사람 %q %d %v", by, at, err)
	}
}

// 사용자 말이 옛 쪽에 붙으면 옛 쪽이 그대로 현재 값이고 새 쪽은 후보로 그대로다.
func TestConflict_옛_쪽에_사용자_말이_붙으면_옛_쪽이_현재_값이고_새_쪽은_후보로_남는다(t *testing.T) {
	s := supStore(t)
	old, nw := conflictPair(t, s, "배포 요일")
	body := "사용자가 다시 확인한 본문"
	if _, err := s.Update(UpdateInput{ID: old.ID, Body: &body, Who: Who{Actor: ActorAI, UserQuote: "금요일이 맞아"},
		Sources: []Source{{Kind: SourceQuote, Excerpt: "금요일이 맞아"}}}); err != nil {
		t.Fatal(err)
	}
	if o, n := stateOf(t, s, old.ID), stateOf(t, s, nw.ID); o.State != StateConfirmed || n.State != StateCandidate {
		t.Fatalf("old %s new %s", o.State, n.State)
	}
	var by string
	if err := s.db.QueryRow(`SELECT resolved_by FROM audit_flags WHERE kind=? AND memory_id=? AND other_id=? AND resolved_at>0`, FlagConflict, nw.ID, old.ID).Scan(&by); err != nil || by != ActorUser {
		t.Fatalf("닫은 사람 %q %v", by, err)
	}
}

// 같은 제목으로 다시 저장해 사용자 말 출처만 더해지는 길에서도 두 갈래가 맞게 동작한다.
func TestConflict_같은_제목_재저장으로_사용자_말만_더해져도_두_갈래가_맞게_동작한다(t *testing.T) {
	resave := func(m Memory) SaveInput {
		return SaveInput{Kind: m.Kind, Title: m.Title, Topic: m.Topic, Scope: m.Scope, Who: Who{Actor: ActorAI},
			Source: Source{Kind: SourceQuote, Excerpt: "그게 맞아"}}
	}
	t.Run("새 쪽", func(t *testing.T) {
		s := supStore(t)
		old, nw := conflictPair(t, s, "배포 요일")
		if _, _, err := s.SaveWith(resave(nw)); err != nil {
			t.Fatal(err)
		}
		if o, n := stateOf(t, s, old.ID), stateOf(t, s, nw.ID); o.State != StateStale || o.SupersededBy != nw.ID || n.State != StateConfirmed {
			t.Fatalf("old %s new %s", o.State, n.State)
		}
	})
	t.Run("옛 쪽", func(t *testing.T) {
		s := supStore(t)
		old, nw := conflictPair(t, s, "배포 요일")
		if _, _, err := s.SaveWith(resave(old)); err != nil {
			t.Fatal(err)
		}
		if o, n := stateOf(t, s, old.ID), stateOf(t, s, nw.ID); o.State != StateConfirmed || n.State != StateCandidate {
			t.Fatalf("old %s new %s", o.State, n.State)
		}
		if got, _ := s.OpenConflictsOf([]string{old.ID}); len(got) != 0 {
			t.Fatalf("풀리지 않았습니다: %v", got)
		}
	})
}

// 사용자가 아닌 출처의 고침은 어긋남을 풀지도 확정하지도 않는다.
func TestConflict_사용자_말이_아닌_출처가_더해져도_어긋남은_그대로다(t *testing.T) {
	s := supStore(t)
	old, nw := conflictPair(t, s, "배포 요일")
	body := "다듬은 본문"
	if _, err := s.Update(UpdateInput{ID: nw.ID, Body: &body, Who: Who{Actor: ActorAI}, Sources: []Source{{Kind: SourceSession, Ref: "s2"}}}); err != nil {
		t.Fatal(err)
	}
	if stateOf(t, s, nw.ID).State != StateCandidate || stateOf(t, s, old.ID).State != StateConfirmed || !hasConflictFlag(t, s, nw.ID, old.ID) {
		t.Fatal("어긋남이 달라졌습니다")
	}
}

// 자동 대체(O→N) 뒤 다른 기억 N2가 N을 대체하고 첫 대체를 되돌리면, O는 확정으로 돌아오지만 주제 머리는 N2다: 그 둘 사이에
// 어긋남을 열어 감사 목록에 보이게 한다.
func TestRevert_되돌린_옛_기억이_다른_현재_값에_가려지면_둘_사이에_어긋남이_열린다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드는 make all", "빌드 명령"))
	cur, _ := mustSave(t, s, checkedFact("빌드는 go build", "빌드 명령"))
	n2, _ := mustSave(t, s, userFact("빌드는 task build", "빌드 명령")) // 사용자 말이 cur를 대체한다
	if stateOf(t, s, cur.ID).State != StateStale {
		t.Fatalf("cur %s", stateOf(t, s, cur.ID).State)
	}
	revertAuto(t, s, old.ID)
	if stateOf(t, s, old.ID).State != StateConfirmed {
		t.Fatalf("old %s", stateOf(t, s, old.ID).State)
	}
	if !hasConflictFlag(t, s, n2.ID, old.ID) {
		t.Fatal("가려진 옛 기억과 현재 값 사이에 어긋남이 없습니다")
	}
	// 가릴 현재 값이 없는 되돌림(옛 기억이 머리로 돌아옴)에는 열지 않는다.
	s2 := supStore(t)
	o2, _ := mustSave(t, s2, userFact("시험은 make test", "시험 명령"))
	mustSave(t, s2, checkedFact("시험은 go test", "시험 명령"))
	revertAuto(t, s2, o2.ID)
	if flags, _ := s2.OpenFlags(); len(flags) != 0 {
		t.Fatalf("열 필요가 없는데 열렸습니다: %+v", flags)
	}
}
