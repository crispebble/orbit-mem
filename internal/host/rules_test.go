package host_test

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

// host 계약 v1은 orbit-mem 쪽 포트다(T-29, ORBIT-23 D3·H2): 비시험 소스는 어댑터(internal/hook)나 조립 지점(cmd)을
// 가져오지 않고, 환경을 읽지 않으며, 호스트·제품 이름과 호스트 입력 모양을 모른다. import 경로와 자기 이름(orbit-mem)은 뺀다.
func TestHost는_호스트와_환경을_모른다(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"claude", "codex", "openclaw", "atlas", "orbit-code", "orbit code", "하네스", "harness", "memory_search",
		"claude_project_dir", "session_id", "hook_event_name", "tool_name", "agent_id", "transcript_path", "hookspecificoutput"}
	// 환경을 읽는 호출: 시간 한도·DB 경로·부모·시각은 만들 때 받는다.
	envCalls := map[string]bool{"os.Getenv": true, "os.LookupEnv": true, "os.Environ": true, "os.Getwd": true, "os.UserHomeDir": true,
		"os.UserConfigDir": true, "os.Executable": true, "config.Home": true, "config.DefaultHome": true, "config.Load": true}
	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.Contains(p, "/internal/hook") || strings.Contains(p, "/cmd/") {
				t.Errorf("%s imports %s", path, p)
			}
			// import 경로는 글자 검사에서 뺀다.
			text = strings.Replace(text, imp.Path.Value, "", 1)
		}
		lower := strings.ToLower(text)
		for _, w := range banned {
			if strings.Contains(lower, w) {
				t.Errorf("%s: %q", path, w)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && envCalls[x.Name+"."+sel.Sel.Name] {
				t.Errorf("%s:%d: %s.%s", path, fset.Position(sel.Pos()).Line, x.Name, sel.Sel.Name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("검사한 소스가 없다")
	}
}
