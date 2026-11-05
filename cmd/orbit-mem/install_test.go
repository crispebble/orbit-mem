package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memplugin"
	"github.com/onidot-labs/orbit-mem/internal/version"
)

// fakePC is a PC with fake claude and codex on PATH that log their arguments: HOME, PATH
// and CODEX_HOME all point into temporary folders, so no test reaches the real hosts or
// launchctl. claude answers its list commands from state files.
type fakePC struct {
	home, bin, log, state, codexHome string
}

func newFakePC(t *testing.T, hosts ...string) *fakePC {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("가짜 호스트는 /bin/sh로 돈다")
	}
	f := &fakePC{home: home(t), bin: t.TempDir(), state: t.TempDir(), codexHome: t.TempDir()}
	f.log = filepath.Join(f.state, "calls")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", f.bin)
	t.Setenv("CODEX_HOME", f.codexHome)
	for _, h := range hosts {
		script := "#!/bin/sh\nprintf '%s\\n' \"" + h + " $*\" >>'" + f.log + "'\n" +
			"case \"$*\" in\n" +
			"\"plugin marketplace list --json\") if [ -f '" + f.state + "/markets.json' ]; then /bin/cat '" + f.state + "/markets.json'; else echo '[]'; fi ;;\n" +
			"\"plugin list --json\") if [ -f '" + f.state + "/plugins.json' ]; then /bin/cat '" + f.state + "/plugins.json'; else echo '[]'; fi ;;\n" +
			"esac\n"
		if err := os.WriteFile(filepath.Join(f.bin, h), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fakePC) calls(t *testing.T) []string {
	t.Helper()
	b, _ := os.ReadFile(f.log)
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (f *fakePC) writes(t *testing.T) []string {
	var out []string
	for _, c := range f.calls(t) {
		if !strings.HasSuffix(c, "list --json") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakePC) localPlugin() string { return filepath.Join(f.home, "plugins", "orbit-mem") }

// 끝 기준: install --local-plugin은 실행 파일·config·DB 뒤에 내장 원본을 $ORBIT_HOME/plugins/orbit-mem에
// 빌드하고, 그 폴더를 Claude·Codex 마켓으로 더해 orbit-mem@orbit-mem을 깔고, Codex /hooks 신뢰를 안내한다.
func TestInstall_local_plugin_끝까지(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	dest := t.TempDir()
	out, errOut, code := run(t, "", "install", "--local-plugin", "--no-schedule", "--bin-dir", dest)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	dir := f.localPlugin()
	want := []string{
		"claude plugin marketplace add " + dir,
		"claude plugin install orbit-mem@orbit-mem --scope user",
		"codex plugin marketplace add " + dir,
		"codex plugin add orbit-mem@orbit-mem",
	}
	if got := f.writes(t); !slices.Equal(got, want) {
		t.Fatalf("호스트 호출 %q", got)
	}
	files, _ := memplugin.Local(version.Version)
	if d := memplugin.Diff(dir, files); len(d) != 0 {
		t.Fatalf("로컬 플러그인 %v", d)
	}
	if _, err := os.Stat(filepath.Join(dest, "orbit-mem")); err != nil {
		t.Fatal("실행 파일이 없습니다")
	}
	if _, err := os.Stat(filepath.Join(f.home, "memory.db")); err != nil {
		t.Fatal("기억 DB가 없습니다")
	}
	if !strings.Contains(out, "/hooks") || strings.Contains(out, "orb install") || strings.Contains(out+errOut, "%!") {
		t.Fatalf("출력: %s", out)
	}
	cfg, _ := config.Load(filepath.Join(f.home, "config.json"))
	if cfg.ClaudePath != filepath.Join(f.bin, "claude") || cfg.CodexPath != filepath.Join(f.bin, "codex") {
		t.Fatalf("config %+v", cfg)
	}
	// 다시 해도 된다: 같은 폴더 마켓은 update, 깔린 플러그인은 update
	os.WriteFile(filepath.Join(f.state, "markets.json"), []byte(`[{"name":"orbit-mem","source":"directory","path":"`+dir+`"}]`), 0o644)
	os.WriteFile(filepath.Join(f.state, "plugins.json"), []byte(`[{"id":"orbit-mem@orbit-mem","version":"0.4.0","enabled":true}]`), 0o644)
	os.Remove(f.log)
	if _, errOut, code := run(t, "", "install", "--local-plugin", "--no-schedule", "--bin-dir", dest); code != 0 {
		t.Fatalf("다시 %d %s", code, errOut)
	}
	if got := f.writes(t); !slices.Equal(got, []string{"claude plugin marketplace update orbit-mem", "claude plugin update orbit-mem@orbit-mem", want[2], want[3]}) {
		t.Fatalf("다시 호출 %q", got)
	}
}

// 끝 기준: 기본 install은 Claude에 GitHub 마켓 onidot-labs/orbit-mem을 더하고, Codex는 언제나 로컬 폴더다.
// 호스트가 하나뿐이면 그 호스트만.
func TestInstall_기본은_GitHub_마켓(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	if out, errOut, code := run(t, "", "install", "--no-schedule", "--bin-dir", t.TempDir()); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	want := []string{
		"claude plugin marketplace add onidot-labs/orbit-mem",
		"claude plugin install orbit-mem@orbit-mem --scope user",
		"codex plugin marketplace add " + f.localPlugin(),
		"codex plugin add orbit-mem@orbit-mem",
	}
	if got := f.writes(t); !slices.Equal(got, want) {
		t.Fatalf("호출 %q", got)
	}

	f = newFakePC(t, "claude")
	if _, errOut, code := run(t, "", "install", "--no-schedule", "--bin-dir", t.TempDir()); code != 0 {
		t.Fatal(errOut)
	}
	if got := f.writes(t); !slices.Equal(got, want[:2]) {
		t.Fatalf("Claude만: %q", got)
	}
	if _, err := os.Stat(f.localPlugin()); !os.IsNotExist(err) {
		t.Fatal("Codex도 --local-plugin도 없는데 로컬 플러그인을 만들었습니다")
	}
}

// 끝 기준: --no-hosts는 호스트를 부르지 않고 플러그인도 만들지 않는다. --dry-run은 아무것도 바꾸지 않고
// 부를 호스트 명령을 보인다. 걷어 낸 --enable-codex-plan은 모르는 플래그다.
func TestInstall_no_hosts와_dry_run(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	dest := t.TempDir()
	out, errOut, code := run(t, "", "install", "--no-hosts", "--no-schedule", "--bin-dir", dest)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if c := f.calls(t); len(c) != 0 {
		t.Fatalf("호스트를 불렀습니다 %q", c)
	}
	if _, err := os.Stat(filepath.Join(f.home, "plugins")); !os.IsNotExist(err) {
		t.Fatal("플러그인을 만들었습니다")
	}
	if !strings.Contains(out, "orbit-mem install") {
		t.Fatalf("나중에 등록하는 법이 없습니다: %s", out)
	}

	h2 := home(t)
	dest2 := t.TempDir()
	out, _, code = run(t, "", "install", "--dry-run", "--local-plugin", "--bin-dir", dest2)
	if code != 0 || len(f.calls(t)) != 0 {
		t.Fatalf("미리 보기가 호스트를 불렀습니다 %d %q", code, f.calls(t))
	}
	for _, want := range []string{filepath.Join(dest2, "orbit-mem"), "claude plugin marketplace add " + filepath.Join(h2, "plugins", "orbit-mem"), "claude plugin install orbit-mem@orbit-mem --scope user", "codex plugin add orbit-mem@orbit-mem"} {
		if !strings.Contains(out, want) {
			t.Errorf("미리 보기에 %q가 없습니다:\n%s", want, out)
		}
	}
	if entries, _ := os.ReadDir(h2); len(entries) != 0 {
		t.Fatalf("미리 보기가 파일을 만들었습니다 %v", entries)
	}
	if entries, _ := os.ReadDir(dest2); len(entries) != 0 {
		t.Fatalf("미리 보기가 실행 파일을 복사했습니다 %v", entries)
	}
	if _, errOut, code := run(t, "", "install", "--dry-run", "--enable-codex-plan"); code == 0 || !strings.Contains(errOut, "not defined") {
		t.Errorf("--enable-codex-plan은 없앴다: %d %q", code, errOut)
	}
}

// 끝 기준: uninstall은 회고 예약을 지우고 Claude·Codex에서 orbit-mem@orbit-mem과 마켓 orbit-mem을 뺀다.
// 기억 DB와 로컬 플러그인 폴더는 남긴다.
func TestUninstall_호스트에서_뺀다(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	if _, errOut, code := run(t, "", "install", "--local-plugin", "--no-schedule", "--bin-dir", t.TempDir()); code != 0 {
		t.Fatal(errOut)
	}
	os.WriteFile(filepath.Join(f.state, "markets.json"), []byte(`[{"name":"orbit-mem","source":"directory","path":"/x"}]`), 0o644)
	os.WriteFile(filepath.Join(f.state, "plugins.json"), []byte(`[{"id":"orbit-mem@orbit-mem"}]`), 0o644)
	os.WriteFile(filepath.Join(f.codexHome, "config.toml"), []byte("[marketplaces.orbit-mem]\nsource = \"/x\"\n\n[plugins.\"orbit-mem@orbit-mem\"]\nenabled = true\n"), 0o600)
	os.Remove(f.log)
	out, errOut, code := run(t, "", "uninstall")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	want := []string{"claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem", "codex plugin remove orbit-mem@orbit-mem", "codex plugin marketplace remove orbit-mem"}
	if got := f.writes(t); !slices.Equal(got, want) {
		t.Fatalf("호출 %q", got)
	}
	if strings.Contains(out, "orb uninstall") {
		t.Fatalf("출력 %s", out)
	}
	for _, p := range []string{filepath.Join(f.home, "memory.db"), f.localPlugin()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s를 지웠습니다", p)
		}
	}
}

// 끝 기준: doctor --json은 정해진 키로 실행 파일·데이터·호스트 상태와 문제마다 고칠 명령을 낸다.
func TestDoctor_JSON(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	os.WriteFile(filepath.Join(f.bin, "orbit-mem"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(f.state, "markets.json"), []byte(`[{"name":"orbit-mem","source":"github","repo":"onidot-labs/orbit-mem"}]`), 0o644)
	os.WriteFile(filepath.Join(f.state, "plugins.json"), []byte(`[{"id":"orbit-mem@orbit-mem","version":"0.3.0","enabled":true}]`), 0o644)
	os.WriteFile(filepath.Join(f.codexHome, "config.toml"), []byte("[marketplaces.orbit-mem]\nsource = \"/x\"\n\n[plugins.\"orbit-mem@orbit-mem\"]\nenabled = true\n"), 0o600)
	seed(t)
	out, errOut, code := run(t, "", "doctor", "--json")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, k := range []string{"version", "binary", "pathBinary", "home", "db", "config", "memories", "pendingProposals", "sessionQueue", "activity", "lastRetros", "claude", "codex", "problems"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("키 %s가 없습니다", k)
		}
	}
	var rep struct {
		Version, PathBinary string
		DB                  bool
		Claude              struct {
			Path, Plugin, Version, Marketplace string
			Enabled                            bool
		}
		Codex struct {
			Path, Config, Plugin               string
			Marketplace, Enabled, HooksTrusted bool
		}
		Problems []struct{ ID, Message, Fix string }
	}
	json.Unmarshal([]byte(out), &rep)
	if rep.Version != version.Version || rep.PathBinary != filepath.Join(f.bin, "orbit-mem") || !rep.DB {
		t.Fatalf("%+v", rep)
	}
	if rep.Claude.Path != filepath.Join(f.bin, "claude") || rep.Claude.Plugin != "installed" || rep.Claude.Version != "0.3.0" || !rep.Claude.Enabled || rep.Claude.Marketplace != "github:onidot-labs/orbit-mem" {
		t.Fatalf("claude %+v", rep.Claude)
	}
	if rep.Codex.Config != filepath.Join(f.codexHome, "config.toml") || rep.Codex.Plugin != "installed" || !rep.Codex.Marketplace || !rep.Codex.Enabled || rep.Codex.HooksTrusted {
		t.Fatalf("codex %+v", rep.Codex)
	}
	ids := map[string]string{}
	for _, p := range rep.Problems {
		ids[p.ID] = p.Fix
		if p.Message == "" || p.Fix == "" {
			t.Errorf("문제 %+v", p)
		}
	}
	if _, ok := raw["schedule"]; ok {
		t.Error("schedule 키는 뺐습니다(회고 예약은 OpenOrbit)")
	}
	for _, id := range []string{"claude-plugin-version", "codex-hooks-untrusted"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("문제 %s가 없습니다: %+v", id, rep.Problems)
		}
	}
	if _, ok := ids["schedule-missing"]; ok {
		t.Error("schedule-missing 문제는 뺐습니다")
	}
	if _, ok := ids["not-on-path"]; ok {
		t.Error("PATH에 있는데 not-on-path")
	}

	// 플러그인 목록을 읽지 못하면 알 수 없음, Codex에 없으면 missing, PATH에 없으면 not-on-path
	os.WriteFile(filepath.Join(f.bin, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	os.Remove(filepath.Join(f.bin, "orbit-mem"))
	os.Remove(filepath.Join(f.codexHome, "config.toml"))
	out, _, _ = run(t, "", "doctor", "--json")
	rep.Problems = nil
	json.Unmarshal([]byte(out), &rep)
	ids = map[string]string{}
	for _, p := range rep.Problems {
		ids[p.ID] = p.Fix
	}
	if rep.Claude.Plugin != "unknown" || rep.Codex.Plugin != "missing" || rep.PathBinary != "" {
		t.Fatalf("%+v", rep)
	}
	for _, id := range []string{"not-on-path", "codex-plugin-missing"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("문제 %s가 없습니다: %+v", id, rep.Problems)
		}
	}
	if _, ok := ids["claude-plugin-missing"]; ok {
		t.Error("알 수 없음인데 missing")
	}
	// 사람이 읽는 출력도 문제와 고칠 명령을 보인다
	out, _, code = run(t, "", "doctor")
	if code != 0 || !strings.Contains(out, ids["codex-plugin-missing"]) {
		t.Fatalf("%d %s", code, out)
	}
}

// 끝 기준: 걷어 낸 명령(run·tui·profile·daemon·runtime·work·plugin fragment|install)은 모르는 명령이거나 사용법 오류다.
func TestUsage_걷어_낸_명령(t *testing.T) {
	home(t)
	for _, args := range [][]string{{"run"}, {"profile"}, {"daemon", "status"}, {"runtime", "status"}, {"work", "list"}, {"tui"}} {
		if _, errOut, code := run(t, "", args...); code != 2 || !strings.Contains(errOut, args[0]) {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	for _, args := range [][]string{{"plugin", "fragment"}, {"plugin", "install"}, {"plugin", "build", "/src"}, {"plugin", "build", "--out", "a", "--repo", "b"}} {
		if _, _, code := run(t, "", args...); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
	if out, _, code := run(t, "", "version"); code != 0 || out != "orbit-mem 0.16.0\n" {
		t.Errorf("version %d %q", code, out)
	}
	if _, errOut, code := run(t, ""); code != 2 || !strings.Contains(errOut, "orbit-mem search") || strings.Contains(errOut, "orb run") || strings.Contains(errOut, "fragment") {
		t.Errorf("사용법 %q", errOut)
	}
}

// 끝 기준: plugin build는 --out(기본 $ORBIT_HOME/plugins/orbit-mem)에 두 호스트 꼴과 두 로컬 마켓을,
// --repo에는 GitHub 마켓(Claude 꼴만)을 쓴다.
func TestPluginBuild(t *testing.T) {
	h := home(t)
	out, errOut, code := run(t, "", "plugin", "build")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	local, _ := memplugin.Local(version.Version)
	if d := memplugin.Diff(filepath.Join(h, "plugins", "orbit-mem"), local); len(d) != 0 || !strings.Contains(out, filepath.Join(h, "plugins", "orbit-mem")) {
		t.Fatalf("%v %s", d, out)
	}
	other := t.TempDir()
	if _, errOut, code := run(t, "", "plugin", "build", "--out", other); code != 0 || len(memplugin.Diff(other, local)) != 0 {
		t.Fatalf("--out %d %s", code, errOut)
	}
	repo := t.TempDir()
	if _, errOut, code := run(t, "", "plugin", "build", "--repo", repo); code != 0 {
		t.Fatalf("--repo %d %s", code, errOut)
	}
	files, _ := memplugin.Repo(version.Version)
	if d := memplugin.Diff(repo, files); len(d) != 0 {
		t.Fatalf("--repo %v", d)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents")); !os.IsNotExist(err) {
		t.Fatal("저장소에 Codex 마켓")
	}
}

// OpenOrbit이 같은 config.json에서 읽는 작업 설정(work·workStaleAfter·workTestCommands)은
// config set과 install이 다시 저장해도 남는다.
func TestConfig_set과_install은_모르는_키를_지우지_않는다(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	cfgPath := filepath.Join(f.home, "config.json")
	os.WriteFile(cfgPath, []byte(`{"work":"off","workStaleAfter":"72h","workTestCommands":{"go":"go test ./..."}}`), 0o600)
	has := func(when string) {
		b, _ := os.ReadFile(cfgPath)
		for _, k := range []string{`"work": "off"`, `"workStaleAfter": "72h"`, `"workTestCommands"`} {
			if !strings.Contains(string(b), k) {
				t.Fatalf("%s: %s가 사라졌습니다\n%s", when, k, b)
			}
		}
	}
	if _, errOut, code := run(t, "", "config", "set", "language", "ko"); code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	has("config set")
	if _, errOut, code := run(t, "", "install", "--no-schedule", "--bin-dir", t.TempDir()); code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	has("install")
}

// 파이프(터미널이 아닌 stdout)면 doctor도 --json 없이 JSON을 낸다.
func TestDoctor_파이프는_JSON(t *testing.T) {
	newFakePC(t, "claude", "codex")
	seed(t)
	out, errOut, code := run(t, "", "doctor")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("파이프인데 JSON이 아닙니다: %v\n%s", err, out)
	}
}
