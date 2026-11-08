package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/update"
	"github.com/crispebble/orbit-mem/internal/version"
)

// releaseServer is a fake GitHub: the newest tag is latest, and its archive holds body.
func releaseServer(t *testing.T, latest, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "orbit-mem", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	name := fmt.Sprintf("orbit-mem-%s-%s-%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(buf.Bytes())
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprintf(w, `{"tag_name":"v%s"}`, latest)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		case strings.HasSuffix(r.URL.Path, name):
			w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

type updEnv struct {
	target      string
	reinstalled []string
}

func updateSetup(t *testing.T, srv *httptest.Server) *updEnv {
	t.Helper()
	home(t)
	t.Setenv("ORBIT_MEM_BASE_URL", srv.URL)
	e := &updEnv{target: filepath.Join(t.TempDir(), "orbit-mem")}
	os.WriteFile(e.target, []byte("OLD"), 0o755)
	oldT, oldR := updateTarget, updateReinstall
	updateTarget = func() string { return e.target }
	updateReinstall = func(bin string, out, errw io.Writer) error { e.reinstalled = append(e.reinstalled, bin); return nil }
	t.Cleanup(func() { updateTarget, updateReinstall = oldT, oldR })
	return e
}

func nextVersion() string { return "99.0.0" }

func TestUpdate_json은_정해진_키로_낸다(t *testing.T) {
	srv, _ := releaseServer(t, nextVersion(), "NEW")
	e := updateSetup(t, srv)
	out, errOut, code := run(t, "", "update", "--json")
	if code != 0 {
		t.Fatalf("code %d %s", code, errOut)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("%q %v", out, err)
	}
	for _, k := range []string{"ok", "current", "latest", "updated", "binary", "steps"} {
		if _, has := raw[k]; !has {
			t.Errorf("키 %s가 없음: %s", k, out)
		}
	}
	if raw["ok"] != true || raw["updated"] != true || raw["current"] != version.Version || raw["latest"] != nextVersion() || raw["binary"] != e.target {
		t.Fatalf("값 %s", out)
	}
	if _, has := raw["error"]; has {
		t.Error("성공인데 error 키")
	}
	if b, _ := os.ReadFile(e.target); string(b) != "NEW" {
		t.Fatalf("파일 %q", b)
	}
	if len(e.reinstalled) != 1 || e.reinstalled[0] != e.target {
		t.Fatalf("install 재실행 %v", e.reinstalled)
	}
}

func TestUpdate_json_실패도_JSON_하나와_종료_코드_1이다(t *testing.T) {
	srv, _ := releaseServer(t, nextVersion(), "NEW")
	e := updateSetup(t, srv)
	t.Setenv("ORBIT_MEM_BASE_URL", "http://127.0.0.1:1")
	out, _, code := run(t, "", "update", "--json")
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil || raw["ok"] != false || raw["error"] == nil || raw["steps"] == nil {
		t.Fatalf("%q %v", out, err)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLD" {
		t.Fatal("바뀜")
	}
}

func TestUpdate_check는_바꾸지_않고_알린다(t *testing.T) {
	srv, hits := releaseServer(t, nextVersion(), "NEW")
	e := updateSetup(t, srv)
	out, _, code := run(t, "", "update", "--check")
	if code != 0 || !strings.Contains(out, nextVersion()) {
		t.Fatalf("%d %q", code, out)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLD" || len(e.reinstalled) != 0 || hits.Load() != 1 {
		t.Fatalf("check가 바꾸거나 받음 hits=%d", hits.Load())
	}
}

func TestUpdate_dry_run은_할_일만_보인다(t *testing.T) {
	srv, _ := releaseServer(t, nextVersion(), "NEW")
	e := updateSetup(t, srv)
	out, _, code := run(t, "", "update", "--dry-run")
	if code != 0 || !strings.Contains(out, "SHA256SUMS") {
		t.Fatalf("%d %q", code, out)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLD" || len(e.reinstalled) != 0 {
		t.Fatal("dry-run이 바꿈")
	}
}

func TestUpdate_이미_최신이면_그대로_둔다(t *testing.T) {
	srv, _ := releaseServer(t, version.Version, "NEW")
	e := updateSetup(t, srv)
	out, _, code := run(t, "", "update", "--json")
	var raw map[string]any
	json.Unmarshal([]byte(out), &raw)
	if code != 0 || raw["updated"] != false {
		t.Fatalf("%d %s", code, out)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLD" || len(e.reinstalled) != 0 {
		t.Fatal("바뀜")
	}
}

func TestUpdate_알_수_없는_플래그는_실패한다(t *testing.T) {
	home(t)
	if _, _, code := run(t, "", "update", "--nope"); code == 0 {
		t.Fatalf("code %d", code)
	}
}

// NT-1~4: 새 판 알림.
func TestVersion_세_곳의_판이_같다(t *testing.T) {
	read := func(p string, v any) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", p))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	var plugin struct{ Version string }
	var market struct {
		Plugins []struct{ Version string }
	}
	read("plugin/.claude-plugin/plugin.json", &plugin)
	read(".claude-plugin/marketplace.json", &market)
	if len(market.Plugins) == 0 {
		t.Fatal("marketplace.json에 plugins가 없음")
	}
	if plugin.Version != version.Version || market.Plugins[0].Version != version.Version {
		t.Fatalf("판 불일치: Version=%s plugin.json=%s marketplace.json=%s", version.Version, plugin.Version, market.Plugins[0].Version)
	}
}

// seedUpdateRule leaves one rule in the memory so the session-start context is not empty.
func seedUpdateRule(t *testing.T) {
	t.Helper()
	s := seed(t)
	if _, err := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}}); err != nil {
		t.Fatal(err)
	}
}

func sessionStart(t *testing.T, extra ...string) string {
	t.Helper()
	return sessionStartOf(t, "s1", extra...)
}

// sessionStartOf is sessionStart of another session: the note lines are in a session's bundle (T-20).
func sessionStartOf(t *testing.T, session string, extra ...string) string {
	t.Helper()
	in := `{"cwd":"/tmp","session_id":"` + session + `","hook_event_name":"SessionStart","source":"startup"}`
	out, _, code := run(t, in, append([]string{"hook", "session-start", "--host", "claude"}, extra...)...)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if out == "" {
		return ""
	}
	var v struct {
		HookSpecificOutput struct{ AdditionalContext string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%q %v", out, err)
	}
	return v.HookSpecificOutput.AdditionalContext
}

// 훅은 네트워크를 기다리지 않는다: 캐시만 읽고, 갱신은 떼어 낸 프로세스(update --refresh-cache)에 맡긴다.
func writeCache(t *testing.T, h string, at time.Time, latest string) {
	t.Helper()
	b, _ := json.Marshal(update.Cache{CheckedAt: at, Latest: latest})
	if err := os.WriteFile(filepath.Join(h, "update-check.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fakeUpdateBG(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	old := startBackground
	startBackground = func(args ...string) error { calls = append(calls, args); return nil }
	t.Cleanup(func() { startBackground = old })
	return &calls
}

func refreshCalls(calls [][]string) int {
	n := 0
	for _, c := range calls {
		if len(c) >= 2 && c[0] == "update" && c[1] == "--refresh-cache" {
			n++
		}
	}
	return n
}

func TestHook_캐시에_새_판이_있으면_시작_문맥_끝에_한_줄을_붙인다(t *testing.T) {
	h := home(t)
	seedUpdateRule(t)
	calls := fakeUpdateBG(t)
	writeCache(t, h, time.Now(), nextVersion())
	got := sessionStart(t)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if !strings.Contains(got, "답은 존댓말") && len(lines) < 2 {
		t.Fatalf("기본 문맥이 없음 %q", got)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "`orbit-mem update`") || !strings.Contains(last, nextVersion()) {
		t.Fatalf("끝 줄이 안내가 아님 %q", got)
	}
	if refreshCalls(*calls) != 0 {
		t.Fatal("신선한 캐시인데 갱신을 요청함")
	}
}

func TestHook_캐시의_판이_같거나_낮으면_안내가_없다(t *testing.T) {
	h := home(t)
	seedUpdateRule(t)
	fakeUpdateBG(t)
	writeCache(t, h, time.Now(), version.Version)
	if got := sessionStart(t); strings.Contains(got, "orbit-mem update") {
		t.Fatalf("%q", got)
	}
}

func TestHook_캐시가_없거나_낡으면_갱신_프로세스를_요청한다(t *testing.T) {
	h := home(t)
	seedUpdateRule(t)
	calls := fakeUpdateBG(t)
	if got := sessionStart(t); strings.Contains(got, "orbit-mem update") {
		t.Fatalf("캐시가 없는데 안내가 붙음 %q", got)
	}
	if refreshCalls(*calls) != 1 {
		t.Fatalf("캐시 없음: calls=%v", *calls)
	}
	writeCache(t, h, time.Now().Add(-25*time.Hour), nextVersion())
	got := sessionStartOf(t, "s2")
	if refreshCalls(*calls) != 2 {
		t.Fatalf("낡은 캐시: calls=%v", *calls)
	}
	if !strings.Contains(got, "orbit-mem update") { // 낡아도 알려진 최신 판 안내는 유지한다
		t.Fatalf("낡은 캐시의 안내 %q", got)
	}
}

func TestHook_신선한_캐시나_updateCheck_끔이면_갱신을_요청하지_않는다(t *testing.T) {
	h := home(t)
	seedUpdateRule(t)
	calls := fakeUpdateBG(t)
	writeCache(t, h, time.Now().Add(-time.Hour), version.Version)
	sessionStart(t)
	if refreshCalls(*calls) != 0 {
		t.Fatalf("신선한데 요청 %v", *calls)
	}
	os.Remove(filepath.Join(h, "update-check.json"))
	if _, _, code := run(t, "", "config", "set", "updateCheck", "false"); code != 0 {
		t.Fatal("config set 실패")
	}
	writeCache(t, h, time.Now().Add(-48*time.Hour), nextVersion())
	if got := sessionStart(t); refreshCalls(*calls) != 0 || strings.Contains(got, "orbit-mem update") {
		t.Fatalf("끔인데 동작 calls=%v %q", *calls, got)
	}
}

func TestHook_요청_훅은_갱신을_요청하지_않는다(t *testing.T) {
	home(t)
	seedUpdateRule(t)
	calls := fakeUpdateBG(t)
	run(t, `{"cwd":"/tmp","session_id":"s1","prompt":"안녕"}`, "hook", "prompt", "--host", "claude")
	if refreshCalls(*calls) != 0 {
		t.Fatalf("%v", *calls)
	}
}

// 서버가 영영 응답하지 않거나 거절해도 훅은 네트워크를 건드리지 않고 기본 문맥을 그대로 0.5초 안에 낸다.
func TestHook_서버가_막혀도_기본_문맥을_바로_낸다(t *testing.T) {
	block := make(chan struct{})
	var hits atomic.Int32
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); <-block }))
	defer hang.Close()
	defer close(block)
	for name, base := range map[string]string{"무응답": hang.URL, "거절": "http://127.0.0.1:1"} {
		home(t)
		seedUpdateRule(t)
		t.Setenv("ORBIT_MEM_BASE_URL", base)
		start := time.Now()
		got := sessionStart(t)
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Errorf("%s: 훅이 늦음 %v", name, d)
		}
		if !strings.Contains(got, "먼저 찾는 곳") || strings.Contains(got, "orbit-mem update") {
			t.Errorf("%s: 문맥 %q", name, got)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("훅이 네트워크로 나감 hits=%d", hits.Load())
	}
}

func TestUpdate_refresh_cache는_캐시를_갱신하고_조용하다(t *testing.T) {
	srv, _ := releaseServer(t, nextVersion(), "NEW")
	e := updateSetup(t, srv)
	out, errOut, code := run(t, "", "update", "--refresh-cache")
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if c, ok := update.ReadCache(paths().Home); !ok || c.Latest != nextVersion() {
		t.Fatalf("캐시 %+v %v", c, ok)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLD" {
		t.Fatal("바뀜")
	}
	// 끈 설정이면 요청하지 않는다
	t.Setenv("ORBIT_MEM_BASE_URL", "http://127.0.0.1:1")
	run(t, "", "config", "set", "updateCheck", "false")
	os.Remove(filepath.Join(paths().Home, "update-check.json"))
	run(t, "", "update", "--refresh-cache")
	if _, ok := update.ReadCache(paths().Home); ok {
		t.Fatal("끈 설정인데 캐시를 씀")
	}
}

func TestUpdate_refresh_cache는_사용법에_보이지_않는다(t *testing.T) {
	home(t)
	_, errOut, _ := run(t, "", "help")
	if strings.Contains(errOut, "refresh-cache") {
		t.Fatal("usage에 노출")
	}
	_, errOut, _ = run(t, "", "update", "-h")
	if strings.Contains(errOut, "refresh-cache") || !strings.Contains(errOut, "force") {
		t.Fatalf("%s", errOut)
	}
}

func TestUpdate_version이_현재보다_낮으면_거절하고_force로_허용한다(t *testing.T) {
	srv, _ := releaseServer(t, "0.1.0", "OLDER")
	e := updateSetup(t, srv)
	out, errOut, code := run(t, "", "update", "--version", "0.1.0", "--json")
	var raw map[string]any
	json.Unmarshal([]byte(out), &raw)
	if code != 1 || raw["ok"] != false || raw["updated"] != false || !strings.Contains(fmt.Sprint(raw["error"]), "--force") {
		t.Fatalf("거절 안 함 %d %s %s", code, out, errOut)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLD" || len(e.reinstalled) != 0 {
		t.Fatal("바뀜")
	}
	out, errOut, code = run(t, "", "update", "--version", "0.1.0", "--force", "--json")
	json.Unmarshal([]byte(out), &raw)
	if code != 0 || raw["updated"] != true {
		t.Fatalf("force 실패 %d %s %s", code, out, errOut)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLDER" {
		t.Fatalf("파일 %q", b)
	}
}

func TestUpdate_version이_현재와_같으면_불변이다(t *testing.T) {
	srv, _ := releaseServer(t, version.Version, "SAME")
	e := updateSetup(t, srv)
	out, _, code := run(t, "", "update", "--version", version.Version, "--json")
	var raw map[string]any
	json.Unmarshal([]byte(out), &raw)
	if code != 0 || raw["updated"] != false {
		t.Fatalf("%d %s", code, out)
	}
	if b, _ := os.ReadFile(e.target); string(b) != "OLD" {
		t.Fatal("바뀜")
	}
}

func TestUpdate_실행_파일_절대_경로를_모르면_아무것도_쓰지_않고_중단한다(t *testing.T) {
	srv, _ := releaseServer(t, nextVersion(), "NEW")
	e := updateSetup(t, srv)
	updateTarget = func() string { return "orbit-mem" } // selfPath가 실패했을 때의 값
	cwd := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(cwd)
	defer os.Chdir(old)
	out, _, code := run(t, "", "update", "--json")
	var raw map[string]any
	json.Unmarshal([]byte(out), &raw)
	if code != 1 || raw["ok"] != false {
		t.Fatalf("%d %s", code, out)
	}
	if ents, _ := os.ReadDir(cwd); len(ents) != 0 {
		t.Fatalf("현재 폴더에 씀: %v", ents)
	}
	if len(e.reinstalled) != 0 {
		t.Fatal("install 실행")
	}
}

func TestDoctor_캐시된_최신_판을_보이고_네트워크는_하지_않는다(t *testing.T) {
	srv, hits := releaseServer(t, nextVersion(), "NEW")
	h := home(t)
	t.Setenv("ORBIT_MEM_BASE_URL", srv.URL)
	// 캐시가 없으면 최신 판은 비어 있다
	out, _, _ := run(t, "", "doctor", "--json")
	var rep struct {
		Update struct {
			Current   string `json:"current"`
			Latest    string `json:"latest"`
			Available bool   `json:"available"`
			Enabled   bool   `json:"enabled"`
		} `json:"update"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || rep.Update.Current != version.Version || rep.Update.Latest != "" || rep.Update.Available || !rep.Update.Enabled {
		t.Fatalf("캐시 없음 %+v %v", rep.Update, err)
	}
	b, _ := json.Marshal(update.Cache{CheckedAt: time.Now(), Latest: nextVersion()})
	os.WriteFile(filepath.Join(h, "update-check.json"), b, 0o600)
	out, _, _ = run(t, "", "doctor", "--json")
	json.Unmarshal([]byte(out), &rep)
	if rep.Update.Latest != nextVersion() || !rep.Update.Available {
		t.Fatalf("캐시 있음 %+v", rep.Update)
	}
	if hits.Load() != 0 {
		t.Fatalf("doctor가 네트워크를 함 hits=%d", hits.Load())
	}
}
