package install

import (
	"errors"
	"strings"
	"testing"
)

const plistOf = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.onidot.orbit.retro</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/me/.local/bin/orbit-mem</string>
		<string>retro</string>
		<string>run</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/bin:/bin</string>
		<key>ORBIT_HOME</key>
		<string>/Users/me/o &amp; b</string>
	</dict>
</dict>
</plist>
`

func TestParseLaunchAgent_실행_파일과_ORBIT_HOME을_읽는다(t *testing.T) {
	bin, home, err := parseLaunchAgent([]byte(plistOf))
	if err != nil || bin != "/Users/me/.local/bin/orbit-mem" || home != "/Users/me/o & b" {
		t.Fatalf("bin=%q home=%q err=%v", bin, home, err)
	}
	noEnv := strings.Replace(plistOf, "<key>ORBIT_HOME</key>\n\t\t<string>/Users/me/o &amp; b</string>\n", "", 1)
	if _, home, _ := parseLaunchAgent([]byte(noEnv)); home != "" {
		t.Fatalf("home without ORBIT_HOME = %q", home)
	}
	if _, _, err := parseLaunchAgent([]byte("not xml")); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestOrbitMemBinary(t *testing.T) {
	for p, want := range map[string]bool{"/x/orbit-mem": true, `C:\x\orbit-mem.exe`: true, "/x/orb": false, "/x/orbit-mem-old": false, `C:\x\ORB.EXE`: false} {
		if orbitMemBinary(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}

// Windows: schtasks /Query /XML가 이 orbit-mem의 retro run이고 같은 홈일 때만 지운다.
func TestRemoveLegacySchedule_Windows(t *testing.T) {
	task := func(bin, home string) string {
		inner := ""
		if home != "" {
			inner = `set &quot;ORBIT_HOME=` + home + `&quot; &amp;&amp; `
		}
		return `<Task><Actions><Exec><Command>cmd.exe</Command><Arguments>/d /c ` + inner + `&quot;` + bin + `&quot; retro run &gt;&gt; &quot;C:\o\logs\retro.log&quot; 2&gt;&amp;1</Arguments></Exec></Actions></Task>`
	}
	for _, tc := range []struct {
		name, xml string
		queryErr  error
		removed   bool
	}{
		{"같은 홈", task(`C:\Users\me\AppData\Local\Programs\orbit\orbit-mem.exe`, `C:\o`), nil, true},
		{"기본 홈", task(`C:\Users\me\AppData\Local\Programs\orbit\orbit-mem.exe`, ""), nil, false},
		{"orb", task(`C:\Users\me\AppData\Local\Programs\orbit\orb.exe`, `C:\o`), nil, false},
		{"다른 홈", task(`C:\x\orbit-mem.exe`, `C:\other`), nil, false},
		{"예약 없음", "", errors.New("not found"), false},
	} {
		var calls []string
		run := func(name string, args ...string) (string, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if args[0] == "/Query" {
				return tc.xml, tc.queryErr
			}
			return "", nil
		}
		removed, err := RemoveLegacySchedule("windows", `C:\o`, `C:\default`, run)
		deleted := len(calls) == 2 && calls[1] == "schtasks /Delete /TN Orbit Retro /F"
		if err != nil || removed != tc.removed || deleted != tc.removed {
			t.Errorf("%s: removed=%v err=%v calls=%v", tc.name, removed, err, calls)
		}
	}
}

func TestRemoveLegacySchedule_다른_OS는_아무것도_하지_않는다(t *testing.T) {
	removed, err := RemoveLegacySchedule("linux", "/h", "/d", func(string, ...string) (string, error) {
		t.Fatal("called")
		return "", nil
	})
	if removed || err != nil {
		t.Fatalf("%v %v", removed, err)
	}
}
