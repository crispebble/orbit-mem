// Package update replaces this orbit-mem with a release from GitHub Releases and
// remembers (once a day at most) which version is the newest.
//
// Releases are public (onidot-labs/orbit-mem). The newest version comes from the GitHub
// API (/repos/<repo>/releases/latest); an archive is orbit-mem-<version>-<os>-<arch>.tar.gz
// (.zip on windows) and the checksums are the release's SHA256SUMS (shasum -a 256 format).
// ORBIT_MEM_BASE_URL replaces both hosts (tests point it at a fake server).
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/install"
)

// Repo is the public repository the releases come from.
const Repo = "onidot-labs/orbit-mem"

// EnvBaseURL replaces the GitHub hosts (for tests).
const EnvBaseURL = "ORBIT_MEM_BASE_URL"

const (
	defaultAPI      = "https://api.github.com"
	defaultDownload = "https://github.com"

	// DefaultMaxDownload bounds an archive (a release is about 20 MB).
	DefaultMaxDownload int64 = 200 << 20
	maxText                  = 1 << 20 // API answer and SHA256SUMS
	downloadTimeout          = 2 * time.Minute
)

// Result is what a run found and did.
type Result struct {
	Current string
	Latest  string
	Updated bool
	Binary  string
	Steps   []string
}

// Options of Run.
type Options struct {
	Current, Want     string // Want is a version to install instead of the newest
	Target            string // the orbit-mem file to replace
	CheckOnly, DryRun bool
	GOOS, GOARCH      string // default: this machine
	Client            *http.Client
	Base              string                 // default: ORBIT_MEM_BASE_URL, else GitHub
	Reinstall         func(bin string) error // runs `install` of the new binary
	MaxDownload       int64
	Force             bool // allow --version older than Current
}

// Normalize drops a leading v.
func Normalize(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// Compare compares dotted numeric versions (0.9.0 < 0.13.0): -1, 0 or 1.
func Compare(a, b string) int {
	pa, pb := parts(a), parts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parts(v string) []int {
	v = Normalize(v)
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, s := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(s)
		out = append(out, n)
	}
	return out
}

func validVersion(v string) bool {
	if v == "" {
		return false
	}
	for _, s := range strings.Split(v, ".") {
		if _, err := strconv.ParseUint(s, 10, 32); err != nil {
			return false
		}
	}
	return true
}

var platforms = map[string]map[string]bool{
	"linux":   {"amd64": true, "arm64": true},
	"darwin":  {"amd64": true, "arm64": true},
	"windows": {"amd64": true, "arm64": true},
}

// AssetName is the archive of a version for an OS and architecture.
func AssetName(ver, goos, goarch string) (string, error) {
	if !platforms[goos][goarch] {
		return "", i18n.Errorf("update.err.platform", goos, goarch)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "orbit-mem-" + ver + "-" + goos + "-" + goarch + ext, nil
}

func hosts(base string) (api, dl string) {
	if base == "" {
		base = os.Getenv(EnvBaseURL)
	}
	if base == "" {
		return defaultAPI, defaultDownload
	}
	base = strings.TrimRight(base, "/")
	return base, base
}

func get(ctx context.Context, c *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "orbit-mem-update")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, i18n.Errorf("update.err.http", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, i18n.Errorf("update.err.too_big", u, limit)
	}
	return b, nil
}

// Latest is the newest release's version (without v).
func Latest(ctx context.Context, c *http.Client, base string) (string, error) {
	if c == nil {
		c = http.DefaultClient
	}
	api, _ := hosts(base)
	b, err := get(ctx, c, api+"/repos/"+Repo+"/releases/latest", maxText)
	if err != nil {
		return "", err
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	v := Normalize(r.Tag)
	if !validVersion(v) {
		return "", i18n.Errorf("update.err.bad_version", r.Tag)
	}
	return v, nil
}

// Run checks for, and unless CheckOnly or DryRun installs, a release.
func Run(ctx context.Context, o Options) (Result, error) {
	res := Result{Current: Normalize(o.Current), Binary: o.Target, Steps: []string{}}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: downloadTimeout}
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.MaxDownload <= 0 {
		o.MaxDownload = DefaultMaxDownload
	}

	want := Normalize(o.Want)
	if o.Want != "" && !validVersion(want) {
		return res, i18n.Errorf("update.err.bad_version", o.Want)
	}
	if want != "" {
		res.Latest = want
	} else {
		res.Steps = append(res.Steps, i18n.T("update.step.latest"))
		latest, err := Latest(ctx, o.Client, o.Base)
		if err != nil {
			return res, i18n.Errorf("update.err.latest", err)
		}
		res.Latest = latest
	}
	if o.CheckOnly {
		return res, nil
	}
	if want != "" && Compare(want, res.Current) < 0 && !o.Force {
		return res, i18n.Errorf("update.err.downgrade", want, res.Current)
	}
	if Compare(res.Latest, res.Current) == 0 || (want == "" && Compare(res.Latest, res.Current) < 0) {
		res.Steps = append(res.Steps, i18n.T("update.step.up_to_date", res.Current))
		return res, nil
	}
	name, err := AssetName(res.Latest, o.GOOS, o.GOARCH)
	if err != nil {
		return res, err
	}
	res.Steps = append(res.Steps, i18n.T("update.step.download", name))
	res.Steps = append(res.Steps, i18n.T("update.step.verify"))
	res.Steps = append(res.Steps, i18n.T("update.step.replace", o.Target))
	res.Steps = append(res.Steps, i18n.T("update.step.install", o.Target))
	if o.DryRun {
		return res, nil
	}
	_, dl := hosts(o.Base)
	rel := dl + "/" + Repo + "/releases/download/v" + res.Latest + "/"
	archive, err := get(ctx, o.Client, rel+url.PathEscape(name), o.MaxDownload)
	if err != nil {
		return res, err
	}
	sums, err := get(ctx, o.Client, rel+"SHA256SUMS", maxText)
	if err != nil {
		return res, err
	}
	if err := verify(archive, name, sums); err != nil {
		return res, err
	}
	dir, err := os.MkdirTemp("", "orbit-mem-update-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, "orbit-mem")
	if o.GOOS == "windows" {
		err = extractZip(archive, exe, o.MaxDownload)
	} else {
		err = extractTarGz(archive, exe, o.MaxDownload)
	}
	if err != nil {
		return res, err
	}
	if _, err := install.CopyBinary(exe, o.Target); err != nil {
		return res, i18n.Errorf("update.err.replace", err)
	}
	res.Updated = true
	if o.Reinstall != nil {
		if err := o.Reinstall(o.Target); err != nil {
			return res, i18n.Errorf("update.err.reinstall", err)
		}
	}
	return res, nil
}

// verify compares the archive's sha256 with its line of SHA256SUMS (`<hash>  <file>`).
func verify(archive []byte, name string, sums []byte) error {
	h := sha256.Sum256(archive)
	got := hex.EncodeToString(h[:])
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.TrimPrefix(f[1], "*") != name {
			continue
		}
		if strings.EqualFold(f[0], got) {
			return nil
		}
		return i18n.Errorf("update.err.checksum", name)
	}
	return i18n.Errorf("update.err.no_checksum", name)
}

// safeName rejects entry names that leave the archive's folder.
func safeName(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func isExe(name string) bool {
	b := path.Base(name)
	return b == "orbit-mem" || b == "orbit-mem.exe"
}

func writeExe(dst string, r io.Reader, limit int64) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = i18n.Errorf("update.err.too_big", "orbit-mem", limit)
	}
	return err
}

// extractTarGz takes only the orbit-mem executable out; any entry that leaves the folder or
// is a link is refused.
func extractTarGz(archive []byte, dst string, limit int64) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !safeName(h.Name) {
			return i18n.Errorf("update.err.unsafe_entry", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			return i18n.Errorf("update.err.link_entry", h.Name)
		case tar.TypeReg:
			if isExe(h.Name) && !found {
				if err := writeExe(dst, tr, limit); err != nil {
					return err
				}
				found = true
			}
		}
	}
	if !found {
		return i18n.Errorf("update.err.no_binary")
	}
	return nil
}

func extractZip(archive []byte, dst string, limit int64) error {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return err
	}
	found := false
	for _, f := range zr.File {
		if !safeName(f.Name) {
			return i18n.Errorf("update.err.unsafe_entry", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return i18n.Errorf("update.err.link_entry", f.Name)
		}
		if f.Mode().IsRegular() && isExe(f.Name) && !found {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = writeExe(dst, rc, limit)
			rc.Close()
			if err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		return i18n.Errorf("update.err.no_binary")
	}
	return nil
}

// Cache is the answer of the last daily check.
type Cache struct {
	CheckedAt time.Time `json:"checkedAt"`
	Latest    string    `json:"latest"`
}

// CacheTTL is how long an answer is fresh.
const CacheTTL = 24 * time.Hour

func cachePath(home string) string { return filepath.Join(home, "update-check.json") }

// ReadCache reads the last answer under home.
func ReadCache(home string) (Cache, bool) {
	var c Cache
	b, err := os.ReadFile(cachePath(home))
	if err != nil || json.Unmarshal(b, &c) != nil {
		return Cache{}, false
	}
	return c, true
}

// CheckDaily gives the newest version known: a cache younger than a day is used without
// any request; otherwise the newest is asked for (within timeout) and the answer written.
// A failure is silent and is also written (with the earlier answer) so that it is not
// tried again before the day is over.
func CheckDaily(ctx context.Context, home string, now time.Time, c *http.Client, base string, timeout time.Duration) Cache {
	old, ok := ReadCache(home)
	if ok && now.Sub(old.CheckedAt) < CacheTTL && now.After(old.CheckedAt.Add(-CacheTTL)) {
		return old
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	next := Cache{CheckedAt: now, Latest: old.Latest}
	if v, err := Latest(ctx, c, base); err == nil {
		next.Latest = v
	}
	if b, err := json.Marshal(next); err == nil {
		if os.MkdirAll(home, 0o700) == nil {
			tmp := cachePath(home) + ".tmp"
			if os.WriteFile(tmp, b, 0o600) == nil {
				_ = os.Rename(tmp, cachePath(home))
			}
		}
	}
	return next
}
