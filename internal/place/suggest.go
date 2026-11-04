package place

// 제품 지도 첫 표(S3-R5, 명세 2.2): 이 PC의 저장소 원격과 작업 폴더로 제안 표를 만든다. AI가 만들고
// 사람은 틀린 줄만 고친다(설계 · orbit-mem 2판 14절). 설정은 쓰지 않는다.

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// skipDirs are folders the suggestion does not look into.
var skipDirs = map[string]bool{"node_modules": true, "tmp": true, "inbox": true, "templates": true}

func skipped(name string) bool {
	return strings.HasPrefix(name, ".") || skipDirs[name] || strings.HasPrefix(name, "archive")
}

func isRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

var notName = regexp.MustCompile(`[^a-z0-9._-]+`)

// productName turns a repository or folder name into a product name, or "" when nothing is left.
func productName(s string) string {
	n := strings.Trim(notName.ReplaceAllString(strings.ToLower(s), "-"), "-._")
	if !ValidName(n) {
		return ""
	}
	return n
}

// Suggest makes a first product map from the folders under roots (S3-R5): each repository with a git
// remote is a product named after the repository with its remote key line; a repository without one and
// a top folder holding no repository are products with their folder line; a top folder holding two or
// more products adds its folder line to each of them, so it becomes a folder of several products.
// remote gives the normalized remote key of a repository folder ("" for none). It looks two levels deep.
func Suggest(roots []string, remote func(dir string) string, home string) Map {
	out := Map{}
	add := func(name, line string) {
		if name == "" {
			return
		}
		p := out[name]
		if !slices.Contains(p.Match, line) {
			p.Match = append(p.Match, line)
		}
		out[name] = p
	}
	folderLine := func(dir string) string {
		d := path.Clean(filepath.ToSlash(dir))
		h := path.Clean(filepath.ToSlash(home))
		if home != "" && strings.HasPrefix(d, h+"/") {
			return "~/" + d[len(h)+1:]
		}
		return d
	}
	repo := func(dir string) string {
		if key := remote(dir); key != "" {
			name := productName(path.Base(key))
			add(name, key)
			return name
		}
		name := productName(filepath.Base(dir))
		add(name, folderLine(dir))
		return name
	}
	children := func(dir string) []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() && !skipped(e.Name()) {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
		return out
	}
	for _, root := range roots {
		for _, top := range children(root) {
			if isRepo(top) {
				repo(top)
				continue
			}
			var found []string
			for _, sub := range children(top) {
				if isRepo(sub) {
					if name := repo(sub); name != "" && !slices.Contains(found, name) {
						found = append(found, name)
					}
				}
			}
			switch {
			case len(found) == 0:
				add(productName(filepath.Base(top)), folderLine(top))
			case len(found) >= 2:
				for _, name := range found {
					add(name, folderLine(top))
				}
			}
		}
	}
	return out
}
