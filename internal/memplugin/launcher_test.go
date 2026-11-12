package memplugin

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 런처 시험은 launchd로 뜬 앱처럼 PATH가 /usr/bin:/bin뿐인 환경(env -i)에서 생성된 명령을 /bin/sh로 돌린다.
// Windows에는 sh가 없어 건너뛴다.

type launcherEnv struct {
	home   string // 임시 HOME
	pathD  string // PATH에 더하는 폴더("" 이면 좁은 PATH만)
	marker string
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("런처는 /bin/sh 꼴이라 Windows에는 해당하지 않습니다")
	}
}

// fakeBin은 dir/orbit-mem에 "나는 who, 인자: $*"를 찍는 가짜 실행 파일을 둔다.
func fakeBin(t *testing.T, dir, who string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"" + who + " $*\"\n"
	if err := os.WriteFile(filepath.Join(dir, "orbit-mem"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (e launcherEnv) run(t *testing.T, shArgs ...string) (stdout, stderr string, code int) {
	t.Helper()
	path := "/usr/bin:/bin"
	if e.pathD != "" {
		path = e.pathD + ":" + path
	}
	cmd := exec.Command("/bin/sh", shArgs...)
	cmd.Env = []string{"HOME=" + e.home, "PATH=" + path}
	var o, er bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &er
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatal(err)
	}
	return o.String(), er.String(), code
}

func mcpLauncher(t *testing.T) []string {
	t.Helper()
	files, err := Claude("0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	mustJSON(t, files, ".mcp.json", &mcp)
	s := mcp.MCPServers["orbit-mem"]
	if s.Command != "/bin/sh" {
		t.Fatalf("command %q", s.Command)
	}
	return s.Args
}

func hookLine(t *testing.T, event string) string {
	t.Helper()
	files, err := Claude("0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	var h hookFile
	mustJSON(t, files, "hooks/hooks.json", &h)
	return h.Hooks[event][0].Hooks[0].Command
}

func TestMCP런처_좁은_PATH에서_기본_설치_폴더를_찾는다(t *testing.T) {
	skipWindows(t)
	args := mcpLauncher(t)
	e := launcherEnv{home: t.TempDir()}
	fakeBin(t, filepath.Join(e.home, ".local", "bin"), "home")
	out, errOut, code := e.run(t, args...)
	if code != 0 || strings.TrimSpace(out) != "home mcp" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

func TestMCP런처_어디에도_없으면_127과_안내(t *testing.T) {
	skipWindows(t)
	e := launcherEnv{home: t.TempDir()}
	out, errOut, code := e.run(t, mcpLauncher(t)...)
	if code != 127 || out != "" || !strings.Contains(errOut, "orbit-mem 실행 파일을 찾지 못했습니다") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

func TestMCP런처_PATH가_기본_설치_폴더보다_우선한다(t *testing.T) {
	skipWindows(t)
	e := launcherEnv{home: t.TempDir(), pathD: t.TempDir()}
	fakeBin(t, filepath.Join(e.home, ".local", "bin"), "home")
	fakeBin(t, e.pathD, "path")
	out, errOut, code := e.run(t, mcpLauncher(t)...)
	if code != 0 || strings.TrimSpace(out) != "path mcp" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

func TestHook_좁은_PATH에서_기본_설치_폴더를_찾는다(t *testing.T) {
	skipWindows(t)
	e := launcherEnv{home: t.TempDir()}
	fakeBin(t, filepath.Join(e.home, ".local", "bin"), "home")
	out, errOut, code := e.run(t, "-c", hookLine(t, "SessionStart"))
	if code != 0 || strings.TrimSpace(out) != "home hook session-start --host claude --plugin-version 0.4.0" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

func TestHook_어디에도_없으면_exit_0(t *testing.T) {
	skipWindows(t)
	e := launcherEnv{home: t.TempDir()}
	out, errOut, code := e.run(t, "-c", hookLine(t, "SessionStart"))
	if code != 0 || errOut != "" || !strings.Contains(out, `"hookEventName":"SessionStart"`) || !strings.Contains(out, "orbit-mem install") {
		t.Fatalf("세션 시작: code %d out %q err %q", code, out, errOut)
	}
	out, errOut, code = e.run(t, "-c", hookLine(t, "UserPromptSubmit"))
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("요청: code %d out %q err %q", code, out, errOut)
	}
}

func TestHook_PATH가_기본_설치_폴더보다_우선한다(t *testing.T) {
	skipWindows(t)
	e := launcherEnv{home: t.TempDir(), pathD: t.TempDir()}
	fakeBin(t, filepath.Join(e.home, ".local", "bin"), "home")
	fakeBin(t, e.pathD, "path")
	out, _, code := e.run(t, "-c", hookLine(t, "PreToolUse"))
	if code != 0 || !strings.HasPrefix(out, "path hook pre-tool ") {
		t.Fatalf("code %d out %q", code, out)
	}
}
