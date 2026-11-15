package migrate

import (
	"path/filepath"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestMigrate_지운_legacy_id는_다시_들여오지_않는다(t *testing.T) {
	path := legacyDB(t, []rec{record("a", "FACT", "VERIFIED", "ACTIVE", "지울 옛 기억", nil, nil, nil, nil)}, nil)
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := Run(s, Options{From: path})
	if err != nil || first.Imported != 1 {
		t.Fatalf("first run: %+v %v", first, err)
	}
	var id string
	if err := s.DB().QueryRow(`SELECT id FROM memories WHERE legacy_id='a'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(memory.ForgetInput{IDs: []string{id}, Who: memory.Who{Actor: memory.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	again, err := Run(s, Options{From: path})
	if err != nil || again.Imported != 0 || again.Skipped != 1 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	if n != 0 {
		t.Fatalf("memories = %d", n)
	}
}
