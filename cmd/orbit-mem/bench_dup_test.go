package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
)

// extract가 쓴 cases.json은 bench recall이 읽는 사례 파일이다: 같은 세션 id의 기록 파일이 여럿이어도 사례 id가 겹치지 않고,
// --projects를 여러 번 줄 수 있다.
func TestBench_extract는_같은_세션_id가_여럿이어도_읽히는_사례_파일을_쓴다(t *testing.T) {
	home(t)
	db, s := openedDB(t)
	s.Close()
	rec := func(uuid, at, text string) string {
		return fmt.Sprintf(`{"type":"user","uuid":%q,"sessionId":"dup","cwd":"/work/demo","timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":%q}}`+"\n", uuid, at, text)
	}
	base := rec("u1", "2026-11-01T10:00:00Z", "하나") + rec("u2", "2026-11-01T10:01:00Z", "둘")
	r1, r2 := t.TempDir(), t.TempDir()
	for path, body := range map[string]string{
		filepath.Join(r1, "pa", "dup.jsonl"):   base + rec("a1", "2026-11-01T10:05:00Z", "가지A"),
		filepath.Join(r1, "pa", "dup-2.jsonl"): base + rec("b1", "2026-11-01T10:03:00Z", "가지B"),
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
	if _, errb, code := run(t, "", "bench", "extract", "--projects", r1, "--projects", r2, "--db", db, "--out", out, "--all"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	set, err := bench.Read(filepath.Join(out, "cases.json"))
	if err != nil {
		t.Fatalf("cases.json을 읽지 못한다: %v", err)
	}
	if len(set.Cases) != 4 {
		t.Fatalf("사례 %d개, want 4(하나·둘·가지B·가지A)", len(set.Cases))
	}
}
