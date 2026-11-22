package claudecode

import "testing"

func TestMCPSession_환경_변수가_없으면_빈_값을_돌려준다(t *testing.T) {
	for _, c := range []struct {
		name string
		vars map[string]string
		want string
	}{
		{"없음", nil, ""},
		{"빈 값", map[string]string{SessionEnv: "  "}, ""},
		{"다른 이름만 있음", map[string]string{"CLAUDE_SESSION_ID": "s-1"}, ""},
		{"있음", map[string]string{SessionEnv: " 0f3c-s1 "}, "0f3c-s1"},
	} {
		vars := c.vars
		if got := MCPSession(func(k string) string { return vars[k] }); got != c.want {
			t.Fatalf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if SessionEnv != "CLAUDE_CODE_SESSION_ID" {
		t.Fatalf("SessionEnv = %q", SessionEnv)
	}
}
