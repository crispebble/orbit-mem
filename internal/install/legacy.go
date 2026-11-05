package install

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

// Until 0.6.0 orbit-mem install registered the retrospective schedule itself; the
// retrospectives now run in orbit-code, whose orb install registers its own schedule,
// possibly under the same names. These find and remove only the schedule this orbit-mem
// left: one that runs an orbit-mem binary for the same ORBIT_HOME.

// LaunchdLabel names the old Mac schedule.
const LaunchdLabel = "dev.onidot.orbit.retro"

// WindowsTask names the old Windows schedule.
const WindowsTask = "Orbit Retro"

// LaunchAgentPath is where the old Mac schedule lives.
func LaunchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist")
}

// RemoveLegacySchedule removes the retrospective schedule an earlier orbit-mem install
// registered for home (this orbit-mem's ORBIT_HOME; defaultHome is the home without
// ORBIT_HOME). A schedule that runs another program (orbit-code's orb) or belongs to
// another home is left alone. It says whether it removed one.
func RemoveLegacySchedule(goos, home, defaultHome string, run func(string, ...string) (string, error)) (bool, error) {
	switch goos {
	case "darwin":
		path := LaunchAgentPath()
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		bin, plistHome, err := parseLaunchAgent(b)
		if err != nil || !orbitMemBinary(bin) || !sameHome(plistHome, home, defaultHome) {
			return false, nil // 알아볼 수 없거나 남의 예약: 건드리지 않는다
		}
		_, _ = run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchdLabel))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		return true, nil
	case "windows":
		out, err := run("schtasks", "/Query", "/TN", WindowsTask, "/XML")
		if err != nil || !legacyTask(out, home, defaultHome) {
			return false, nil
		}
		if _, err := run("schtasks", "/Delete", "/TN", WindowsTask, "/F"); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func orbitMemBinary(path string) bool {
	base := filepath.Base(strings.ReplaceAll(path, `\`, "/"))
	return strings.EqualFold(base, "orbit-mem") || strings.EqualFold(base, "orbit-mem.exe")
}

// sameHome says whether a schedule's ORBIT_HOME ("" = the default home) is home.
func sameHome(scheduled, home, defaultHome string) bool {
	if scheduled == "" {
		scheduled = defaultHome
	}
	clean := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return clean(scheduled) == clean(home)
}

type plistNode struct {
	XMLName xml.Name
	Text    string      `xml:",chardata"`
	Nodes   []plistNode `xml:",any"`
}

// pairs walks a plist dict's key and value elements.
func (n plistNode) pairs(f func(key string, v plistNode)) {
	for i := 0; i+1 < len(n.Nodes); i++ {
		if n.Nodes[i].XMLName.Local == "key" {
			f(strings.TrimSpace(n.Nodes[i].Text), n.Nodes[i+1])
			i++
		}
	}
}

// parseLaunchAgent reads ProgramArguments[0] and EnvironmentVariables.ORBIT_HOME of a plist.
func parseLaunchAgent(b []byte) (bin, home string, err error) {
	var root plistNode
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	if err := d.Decode(&root); err != nil {
		return "", "", err
	}
	if len(root.Nodes) == 0 || root.Nodes[0].XMLName.Local != "dict" {
		return "", "", fmt.Errorf("plist without a dict")
	}
	root.Nodes[0].pairs(func(key string, v plistNode) {
		switch {
		case key == "ProgramArguments" && v.XMLName.Local == "array" && len(v.Nodes) > 0:
			bin = strings.TrimSpace(v.Nodes[0].Text)
		case key == "EnvironmentVariables" && v.XMLName.Local == "dict":
			v.pairs(func(k string, e plistNode) {
				if k == "ORBIT_HOME" {
					home = strings.TrimSpace(e.Text)
				}
			})
		}
	})
	return bin, home, nil
}

// legacyTask says whether a Task Scheduler definition (schtasks /Query /XML) runs this
// orbit-mem's retro run for home.
func legacyTask(x, home, defaultHome string) bool {
	x = html.UnescapeString(x)
	if !strings.Contains(x, "retro run") || !strings.Contains(strings.ToLower(x), "orbit-mem.exe") {
		return false
	}
	scheduled := ""
	if i := strings.Index(x, `set "ORBIT_HOME=`); i >= 0 {
		rest := x[i+len(`set "ORBIT_HOME=`):]
		if j := strings.Index(rest, `"`); j >= 0 {
			scheduled = rest[:j]
		}
	}
	return sameHome(scheduled, home, defaultHome)
}
