package legacy_test

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/crispebble/orbit-mem/internal/"

// allowed is what the frozen copy may still use of the live packages (계획 orbit-mem-1-5 「정한 것」 2의 legacy 줄).
// Everything else of the recall is copied into legacy, so a change of memory, place or project outside this list
// cannot move the baseline. Keys: package.Name for a package-level name, package.Type.Name for a method or field;
// "place.*" allows the whole package, "memory.Memory.*" every field of a memory (data, no behaviour).
var allowed = map[string]bool{
	// 사본 DB를 연다.
	"memory.Open": true, "memory.Store": true, "memory.Store.Close()": true, "memory.Store.Now": true,
	// 계획이 남겨 쓰는 저장소 읽기·세션 기록.
	"memory.Store.List()": true, "memory.Store.TopicHeads()": true, "memory.Store.UsageCounts()": true, "memory.Store.Triggered()": true,
	"memory.Store.Core()": true, "memory.Store.SessionBundle()": true, "memory.Store.SessionBundleAt()": true, "memory.Store.SaveSessionBundle()": true,
	"memory.Store.ResetSession()": true, "memory.Store.MarkSession()": true, "memory.Store.SessionMarks()": true,
	"memory.Store.ConfirmedSince()": true, "memory.Store.RecentAutoReverts()": true, "memory.Store.Get()": true,
	"memory.TopicHeads": true, "memory.TopicHeads.Superseding()": true,
	"memory.DomainOf": true, "memory.AgeWeight": true, "memory.CoreEntry": true, "memory.ClipRunes": true, "memory.Day": true,
	// 기억의 값과 이름(행동 없음).
	"memory.Memory": true, "memory.Memory.*": true, "memory.Usage.Followed": true, "memory.Usage.Irrelevant": true,
	"memory.MarkShown": true, "memory.MarkNew": true, "memory.MarkTool": true, "memory.MarkCorrection": true,
	"memory.ScopeGlobal": true, "memory.StateCandidate": true, "memory.StateConfirmed": true, "memory.StateStale": true,
	"memory.KindRule": true, "memory.KindDecision": true, "memory.KindFact": true, "memory.KindLesson": true, "memory.KindJournal": true,
	// cwd → 범위는 호스트와 같은 길.
	"place.*": true, "project.Resolve": true, "project.Project": true, "project.Project.Dir": true, "project.Project.Scope": true,
}

// banned are the packages the frozen copy must not import: the live recall, its settings and the hosts.
var banned = []string{"inject", "config", "mcpserver", "host", "hook"}

func TestLegacy는_허용한_memory_place_project_심볼만_쓴다(t *testing.T) {
	used, imports := legacySymbols(t)
	for _, p := range imports {
		for _, b := range banned {
			if p == module+b || strings.HasPrefix(p, module+b+"/") {
				t.Errorf("legacy imports %s", p)
			}
		}
		if strings.Contains(p, "/cmd/") {
			t.Errorf("legacy imports %s", p)
		}
	}
	if len(used) == 0 {
		t.Fatal("no memory, place or project symbol found: the check reads nothing")
	}
	t.Logf("used: %s", strings.Join(used, " "))
	for _, key := range used {
		if !permitted(key) {
			t.Errorf("legacy uses %s, which is not on the list (copy it into legacy or change the plan)", key)
		}
	}
}

func permitted(key string) bool {
	if allowed[key] {
		return true
	}
	parts := strings.Split(key, ".")
	method := strings.HasSuffix(key, "()")
	return allowed[parts[0]+".*"] || (len(parts) == 3 && !method && allowed[parts[0]+"."+parts[1]+".*"])
}

// legacySymbols type-checks the non-test files of the package and lists the memory, place and project
// symbols they use (sorted, each once) and the paths they import.
func legacySymbols(t *testing.T) ([]string, []string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	var imports []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			imports = append(imports, p)
		}
	}
	if len(parsed) == 0 {
		t.Fatal("no source to check")
	}
	exports := exportData(t)
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	if _, err := (&types.Config{Importer: imp}).Check(module+"bench/legacy", fset, parsed, info); err != nil {
		t.Fatal(err)
	}
	watched := map[string]string{module + "memory": "memory", module + "place": "place", module + "project": "project"}
	seen := map[string]bool{}
	for _, obj := range info.Uses {
		if obj.Pkg() == nil || watched[obj.Pkg().Path()] == "" || obj.Parent() != obj.Pkg().Scope() {
			continue // methods and fields come through Selections with their receiver
		}
		seen[watched[obj.Pkg().Path()]+"."+obj.Name()] = true
	}
	for _, sel := range info.Selections {
		obj := sel.Obj()
		if obj.Pkg() == nil || watched[obj.Pkg().Path()] == "" {
			continue
		}
		recv := sel.Recv()
		if p, ok := recv.(*types.Pointer); ok {
			recv = p.Elem()
		}
		named, ok := recv.(*types.Named)
		if !ok {
			t.Errorf("%s: a selection on an unnamed type", fset.Position(obj.Pos()))
			continue
		}
		key := watched[obj.Pkg().Path()] + "." + named.Obj().Name() + "." + obj.Name()
		if sel.Kind() != types.FieldVal {
			key += "()"
		}
		seen[key] = true
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, imports
}

// exportData maps every dependency of the package to its compiled export data (go list -export).
func exportData(t *testing.T) map[string]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "HOME="+toolHome) // 빌드·모듈 캐시만: 기억 DB는 ORBIT_HOME(임시)이다
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	m := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if path, file, ok := strings.Cut(sc.Text(), "\t"); ok && file != "" {
			m[path] = file
		}
	}
	return m
}
