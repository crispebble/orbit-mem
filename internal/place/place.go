// Package place says where a memory applies and where a session works (설계 · orbit-mem 2판 4.1,
// S3 명세 2.1~2.3): the applies-to values (user, product:<name>, product:?, repo:<key>) and their
// stored scope values, the product map that ties repositories and folders to products, and which
// memories a session in a folder sees.
package place

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/project"
)

// Stored scope values. The memories table keeps "global" and "project:<key>" as before (옛 판 실행
// 파일과 회고 제출이 같은 DB를 쓴다); S3 adds the two product forms.
const (
	ScopeGlobal     = "global"
	projectPrefix   = "project:"
	productPrefix   = "product:"
	ScopeUnknown    = "product:?"
	appliesUser     = "user"
	appliesRepo     = "repo:"
	remoteSpecifity = 1 << 20 // 원격 키는 어떤 폴더 줄보다 구체적이다
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidName says whether name can name a product.
func ValidName(name string) bool { return nameRE.MatchString(name) && len(name) <= 64 }

// ValidScope says whether s is a stored scope value: global, project:<key>, product:<name> or product:?.
func ValidScope(s string) bool {
	switch {
	case s == ScopeGlobal, s == ScopeUnknown:
		return true
	case strings.HasPrefix(s, projectPrefix):
		return len(s) > len(projectPrefix)
	case strings.HasPrefix(s, productPrefix):
		return ValidName(s[len(productPrefix):])
	}
	return false
}

// AppliesTo is the applies-to value of a stored scope: global → user, project:<key> → repo:<key>.
func AppliesTo(scope string) string {
	switch {
	case scope == ScopeGlobal:
		return appliesUser
	case strings.HasPrefix(scope, projectPrefix):
		return appliesRepo + scope[len(projectPrefix):]
	}
	return scope
}

// ScopeOf is the stored scope of a full applies-to value (user, repo:<key>, product:<name>, product:?).
func ScopeOf(applies string) (string, bool) {
	switch {
	case applies == appliesUser:
		return ScopeGlobal, true
	case applies == ScopeUnknown:
		return ScopeUnknown, true
	case strings.HasPrefix(applies, appliesRepo) && len(applies) > len(appliesRepo):
		return projectPrefix + applies[len(appliesRepo):], true
	case strings.HasPrefix(applies, productPrefix) && ValidName(applies[len(productPrefix):]):
		return applies, true
	}
	return "", false
}

// ProductOf is the product name of a product:<name> scope, or "".
func ProductOf(scope string) string {
	if strings.HasPrefix(scope, productPrefix) && scope != ScopeUnknown {
		return scope[len(productPrefix):]
	}
	return ""
}

// Product is one product of the map: what ties a folder to it and the words that name it in a request.
type Product struct {
	// Match lines: a git remote key (host/owner/repo) or a folder (starting with / or ~), which may hold
	// * ? […] globs. A folder line matches the folder and everything under it.
	Match []string `json:"match"`
	// Names are words that name the product in a request (the product name always does).
	Names []string `json:"names,omitempty"`
}

// Map is the product map: product name → product (config "products").
type Map map[string]Product

// Validate checks a hand-written product map.
func (m Map) Validate() error {
	fail := func(f string, a ...any) error { return fmt.Errorf("products: "+f, a...) }
	for name, p := range m {
		if !ValidName(name) {
			return fail("%q is not a product name (lowercase letters, digits, . _ -)", name)
		}
		if len(p.Match) == 0 {
			return fail("%s has no match lines", name)
		}
		for _, line := range p.Match {
			line = strings.TrimSpace(line)
			switch {
			case line == "":
				return fail("%s has an empty match line", name)
			case isFolder(line):
				if _, err := path.Match(expand(line, "/h"), "/"); errors.Is(err, path.ErrBadPattern) {
					return fail("%s: bad glob %q", name, line)
				}
			case strings.HasPrefix(line, ".") || strings.ContainsAny(line, "*?[\\ "):
				return fail("%s: %q is neither a git remote key nor a folder starting with / or ~", name, line)
			}
		}
		for _, n := range p.Names {
			if strings.TrimSpace(n) == "" {
				return fail("%s has an empty name", name)
			}
		}
	}
	return nil
}

func isFolder(line string) bool {
	return strings.HasPrefix(line, "/") || strings.HasPrefix(line, "~") || filepath.IsAbs(line) || (len(line) > 2 && line[1] == ':' && (line[2] == '/' || line[2] == '\\'))
}

// expand turns ~ into home and the line into a clean slash path.
func expand(line, home string) string {
	line = strings.TrimSpace(line)
	if line == "~" || strings.HasPrefix(line, "~/") || strings.HasPrefix(line, `~\`) {
		line = home + line[1:]
	}
	return path.Clean(filepath.ToSlash(line))
}

// realLine resolves the symbolic links of the part of a folder line before its first glob, as the scope
// of a folder is (project.Resolve), so /tmp/x matches /private/tmp/x on macOS.
func realLine(pat string) string {
	parts := strings.Split(pat, "/")
	i := len(parts)
	for j, seg := range parts {
		if strings.ContainsAny(seg, "*?[") {
			i = j
			break
		}
	}
	head := strings.Join(parts[:i], "/")
	if head == "" {
		return pat
	}
	real, err := filepath.EvalSymlinks(filepath.FromSlash(head))
	if err != nil {
		return pat
	}
	return path.Join(append([]string{filepath.ToSlash(real)}, parts[i:]...)...)
}

// foldCase says whether folders compare without case: the default file systems of macOS and Windows.
var foldCase = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

func fold(s string) string {
	if foldCase {
		return strings.ToLower(s)
	}
	return s
}

// Place is where a session works: its scope key ("project:<key>"), the session folder and the products
// the map gives it.
type Place struct {
	Scope    string
	Products []string // 가장 구체적인 줄의 제품, 이름 순. 둘 이상이면 여러 제품 폴더
	// Dir is the session folder as a real slash path (S3 명세 2.2b): the folder whose parents a session
	// inherits the memories of (S3-R2). For a folder scope without a session folder it is the folder.
	Dir   string
	home  string
	names map[string][]string
}

// Multi says whether the folder belongs to several products (a parent folder like ~/_workspace/onidot).
func (p Place) Multi() bool { return len(p.Products) > 1 }

// Product is the one product of the folder, or "" when it has none or several.
func (p Place) Product() string {
	if len(p.Products) == 1 {
		return p.Products[0]
	}
	return ""
}

// Label is the scope key without its prefix (the remote key or the folder).
func (p Place) Label() string { return strings.TrimPrefix(p.Scope, projectPrefix) }

// Resolve is the place of a session, with this user's home. where is the session folder (an absolute
// path: its project is resolved) or a scope key ("project:<key>"); "" is no place.
func Resolve(where string, m Map) Place {
	home, _ := os.UserHomeDir()
	if where != "" && !strings.HasPrefix(where, projectPrefix) {
		p := project.Resolve(where)
		return AtIn(p.Scope, p.Dir, m, home)
	}
	return ResolveIn(where, m, home)
}

// At is the place of a session whose project scope is scope and whose folder is dir.
func At(scope, dir string, m Map) Place {
	home, _ := os.UserHomeDir()
	return AtIn(scope, dir, m, home)
}

// AtIn is At with home given. An empty dir is the folder of a folder scope.
func AtIn(scope, dir string, m Map, home string) Place {
	p := ResolveIn(scope, m, home)
	if dir != "" {
		p.Dir = path.Clean(filepath.ToSlash(dir))
	}
	return p
}

// ResolveIn is the place of a scope key with home given. A remote key line matches the scope of a repository with that
// remote; a folder line matches a folder scope that is the folder or under it (a repository with a remote
// is found by its remote key only, S3 명세 2.2). The most specific lines win: a remote key, then the folder
// with the most path segments, a plain folder before a glob of as many; their products are the place's.
func ResolveIn(scope string, m Map, home string) Place {
	p := Place{Scope: scope, home: path.Clean(filepath.ToSlash(home))}
	key := strings.TrimPrefix(scope, projectPrefix)
	if scope == "" || key == scope {
		return p
	}
	folder := isFolder(key)
	if folder {
		p.Dir = path.Clean(filepath.ToSlash(key))
	}
	var dirs []string
	if folder {
		for d := path.Clean(filepath.ToSlash(key)); ; d = path.Dir(d) {
			dirs = append(dirs, fold(d))
			if next := path.Dir(d); next == d {
				break
			}
		}
	}
	best := 0
	for name, prod := range m {
		spec := 0
		for _, line := range prod.Match {
			line = strings.TrimSpace(line)
			switch {
			case !isFolder(line):
				if !folder && strings.EqualFold(line, key) {
					spec = remoteSpecifity
				}
			case folder:
				pat := fold(realLine(expand(line, filepath.ToSlash(home))))
				plain := 0
				if !strings.ContainsAny(pat, "*?[") {
					plain = 1
				}
				for _, d := range dirs {
					if ok, _ := path.Match(pat, d); ok {
						spec = max(spec, 2*segments(pat)+plain)
						break
					}
				}
			}
		}
		switch {
		case spec == 0:
		case spec > best:
			best, p.Products = spec, []string{name}
		case spec == best:
			p.Products = append(p.Products, name)
		}
	}
	sort.Strings(p.Products)
	p.names = map[string][]string{}
	for _, name := range p.Products {
		words := []string{strings.ToLower(name)}
		for _, n := range m[name].Names {
			words = append(words, strings.ToLower(strings.TrimSpace(n)))
		}
		p.names[name] = words
	}
	return p
}

func segments(p string) int { return len(strings.Split(strings.Trim(p, "/"), "/")) }

// Sees says whether a memory with this stored scope (and saving folder, for product:?) applies here:
// user everywhere, a product in its folders and in a folder of several products that includes it, a
// repository or a folder (repo:<key>, product:? with its folder) in that repository or folder and in
// every folder under it (S3-R2: the session inherits the memories of its parent folders).
func (p Place) Sees(scope, project string) bool {
	switch {
	case scope == ScopeGlobal:
		return true
	case scope == ScopeUnknown:
		return project != "" && p.within(project)
	case strings.HasPrefix(scope, productPrefix):
		return slices.Contains(p.Products, scope[len(productPrefix):])
	case strings.HasPrefix(scope, projectPrefix):
		return p.within(scope[len(projectPrefix):])
	}
	return false
}

// within says whether the session works in the repository or folder key: the session's own scope key, or
// a folder that is the session folder or one of its parents. Parents stop below home (the folder right
// under home is the last), and outside home above the top folder (/opt is not a parent, /opt/work is).
func (p Place) within(key string) bool {
	if p.Scope != "" && projectPrefix+key == p.Scope {
		return true
	}
	if p.Dir == "" || !isFolder(key) {
		return false
	}
	k, d := fold(path.Clean(filepath.ToSlash(key))), fold(p.Dir)
	if k == d {
		return true
	}
	if !strings.HasPrefix(d, strings.TrimSuffix(k, "/")+"/") {
		return false
	}
	h := fold(p.home)
	if p.home != "" && (d == h || strings.HasPrefix(d, h+"/")) {
		return strings.HasPrefix(k, h+"/")
	}
	return segments(k) >= 2 && !(p.home != "" && (k == h || strings.HasPrefix(h, k+"/")))
}

// Default is the stored scope of a memory saved here without an applies-to (S3-R1): the folder's product;
// in a folder of several products the one its text names (S3-R2b); else, in a git checkout, this
// repository; else product:? (this folder). A temporary folder is always product:? (S3-R4).
func (p Place) Default(git, temp bool, text string) string {
	switch {
	case p.Product() != "":
		return productPrefix + p.Product()
	case p.Multi() && p.Narrow(text) != "":
		return productPrefix + p.Narrow(text)
	case temp:
	case git && p.Scope != "":
		return p.Scope
	}
	return ScopeUnknown
}

// Local says whether a memory belongs to this folder more than to the user: this repository, the
// folder's product, the products a request names in a folder of several, the folder of a product:?.
func (p Place) Local(scope, project string, named []string) bool {
	switch {
	case scope == ScopeGlobal:
		return false
	case strings.HasPrefix(scope, productPrefix) && scope != ScopeUnknown:
		name := scope[len(productPrefix):]
		return (p.Product() == name) || slices.Contains(named, name)
	default:
		return p.Sees(scope, project)
	}
}

// Named are the products of this place that a request names (a product name or one of its names).
func (p Place) Named(prompt string) []string {
	text := strings.ToLower(prompt)
	var out []string
	for _, name := range p.Products {
		for _, w := range p.names[name] {
			if w != "" && containsWord(text, w) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// Narrow is the one product of this folder that a saved memory's text names (S3-R2b: a folder of several
// products narrows to the product named once), or "" when it names none or several. Words only, no model.
func (p Place) Narrow(text string) string {
	if named := p.Named(text); len(named) == 1 {
		return named[0]
	}
	return ""
}

// containsWord finds w in text as a word: no ASCII letter or digit right before or after it, so forma is
// not in format but is in "forma의" (lowercase text and w).
func containsWord(text, w string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], w)
		if j < 0 {
			return false
		}
		at := i + j
		end := at + len(w)
		if (at == 0 || !asciiWord(text[at-1])) && (end == len(text) || !asciiWord(text[end])) {
			return true
		}
		i = at + 1
	}
}

func asciiWord(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
