package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

type fakeRun struct{ calls []string }

func (f *fakeRun) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

func TestRegisterPlugin_두_호스트에_마켓과_플러그인을_등록한다(t *testing.T) {
	f := &fakeRun{}
	h := Hosts{Claude: "/bin/claude", Codex: "/bin/codex", Run: f.run}
	if err := h.RegisterPlugin("/m", "orbit", "orbit"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/bin/claude plugin marketplace add /m",
		"/bin/claude plugin install orbit@orbit --scope user",
		"/bin/codex plugin marketplace add /m",
		"/bin/codex plugin add orbit@orbit",
	}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(f.calls, "\n"))
	}
	f.calls = nil
	(&Hosts{Codex: "/bin/codex", Run: f.run}).RegisterPlugin("/m", "orbit", "orbit")
	if len(f.calls) != 2 {
		t.Fatalf("claude missing must be skipped: %v", f.calls)
	}
}

func TestCopyCodexAgents_내가_만든_파일만_덮어쓴다(t *testing.T) {
	plugin, codex := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(plugin, "codex", "agents"), 0o755)
	os.WriteFile(filepath.Join(plugin, "codex", "agents", "harness-worker.toml"), []byte("v1"), 0o644)
	os.WriteFile(filepath.Join(plugin, "codex", "agents", "harness-qa.toml"), []byte("qa"), 0o644)
	os.MkdirAll(filepath.Join(codex, "agents"), 0o755)
	os.WriteFile(filepath.Join(codex, "agents", "harness-qa.toml"), []byte("사용자 것"), 0o644)

	res, err := CopyCodexAgents(plugin, codex)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Copied) != 1 || len(res.Kept) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(codex, "agents", "harness-qa.toml")); string(b) != "사용자 것" {
		t.Fatal("user file overwritten")
	}
	os.WriteFile(filepath.Join(plugin, "codex", "agents", "harness-worker.toml"), []byte("v2"), 0o644)
	os.Remove(filepath.Join(plugin, "codex", "agents", "harness-qa.toml"))
	CopyCodexAgents(plugin, codex)
	if b, _ := os.ReadFile(filepath.Join(codex, "agents", "harness-worker.toml")); string(b) != "v2" {
		t.Fatal("owned file not updated")
	}
	os.WriteFile(filepath.Join(codex, "agents", "harness-worker.toml"), []byte("사용자가 고침"), 0o644)
	res, _ = CopyCodexAgents(plugin, codex)
	if b, _ := os.ReadFile(filepath.Join(codex, "agents", "harness-worker.toml")); string(b) != "사용자가 고침" || len(res.Kept) != 1 {
		t.Fatalf("user-edited owned file overwritten: %+v", res)
	}
}

func TestLaunchdPlist_매일_시각과_로그인_때(t *testing.T) {
	p := LaunchdPlist(Schedule{Oni: "/Users/me/.local/bin/oni", Hour: 4, Home: "/Users/me/orbit", PathDirs: []string{"/Users/me/.local/bin", "/opt/homebrew/bin"}, Logs: "/Users/me/orbit/logs"})
	for _, want := range []string{"<string>dev.onidot.orbit.retro</string>", "<string>/Users/me/.local/bin/oni</string>", "<string>retro</string>", "<string>run</string>",
		"<key>Hour</key>\n\t\t<integer>4</integer>", "<key>RunAtLoad</key>\n\t<true/>", "/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin", "<key>ORBIT_HOME</key>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q:\n%s", want, p)
		}
	}
}

func TestTaskXML_매일과_로그온과_놓치면_실행(t *testing.T) {
	b := TaskXML(Schedule{Oni: `C:\Users\me\AppData\Local\Programs\orbit\oni.exe`, Hour: 4, Home: `C:\o`, Logs: `C:\o\logs`})
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatal("UTF-16LE BOM missing")
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
	}
	x := string(utf16.Decode(u))
	for _, want := range []string{"<CalendarTrigger>", "T04:00:00", "<LogonTrigger>", "<StartWhenAvailable>true</StartWhenAvailable>", "<Command>cmd.exe</Command>", `oni.exe&#34; retro run`, "ORBIT_HOME=C:\\o", "retro.log", "PT6H"} {
		if !strings.Contains(x, want) {
			t.Errorf("xml missing %q:\n%s", want, x)
		}
	}
}

func TestOwnedManifest_JSON(t *testing.T) {
	dir := t.TempDir()
	saveOwned(dir, map[string]string{"a.toml": "h"})
	b, _ := os.ReadFile(filepath.Join(dir, ownedFile))
	var m map[string]string
	if json.Unmarshal(b, &m) != nil || m["a.toml"] != "h" {
		t.Fatal(string(b))
	}
}
