package main

import (
	"os"
	"strings"
	"testing"
)

// /dev/null도 문자 장치라 TTY로 보면 안 된다(orbit-mem search … </dev/null >/dev/null이 사람용 표를 내지 않게).
func TestIsTerminal_null_장치와_파이프는_TTY가_아니다(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Error("null 장치를 TTY로 봤습니다")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) || isTerminal(strings.NewReader("")) {
		t.Error("파이프·Reader를 TTY로 봤습니다")
	}
}
