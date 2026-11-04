package migrate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/memory"
)

type rec map[string]any

func legacyDB(t *testing.T, records []rec, observations []rec) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "knowledge-auto.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE records (id TEXT PRIMARY KEY, space TEXT NOT NULL, body TEXT NOT NULL)`,
		`CREATE TABLE observations (id TEXT PRIMARY KEY, body TEXT NOT NULL)`,
		`CREATE TABLE history (id TEXT, version INTEGER, body TEXT)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range records {
		b, _ := json.Marshal(r)
		db.Exec(`INSERT INTO records VALUES(?,?,?)`, r["id"], "personal", string(b))
	}
	for _, o := range observations {
		b, _ := json.Marshal(o)
		db.Exec(`INSERT INTO observations VALUES(?,?)`, o["id"], string(b))
	}
	return path
}

func record(id, kind, status, lifecycle, title string, sources []rec, workIDs, pageIDs []string, tags []string) rec {
	if sources == nil {
		sources = []rec{}
	}
	if workIDs == nil {
		workIDs = []string{}
	}
	if pageIDs == nil {
		pageIDs = []string{}
	}
	if tags == nil {
		tags = []string{}
	}
	return rec{"id": id, "kind": kind, "status": status, "lifecycle": lifecycle, "title": title, "body": "본문 " + title, "tags": tags,
		"sources": sources, "links": rec{"workIds": workIDs, "pageIds": pageIDs}, "version": 2, "spaceId": "personal",
		"created": rec{"at": 1792205709405}, "updated": rec{"at": 1792210040156}}
}

func ref(id, kind string) rec { return rec{"sourceId": id, "kind": kind, "provenance": "CLIENT_CLAIM"} }

func obs(id, kind string, locator rec, excerpt string) rec {
	return rec{"id": id, "kind": kind, "locator": locator, "excerpt": excerpt}
}

func fileHash(t *testing.T, p string) string {
	b, _ := os.ReadFile(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestMigrate_상태와_종류를_설계표대로_옮긴다(t *testing.T) {
	dir := t.TempDir()
	same := filepath.Join(dir, "memory", "same.md")
	changed := filepath.Join(dir, "memory", "changed.md")
	hashed := filepath.Join(dir, "hashed.md")
	os.MkdirAll(filepath.Dir(same), 0o755)
	os.WriteFile(same, []byte("그대로"), 0o644)
	os.WriteFile(changed, []byte("바뀜"), 0o644)
	os.WriteFile(hashed, []byte("내용"), 0o644)
	old := time.Date(2026, 10, 10, 0, 0, 0, 0, time.Local)
	os.Chtimes(same, old, old)
	path := legacyDB(t, []rec{
		record("a", "FACT", "VERIFIED", "ACTIVE", "검증 사실", []rec{ref("o-user", "USER")}, nil, nil, nil),
		record("b", "DECISION", "ASSUMED", "ACTIVE", "추정 결정", nil, []string{"w1", "w2"}, []string{"p1"}, nil),
		record("c", "CORRECTION", "ASSUMED", "ACTIVE", "교정 규칙", nil, nil, nil, nil),
		record("d", "ISSUE", "VERIFIED", "ACTIVE", "옛 이슈", nil, nil, nil, []string{"t"}),
		record("e", "NOTE", "UNVERIFIED", "ACTIVE", "옛 노트", nil, nil, nil, nil),
		record("f", "LESSON", "VERIFIED", "RETIRED", "은퇴한 교훈", nil, nil, nil, nil),
		record("g", "LESSON", "VERIFIED", "ACTIVE", "파일 근거 그대로", []rec{ref("o-same", "FILE")}, nil, nil, nil),
		record("h", "LESSON", "VERIFIED", "ACTIVE", "파일 근거 바뀜", []rec{ref("o-changed", "FILE"), ref("o-cmd", "COMMAND")}, nil, nil, nil),
		record("i", "FACT", "VERIFIED", "ACTIVE", "해시 근거", []rec{ref("o-hash", "FILE"), {"kind": "PROVIDER_DOCUMENT", "itemId": "pg", "revision": "r1"}}, nil, nil, nil),
	}, []rec{
		obs("o-user", "USER", rec{"sessionId": "s", "turnId": "t"}, "사용자가 한 말"),
		obs("o-same", "FILE", rec{"path": same, "version": "2026-10-19"}, "그대로"),
		obs("o-changed", "FILE", rec{"path": changed, "version": "2026-10-19"}, "옛 내용"),
		obs("o-hash", "FILE", rec{"path": hashed, "version": "sha256:" + fileHash(t, hashed)}, "내용"),
		obs("o-cmd", "COMMAND", rec{"runId": "qa-1"}, "시험 통과"),
	})
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rep, err := Run(s, Options{From: path, HostMemoryDirs: []string{filepath.Join(dir, "memory")}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Records != 9 || rep.Imported != 9 {
		t.Fatalf("report = %+v", rep)
	}
	all, _ := s.List()
	got := map[string]memory.Memory{}
	for _, m := range all {
		full, _ := s.Get(m.ID)
		got[m.LegacyID] = full
	}
	check := func(id, kind, state string) {
		t.Helper()
		if got[id].Kind != kind || got[id].State != state {
			t.Errorf("%s: kind=%s state=%s, want %s %s", id, got[id].Kind, got[id].State, kind, state)
		}
	}
	check("a", "fact", "confirmed")
	check("b", "decision", "candidate")
	check("c", "rule", "confirmed")
	check("d", "fact", "candidate")
	check("e", "fact", "candidate")
	check("f", "lesson", "stale")
	if !contains(got["d"].Tags, "옛:ISSUE") || !contains(got["e"].Tags, "옛:NOTE") || !contains(got["d"].Tags, "t") {
		t.Errorf("legacy kind tags: %v %v", got["d"].Tags, got["e"].Tags)
	}
	kinds := func(m memory.Memory) string {
		var k []string
		for _, s := range m.Sources {
			k = append(k, s.Kind+":"+s.Ref)
		}
		return strings.Join(k, ",")
	}
	quoted := false
	for _, src := range got["a"].Sources {
		quoted = quoted || (src.Kind == "quote" && src.Excerpt == "사용자가 한 말")
	}
	if !quoted {
		t.Errorf("user observation -> quote: %+v", got["a"].Sources)
	}
	if !strings.Contains(kinds(got["b"]), "legacy:work:w1") || !strings.Contains(kinds(got["b"]), "legacy:wiki:p1") {
		t.Errorf("work/page links -> sources: %s", kinds(got["b"]))
	}
	if !strings.Contains(kinds(got["h"]), "command:qa-1") || !strings.Contains(kinds(got["i"]), "legacy:wiki:pg@r1") {
		t.Errorf("command/provider: %s / %s", kinds(got["h"]), kinds(got["i"]))
	}
	for _, src := range got["g"].Sources {
		if src.Kind == "file" && src.Hash != fileHash(t, same) {
			t.Errorf("unchanged file must keep current hash: %+v", src)
		}
	}
	if got["a"].CreatedAt != 1792205709405 {
		t.Errorf("created time not kept: %d", got["a"].CreatedAt)
	}
	// 근거 파일이 바뀐 기억은 대기 제안 없이 출처 해시를 비워 두고, 하루 회고가 다시 판정한다(설계 · orbit-mem 2판 18.3).
	if pending, _ := s.Proposals(memory.StatusPending); len(pending) != 0 {
		t.Errorf("pending = %+v", pending)
	}
	if rep.ToJudge != 1 {
		t.Errorf("to judge = %d", rep.ToJudge)
	}
	for _, src := range got["h"].Sources {
		if src.Kind == "file" && src.Hash != "" {
			t.Errorf("changed file source must have no hash: %+v", src)
		}
	}
	if rep.FilesChanged != 1 || rep.FilesSame != 2 {
		t.Errorf("file report = %+v", rep)
	}
	var absorbed int
	s.DB().QueryRow(`SELECT COUNT(*) FROM absorbed WHERE path=?`, same).Scan(&absorbed)
	if absorbed != 1 {
		t.Errorf("unchanged host memory file must be marked absorbed")
	}

	again, err := Run(s, Options{From: path})
	if err != nil || again.Imported != 0 || again.Skipped != 9 {
		t.Fatalf("second run must skip all: %+v %v", again, err)
	}
}

func TestMigrate_미리보기는_쓰지_않는다(t *testing.T) {
	path := legacyDB(t, []rec{record("a", "FACT", "VERIFIED", "ACTIVE", "x", nil, nil, nil, nil)}, nil)
	s, _ := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	defer s.Close()
	rep, err := Run(s, Options{From: path, DryRun: true})
	if err != nil || rep.Imported != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if n, _ := s.Count(); n != 0 {
		t.Fatalf("dry run wrote %d", n)
	}
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
