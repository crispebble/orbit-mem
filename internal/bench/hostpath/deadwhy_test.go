package hostpath

import (
	"testing"

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
