package memory

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// E-1·E-2(명세 · orbit-mem 기억 정확성과 회상 품질 개선): AI가 배운 것은 서로 다른 세션에서, 다른 날 다시 나왔을 때만
// 확정하고, 그 사이 사용자가 같은 범위·주제를 반대로 교정했으면 확정하지 않는다.

func inSession(in SaveInput, session string) SaveInput {
	in.Session = session
	return in
}

func stateNow(t *testing.T, s *Store, id string) string {
	t.Helper()
	return stateOf(t, s, id).State
}

func TestEvidence_같은_세션에서_날만_넘겨_두번_저장하면_확정되지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	in := aiLearns(KindLesson, "codex exec는 stdin을 닫는다", "")
	first, _ := mustSave(t, s, inSession(in, "sess-a"))
	c.t = c.t.Add(24 * time.Hour)
	if again, _ := mustSave(t, s, inSession(in, "sess-a")); again.State != StateCandidate {
		t.Fatalf("같은 세션 다음 날에 확정됐습니다: %s", again.State)
	}
	// 하위 에이전트의 저장(「세션/agent」)은 부모 세션과 같은 세션이다.
	c.t = c.t.Add(24 * time.Hour)
	if again, _ := mustSave(t, s, inSession(in, "sess-a/agent-1")); again.State != StateCandidate {
		t.Fatalf("하위 에이전트의 저장이 다른 세션으로 세어졌습니다: %s", again.State)
	}
	if n, err := s.ConfirmCandidates(); err != nil || n != 0 {
		t.Fatalf("정리 확정 %d %v", n, err)
	}
	if got := stateNow(t, s, first.ID); got != StateCandidate {
		t.Fatalf("상태 %s", got)
	}
	if n := len(mustLedger(t, s)); n != 0 {
		t.Fatalf("원장 %d줄", n)
	}
}

func TestEvidence_다른_세션_다른_날_두번이면_확정된다(t *testing.T) {
	s, c := newTestStore(t)
	in := aiLearns(KindLesson, "codex exec는 stdin을 닫는다", "")
	first, _ := mustSave(t, s, inSession(in, "sess-a"))
	// 세션만 다르고 같은 날이면 아직 아니다.
	if again, _ := mustSave(t, s, inSession(in, "sess-b")); again.State != StateCandidate {
		t.Fatalf("같은 날 다른 세션에 확정됐습니다: %s", again.State)
	}
	c.t = c.t.Add(24 * time.Hour)
	again, _ := mustSave(t, s, inSession(in, "sess-b"))
	if again.State != StateConfirmed {
		t.Fatalf("다른 세션·다른 날인데 확정되지 않았습니다: %s", again.State)
	}
	all := mustLedger(t, s)
	if len(all) != 1 || all[0].ProposalKind != ProposalConfirm || len(all[0].Targets) != 1 || all[0].Targets[0] != first.ID {
		t.Fatalf("원장 = %+v", all)
	}
	if p, err := s.GetProposal(all[0].ProposalID); err != nil || p.Payload.Evidence != EvidenceSeenDays {
		t.Fatalf("증거 = %+v %v", p.Payload, err)
	}
}

// 세 단계 어디서나 두 번째 다른 세션·다른 날 저장이 확정 근거가 되는 꼴로 둔다. 막는 까닭만 다르다.
func TestEvidence_같은_주제에_거절된_기억이나_어긋남이_있으면_확정되지_않는다(t *testing.T) {
	const topic = "배포 창"
	t.Run("후보가 생긴 뒤 같은 주제의 거절", func(t *testing.T) {
		s, c := newTestStore(t)
		in := aiLearns(KindFact, "배포는 화요일 오전이다", topic)
		cand, _ := mustSave(t, s, inSession(in, "sess-a"))
		// 휘발 기억은 주제의 값을 대체하지 않으므로 같은 주제에 후보와 함께 있을 수 있다.
		other := aiLearns(KindFact, "이번 주 배포는 수요일이다", topic)
		other.Retention = RetentionVolatile
		h, _ := mustSave(t, s, inSession(other, "sess-a"))
		if _, err := s.SetState(h.ID, StateHidden, "사용자가 아니라고 함"); err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(24 * time.Hour)
		if again, _ := mustSave(t, s, inSession(in, "sess-b")); again.State != StateCandidate {
			t.Fatalf("거절 뒤에 확정됐습니다: %s", again.State)
		}
		if n, err := s.ConfirmCandidates(); err != nil || n != 0 {
			t.Fatalf("정리 확정 %d %v", n, err)
		}
		if got := stateNow(t, s, cand.ID); got != StateCandidate {
			t.Fatalf("상태 %s", got)
		}
	})
	t.Run("같은 주제의 열린 어긋남 쌍", func(t *testing.T) {
		s, c := newTestStore(t)
		rule := quoted(t, s, "배포 창은 사람이 정한다")
		in := aiLearns(KindFact, "배포는 화요일 오전이다", topic)
		cand, _ := mustSave(t, s, inSession(in, "sess-a"))
		other := aiLearns(KindFact, "이번 주 배포는 수요일이다", topic)
		other.Retention = RetentionVolatile
		x, _ := mustSave(t, s, inSession(other, "sess-a"))
		f, err := s.FlagConflict(x.ID, rule.ID, "사용자 규칙과 어긋남", Who{Actor: ActorAI})
		if err != nil {
			t.Fatal(err)
		}
		c.t = c.t.Add(24 * time.Hour)
		if again, _ := mustSave(t, s, inSession(in, "sess-b")); again.State != StateCandidate {
			t.Fatalf("같은 주제에 어긋남이 열려 있는데 확정됐습니다: %s", again.State)
		}
		// 어긋남이 풀리면 쌓인 증거로 확정된다: 막은 것은 어긋남이다.
		if err := s.ResolveFlag(f.ID, "dismissed", Who{}); err != nil {
			t.Fatal(err)
		}
		if n, err := s.ConfirmCandidates(); err != nil || n != 1 {
			t.Fatalf("어긋남을 푼 뒤 정리 확정 %d %v", n, err)
		}
		if got := stateNow(t, s, cand.ID); got != StateConfirmed {
			t.Fatalf("상태 %s", got)
		}
	})
}

func TestEvidence_후보가_생기기_전의_거절은_확정을_막지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	const topic = "배포 창"
	old, _ := mustSave(t, s, aiLearns(KindFact, "배포는 금요일이다", topic))
	if _, err := s.SetState(old.ID, StateHidden, "사용자가 아니라고 함"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
	in := aiLearns(KindFact, "배포는 화요일 오전이다", topic)
	mustSave(t, s, inSession(in, "sess-a"))
	c.t = c.t.Add(24 * time.Hour)
	if again, _ := mustSave(t, s, inSession(in, "sess-b")); again.State != StateConfirmed {
		t.Fatalf("후보 전의 거절이 확정을 막았습니다: %s", again.State)
	}
}

// 주제가 없는 기억에는 거절 조건을 적용하지 않는다(어긋남 쌍 조건만 본다): 주제 없는 거절이 아무 후보나 막지 않는다.
func TestEvidence_주제가_없는_후보는_거절_기록을_보지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	in := aiLearns(KindLesson, "codex exec는 stdin을 닫는다", "")
	mustSave(t, s, inSession(in, "sess-a"))
	h, _ := mustSave(t, s, aiLearns(KindLesson, "codex exec는 stdout을 버린다", ""))
	if _, err := s.SetState(h.ID, StateHidden, "사용자가 아니라고 함"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24 * time.Hour)
	if again, _ := mustSave(t, s, inSession(in, "sess-b")); again.State != StateConfirmed {
		t.Fatalf("주제 없는 거절이 확정을 막았습니다: %s", again.State)
	}
}

// 내보낸 줄은 다시 나온 날만 알아, 들여온 후보의 날은 세션을 모르는 한 세션(legacy)으로 센다: 들여오기만으로 확정 근거가 생기지 않는다.
func TestEvidence_들여온_후보의_날은_legacy_세션_하나로_센다(t *testing.T) {
	s, _ := newTestStore(t)
	m := Memory{ID: NewID("m-"), Kind: KindLesson, Scope: ScopeGlobal, State: StateCandidate, Title: "들여온 교훈",
		Days: []string{"2026-09-01", "2026-09-02", "2026-09-03"}, Sources: []Source{{Kind: SourceSession, Ref: "s"}}}
	if res, err := s.Import(m, "test", false); err != nil || res != ImportDone {
		t.Fatalf("import %s %v", res, err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sighting_sessions WHERE memory_id=? AND session=?`, m.ID, legacySession).Scan(&n); err != nil || n != 3 {
		t.Fatalf("legacy 줄 %d %v", n, err)
	}
	if ev, err := s.EvidenceFor(m.ID); err != nil || ev != "" {
		t.Fatalf("증거 = %q %v", ev, err)
	}
}

// 두 연결이 같은 제목을 동시에 여러 날 저장한다. 한 세션의 하위 에이전트 둘이면 끝까지 확정되지 않고, 세션이 다르면
// 확정되며 확정 원장 줄은 하나다(다시 나온 줄은 하나도 잃지 않는다).
func TestEvidence_두_연결이_동시에_저장해도_같은_세션이면_확정되지_않고_확정은_한_번이다(t *testing.T) {
	for _, c := range []struct {
		name     string
		sessions [2]string
		want     string
		ledger   int
	}{
		{"한 세션의 하위 에이전트 둘", [2]string{"s-1/agent-a", "s-1/agent-b"}, StateCandidate, 0},
		{"서로 다른 세션", [2]string{"s-1", "s-2"}, StateConfirmed, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "memory.db")
			var stores []*Store
			for range 2 {
				s, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				stores = append(stores, s)
			}
			const rounds = 10
			in := aiLearns(KindLesson, "codex exec는 stdin을 닫는다", "")
			start := make(chan struct{})
			var wg sync.WaitGroup
			errs := make(chan error, 2*rounds)
			for w, s := range stores {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for i := range rounds {
						x := inSession(in, c.sessions[w])
						x.Day = fmt.Sprintf("2026-12-%02d", 1+2*i+w) // 연결마다, 차례마다 다른 날
						if _, err := s.Save(x); err != nil {
							errs <- err
						}
					}
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
			all, err := stores[0].List()
			if err != nil || len(all) != 1 || all[0].State != c.want {
				t.Fatalf("기억 = %+v %v", all, err)
			}
			if n := len(mustLedger(t, stores[0])); n != c.ledger {
				t.Fatalf("원장 %d줄, want %d", n, c.ledger)
			}
			var rows int
			if err := stores[0].DB().QueryRow(`SELECT COUNT(*) FROM sighting_sessions WHERE memory_id=?`, all[0].ID).Scan(&rows); err != nil || rows != 2*rounds {
				t.Fatalf("다시 나온 줄 %d %v", rows, err)
			}
		})
	}
}

// 스키마 5 DB의 다시 나온 날은 이행에서 세션 legacy 하나로 옮겨진다(v5DB: 후보 cand에 날 둘). 옛 날 둘만으로는 확정되지
// 않고, 새 세션이 다른 날 한 번 다시 나오면 legacy 한 줄과 세션·날이 둘 다 달라 확정된다.
func TestEvidence_옛_기록만_있는_후보는_legacy_세션_하나로_세어_확정되지_않는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	f := v5DB(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &clock{t: time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local)}
	s.Now = c.now
	if ev, err := s.EvidenceFor(f.cand.ID); err != nil || ev != "" {
		t.Fatalf("옛 날 둘의 증거 = %q %v", ev, err)
	}
	if n, err := s.ConfirmCandidates(); err != nil || n != 0 {
		t.Fatalf("정리 확정 %d %v", n, err)
	}
	if got := stateNow(t, s, f.cand.ID); got != StateCandidate {
		t.Fatalf("상태 %s", got)
	}
	// 세션을 모르는 저장(세션 없이 온 저장)도 legacy와 같은 한 세션으로 센다: 다른 날이어도 확정 근거가 아니다.
	in := SaveInput{Kind: KindFact, Title: f.cand.Title, Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}, Who: Who{Actor: ActorAI}}
	if again, _ := mustSave(t, s, in); again.State != StateCandidate {
		t.Fatalf("세션 없는 저장이 다른 세션으로 세어졌습니다: %s", again.State)
	}
	c.t = c.t.Add(24 * time.Hour)
	if again, _ := mustSave(t, s, inSession(in, "sess-new")); again.State != StateConfirmed {
		t.Fatalf("새 세션·다른 날인데 확정되지 않았습니다: %s", again.State)
	}
	if got := stateNow(t, s, f.confirmed.ID); got != StateConfirmed {
		t.Fatalf("이미 확정된 기억의 상태가 바뀌었습니다: %s", got)
	}
}
