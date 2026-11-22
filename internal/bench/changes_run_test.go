package bench_test

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

// 변경을 옮기지 못하는 회상 경로(Env.Change 없음)에 changes가 있는 사례를 주면 조용히 건너뛰지 않고 오류다:
// 건너뛰면 낡은 기억이 그대로 나와 정답이 「틀림」으로 굳는다.
func TestRun_Change가_없는_경로에_changes_사례를_주면_오류다(t *testing.T) {
	p := prepare(t, `{"id":"c1","session":"s1","kind":"start","source":"resume","changes":[{"memory":"a","state":"stale"}]}`)
	_, err := bench.Run(p, func(db string, set *bench.Set) (bench.Env, error) {
		return bench.Env{Recaller: fake{name: "fake", idOf: p.IDOf, calls: new([]string)}, Close: func() error { return nil }}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changes") {
		t.Fatalf("err = %v, want one that names the changes the path cannot apply", err)
	}
}
