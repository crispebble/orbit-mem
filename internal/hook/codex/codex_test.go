package codex

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/hook/hooktest"
)

// 계약 시험(ORBIT-23 D5): Codex 훅 입력이 짝 파일의 요청(차례 포함)이 되고, 문맥이 지금과 같은 훅 출력 바이트가 된다.
func TestCodex_훅_입력은_짝_파일의_계약_요청이_된다(t *testing.T) {
	hooktest.Plan(t, New(), "codex", "")
}

// Codex는 CLAUDE_PROJECT_DIR를 주지 않는다: 세션 폴더는 훅 입력의 cwd다. 같은 프로세스에 그 변수가 있어도 읽지 않는다.
func TestCodex_세션_폴더는_입력_cwd다(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "/w/start")
	hooktest.Plan(t, New(), "codex", "")
}

func TestCodex_훅_출력은_지금과_같은_바이트다(t *testing.T) {
	hooktest.Output(t, New(), hooktest.Names)
}
