package i18n

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"en": EN, "ko": KO, "KO": KO, "ko-KR": KO, "ko_KR.UTF-8": KO, "en_US": EN, " en ": EN} {
		if got, ok := Parse(in); !ok || got != want {
			t.Errorf("Parse(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "ja", "korean", "e"} {
		if _, ok := Parse(in); ok {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		env, setting string
		want         Lang
	}{
		{"", "", EN},
		{"", "ko", KO},
		{"en", "ko", EN},
		{"ko", "", KO},
		{"ja", "ko", KO},
		{"ja", "fr", EN},
	}
	for _, c := range cases {
		if got := Resolve(c.env, c.setting); got != c.want {
			t.Errorf("Resolve(%q,%q) = %q; want %q", c.env, c.setting, got, c.want)
		}
	}
}

func TestTAndFallback(t *testing.T) {
	defer Set(Current())
	setCatalog(t, map[Lang]map[string]string{
		EN: {"greet": "hello %s", "only.en": "english"},
		KO: {"greet": "%s 안녕"},
	})
	Set(EN)
	if got := T("greet", "a"); got != "hello a" {
		t.Fatalf("en: %q", got)
	}
	Set(KO)
	if got := T("greet", "a"); got != "a 안녕" {
		t.Fatalf("ko: %q", got)
	}
	if got := T("only.en"); got != "english" {
		t.Fatalf("fallback to en: %q", got)
	}
	if got := T("missing.key"); got != "missing.key" {
		t.Fatalf("missing key: %q", got)
	}
	Set(EN)
	if got := TIn(KO, "greet", "b"); got != "b 안녕" {
		t.Fatalf("TIn: %q", got)
	}
}

func TestErrors(t *testing.T) {
	defer Set(Current())
	setCatalog(t, map[Lang]map[string]string{
		EN: {"e.fixed": "not found", "e.wrap": "open failed: %w"},
		KO: {"e.fixed": "없습니다", "e.wrap": "열지 못했습니다: %w"},
	})
	sentinel := Err("e.fixed")
	Set(EN)
	if sentinel.Error() != "not found" {
		t.Fatalf("en sentinel: %q", sentinel)
	}
	Set(KO)
	if sentinel.Error() != "없습니다" {
		t.Fatalf("sentinel follows the language when read: %q", sentinel)
	}
	inner := errors.New("disk")
	err := Errorf("e.wrap", inner)
	if err.Error() != "열지 못했습니다: disk" || !errors.Is(err, inner) {
		t.Fatalf("wrap: %v", err)
	}
	if !errors.Is(fmt.Errorf("x: %w", sentinel), sentinel) {
		t.Fatal("sentinel keeps its identity")
	}
}

func setCatalog(t *testing.T, c map[Lang]map[string]string) {
	t.Helper()
	old := catalogs
	catalogs = c
	t.Cleanup(func() { catalogs = old })
}

// The shipped catalogs: every language has the same keys, and each message has
// the same format verbs in the same order.
func TestCatalogsMatch(t *testing.T) {
	if len(loadErrs) > 0 {
		t.Fatalf("catalog load: %v", loadErrs)
	}
	en := catalogs[EN]
	if len(en) == 0 {
		t.Fatal("empty English catalog")
	}
	for _, l := range Langs {
		c := catalogs[l]
		for k := range en {
			if _, ok := c[k]; !ok {
				t.Errorf("%s: missing key %s", l, k)
			}
		}
		for k, v := range c {
			e, ok := en[k]
			if !ok {
				t.Errorf("%s: key %s is not in en", l, k)
				continue
			}
			if !slices.Equal(Verbs(v), Verbs(e)) {
				t.Errorf("%s: %s has verbs %v, en has %v", l, k, Verbs(v), Verbs(e))
			}
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: %s is empty", l, k)
			}
		}
	}
	for k, v := range en {
		if strings.ContainsFunc(v, isHangul) {
			t.Errorf("en: %s has Korean: %q", k, v)
		}
	}
}

func TestVerbs(t *testing.T) {
	got := Verbs("a %s b %-7s %d%% %.2f %q %w %v")
	want := []string{"%s", "%-7s", "%d", "%.2f", "%q", "%w", "%v"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
}

func isHangul(r rune) bool { return unicode.Is(unicode.Hangul, r) }

// Every key the code asks for is in the catalog, with as many arguments as the
// English message has verbs.
func TestCodeKeysExist(t *testing.T) {
	root := moduleRoot(t)
	used := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == "studio-plugin" || name == "source" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		alias := importName(f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calledName(call, alias)
			args := call.Args
			if name == "TIn" && len(args) > 0 {
				args = args[1:]
			}
			if name == "" || len(args) == 0 {
				return true
			}
			lit, ok := args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(lit.Value)
			used[key] = true
			msg, ok := catalogs[EN][key]
			if !ok {
				t.Errorf("%s: key %q is not in the catalog", fset.Position(call.Pos()), key)
				return true
			}
			if call.Ellipsis.IsValid() {
				return true
			}
			got := len(args) - 1
			if name == "Err" {
				got = 0
			}
			if v := len(Verbs(msg)); v != got {
				t.Errorf("%s: %q takes %d arguments, got %d", fset.Position(call.Pos()), key, v, got)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for k := range catalogs[EN] {
		if !used[k] && !dynamicKey(k) {
			t.Errorf("key %s is not used in the code", k)
		}
	}
}

// dynamicKey are keys the code builds from a value (a prefix plus a code).
func dynamicKey(k string) bool {
	for _, p := range DynamicPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func importName(f *ast.File) string {
	if f.Name.Name == "i18n" {
		return ""
	}
	for _, imp := range f.Imports {
		if strings.HasSuffix(imp.Path.Value, `/internal/i18n"`) {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return "i18n"
		}
	}
	return "-"
}

func calledName(call *ast.CallExpr, alias string) string {
	var name string
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		x, ok := fn.X.(*ast.Ident)
		if !ok || alias == "" || x.Name != alias {
			return ""
		}
		name = fn.Sel.Name
	case *ast.Ident:
		if alias != "" {
			return ""
		}
		name = fn.Name
	default:
		return ""
	}
	if name == "T" || name == "Errorf" || name == "Err" || name == "TIn" {
		return name
	}
	return ""
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.Abs(".")
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// aiFacing are files whose Korean text is written for the AI (MCP tool
// descriptions and results, retrospective prompts and material, injected
// context) or is data, not text oni shows a person. They are not translated.
var aiFacing = []string{
	"internal/mcpserver/",
	"internal/inject/",
	"internal/orbitplugin/",
	"internal/contractkit/",
	"internal/transcript/",
	"internal/retro/submit.go",
	"internal/retro/material.go",
	"internal/retro/prompt.go",
	"internal/work/read.go",
	"internal/work/query.go",
	"internal/work/spool.go",
}

// Errors a person can read are in the catalog: no errors.New or fmt.Errorf with
// Korean text outside the files written for the AI. A line that keeps Korean
// on purpose says so with an i18n:keep comment.
func TestErrorsAreInTheCatalog(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "studio-plugin" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		for _, p := range aiFacing {
			if strings.HasPrefix(rel, p) {
				return nil
			}
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		lines := strings.Split(string(src), "\n")
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || !(x.Name == "errors" && sel.Sel.Name == "New" || x.Name == "fmt" && sel.Sel.Name == "Errorf") {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || !strings.ContainsFunc(lit.Value, isHangul) {
				return true
			}
			pos := fset.Position(call.Pos())
			if !strings.Contains(lines[pos.Line-1], "i18n:keep") {
				t.Errorf("%s:%d: %s", rel, pos.Line, lit.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
