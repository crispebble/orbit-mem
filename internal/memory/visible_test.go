package memory

// 보임 판정(명세 6.2 변경 축): 「이 기억이 현재 값으로 보일 수 있는가」는 Viewer 한 곳이 가른다.

import (
	"testing"
	"time"
)

func viewer(t *testing.T, s *Store) Viewer {
	t.Helper()
	v, err := s.Viewer()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestViewer_폐기_거절_지움_만료_옛버전을_가른다(t *testing.T) {
	s := supStore(t)
	cur, _ := mustSave(t, s, userSays(KindRule, "보고는 한국어로", "보고 언어"))
	// 같은 주제의 새 사용자 말이 옛 것을 낡음으로 대체한다.
	next, _ := mustSave(t, s, userSays(KindRule, "보고는 한국어 존댓말로", "보고 언어"))
	rej, _ := mustSave(t, s, aiLearns(KindFact, "빌드는 make", "빌드"))
	if _, err := s.SetState(rej.ID, StateHidden, "아님"); err != nil {
		t.Fatal(err)
	}
	gone, _ := mustSave(t, s, userSays(KindFact, "곧 지울 것", "지울 것"))
	if _, err := s.Forget(ForgetInput{IDs: []string{gone.ID}, Reason: "시험"}); err != nil {
		t.Fatal(err)
	}
	// 임시 값이 머리인 동안 주제의 옛 값은 살아 있어도 옛 버전으로 보인다.
	base, _ := mustSave(t, s, userSays(KindRule, "기본은 A", "기본값"))
	in := userSays(KindRule, "이번 주만 B", "기본값")
	in.Retention, in.TTLDays = RetentionVolatile, 1
	tmp, _ := mustSave(t, s, in)

	v := viewer(t, s)
	cases := []struct {
		name string
		id   string
		want Why
	}{
		{"대체돼 낡음", cur.ID, WhyStale},
		{"대체한 새 값", next.ID, WhyCurrent},
		{"거절한 후보", rej.ID, WhyRejected},
		{"지운 기억", gone.ID, WhyGone},
		{"없는 id", "m-없음", WhyGone},
		{"임시 값이 머리라 옛 버전", base.ID, WhyOlder},
		{"임시 값 자신", tmp.ID, WhyCurrent},
	}
	for _, c := range cases {
		m, vis := v.OfID(c.id)
		if vis.Why != c.want || vis.Current() != (c.want == WhyCurrent) {
			t.Errorf("%s: why=%q current=%v, want %q", c.name, vis.Why, vis.Current(), c.want)
		}
		if c.want == WhyGone && m.ID != "" {
			t.Errorf("%s: 지운 기억은 빈 Memory여야 한다: %+v", c.name, m)
		}
	}
	// 날이 지나면 임시 값은 만료로 보이고 옛 값이 다시 현재 값이다.
	at := s.Now().Add(48 * time.Hour)
	s.Now = func() time.Time { return at }
	v = viewer(t, s)
	if _, vis := v.OfID(tmp.ID); vis.Why != WhyExpired {
		t.Errorf("임시 값: %q, want expired", vis.Why)
	}
	if _, vis := v.OfID(base.ID); !vis.Current() {
		t.Errorf("옛 값은 임시 값이 만료되면 현재 값이다: %q", vis.Why)
	}
}

func TestViewer_사용자_말_휘발은_만료여도_상태는_그대로이고_만료로_보인다(t *testing.T) {
	s := supStore(t)
	in := userSays(KindRule, "이번 주만 영어로", "보고 언어")
	in.Retention, in.TTLDays = RetentionVolatile, 1
	m, _ := mustSave(t, s, in)
	at := s.Now().Add(48 * time.Hour)
	s.Now = func() time.Time { return at }
	got, vis := viewer(t, s).OfID(m.ID)
	if got.State != StateConfirmed {
		t.Fatalf("상태가 바뀌었다: %q", got.State)
	}
	if vis.Why != WhyExpired || vis.Current() {
		t.Fatalf("만료로 보여야 한다: %q", vis.Why)
	}
}

func TestViewer_후계를_따라가_살아있는_것을_준다(t *testing.T) {
	s := supStore(t)
	a, _ := mustSave(t, s, userSays(KindRule, "빌드는 make", "빌드 명령"))
	mustSave(t, s, userSays(KindRule, "빌드는 go build", "빌드 명령"))
	c, _ := mustSave(t, s, userSays(KindRule, "빌드는 task build", "빌드 명령"))
	v := viewer(t, s)
	// a는 b에게, b는 c에게 대체됐다: 살아 있는 끝은 c다.
	old, _ := v.OfID(a.ID)
	if got, ok := v.Successor(old); !ok || got.ID != c.ID {
		t.Fatalf("successor = %v %v, want %s", got.ID, ok, c.ID)
	}
	if _, vis := v.OfID(a.ID); vis.Successor != c.ID {
		t.Fatalf("Visibility.Successor = %q, want %s", vis.Successor, c.ID)
	}
	if _, vis := v.OfID(c.ID); vis.Successor != "" {
		t.Fatalf("현재 값에는 후계가 없다: %q", vis.Successor)
	}
	// 끝 c를 낡게 하고 후계가 없으면 이어갈 곳이 없다.
	if _, err := s.SetState(c.ID, StateStale, "x"); err != nil {
		t.Fatal(err)
	}
	v = viewer(t, s)
	if _, ok := v.Successor(stateOf(t, s, a.ID)); ok {
		t.Fatal("살아 있는 후계가 없는데 찾았다")
	}
}

func TestViewer_재검토일이_지나면_표시하되_현재_값이다(t *testing.T) {
	s := supStore(t)
	d, _ := mustSave(t, s, SaveInput{Kind: KindDecision, Title: "엔진은 자체 포크", Retention: RetentionDurable, ReviewAfterDays: 5,
		Source: Source{Kind: SourceQuote, Excerpt: "엔진은 자체 포크"}, Who: Who{Actor: ActorAI}})
	if _, vis := viewer(t, s).OfID(d.ID); vis.ReviewDue {
		t.Fatal("아직 재검토일 전")
	}
	at := s.Now().Add(6 * 24 * time.Hour)
	s.Now = func() time.Time { return at }
	if _, vis := viewer(t, s).OfID(d.ID); !vis.ReviewDue || !vis.Current() {
		t.Fatalf("review due=%v current=%v", vis.ReviewDue, vis.Current())
	}
}

// chainTo는 ids[0]이 ids[1]에게, ids[1]이 ids[2]에게 대체된 사슬을 DB에 직접 놓는다(저장 경로는 한 걸음씩만 만든다).
func chainTo(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	for i := 0; i+1 < len(ids); i++ {
		if _, err := s.db.Exec(`UPDATE memories SET state='stale', superseded_by=? WHERE id=?`, ids[i+1], ids[i]); err != nil {
			t.Fatal(err)
		}
	}
}

func chainMemories(t *testing.T, s *Store, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		name := "사슬 " + string(rune('가'+i))
		m, _ := mustSave(t, s, userSays(KindFact, name, name))
		ids[i] = m.ID
	}
	return ids
}

func TestViewer_후계_사슬은_여덟_걸음까지_따라간다(t *testing.T) {
	s := supStore(t)
	// ids[0]에서 여덟 번 따라가면 ids[8]에 닿는다. 아홉째(ids[9])는 닿지 못한다.
	ids := chainMemories(t, s, 10)
	chainTo(t, s, ids[:9]...)
	v := viewer(t, s)
	if got, ok := v.Successor(stateOf(t, s, ids[0])); !ok || got.ID != ids[8] {
		t.Fatalf("여덟 걸음: %q %v, want %s", got.ID, ok, ids[8])
	}
	chainTo(t, s, ids...)
	if _, ok := v.Successor(stateOf(t, s, ids[0])); ok {
		t.Fatal("아홉 걸음 끝의 후계는 찾지 않는다")
	}
}

func TestViewer_후계_사슬이_돌면_끝내고_없다고_한다(t *testing.T) {
	s := supStore(t)
	ids := chainMemories(t, s, 2)
	chainTo(t, s, ids[0], ids[1], ids[0])
	if _, ok := viewer(t, s).Successor(stateOf(t, s, ids[0])); ok {
		t.Fatal("순환 사슬에서 후계를 찾았다")
	}
}

func TestViewer_후보_상태의_후계도_살아있는_것으로_인정한다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userSays(KindFact, "옛 값", "값"))
	cand, _ := mustSave(t, s, aiLearns(KindFact, "후보 값", "다른 값"))
	chainTo(t, s, old.ID, cand.ID)
	if got := stateOf(t, s, cand.ID); got.State != StateCandidate {
		t.Fatalf("후보여야 한다: %q", got.State)
	}
	if got, ok := viewer(t, s).Successor(stateOf(t, s, old.ID)); !ok || got.ID != cand.ID {
		t.Fatalf("후보 후계: %q %v", got.ID, ok)
	}
}
