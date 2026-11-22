package hostpath

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 기준값은 낡음·숨김·만료·지움으로 Dead를 센다. 주제의 옛 버전(WhyOlder)을 넣으면 기준값과 새 값을 견줄 수 없다.
func TestDeadWhy_기준값_정의대로_낡음_숨김_만료_지움만_Dead이고_옛버전은_아니다(t *testing.T) {
	cases := []struct {
		why  memory.Why
		want string
	}{
		{memory.WhyStale, bench.DeadStale},
		{memory.WhyRejected, bench.DeadHidden},
		{memory.WhyExpired, bench.DeadExpired},
		{memory.WhyGone, bench.DeadGone},
		{memory.WhyOlder, ""},
		{memory.WhyCurrent, ""},
	}
	for _, c := range cases {
		if got := deadWhy(c.why); got != c.want {
			t.Errorf("deadWhy(%q) = %q, want %q", c.why, got, c.want)
		}
	}
}

// 호출이 낸 id가 사본에 없으면(그 전에 지운 기억) 까닭 gone의 Dead다. 감사도 지운 시각 뒤의 주입을 gone으로 센다. 0.20.0의
// 재개 시작은 지운 기억을 묶음에서 빼므로, 이 판정은 경로를 거치지 않고 직접 잰다.
func TestDead_사본에_없는_id는_까닭_gone이다(t *testing.T) {
	db := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말로 한다", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "답은 존댓말로 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	r := &recaller{db: db, at: &clock{t: time.Now()}}
	defer r.close()
	const gone = "m-000000000000"
	dead, why, err := r.dead([]string{m.ID, gone})
	if err != nil || !slices.Equal(dead, []string{gone}) || why[gone] != bench.DeadGone || len(why) != 1 {
		t.Fatalf("dead %v why %v err %v, want %s gone alone", dead, why, err, gone)
	}
}
