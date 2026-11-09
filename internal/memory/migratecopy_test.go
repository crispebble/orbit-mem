package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestMigrateCopy는_옛_판_사본을_올리고_백업을_만들지_않는다(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.db")
	v4DB(t, path, "사본 이전 기억")
	before, err := MigrateCopy(path)
	if err != nil || before.State != SchemaOlder || before.DB != 4 {
		t.Fatalf("before = %+v %v", before, err)
	}
	s, info, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info.State != SchemaOK {
		t.Errorf("after = %+v", info)
	}
	if got, err := s.Find(FindInput{Query: "사본 이전"}); err != nil || len(got) != 1 {
		t.Errorf("index not filled: %v %v", got, err)
	}
	if _, err := os.Stat(schemaBackupDir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a backup folder next to the copy: %v", err)
	}
	if again, err := MigrateCopy(path); err != nil || again.State != SchemaOK {
		t.Errorf("a second run = %+v %v", again, err)
	}
}

func TestMigrateCopy는_새_판_DB를_건드리지_않는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE meta SET value=? WHERE key='schema.version'`, strconv.Itoa(SchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := MigrateCopy(path); !errors.Is(err, ErrSchemaNewer) {
		t.Errorf("err = %v, want ErrSchemaNewer", err)
	}
	if _, info, _ := OpenReadOnly(path); info.DB != SchemaVersion+1 {
		t.Errorf("version = %d", info.DB)
	}
}

func TestMigrateCopy는_없는_파일을_만들지_않는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := MigrateCopy(path); err == nil {
		t.Error("no error for a missing file")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("made %s", path)
	}
}
