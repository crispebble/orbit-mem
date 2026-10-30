// Package install puts oni on this PC: the binary, the Orbit plugin in each
// host's marketplace, the Codex role files of a built plugin and the OS
// schedule for retrospectives (launchd on Mac, Task Scheduler on Windows).
package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/onidot-labs/orbit/internal/i18n"
)

// Hosts are the host CLIs found on this PC. An empty path means not installed.
type Hosts struct {
	Claude, Codex string
	Run           func(name string, args ...string) (string, error)
}

// RunCommand runs a command and returns its combined output.
func RunCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Stdin = nil
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %w\n%s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// FindHosts looks up claude and codex on PATH.
func FindHosts() Hosts {
	h := Hosts{Run: RunCommand}
	if p, err := exec.LookPath("claude"); err == nil {
		h.Claude = p
	}
	if p, err := exec.LookPath("codex"); err == nil {
		h.Codex = p
	}
	return h
}

// RegisterPlugin adds the marketplace at dir and installs plugin name from it
// in every host present. Both hosts accept the same commands again.
func (h Hosts) RegisterPlugin(dir, name, market string) error {
	var errs []error
	if h.Claude != "" {
		if _, err := h.Run(h.Claude, "plugin", "marketplace", "add", dir); err != nil {
			errs = append(errs, err)
		} else if _, err := h.Run(h.Claude, "plugin", "install", name+"@"+market, "--scope", "user"); err != nil {
			errs = append(errs, err)
		}
	}
	if h.Codex != "" {
		if _, err := h.Run(h.Codex, "plugin", "marketplace", "add", dir); err != nil {
			errs = append(errs, err)
		} else if _, err := h.Run(h.Codex, "plugin", "add", name+"@"+market); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// UnregisterPlugin removes plugin name and its marketplace from every host present.
func (h Hosts) UnregisterPlugin(name, market string) error {
	var errs []error
	if h.Claude != "" {
		if _, err := h.Run(h.Claude, "plugin", "uninstall", name+"@"+market, "--scope", "user"); err != nil {
			errs = append(errs, err)
		}
		if _, err := h.Run(h.Claude, "plugin", "marketplace", "remove", market); err != nil {
			errs = append(errs, err)
		}
	}
	if h.Codex != "" {
		if _, err := h.Run(h.Codex, "plugin", "remove", name+"@"+market); err != nil {
			errs = append(errs, err)
		}
		if _, err := h.Run(h.Codex, "plugin", "marketplace", "remove", market); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CopyResult says what happened to Codex role files.
type CopyResult struct {
	Copied  []string `json:"copied,omitempty"`
	Kept    []string `json:"kept,omitempty"`    // 사용자 파일이거나 사용자가 고쳐 그대로 둠
	Removed []string `json:"removed,omitempty"` // 더는 만들지 않는 내 파일
}

const ownedFile = ".orbit-owned.json"

func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func loadOwned(dir string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(dir, ownedFile)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func saveOwned(dir string, m map[string]string) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(dir, ownedFile), append(b, '\n'), 0o644)
}

// CopyCodexAgents copies a built plugin's codex/agents/*.toml into
// <codexHome>/agents. Codex plugins cannot carry roles, so they are installed
// as files. Only files Orbit wrote and nobody changed since are replaced or removed.
func CopyCodexAgents(pluginDir, codexHome string) (CopyResult, error) {
	var res CopyResult
	srcs, err := filepath.Glob(filepath.Join(pluginDir, "codex", "agents", "*.toml"))
	if err != nil {
		return res, err
	}
	dst := filepath.Join(codexHome, "agents")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return res, err
	}
	owned := loadOwned(dst)
	prefix := filepath.Base(pluginDir) + "-"
	want := map[string]bool{}
	for _, src := range srcs {
		name := filepath.Base(src)
		want[name] = true
		b, err := os.ReadFile(src)
		if err != nil {
			return res, err
		}
		target := filepath.Join(dst, name)
		cur, err := os.ReadFile(target)
		switch {
		case err == nil && bytes.Equal(cur, b):
			owned[name] = hashBytes(b)
			continue
		case err == nil && owned[name] != hashBytes(cur):
			res.Kept = append(res.Kept, target)
			continue
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return res, err
		}
		owned[name] = hashBytes(b)
		res.Copied = append(res.Copied, target)
	}
	for name, h := range owned {
		if want[name] || !strings.HasPrefix(name, prefix) {
			continue
		}
		target := filepath.Join(dst, name)
		cur, err := os.ReadFile(target)
		if err == nil && hashBytes(cur) == h {
			if os.Remove(target) == nil {
				res.Removed = append(res.Removed, target)
			}
		}
		delete(owned, name)
	}
	sort.Strings(res.Copied)
	return res, saveOwned(dst, owned)
}

// Schedule is the retrospective schedule of this PC.
type Schedule struct {
	Oni      string   // oni 절대경로
	Hour     int      // 하루 회고 시각
	Home     string   // ORBIT_HOME(기본 위치가 아니면)
	PathDirs []string // claude·codex가 있는 디렉터리
	Logs     string
}

// LaunchdLabel names the Mac schedule.
const LaunchdLabel = "dev.onidot.orbit.retro"

// WindowsTask names the Windows schedule.
const WindowsTask = "Orbit Retro"

func xmlEscape(s string) string { return html.EscapeString(s) }

// LaunchdPlist is the launchd agent: daily at Hour (a missed run during sleep
// runs on wake) and at login (catches up after the PC was off).
func LaunchdPlist(s Schedule) string {
	path := strings.Join(append(append([]string{}, s.PathDirs...), "/usr/bin", "/bin", "/usr/sbin", "/sbin"), ":")
	env := fmt.Sprintf("\t\t<key>PATH</key>\n\t\t<string>%s</string>\n", xmlEscape(path))
	if s.Home != "" {
		env += fmt.Sprintf("\t\t<key>ORBIT_HOME</key>\n\t\t<string>%s</string>\n", xmlEscape(s.Home))
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
		<string>retro</string>
		<string>run</string>
	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key>
		<integer>%d</integer>
		<key>Minute</key>
		<integer>0</integer>
	</dict>
	<key>RunAtLoad</key>
	<true/>
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
`, LaunchdLabel, xmlEscape(s.Oni), s.Hour, env, xmlEscape(filepath.Join(s.Logs, "retro.log")), xmlEscape(filepath.Join(s.Logs, "retro.log")))
}

// TaskXML is the Windows Task Scheduler definition (UTF-16LE with BOM, as
// schtasks /XML expects): daily at Hour, at this user's logon, and as soon as
// possible after a missed start. It runs through cmd.exe to set ORBIT_HOME and
// keep a log.
func TaskXML(s Schedule) []byte {
	user := os.Getenv("USERNAME")
	if d := os.Getenv("USERDOMAIN"); d != "" && user != "" {
		user = d + `\` + user
	}
	userID := ""
	if user != "" {
		userID = "<UserId>" + xmlEscape(user) + "</UserId>"
	}
	inner := ""
	if s.Home != "" {
		inner = `set "ORBIT_HOME=` + s.Home + `" && `
	}
	inner += `"` + s.Oni + `" retro run`
	if s.Logs != "" {
		inner += ` >> "` + filepath.Join(s.Logs, "retro.log") + `" 2>&1`
	}
	x := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Orbit 회고(하루·주간)</Description></RegistrationInfo>
  <Triggers>
    <CalendarTrigger>
      <StartBoundary>2026-01-01T%02d:00:00</StartBoundary>
      <ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay>
    </CalendarTrigger>
    <LogonTrigger>%s<Delay>PT5M</Delay></LogonTrigger>
  </Triggers>
  <Settings>
    <StartWhenAvailable>true</StartWhenAvailable>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <ExecutionTimeLimit>PT6H</ExecutionTimeLimit>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>cmd.exe</Command>
      <Arguments>/d /c %s</Arguments>
    </Exec>
  </Actions>
</Task>
`, s.Hour, userID, xmlEscape(inner))
	u := utf16.Encode([]rune(x))
	b := make([]byte, 2+2*len(u))
	b[0], b[1] = 0xFF, 0xFE
	for i, c := range u {
		b[2+2*i], b[3+2*i] = byte(c), byte(c>>8)
	}
	return b
}

// LaunchAgentPath is where the Mac schedule lives.
func LaunchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist")
}

// ApplySchedule registers the schedule with the OS.
func ApplySchedule(s Schedule, run func(string, ...string) (string, error)) (string, error) {
	if err := os.MkdirAll(s.Logs, 0o700); err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		path := LaunchAgentPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(LaunchdPlist(s)), 0o644); err != nil {
			return "", err
		}
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		_, _ = run("launchctl", "bootout", domain+"/"+LaunchdLabel)
		var err error
		for attempt := 0; attempt < 5; attempt++ { // bootout은 비동기라 곧바로 bootstrap하면 실패할 수 있다
			if _, err = run("launchctl", "bootstrap", domain, path); err == nil {
				return path, nil
			}
			time.Sleep(time.Second)
		}
		return path, err
	case "windows":
		dir, err := os.MkdirTemp("", "orbit-task")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		file := filepath.Join(dir, "task.xml")
		if err := os.WriteFile(file, TaskXML(s), 0o600); err != nil {
			return "", err
		}
		_, err = run("schtasks", "/Create", "/TN", WindowsTask, "/XML", file, "/F")
		return WindowsTask, err
	}
	return "", i18n.Errorf("install.err.no_schedule", runtime.GOOS)
}

// RemoveSchedule unregisters the schedule.
func RemoveSchedule(run func(string, ...string) (string, error)) error {
	switch runtime.GOOS {
	case "darwin":
		_, _ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchdLabel))
		err := os.Remove(LaunchAgentPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	case "windows":
		_, err := run("schtasks", "/Delete", "/TN", WindowsTask, "/F")
		return err
	}
	return nil
}

// ScheduleInstalled reports whether the OS schedule is registered (loaded).
func ScheduleInstalled(run func(string, ...string) (string, error)) bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := run("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchdLabel))
		return err == nil
	case "windows":
		_, err := run("schtasks", "/Query", "/TN", WindowsTask)
		return err == nil
	}
	return false
}

// DefaultBinDir is where oni is installed: ~/.local/bin on Mac and Linux,
// %LOCALAPPDATA%\Programs\orbit on Windows.
func DefaultBinDir() string {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Programs", "orbit")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

// BinaryName is oni's file name on this OS.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "oni.exe"
	}
	return "oni"
}

// CopyBinary installs the running oni at dst (atomically) unless it already is dst.
func CopyBinary(src, dst string) (bool, error) {
	if a, err := filepath.EvalSymlinks(src); err == nil {
		src = a
	}
	if b, err := filepath.EvalSymlinks(dst); err == nil && b == src {
		return false, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return false, err
	}
	if err := out.Close(); err != nil {
		return false, err
	}
	if runtime.GOOS == "windows" {
		old := dst + ".old"
		_ = os.Remove(old) // 지난번에 남은 것(실행 중이면 실패해도 된다)
		if _, err := os.Stat(dst); err == nil {
			if err := os.Rename(dst, old); err != nil { // 실행 중인 exe는 지울 수 없어 이름만 바꾼다
				os.Remove(tmp)
				return false, i18n.Errorf("install.err.replace_oni", err)
			}
			if err := os.Rename(tmp, dst); err != nil {
				_ = os.Rename(old, dst)
				return false, err
			}
			return true, nil
		}
	}
	return true, os.Rename(tmp, dst)
}
