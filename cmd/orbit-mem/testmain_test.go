package main

import (
	"os"
	"testing"
)

// TestMain keeps tests from starting real background processes: the session-end hook spawns
// selfPath() retro sessions, and in a test selfPath() is the test binary, which would run the
// whole suite again (and spawn again, without end). Tests that check the spawn replace
// spawnDetached themselves.
func TestMain(m *testing.M) {
	spawnDetached = func(string, []string, string) error { return nil }
	os.Exit(m.Run())
}
