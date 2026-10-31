package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// DaemonLabel names the launchd agent that starts the orbit-pi daemon at login
// (설계 · OpenOrbit 4차 R4-4, 회고 일정과 같은 방식).
const DaemonLabel = "dev.onidot.orbit.daemon"

// DaemonAgent is what the login agent runs: orb daemon serve, with a PATH that
// has node, claude and codex, and ORBIT_HOME when it is not the default.
type DaemonAgent struct {
	Orb      string
	Home     string   // ORBIT_HOME(기본 위치가 아니면)
	PathDirs []string // node·claude·codex·orb가 있는 디렉터리
	Log      string
}

// DaemonPlist starts the daemon at login and again after a crash, but not after
// orb daemon stop (a clean exit).
func DaemonPlist(a DaemonAgent) string {
	path := strings.Join(append(append([]string{}, a.PathDirs...), "/usr/bin", "/bin", "/usr/sbin", "/sbin"), ":")
	env := fmt.Sprintf("\t\t<key>PATH</key>\n\t\t<string>%s</string>\n", xmlEscape(path))
	if a.Home != "" {
		env += fmt.Sprintf("\t\t<key>ORBIT_HOME</key>\n\t\t<string>%s</string>\n", xmlEscape(a.Home))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>EnvironmentVariables</key>
	<dict>
%s	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`, DaemonLabel, xmlEscape(a.Orb), env, xmlEscape(a.Log), xmlEscape(a.Log))
}

// DaemonAgentPath is where the login agent lives.
func DaemonAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", DaemonLabel+".plist")
}

// ApplyDaemonAgent registers the login agent; launchd starts the daemon right away.
func ApplyDaemonAgent(a DaemonAgent, run func(string, ...string) (string, error)) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", i18n.Errorf("install.err.no_daemon_agent", runtime.GOOS)
	}
	if err := os.MkdirAll(filepath.Dir(a.Log), 0o700); err != nil {
		return "", err
	}
	path := DaemonAgentPath()
	return path, applyAgent(path, DaemonLabel, DaemonPlist(a), run)
}

// RemoveDaemonAgent unregisters the login agent (and stops a daemon it started).
func RemoveDaemonAgent(run func(string, ...string) (string, error)) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return removeAgent(DaemonAgentPath(), DaemonLabel, run)
}

// DaemonAgentInstalled reports whether the login agent is loaded.
func DaemonAgentInstalled(run func(string, ...string) (string, error)) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := run("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), DaemonLabel))
	return err == nil
}

// applyAgent writes a launchd agent and (re)loads it in the user's GUI domain.
func applyAgent(path, label, plist string, run func(string, ...string) (string, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_, _ = run("launchctl", "bootout", domain+"/"+label)
	var err error
	for attempt := 0; attempt < 5; attempt++ { // bootout은 비동기라 곧바로 bootstrap하면 실패할 수 있다
		if _, err = run("launchctl", "bootstrap", domain, path); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return err
}

// removeAgent unloads a launchd agent and deletes its file; a missing one is fine.
func removeAgent(path, label string, run func(string, ...string) (string, error)) error {
	_, _ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
