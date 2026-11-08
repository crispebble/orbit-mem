package claudecode

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/hook/hooktest"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// 계약 시험(ORBIT-23 D5): Claude Code 훅 입력이 짝 파일의 요청(차례 포함)이 되고, 문맥이 지금과 같은 훅 출력 바이트가 된다.
func TestClaudeCode_훅_입력은_짝_파일의_계약_요청이_된다(t *testing.T) {
	hooktest.Plan(t, New(env(nil)), "claude", "")
}

// 세션 폴더는 Claude Code가 훅에 주는 CLAUDE_PROJECT_DIR(세션을 시작한 폴더)이고, 없을 때만 입력 cwd다(S3-R1).
// 안내 줄용 날 cwd(Plan.Cwd)는 입력 그대로다.
func TestClaudeCode_세션_폴더는_CLAUDE_PROJECT_DIR가_먼저다(t *testing.T) {
	hooktest.Plan(t, New(env(map[string]string{"CLAUDE_PROJECT_DIR": "/w/start"})), "claude", "/w/start")
}

func TestClaudeCode_훅_출력은_지금과_같은_바이트다(t *testing.T) {
	hooktest.Output(t, New(env(nil)), hooktest.Names)
}
