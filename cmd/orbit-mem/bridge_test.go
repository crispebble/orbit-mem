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

	"github.com/onidot-labs/orbit-mem/internal/memplugin"
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
if [ "$1" != source ]; then
	printf 'args=%s\n' "$*"
	printf 'home=%s\n' "$ORBIT_HOME"
	printf 'lang=%s\n' "$ORBIT_LANG"
fi
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
	if [ -n "$out" ] && [ -n "$frag" ]; then cp "$frag" "$out/plugins/orbit/fragment.json"; fi
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

// 끝 기준: orb plugin fragment는 이 orb(또는 --orb) 경로로 채운 기억 MCP·훅 조각을 낸다.
func TestPluginFragment_조각을_낸다(t *testing.T) {
	home(t)
	out, errOut, code := run(t, "", "plugin", "fragment", "--orb", `/x y/"q"/orb`)
	want, _ := memplugin.Fragment(`/x y/"q"/orb`)
	if code != 0 || out != string(want) {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	out, _, code = run(t, "", "plugin", "fragment")
	want, _ = memplugin.Fragment(selfPath())
	if code != 0 || out != string(want) {
		t.Fatalf("기본은 지금 orb: %s", out)
	}
	if _, _, code := run(t, "", "plugin", "fragment", "extra"); code != 2 {
		t.Fatalf("위치 인자는 받지 않는다: %d", code)
	}
}

// 끝 기준: orb plugin build|install은 인자 그대로 openorbit-harness build|install에 넘기고 종료 코드를 잇는다.
func TestPlugin_build와_install은_하네스로_넘긴다(t *testing.T) {
	cli := fakeTS(t)
	h := t.TempDir()
	env := []string{"ORBIT_HOME=" + h, "ORBIT_NODE=/bin/sh", "ORBIT_HARNESS_CLI=" + cli}
	out, errOut, code := runOrb(t, "", env, "plugin", "build", "src", "--check", "--var", "a=b c", "--fragment", "f.json")
	if code != 0 || errOut != "" || !strings.Contains(out, "args=build src --check --var a=b c --fragment f.json\n") || !strings.Contains(out, "home="+h+"\n") {
		t.Fatalf("build: %d %q %q", code, out, errOut)
	}
	out, _, code = runOrb(t, "", append(env, "FAKE_EXIT=1"), "plugin", "install", "/m")
	if code != 1 || !strings.Contains(out, "args=install /m\n") {
		t.Fatalf("install: %d %q", code, out)
	}
}

func readLines(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// installEnv points orb install at a fake harness and keeps it away from this PC's hosts.
func installEnv(t *testing.T) (h, log, src string) {
	t.Helper()
	cli := fakeTS(t)
	h = home(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin") // 이 PC의 claude·codex를 찾지 않는다
	t.Setenv("CODEX_HOME", "")
	t.Setenv("ORBIT_NODE", "/bin/sh")
	t.Setenv("ORBIT_HARNESS_CLI", cli)
	log = filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("FAKE_LOG", log)
	src = filepath.Join(t.TempDir(), "harness-source")
	t.Setenv("FAKE_SOURCE", src)
	return h, log, src
}

// 끝 기준: orb install은 설치할 orb 경로로 채운 조각과 하네스 원본(openorbit-harness source)으로
// openorbit-harness build를 부르고, --no-hosts가 아니면 openorbit-harness install로 등록한다.
func TestInstall_하네스로_빌드하고_등록한다(t *testing.T) {
	h, log, src := installEnv(t)
	bin := t.TempDir()
	dst := filepath.Join(bin, "orb")
	out, errOut, code := run(t, "", "install", "--no-schedule", "--bin-dir", bin)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	market := filepath.Join(h, "plugin")
	calls := readLines(t, log)
	if len(calls) != 3 || calls[0] != "source" || calls[2] != "install "+market+" --codex-home "+filepath.Join(os.Getenv("HOME"), ".codex") {
		t.Fatalf("호출: %q", calls)
	}
	build := strings.Fields(calls[1])
	if len(build) != 10 || build[0] != "build" || build[1] != src || build[2] != "--out" || build[3] != market || build[4] != "--fragment" ||
		build[6] != "--var" || build[7] != "orb="+dst || build[8] != "--var" || build[9] != "orbcmd="+dst {
		t.Fatalf("빌드: %q", calls[1])
	}
	if _, err := os.Stat(build[5]); !os.IsNotExist(err) {
		t.Errorf("임시 조각 파일이 남았습니다: %s", build[5])
	}
	got, _ := os.ReadFile(filepath.Join(market, "plugins", "orbit", "fragment.json"))
	want, _ := memplugin.Fragment(dst)
	if string(got) != string(want) {
		t.Errorf("조각은 설치할 orb 경로로 채운다:\n%s", got)
	}
	if !strings.Contains(out, "Built the OpenOrbit plugin: "+market+" (source built-in)\n") || !strings.Contains(out, "args=install "+market) {
		t.Errorf("출력: %s", out)
	}
	if strings.Contains(out, "args=build") || strings.Contains(out, "args=source") {
		t.Errorf("빌드 출력은 받아 두고 내지 않는다: %s", out)
	}

	// --no-hosts: 등록하지 않는다. --plugin-source: 그 원본으로 빌드한다
	os.Remove(log)
	own := filepath.Join(t.TempDir(), "own")
	os.MkdirAll(own, 0o755)
	os.WriteFile(filepath.Join(own, "plugin.json"), []byte("{}"), 0o644)
	out, errOut, code = run(t, "", "install", "--no-schedule", "--no-hosts", "--bin-dir", bin, "--plugin-source", own)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	calls = readLines(t, log)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "build "+own+" --out "+market+" --fragment ") || !strings.Contains(out, "(source "+own+")") {
		t.Fatalf("원본 지정: %q %s", calls, out)
	}
}

// 끝 기준: 하네스 빌드가 실패하면 그 오류(stderr)를 담아 실패하고 등록하지 않는다.
func TestInstall_빌드_실패를_알린다(t *testing.T) {
	_, log, _ := installEnv(t)
	t.Setenv("FAKE_BUILD_FAIL", "원본에 plugin.json이 없습니다")
	_, errOut, code := run(t, "", "install", "--no-schedule", "--bin-dir", t.TempDir())
	calls := readLines(t, log)
	if code != 1 || !strings.Contains(errOut, "원본에 plugin.json이 없습니다") || len(calls) != 2 || !strings.HasPrefix(calls[1], "build ") {
		t.Fatalf("%d %s %q", code, errOut, calls)
	}
	// 하네스를 찾지 못하면 런타임 설치를 안내한다
	t.Setenv("ORBIT_HARNESS_CLI", "")
	if _, errOut, code := run(t, "", "install", "--no-schedule", "--bin-dir", t.TempDir()); code != 1 || !strings.Contains(errOut, "orb runtime install") {
		t.Fatalf("%d %s", code, errOut)
	}
}

// 끝 기준: orb run은 orbit-pi에 넘기기 전에 openorbit-harness roles --out <ORBIT_HOME/runtime/roles>로
// 역할 폴더를 쓰고, 실패하면 넘기지 않는다.
func TestRun_역할_폴더는_하네스가_쓴다(t *testing.T) {
	cli := fakeTS(t)
	h, pi, bin := t.TempDir(), t.TempDir(), t.TempDir()
	entry := filepath.Join(pi, "packages", "orbit-orchestra", "src", "cli.ts")
	os.MkdirAll(filepath.Dir(entry), 0o755)
	os.WriteFile(entry, []byte(""), 0o644)
	os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\necho \"node $*\"\n"), 0o755)
	log := filepath.Join(t.TempDir(), "calls.log")
	env := []string{"PATH=" + bin + ":/usr/bin:/bin", "ORBIT_HOME=" + h, "ORBIT_PI_DIR=" + pi, "ORBIT_NODE=/bin/sh", "ORBIT_HARNESS_CLI=" + cli, "FAKE_LOG=" + log}
	out, errOut, code := runOrb(t, "", env, "run", "일")
	if code != 0 || !strings.Contains(out, "node "+entry+" run 일") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if calls := readLines(t, log); len(calls) != 1 || calls[0] != "roles --out "+filepath.Join(h, "runtime", "roles") {
		t.Fatalf("역할: %q", calls)
	}
	out, errOut, code = runOrb(t, "", append(env, "FAKE_EXIT=1"), "run", "일")
	if code != 1 || strings.Contains(out, "node ") || !strings.Contains(errOut, "role folder") {
		t.Fatalf("실패: %d %q %q", code, out, errOut)
	}
}
