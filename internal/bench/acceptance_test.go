package bench_test

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/paths"
)

// 수용 기준을 판정 전용 묶음(holdout_set.json)의 한 번 판정으로 단언한다(계획 1-6 b6 2차). 판정은 d62c9ea의 규칙·기본값으로
// 한 번 쟀고, 그 뒤 값을 고치지 않았다. 기준을 넘은 지표만 단언한다: L-5의 넣지 않음(33/34). 미달인 L-5 적중(55/99)·틀린
// 회상(9/68)과 L-7 놓침 복구(25/35·33/44)는 시험으로 덮지 않고 판정 기록(README.md 「판정 기록」)에 미달로 둔다.
//
// 1차의 공개 묶음 판정용 L-7 단언은 뺐다: 2차에서 공개 묶음 전체를 문턱 고르기에 썼으니 그 판정용은 개발 자료이고, 개발
// 자료의 값은 baseline-current.json이 그대로 지킨다.

// L-5: 정답이 없는 요청(사람 아닌 글 포함)에 아무것도 넣지 않는 비율 ≥90%.
func TestHoldoutAcceptance_L5_넣지_않음은_90퍼센트_이상이다(t *testing.T) {
	fixHome(t)
	set, err := bench.Read(holdoutSet)
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
	r := rep.Runs[0].All.Requests.Empty
	if r.Den == 0 || r.Value == nil || *r.Value < 0.9 {
		t.Errorf("판정 묶음 넣지 않음 %d/%d, 기준 ≥90%%", r.Num, r.Den)
	}
}
