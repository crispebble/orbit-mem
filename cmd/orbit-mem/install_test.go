package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
)

// 끝 기준(M7): install은 orbit-mem을 bin 폴더에 두고 config와 기억 DB를 만든다. 별칭(oni)을 두지 않고,
// 플러그인을 빌드·등록하지 않으며(OpenOrbit orb install의 일) 그 안내를 한 줄 낸다.
func TestInstall_orbit_mem만_두고_orb_install을_안내한다(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("실행 파일 이름이 다르다")
	}
	h := home(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // claude·codex·orb를 찾지 못하는 PC
	bin := t.TempDir()
	src := filepath.Join(t.TempDir(), "source")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "plugin.json"), []byte("{}"), 0o644)
	out, errOut, code := run(t, "", "install", "--no-schedule", "--bin-dir", bin, "--plugin-source", src, "--apply-target", "/x/AGENTS.md")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	entries, _ := os.ReadDir(bin)
	if len(entries) != 1 || entries[0].Name() != "orbit-mem" {
		t.Fatalf("bin 폴더: %v", entries)
	}
	if !strings.Contains(out, "orb install") || strings.Contains(out, "oni") {
		t.Fatalf("출력: %s", out)
	}
	if _, err := os.Stat(filepath.Join(h, "plugin")); !os.IsNotExist(err) {
		t.Fatalf("플러그인을 빌드했습니다: %v", err)
	}
	cfg, err := config.Load(filepath.Join(h, "config.json"))
	if err != nil || cfg.PluginSource != src || len(cfg.ApplyTargets) != 2 {
		t.Fatalf("config: %+v %v", cfg, err)
	}
	if _, err := os.Stat(filepath.Join(h, "memory.db")); err != nil {
		t.Fatal("기억 DB가 없습니다")
	}
	// 미리 보기에도 별칭·플러그인 빌드 단계가 없다
	out, _, _ = run(t, "", "install", "--dry-run", "--bin-dir", bin)
	if strings.Contains(out, "oni") || strings.Contains(out, "Build the OpenOrbit plugin") || !strings.Contains(out, filepath.Join(bin, "orbit-mem")) {
		t.Fatalf("미리 보기: %s", out)
	}
	for _, flag := range []string{"--enable-codex-plan", "--no-hosts"} {
		if _, errOut, code := run(t, "", "install", "--dry-run", flag); code == 0 || !strings.Contains(errOut, "not defined") {
			t.Errorf("%s는 없앴다: %d %q", flag, code, errOut)
		}
	}
}

// 끝 기준: uninstall은 회고 예약만 해제하고, 플러그인 해제는 orb uninstall이라고 알린다.
func TestUninstall_예약만_해제한다(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("예약 해제가 실제 launchd·schtasks를 부르지 않는 OS에서만 돈다")
	}
	home(t)
	out, errOut, code := run(t, "", "uninstall")
	if code != 0 || !strings.Contains(out, "orb uninstall") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
}

// 끝 기준: 걷어 낸 명령(run·tui·profile·daemon·runtime·work·plugin build|install·memory)은 모르는 명령이다.
func TestUsage_걷어_낸_명령(t *testing.T) {
	home(t)
	for _, args := range [][]string{{"run"}, {"profile"}, {"daemon", "status"}, {"runtime", "status"}, {"work", "list"}, {"tui"}} {
		if _, errOut, code := run(t, "", args...); code != 2 || !strings.Contains(errOut, args[0]) {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	for _, sub := range []string{"build", "install"} {
		if _, _, code := run(t, "", "plugin", sub, "/src"); code != 2 {
			t.Errorf("plugin %s: %d", sub, code)
		}
	}
	if out, _, code := run(t, "", "version"); code != 0 || out != "orbit-mem 0.4.0\n" {
		t.Errorf("version %d %q", code, out)
	}
	if _, errOut, code := run(t, ""); code != 2 || !strings.Contains(errOut, "orbit-mem search") || strings.Contains(errOut, "orb run") {
		t.Errorf("사용법 %q", errOut)
	}
}

// 끝 기준: plugin fragment [--path P]는 그 경로로 채운 조각을 내고 {{host}}만 남긴다.
func TestPluginFragment_path(t *testing.T) {
	home(t)
	out, errOut, code := run(t, "", "plugin", "fragment", "--path", "/x y/orbit-mem")
	if code != 0 || !strings.Contains(out, `"orbit-mem"`) || !strings.Contains(out, `"\"/x y/orbit-mem\" hook prompt --host {{host}}"`) || strings.Contains(out, "{{mem") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if _, errOut, code := run(t, "", "plugin", "fragment", "--orb", "/x"); code == 0 || !strings.Contains(errOut, "not defined") {
		t.Fatalf("--orb는 --path로 바뀌었다: %d %q", code, errOut)
	}
}
