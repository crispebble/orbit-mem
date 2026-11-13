package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memplugin"
	"github.com/crispebble/orbit-mem/internal/version"
)

// 설정 claudeMarketplace의 기본값은 orbit-mem 자기 GitHub 마켓이다(config는 memplugin을 가져올 수 없어 값을 따로 둔다).
func TestClaudeMarketplace_기본값은_자기_GitHub_마켓(t *testing.T) {
	if config.DefaultClaudeMarketplace != memplugin.GitHubRepo || config.Default().ClaudeMarketplace != memplugin.GitHubRepo {
		t.Fatalf("%q %q", config.DefaultClaudeMarketplace, config.Default().ClaudeMarketplace)
	}
}

func (f *fakePC) setState(t *testing.T, markets, plugins string) {
	t.Helper()
	os.WriteFile(filepath.Join(f.state, "markets.json"), []byte(markets), 0o644)
	os.WriteFile(filepath.Join(f.state, "plugins.json"), []byte(plugins), 0o644)
	os.Remove(f.log)
}

func savedMarketplace(t *testing.T, home string) string {
	t.Helper()
	var c struct {
		ClaudeMarketplace *string `json:"claudeMarketplace"`
	}
	b, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(b, &c)
	if c.ClaudeMarketplace == nil {
		t.Fatalf("claudeMarketplace 키가 없습니다: %s", b)
	}
	return *c.ClaudeMarketplace
}

const oldOwnMarket = `{"name":"orbit-mem","source":"github","repo":"crispebble/orbit-mem"}`

func catalogMarket(dir string) string {
	return `{"name":"orbit-plugins","source":"directory","path":"` + dir + `"}`
}

// 끝 기준: install --marketplace <카탈로그>는 setup.sh가 같은 출처로 등록해 둔 마켓을 update만 하고(remove·add 없음)
// orbit-mem@<그 마켓>을 깐 뒤, 옛 orbit-mem@orbit-mem과 자기 마켓을 뺀다. 출처는 설정에 남고, 다음 install(update가
// 다시 부르는 것)은 --marketplace 없이도 같은 마켓을 쓰며 crispebble/orbit-mem을 다시 만들지 않는다. Codex는 그대로다.
func TestInstall_marketplace_카탈로그로_옮긴다(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	cat := t.TempDir()
	f.setState(t, "["+oldOwnMarket+","+catalogMarket(cat)+"]",
		`[{"id":"orbit-mem@orbit-mem","version":"0.17.1","enabled":true,"scope":"user"},{"id":"superpowers@orbit-plugins","enabled":true,"scope":"user"}]`)
	bin := t.TempDir()
	out, errOut, code := run(t, "", "install", "--marketplace", cat, "--bin-dir", bin)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	want := []string{
		"claude plugin marketplace update orbit-plugins",
		"claude plugin install orbit-mem@orbit-plugins --scope user",
		"claude plugin uninstall orbit-mem@orbit-mem --scope user",
		"claude plugin marketplace remove orbit-mem",
		"codex plugin marketplace add " + f.localPlugin(),
		"codex plugin add orbit-mem@orbit-mem",
	}
	if got := f.writes(t); !slices.Equal(got, want) {
		t.Fatalf("호스트 호출 %q", got)
	}
	if got := savedMarketplace(t, f.home); got != cat {
		t.Fatalf("설정 %q", got)
	}
	for _, s := range []string{"orbit-mem@orbit-plugins", "claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"} {
		if !strings.Contains(out, s) {
			t.Errorf("출력에 %q가 없습니다:\n%s", s, out)
		}
	}

	// 다음 install(update가 새 판으로 부르는 것): 설정의 마켓을 쓴다
	f.setState(t, "["+catalogMarket(cat)+"]", `[{"id":"orbit-mem@orbit-plugins","enabled":true,"scope":"user"},{"id":"superpowers@orbit-plugins","enabled":true,"scope":"user"}]`)
	if _, errOut, code := run(t, "", "install", "--bin-dir", bin); code != 0 {
		t.Fatalf("다시 %d %s", code, errOut)
	}
	want = []string{"claude plugin marketplace update orbit-plugins", "claude plugin update orbit-mem@orbit-plugins", want[4], want[5]}
	if got := f.writes(t); !slices.Equal(got, want) {
		t.Fatalf("다시 호출 %q", got)
	}
	if got := savedMarketplace(t, f.home); got != cat {
		t.Fatalf("다시 설정 %q", got)
	}
}

// 끝 기준: install --dry-run --json --marketplace는 아무것도 바꾸지 않고(설정도 쓰지 않는다), 옛 플러그인과 자기 마켓을
// 빼는 명령을 단계에 그대로 보인다.
func TestInstall_marketplace_dry_run은_빼는_단계를_보인다(t *testing.T) {
	f := newFakePC(t, "claude")
	f.setState(t, "["+oldOwnMarket+"]", `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"}]`)
	out, errOut, code := run(t, "", "install", "--dry-run", "--json", "--marketplace", "crispebble/orbit-plugins", "--bin-dir", t.TempDir())
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	var res struct {
		OK    bool
		Steps []string
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || !res.OK {
		t.Fatalf("%v %s", err, out)
	}
	all := strings.Join(res.Steps, "\n")
	for _, s := range []string{"claude plugin marketplace add crispebble/orbit-plugins", "claude plugin install orbit-mem@", "claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"} {
		if !strings.Contains(all, s) {
			t.Errorf("단계에 %q가 없습니다:\n%s", s, all)
		}
	}
	if w := f.writes(t); len(w) != 0 {
		t.Fatalf("미리 보기가 호스트를 바꿨습니다 %q", w)
	}
	if _, err := os.Stat(filepath.Join(f.home, "config.json")); !os.IsNotExist(err) {
		t.Fatal("미리 보기가 설정을 썼습니다")
	}
}

// 끝 기준: --marketplace는 claude plugin marketplace add가 받는 꼴만 받는다(틀리면 인자 오류 2, --json이면 JSON 하나).
// --local-plugin과 함께 줄 수 없다. github:owner/repo는 owner/repo로 남긴다. --no-hosts여도 설정에는 남긴다.
func TestInstall_marketplace_인자(t *testing.T) {
	f := newFakePC(t, "claude")
	for _, args := range [][]string{{"--marketplace", "-x"}, {"--marketplace", "orbit-plugins"}, {"--marketplace", "a/b", "--local-plugin"}} {
		if out, errOut, code := run(t, "", append([]string{"install", "--bin-dir", t.TempDir()}, args...)...); code != 2 {
			t.Errorf("%v: %d %s %s", args, code, out, errOut)
		}
		out, _, code := run(t, "", append([]string{"install", "--json", "--bin-dir", t.TempDir()}, args...)...)
		var res struct {
			OK    bool
			Error string
		}
		if code != 2 || json.Unmarshal([]byte(out), &res) != nil || res.OK || res.Error == "" {
			t.Errorf("--json %v: %d %q", args, code, out)
		}
	}
	if w := f.writes(t); len(w) != 0 {
		t.Fatalf("인자 오류인데 호스트를 바꿨습니다 %q", w)
	}
	out, errOut, code := run(t, "", "install", "--no-hosts", "--marketplace", "github:crispebble/orbit-plugins", "--bin-dir", t.TempDir())
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if got := savedMarketplace(t, f.home); got != "crispebble/orbit-plugins" {
		t.Fatalf("설정 %q", got)
	}
	if !strings.Contains(out, "crispebble/orbit-plugins") || strings.Contains(out, "crispebble/orbit-mem") {
		t.Fatalf("나중에 등록하는 법이 설정의 마켓이 아닙니다: %s", out)
	}
}

// 끝 기준: 카탈로그로 깐 orbit-mem의 uninstall은 orbit-mem@<카탈로그>만 빼고 카탈로그 마켓은 지우지 않는다.
func TestUninstall_카탈로그_마켓은_두고_플러그인만_뺀다(t *testing.T) {
	f := newFakePC(t, "claude", "codex")
	cat := t.TempDir()
	if _, errOut, code := run(t, "", "install", "--no-hosts", "--marketplace", cat, "--bin-dir", t.TempDir()); code != 0 {
		t.Fatal(errOut)
	}
	f.setState(t, "["+catalogMarket(cat)+"]", `[{"id":"orbit-mem@orbit-plugins","enabled":true,"scope":"user"},{"id":"superpowers@orbit-plugins","enabled":true,"scope":"user"}]`)
	os.WriteFile(filepath.Join(f.codexHome, "config.toml"), []byte("[marketplaces.orbit-mem]\nsource = \"/x\"\n\n[plugins.\"orbit-mem@orbit-mem\"]\nenabled = true\n"), 0o600)
	out, errOut, code := run(t, "", "uninstall")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	want := []string{"claude plugin uninstall orbit-mem@orbit-plugins --scope user", "codex plugin remove orbit-mem@orbit-mem", "codex plugin marketplace remove orbit-mem"}
	if got := f.writes(t); !slices.Equal(got, want) {
		t.Fatalf("호출 %q", got)
	}
	if !strings.Contains(out, "orbit-mem@orbit-plugins") {
		t.Fatalf("출력 %s", out)
	}
}

// 끝 기준: doctor는 설정이 가리키는 마켓의 orbit-mem을 보고(claude.id), 다른 마켓에도 깔려 있으면 claude-plugin-duplicate와
// 그 플러그인(자기 마켓이면 마켓도)을 빼는 명령을 낸다. 꺼진 것을 켜는 명령도 그 마켓의 id다.
func TestDoctor_카탈로그와_겹침(t *testing.T) {
	f := newFakePC(t, "claude")
	cat := t.TempDir()
	if _, errOut, code := run(t, "", "install", "--no-hosts", "--marketplace", cat, "--bin-dir", t.TempDir()); code != 0 {
		t.Fatal(errOut)
	}
	type report struct {
		Config struct{ ClaudeMarketplace string }
		Claude struct {
			ID, Plugin, Marketplace string
			Duplicates              []string
		}
		Problems []struct{ ID, Message, Fix string }
	}
	doctor := func() (report, map[string]string) {
		t.Helper()
		out, errOut, code := run(t, "", "doctor", "--json")
		if code != 0 {
			t.Fatalf("%d %s", code, errOut)
		}
		var rep report
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatal(err)
		}
		ids := map[string]string{}
		for _, p := range rep.Problems {
			ids[p.ID] = p.Fix
			if strings.Contains(p.Message, "%!") {
				t.Errorf("메시지 %q", p.Message)
			}
		}
		return rep, ids
	}
	f.setState(t, "["+oldOwnMarket+","+catalogMarket(cat)+"]",
		`[{"id":"orbit-mem@orbit-plugins","version":"`+version.Version+`","enabled":true,"scope":"user"},{"id":"orbit-mem@orbit-mem","version":"0.17.1","enabled":true,"scope":"user"}]`)
	rep, ids := doctor()
	if rep.Config.ClaudeMarketplace != cat || rep.Claude.ID != "orbit-mem@orbit-plugins" || rep.Claude.Plugin != "installed" || rep.Claude.Marketplace != "directory:"+cat ||
		!slices.Equal(rep.Claude.Duplicates, []string{"orbit-mem@orbit-mem"}) {
		t.Fatalf("%+v", rep)
	}
	if fix := ids["claude-plugin-duplicate"]; fix != "claude plugin uninstall orbit-mem@orbit-mem --scope user && claude plugin marketplace remove orbit-mem" {
		t.Fatalf("겹침 고칠 명령 %q (%v)", fix, ids)
	}
	if _, ok := ids["claude-plugin-version"]; ok {
		t.Error("같은 판인데 claude-plugin-version")
	}

	// 꺼져 있으면 그 마켓의 id로 켠다
	f.setState(t, "["+catalogMarket(cat)+"]", `[{"id":"orbit-mem@orbit-plugins","version":"`+version.Version+`","enabled":false,"scope":"user"}]`)
	if _, ids = doctor(); ids["claude-plugin-disabled"] != "claude plugin enable orbit-mem@orbit-plugins" {
		t.Fatalf("%v", ids)
	}
	if _, ok := ids["claude-plugin-duplicate"]; ok {
		t.Error("한 벌인데 겹침")
	}

	// 옛 것만 있으면 빠진 것(orbit-mem install이 옮긴다)이고 겹침은 아니다
	f.setState(t, "["+oldOwnMarket+","+catalogMarket(cat)+"]", `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"}]`)
	rep, ids = doctor()
	if rep.Claude.Plugin != "missing" || ids["claude-plugin-missing"] != "orbit-mem install" {
		t.Fatalf("%+v %v", rep, ids)
	}
	if _, ok := ids["claude-plugin-duplicate"]; ok {
		t.Error("한 벌인데 겹침")
	}
	// 사람이 읽는 출력도 설정의 마켓을 보인다
	out, _, _ := run(t, "", "doctor")
	if !strings.Contains(out, "orbit-mem@orbit-plugins") && !strings.Contains(out, cat) {
		t.Fatalf("%s", out)
	}
}

// 끝 기준: update(--dry-run 포함)는 새 판의 install이 Claude Code에 할 일을 단계로 보인다: 카탈로그로 옮기면 옛 플러그인과
// 자기 마켓을 빼는 명령까지.
func TestUpdate_dry_run은_호스트_단계를_보인다(t *testing.T) {
	srv, _ := releaseServer(t, nextVersion(), "NEW")
	updateSetup(t, srv)
	updateHostSteps = hostPlanSteps // updateSetup이 막아 둔 것을 이 시험에서만 가짜 호스트로 돌린다
	f := newFakePC(t, "claude")
	t.Setenv("ORBIT_MEM_BASE_URL", srv.URL)
	cat := t.TempDir()
	if _, errOut, code := run(t, "", "install", "--no-hosts", "--marketplace", cat, "--bin-dir", t.TempDir()); code != 0 {
		t.Fatal(errOut)
	}
	f.setState(t, "["+oldOwnMarket+","+catalogMarket(cat)+"]", `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"}]`)
	out, errOut, code := run(t, "", "update", "--dry-run")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	for _, s := range []string{"claude plugin install orbit-mem@orbit-plugins --scope user", "claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"} {
		if !strings.Contains(out, s) {
			t.Errorf("단계에 %q가 없습니다:\n%s", s, out)
		}
	}
	if w := f.writes(t); len(w) != 0 {
		t.Fatalf("미리 보기가 호스트를 바꿨습니다 %q", w)
	}
}
