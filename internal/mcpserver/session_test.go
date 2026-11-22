package mcpserver

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// sessionsOf is the sessions memory id came up again in, from the table the confirmation counts (E-1).
func sessionsOf(t *testing.T, s *memory.Store, id string) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT DISTINCT session FROM sighting_sessions WHERE memory_id=? ORDER BY session`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

const sessionSaveInput = `{"kind":"lesson","title":"codex exec는 stdin을 닫는다","body":"입력을 파이프로 주지 않으면 바로 끝난다","appliesTo":"user","source":{"kind":"session","ref":"s"}}`

// 호스트가 CLI로 부르는 memory_save(host call)는 요청의 세션 키로 다시 나온 기록을 남긴다. 하위 에이전트의 키
// 「세션/agent」는 앞 세션으로 접힌다: 같은 세션의 하위 에이전트 둘이 날을 넘겨 저장해도 확정되지 않고,
// 다른 세션이 다른 날 저장하면 확정된다.
func TestCall_memory_save는_요청_세션을_다시_나온_기록에_남긴다(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return now }
	tools := NewTools(s, Options{Project: "project:a", Actor: memory.ActorAI})
	save := func(session string) SaveOut {
		t.Helper()
		out, err := tools.Call("memory_save", []byte(sessionSaveInput), "claude", session)
		if err != nil {
			t.Fatal(err)
		}
		r, ok := out.(SaveOut)
		if !ok || !r.Saved {
			t.Fatalf("결과 = %#v", out)
		}
		return r
	}
	first := save("sess-1/agent-a")
	now = now.Add(24 * time.Hour)
	if r := save("sess-1/agent-b"); r.Memory.State != memory.StateCandidate {
		t.Fatalf("같은 세션의 하위 에이전트 저장으로 확정됐습니다: %s", r.Memory.State)
	}
	if got := sessionsOf(t, s, first.Memory.ID); !slices.Equal(got, []string{"sess-1"}) {
		t.Fatalf("세션 = %v", got)
	}
	now = now.Add(24 * time.Hour)
	if r := save("sess-2"); r.Memory.State != memory.StateConfirmed {
		t.Fatalf("다른 세션·다른 날인데 확정되지 않았습니다: %s", r.Memory.State)
	}
	if got := sessionsOf(t, s, first.Memory.ID); !slices.Equal(got, []string{"sess-1", "sess-2"}) {
		t.Fatalf("세션 = %v", got)
	}
}

// MCP 서버로 온 memory_save는 서버 조립이 정한 세션(Options.Session)으로 다시 나온 기록을 남긴다.
func TestMemorySave_서버_세션을_다시_나온_기록에_남긴다(t *testing.T) {
	cs, s := connectWith(t, Options{Project: "project:a", ProjectLabel: "a", Version: "t", Actor: memory.ActorAI, Session: "run-0a1b"})
	var out MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "codex exec는 stdin을 닫는다", "body": "입력을 파이프로 주지 않으면 바로 끝난다",
		"appliesTo": "user", "source": map[string]any{"kind": "session", "ref": "s"}}, &out)
	if got := sessionsOf(t, s, out.Memory.ID); !slices.Equal(got, []string{"run-0a1b"}) {
		t.Fatalf("세션 = %v", got)
	}
}
