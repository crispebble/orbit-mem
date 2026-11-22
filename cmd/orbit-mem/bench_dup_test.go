package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

// extract가 쓴 cases.json은 bench recall이 읽는 사례 파일이다: 같은 세션 id의 기록 파일이 여럿이어도 사례 id가 겹치지 않고,
// --projects를 여러 번 줄 수 있다. 이어받은 파일의 꼬리는 새 세션이다.
func TestBench_extract는_같은_세션_id가_여럿이어도_읽히는_사례_파일을_쓴다(t *testing.T) {
	home(t)
	db, s := openedDB(t)
	s.Close()
	rec := func(sid, uuid, at, text string) string {
		return fmt.Sprintf(`{"type":"user","uuid":%q,"sessionId":%q,"cwd":"/work/demo","timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":%q}}`+"\n", uuid, sid, at, text)
	}
	base := rec("dup", "u1", "2026-11-01T10:00:00Z", "하나") + rec("dup", "u2", "2026-11-01T10:01:00Z", "둘")
	r1, r2 := t.TempDir(), t.TempDir()
	for path, body := range map[string]string{
		filepath.Join(r1, "pa", "dup.jsonl"):   base + rec("dup", "a1", "2026-11-01T10:05:00Z", "가지A"),
		filepath.Join(r1, "pa", "child.jsonl"): base + rec("child", "b1", "2026-11-01T10:03:00Z", "가지B"),
		filepath.Join(r2, "pb", "dup.jsonl"):   base,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "replay")
	stdout, errb, code := run(t, "", "bench", "extract", "--projects", r1, "--projects", r2, "--db", db, "--out", out, "--all")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	set, err := bench.Read(filepath.Join(out, "cases.json"))
	if err != nil {
		t.Fatalf("cases.json을 읽지 못한다: %v", err)
	}
	if len(set.Cases) != 4 {
		t.Fatalf("사례 %d개, want 4(dup: 하나·둘·가지A, child: 가지B)", len(set.Cases))
	}
	// 합친 세션(앞부분 사본으로 합친 dup 하나)과 이어받은 세션(child 하나)의 수가 출력에 나온다.
	if !strings.Contains(stdout, "merged sessions 1") || !strings.Contains(stdout, "taken-over sessions 1") {
		t.Errorf("합친·이어받은 세션 수가 출력에 없다: %s", stdout)
	}
	b, _ := os.ReadFile(filepath.Join(out, "sessions.json"))
	var ss []struct{ ID, Merge, Parent string }
	if err := json.Unmarshal(b, &ss); err != nil {
		t.Fatal(err)
	}
	for _, x := range ss {
		if (x.ID == "dup" && x.Merge != "copy") || (x.ID == "child" && x.Parent != "dup") {
			t.Errorf("sessions.json 표시 %+v", x)
		}
	}
}
