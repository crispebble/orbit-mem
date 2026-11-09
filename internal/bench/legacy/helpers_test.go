package legacy_test

import (
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// saveLater saves a memory into the DB at db through another connection, an hour after the
// set's now: a memory confirmed elsewhere after a session started.
func saveLater(t *testing.T, db string, set *bench.Set, in memory.SaveInput) string {
	t.Helper()
	s, err := memory.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := set.At().Add(time.Hour)
	s.Now = func() time.Time { return at }
	m, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}
