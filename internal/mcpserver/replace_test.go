package mcpserver

import (
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// 검증 출처의 재확인(명세 C-1·E-4): memory_save는 검증 출처의 근거가 절대경로 파일이면 서버가 그 파일을 직접 읽어 발췌가
// 있는지 본다. 있으면 재확인한 근거라 같은 범위·주제의 사용자 말 사실을 스스로 대체하고 바로 확정된다. 읽은 파일의 다른
// 내용은 어디에도 남지 않는다.

const replaceApplies = "repo:github.com/me/app"

func replaceServer(t *testing.T, o Options) (*memory.Store, func(args map[string]any) SaveOut) {
	t.Helper()
	o.Project, o.ProjectLabel, o.Version, o.Git = "project:github.com/me/app", "github.com/me/app", "test", true
	cs, s := connectWith(t, o)
	save := func(args map[string]any) SaveOut {
		t.Helper()
		var out SaveOut
		res := call(t, cs, "memory_save", args, &out)
		if res.IsError || !out.Saved {
			t.Fatalf("save %v: %s", args, text(res))
		}
		return out
	}
	return s, save
}

func userFactAt(t *testing.T, s *memory.Store, title, topic string) memory.Memory {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Topic: topic, Scope: "project:github.com/me/app",
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func verifiedArgs(title, topic, ref, excerpt string) map[string]any {
	return map[string]any{"kind": "fact", "title": title, "topic": topic, "appliesTo": replaceApplies,
		"source": map[string]any{"kind": "verification", "ref": ref, "excerpt": excerpt}}
}

func TestSave_서버가_파일을_읽어_확인한_검증_출처만_사용자_말_사실을_자동_대체한다(t *testing.T) {
	s, save := replaceServer(t, Options{})
	dir := t.TempDir()
	file := filepath.Join(dir, "Makefile")
	const marker = "다른줄표식-zq81"
	if err := os.WriteFile(file, []byte("# "+marker+"\nbuild:\n\tgo build ./cmd/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 파일에 없는 발췌·상대경로는 재확인이 아니다: 자동 모드 끔이면 어긋남만 남는다.
	old := userFactAt(t, s, "빌드는 make all", "빌드 명령")
	for _, args := range []map[string]any{
		verifiedArgs("빌드는 go build ./...", "빌드 명령", file, "build: go build ./..."),
		verifiedArgs("빌드는 go build ./cmd/app", "빌드 명령", "Makefile", "build: go build ./cmd/app"),
	} {
		out := save(args) // 앞 저장의 새 사실(AI 후보)은 지금처럼 대체한다. 사용자 말 사실만 남긴다
		if got, _ := s.Get(old.ID); !slices.Contains(out.Conflicts, old.ID) || slices.Contains(out.Superseded, old.ID) || got.State != memory.StateConfirmed {
			t.Fatalf("%v: %+v", args["source"], out.MemoryOut)
		}
	}
	out := save(verifiedArgs("빌드 대상은 ./cmd/app", "빌드 명령", file, "build:\n  go build ./cmd/app"))
	if !slices.Contains(out.Superseded, old.ID) {
		t.Fatalf("not replaced: %+v", out.MemoryOut)
	}
	if got, _ := s.Get(old.ID); got.State != memory.StateStale || got.SupersededBy != out.Memory.ID {
		t.Fatalf("old %+v", got)
	}
	if out.Memory.State != memory.StateConfirmed {
		t.Fatalf("new state %s", out.Memory.State)
	}
	var rechecked int
	var excerpt string
	if err := s.DB().QueryRow(`SELECT rechecked, excerpt FROM sources WHERE memory_id=?`, out.Memory.ID).Scan(&rechecked, &excerpt); err != nil ||
		rechecked != 1 || excerpt != "build:\n  go build ./cmd/app" {
		t.Fatalf("source rechecked %d excerpt %q %v", rechecked, excerpt, err)
	}
	// 읽은 파일의 다른 줄은 기억·출처·이력·사건·원장 어디에도 없다.
	for _, q := range []string{`SELECT COUNT(*) FROM sources WHERE excerpt LIKE ? OR ref LIKE ?`, `SELECT COUNT(*) FROM memories WHERE body LIKE ? OR title LIKE ?`,
		`SELECT COUNT(*) FROM memory_versions WHERE snapshot LIKE ? OR reason LIKE ?`, `SELECT COUNT(*) FROM events WHERE data LIKE ? OR kind LIKE ?`,
		`SELECT COUNT(*) FROM ledger WHERE change LIKE ? OR basis LIKE ?`, `SELECT COUNT(*) FROM proposals WHERE body LIKE ? OR title LIKE ?`} {
		var n int
		if err := s.DB().QueryRow(q, "%"+marker+"%", "%"+marker+"%").Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", q, n, err)
		}
	}
}

func TestSave_설정을_다시_읽는_서버는_자동_모드_변경을_다음_저장부터_따른다(t *testing.T) {
	var on atomic.Bool
	s, save := replaceServer(t, Options{LoadPolicy: func() memory.Policy { return memory.Policy{AutoMode: on.Load()} }})
	s.Policy.AutoMode = true // 서버가 뜰 때 읽은 값보다 다시 읽은 값이 앞선다
	old := userFactAt(t, s, "시험은 make test", "시험 명령")
	if out := save(verifiedArgs("시험은 go test ./...", "시험 명령", "go test ./...", "ok  github.com/me/app  0.3s")); !slices.Contains(out.Conflicts, old.ID) {
		t.Fatalf("끔: %+v", out.MemoryOut)
	}
	on.Store(true)
	out := save(verifiedArgs("시험은 go test -race ./...", "시험 명령", "go test -race ./...", "ok  github.com/me/app  1.2s"))
	if !slices.Contains(out.Superseded, old.ID) || out.Memory.State != memory.StateCandidate {
		t.Fatalf("켬: %+v", out.MemoryOut)
	}
}
