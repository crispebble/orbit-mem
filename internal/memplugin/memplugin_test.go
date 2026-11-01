package memplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/openorbit"
)

func TestVars_공백이_있는_경로만_따옴표(t *testing.T) {
	if got := Vars(`C:\Users\Kim Lee\orb.exe`); got["orbcmd"] != `"C:/Users/Kim Lee/orb.exe"` || got["orb"] != `C:\Users\Kim Lee\orb.exe` {
		t.Fatal(got)
	}
	if Vars("/a/orb")["orbcmd"] != "/a/orb" {
		t.Fatal("quoted without spaces")
	}
	if Vars("/a/b\tc/orb")["orbcmd"] != "\"/a/b\tc/orb\"" {
		t.Fatal("tab")
	}
}

// 끝 기준: 흔한 경로면 조각은 원본 파일의 {{orb}}·{{orbcmd}}만 바꾼 것과 바이트까지 같다
// (칸 순서·들여쓰기·{{host}}가 그대로라 하네스 빌드 결과가 옛 Go 빌드와 같다).
func TestFragment_자리표시자만_채운다(t *testing.T) {
	got, err := Fragment("/Users/me/.local/bin/orb")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("{{orb}}", "/Users/me/.local/bin/orb", "{{orbcmd}}", "/Users/me/.local/bin/orb").Replace(string(source))
	if string(got) != want {
		t.Fatalf("조각:\n%s", got)
	}
	if !strings.Contains(string(got), "--host {{host}}") || strings.Contains(string(got), "{{orb") {
		t.Fatalf("{{host}}만 남아야 합니다:\n%s", got)
	}
}

type fragment struct {
	MCPServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
	Hooks map[string][]struct {
		Command string `json:"command"`
		Async   bool   `json:"async"`
		Timeout int    `json:"timeout"`
	} `json:"hooks"`
}

// 끝 기준: 따옴표·역슬래시·공백·&가 든 경로도 JSON이 깨지지 않고 값이 정확하다.
func TestFragment_특수문자_경로(t *testing.T) {
	for _, orb := range []string{`C:\Users\Kim "Q" Lee\orb.exe`, `/tmp/a&b <c>/orb`, `/x/"y"\z`} {
		b, err := Fragment(orb)
		if err != nil {
			t.Fatal(err)
		}
		var f fragment
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatalf("%s: JSON이 깨졌습니다: %v\n%s", orb, err, b)
		}
		if f.MCPServers["orbit"].Command != orb || strings.Join(f.MCPServers["orbit"].Args, " ") != "mcp" {
			t.Errorf("%s: MCP %+v", orb, f.MCPServers)
		}
		want := Vars(orb)["orbcmd"] + " hook session-start --host {{host}}"
		if got := f.Hooks["SessionStart"][0].Command; got != want {
			t.Errorf("%s: 훅 %q, 기대 %q", orb, got, want)
		}
		if strings.Contains(string(b), `\u0026`) || !strings.HasSuffix(string(b), "}\n") {
			t.Errorf("%s: 모양 %s", orb, b)
		}
	}
	// 숫자·불은 그대로
	b, _ := Fragment("/o")
	var f fragment
	json.Unmarshal(b, &f)
	if f.Hooks["SessionEnd"][0].Timeout != 3 || f.Hooks["Stop"][0].Timeout != 5 || f.Hooks["Stop"][0].Async {
		t.Errorf("값 %+v", f.Hooks)
	}
	if pt := f.Hooks["PostToolUse"]; len(pt) != 3 || !pt[2].Async {
		t.Errorf("PostToolUse %+v", pt)
	}
}

// 끝 기준: Build는 orb 경로로 채운 조각을 임시 파일로 넘겨 openorbit-harness build를 부르고, 임시 파일을 지운다.
// 실패하면 하네스의 stderr를 담는다.
func TestBuild_하네스_빌드를_부른다(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("가짜 CLI는 /bin/sh로 돈다")
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, "cli.js")
	os.WriteFile(cli, []byte(`printf '%s\n' "$@" >"$FAKE_ARGS"
while [ $# -gt 0 ]; do [ "$1" = --fragment ] && cp "$2" "$FAKE_FRAG"; shift; done
if [ -n "$FAKE_FAIL" ]; then echo "$FAKE_FAIL" >&2; exit 1; fi
`), 0o755)
	argsFile, fragFile := filepath.Join(dir, "args"), filepath.Join(dir, "frag")
	r := openorbit.Runner{Home: dir, Env: []string{"PATH=/usr/bin:/bin", "ORBIT_NODE=/bin/sh", "ORBIT_HARNESS_CLI=" + cli, "FAKE_ARGS=" + argsFile, "FAKE_FRAG=" + fragFile}}
	if err := Build(r, "/src", "/out", "/a b/orb"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(args) != 10 || strings.Join(args[:4], " ") != "build /src --out /out" || args[4] != "--fragment" ||
		strings.Join(args[6:], "|") != `--var|orb=/a b/orb|--var|orbcmd="/a b/orb"` {
		t.Fatalf("인자 %q", args)
	}
	if _, err := os.Stat(args[5]); !os.IsNotExist(err) {
		t.Errorf("임시 조각이 남았습니다: %s", args[5])
	}
	got, _ := os.ReadFile(fragFile)
	want, _ := Fragment("/a b/orb")
	if string(got) != string(want) {
		t.Errorf("조각 %s", got)
	}
	r.Env = append(r.Env, "FAKE_FAIL=원본이 없습니다")
	if err := Build(r, "/src", "/out", "/o"); err == nil || !strings.Contains(err.Error(), "원본이 없습니다") {
		t.Fatalf("%v", err)
	}
}
