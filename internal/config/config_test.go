package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// 작업 기록 설정(work·workStaleAfter·workTestCommands)은 orbit-code가 읽는다. 값과 무관하게
// 실패 없이 읽고, 다시 저장해도 그 키는 그대로 남는다.
func TestLoad_작업_기록_키는_읽을_때_무시하고_저장할_때_보존한다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"retroHost":"codex","work":"bogus","workStaleAfter":"x","workTestCommands":["make t"]}`), 0o600)
	c, err := Load(p)
	if err != nil || c.RetroHost != "codex" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"work": "bogus"`) || !strings.Contains(string(b), "workStaleAfter") || !strings.Contains(string(b), "workTestCommands") {
		t.Fatalf("키가 사라졌습니다: %s", b)
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

func TestLanguage_기본은_영어이고_en_ko만_받는다(t *testing.T) {
	if Default().Language != "en" {
		t.Fatalf("default %q", Default().Language)
	}
	for _, ok := range []string{"", "en", "ko", "KO", "ko-KR"} {
		c := Default()
		c.Language = ok
		if err := c.Validate(); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"ja", "korean"} {
		c := Default()
		c.Language = bad
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "language is en or ko") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestLanguage_검증에_실패한_설정에서도_언어를_읽는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if Language(path) != "" {
		t.Fatal("no file")
	}
	os.WriteFile(path, []byte(`{"language":"ko","retroHour":99}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("retroHour 99 should fail")
	}
	if Language(path) != "ko" {
		t.Fatalf("%q", Language(path))
	}
}

func TestLanguage_손으로_쓴_언어도_읽고_맞춰_둔다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"language":"KO"}`), 0o600)
	c, err := Load(path)
	if err != nil || c.Language != "ko" {
		t.Fatalf("%+v %v", c.Language, err)
	}
}

func TestSave_모르는_키는_보존(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	raw := `{"language":"en","work":"off","workStaleAfter":"72h","workTestCommands":{"go":"go test ./..."},"claudePath":"/x/claude"}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Language, c.ClaudePath = "ko", ""
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["work"] != "off" || got["workStaleAfter"] != "72h" || got["workTestCommands"].(map[string]any)["go"] != "go test ./..." {
		t.Fatalf("모르는 키가 사라졌습니다: %s", b)
	}
	if got["language"] != "ko" {
		t.Fatalf("아는 키는 덮어씁니다: %s", b)
	}
	if _, ok := got["claudePath"]; ok {
		t.Fatalf("비운 아는 키는 지웁니다: %s", b)
	}
}

// 새 판 알림 설정(updateCheck): 기본은 켜짐이고, 옛 설정 파일(키 없음)도 켜짐으로 읽는다. 끈 값은 보존한다.
func TestUpdateCheck_기본은_켜짐이고_옛_설정도_켜짐이다(t *testing.T) {
	if !Default().UpdateCheck {
		t.Fatal("기본이 꺼짐")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"retroHost":"codex"}`), 0o600)
	c, err := Load(p)
	if err != nil || !c.UpdateCheck {
		t.Fatalf("옛 설정 %+v %v", c, err)
	}
	c.UpdateCheck = false
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.UpdateCheck {
		t.Fatalf("끈 값이 사라짐 %+v %v", got, err)
	}
	if !knownKeys()["updateCheck"] {
		t.Fatal("knownKeys에 updateCheck가 없음")
	}
}
