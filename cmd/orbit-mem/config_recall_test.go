package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestConfigSet_recall과_inject는_두_키를_함께_맞춘다(t *testing.T) {
	home(t)
	read := func() map[string]any {
		b, err := os.ReadFile(paths().Config())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	if _, errOut, code := run(t, "", "config", "set", "recall", "on_demand"); code != 0 {
		t.Fatalf("code %d %s", code, errOut)
	}
	if m := read(); m["recall"] != "on_demand" || m["inject"] != "rules" {
		t.Fatalf("recall set: %v", m)
	}
	if _, errOut, code := run(t, "", "config", "set", "inject", "off"); code != 0 {
		t.Fatalf("code %d %s", code, errOut)
	}
	if m := read(); m["recall"] != "off" || m["inject"] != "off" {
		t.Fatalf("inject set: %v", m)
	}
	if _, _, code := run(t, "", "config", "set", "recall", "auto"); code != 0 {
		t.Fatal(code)
	}
	if m := read(); m["recall"] != "auto" || m["inject"] != "full" {
		t.Fatalf("auto: %v", m)
	}
	if _, _, code := run(t, "", "config", "set", "recall", "bogus"); code == 0 {
		t.Fatal("bogus recall accepted")
	}
}
