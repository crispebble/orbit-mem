package orbitplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// orbit-pi는 역할 정본을 ORBIT_ROLES_DIR에서 읽는다(설계 · OpenOrbit 4차 R4-5).
func TestWriteRoles_역할과_공통_지침을_펼쳐_쓴다(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "roles")
	if err := WriteRoles(dir); err != nil {
		t.Fatal(err)
	}
	src, _ := source.ReadFile("source/agents/worker.md")
	got, err := os.ReadFile(filepath.Join(dir, "agents", "worker.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), strings.SplitN(string(src), "\n", 2)[0]) {
		t.Errorf("worker.md가 원본과 다릅니다")
	}
	if _, err := os.Stat(filepath.Join(dir, "agents-common.md")); err != nil {
		t.Error(err)
	}
	knowledge := filepath.ToSlash(filepath.Join(dir, "knowledge"))
	var expanded bool
	entries, _ := os.ReadDir(filepath.Join(dir, "agents"))
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, "agents", e.Name()))
		if strings.Contains(string(b), "{{") {
			t.Errorf("%s: 펼치지 않은 자리표시자가 남았습니다", e.Name())
		}
		expanded = expanded || strings.Contains(string(b), knowledge)
	}
	if !expanded {
		t.Error("{{knowledgeDir}}가 역할 폴더의 knowledge로 펼쳐지지 않았습니다")
	}
	sig, err := filepath.Glob(filepath.Join(dir, "knowledge", "*", "SIGNALS.md"))
	if err != nil || len(sig) == 0 {
		t.Errorf("분야 지식이 없습니다: %v", err)
	}
}

func TestWriteRoles_같으면_쓰지_않고_낡은_역할은_지운다(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRoles(dir); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "agents", "worker.md")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	_ = os.Chtimes(p, old, old)
	stale := filepath.Join(dir, "agents", "gone.md")
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	if err := WriteRoles(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); !fi.ModTime().Equal(old) {
		t.Errorf("내용이 같은 파일을 다시 썼습니다")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("낡은 역할이 남았습니다")
	}
}
