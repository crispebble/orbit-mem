package install

import (
	"strings"
	"testing"
	"unicode/utf16"
)

type fakeRun struct{ calls []string }

func (f *fakeRun) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return "", nil
}

// 호스트 등록·해제 시험은 hosts_test.go에 있다.
func TestLaunchdPlist_매일_시각과_로그인_때(t *testing.T) {
	p := LaunchdPlist(Schedule{Bin: "/Users/me/.local/bin/orbit-mem", Hour: 4, Home: "/Users/me/orbit", PathDirs: []string{"/Users/me/.local/bin", "/opt/homebrew/bin"}, Logs: "/Users/me/orbit/logs"})
	for _, want := range []string{"<string>dev.onidot.orbit.retro</string>", "<string>/Users/me/.local/bin/orbit-mem</string>", "<string>retro</string>", "<string>run</string>",
		"<key>Hour</key>\n\t\t<integer>4</integer>", "<key>RunAtLoad</key>\n\t<true/>", "/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin", "<key>ORBIT_HOME</key>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q:\n%s", want, p)
		}
	}
}

func TestTaskXML_매일과_로그온과_놓치면_실행(t *testing.T) {
	b := TaskXML(Schedule{Bin: `C:\Users\me\AppData\Local\Programs\orbit\orbit-mem.exe`, Hour: 4, Home: `C:\o`, Logs: `C:\o\logs`})
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatal("UTF-16LE BOM missing")
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
	}
	x := string(utf16.Decode(u))
	for _, want := range []string{"<CalendarTrigger>", "T04:00:00", "<LogonTrigger>", "<StartWhenAvailable>true</StartWhenAvailable>", "<Command>cmd.exe</Command>", `orbit-mem.exe&#34; retro run`, "ORBIT_HOME=C:\\o", "retro.log", "PT6H"} {
		if !strings.Contains(x, want) {
			t.Errorf("xml missing %q:\n%s", want, x)
		}
	}
}
