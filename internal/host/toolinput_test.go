package host_test

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/host"
)

func TestToolInputText_Bash는_command만_Edit는_file_path를_돌려준다(t *testing.T) {
	long := strings.Repeat("가", 2500)
	for _, c := range []struct {
		tool, raw, want string
	}{
		{"Bash", `{"command":"git commit -m x","description":"커밋한다"}`, "git commit -m x"},
		{"Edit", `{"file_path":"/a/b.go","old_string":"x","new_string":"y"}`, "/a/b.go"},
		{"Read", `{"file_path":"/a/c.go","limit":10}`, "/a/c.go"},
		{"Write", `{"content":"본문","file_path":"/a/d.go"}`, "/a/d.go"},
		// 그 밖의 도구는 문자열 값을 글에 나온 차례로 잇는다: 키·숫자·참거짓은 빼고, 안쪽 값도 넣는다.
		{"Grep", `{"pattern":"foo","path":"/src","-n":true,"head_limit":5}`, "foo /src"},
		{"mcp__x__find", `{"q":"배포 창","opts":{"scope":"user"},"list":["a",1,"b"]}`, "배포 창 user a b"},
		// 이름이 정한 칸이 없거나 문자열이 아니면 빈 글이다(다른 칸으로 메우지 않는다).
		{"Bash", `{"description":"설명만"}`, ""},
		{"Edit", `{"file_path":7}`, ""},
		{"Bash", ``, ""},
		{"Bash", `{"command":`, ""},
		{"Grep", `null`, ""},
		{"Bash", `{"command":"  ls -la  "}`, "ls -la"},
	} {
		if got := host.ToolInputText(c.tool, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.tool, c.raw, got, c.want)
		}
	}
	got := host.ToolInputText("Bash", json.RawMessage(`{"command":"`+long+`"}`))
	if n := utf8.RuneCountInString(got); host.MaxToolInputRunes != 2000 || n != 2000 {
		t.Errorf("long command = %d runes, want 2000", n)
	}
}
