package orbitplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit/internal/pluginbuild"
)

func TestOrbit플러그인_두_호스트로_빌드된다(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	if err := Write(src); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginbuild.Build(pluginbuild.Options{Source: src, Out: out, Vars: Vars("/Users/me/.local/bin/oni")}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(out, "plugins", "orbit", "hooks", "codex.json"))
	if !strings.Contains(string(b), "/Users/me/.local/bin/oni hook session-start --host codex") {
		t.Fatalf("codex hooks: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(out, "plugins", "orbit", ".mcp.json"))
	if !strings.Contains(string(b), `"/Users/me/.local/bin/oni"`) {
		t.Fatalf("mcp: %s", b)
	}
}

func TestVars_공백이_있는_경로만_따옴표(t *testing.T) {
	if Vars(`C:\Users\Kim Lee\oni.exe`)["onicmd"] != `"C:/Users/Kim Lee/oni.exe"` {
		t.Fatal(Vars(`C:\Users\Kim Lee\oni.exe`))
	}
	if Vars("/a/oni")["onicmd"] != "/a/oni" {
		t.Fatal("quoted without spaces")
	}
}
