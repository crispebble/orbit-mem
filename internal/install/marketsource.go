package install

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// A marketplace source is what claude plugin marketplace add takes: a GitHub owner/repo, a git
// URL, or a local folder. orbit-mem keeps it as given (cleaned) and compares sources by the
// repository or folder they name, so another spelling of the same source is the same
// marketplace (sameness decides between update and remove; see RegisterClaude).

var (
	ownerRepoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+$`)
	// scpRE is git's scp-like address: user@host:path (git@github.com:owner/repo.git).
	scpRE = regexp.MustCompile(`^[A-Za-z0-9._-]+@([A-Za-z0-9.-]+):(.+)$`)
)

// MarketSource checks a marketplace source and returns the form orbit-mem passes to Claude
// Code and keeps in its settings: owner/repo (github:owner/repo becomes owner/repo), a git
// URL as given, or a folder as a clean absolute path (./ and ../ are taken from the working
// folder; a folder may have spaces in its name). Anything else, a value that would read as an
// option, and control characters are refused.
func MarketSource(s string) (string, error) {
	s = strings.TrimSpace(s)
	bad := func() (string, error) { return "", i18n.Errorf("install.err.market_source", s) }
	if s == "" || strings.HasPrefix(s, "-") || strings.ContainsFunc(s, unicode.IsControl) {
		return bad()
	}
	folder := s == "." || s == ".." || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || filepath.IsAbs(s)
	if !folder && strings.ContainsFunc(s, unicode.IsSpace) {
		return bad()
	}
	if rest, ok := strings.CutPrefix(s, "github:"); ok {
		if !ownerRepoRE.MatchString(rest) {
			return bad()
		}
		return rest, nil
	}
	switch {
	case folder:
		abs, err := filepath.Abs(s) // 절대경로는 Clean만 한다
		if err != nil {
			return bad()
		}
		return abs, nil
	case strings.Contains(s, "://") || scpRE.MatchString(s):
		return s, nil
	case ownerRepoRE.MatchString(s):
		return s, nil
	}
	return bad()
}

// SameMarketSource says whether a and b name the same marketplace: one GitHub repository in
// any spelling (owner/repo, github:owner/repo, its https, ssh or scp-like git URL, with or
// without .git; GitHub names ignore case), the same git URL (host case and .git aside), or
// the same folder (symbolic links resolved).
func SameMarketSource(a, b string) bool {
	ka, kb := marketKey(a), marketKey(b)
	return ka != "" && ka == kb
}

// marketKey is what SameMarketSource compares: "dir:<real path>" or "git:<host>/<path>[#ref]".
// "" for no source.
func marketKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if filepath.IsAbs(s) {
		p := filepath.Clean(s)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return "dir:" + p
	}
	s = strings.TrimPrefix(s, "github:")
	if ownerRepoRE.MatchString(s) {
		return gitKey("github.com", s, "")
	}
	if m := scpRE.FindStringSubmatch(s); m != nil && !strings.Contains(s, "://") {
		path, ref, _ := strings.Cut(m[2], "#")
		return gitKey(m[1], path, ref)
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return gitKey(u.Hostname(), u.Path, u.Fragment)
	}
	return "src:" + s
}

func gitKey(host, path, ref string) string {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "github.com" {
		path = strings.ToLower(path)
	}
	k := "git:" + host + "/" + path
	if ref != "" {
		k += "#" + ref
	}
	return k
}

// sourceKey is the marketKey of a listed marketplace, by the field its source type fills.
func (m ClaudeMarket) sourceKey() string {
	switch m.Source {
	case "github":
		return marketKey(m.Repo)
	case "git", "url":
		return marketKey(m.URL)
	case "directory", "file":
		return marketKey(m.Path)
	}
	return ""
}
