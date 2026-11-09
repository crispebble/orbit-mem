package bench_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLegacy는_없는_기준선을_깃발_없이는_실패로_본다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline-legacy.json")
	err := checkLegacy(path, []byte("now\n"), false)
	if err == nil || !strings.Contains(err.Error(), "-write-legacy-baseline") {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a missing baseline was written without the flag")
	}
}

func TestCheckLegacy는_깃발이_있으면_없는_기준선만_쓴다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline-legacy.json")
	if err := checkLegacy(path, []byte("first\n"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "first\n" {
		t.Fatalf("wrote %q", b)
	}
	if err := checkLegacy(path, []byte("second\n"), true); err == nil {
		t.Error("the flag rewrote a baseline that exists")
	}
	if b, _ := os.ReadFile(path); string(b) != "first\n" {
		t.Errorf("the baseline moved: %q", b)
	}
}

func TestCheckLegacy는_있는_기준선과_엄격히_비교한다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline-legacy.json")
	os.WriteFile(path, []byte("a\nb\n"), 0o644)
	if err := checkLegacy(path, []byte("a\nb\n"), false); err != nil {
		t.Errorf("same: %v", err)
	}
	if err := checkLegacy(path, []byte("a\nc\n"), false); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("differs: %v", err)
	}
}
