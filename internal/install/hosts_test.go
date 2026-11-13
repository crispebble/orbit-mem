package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeHost answers the list commands with the given JSON and records every call. After a
// marketplace add, the marketplace list answers afterAdd when it is set (the added marketplace).
type fakeHost struct {
	calls    []string
	markets  string // claude plugin marketplace list --json
	plugins  string // claude plugin list --json
	afterAdd string
	fail     map[string]bool
}

func (f *fakeHost) run(name string, args ...string) (string, error) {
	line := filepath.Base(name) + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.fail[line] {
		return "boom", errors.New(line + ": boom")
	}
	switch a := strings.Join(args, " "); {
	case a == "plugin marketplace list --json":
		if f.markets == "" {
			return "[]", nil
		}
		return f.markets, nil
	case a == "plugin list --json":
		if f.plugins == "" {
			return "[]", nil
		}
		return f.plugins, nil
	case strings.HasPrefix(a, "plugin marketplace add ") && f.afterAdd != "":
		f.markets = f.afterAdd
	}
	return "", nil
}

func (f *fakeHost) writes() []string {
	var out []string
	for _, c := range f.calls {
		if !strings.HasSuffix(c, "list --json") {
			out = append(out, c)
		}
	}
	return out
}

func ownTarget(source string) ClaudeTarget {
	return ClaudeTarget{Source: source, Plugin: "orbit-mem", OwnMarket: "orbit-mem", Own: true}
}

func catalogTarget(source string) ClaudeTarget {
	return ClaudeTarget{Source: source, Plugin: "orbit-mem", OwnMarket: "orbit-mem"}
}

const (
	ownGitHub   = `{"name":"orbit-mem","source":"github","repo":"crispebble/orbit-mem"}`
	catalogDir  = `{"name":"orbit-plugins","source":"directory","path":"/w/orbit-plugins"}`
	catalogRepo = `{"name":"orbit-plugins","source":"github","repo":"CrispEbble/Orbit-Plugins"}`
	catalogGit  = `{"name":"orbit-plugins","source":"git","url":"https://github.com/crispebble/orbit-plugins.git"}`
)

func list(items ...string) string { return "[" + strings.Join(items, ",") + "]" }

// 끝 기준(기본값, 이전 동작과 같다): orbit-mem 자기 마켓은 없으면 add, 같은 출처면 update, 다른 출처면 remove 뒤 add,
// 그다음 깔려 있지 않으면 install --scope user, 깔려 있으면 update.
func TestRegisterClaude(t *testing.T) {
	cases := []struct {
		name, source, markets, plugins string
		want                           []string
	}{
		{"처음 GitHub", "crispebble/orbit-mem", "", "",
			[]string{"claude plugin marketplace add crispebble/orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}},
		{"같은 GitHub 마켓과 깔린 플러그인", "crispebble/orbit-mem", list(ownGitHub), `[{"id":"orbit-mem@orbit-mem","version":"0.3.0","enabled":true}]`,
			[]string{"claude plugin marketplace update orbit-mem", "claude plugin update orbit-mem@orbit-mem"}},
		{"같은 GitHub 저장소를 https 주소로 등록했다", "crispebble/orbit-mem", `[{"name":"orbit-mem","source":"git","url":"https://github.com/crispebble/orbit-mem.git"}]`, "",
			[]string{"claude plugin marketplace update orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}},
		{"GitHub에서 로컬 폴더로", "/h/plugins/orbit-mem", list(ownGitHub, `{"name":"orbit","source":"directory","path":"/x"}`), "",
			[]string{"claude plugin marketplace remove orbit-mem", "claude plugin marketplace add /h/plugins/orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}},
		{"같은 로컬 폴더", "/h/plugins/orbit-mem", `[{"name":"orbit-mem","source":"directory","path":"/h/plugins/orbit-mem"}]`, "",
			[]string{"claude plugin marketplace update orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeHost{markets: c.markets, plugins: c.plugins}
			h := Hosts{Claude: "/bin/claude", Run: f.run}
			if _, err := h.RegisterClaude(ownTarget(c.source)); err != nil {
				t.Fatal(err)
			}
			if got := f.writes(); !slices.Equal(got, c.want) {
				t.Fatalf("호출 %q", got)
			}
		})
	}
	// 마켓을 더하지 못하면 설치하지 않는다
	f := &fakeHost{fail: map[string]bool{"claude plugin marketplace add crispebble/orbit-mem": true}}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).RegisterClaude(ownTarget("crispebble/orbit-mem")); err == nil || len(f.writes()) != 1 {
		t.Fatalf("%v %q", err, f.writes())
	}
	// 목록을 읽지 못하는 claude: 자기 마켓은 이름을 알므로 add 뒤 install
	f = &fakeHost{fail: map[string]bool{"claude plugin marketplace list --json": true, "claude plugin list --json": true}}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).RegisterClaude(ownTarget("crispebble/orbit-mem")); err != nil ||
		!slices.Equal(f.writes(), []string{"claude plugin marketplace add crispebble/orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}) {
		t.Fatalf("%v %q", err, f.writes())
	}
}

// 끝 기준(카탈로그 마켓): 같은 출처로 이미 등록된 마켓(어느 표기든)은 update만 하고 remove·add하지 않는다. 마켓 이름은 목록에서
// 읽어 orbit-mem@<그 이름>을 깐다. 등록돼 있지 않으면 add 뒤 목록에서 이름을 읽는다. 카탈로그 마켓은 어떤 경우에도 지우지 않는다.
func TestRegisterClaude_카탈로그(t *testing.T) {
	cases := []struct {
		name, source, markets, afterAdd, plugins string
		want                                     []string
	}{
		{"setup.sh가 로컬 폴더로 등록해 둠", "/w/orbit-plugins", list(catalogDir), "", `[{"id":"superpowers@orbit-plugins","enabled":true,"scope":"user"}]`,
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user"}},
		{"owner/repo로 등록, 출처는 https 주소", "https://github.com/crispebble/orbit-plugins.git", list(catalogRepo), "", "",
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user"}},
		{"git 주소로 등록, 출처는 github:", "crispebble/orbit-plugins", list(catalogGit), "", `[{"id":"orbit-mem@orbit-plugins","enabled":true,"scope":"user"}]`,
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin update orbit-mem@orbit-plugins"}},
		{"아직 등록 안 됨", "crispebble/orbit-plugins", "", list(catalogRepo), "",
			[]string{"claude plugin marketplace add crispebble/orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user"}},
		{"옛 orbit-mem@orbit-mem에서 옮김", "/w/orbit-plugins", list(ownGitHub, catalogDir), "",
			`[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"},{"id":"superpowers@orbit-plugins","enabled":true,"scope":"user"}]`,
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user",
				"claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"}},
		{"새 것이 꺼져 있으면 켠 뒤 옛 것을 뺀다", "/w/orbit-plugins", list(ownGitHub, catalogDir), "",
			`[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"},{"id":"orbit-mem@orbit-plugins","enabled":false,"scope":"user"}]`,
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin update orbit-mem@orbit-plugins", "claude plugin enable orbit-mem@orbit-plugins",
				"claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"}},
		{"플러그인 없이 남은 자기 마켓도 뺀다", "/w/orbit-plugins", list(ownGitHub, catalogDir), "", "",
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user", "claude plugin marketplace remove orbit-mem"}},
		{"자기 마켓에 다른 플러그인이 있으면 마켓은 둔다", "/w/orbit-plugins", list(ownGitHub, catalogDir), "",
			`[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"},{"id":"extra@orbit-mem","enabled":true,"scope":"user"}]`,
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user", "claude plugin uninstall orbit-mem@orbit-mem --scope user"}},
		{"프로젝트 범위의 것은 건드리지 않는다", "/w/orbit-plugins", list(catalogDir), "", `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"project"}]`,
			[]string{"claude plugin marketplace update orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeHost{markets: c.markets, plugins: c.plugins, afterAdd: c.afterAdd}
			h := Hosts{Claude: "/bin/claude", Run: f.run}
			if _, err := h.RegisterClaude(catalogTarget(c.source)); err != nil {
				t.Fatal(err)
			}
			got := f.writes()
			if !slices.Equal(got, c.want) {
				t.Fatalf("호출 %q", got)
			}
			for _, w := range got {
				if strings.Contains(w, "marketplace remove orbit-plugins") {
					t.Fatalf("카탈로그 마켓을 지웠습니다: %q", got)
				}
			}
		})
	}
	// 목록을 읽지 못하면 마켓 이름을 알 수 없다: 아무것도 바꾸지 않고 실패한다
	f := &fakeHost{fail: map[string]bool{"claude plugin marketplace list --json": true}}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).RegisterClaude(catalogTarget("crispebble/orbit-plugins")); err == nil || len(f.writes()) != 0 {
		t.Fatalf("%v %q", err, f.writes())
	}
	// add 뒤에도 그 출처의 마켓이 목록에 없으면 실패하고 설치하지 않는다
	f = &fakeHost{}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).RegisterClaude(catalogTarget("crispebble/orbit-plugins")); err == nil ||
		!slices.Equal(f.writes(), []string{"claude plugin marketplace add crispebble/orbit-plugins"}) {
		t.Fatalf("%v %q", err, f.writes())
	}
	// 새 것을 깔지 못하면 옛 것을 빼지 않는다
	f = &fakeHost{markets: list(ownGitHub, catalogDir), plugins: `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"}]`,
		fail: map[string]bool{"claude plugin install orbit-mem@orbit-plugins --scope user": true}}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).RegisterClaude(catalogTarget("/w/orbit-plugins")); err == nil {
		t.Fatal("실패를 알리지 않았습니다")
	}
	for _, w := range f.writes() {
		if strings.Contains(w, "uninstall") || strings.Contains(w, "remove") {
			t.Fatalf("새 것을 못 깔았는데 옛 것을 뺐습니다: %q", f.writes())
		}
	}
}

// 끝 기준(기본값으로 돌아옴): 자기 마켓에서 깔 때 카탈로그의 orbit-mem이 깔려 있으면 그 플러그인만 빼고 카탈로그 마켓은 둔다.
func TestRegisterClaude_자기_마켓으로_돌아옴(t *testing.T) {
	f := &fakeHost{markets: list(ownGitHub, catalogDir),
		plugins: `[{"id":"orbit-mem@orbit-plugins","enabled":true,"scope":"user"},{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"},{"id":"superpowers@orbit-plugins","enabled":true,"scope":"user"}]`}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).RegisterClaude(ownTarget("crispebble/orbit-mem")); err != nil {
		t.Fatal(err)
	}
	want := []string{"claude plugin marketplace update orbit-mem", "claude plugin update orbit-mem@orbit-mem", "claude plugin uninstall orbit-mem@orbit-plugins --scope user"}
	if got := f.writes(); !slices.Equal(got, want) {
		t.Fatalf("호출 %q", got)
	}
}

// 끝 기준: 계획(--dry-run과 단계)은 목록만 읽고 아무것도 바꾸지 않으며, 옛 플러그인과 자기 마켓을 빼는 명령을 그대로 보인다.
// 아직 등록하지 않은 카탈로그는 이름을 모르므로 자리표시로 보인다.
func TestPlanClaude(t *testing.T) {
	f := &fakeHost{markets: list(ownGitHub, catalogDir), plugins: `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"}]`}
	p, err := (Hosts{Claude: "/bin/claude", Run: f.run}).PlanClaude(catalogTarget("/w/orbit-plugins"))
	if err != nil || len(f.writes()) != 0 {
		t.Fatalf("%v %q", err, f.writes())
	}
	var got []string
	for _, s := range p.Steps {
		got = append(got, "claude "+strings.Join(s.Args, " "))
	}
	want := []string{"claude plugin marketplace update orbit-plugins", "claude plugin install orbit-mem@orbit-plugins --scope user",
		"claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"}
	if !slices.Equal(got, want) || p.Market != "orbit-plugins" || p.ID != "orbit-mem@orbit-plugins" {
		t.Fatalf("%q %+v", got, p)
	}
	for _, s := range p.Steps[2:] {
		if s.Note == "" || !strings.Contains(s.String(), s.Note) || !strings.HasPrefix(s.String(), "claude plugin ") {
			t.Errorf("빼는 단계에 까닭이 없습니다: %q", s.String())
		}
	}
	// 아직 등록하지 않은 카탈로그
	f = &fakeHost{plugins: `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"}]`}
	p, err = (Hosts{Claude: "/bin/claude", Run: f.run}).PlanClaude(catalogTarget("crispebble/orbit-plugins"))
	if err != nil || p.Market != "" || len(f.writes()) != 0 {
		t.Fatalf("%v %+v %q", err, p, f.writes())
	}
	got = nil
	for _, s := range p.Steps {
		got = append(got, "claude "+strings.Join(s.Args, " "))
	}
	if len(got) != 3 || got[0] != "claude plugin marketplace add crispebble/orbit-plugins" || !strings.HasPrefix(got[1], "claude plugin install orbit-mem@") ||
		got[2] != "claude plugin uninstall orbit-mem@orbit-mem --scope user" {
		t.Fatalf("%q", got)
	}
}

// 끝 기준: Codex 등록은 orbit-code 하네스 registerPlugin과 같다: 로컬 폴더 마켓 add 뒤 plugin add(다시 해도 된다).
func TestRegisterCodex(t *testing.T) {
	f := &fakeHost{}
	h := Hosts{Codex: "/bin/codex", Run: f.run}
	if err := h.RegisterCodex("/h/plugins/orbit-mem", "orbit-mem", "orbit-mem"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"codex plugin marketplace add /h/plugins/orbit-mem", "codex plugin add orbit-mem@orbit-mem"}; !slices.Equal(f.calls, want) {
		t.Fatalf("호출 %q", f.calls)
	}
}

const codexConfig = `model = "x"

[marketplaces.orbit-mem]
source_type = "local"
source = "/h/plugins/orbit-mem"

[marketplaces.orbit]
source_type = "local"

[plugins."orbit-mem@orbit-mem"]
enabled = true

[plugins."orbit@orbit"]
enabled = true

[hooks.state."orbit-mem@orbit-mem:hooks/codex.json:session_start:0:0"]
trusted_hash = "sha256:abc"
`

// 끝 기준: Codex 상태는 config.toml의 [marketplaces.orbit-mem]·[plugins."orbit-mem@orbit-mem"]과
// [hooks.state."orbit-mem@orbit-mem:hooks/codex.json:…"] 항목이 있는지로 읽는다.
func TestReadCodexState(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	os.WriteFile(p, []byte(codexConfig), 0o600)
	st := ReadCodexState(p, "orbit-mem", "orbit-mem")
	if !st.Marketplace || st.MarketplaceSource != "/h/plugins/orbit-mem" || !st.Plugin || !st.Enabled || !st.HooksTrusted {
		t.Fatalf("%+v", st)
	}
	os.WriteFile(p, []byte(strings.ReplaceAll(strings.ReplaceAll(codexConfig, "orbit-mem@orbit-mem:hooks", "x@y:hooks"), "enabled = true\n\n[plugins.\"orbit@orbit\"]", "enabled = false\n\n[plugins.\"orbit@orbit\"]")), 0o600)
	st = ReadCodexState(p, "orbit-mem", "orbit-mem")
	if !st.Plugin || st.Enabled || st.HooksTrusted {
		t.Fatalf("끔·신뢰 없음 %+v", st)
	}
	st = ReadCodexState(filepath.Join(dir, "none.toml"), "orbit-mem", "orbit-mem")
	if st.Marketplace || st.Plugin || st.HooksTrusted {
		t.Fatalf("파일 없음 %+v", st)
	}
	os.WriteFile(p, []byte("[marketplaces.\"orbit-mem\"]\nsource = 'x'\n"), 0o600)
	if st := ReadCodexState(p, "orbit-mem", "orbit-mem"); !st.Marketplace || st.MarketplaceSource != "x" {
		t.Fatalf("따옴표 키 %+v", st)
	}
}

// 끝 기준: 해제는 있는 것만 뺀다: Claude는 orbit-mem 플러그인을 plugin uninstall --scope user로 빼고, 마켓은 orbit-mem 자기
// 마켓(이름 orbit-mem)만 marketplace remove한다. Codex는 plugin remove와 marketplace remove. 데이터·파일은 건드리지 않는다.
func TestUnregister(t *testing.T) {
	f := &fakeHost{markets: list(ownGitHub), plugins: `[{"id":"orbit-mem@orbit-mem","scope":"user"}]`}
	h := Hosts{Claude: "/bin/claude", Codex: "/bin/codex", Run: f.run}
	if _, err := h.UnregisterClaude(ownTarget("crispebble/orbit-mem")); err != nil {
		t.Fatal(err)
	}
	if err := h.UnregisterCodex(CodexState{Marketplace: true, Plugin: true}, "orbit-mem", "orbit-mem"); err != nil {
		t.Fatal(err)
	}
	want := []string{"claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem", "codex plugin remove orbit-mem@orbit-mem", "codex plugin marketplace remove orbit-mem"}
	if got := f.writes(); !slices.Equal(got, want) {
		t.Fatalf("호출 %q", got)
	}
	f = &fakeHost{}
	h = Hosts{Claude: "/bin/claude", Codex: "/bin/codex", Run: f.run}
	if _, err := h.UnregisterClaude(ownTarget("crispebble/orbit-mem")); err != nil || len(f.writes()) != 0 {
		t.Fatalf("없는 것을 뺐습니다 %v %q", err, f.writes())
	}
	if err := h.UnregisterCodex(CodexState{}, "orbit-mem", "orbit-mem"); err != nil || len(f.writes()) != 0 {
		t.Fatalf("없는 것을 뺐습니다 %v %q", err, f.writes())
	}
	// 목록을 읽지 못하면 자기 마켓은 둘 다 해 본다
	f = &fakeHost{fail: map[string]bool{"claude plugin marketplace list --json": true, "claude plugin list --json": true}}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).UnregisterClaude(ownTarget("crispebble/orbit-mem")); err != nil ||
		!slices.Equal(f.writes(), []string{"claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"}) {
		t.Fatalf("%v %q", err, f.writes())
	}
}

// 끝 기준: 카탈로그 마켓으로 깐 orbit-mem은 플러그인만 빼고 카탈로그 마켓은 둔다(그 마켓의 다른 플러그인이 지워지지 않게).
// 남은 옛 orbit-mem@orbit-mem과 자기 마켓은 함께 뺀다.
func TestUnregister_카탈로그(t *testing.T) {
	f := &fakeHost{markets: list(catalogDir), plugins: `[{"id":"orbit-mem@orbit-plugins","scope":"user"},{"id":"superpowers@orbit-plugins","scope":"user"}]`}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).UnregisterClaude(catalogTarget("/w/orbit-plugins")); err != nil {
		t.Fatal(err)
	}
	if got := f.writes(); !slices.Equal(got, []string{"claude plugin uninstall orbit-mem@orbit-plugins --scope user"}) {
		t.Fatalf("호출 %q", got)
	}
	f = &fakeHost{markets: list(ownGitHub, catalogDir), plugins: `[{"id":"orbit-mem@orbit-plugins","scope":"user"},{"id":"orbit-mem@orbit-mem","scope":"user"},{"id":"superpowers@orbit-plugins","scope":"user"}]`}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).UnregisterClaude(catalogTarget("/w/orbit-plugins")); err != nil {
		t.Fatal(err)
	}
	want := []string{"claude plugin uninstall orbit-mem@orbit-plugins --scope user", "claude plugin uninstall orbit-mem@orbit-mem --scope user", "claude plugin marketplace remove orbit-mem"}
	if got := f.writes(); !slices.Equal(got, want) {
		t.Fatalf("호출 %q", got)
	}
	// 플러그인 목록을 읽지 못하면 카탈로그는 마켓 목록에서 이름을 찾아 그 플러그인만 빼 본다
	f = &fakeHost{markets: list(catalogDir), fail: map[string]bool{"claude plugin list --json": true}}
	if _, err := (Hosts{Claude: "/bin/claude", Run: f.run}).UnregisterClaude(catalogTarget("/w/orbit-plugins")); err != nil ||
		!slices.Equal(f.writes(), []string{"claude plugin uninstall orbit-mem@orbit-plugins --scope user"}) {
		t.Fatalf("%v %q", err, f.writes())
	}
}

// 끝 기준: Claude 상태는 설정이 가리키는 마켓(출처로 찾은 이름)의 orbit-mem을 plugin list --json에서 읽고, 실패하면 알 수 없음이다.
// 다른 마켓에서도 깔린 orbit-mem은 Others로 알린다.
func TestClaudeStatus(t *testing.T) {
	f := &fakeHost{markets: `[{"name":"orbit-mem","source":"directory","path":"/h/p"}]`, plugins: `[{"id":"orbit-mem@orbit-mem","version":"0.4.0","enabled":false,"scope":"user"}]`}
	st := Hosts{Claude: "/bin/claude", Run: f.run}.ClaudeStatus(ownTarget("/h/p"))
	if !st.Known || !st.Installed || st.ID != "orbit-mem@orbit-mem" || st.Version != "0.4.0" || st.Enabled || st.Marketplace != "directory:/h/p" || len(st.Others) != 0 {
		t.Fatalf("%+v", st)
	}
	f = &fakeHost{fail: map[string]bool{"claude plugin list --json": true}}
	if st := (Hosts{Claude: "/bin/claude", Run: f.run}).ClaudeStatus(ownTarget("crispebble/orbit-mem")); st.Known {
		t.Fatalf("%+v", st)
	}
	// 카탈로그: 이름은 목록에서, 겹친 옛 플러그인은 Others
	f = &fakeHost{markets: list(ownGitHub, catalogGit), plugins: `[{"id":"orbit-mem@orbit-plugins","version":"0.18.0","enabled":true,"scope":"user"},{"id":"orbit-mem@orbit-mem","version":"0.17.1","enabled":true,"scope":"user"},{"id":"superpowers@orbit-plugins","enabled":true,"scope":"user"}]`}
	st = Hosts{Claude: "/bin/claude", Run: f.run}.ClaudeStatus(catalogTarget("crispebble/orbit-plugins"))
	if !st.Known || !st.Installed || st.ID != "orbit-mem@orbit-plugins" || st.Version != "0.18.0" || st.Marketplace != "git:https://github.com/crispebble/orbit-plugins.git" ||
		len(st.Others) != 1 || st.Others[0].ID != "orbit-mem@orbit-mem" {
		t.Fatalf("%+v", st)
	}
	// 카탈로그를 아직 등록하지 않음: 깔려 있지 않고, 옛 것은 Others
	f = &fakeHost{markets: list(ownGitHub), plugins: `[{"id":"orbit-mem@orbit-mem","enabled":true,"scope":"user"}]`}
	st = Hosts{Claude: "/bin/claude", Run: f.run}.ClaudeStatus(catalogTarget("crispebble/orbit-plugins"))
	if !st.Known || st.Installed || st.Marketplace != "" || len(st.Others) != 1 {
		t.Fatalf("%+v", st)
	}
	if f.writes() != nil {
		t.Fatalf("상태가 무언가를 바꿨습니다 %q", f.writes())
	}
}
