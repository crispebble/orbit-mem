package bench_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// 시험이 이 PC의 실제 기억 DB나 홈을 열지 못하게 시험 전체에 임시 홈을 건다.
	tmp, err := os.MkdirTemp("", "orbit-mem-bench-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("ORBIT_HOME", tmp)
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// fixHome pins HOME for a test that needs a fixed place (scope keys come from the path).
func fixHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}
