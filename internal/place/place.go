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
	"slices"
	"sort"
	"strings"
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
	if line == "~" || strings.HasPrefix(line, "~/") {
		line = home + line[1:]
	}
	return path.Clean(filepath.ToSlash(line))
}

// Place is where a session works: its scope key ("project:<key>") and the products the map gives it.
type Place struct {
	Scope    string
	Products []string // 가장 구체적인 줄의 제품, 이름 순. 둘 이상이면 여러 제품 폴더
	names    map[string][]string
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

// Resolve is the place of a session whose project scope is scope, with this user's home.
func Resolve(scope string, m Map) Place {
	home, _ := os.UserHomeDir()
	return ResolveIn(scope, m, home)
}

// ResolveIn is Resolve with home given. A remote key line matches the scope of a repository with that
// remote; a folder line matches a folder scope that is the folder or under it. The most specific lines
// win (a remote key, then the folder with the most path segments); their products are the place's.
func ResolveIn(scope string, m Map, home string) Place {
	p := Place{Scope: scope}
	key := strings.TrimPrefix(scope, projectPrefix)
	if scope == "" || key == scope {
		return p
	}
	folder := isFolder(key)
	var dirs []string
	if folder {
		for d := path.Clean(filepath.ToSlash(key)); ; d = path.Dir(d) {
			dirs = append(dirs, d)
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
				pat := expand(line, filepath.ToSlash(home))
				for _, d := range dirs {
					if ok, _ := path.Match(pat, d); ok {
						spec = max(spec, segments(pat))
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
// user everywhere, a repository in that repository, a product in its folders and in a folder of several
// products that includes it, product:? only in the folder it was saved in.
func (p Place) Sees(scope, project string) bool {
	switch {
	case scope == ScopeGlobal:
		return true
	case scope == ScopeUnknown:
		return p.Scope != "" && project != "" && projectPrefix+project == p.Scope
	case strings.HasPrefix(scope, productPrefix):
		return slices.Contains(p.Products, scope[len(productPrefix):])
	default:
		return p.Scope != "" && scope == p.Scope
	}
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
			if w != "" && strings.Contains(text, w) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}
