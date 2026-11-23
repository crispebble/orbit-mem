package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// seedDeadLink makes a live memory linked to a stale one.
func seedDeadLink(t *testing.T) {
	t.Helper()
	s := seed(t)
	save := func(title string) memory.Memory {
		m, err := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: title, Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession, Ref: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	live, dead := save("산 기억"), save("낡은 기억")
	if _, _, err := s.Link(memory.LinkInput{From: live.ID, To: dead.ID, Origin: memory.LinkOriginUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(dead.ID, memory.StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
}

// F-6: 기본 쓰임인 자동 정리의 보고(tidy.log 한 줄)에 죽은 연결 수가 나온다.
func TestTidy_auto는_로그에_죽은_연결_수를_쓴다(t *testing.T) {
	h := home(t)
	seedDeadLink(t)
	if _, errOut, code := run(t, "", "tidy", "auto"); code != 0 {
		t.Fatalf("code %d %s", code, errOut)
	}
	log, _ := os.ReadFile(filepath.Join(h, "logs", "tidy.log"))
	if !strings.Contains(string(log), "deadLinks 1") {
		t.Fatalf("tidy.log = %q", log)
	}
}

// F-6: 사람이 읽는 정리 보고는 죽은 연결이 0이면 그 줄을 내지 않고, 0보다 크면 낸다.
func TestTidy_죽은_연결_줄은_0이면_없고_있으면_나온다(t *testing.T) {
	home(t)
	seed(t)
	old := outputIsTerminal
	outputIsTerminal = func(io.Writer) bool { return true } // 사람이 읽는 글로 본다
	t.Cleanup(func() { outputIsTerminal = old })
	out, _, code := run(t, "", "tidy")
	if code != 0 || strings.Contains(out, "stale or hidden") {
		t.Fatalf("0일 때: code %d out %q", code, out)
	}
	seedDeadLink(t)
	out, _, code = run(t, "", "tidy")
	if code != 0 || !strings.Contains(out, "stale or hidden one: 1") {
		t.Fatalf("1일 때: code %d out %q", code, out)
	}
}
