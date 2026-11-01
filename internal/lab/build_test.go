package lab

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
)

// 끝 기준: Build가 없으면 후보는 OpenOrbit의 orb plugin build <원본> --out <폴더>로 빌드한다(기억 조각 없이:
// 하네스 플러그인만). orb를 찾지 못하면 알기 쉬운 오류로 멈추고, orb가 실패하면 그 출력을 알린다.
func TestBuild_기본은_OpenOrbit_orb_plugin_build(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("가짜 orb는 /bin/sh로 돈다")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	orb := filepath.Join(dir, "orb")
	script := `#!/bin/sh
printf '%s\n' "$*" >>"` + log + `"
out=""
while [ $# -gt 0 ]; do
	case "$1" in
	--out) out=$2; shift ;;
	esac
	shift
done
mkdir -p "$out/plugins/orbit" && printf x >"$out/plugins/orbit/x"
`
	if err := os.WriteFile(orb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORBIT_ORB", orb)
	l := &Lab{Cfg: config.Default(), Paths: config.Paths{Home: t.TempDir()}}
	src, out := t.TempDir(), filepath.Join(t.TempDir(), "market")
	plugin, hash, err := l.build(src, out)
	if err != nil || hash == "" || plugin != filepath.Join(out, "plugins", "orbit") {
		t.Fatalf("%q %q %v", plugin, hash, err)
	}
	calls, _ := os.ReadFile(log)
	if string(calls) != "plugin build "+src+" --out "+out+"\n" {
		t.Fatalf("호출 %q", calls)
	}

	os.WriteFile(orb, []byte("#!/bin/sh\necho 'no plugin.json here' >&2\nexit 1\n"), 0o755)
	if _, _, err := l.build(src, filepath.Join(t.TempDir(), "m3")); err == nil || !strings.Contains(err.Error(), "no plugin.json here") {
		t.Fatalf("orb 실패: %v", err)
	}

	t.Setenv("ORBIT_ORB", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, _, err := l.build(src, filepath.Join(t.TempDir(), "m2")); err == nil || !strings.Contains(err.Error(), "ORBIT_ORB") {
		t.Fatalf("orb 없음: %v", err)
	}
}
