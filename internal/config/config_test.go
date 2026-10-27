package config

import (
	"path/filepath"
	"testing"
)

func TestHome_ORBIT_HOME가_우선(t *testing.T) {
	t.Setenv("ORBIT_HOME", "/tmp/x-orbit")
	if Home() != "/tmp/x-orbit" {
		t.Fatal(Home())
	}
}

func TestLoad_파일이_없으면_기본값(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.RetroHost != "claude" || c.RetroHour != 4 || c.Inject != InjectFull || c.BackupKeep != 14 {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestSave_다시_읽으면_같다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.RetroHost, c.RetroModel, c.Inject = "codex", "gpt-x", InjectRules
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.RetroHost != "codex" || got.RetroModel != "gpt-x" || got.Inject != InjectRules {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestValidate_잘못된_값은_거절(t *testing.T) {
	c := Default()
	c.RetroHost = "gemini"
	if c.Validate() == nil {
		t.Fatal("bad host accepted")
	}
	c = Default()
	c.RetroHour = 24
	if c.Validate() == nil {
		t.Fatal("bad hour accepted")
	}
}
