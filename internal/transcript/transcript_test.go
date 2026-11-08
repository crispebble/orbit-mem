package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func w(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHostMemoryFiles_두_호스트의_기억_파일(t *testing.T) {
	r := Roots{Claude: t.TempDir(), Codex: t.TempDir()}
	w(t, filepath.Join(r.Claude, "projects", "-w-app", "s1.jsonl"), "{}")
	w(t, filepath.Join(r.Claude, "projects", "-w-app", "memory", "MEMORY.md"), "# idx")
	w(t, filepath.Join(r.Claude, "projects", "-w-app", "memory", "rule.md"), "규칙")
	w(t, filepath.Join(r.Codex, "memories", "MEMORY.md"), "codex")
	w(t, filepath.Join(r.Codex, "memories", "rollout_summaries", "x.md"), "요약")
	files := HostMemoryFiles(r)
	if len(files) != 3 {
		t.Fatalf("files = %v", files)
	}
}

func TestDefaultRoots_환경변수를_따른다(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/c")
	t.Setenv("CODEX_HOME", "/x")
	if r := DefaultRoots(); r.Claude != "/c" || r.Codex != "/x" {
		t.Fatalf("roots = %+v", r)
	}
}
