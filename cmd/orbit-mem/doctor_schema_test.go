package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

func schemaProblems(t *testing.T) (map[string]problem, memory.SchemaInfo) {
	t.Helper()
	out, errOut, code := run(t, "", "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor %d %s", code, errOut)
	}
	var rep struct {
		Problems []problem         `json:"problems"`
		Schema   memory.SchemaInfo `json:"schema"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m := map[string]problem{}
	for _, p := range rep.Problems {
		m[p.ID] = p
	}
	return m, rep.Schema
}

// oldShapeDB makes the DB at the home look like an older orbit-mem left it.
func oldShapeDB(t *testing.T) string {
	t.Helper()
	s := seed(t)
	if _, err := s.DB().Exec(`DROP TABLE audit_acks; DROP TABLE audit_flags; DELETE FROM meta WHERE key='schema.version'; PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	return paths().DB()
}

func dbSum(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func TestDoctor_DB가_실행_파일보다_새_판이면_문제를_보이고_DB를_바꾸지_않는다(t *testing.T) {
	newFakePC(t)
	s := seed(t)
	if _, err := s.DB().Exec(`UPDATE meta SET value='99' WHERE key='schema.version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	before := dbSum(t, paths().DB())
	probs, sc := schemaProblems(t)
	p, ok := probs["binary-older-than-db"]
	if !ok || p.Fix != "orbit-mem update" || !strings.Contains(p.Message, "99") {
		t.Fatalf("problems = %+v", probs)
	}
	if _, ok := probs["migration-needed"]; ok {
		t.Fatalf("migration-needed with a newer DB: %+v", probs)
	}
	if sc.State != memory.SchemaNewer || sc.DB != 99 || sc.Binary != memory.SchemaVersion {
		t.Fatalf("schema = %+v", sc)
	}
	if dbSum(t, paths().DB()) != before {
		t.Fatal("doctor changed the DB")
	}
}

func TestDoctor_옛_꼴_DB는_이전_필요를_보이고_DB를_바꾸지_않는다(t *testing.T) {
	newFakePC(t)
	path := oldShapeDB(t)
	before := dbSum(t, path)
	probs, sc := schemaProblems(t)
	if p, ok := probs["migration-needed"]; !ok || p.Fix != "orbit-mem migrate" {
		t.Fatalf("problems = %+v", probs)
	}
	if _, ok := probs["binary-older-than-db"]; ok {
		t.Fatal("binary-older-than-db with an older DB")
	}
	if sc.State != memory.SchemaOlder {
		t.Fatalf("schema = %+v", sc)
	}
	if dbSum(t, path) != before {
		t.Fatal("doctor changed the DB")
	}
	if _, err := os.Stat(paths().Backups()); err == nil {
		es, _ := os.ReadDir(paths().Backups())
		if len(es) != 0 {
			t.Fatalf("doctor left copies: %v", es)
		}
	}
	s, info, err := memory.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var uv int
	s.DB().QueryRow(`PRAGMA user_version`).Scan(&uv)
	if uv != 3 || info.State != memory.SchemaOlder {
		t.Fatalf("user_version %d state %s", uv, info.State)
	}
}

func TestDoctor_정상_DB는_판_문제가_없고_schema가_ok이다(t *testing.T) {
	newFakePC(t)
	seed(t).Close()
	probs, sc := schemaProblems(t)
	for _, id := range []string{"binary-older-than-db", "migration-needed"} {
		if _, ok := probs[id]; ok {
			t.Fatalf("%s on a fine DB", id)
		}
	}
	if sc.State != memory.SchemaOK || sc.DB != memory.SchemaVersion || sc.Binary != memory.SchemaVersion {
		t.Fatalf("schema = %+v", sc)
	}
}

func TestMigrate_인자_없이_부르면_사본을_남기고_스키마를_이전한다(t *testing.T) {
	newFakePC(t)
	path := oldShapeDB(t)
	out, errOut, code := run(t, "", "migrate")
	if code != 0 {
		t.Fatalf("migrate %d %s", code, errOut)
	}
	if out == "" {
		t.Fatal("migrate said nothing")
	}
	es, _ := os.ReadDir(paths().Backups())
	if len(es) == 0 {
		t.Fatal("no copy in backups/")
	}
	s, info, err := memory.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if info.State != memory.SchemaOK {
		t.Fatalf("after migrate: %+v", info)
	}
	// 이미 맞는 판이면 다시 불러도 괜찮다.
	if _, errOut, code := run(t, "", "migrate"); code != 0 {
		t.Fatalf("second migrate %d %s", code, errOut)
	}
}

func TestMigrate_인자_없이_새_판_DB면_이전하지_않고_오류로_끝난다(t *testing.T) {
	newFakePC(t)
	s := seed(t)
	s.DB().Exec(`UPDATE meta SET value='99' WHERE key='schema.version'`)
	s.Close()
	before := dbSum(t, paths().DB())
	_, errOut, code := run(t, "", "migrate")
	if code != 1 || errOut == "" {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if dbSum(t, paths().DB()) != before {
		t.Fatal("migrate changed a newer DB")
	}
	if es, _ := os.ReadDir(filepath.Join(paths().Home, "backups")); len(es) != 0 {
		t.Fatalf("copies: %v", es)
	}
}

func TestMigrate_from_없이_다른_플래그만_주면_이전_대신_사용법이다(t *testing.T) {
	newFakePC(t)
	seed(t).Close()
	if _, _, code := run(t, "", "migrate", "--dry-run"); code != 2 {
		t.Fatalf("code %d", code)
	}
}
