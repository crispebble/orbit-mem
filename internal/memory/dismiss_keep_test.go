package memory

// 자동 모드가 꺼져 있으면 사용자 말로 확정된 사실은 어떤 길로도 스스로 대체되지 않는다: 같은 자리(범위·주제)에 들어온 새
// 후보는 어긋남 표시가 열려 있든, 사람이 닫았든(dismiss = 옛 값 유지), 표시를 쓰지 못했든 스스로 확정해 주제의 현재 값을
// 가져가지 않는다. 새 값으로 바꾸는 것은 사용자 말로 새 쪽을 고를 때뿐이고, 그때 쌍은 한 상태로 닫힌다.

import (
	"testing"
	"time"
)

// openConflictID is the id of the open conflict mark of the pair (newID, oldID).
func openConflictID(t *testing.T, s *Store, newID, oldID string) string {
	t.Helper()
	flags, err := s.OpenFlags()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range flags {
		if f.Kind == FlagConflict && f.MemoryID == newID && f.OtherID == oldID {
			return f.ID
		}
	}
	t.Fatalf("열린 어긋남 표시가 없습니다: %s → %s", newID, oldID)
	return ""
}

// stillCurrent fails unless old is confirmed, not superseded, and the current value of its topic.
func stillCurrent(t *testing.T, s *Store, oldID string) {
	t.Helper()
	got := stateOf(t, s, oldID)
	if got.State != StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("사용자 말 사실이 바뀌었습니다: %+v", got)
	}
	v, err := s.Viewer()
	if err != nil {
		t.Fatal(err)
	}
	if why := v.Of(got).Why; why != WhyCurrent {
		t.Fatalf("사용자 말 사실이 주제의 현재 값이 아닙니다: %s", why)
	}
}

// talkFact is a fact an AI learned in a conversation, in the replacement scope, saved in session.
func talkFact(title, topic, session string) SaveInput {
	return SaveInput{Kind: KindFact, Title: title, Topic: topic, Scope: replScope, Who: Who{Actor: ActorAI},
		Source: Source{Kind: SourceSession, Ref: "s"}, Session: session}
}

// 재현 (a): 사용자 말 사실과 같은 자리에 대화로 배운 후보가 와 어긋남이 열리고, 사람이 그 어긋남을 닫은(dismiss) 뒤 다른 날 다른
// 세션에서 같은 제목이 다시 저장돼도 새 후보는 스스로 확정되지 않고 옛 사실이 현재 값으로 남는다.
func TestDismiss_같은_자리_어긋남을_닫은_뒤_다른_세션에_다시_저장해도_새_후보는_스스로_확정되지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
	nin := talkFact("배포는 화요일에 한다", "배포 요일", "a")
	nw, _ := mustSave(t, s, nin)
	if err := s.ResolveFlag(openConflictID(t, s, nw.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"b", "c"} { // 다른 날·다른 세션: 증거 ①이 차는 조건
		c.t = c.t.Add(24 * time.Hour)
		nin.Session = session
		if again, _ := mustSave(t, s, nin); again.ID != nw.ID || again.State != StateCandidate {
			t.Fatalf("세션 %s: 닫은 어긋남의 새 후보가 스스로 확정됐습니다: %+v", session, again)
		}
	}
	if why, err := s.confirmBlockedUnder(nw.ID, false); err != nil || why != blockUserValue {
		t.Fatalf("confirmBlocked %q %v", why, err)
	}
	stillCurrent(t, s, old.ID)
}

// 재현 (b): 서버가 근거를 다시 확인한 검증 사실 후보도, 사람이 어긋남을 닫은 뒤 하루 정리(ConfirmCandidates)가 확정하지 않는다.
func TestDismiss_다시_확인된_검증_사실의_어긋남을_닫아도_하루_정리가_확정하지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	cur, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	if err := s.ResolveFlag(openConflictID(t, s, cur.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	if ev, err := s.EvidenceFor(cur.ID); err != nil || ev != EvidenceVerifiedFact {
		t.Fatalf("증거가 있어야 이 시험이 뜻이 있습니다: %q %v", ev, err)
	}
	n, err := s.ConfirmCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || stateNow(t, s, cur.ID) != StateCandidate {
		t.Fatalf("하루 정리가 닫은 어긋남의 새 후보를 확정했습니다: %d %s", n, stateNow(t, s, cur.ID))
	}
	stillCurrent(t, s, old.ID)
}

// N1: 어긋남 표시가 없어도(저장 때 표시 쓰기가 실패했거나 표시가 사라짐) 같은 자리 사용자 말 사실이 살아 있으면 같은 제목 재저장도
// 하루 정리도 새 후보를 확정하지 않는다. 표시의 유무와 무관하게 막는다.
func TestDismiss_어긋남_표시가_없어도_같은_자리_사용자_말_사실이_있으면_스스로_확정하지_않는다(t *testing.T) {
	t.Run("같은_제목_재저장", func(t *testing.T) {
		s, c := newTestStore(t)
		old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
		if _, err := s.DB().Exec(`CREATE TRIGGER audit_flags_block BEFORE INSERT ON audit_flags BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
			t.Fatal(err)
		}
		nin := talkFact("배포는 화요일에 한다", "배포 요일", "a")
		nw, _ := mustSave(t, s, nin)
		if _, err := s.DB().Exec(`DROP TRIGGER audit_flags_block`); err != nil {
			t.Fatal(err)
		}
		if flags, _ := s.OpenFlags(); len(flags) != 0 {
			t.Fatalf("표시가 없는 상태를 만들지 못했습니다: %+v", flags)
		}
		for _, session := range []string{"b", "c"} {
			c.t = c.t.Add(24 * time.Hour)
			nin.Session = session
			if again, _ := mustSave(t, s, nin); again.ID != nw.ID || again.State != StateCandidate {
				t.Fatalf("세션 %s: 표시 없는 새 후보가 스스로 확정됐습니다: %+v", session, again)
			}
		}
		stillCurrent(t, s, old.ID)
	})
	t.Run("하루_정리", func(t *testing.T) {
		s := supStore(t)
		old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
		cur, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
		if _, err := s.DB().Exec(`DELETE FROM audit_flags`); err != nil { // 표시가 사라진 상태
			t.Fatal(err)
		}
		if n, err := s.ConfirmCandidates(); err != nil || n != 0 || stateNow(t, s, cur.ID) != StateCandidate {
			t.Fatalf("표시 없는 새 후보를 하루 정리가 확정했습니다: %d %v %s", n, err, stateNow(t, s, cur.ID))
		}
		stillCurrent(t, s, old.ID)
	})
}

// N2: 새 후보를 사용자 말과 함께 확정(memory_update action=confirm + userQuote)하면 쌍이 한 상태로 닫힌다: 옛 쪽은 새 쪽으로
// 대체되고, 새 쪽은 확정되며 옛 쪽의 핵심(pin) 자리를 이어받고, 표시는 사용자 말로 닫힌다.
func TestDismiss_새_후보를_사용자_말로_확정하면_옛_쪽이_대체되고_핵심_자리가_넘어간다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "커밋 메시지는 영어로", Topic: "커밋 언어", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "커밋 메시지는 영어로"}, Who: Who{Actor: ActorAI}})
	if _, err := s.Pin(rule.ID, Who{UserQuote: "늘 지켜"}); err != nil {
		t.Fatal(err)
	}
	cand, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "커밋 메시지는 한글로", Topic: "커밋 언어", Scope: ScopeGlobal,
		Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	if !hasConflictFlag(t, s, cand.ID, rule.ID) {
		t.Fatal("어긋남 표시가 열리지 않았습니다")
	}
	confirmed := StateConfirmed
	if _, err := s.Update(UpdateInput{ID: cand.ID, State: &confirmed, Action: "confirm",
		Who: Who{Actor: ActorAI, UserQuote: "이제부터 커밋 메시지는 한글로 써"}}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, s, rule.ID); got.State != StateStale || got.SupersededBy != cand.ID {
		t.Fatalf("옛 규칙이 대체되지 않았습니다: %+v", got)
	}
	if got := stateOf(t, s, cand.ID); got.State != StateConfirmed || !got.Pinned {
		t.Fatalf("새 규칙이 확정되지 않았거나 핵심을 이어받지 못했습니다: %+v", got)
	}
	if at, res, ok := flagOf(t, s, cand.ID, rule.ID); !ok || at == 0 || res != "user_words" {
		t.Fatalf("닫힌 표시 %d %q %v", at, res, ok)
	}
}

// 어긋남을 닫은(옛 값 유지) 뒤 사용자가 마음을 바꿔 사용자 말로 새 값을 고르면 새 값이 현재 값이 된다: 막는 것은 스스로
// 확정하는 길뿐이다. 고르는 두 길(사용자 말로 확정, 사용자 말로 같은 제목 재저장)을 모두 본다.
func TestDismiss_닫은_뒤에도_사용자_말로_새_값을_고르면_새_값이_현재_값이_된다(t *testing.T) {
	pick := map[string]func(t *testing.T, s *Store, nw Memory){
		"사용자_말로_확정": func(t *testing.T, s *Store, nw Memory) {
			confirmed := StateConfirmed
			if _, err := s.Update(UpdateInput{ID: nw.ID, State: &confirmed, Action: "confirm",
				Who: Who{Actor: ActorAI, UserQuote: "배포는 이제 화요일이야"}}); err != nil {
				t.Fatal(err)
			}
		},
		"사용자_말로_재저장": func(t *testing.T, s *Store, nw Memory) {
			in := talkFact(nw.Title, nw.Topic, "z")
			in.Source = Source{Kind: SourceQuote, Excerpt: "배포는 이제 화요일이야"}
			mustSave(t, s, in)
		},
	}
	for name, choose := range pick {
		t.Run(name, func(t *testing.T) {
			s := supStore(t)
			old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
			nw, _ := mustSave(t, s, talkFact("배포는 화요일에 한다", "배포 요일", "a"))
			if err := s.ResolveFlag(openConflictID(t, s, nw.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
				t.Fatal(err)
			}
			choose(t, s, nw)
			if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != nw.ID {
				t.Fatalf("옛 사실 %+v", got)
			}
			if got := stateOf(t, s, nw.ID); got.State != StateConfirmed {
				t.Fatalf("새 사실 %+v", got)
			}
			if _, res, ok := flagOf(t, s, nw.ID, old.ID); !ok || res != "user_words" {
				t.Fatalf("닫힌 표시의 사유 %q %v", res, ok)
			}
		})
	}
}

// 불변식은 자동 모드 끔에만 건다: 켬이면 같은 자리의 사용자 말 사실이 있어도 confirmBlocked가 그 까닭으로 막지 않는다(켬의 동작은
// 그대로다). 사용자 말이 아닌 값만 있는 자리, 날이 지난 휘발 사용자 말 사실의 자리도 막지 않는다.
func TestConfirmBlocked_같은_자리_사용자_말_사실은_자동_모드_끔에서_살아_있을_때만_막는다(t *testing.T) {
	s, c := newTestStore(t)
	old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
	nw, _ := mustSave(t, s, talkFact("배포는 화요일에 한다", "배포 요일", "a"))
	if err := s.ResolveFlag(openConflictID(t, s, nw.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	if why, err := s.confirmBlockedUnder(nw.ID, false); err != nil || why != blockUserValue {
		t.Fatalf("끔: %q %v", why, err)
	}
	if why, err := s.confirmBlockedUnder(nw.ID, true); err != nil || why != "" {
		t.Fatalf("켬: %q %v", why, err)
	}
	// 다른 주제의 후보는 막지 않는다.
	other, _ := mustSave(t, s, talkFact("점검은 월요일에 한다", "점검 요일", "a"))
	if why, err := s.confirmBlockedUnder(other.ID, false); err != nil || why != "" {
		t.Fatalf("다른 자리: %q %v", why, err)
	}
	// 날이 지난 휘발 사용자 말 사실은 현재 값이 아니라 막지 않는다.
	tmp := userFact("점검은 이번 주만 수요일에 한다", "점검 시각")
	tmp.Retention, tmp.TTLDays = RetentionVolatile, 1
	vol, _ := mustSave(t, s, tmp)
	later, _ := mustSave(t, s, talkFact("점검은 목요일에 한다", "점검 시각", "a"))
	if err := s.ResolveFlag(openConflictID(t, s, later.ID, vol.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	if why, err := s.confirmBlockedUnder(later.ID, false); err != nil || why != blockUserValue {
		t.Fatalf("휘발 사실이 살아 있는 동안: %q %v", why, err)
	}
	c.t = c.t.Add(48 * time.Hour)
	if why, err := s.confirmBlockedUnder(later.ID, false); err != nil || why != "" {
		t.Fatalf("날이 지난 휘발 사실: %q %v", why, err)
	}
}

// 자동 모드 켬의 동작은 그대로다: 저장 하나가 켬으로 오면(서버는 저장마다 설정을 다시 읽는다) 저장소 정책이 끔이어도 그 저장의
// 확정 판단은 켬으로 하고, 같은 자리 사용자 말 사실 때문에 막지 않는다.
func TestDismiss_자동_모드_켬으로_온_저장은_불변식에_걸리지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
	nin := talkFact("배포는 화요일에 한다", "배포 요일", "a")
	nin.AutoMode = ptr(true)
	nw, _ := mustSave(t, s, nin)
	if err := s.ResolveFlag(openConflictID(t, s, nw.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24 * time.Hour)
	nin.Session = "b"
	if again, _ := mustSave(t, s, nin); again.State != StateConfirmed {
		t.Fatalf("켬으로 온 저장이 저장소 정책(끔)으로 막혔습니다: %s", again.State)
	}
}

// 어긋남을 닫은 뒤 사용자가 사용자 말로 새 후보를 버리면(낡음) 옛 사실은 그대로 현재 값이다: 닫힌 쌍을 마음을 바꾼 것으로 읽는
// 길이 버린 새 쪽으로 옛 쪽을 대체하지 않는다.
func TestDismiss_닫은_뒤_사용자_말로_새_후보를_버리면_옛_사실이_그대로다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
	nw, _ := mustSave(t, s, talkFact("배포는 화요일에 한다", "배포 요일", "a"))
	if err := s.ResolveFlag(openConflictID(t, s, nw.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	stale := StateStale
	if _, err := s.Update(UpdateInput{ID: nw.ID, State: &stale, Action: "stale", Reason: "틀림",
		Who: Who{Actor: ActorAI, UserQuote: "화요일은 틀렸어"}}); err != nil {
		t.Fatal(err)
	}
	if got := stateNow(t, s, nw.ID); got != StateStale {
		t.Fatalf("새 후보 %s", got)
	}
	stillCurrent(t, s, old.ID)
	if _, res, ok := flagOf(t, s, nw.ID, old.ID); !ok || res != resolutionDismissed {
		t.Fatalf("닫힌 표시의 사유가 바뀌었습니다 %q %v", res, ok)
	}
}
