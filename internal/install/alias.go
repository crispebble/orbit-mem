package install

// 4차 동안의 별칭(ONID-107): 옛 명령 이름 oni를 orb와 같은 바이너리로 남긴다.
// 4차가 끝나면 이 파일, alias_test.go, 그리고 부르는 곳(cmd/orb의 install·uninstall,
// i18n의 cli.install.alias*·cli.uninstall.alias)을 지운다.

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/onidot-labs/openorbit/internal/i18n"
)

// AliasName is the old command name kept as an alias of orb on this OS.
func AliasName() string {
	if runtime.GOOS == "windows" {
		return "oni.exe"
	}
	return "oni"
}

// AliasPath is where the alias of an orb installed at dst lives.
func AliasPath(dst string) string {
	return filepath.Join(filepath.Dir(dst), AliasName())
}

// InstallAlias puts the oni alias next to dst, the installed orb: a relative
// symlink on Mac and Linux, a copy on Windows. Whatever was there (the binary
// an earlier install left, a broken link) is replaced atomically. It reports
// whether anything changed.
func InstallAlias(dst string) (string, bool, error) {
	alias := AliasPath(dst)
	if filepath.Clean(alias) == filepath.Clean(dst) { // 자기 자신을 가리키는 고리를 만들지 않는다
		return alias, false, i18n.Errorf("install.err.alias_self", dst)
	}
	if _, err := os.Stat(dst); err != nil {
		return alias, false, err
	}
	if runtime.GOOS == "windows" {
		changed, err := CopyBinary(dst, alias)
		return alias, changed, err
	}
	target := filepath.Base(dst)
	if t, err := os.Readlink(alias); err == nil && (t == target || t == dst) {
		return alias, false, nil
	}
	tmp := alias + ".new"
	_ = os.Remove(tmp) // 지난번에 남은 것
	if err := os.Symlink(target, tmp); err != nil {
		return alias, false, err
	}
	if err := os.Rename(tmp, alias); err != nil {
		os.Remove(tmp)
		return alias, false, err
	}
	return alias, true, nil
}
