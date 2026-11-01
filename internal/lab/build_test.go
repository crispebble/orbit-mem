package lab

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memplugin"
)

// 끝 기준(M8): Build가 없으면 후보는 OpenOrbit orb plugin build <원본> --out <폴더> --fragment <조각>으로
// 빌드한다. orb를 찾지 못하면 알기 쉬운 오류로 멈춘다.
func TestBuild_기본은_OpenOrbit_orb_plugin_build(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("가짜 orb는 /bin/sh로 돈다")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	orb := filepath.Join(dir, "orb")
	script := `#!/bin/sh
printf '%s\n' "$*" >>"` + log + `"
out=""; frag=""
while [ $# -gt 0 ]; do
	case "$1" in
	--out) out=$2; shift ;;
	--fragment) frag=$2; shift ;;
	esac
	shift
done
mkdir -p "$out/plugins/orbit" && cp "$frag" "$out/plugins/orbit/fragment.json"
`
	if err := os.WriteFile(orb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORBIT_ORB", orb)
	l := &Lab{Cfg: config.Default(), Paths: config.Paths{Home: t.TempDir()}, Mem: "/opt/orbit-mem"}
	src, out := t.TempDir(), filepath.Join(t.TempDir(), "market")
	plugin, hash, err := l.build(src, out)
	if err != nil || hash == "" || plugin != filepath.Join(out, "plugins", "orbit") {
		t.Fatalf("%q %q %v", plugin, hash, err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.HasPrefix(string(calls), "plugin build "+src+" --out "+out+" --fragment ") {
		t.Fatalf("호출 %q", calls)
	}
	got, _ := os.ReadFile(filepath.Join(plugin, "fragment.json"))
	want, _ := memplugin.Fragment("/opt/orbit-mem")
	if string(got) != string(want) {
		t.Fatalf("조각:\n%s", got)
	}

	t.Setenv("ORBIT_ORB", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, _, err := l.build(src, filepath.Join(t.TempDir(), "m2")); err == nil || !strings.Contains(err.Error(), "ORBIT_ORB") {
		t.Fatalf("orb 없음: %v", err)
	}
}
