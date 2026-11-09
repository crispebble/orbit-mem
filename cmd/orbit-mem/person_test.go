package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// 사람 요청 판정(훅 단): Claude Code·Codex 둘 다 틀만 있는 요청에는 stdout 0바이트이고,
// 사람이 틀 글을 붙여 넣고 질문하면 회상을 낸다.
func TestHook_틀만_있는_요청은_빈_출력이고_붙여_넣고_질문하면_회상한다(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "inject", "testdata", "human_fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Persons []struct {
			Name   string `json:"name"`
			Prompt string `json:"prompt"`
			Person bool   `json:"person"`
		} `json:"persons"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	home(t)
	s := seed(t)
	if _, err := s.Save(memory.SaveInput{Kind: "decision", Scope: memory.ScopeGlobal, Title: "금요일 배포 금지", Body: "주말 장애", Source: memory.Source{Kind: "quote", Excerpt: "금요일 배포 금지"}}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, hostName := range []string{"claude", "codex"} {
		for _, c := range fx.Persons {
			if c.Person {
				continue
			}
			n++
			plain, _ := json.Marshal(map[string]string{"cwd": "/tmp", "session_id": "s-1", "prompt": c.Prompt})
			if out, _, code := run(t, string(plain), "hook", "prompt", "--host", hostName); code != 0 || out != "" {
				t.Fatalf("%s/%s: code %d out %q", hostName, c.Name, code, out)
			}
		}
		pasted, _ := json.Marshal(map[string]string{"cwd": "/tmp", "session_id": "s-1",
			"prompt": "<task-notification>\n<status>completed</status>\n</task-notification>\n\n금요일 배포 금지 맞나"})
		out, _, code := run(t, string(pasted), "hook", "prompt", "--host", hostName)
		if code != 0 || !strings.Contains(out, "금요일 배포 금지") {
			t.Fatalf("%s 붙여 넣고 질문 = %d %q", hostName, code, out)
		}
	}
	if n == 0 {
		t.Fatal("틀 글 사례가 없다")
	}
}
