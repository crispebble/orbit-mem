package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// 훅이 뒤에서 띄우는 프로세스(backup auto·tidy auto)는 기본이 가짜다: 시험 바이너리를 다시 띄우지 않는다.
	startBackground = func(args ...string) error { return nil }
	// 새 판 확인(세션 시작 훅)이 시험 중 실제 GitHub로 나가지 않는다. 연결이 바로 거절되는 주소로 둔다.
	os.Setenv("ORBIT_MEM_BASE_URL", "http://127.0.0.1:1")
	// 시험이 t.Setenv를 되돌린 뒤 남은 고루틴이 이 PC의 실제 기억 DB를 열지 못하게 임시 홈을 건다.
	// 각 시험의 t.Setenv가 이보다 앞선다.
	tmp, err := os.MkdirTemp("", "orbit-mem-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("ORBIT_HOME", tmp)
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
