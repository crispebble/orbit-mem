package bench_test

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/paths"
)

// 수용 기준을 공개 고정 묶음의 판정용(30%)으로 단언한다(계획 1-6 b6). 판정용은 문턱을 고른 뒤 한 번만 잰 값이고, 판정용을
// 보고 규칙이나 문턱을 고치지 않는다. 기준에 못 미친 지표(L-5 적중·틀린 회상·넣지 않음)는 시험으로 덮지 않고 보고에 미달로
// 적는다: 지금 단언하는 것은 이미 기준을 넘은 L-7 놓침 복구뿐이다.

// L-7: 회상이 아무것도 넣지 않은 정답 있는 요청 가운데 원문 찾기(Find(요청, 8))로 정답을 모두 되찾는 비율 ≥90%.
func TestAcceptance_L7_판정용_놓침_복구는_90퍼센트_이상이다(t *testing.T) {
	fixHome(t)
	set, err := bench.Read(publicSet)
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := bench.Run(p, factory(t, paths.Current))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		name string
		rate bench.Rate
	}{
		{"아무것도 넣지 않은 사례", rep.Runs[0].Test.Requests.Recover},
		{"적중 실패 전부", rep.Runs[0].Test.Requests.RecoverMissed},
	} {
		if r.rate.Den == 0 || r.rate.Value == nil || *r.rate.Value < 0.9 {
			t.Errorf("판정용 놓침 복구(%s) %d/%d, 기준 ≥90%%", r.name, r.rate.Num, r.rate.Den)
		}
	}
}
