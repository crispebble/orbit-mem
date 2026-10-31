package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 설계 · OpenOrbit 4차 R4-4 H10: 로그인 때 orbit-pi 데몬을 띄우는 launchd 에이전트(회고와 같은 방식).
func TestDaemonPlist_로그인때_뜨고_비정상_종료면_다시_뜬다(t *testing.T) {
	p := DaemonPlist(DaemonAgent{Orb: "/Users/me/.local/bin/orb", Home: "/Users/me/orbit", PathDirs: []string{"/opt/homebrew/bin", "/Users/me/.local/bin"}, Log: "/Users/me/orbit/runtime/daemon/daemon.log"})
	for _, want := range []string{
		"<string>dev.onidot.orbit.daemon</string>",
		"<string>/Users/me/.local/bin/orb</string>\n\t\t<string>daemon</string>\n\t\t<string>serve</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>",
		"/opt/homebrew/bin:/Users/me/.local/bin:/usr/bin:/bin",
		"<key>ORBIT_HOME</key>\n\t\t<string>/Users/me/orbit</string>",
		"<string>/Users/me/orbit/runtime/daemon/daemon.log</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(DaemonPlist(DaemonAgent{Orb: "/o", Log: "/l"}), "ORBIT_HOME") {
		t.Error("기본 ORBIT_HOME이면 넣지 않아야 합니다")
	}
}

func TestApplyAgent_파일을_쓰고_다시_올린다(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "LaunchAgents", DaemonLabel+".plist")
	var calls []string
	run := func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	if err := applyAgent(path, DaemonLabel, "<plist/>", run); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "<plist/>" {
		t.Errorf("plist %q", b)
	}
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "launchctl bootout gui/") || !strings.HasPrefix(calls[1], "launchctl bootstrap gui/") || !strings.HasSuffix(calls[1], path) {
		t.Errorf("calls %q", calls)
	}
	calls = nil
	if err := removeAgent(path, DaemonLabel, run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || len(calls) != 1 || !strings.Contains(calls[0], "bootout") {
		t.Errorf("remove: %v %q", err, calls)
	}
	if err := removeAgent(path, DaemonLabel, run); err != nil {
		t.Errorf("이미 없으면 그대로 성공: %v", err)
	}
}
