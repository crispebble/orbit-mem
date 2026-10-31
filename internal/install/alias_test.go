package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 명세 · ONID-107: 실행 파일 이름은 orb이고, 4차 동안 oni는 같은 바이너리를 가리키는 별칭이다.
func Test실행_파일_이름은_orb이고_별칭은_oni다(t *testing.T) {
	want, alias := "orb", "oni"
	if runtime.GOOS == "windows" {
		want, alias = "orb.exe", "oni.exe"
	}
	if BinaryName() != want {
		t.Fatalf("BinaryName() = %q, want %q", BinaryName(), want)
	}
	if AliasName() != alias {
		t.Fatalf("AliasName() = %q, want %q", AliasName(), alias)
	}
}

func writeBin(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	sa, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat %s: %v", a, err)
	}
	sb, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(sa, sb)
}

func Test별칭은_새로_깔린_orb를_가리킨다(t *testing.T) {
	dir := t.TempDir()
	orb := filepath.Join(dir, BinaryName())
	writeBin(t, orb, "orb-new")
	alias, changed, err := InstallAlias(orb)
	if err != nil {
		t.Fatal(err)
	}
	if alias != filepath.Join(dir, AliasName()) || !changed {
		t.Fatalf("alias=%q changed=%v", alias, changed)
	}
	b, err := os.ReadFile(alias)
	if err != nil || string(b) != "orb-new" {
		t.Fatalf("alias content %q %v", b, err)
	}
	if runtime.GOOS != "windows" && !sameFile(t, alias, orb) {
		t.Fatal("alias does not point at orb")
	}
}

// 지난 설치가 남긴 옛 oni 실제 파일은 별칭으로 바뀌고, orb는 건드리지 않는다.
func Test옛_oni_실제_파일은_별칭으로_바뀐다(t *testing.T) {
	dir := t.TempDir()
	orb := filepath.Join(dir, BinaryName())
	old := filepath.Join(dir, AliasName())
	writeBin(t, orb, "orb-new")
	writeBin(t, old, "oni-old")
	if _, changed, err := InstallAlias(orb); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if b, _ := os.ReadFile(old); string(b) != "orb-new" {
		t.Fatalf("old oni not replaced: %q", b)
	}
	if b, _ := os.ReadFile(orb); string(b) != "orb-new" {
		t.Fatalf("orb changed: %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Lstat(old); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("oni is not a link: %v %v", fi, err)
		}
	}
}

// 다시 깔아도 같은 상태이고, 이미 맞으면 바꾸지 않는다. 지난번에 남은 임시 파일이 있어도 된다.
func Test별칭_설치는_다시_해도_같다(t *testing.T) {
	dir := t.TempDir()
	orb := filepath.Join(dir, BinaryName())
	writeBin(t, orb, "orb-new")
	writeBin(t, filepath.Join(dir, AliasName())+".new", "leftover")
	if _, _, err := InstallAlias(orb); err != nil {
		t.Fatal(err)
	}
	alias, changed, err := InstallAlias(orb)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && changed {
		t.Fatal("second install changed an alias that already pointed at orb")
	}
	if b, _ := os.ReadFile(alias); string(b) != "orb-new" {
		t.Fatalf("alias content %q", b)
	}
	if _, err := os.Lstat(alias + ".new"); !os.IsNotExist(err) {
		t.Fatalf("temporary file left: %v", err)
	}
}

// 다른 곳을 가리키거나 깨진 옛 링크도 orb로 바로잡는다.
func Test깨진_oni_링크는_orb로_바로잡는다(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows의 별칭은 복사본이다")
	}
	dir := t.TempDir()
	orb := filepath.Join(dir, BinaryName())
	writeBin(t, orb, "orb-new")
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, AliasName())); err != nil {
		t.Fatal(err)
	}
	alias, changed, err := InstallAlias(orb)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !sameFile(t, alias, orb) {
		t.Fatal("alias does not point at orb")
	}
}

// 별칭은 orb가 있어야 만든다. orb가 없으면 실패하고 아무것도 남기지 않는다.
func Test_orb가_없으면_별칭을_만들지_않는다(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := InstallAlias(filepath.Join(dir, BinaryName())); err == nil {
		t.Fatal("expected error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left files: %v", entries)
	}
}

// oni 경로를 넘기면 자기 자신을 가리키는 고리를 만들지 않고 실패한다.
func Test별칭은_자기_자신을_가리키지_않는다(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, AliasName())
	writeBin(t, self, "oni-old")
	if _, _, err := InstallAlias(self); err == nil {
		t.Fatal("expected error")
	}
	if b, err := os.ReadFile(self); err != nil || string(b) != "oni-old" {
		t.Fatalf("oni changed: %q %v", b, err)
	}
}
