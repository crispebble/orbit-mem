package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	orbBinOnce sync.Once
	orbBinPath string
	orbBinErr  error
)

// orbBin builds this orb once per test run. Bridge commands that hand the process to a TS
// CLI (execve) are run through it so the test binary itself is not replaced.
func orbBin(t *testing.T) string {
	t.Helper()
	orbBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "orb-bridge-test")
		if err != nil {
			orbBinErr = err
			return
		}
		orbBinPath = filepath.Join(dir, "orb")
		if out, err := exec.Command("go", "build", "-o", orbBinPath, ".").CombinedOutput(); err != nil {
			orbBinErr = errors.New(err.Error() + "\n" + string(out))
		}
	})
	if orbBinErr != nil {
		t.Fatal(orbBinErr)
	}
	return orbBinPath
}

func TestMain(m *testing.M) {
	code := m.Run()
	if orbBinPath != "" {
		os.RemoveAll(filepath.Dir(orbBinPath))
	}
	os.Exit(code)
}

// fakeTS writes a shell script that stands in for a TS CLI (run with ORBIT_NODE=/bin/sh). It
// prints its arguments and the environment orb set, echoes stdin when asked, can write
// files a real build would write, and exits with $FAKE_EXIT.
func fakeTS(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("가짜 CLI는 /bin/sh로 돈다")
	}
	p := filepath.Join(t.TempDir(), "cli.js")
	script := `PATH=/usr/bin:/bin
printf 'args=%s\n' "$*"
printf 'home=%s\n' "$ORBIT_HOME"
printf 'lang=%s\n' "$ORBIT_LANG"
if [ -n "$FAKE_LOG" ]; then printf '%s\n' "$*" >>"$FAKE_LOG"; fi
if [ -n "$FAKE_STDIN" ]; then printf 'stdin=%s\n' "$(cat)"; fi
case "$1" in
source) printf '%s\n' "$FAKE_SOURCE" ;;
build)
	if [ -n "$FAKE_BUILD_FAIL" ]; then printf 'orb plugin: %s\n' "$FAKE_BUILD_FAIL" >&2; exit 1; fi
	out=""; frag=""
	while [ $# -gt 0 ]; do
		case "$1" in
		--out) out=$2; shift ;;
		--fragment) frag=$2; shift ;;
		esac
		shift
	done
	if [ -n "$out" ]; then mkdir -p "$out/plugins/orbit"; fi
	if [ -n "$frag" ] && [ -n "$FAKE_FRAGMENT_COPY" ]; then cp "$frag" "$FAKE_FRAGMENT_COPY"; fi
	;;
esac
exit "${FAKE_EXIT:-0}"
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runOrb runs the built orb with a clean temporary HOME and the given environment.
func runOrb(t *testing.T, stdin string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(orbBin(t), args...)
	base := []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	cmd.Env = append(base, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

// 끝 기준: orb work …는 인자·표준 입출력·종료 코드를 그대로 openorbit-work에 넘기고,
// 자식에 orb가 푼 ORBIT_HOME과(없으면) orb의 언어를 준다.
func TestWork_openorbit_work로_넘긴다(t *testing.T) {
	cli := fakeTS(t)
	h := t.TempDir()
	os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"language":"ko"}`), 0o644)
	env := []string{"ORBIT_HOME=" + h, "ORBIT_NODE=/bin/sh", "ORBIT_WORK_CLI=" + cli}
	for _, args := range [][]string{
		{"list", "--state", "open", "--cwd", "."},
		{"get", "w-1", "--children"},
		{"mcp"},
	} {
		out, errOut, code := runOrb(t, "", env, append([]string{"work"}, args...)...)
		if code != 0 || errOut != "" || !strings.Contains(out, "args="+strings.Join(args, " ")+"\n") ||
			!strings.Contains(out, "home="+h+"\n") || !strings.Contains(out, "lang=ko\n") {
			t.Errorf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	out, _, code := runOrb(t, `{"kind":"session.start"}`, append(env, "FAKE_STDIN=1", "FAKE_EXIT=3", "ORBIT_LANG=en"), "work", "emit")
	if code != 3 || !strings.Contains(out, `stdin={"kind":"session.start"}`) || !strings.Contains(out, "lang=en\n") {
		t.Errorf("emit: %d %q", code, out)
	}
}

// 끝 기준: ORBIT_HOME이 환경에 없어도 orb가 푼 기본 홈을 넘긴다(앱이 다른 홈을 보지 않게).
func TestWork_기본_홈을_넘긴다(t *testing.T) {
	cli := fakeTS(t)
	cmd := exec.Command(orbBin(t), "work", "list")
	home := t.TempDir()
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "xdg"), "ORBIT_NODE=/bin/sh", "ORBIT_WORK_CLI=" + cli}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Library", "Application Support", "orbit")
	if runtime.GOOS != "darwin" {
		want = filepath.Join(home, "xdg", "orbit")
	}
	if !strings.Contains(string(out), "home="+want+"\n") || !strings.Contains(string(out), "lang=en\n") {
		t.Errorf("%q", out)
	}
}

// 끝 기준: TS CLI가 없으면 실행하지 않고 orb runtime install|update를 안내한다.
func TestWork_CLI가_없으면_설치를_안내한다(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_WORK_CLI", "")
	t.Setenv("ORBIT_PI_DIR", "")
	_, errOut, code := run(t, "", "work", "list")
	if code != 1 || !strings.Contains(errOut, "orb work: ") || !strings.Contains(errOut, "orb runtime install") || !strings.Contains(errOut, "openorbit-work") {
		t.Fatalf("%d %s", code, errOut)
	}
}
