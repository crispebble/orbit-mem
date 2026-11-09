package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeHost answers the list commands with the given JSON and records every call.
type fakeHost struct {
	calls   []string
	markets string // claude plugin marketplace list --json
	plugins string // claude plugin list --json
	fail    map[string]bool
}

func (f *fakeHost) run(name string, args ...string) (string, error) {
	line := filepath.Base(name) + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.fail[line] {
		return "boom", errors.New(line + ": boom")
	}
	switch strings.Join(args, " ") {
	case "plugin marketplace list --json":
		if f.markets == "" {
			return "[]", nil
		}
		return f.markets, nil
	case "plugin list --json":
		if f.plugins == "" {
			return "[]", nil
		}
		return f.plugins, nil
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

// 끝 기준: Claude 등록은 마켓이 없으면 add, 같은 출처면 update, 다른 출처면 remove 뒤 add, 그다음
// 깔려 있지 않으면 install --scope user, 깔려 있으면 update.
func TestRegisterClaude(t *testing.T) {
	cases := []struct {
		name, source, markets, plugins string
		want                           []string
	}{
		{"처음 GitHub", "crispebble/orbit-mem", "", "",
			[]string{"claude plugin marketplace add crispebble/orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}},
		{"같은 GitHub 마켓과 깔린 플러그인", "crispebble/orbit-mem", `[{"name":"orbit-mem","source":"github","repo":"crispebble/orbit-mem"}]`, `[{"id":"orbit-mem@orbit-mem","version":"0.3.0","enabled":true}]`,
			[]string{"claude plugin marketplace update orbit-mem", "claude plugin update orbit-mem@orbit-mem"}},
		{"GitHub에서 로컬 폴더로", "/h/plugins/orbit-mem", `[{"name":"orbit-mem","source":"github","repo":"crispebble/orbit-mem"},{"name":"orbit","source":"directory","path":"/x"}]`, "",
			[]string{"claude plugin marketplace remove orbit-mem", "claude plugin marketplace add /h/plugins/orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}},
		{"같은 로컬 폴더", "/h/plugins/orbit-mem", `[{"name":"orbit-mem","source":"directory","path":"/h/plugins/orbit-mem"}]`, "",
			[]string{"claude plugin marketplace update orbit-mem", "claude plugin install orbit-mem@orbit-mem --scope user"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeHost{markets: c.markets, plugins: c.plugins}
			h := Hosts{Claude: "/bin/claude", Run: f.run}
			if err := h.RegisterClaude(c.source, "orbit-mem", "orbit-mem"); err != nil {
				t.Fatal(err)
			}
			if got := f.writes(); !slices.Equal(got, c.want) {
				t.Fatalf("호출 %q", got)
			}
		})
	}
	// 마켓을 더하지 못하면 설치하지 않는다
	f := &fakeHost{fail: map[string]bool{"claude plugin marketplace add crispebble/orbit-mem": true}}
	if err := (Hosts{Claude: "/bin/claude", Run: f.run}).RegisterClaude("crispebble/orbit-mem", "orbit-mem", "orbit-mem"); err == nil || len(f.writes()) != 1 {
		t.Fatalf("%v %q", err, f.writes())
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

// 끝 기준: 해제는 있는 것만 뺀다: Claude는 plugin uninstall --scope user와 marketplace remove,
// Codex는 plugin remove와 marketplace remove. 데이터·파일은 건드리지 않는다.
func TestUnregister(t *testing.T) {
	f := &fakeHost{markets: `[{"name":"orbit-mem","source":"github","repo":"crispebble/orbit-mem"}]`, plugins: `[{"id":"orbit-mem@orbit-mem"}]`}
	h := Hosts{Claude: "/bin/claude", Codex: "/bin/codex", Run: f.run}
	if err := h.UnregisterClaude("orbit-mem", "orbit-mem"); err != nil {
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
	if err := h.UnregisterClaude("orbit-mem", "orbit-mem"); err != nil || len(f.writes()) != 0 {
		t.Fatalf("없는 것을 뺐습니다 %v %q", err, f.writes())
	}
	if err := h.UnregisterCodex(CodexState{}, "orbit-mem", "orbit-mem"); err != nil || len(f.writes()) != 0 {
		t.Fatalf("없는 것을 뺐습니다 %v %q", err, f.writes())
	}
}

// 끝 기준: Claude 상태는 plugin list --json에서 읽고, 실패하면 알 수 없음이다.
func TestClaudeStatus(t *testing.T) {
	f := &fakeHost{markets: `[{"name":"orbit-mem","source":"directory","path":"/h/p"}]`, plugins: `[{"id":"orbit-mem@orbit-mem","version":"0.4.0","enabled":false,"scope":"user"}]`}
	st := Hosts{Claude: "/bin/claude", Run: f.run}.ClaudeStatus("orbit-mem", "orbit-mem")
	if !st.Known || !st.Installed || st.Version != "0.4.0" || st.Enabled || st.Marketplace != "directory:/h/p" {
		t.Fatalf("%+v", st)
	}
	f = &fakeHost{fail: map[string]bool{"claude plugin list --json": true}}
	if st := (Hosts{Claude: "/bin/claude", Run: f.run}).ClaudeStatus("orbit-mem", "orbit-mem"); st.Known {
		t.Fatalf("%+v", st)
	}
}
