package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// oldPlist is the launchd agent orbit-mem install wrote before 0.6.0 (dev.onidot.orbit.retro).
func oldPlist(bin, orbitHome string) string {
	env := "\t\t<key>PATH</key>\n\t\t<string>/usr/bin:/bin</string>\n"
	if orbitHome != "" {
		env += "\t\t<key>ORBIT_HOME</key>\n\t\t<string>" + orbitHome + "</string>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.onidot.orbit.retro</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + bin + `</string>
		<string>retro</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>EnvironmentVariables</key>
	<dict>
` + env + `	</dict>
</dict>
</plist>
`
}

// fakeSchedule replaces the OS commands of the schedule with a log: no test reaches launchctl.
func fakeSchedule(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	old := scheduleRun
	scheduleRun = func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	t.Cleanup(func() { scheduleRun = old })
	return &calls
}

// K4: install은 아무것도 예약하지 않고, 이 orbit-mem(같은 ORBIT_HOME)이 예전에 남긴 회고 예약만 지운다.
// orbit-code의 orb가 도는 예약과 다른 ORBIT_HOME의 예약은 그대로 둔다.
func TestInstall_옛_orbit_mem_회고_예약만_지운다(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd 예약")
	}
	for _, tc := range []struct {
		name, bin, plistHome string
		sameHome, noSchedule bool
		removed              bool
	}{
		{name: "같은 홈의 orbit-mem", bin: "/Users/me/.local/bin/orbit-mem", sameHome: true, removed: true},
		{name: "orbit-code의 orb", bin: "/Users/me/.local/bin/orb", sameHome: true},
		{name: "다른 ORBIT_HOME", bin: "/Users/me/.local/bin/orbit-mem", plistHome: "/Users/me/other-orbit"},
		{name: "ORBIT_HOME 없음(기본 홈)", bin: "/Users/me/.local/bin/orbit-mem"},
		{name: "--no-schedule", bin: "/Users/me/.local/bin/orbit-mem", sameHome: true, noSchedule: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakePC(t)
			calls := fakeSchedule(t)
			plistHome := tc.plistHome
			if tc.sameHome {
				plistHome = f.home
			}
			userHome, _ := os.UserHomeDir()
			path := filepath.Join(userHome, "Library", "LaunchAgents", "dev.onidot.orbit.retro.plist")
			if !strings.HasPrefix(path, os.TempDir()) && !strings.Contains(path, "/T/") {
				t.Fatalf("HOME is not a temp dir: %s", path)
			}
			os.MkdirAll(filepath.Dir(path), 0o755)
			os.WriteFile(path, []byte(oldPlist(tc.bin, plistHome)), 0o644)
			args := []string{"install", "--no-hosts", "--bin-dir", t.TempDir()}
			if tc.noSchedule {
				args = append(args, "--no-schedule")
			}
			if out, errOut, code := run(t, "", args...); code != 0 {
				t.Fatalf("%d %s %s", code, out, errOut)
			}
			_, err := os.Stat(path)
			if tc.removed {
				want := fmt.Sprintf("launchctl bootout gui/%d/dev.onidot.orbit.retro", os.Getuid())
				if !os.IsNotExist(err) || strings.Join(*calls, "|") != want {
					t.Fatalf("legacy schedule not removed: stat=%v calls=%v", err, *calls)
				}
				return
			}
			if err != nil || len(*calls) != 0 {
				t.Fatalf("schedule touched: stat=%v calls=%v", err, *calls)
			}
		})
	}
}

func TestInstall_예약이_없으면_아무것도_부르지_않는다(t *testing.T) {
	newFakePC(t)
	calls := fakeSchedule(t)
	if out, errOut, code := run(t, "", "install", "--no-hosts", "--bin-dir", t.TempDir()); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	userHome, _ := os.UserHomeDir()
	if len(*calls) != 0 {
		t.Fatalf("calls = %v", *calls)
	}
	if _, err := os.Stat(filepath.Join(userHome, "Library", "LaunchAgents", "dev.onidot.orbit.retro.plist")); !os.IsNotExist(err) {
		t.Fatalf("install scheduled something: %v", err)
	}
}

func TestUninstall_옛_orbit_mem_회고_예약만_지운다(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd 예약")
	}
	f := newFakePC(t)
	calls := fakeSchedule(t)
	userHome, _ := os.UserHomeDir()
	path := filepath.Join(userHome, "Library", "LaunchAgents", "dev.onidot.orbit.retro.plist")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(oldPlist("/Users/me/.local/bin/orb", f.home)), 0o644)
	if _, errOut, code := run(t, "", "uninstall"); code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if _, err := os.Stat(path); err != nil || len(*calls) != 0 {
		t.Fatalf("orb schedule touched: %v %v", err, *calls)
	}
	os.WriteFile(path, []byte(oldPlist("/Users/me/.local/bin/orbit-mem", f.home)), 0o644)
	if _, errOut, code := run(t, "", "uninstall"); code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || len(*calls) != 1 {
		t.Fatalf("legacy schedule left: %v %v", err, *calls)
	}
}
