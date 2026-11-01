package memplugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestVars_공백이_있는_경로만_따옴표(t *testing.T) {
	if got := Vars(`C:\Users\Kim Lee\orbit-mem.exe`); got["memcmd"] != `"C:/Users/Kim Lee/orbit-mem.exe"` || got["mem"] != `C:\Users\Kim Lee\orbit-mem.exe` {
		t.Fatal(got)
	}
	if Vars("/a/orbit-mem")["memcmd"] != "/a/orbit-mem" {
		t.Fatal("quoted without spaces")
	}
	if Vars("/a/b\tc/orbit-mem")["memcmd"] != "\"/a/b\tc/orbit-mem\"" {
		t.Fatal("tab")
	}
}

// 끝 기준: 흔한 경로면 조각은 원본 파일의 {{mem}}·{{memcmd}}만 바꾼 것과 바이트까지 같다
// (칸 순서·들여쓰기·{{host}}가 그대로라 하네스 빌드가 그대로 받는다).
func TestFragment_자리표시자만_채운다(t *testing.T) {
	got, err := Fragment("/Users/me/.local/bin/orbit-mem")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("{{mem}}", "/Users/me/.local/bin/orbit-mem", "{{memcmd}}", "/Users/me/.local/bin/orbit-mem").Replace(string(source))
	if string(got) != want {
		t.Fatalf("조각:\n%s", got)
	}
	if !strings.Contains(string(got), "--host {{host}}") || strings.Contains(string(got), "{{mem") {
		t.Fatalf("{{host}}만 남아야 합니다:\n%s", got)
	}
}

type fragment struct {
	MCPServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		EnvVars []string `json:"envVars"`
	} `json:"mcpServers"`
	Hooks map[string][]struct {
		Hosts   []string `json:"hosts"`
		Matcher string   `json:"matcher"`
		Command string   `json:"command"`
		Async   bool     `json:"async"`
		Timeout int      `json:"timeout"`
	} `json:"hooks"`
}

// 끝 기준(M4·M5): MCP 서버 이름은 orbit-mem 하나, 훅은 세션 시작·요청·세션 끝 셋뿐이다.
// 작업 기록 훅(PostToolUse·PreToolUse·Stop…)은 없다.
func TestFragment_서버_이름과_훅_셋(t *testing.T) {
	b, err := Fragment("/o/orbit-mem")
	if err != nil {
		t.Fatal(err)
	}
	var f fragment
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.MCPServers) != 1 {
		t.Fatalf("MCP 서버 %v", f.MCPServers)
	}
	srv, ok := f.MCPServers["orbit-mem"]
	if !ok || srv.Command != "/o/orbit-mem" || !slices.Equal(srv.Args, []string{"mcp"}) || !slices.Equal(srv.EnvVars, []string{"ORBIT_HOME"}) {
		t.Fatalf("MCP 서버 %+v", f.MCPServers)
	}
	var events []string
	for e := range f.Hooks {
		events = append(events, e)
	}
	slices.Sort(events)
	if !slices.Equal(events, []string{"SessionEnd", "SessionStart", "UserPromptSubmit"}) {
		t.Fatalf("훅 %v", events)
	}
	for e, want := range map[string]string{"SessionStart": "session-start", "UserPromptSubmit": "prompt", "SessionEnd": "session-end"} {
		hs := f.Hooks[e]
		if len(hs) != 1 || hs[0].Command != "/o/orbit-mem hook "+want+" --host {{host}}" || hs[0].Async {
			t.Errorf("%s: %+v", e, hs)
		}
	}
	if f.Hooks["SessionStart"][0].Matcher != "startup|resume|clear|compact|fork" {
		t.Errorf("SessionStart matcher %q", f.Hooks["SessionStart"][0].Matcher)
	}
	if strings.Contains(string(b), "hook work") {
		t.Errorf("작업 기록 훅이 남았습니다:\n%s", b)
	}
}

// 끝 기준: 따옴표·역슬래시·공백·&가 든 경로도 JSON이 깨지지 않고 값이 정확하다.
func TestFragment_특수문자_경로(t *testing.T) {
	for _, bin := range []string{`C:\Users\Kim "Q" Lee\orbit-mem.exe`, `/tmp/a&b <c>/orbit-mem`, `/x/"y"\z`} {
		b, err := Fragment(bin)
		if err != nil {
			t.Fatal(err)
		}
		var f fragment
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatalf("%s: JSON이 깨졌습니다: %v\n%s", bin, err, b)
		}
		if f.MCPServers["orbit-mem"].Command != bin {
			t.Errorf("%s: MCP %+v", bin, f.MCPServers)
		}
		want := Vars(bin)["memcmd"] + " hook session-start --host {{host}}"
		if got := f.Hooks["SessionStart"][0].Command; got != want {
			t.Errorf("%s: 훅 %q, 기대 %q", bin, got, want)
		}
		if strings.Contains(string(b), `\u0026`) || !strings.HasSuffix(string(b), "}\n") {
			t.Errorf("%s: 모양 %s", bin, b)
		}
	}
	b, _ := Fragment("/o")
	var f fragment
	json.Unmarshal(b, &f)
	if f.Hooks["SessionEnd"][0].Timeout != 3 || f.Hooks["UserPromptSubmit"][0].Timeout != 5 || f.Hooks["SessionStart"][0].Timeout != 10 {
		t.Errorf("시간 제한 %+v", f.Hooks)
	}
}

// 끝 기준(M8): 실험실 후보 빌드는 OpenOrbit orb plugin build <원본> --out <폴더> --fragment <조각 파일>을
// 부르고, 조각 파일은 부르는 동안만 있다.
func TestBuild_orb_plugin_build를_부른다(t *testing.T) {
	var got []string
	var frag []byte
	run := func(name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		frag, _ = os.ReadFile(args[len(args)-1])
		return nil, nil
	}
	if err := Build("/bin/orb", "/src", "/out", "/x/orbit-mem", run); err != nil {
		t.Fatal(err)
	}
	if len(got) != 8 || !slices.Equal(got[:7], []string{"/bin/orb", "plugin", "build", "/src", "--out", "/out", "--fragment"}) {
		t.Fatalf("호출 %q", got)
	}
	want, _ := Fragment("/x/orbit-mem")
	if string(frag) != string(want) {
		t.Fatalf("조각 파일:\n%s", frag)
	}
	if _, err := os.Stat(got[7]); !os.IsNotExist(err) {
		t.Errorf("임시 조각 파일이 남았습니다: %s", got[7])
	}
	// 실패는 그 출력과 함께 알린다
	fail := func(string, ...string) ([]byte, error) {
		return []byte("원본에 plugin.json이 없습니다"), errors.New("exit status 1")
	}
	if err := Build("/bin/orb", "/src", "/out", "/x/orbit-mem", fail); err == nil || !strings.Contains(err.Error(), "원본에 plugin.json이 없습니다") {
		t.Fatalf("실패 %v", err)
	}
}

// 끝 기준(M8): orb는 ORBIT_ORB, PATH, ~/.local/bin/orb 순으로 찾고, 없으면 알기 쉬운 오류.
func TestFindOrb_순서와_없음(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("실행 비트로 찾는다")
	}
	exe := func(dir string) string {
		p := filepath.Join(dir, "orb")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
		return p
	}
	home := t.TempDir()
	pathDir := t.TempDir()
	envOrb := exe(t.TempDir())
	onPath := exe(pathDir)
	local := exe(filepath.Join(home, ".local", "bin"))

	t.Setenv("HOME", home)
	t.Setenv("PATH", pathDir)
	t.Setenv("ORBIT_ORB", envOrb)
	if got, err := FindOrb(); err != nil || got != envOrb {
		t.Fatalf("ORBIT_ORB: %q %v", got, err)
	}
	t.Setenv("ORBIT_ORB", "")
	if got, err := FindOrb(); err != nil || got != onPath {
		t.Fatalf("PATH: %q %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := FindOrb(); err != nil || got != local {
		t.Fatalf("~/.local/bin: %q %v", got, err)
	}
	os.Remove(local)
	if _, err := FindOrb(); err == nil || !errors.Is(err, ErrNoOrb) || !strings.Contains(err.Error(), "ORBIT_ORB") {
		t.Fatalf("없음: %v", err)
	}
	// ORBIT_ORB가 틀린 곳을 가리키면 그대로 알린다(다음 후보로 넘어가지 않는다)
	t.Setenv("ORBIT_ORB", filepath.Join(home, "none"))
	if _, err := FindOrb(); err == nil || !strings.Contains(err.Error(), filepath.Join(home, "none")) {
		t.Fatalf("틀린 ORBIT_ORB: %v", err)
	}
}
