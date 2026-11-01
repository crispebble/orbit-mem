package openorbit

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

// fakeCLI writes a shell script that stands in for a TS CLI: run with ORBIT_NODE=/bin/sh it
// prints its arguments and the environment the bridge set, reads stdin, and exits with $FAKE_EXIT.
func fakeCLI(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("가짜 CLI는 /bin/sh로 돈다")
	}
	p := filepath.Join(t.TempDir(), "cli.js")
	script := `printf 'args=%s\n' "$*"
printf 'home=%s\n' "$ORBIT_HOME"
printf 'lang=%s\n' "$ORBIT_LANG"
printf 'keep=%s\n' "$KEEP"
if [ -n "$FAKE_STDIN" ]; then printf 'stdin=%s\n' "$(cat)"; fi
if [ -n "$FAKE_STDERR" ]; then printf '%s\n' "$FAKE_STDERR" >&2; fi
exit "${FAKE_EXIT:-0}"
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func executable(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// 끝 기준: 패키지별 환경변수가 있으면 그 CLI를 쓴다.
func TestCLI_환경변수가_앞선다(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "cli.js")
	os.WriteFile(cli, []byte("x"), 0o644)
	r := Runner{Home: t.TempDir(), Env: []string{"ORBIT_WORK_CLI=" + cli, "ORBIT_PI_DIR=" + t.TempDir()}}
	if got, err := r.CLI(Work); err != nil || got != cli {
		t.Fatalf("%q %v", got, err)
	}
	// 다른 패키지의 변수는 상관없다
	if _, err := r.CLI(Harness); err == nil {
		t.Fatal("하네스 CLI는 없어야 합니다")
	}
}

// 끝 기준: 없으면 ORBIT_PI_DIR, 그것도 없으면 $ORBIT_HOME/runtime/orbit-pi의 node_modules에서 찾는다.
func TestCLI_런타임의_node_modules(t *testing.T) {
	home := t.TempDir()
	inHome := filepath.Join(home, "runtime", "orbit-pi", "node_modules", "@onidot-labs", "openorbit-harness", "dist", "cli.js")
	executable(t, inHome)
	r := Runner{Home: home, Env: []string{"PATH=/usr/bin"}}
	if got, err := r.CLI(Harness); err != nil || got != inHome {
		t.Fatalf("홈 런타임: %q %v", got, err)
	}
	pi := t.TempDir()
	inPi := filepath.Join(pi, "node_modules", "@onidot-labs", "openorbit-harness", "dist", "cli.js")
	executable(t, inPi)
	r.Env = append(r.Env, "ORBIT_PI_DIR="+pi)
	if got, err := r.CLI(Harness); err != nil || got != inPi {
		t.Fatalf("ORBIT_PI_DIR: %q %v", got, err)
	}
}

// 끝 기준: CLI가 없으면 orb runtime install|update를 안내하고, 찾은 경로를 보인다.
func TestCLI_없으면_설치를_안내한다(t *testing.T) {
	home := t.TempDir()
	r := Runner{Home: home}
	_, err := r.CLI(Work)
	want := filepath.Join(home, "runtime", "orbit-pi", "node_modules", "@onidot-labs", "openorbit-work", "dist", "cli.js")
	if err == nil || !strings.Contains(err.Error(), "orb runtime install") || !strings.Contains(err.Error(), "orb runtime update") || !strings.Contains(err.Error(), want) {
		t.Fatalf("%v", err)
	}
	// 환경변수가 가리킨 파일이 없어도 안내한다(디렉터리도 CLI가 아니다)
	r.Env = []string{"ORBIT_WORK_CLI=" + t.TempDir()}
	if _, err := r.CLI(Work); err == nil || !strings.Contains(err.Error(), "ORBIT_WORK_CLI") {
		t.Fatalf("%v", err)
	}
}

// 끝 기준: node는 ORBIT_NODE → PATH → 흔한 위치 순서로 찾는다(GUI 앱이 짧은 PATH로 띄워도 돈다).
func TestNode_찾는_순서(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("실행 비트 기반")
	}
	home := t.TempDir()
	onPath := filepath.Join(t.TempDir(), "node")
	executable(t, onPath)
	local := filepath.Join(home, ".local", "bin", "node")
	executable(t, local)
	volta := filepath.Join(home, ".volta", "bin", "node")
	executable(t, volta)

	r := Runner{Env: []string{"HOME=" + home, "PATH=" + filepath.Dir(onPath), "ORBIT_NODE=/bin/sh"}}
	if got, err := r.Node(); err != nil || got != "/bin/sh" {
		t.Fatalf("ORBIT_NODE: %q %v", got, err)
	}
	r.Env = []string{"HOME=" + home, "PATH=" + t.TempDir() + string(os.PathListSeparator) + filepath.Dir(onPath)}
	if got, err := r.Node(); err != nil || got != onPath {
		t.Fatalf("PATH: %q %v", got, err)
	}
	// 짧은 PATH: 흔한 위치. /opt/homebrew/bin·/usr/local/bin에 node가 있는 PC에서는 그것이 먼저다
	r.Env = []string{"HOME=" + home, "PATH=" + t.TempDir()}
	got, err := r.Node()
	if err != nil {
		t.Fatal(err)
	}
	if want := local; got != want && filepath.Dir(got) != "/opt/homebrew/bin" && filepath.Dir(got) != "/usr/local/bin" {
		t.Fatalf("흔한 위치: %q, 기대 %q", got, want)
	}
	os.Remove(local)
	if got, err := r.Node(); err != nil || (got != volta && filepath.Dir(got) != "/opt/homebrew/bin" && filepath.Dir(got) != "/usr/local/bin") {
		t.Fatalf("volta: %q %v", got, err)
	}
	// ORBIT_NODE가 실행할 수 없는 것을 가리키면 다른 곳을 찾지 않고 알린다
	r.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(onPath), "ORBIT_NODE=" + filepath.Join(t.TempDir(), "없음")}
	if _, err := r.Node(); err == nil || !strings.Contains(err.Error(), "ORBIT_NODE") {
		t.Fatalf("ORBIT_NODE 없음: %v", err)
	}
}

// 끝 기준: 자식 환경에 orb가 푼 ORBIT_HOME을 넣고, ORBIT_LANG은 없을(비었을) 때만 orb가 푼 언어로 넣는다. 나머지는 물려준다.
func TestCommand_자식_환경(t *testing.T) {
	cli := fakeCLI(t)
	r := Runner{Home: "/h", Lang: "ko", Env: []string{"ORBIT_NODE=/bin/sh", "ORBIT_WORK_CLI=" + cli, "ORBIT_HOME=/old", "ORBIT_LANG=", "KEEP=1"}}
	argv, env, err := r.Command(Work, "list", "--state", "open")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, " ") != "/bin/sh "+cli+" list --state open" {
		t.Fatalf("argv %v", argv)
	}
	count := map[string]int{}
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		count[k]++
	}
	if envValue(env, "ORBIT_HOME") != "/h" || envValue(env, "ORBIT_LANG") != "ko" || envValue(env, "KEEP") != "1" || count["ORBIT_HOME"] != 1 || count["ORBIT_LANG"] != 1 {
		t.Fatalf("env %v", env)
	}
	r.Env = append(r.Env, "ORBIT_LANG=en")
	_, env, _ = r.Command(Work)
	if envValue(env, "ORBIT_LANG") != "en" {
		t.Fatalf("사용자가 준 ORBIT_LANG을 따라야 합니다: %v", env)
	}
	r.Lang = ""
	r.Env = []string{"ORBIT_NODE=/bin/sh", "ORBIT_WORK_CLI=" + cli}
	if _, env, _ = r.Command(Work); envValue(env, "ORBIT_LANG") != "" {
		t.Fatalf("언어를 모르면 넣지 않는다: %v", env)
	}
}

// 끝 기준: 캡처형은 표준 출력을 돌려주고, 실패하면 그 표준 오류를 담은 오류를 낸다.
func TestOutput_캡처와_실패(t *testing.T) {
	cli := fakeCLI(t)
	r := Runner{Home: "/h", Lang: "en", Env: []string{"ORBIT_NODE=/bin/sh", "ORBIT_HARNESS_CLI=" + cli}}
	out, err := r.Output(Harness, "source")
	if err != nil || !strings.Contains(string(out), "args=source\n") || !strings.Contains(string(out), "home=/h\n") {
		t.Fatalf("%q %v", out, err)
	}
	r.Env = append(r.Env, "FAKE_EXIT=3", "FAKE_STDERR=orb plugin: 원본이 없습니다")
	_, err = r.Output(Harness, "build", "/src")
	var code ExitCode
	if err == nil || !strings.Contains(err.Error(), "orb plugin: 원본이 없습니다") || !strings.Contains(err.Error(), "openorbit-harness build") || !errors.As(err, &code) || code != 3 {
		t.Fatalf("%v", err)
	}
	// 찾지 못하면 실행하지 않고 안내한다
	r.Env = []string{"ORBIT_NODE=/bin/sh"}
	if _, err := r.Output(Work, "list"); err == nil || !strings.Contains(err.Error(), "orb runtime install") {
		t.Fatalf("%v", err)
	}
}

// 끝 기준: 출력을 넘기는 실행은 주어진 입출력에 그대로 잇고 종료 코드를 ExitCode로 준다.
func TestRun_입출력을_잇는다(t *testing.T) {
	cli := fakeCLI(t)
	r := Runner{Home: "/h", Env: []string{"ORBIT_NODE=/bin/sh", "ORBIT_HARNESS_CLI=" + cli, "FAKE_STDIN=1", "FAKE_EXIT=4", "FAKE_STDERR=err"}}
	var out, errb bytes.Buffer
	err := r.Run(Harness, strings.NewReader("입력"), &out, &errb, "install", "/m")
	var code ExitCode
	if !errors.As(err, &code) || code != 4 || !strings.Contains(out.String(), "args=install /m\n") || !strings.Contains(out.String(), "stdin=입력") || errb.String() != "err\n" {
		t.Fatalf("%v %q %q", err, out.String(), errb.String())
	}
}

// Exec는 이 프로세스를 바꿔치기하므로 시험은 자기 자신을 자식으로 띄워 그 안에서 부른다.
func TestExec_통과형(t *testing.T) {
	cli := fakeCLI(t)
	if os.Getenv("OPENORBIT_EXEC_HELPER") == "1" {
		r := Runner{Home: "/h", Lang: "ko", Env: os.Environ()}
		err := r.Exec(Work, "mcp", "--x")
		os.Stderr.WriteString("exec returned: " + errString(err) + "\n")
		os.Exit(99)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExec_통과형$")
	cmd.Env = append(os.Environ(), "OPENORBIT_EXEC_HELPER=1", "ORBIT_NODE=/bin/sh", "ORBIT_WORK_CLI="+cli, "FAKE_EXIT=7", "FAKE_STDIN=1", "ORBIT_LANG=")
	cmd.Stdin = strings.NewReader("요청")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("종료 코드: %v\n%s\n%s", err, out.String(), errb.String())
	}
	for _, want := range []string{"args=mcp --x\n", "home=/h\n", "lang=ko\n", "stdin=요청"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%q가 없습니다: %s", want, out.String())
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
