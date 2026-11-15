package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/backup"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func forgetSeed(t *testing.T, s *memory.Store, title string) memory.Memory {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: "fact", Title: title, Source: memory.Source{Kind: "quote", Excerpt: title}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestForgetCmd_터미널이_아니고_yes가_없으면_지우지_않고_종료_1(t *testing.T) {
	home(t)
	s := seed(t)
	m := forgetSeed(t, s, "지울 기억")
	_, errOut, code := run(t, "", "forget", m.ID)
	if code != 1 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if _, err := s.Get(m.ID); err != nil {
		t.Fatalf("지워졌다: %v", err)
	}
}

func TestForgetCmd_yes면_지우고_id를_보인다(t *testing.T) {
	home(t)
	s := seed(t)
	m := forgetSeed(t, s, "지울 기억")
	keep := forgetSeed(t, s, "남길 기억")
	out, errOut, code := run(t, "", "forget", m.ID, "--yes", "--reason", "잘못 적었다")
	if code != 0 || !strings.Contains(out, m.ID) {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
	if _, err := s.Get(m.ID); err == nil {
		t.Fatal("지우지 않았다")
	}
	if _, err := s.Get(keep.ID); err != nil {
		t.Fatalf("다른 기억이 지워졌다: %v", err)
	}
	if ok, err := s.IsForgotten(m.ID, ""); err != nil || !ok {
		t.Fatalf("forgotten에 없다: %v %v", ok, err)
	}
	var got struct {
		Forgotten []string `json:"forgotten"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Forgotten) != 1 || got.Forgotten[0] != m.ID {
		t.Fatalf("JSON %q: %v %+v", out, err, got)
	}
}

func TestForgetCmd_없는_id가_있으면_아무것도_지우지_않고_알린다(t *testing.T) {
	home(t)
	s := seed(t)
	m := forgetSeed(t, s, "지울 기억")
	_, errOut, code := run(t, "", "forget", m.ID, "m-없는id", "--yes")
	if code != 1 || !strings.Contains(errOut, "m-없는id") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if _, err := s.Get(m.ID); err != nil {
		t.Fatalf("일부만 지워졌다: %v", err)
	}
}

func TestForgetCmd_ORBIT_ACTOR가_ai면_quote_없이는_yes라도_거절한다(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_ACTOR", "ai")
	s := seed(t)
	m := forgetSeed(t, s, "지울 기억")
	_, errOut, code := run(t, "", "forget", m.ID, "--yes")
	if code == 0 || errOut == "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if _, err := s.Get(m.ID); err != nil {
		t.Fatalf("quote 없이 지워졌다: %v", err)
	}
	if _, errOut, code = run(t, "", "forget", m.ID, "--yes", "--quote", "지워"); code != 0 {
		t.Fatalf("quote가 있는데 거절: %d %q", code, errOut)
	}
	if _, err := s.Get(m.ID); err == nil {
		t.Fatal("quote를 줬는데 지우지 않았다")
	}
}

func TestForgetCmd_지우기_전에_id와_제목을_보인다(t *testing.T) {
	home(t)
	old := outputIsTerminal
	outputIsTerminal = func(io.Writer) bool { return true } // 사람이 읽는 글로 본다
	t.Cleanup(func() { outputIsTerminal = old })
	s := seed(t)
	m := forgetSeed(t, s, "미리 보일 제목")
	out, _, code := run(t, "", "forget", m.ID) // --yes 없이: 보이기만 하고 지우지 않는다
	if code != 1 || !strings.Contains(out, m.ID) || !strings.Contains(out, "미리 보일 제목") {
		t.Fatalf("code %d, out %q", code, out)
	}
	if _, err := s.Get(m.ID); err != nil {
		t.Fatalf("지워졌다: %v", err)
	}
}

func TestBackupRestore_지운_기억이_사본에_있으면_돌아온다고_알린다(t *testing.T) {
	h := home(t)
	s := seed(t)
	m := forgetSeed(t, s, "사본에 있던 기억")
	forgetSeed(t, s, "그대로인 기억")
	if _, _, code := run(t, "", "backup"); code != 0 {
		t.Fatal(code)
	}
	cp, _ := backup.Last(filepath.Join(h, "backups"), backup.PrefixMemory)
	if _, _, code := run(t, "", "forget", m.ID, "--yes"); code != 0 {
		t.Fatal(code)
	}
	out, _, _ := run(t, "", "backup", "restore", cp.Name) // --yes 없이: 미리 보기만
	if !strings.Contains(out, "forgotten memories (1)") {
		t.Fatalf("복구 미리 보기에 지운 기억 안내가 없다: %q", out)
	}
	n, err := s.ForgottenInCopy(cp.Path)
	if err != nil || n != 1 {
		t.Fatalf("ForgottenInCopy = %d %v", n, err)
	}
}

func TestBackupRestore_지운_기억이_없으면_안내하지_않는다(t *testing.T) {
	h := home(t)
	s := seed(t)
	forgetSeed(t, s, "a")
	if _, _, code := run(t, "", "backup"); code != 0 {
		t.Fatal(code)
	}
	cp, _ := backup.Last(filepath.Join(h, "backups"), backup.PrefixMemory)
	out, _, _ := run(t, "", "backup", "restore", cp.Name)
	if strings.Contains(out, "forgotten memories") {
		t.Fatalf("안내가 나왔다: %q", out)
	}
}
