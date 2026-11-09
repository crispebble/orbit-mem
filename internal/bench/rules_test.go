package bench_test

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

const modulePath = "github.com/crispebble/orbit-mem"

// bench 도메인은 회상 경로를 포트로만 받는다: 비시험 소스는 회상·MCP·호스트·훅·설정 패키지를 가져오지 않고,
// 제품·호스트 이름과 호스트 입력 모양을 모른다(internal/host/rules_test.go와 같은 금지어). import 경로는 글자 검사에서 뺀다.
func TestBench는_회상_경로와_제품을_모른다(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"claude", "codex", "openclaw", "atlas", "orbit-code", "orbit code", "하네스", "harness", "memory_search",
		"claude_project_dir", "session_id", "hook_event_name", "tool_name", "agent_id", "transcript_path", "hookspecificoutput"}
	deps := []string{"/internal/inject", "/internal/mcpserver", "/internal/host", "/internal/hook", "/internal/config", "/cmd/"}
	// 허용 목록: 표준 라이브러리 가운데 순수 계산·입출력 몫과, 지금 쓰는 내부 패키지(기억 저장소 읽기·DB 열기)만 가져온다.
	// 늘릴 때는 이 목록을 고치며 이유를 남긴다.
	allowed := map[string]bool{"bytes": true, "database/sql": true, "encoding/json": true, "errors": true, "fmt": true,
		"hash/fnv": true, "io": true, "math": true, "os": true, "path/filepath": true, "slices": true, "sort": true,
		"strings": true, "time": true, "cmp": true, "maps": true, "strconv": true, "unicode/utf8": true,
		modulePath + "/internal/memory": true, modulePath + "/internal/sqlitedb": true}
	envCalls := map[string]bool{"os.Getenv": true, "os.LookupEnv": true, "os.Environ": true, "os.UserHomeDir": true, "os.UserConfigDir": true}
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
			if !allowed[p] {
				t.Errorf("%s imports %s: 허용 목록에 없다", path, p)
			}
			for _, d := range deps {
				if strings.Contains(p, d) {
					t.Errorf("%s imports %s", path, p)
				}
			}
			text = strings.Replace(text, imp.Path.Value, "", 1)
		}
		lower := strings.ToLower(text)
		for _, w := range banned {
			if strings.Contains(lower, w) {
				t.Errorf("%s: %q", path, w)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && envCalls[x.Name+"."+sel.Sel.Name] {
					t.Errorf("%s:%d: %s.%s", path, fset.Position(sel.Pos()).Line, x.Name, sel.Sel.Name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("검사한 소스가 없다")
	}
}
