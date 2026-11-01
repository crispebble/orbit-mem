package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// execAllowed are the files that may import os/exec, and why. None of them runs a model.
var execAllowed = map[string]string{
	"internal/install/install.go":     "registers the plugin in the hosts (claude plugin …, codex config) and removes the old OS schedule (launchctl, schtasks)",
	"cmd/orbit-mem/ops_cmds.go":       "LookPath only: which orbit-mem is on PATH (install, doctor)",
	"internal/memplugin/memplugin.go": "LookPath only: finds the hosts to register with",
	"internal/project/project.go":     "git: resolves a working directory to its project",
	// The lab (orbit-mem lab) is the one documented exception that still runs a headless
	// host: its candidates and its judge. ONID-124 removes the lab and these entries.
	"internal/lab/host.go":         "lab: runs the host (exception until ONID-124)",
	"internal/lab/lab.go":          "lab: orb plugin build for a candidate (exception until ONID-124)",
	"internal/lab/proc_unix.go":    "lab: the host's process group (exception until ONID-124)",
	"internal/lab/proc_windows.go": "lab: the host's process group (exception until ONID-124)",
	"cmd/orbit-mem/lab_cmds.go":    "lab command (exception until ONID-124)",
}

// hostArgs are argument literals of a headless host run: claude -p … and codex exec ….
var hostArgs = []string{"-p", "--print", "--output-format", "--ephemeral", "--strict-mcp-config", "--mcp-config", "--allowedTools",
	"--no-session-persistence", "--skip-git-repo-check", "--dangerously-skip-permissions"}

// K2: orbit-mem keeps the memory and never runs a model or a headless host; the
// retrospectives run in OpenOrbit. Outside the lab no file imports os/exec unless the
// allow-list says why, and no file carries the argument literals of a headless host run.
func TestNoHostCalls(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if name := d.Name(); rel != "." && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata" || name == "studio-plugin" || name == "plugin") {
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
		checked++
		lab := strings.HasPrefix(rel, "internal/lab/") || rel == "cmd/orbit-mem/lab_cmds.go"
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" {
				if _, ok := execAllowed[rel]; !ok {
					t.Errorf("%s imports os/exec: orbit-mem does not start host processes (add it to execAllowed only with a reason that is not a model run)", rel)
				}
			}
		}
		if lab {
			return nil
		}
		var codexExec bool
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, _ := strconv.Unquote(lit.Value)
			for _, a := range hostArgs {
				if s == a {
					t.Errorf("%s:%d has the headless host argument %q", rel, fset.Position(lit.Pos()).Line, s)
				}
			}
			codexExec = codexExec || s == "exec"
			return true
		})
		if codexExec {
			t.Errorf(`%s has the literal "exec" (codex exec): orbit-mem does not run codex`, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("checked only %d files: wrong module root %s", checked, root)
	}
}
