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
	if c.Inject != InjectFull || c.BackupKeep != 14 || !c.AutoBackup || !c.AutoTidy {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestSave_다시_읽으면_같다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.Inject, c.AutoTidy = InjectRules, false
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.Inject != InjectRules || got.AutoTidy {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestValidate_잘못된_값은_거절(t *testing.T) {
	c := Default()
	c.Inject = "some"
	if c.Validate() == nil {
		t.Fatal("bad inject accepted")
	}
}

// 작업 기록 설정(work·workStaleAfter·workTestCommands)은 orbit-code가 읽는다. 값과 무관하게
// 실패 없이 읽고, 다시 저장해도 그 키는 그대로 남는다.
func TestLoad_작업_기록_키는_읽을_때_무시하고_저장할_때_보존한다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"inject":"rules","work":"bogus","workStaleAfter":"x","workTestCommands":["make t"]}`), 0o600)
	c, err := Load(p)
	if err != nil || c.Inject != InjectRules {
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
	if c.RecallChars != 1000 || c.LedgerWindowDays != 7 || c.CandidateExpiryDays != 28 {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestLoad_옛_설정_파일에는_2차_기본값이_채워진다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"retroHost":"claude","retroHour":4,"inject":"full","recentDecisions":5,"backupKeep":14}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || !c.AutoTidy || c.RecallChars != 1000 || c.LedgerWindowDays != 7 {
		t.Fatalf("got %+v %v", c, err)
	}
}

func TestValidate_2차_설정_범위(t *testing.T) {
	for _, f := range []func(*Config){
		func(c *Config) { c.RecallChars = 99 },
		func(c *Config) { c.RecallChars = 9001 },
		func(c *Config) { c.LedgerWindowDays = 0 },
		func(c *Config) { c.LedgerWindowDays = 91 },
	} {
		c := Default()
		f(&c)
		if c.Validate() == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

// 기획 · orbit-mem R5: 회고 설정 키(retroHost·retroHour·sessionRetro·graphTidy*·legacy*·claudePath·applyTargets·
// pluginSource·retiredNames…)는 걷어냈다. 옛 config.json에 남은 키는 값이 무엇이든(범위 밖이어도) 읽기를 막지 않고,
// 다시 저장해도 파일에 그대로 남는다(orbit-code 하네스가 pluginSource를 읽을 수 있다).
func TestLoad_옛_회고_키는_무시하고_저장할_때_보존한다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	raw := `{"language":"en","retroHost":"gemini","retroModel":"x","retroHour":99,"sessionRetro":true,"sessionRetroModel":"haiku",
"sessionRetroDailyMax":-1,"graphTidyMemories":999,"graphTidyUntagged":20,"graphTidyProposals":50,"legacyMemories":0,"legacyBatches":99,
"claudePath":"/x/claude","codexPath":"/x/codex","applyTargets":["/a"],"pluginSource":"/p/source","retiredNames":["old"]}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("옛 키가 읽기를 막았다: %v", err)
	}
	c.Inject = InjectRules
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"retroHost", "retroHour", "sessionRetro", "graphTidyProposals", "legacyBatches", "claudePath", "applyTargets", "pluginSource", "retiredNames"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s가 사라졌다: %s", k, b)
		}
	}
	if got["inject"] != InjectRules || got["pluginSource"] != "/p/source" {
		t.Fatalf("저장 = %s", b)
	}
	if knownKeys()["retroHost"] || knownKeys()["pluginSource"] {
		t.Fatal("회고 키가 아직 아는 키다")
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
	os.WriteFile(path, []byte(`{"language":"ko","recallChars":1}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("recallChars 1 should fail")
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
	raw := `{"language":"en","work":"off","workStaleAfter":"72h","workTestCommands":{"go":"go test ./..."},"hostMemory":"keep"}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Language, c.HostMemory = "ko", ""
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
	if _, ok := got["hostMemory"]; ok {
		t.Fatalf("비운 아는 키는 지웁니다: %s", b)
	}
}

// 새 판 알림 설정(updateCheck): 기본은 켜짐이고, 옛 설정 파일(키 없음)도 켜짐으로 읽는다. 끈 값은 보존한다.
func TestUpdateCheck_기본은_켜짐이고_옛_설정도_켜짐이다(t *testing.T) {
	if !Default().UpdateCheck {
		t.Fatal("기본이 꺼짐")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"inject":"rules"}`), 0o600)
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
