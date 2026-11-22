package memory

// 같은 자리의 사용자 말 값이 새 후보의 스스로 확정을 막는 범위(confirmBlockedUnder, blockUserValue)는 대체 정책 표와 같다:
// 규칙·결정·교훈과 종류가 다른 쌍은 사용자 말이 아닌 것으로 대체되지 않으므로 자동 모드와 무관하게 막고, 사실끼리는 자동
// 모드가 꺼져 있을 때만 막는다(켬은 자동 대체를 허락한다).

import (
	"errors"
	"testing"
	"time"
)

// kindPair saves the user's words of kind in the global scope with topic, then an AI candidate of the same kind
// and topic in session a, and closes their conflict as dismissed by the person.
func kindPair(t *testing.T, s *Store, kind, topic string) (old, nw Memory, nin SaveInput) {
	t.Helper()
	old, _ = mustSave(t, s, SaveInput{Kind: kind, Title: "사용자 값 " + topic, Topic: topic, Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "사용자 값 " + topic}, Who: Who{Actor: ActorAI}})
	nin = SaveInput{Kind: kind, Title: "AI 값 " + topic, Topic: topic, Scope: ScopeGlobal, Who: Who{Actor: ActorAI},
		Source: Source{Kind: SourceSession, Ref: "s"}, Session: "a"}
	nw, _ = mustSave(t, s, nin)
	if err := s.ResolveFlag(openConflictID(t, s, nw.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	return old, nw, nin
}

// 검토에서 재현한 꼴: 전역 범위 사용자 말 규칙 → 같은 주제 AI 후보 규칙 → 어긋남 dismiss → 다른 날 다른 세션 둘에서 재저장.
// 규칙·결정·교훈은 자동 모드 끔과 켬 모두에서 후보로 남고 사용자 값이 현재 값으로 남는다.
func TestUserValue_규칙_결정_교훈_후보는_자동_모드와_무관하게_스스로_확정되지_않는다(t *testing.T) {
	for _, kind := range []string{KindRule, KindDecision, KindLesson} {
		for _, auto := range []bool{false, true} {
			t.Run(kind+"_"+map[bool]string{false: "끔", true: "켬"}[auto], func(t *testing.T) {
				s, c := newTestStore(t)
				s.Policy.AutoMode = auto
				old, nw, nin := kindPair(t, s, kind, "주제 "+kind)
				nin.AutoMode = ptr(auto)
				for _, session := range []string{"b", "c"} {
					c.t = c.t.Add(24 * time.Hour)
					nin.Session = session
					if again, _ := mustSave(t, s, nin); again.ID != nw.ID || again.State != StateCandidate {
						t.Fatalf("세션 %s: 후보가 스스로 확정됐습니다: %s", session, again.State)
					}
				}
				if n, err := s.ConfirmCandidates(); err != nil || n != 0 {
					t.Fatalf("하루 정리 %d %v", n, err)
				}
				stillCurrent(t, s, old.ID)
			})
		}
	}
}

// 종류 × 자동 모드 표: 규칙·결정·교훈·종류가 다른 쌍은 늘 막고, 사실끼리는 끔에서만 막는다.
func TestUserValue_막는_범위는_종류와_자동_모드로_대체_정책_표를_따른다(t *testing.T) {
	cases := []struct {
		name             string
		oldKind, newKind string
		off, on          string
	}{
		{"규칙", KindRule, KindRule, blockUserValue, blockUserValue},
		{"결정", KindDecision, KindDecision, blockUserValue, blockUserValue},
		{"교훈", KindLesson, KindLesson, blockUserValue, blockUserValue},
		{"사실", KindFact, KindFact, blockUserValue, ""},
		{"옛 사실 새 교훈", KindFact, KindLesson, blockUserValue, blockUserValue},
		{"옛 규칙 새 사실", KindRule, KindFact, blockUserValue, blockUserValue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := supStore(t)
			if _, err := s.DB().Exec(`CREATE TRIGGER audit_flags_block BEFORE INSERT ON audit_flags BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
				t.Fatal(err) // 표시 없이 불변식만 본다
			}
			mustSave(t, s, SaveInput{Kind: c.oldKind, Title: "사용자 값", Topic: "자리", Scope: replScope,
				Source: Source{Kind: SourceQuote, Excerpt: "사용자 값"}, Who: Who{Actor: ActorAI}})
			nw, _ := mustSave(t, s, SaveInput{Kind: c.newKind, Title: "AI 값", Topic: "자리", Scope: replScope,
				Source: Source{Kind: SourceSession, Ref: "s"}, Who: Who{Actor: ActorAI}})
			if why, err := s.confirmBlockedUnder(nw.ID, false); err != nil || why != c.off {
				t.Fatalf("끔: %q %v, want %q", why, err, c.off)
			}
			if why, err := s.confirmBlockedUnder(nw.ID, true); err != nil || why != c.on {
				t.Fatalf("켬: %q %v, want %q", why, err, c.on)
			}
		})
	}
}

// 사람이 아닌 호출자가 사용자 말 없이 옛 대기 확정 제안을 승인해 불변식을 비켜 가지 못한다. 사용자 말을 주면 승인되고 쌍이
// 한 상태로 닫히며(옛 쪽 대체), 사람의 승인은 그대로 된다.
func TestUserValue_옛_대기_확정_제안을_사용자_말_없이_승인하지_못한다(t *testing.T) {
	t.Run("AI_사용자_말_없음", func(t *testing.T) {
		s := supStore(t)
		old, nw := conflictPair(t, s, "배포 요일")
		p, err := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정", MemoryIDs: []string{nw.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Who: Who{Actor: ActorAI}}); !errors.Is(err, ErrProtected) {
			t.Fatalf("err %v", err)
		}
		if stateNow(t, s, nw.ID) != StateCandidate {
			t.Fatal("사용자 말 없는 승인으로 확정됐습니다")
		}
		stillCurrent(t, s, old.ID)
	})
	t.Run("AI_사용자_말", func(t *testing.T) {
		s := supStore(t)
		old, nw := conflictPair(t, s, "배포 요일")
		p, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정", MemoryIDs: []string{nw.ID}})
		if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Who: Who{Actor: ActorAI, UserQuote: "화요일로 바꿔"}}); err != nil {
			t.Fatal(err)
		}
		if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != nw.ID {
			t.Fatalf("옛 쪽 %+v", got)
		}
		if stateNow(t, s, nw.ID) != StateConfirmed {
			t.Fatal("새 쪽이 확정되지 않았습니다")
		}
	})
	t.Run("사람", func(t *testing.T) {
		s := supStore(t)
		_, nw := conflictPair(t, s, "배포 요일")
		p, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정", MemoryIDs: []string{nw.ID}})
		if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Who: Who{Actor: ActorUser}}); err != nil {
			t.Fatal(err)
		}
		if stateNow(t, s, nw.ID) != StateConfirmed {
			t.Fatal("사람의 승인이 막혔습니다")
		}
	})
}

// topic이 빈 후보는 같은 자리가 없어 막지 않는다: 사용자 말 기억이 있어도 증거로 확정된다.
func TestUserValue_주제가_빈_후보는_영향이_없다(t *testing.T) {
	s, c := newTestStore(t)
	mustSave(t, s, userFact("배포는 금요일에 한다", ""))
	in := talkFact("배포 스크립트는 deploy.sh다", "", "a")
	nw, _ := mustSave(t, s, in)
	if why, err := s.confirmBlockedUnder(nw.ID, false); err != nil || why != "" {
		t.Fatalf("%q %v", why, err)
	}
	c.t = c.t.Add(24 * time.Hour)
	in.Session = "b"
	if again, _ := mustSave(t, s, in); again.State != StateConfirmed {
		t.Fatalf("주제 없는 후보가 확정되지 않았습니다: %s", again.State)
	}
}

// 사용자 말 기억이 낡음이나 숨김으로 바뀌면 그 자리는 더 막지 않는다.
func TestUserValue_사용자_말_기억이_낡거나_숨겨지면_풀린다(t *testing.T) {
	for _, state := range []string{StateStale, StateHidden} {
		t.Run(state, func(t *testing.T) {
			s := supStore(t)
			old, nw := conflictPair(t, s, "배포 요일")
			if err := s.ResolveFlag(openConflictID(t, s, nw.ID, old.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
				t.Fatal(err)
			}
			if why, _ := s.confirmBlockedUnder(nw.ID, false); why != blockUserValue {
				t.Fatalf("살아 있을 때 %q", why)
			}
			if _, err := s.SetState(old.ID, state, "사람이 바꿈"); err != nil {
				t.Fatal(err)
			}
			if why, err := s.confirmBlockedUnder(nw.ID, false); err != nil || why != "" {
				t.Fatalf("%s 뒤 %q %v", state, why, err)
			}
		})
	}
}

// 어긋남을 닫은 뒤 새 후보의 제목을 사용자 말(quote)로 다시 저장하면 새 쪽이 확정되고 옛 쪽을 대체하며 핵심(pin) 자리를 잇는다.
func TestUserValue_사용자_말_재저장은_옛_핵심_자리를_새_쪽이_잇는다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "커밋 메시지는 영어로", Topic: "커밋 언어", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "커밋 메시지는 영어로"}, Who: Who{Actor: ActorAI}})
	if _, err := s.Pin(rule.ID, Who{UserQuote: "늘 지켜"}); err != nil {
		t.Fatal(err)
	}
	cin := SaveInput{Kind: KindRule, Title: "커밋 메시지는 한글로", Topic: "커밋 언어", Scope: ScopeGlobal,
		Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}}
	cand, _ := mustSave(t, s, cin)
	if err := s.ResolveFlag(openConflictID(t, s, cand.ID, rule.ID), "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	cin.Source = Source{Kind: SourceQuote, Excerpt: "커밋 메시지는 이제 한글로 써"}
	mustSave(t, s, cin)
	if got := stateOf(t, s, rule.ID); got.State != StateStale || got.SupersededBy != cand.ID {
		t.Fatalf("옛 규칙 %+v", got)
	}
	if got := stateOf(t, s, cand.ID); got.State != StateConfirmed || !got.Pinned {
		t.Fatalf("새 규칙 %+v", got)
	}
}

// 다른 자리 쌍의 새 쪽을 사용자 말로 확정하면 표시는 예전처럼 확정 사유로 닫힌다: 다른 자리의 옛 사용자 말이 「사용자가 고른
// 값」(revertedOldTx)으로 바뀌지 않는다.
func TestUserValue_다른_자리_쌍은_사용자_말_확정에서_확정_사유로_닫힌다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "답은 한글 존댓말로", Topic: "말투", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "답은 한글 존댓말로"}, Who: Who{Actor: ActorAI}})
	cand, _ := mustSave(t, s, SaveInput{Kind: KindFact, Title: "이 프로젝트는 반말로 답한다", Topic: "말투 예외", Scope: replScope,
		ConflictsWith: []string{rule.ID}, Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	confirmed := StateConfirmed
	if _, err := s.Update(UpdateInput{ID: cand.ID, State: &confirmed, Action: "confirm",
		Who: Who{Actor: ActorAI, UserQuote: "이 프로젝트는 반말로 해"}}); err != nil {
		t.Fatal(err)
	}
	if at, res, ok := flagOf(t, s, cand.ID, rule.ID); !ok || at == 0 || res != StateConfirmed {
		t.Fatalf("닫힌 표시 %d %q %v", at, res, ok)
	}
	if picked, err := revertedOldTx(s.DB(), rule.ID); err != nil || picked {
		t.Fatalf("다른 자리 옛 규칙이 골라진 값이 됐습니다 %v %v", picked, err)
	}
	if got := stateOf(t, s, rule.ID); got.State != StateConfirmed {
		t.Fatalf("다른 자리 옛 규칙 %+v", got)
	}
}
