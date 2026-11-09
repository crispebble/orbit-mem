package bench_test

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/paths"
)

// 수용 기준을 판정 전용 묶음(holdout_set.json)의 한 번 판정으로 단언한다(계획 1-6 b6 2차). 판정은 d62c9ea의 규칙·기본값으로
// 한 번 쟀고, 그 뒤 값을 고치지 않았다. 기준을 넘은 지표만 단언한다: L-5의 넣지 않음(33/34). 미달인 L-5 적중(55/99)·틀린
// 회상(9/68)과 L-7 놓침 복구(25/35·33/44)는 시험으로 덮지 않고 판정 기록(README.md 「판정 기록」)에 미달로 둔다.
// 위임 행은 cc70ff8(위임 제목 몫 문턱) 뒤 두 번째로 열었다: 판정 전용이 아니고, 판정 기록의 위임 행은 지금 규칙으로 다시 잰
// 값이다(1-6 b10). 요청 쪽 판정 값은 그대로다.
//
// 위 괄호 값(넣지 않음 33/34, 적중 55/99, 틀린 회상 9/68)은 판정 때의 지표 정의로 잰 옛 값이다. 그 뒤 지표 정의가
// 바뀌었다(같은 세션 앞 요청의 회상도 보인 것으로 셈 df5ea32, 세션 소식을 따로 셈 9c8b1a8). 지금 정의로 다시 재면
// (12a00d5, 2026-11-09) 적중 62/99, 틀린 회상 7/63, 넣지 않음 34/34다. 이 값은 다시 연 묶음의 값이라 판정이 아니다.
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
