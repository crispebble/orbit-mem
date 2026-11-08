package hook_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/crispebble/orbit-mem/"

// source is one non-test Go file: its text without the import paths, its imports and its env calls.
type source struct {
	path    string
	text    string
	imports []string
	env     []string
}

// envCalls read the environment: the adapters are given it, the runner never needs it.
var envCalls = map[string]bool{"os.Getenv": true, "os.LookupEnv": true, "os.Environ": true, "os.Getwd": true,
	"os.UserHomeDir": true, "config.Home": true, "config.Load": true}

func sources(t *testing.T, dir string) []source {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []source
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		s := source{path: path, text: string(src)}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			s.imports = append(s.imports, p)
			s.text = strings.Replace(s.text, imp.Path.Value, "", 1)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && envCalls[x.Name+"."+sel.Sel.Name] {
					s.env = append(s.env, x.Name+"."+sel.Sel.Name)
				}
			}
			return true
		})
		out = append(out, s)
	}
	if len(out) == 0 {
		t.Fatalf("%s: 검사한 소스가 없다", dir)
	}
	return out
}

// allowed says whether a package may import p: the standard library and the given module packages.
func allowed(p string, own ...string) bool {
	if !strings.HasPrefix(p, module) {
		return !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") // 표준 라이브러리
	}
	for _, o := range own {
		if p == module+o {
			return true
		}
	}
	return false
}

// 포트-어댑터 의존 방향(T-29, ORBIT-23 D5): 훅 실행기는 포트(internal/host)만 가져오고 호스트를 모른다
// (호스트 이름, 훅 JSON 칸 이름, 환경). 호스트별 모양은 어댑터에만 있다.
func TestHook_실행기는_포트만_보고_호스트를_모른다(t *testing.T) {
	banned := []string{"claude", "codex", "openclaw", "session_id", "hook_event_name", "tool_name", "agent_id", "hookspecificoutput", "userpromptsubmit", "pretooluse"}
	for _, s := range sources(t, ".") {
		for _, p := range s.imports {
			if !allowed(p, "internal/host") {
				t.Errorf("%s imports %s", s.path, p)
			}
		}
		lower := strings.ToLower(s.text)
		for _, w := range banned {
			if strings.Contains(lower, w) {
				t.Errorf("%s: %q", s.path, w)
			}
		}
		if len(s.env) > 0 {
			t.Errorf("%s reads the environment: %v", s.path, s.env)
		}
	}
}

// 어댑터 → internal/hook·internal/host. 도메인(기억·회상)이나 조립 지점(cmd), 다른 어댑터를 가져오지 않고,
// 환경은 받아서 읽는다(시험이 바꿀 수 있게).
func TestHook_어댑터는_훅과_포트만_가져오고_환경을_직접_읽지_않는다(t *testing.T) {
	for _, dir := range []string{"claudecode", "codex"} {
		for _, s := range sources(t, dir) {
			for _, p := range s.imports {
				if !allowed(p, "internal/hook", "internal/host") {
					t.Errorf("%s imports %s", s.path, p)
				}
			}
			if len(s.env) > 0 {
				t.Errorf("%s reads the environment: %v", s.path, s.env)
			}
		}
	}
}
