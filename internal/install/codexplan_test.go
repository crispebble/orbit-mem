package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 설계 O4: Codex update_plan이 꺼져 있으면 알리고, 사용자가 켜라고 할 때만 config.toml에 켠다.
func TestCodexPlan_켜짐_판단과_켜기(t *testing.T) {
	cases := map[string]bool{
		"":                                      false,
		"model = \"x\"\n":                       false,
		"[tools.update_plan]\nenabled = true\n": true,
		"[tools.update_plan]\nenabled = false\n[other]\nenabled = true\n": false,
		"tools.update_plan.enabled = true\n":                              true,
		"[tools]\nupdate_plan.enabled = true\n":                           true,
		"[tools.update_plan]\n# enabled = true\n":                         false,
	}
	for body, want := range cases {
		dir := t.TempDir()
		if body != "" {
			os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600)
		}
		if got := CodexPlanEnabled(dir); got != want {
			t.Errorf("%q → %v", body, got)
		}
		if err := EnableCodexPlan(dir); err != nil {
			t.Fatal(err)
		}
		if !CodexPlanEnabled(dir) {
			b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
			t.Errorf("켠 뒤에도 꺼져 있습니다: %q → %q", body, b)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
		if body != "" && !strings.Contains(string(b), strings.Split(body, "\n")[0]) {
			t.Errorf("원래 설정을 잃었습니다: %q", b)
		}
		if body != "" && !want {
			if baks, _ := filepath.Glob(filepath.Join(dir, "config.toml.orb-*")); len(baks) != 1 {
				t.Errorf("고치기 전 사본이 없습니다: %v", baks)
			}
		}
	}
}
