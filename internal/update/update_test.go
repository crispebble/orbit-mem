package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCompare_숫자로_비교한다(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.9.0", "0.13.0", -1},
		{"0.13.0", "0.9.0", 1},
		{"0.13.0", "0.13.0", 0},
		{"v0.13.0", "0.13.0", 0},
		{"0.13.1", "0.13.0", 1},
		{"1.0.0", "0.99.99", 1},
		{"0.13", "0.13.0", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}

type entry struct {
	name, body, link string
	typ              byte
}

func tarGz(t *testing.T, es ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range es {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o755, Linkname: e.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type fake struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	latestTag string
	archives  map[string][]byte // asset name -> bytes
	sums      string            // "" = computed from archives
	badSum    bool
}

func (f *fake) count(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.paths {
		if strings.Contains(p, sub) {
			n++
		}
	}
	return n
}

func newFake(t *testing.T, tag string) *fake {
	f := &fake{latestTag: tag, archives: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		const rel = "/onidot-labs/orbit-mem/releases/download/"
		switch {
		case r.URL.Path == "/repos/onidot-labs/orbit-mem/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q}`, f.latestTag)
		case strings.HasPrefix(r.URL.Path, rel) && strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			if f.sums != "" {
				fmt.Fprint(w, f.sums)
				return
			}
			for name, b := range f.archives {
				h := sha256.Sum256(b)
				s := hex.EncodeToString(h[:])
				if f.badSum {
					s = strings.Repeat("0", 64)
				}
				fmt.Fprintf(w, "%s  %s\n", s, name)
			}
		case strings.HasPrefix(r.URL.Path, rel):
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if b, ok := f.archives[name]; ok {
				w.Write(b)
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func target(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "orbit-mem")
	if err := os.WriteFile(p, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func content(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func opts(f *fake, tgt, cur string) Options {
	return Options{Current: cur, Target: tgt, GOOS: "linux", GOARCH: "amd64", Base: f.srv.URL, Client: f.srv.Client(), Reinstall: func(string) error { return nil }}
}

const asset = "orbit-mem-0.14.0-linux-amd64.tar.gz"

func TestRun_최신이_더_높으면_교체한다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	tgt := target(t)
	var reinstalled string
	o := opts(f, tgt, "0.13.0")
	o.Reinstall = func(bin string) error { reinstalled = bin; return nil }
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.Current != "0.13.0" || res.Latest != "0.14.0" || res.Binary != tgt || len(res.Steps) == 0 {
		t.Fatalf("res = %+v", res)
	}
	if content(t, tgt) != "NEW" {
		t.Fatalf("파일 = %q", content(t, tgt))
	}
	if reinstalled != tgt {
		t.Fatalf("reinstall = %q", reinstalled)
	}
}

func TestRun_최신이_같거나_낮으면_그대로_둔다(t *testing.T) {
	for _, cur := range []string{"0.14.0", "0.15.0"} {
		f := newFake(t, "v0.14.0")
		f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
		tgt := target(t)
		called := false
		o := opts(f, tgt, cur)
		o.Reinstall = func(string) error { called = true; return nil }
		res, err := Run(context.Background(), o)
		if err != nil || res.Updated || res.Latest != "0.14.0" || res.Current != cur {
			t.Fatalf("%s: %+v %v", cur, res, err)
		}
		if content(t, tgt) != "OLD" || called || f.count("/download/") != 0 {
			t.Fatalf("%s: 바뀜 %q called=%v", cur, content(t, tgt), called)
		}
	}
}

func TestRun_0_9_0은_0_13_0보다_낮다(t *testing.T) {
	f := newFake(t, "v0.13.0")
	f.archives["orbit-mem-0.13.0-linux-amd64.tar.gz"] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	tgt := target(t)
	res, err := Run(context.Background(), opts(f, tgt, "0.9.0"))
	if err != nil || !res.Updated || content(t, tgt) != "NEW" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRun_체크섬이_다르면_실행_파일은_그대로다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	f.badSum = true
	tgt := target(t)
	called := false
	o := opts(f, tgt, "0.13.0")
	o.Reinstall = func(string) error { called = true; return nil }
	res, err := Run(context.Background(), o)
	if err == nil || res.Updated {
		t.Fatalf("통과함 %+v %v", res, err)
	}
	if content(t, tgt) != "OLD" || called {
		t.Fatalf("바뀜 %q", content(t, tgt))
	}
}

func TestRun_체크섬_줄이_없으면_거절한다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	f.sums = strings.Repeat("a", 64) + "  other.tar.gz\n"
	tgt := target(t)
	if _, err := Run(context.Background(), opts(f, tgt, "0.13.0")); err == nil || content(t, tgt) != "OLD" {
		t.Fatalf("err=%v", err)
	}
}

func TestRun_교체가_실패하면_옛_파일을_보존한다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	tgt := target(t)
	if err := os.Mkdir(tgt+".new", 0o755); err != nil { // CopyBinary가 임시 파일을 만들 수 없게 한다
		t.Fatal(err)
	}
	called := false
	o := opts(f, tgt, "0.13.0")
	o.Reinstall = func(string) error { called = true; return nil }
	res, err := Run(context.Background(), o)
	if err == nil || res.Updated || content(t, tgt) != "OLD" || called {
		t.Fatalf("%+v %v %q", res, err, content(t, tgt))
	}
}

func TestRun_check는_받지_않고_알린다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	tgt := target(t)
	o := opts(f, tgt, "0.13.0")
	o.CheckOnly = true
	res, err := Run(context.Background(), o)
	if err != nil || res.Updated || res.Latest != "0.14.0" {
		t.Fatalf("%+v %v", res, err)
	}
	if content(t, tgt) != "OLD" || f.count("/download/") != 0 {
		t.Fatal("check가 받거나 바꿈")
	}
}

func TestRun_dry_run은_할_일만_보인다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	tgt := target(t)
	o := opts(f, tgt, "0.13.0")
	o.DryRun = true
	res, err := Run(context.Background(), o)
	if err != nil || res.Updated || len(res.Steps) == 0 || res.Binary != tgt {
		t.Fatalf("%+v %v", res, err)
	}
	if content(t, tgt) != "OLD" || f.count("/download/") != 0 {
		t.Fatal("dry-run이 받거나 바꿈")
	}
}

func TestRun_version으로_특정_판을_받는다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives["orbit-mem-0.12.0-linux-amd64.tar.gz"] = tarGz(t, entry{name: "orbit-mem", body: "OLDER"})
	tgt := target(t)
	o := opts(f, tgt, "0.13.0")
	o.Want = "v0.12.0"
	o.Force = true // 낮은 판이라 --force가 필요하다
	res, err := Run(context.Background(), o)
	if err != nil || !res.Updated || res.Latest != "0.12.0" || content(t, tgt) != "OLDER" {
		t.Fatalf("%+v %v", res, err)
	}
	if f.count("/releases/latest") != 0 {
		t.Fatal("판을 정했는데 최신을 물음")
	}
}

func TestRun_알_수_없는_OS와_arch는_거절한다(t *testing.T) {
	for _, p := range [][2]string{{"plan9", "amd64"}, {"linux", "riscv64"}} {
		f := newFake(t, "v0.14.0")
		tgt := target(t)
		o := opts(f, tgt, "0.13.0")
		o.GOOS, o.GOARCH = p[0], p[1]
		if _, err := Run(context.Background(), o); err == nil || content(t, tgt) != "OLD" || f.count("/download/") != 0 {
			t.Fatalf("%v: err=%v", p, err)
		}
	}
}

func TestRun_tar의_경로_이탈과_심볼릭_링크는_거절한다(t *testing.T) {
	bad := map[string][]entry{
		"경로 이탈":    {{name: "../evil", body: "x"}, {name: "orbit-mem", body: "NEW"}},
		"절대 경로":    {{name: "/etc/evil", body: "x"}, {name: "orbit-mem", body: "NEW"}},
		"심볼릭 링크":   {{name: "orbit-mem", typ: tar.TypeSymlink, link: "/bin/sh"}},
		"하드 링크":    {{name: "orbit-mem", typ: tar.TypeLink, link: "/bin/sh"}},
		"실행 파일 없음": {{name: "README", body: "x"}},
	}
	for name, es := range bad {
		f := newFake(t, "v0.14.0")
		f.archives[asset] = tarGz(t, es...)
		tgt := target(t)
		res, err := Run(context.Background(), opts(f, tgt, "0.13.0"))
		if err == nil || res.Updated || content(t, tgt) != "OLD" {
			t.Errorf("%s: 거절하지 않음 %+v %v", name, res, err)
		}
	}
}

func TestRun_하위_폴더_안의_실행_파일도_꺼낸다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem-0.14.0/", typ: tar.TypeDir}, entry{name: "orbit-mem-0.14.0/orbit-mem", body: "NEW"})
	tgt := target(t)
	if _, err := Run(context.Background(), opts(f, tgt, "0.13.0")); err != nil || content(t, tgt) != "NEW" {
		t.Fatalf("err=%v", err)
	}
}

func TestRun_windows는_zip에서_꺼낸다(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("orbit-mem.exe")
	w.Write([]byte("WIN"))
	zw.Close()
	f := newFake(t, "v0.14.0")
	f.archives["orbit-mem-0.14.0-windows-amd64.zip"] = buf.Bytes()
	tgt := target(t)
	o := opts(f, tgt, "0.13.0")
	o.GOOS = "windows"
	if _, err := Run(context.Background(), o); err != nil || content(t, tgt) != "WIN" {
		t.Fatalf("err=%v %q", err, content(t, tgt))
	}
}

func TestRun_교체_뒤_install이_실패하면_오류와_함께_교체는_알린다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: "NEW"})
	tgt := target(t)
	o := opts(f, tgt, "0.13.0")
	o.Reinstall = func(string) error { return fmt.Errorf("boom") }
	res, err := Run(context.Background(), o)
	if err == nil || !res.Updated || content(t, tgt) != "NEW" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRun_다운로드_크기_상한을_넘으면_거절한다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives[asset] = tarGz(t, entry{name: "orbit-mem", body: strings.Repeat("x", 4096)})
	tgt := target(t)
	o := opts(f, tgt, "0.13.0")
	o.MaxDownload = 100
	if _, err := Run(context.Background(), o); err == nil || content(t, tgt) != "OLD" {
		t.Fatalf("err=%v", err)
	}
}

func TestLatest_태그에서_v를_뗀다(t *testing.T) {
	f := newFake(t, "v0.20.1")
	got, err := Latest(context.Background(), f.srv.Client(), f.srv.URL)
	if err != nil || got != "0.20.1" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestCheckDaily_신선한_캐시면_요청하지_않는다(t *testing.T) {
	f := newFake(t, "v0.20.0")
	home := t.TempDir()
	now := time.Now()
	c := CheckDaily(context.Background(), home, now, f.srv.Client(), f.srv.URL, time.Second)
	if c.Latest != "0.20.0" || f.count("/releases/latest") != 1 {
		t.Fatalf("첫 확인 %+v", c)
	}
	if got, ok := ReadCache(home); !ok || got.Latest != "0.20.0" {
		t.Fatalf("캐시 %+v %v", got, ok)
	}
	c = CheckDaily(context.Background(), home, now.Add(23*time.Hour), f.srv.Client(), f.srv.URL, time.Second)
	if c.Latest != "0.20.0" || f.count("/releases/latest") != 1 {
		t.Fatalf("신선한데 요청함 %+v n=%d", c, f.count("/releases/latest"))
	}
	f.latestTag = "v0.21.0"
	c = CheckDaily(context.Background(), home, now.Add(25*time.Hour), f.srv.Client(), f.srv.URL, time.Second)
	if c.Latest != "0.21.0" || f.count("/releases/latest") != 2 {
		t.Fatalf("묵은 캐시 %+v", c)
	}
}

func TestCheckDaily_실패해도_조용히_하루_한_번만_시도한다(t *testing.T) {
	f := newFake(t, "v0.20.0")
	f.srv.Close()
	home := t.TempDir()
	now := time.Now()
	c := CheckDaily(context.Background(), home, now, http.DefaultClient, f.srv.URL, 500*time.Millisecond)
	if c.Latest != "" {
		t.Fatalf("%+v", c)
	}
	if got, ok := ReadCache(home); !ok || !got.CheckedAt.After(now.Add(-time.Minute)) {
		t.Fatalf("실패도 시각을 남겨야 한다 %+v %v", got, ok)
	}
}

func TestCheckDaily_제한_시간_안에_돌아온다(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(3 * time.Second) }))
	defer slow.Close()
	start := time.Now()
	CheckDaily(context.Background(), t.TempDir(), time.Now(), slow.Client(), slow.URL, 300*time.Millisecond)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("너무 오래 걸림 %v", time.Since(start))
	}
}

func TestRun_낮은_판_지정은_force_없이는_거절한다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	f.archives["orbit-mem-0.12.0-linux-amd64.tar.gz"] = tarGz(t, entry{name: "orbit-mem", body: "OLDER"})
	tgt := target(t)
	called := false
	o := opts(f, tgt, "0.13.0")
	o.Want = "0.12.0"
	o.Reinstall = func(string) error { called = true; return nil }
	res, err := Run(context.Background(), o)
	if err == nil || res.Updated || content(t, tgt) != "OLD" || called || f.count("/download/") != 0 || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("%+v %v", res, err)
	}
	o.Force = true
	res, err = Run(context.Background(), o)
	if err != nil || !res.Updated || content(t, tgt) != "OLDER" {
		t.Fatalf("force %+v %v", res, err)
	}
}

func TestRun_같은_판_지정은_불변이다(t *testing.T) {
	f := newFake(t, "v0.14.0")
	tgt := target(t)
	o := opts(f, tgt, "0.13.0")
	o.Want = "0.13.0"
	o.Force = true
	res, err := Run(context.Background(), o)
	if err != nil || res.Updated || content(t, tgt) != "OLD" {
		t.Fatalf("%+v %v", res, err)
	}
}
