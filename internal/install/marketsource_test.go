package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 끝 기준: 마켓 출처는 claude plugin marketplace add가 받는 꼴(owner/repo, git 주소, 로컬 폴더 절대경로)만 받고,
// github:owner/repo는 owner/repo로, ./·../ 상대 경로는 절대경로로 바꾼다. 옵션처럼 보이거나 빈칸이 든 값은 거절한다.
func TestMarketSource(t *testing.T) {
	cwd, _ := os.Getwd()
	ok := map[string]string{
		"crispebble/orbit-plugins":                        "crispebble/orbit-plugins",
		" crispebble/orbit-plugins ":                      "crispebble/orbit-plugins",
		"github:crispebble/orbit-plugins":                 "crispebble/orbit-plugins",
		"https://github.com/crispebble/orbit-plugins.git": "https://github.com/crispebble/orbit-plugins.git",
		"https://github.com/crispebble/orbit-plugins":     "https://github.com/crispebble/orbit-plugins",
		"git@github.com:crispebble/orbit-plugins.git":     "git@github.com:crispebble/orbit-plugins.git",
		"ssh://git@example.com/team/plugins.git":          "ssh://git@example.com/team/plugins.git",
		"/srv/plugins/catalog/":                           "/srv/plugins/catalog",
		"/Users/a/Library/Application Support/catalog":    "/Users/a/Library/Application Support/catalog",
		"./catalog":  filepath.Join(cwd, "catalog"),
		"../catalog": filepath.Join(filepath.Dir(cwd), "catalog"),
	}
	if runtime.GOOS == "windows" {
		delete(ok, "/srv/plugins/catalog/")
		delete(ok, "/Users/a/Library/Application Support/catalog")
	}
	for in, want := range ok {
		got, err := MarketSource(in)
		if err != nil || got != want {
			t.Errorf("%q → %q %v (기대 %q)", in, got, err, want)
		}
	}
	for _, in := range []string{"", "  ", "-x", "--scope", "orbit-plugins", "a/b/c", "github:", "github:a", "crisp ebble/x", "a/\nb", "catalog/"} {
		if got, err := MarketSource(in); err == nil {
			t.Errorf("%q를 받았습니다: %q", in, got)
		}
	}
}

// 끝 기준: 같은 출처는 표기가 달라도 같다. 같은 GitHub 저장소(owner/repo, github:, https 주소와 .git·끝 /, ssh·scp 꼴,
// 대소문자), 같은 git 주소, 같은 폴더(심볼릭 링크를 풀어서). 다른 저장소·다른 폴더·폴더와 저장소는 다르다.
func TestSameMarketSource(t *testing.T) {
	same := [][2]string{
		{"crispebble/orbit-plugins", "github:crispebble/orbit-plugins"},
		{"crispebble/orbit-plugins", "https://github.com/crispebble/orbit-plugins"},
		{"crispebble/orbit-plugins", "https://github.com/crispebble/orbit-plugins.git"},
		{"crispebble/orbit-plugins", "https://github.com/crispebble/orbit-plugins/"},
		{"crispebble/orbit-plugins", "http://www.github.com/crispebble/orbit-plugins.git"},
		{"crispebble/orbit-plugins", "git@github.com:crispebble/orbit-plugins.git"},
		{"crispebble/orbit-plugins", "ssh://git@github.com/crispebble/orbit-plugins.git"},
		{"CrispEbble/Orbit-Plugins", "https://GitHub.com/crispebble/orbit-plugins.git"},
		{"https://example.com/team/plugins.git", "https://EXAMPLE.com/team/plugins"},
	}
	for _, c := range same {
		if !SameMarketSource(c[0], c[1]) || !SameMarketSource(c[1], c[0]) {
			t.Errorf("같은 출처로 보지 않았습니다: %q %q", c[0], c[1])
		}
	}
	diff := [][2]string{
		{"crispebble/orbit-plugins", "crispebble/orbit-mem"},
		{"crispebble/orbit-plugins", "other/orbit-plugins"},
		{"crispebble/orbit-plugins", "https://gitlab.com/crispebble/orbit-plugins.git"},
		{"https://example.com/team/Plugins.git", "https://example.com/team/plugins.git"}, // GitHub 밖은 경로 대소문자를 가린다
		{"crispebble/orbit-plugins", "/srv/crispebble/orbit-plugins"},
		{"/srv/a", "/srv/b"},
		{"", ""},
	}
	for _, c := range diff {
		if SameMarketSource(c[0], c[1]) {
			t.Errorf("다른 출처를 같다고 봤습니다: %q %q", c[0], c[1])
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "catalog")
	os.Mkdir(real, 0o755)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if !SameMarketSource(real, link) || !SameMarketSource(real+"/", real) {
		t.Errorf("같은 폴더를 다르다고 봤습니다: %s %s", real, link)
	}
}
