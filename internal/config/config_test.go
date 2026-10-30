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

func TestWork_작업_기록_설정(t *testing.T) {
	c := Default()
	if !c.WorkOn() || c.StaleAfter() != 0 {
		t.Fatalf("기본은 켜짐, 응답 없음 계산 안 함: %+v", c)
	}
	c.Work, c.WorkStaleAfter = WorkOff, "20m"
	if c.Validate() != nil || c.WorkOn() || c.StaleAfter().Minutes() != 20 {
		t.Fatalf("%+v", c)
	}
	for _, bad := range []Config{{Work: "maybe"}, {WorkStaleAfter: "soon"}, {WorkStaleAfter: "-1m"}} {
		b := Default()
		if bad.Work != "" {
			b.Work = bad.Work
		}
		b.WorkStaleAfter = bad.WorkStaleAfter
		if b.Validate() == nil {
			t.Errorf("잘못된 값을 받았습니다: %+v", bad)
		}
	}
	p := Paths{Home: "/h"}
	if p.WorkDB() != "/h/work.db" || p.WorkSpool() != "/h/work/spool" {
		t.Error(p.WorkDB(), p.WorkSpool())
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

// 기억 그래프 정리 기본값: 회당 클러스터 기억 40·태그 없는 기억 20을 보고 제안 20건까지(2026-10-30 사용자 결정).
func TestDefault_그래프_정리(t *testing.T) {
	c := Default()
	if c.GraphTidyMemories != 40 || c.GraphTidyUntagged != 20 || c.GraphTidyProposals != 20 {
		t.Fatalf("기본값: %+v", c)
	}
	c.GraphTidyProposals = 21
	if c.Validate() == nil {
		t.Error("정리 제안 상한은 회고 제출 상한(20)을 넘지 않는다")
	}
}

// 옛 기억 정리 기본값: 한 묶음 30개, 예약 회고는 하루 1묶음(2026-10-30 사용자 결정). 범위 밖 값은 거절한다.
func TestDefault_옛_기억_정리(t *testing.T) {
	c := Default()
	if c.LegacyMemories != 30 || c.LegacyBatches != 1 || len(c.RetiredNames) != 0 {
		t.Fatalf("기본값: %+v", c)
	}
	for _, bad := range []func(*Config){func(c *Config) { c.LegacyMemories = 0 }, func(c *Config) { c.LegacyMemories = 101 },
		func(c *Config) { c.LegacyBatches = -1 }, func(c *Config) { c.LegacyBatches = 21 }} {
		c := Default()
		bad(&c)
		if c.Validate() == nil {
			t.Errorf("받으면 안 됩니다: %+v", c)
		}
	}
}
