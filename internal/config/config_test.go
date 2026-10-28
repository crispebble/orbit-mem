package config

import (
	"os"
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

func TestDefault_2차_설정_기본값(t *testing.T) {
	c := Default()
	if !c.SessionRetro || c.SessionRetroModel != "" || c.SessionRetroDailyMax != 0 || c.RecallChars != 1000 || c.LedgerWindowDays != 7 {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestLoad_옛_설정_파일에는_2차_기본값이_채워진다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"retroHost":"claude","retroHour":4,"inject":"full","recentDecisions":5,"backupKeep":14}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || !c.SessionRetro || c.RecallChars != 1000 || c.LedgerWindowDays != 7 {
		t.Fatalf("got %+v %v", c, err)
	}
}

func TestSessionModel_claude는_haiku_codex는_호스트_기본(t *testing.T) {
	c := Default()
	if c.SessionModel() != "haiku" {
		t.Fatalf("claude = %q", c.SessionModel())
	}
	c.RetroHost = "codex"
	if c.SessionModel() != "" {
		t.Fatalf("codex = %q", c.SessionModel())
	}
	c.SessionRetroModel = "sonnet"
	if c.SessionModel() != "sonnet" {
		t.Fatalf("set = %q", c.SessionModel())
	}
}

func TestValidate_2차_설정_범위(t *testing.T) {
	for _, f := range []func(*Config){
		func(c *Config) { c.RecallChars = 99 },
		func(c *Config) { c.RecallChars = 9001 },
		func(c *Config) { c.LedgerWindowDays = 0 },
		func(c *Config) { c.SessionRetroDailyMax = -1 },
	} {
		c := Default()
		f(&c)
		if c.Validate() == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}
