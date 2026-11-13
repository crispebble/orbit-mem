package config

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
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
	if c.RecallChars != MaxPromptRecallChars || c.LedgerWindowDays != 7 || c.CandidateExpiryDays != 28 {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestLoad_옛_설정_파일에는_2차_기본값이_채워진다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"retroHost":"claude","retroHour":4,"inject":"full","recentDecisions":5,"backupKeep":14}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || !c.AutoTidy || c.RecallChars != MaxPromptRecallChars || c.LedgerWindowDays != 7 {
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

// host 계약 v1(ORBIT-23 a3, 설계 10.1): op마다 시간 한도가 있고 hostTimeouts(op → Go 기간 글)로 바꾼다.
// 비어 있으면 설계 표의 기본값이고, 저장할 때 기본값을 파일에 쓰지 않는다.
func TestHostTimeout_기본값과_설정값(t *testing.T) {
	want := map[string]time.Duration{"version": 500 * time.Millisecond, "context": 1500 * time.Millisecond, "recall": time.Second,
		"tool-context": 500 * time.Millisecond, "call": 5 * time.Second, "delivered": 500 * time.Millisecond, "reset": 500 * time.Millisecond, "session-end": 500 * time.Millisecond}
	c := Default()
	for op, d := range want {
		if got := c.HostTimeout(op); got != d {
			t.Errorf("%s = %v, want %v", op, got, d)
		}
	}
	if got := DefaultHostTimeouts(); len(got) != len(want) {
		t.Errorf("DefaultHostTimeouts = %v", got)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full","recallChars":1000,"ledgerWindowDays":7,"recentDecisions":5,"backupKeep":14,"confirmSeenDays":2,"confirmFollowedVerified":2,"correctionRepeatSessions":3,"hostTimeouts":{"recall":"2s","context":"1ns"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.HostTimeout("recall") != 2*time.Second || got.HostTimeout("context") != time.Nanosecond || got.HostTimeout("call") != 5*time.Second {
		t.Fatalf("loaded = %v", got.HostTimeouts)
	}
	q := filepath.Join(t.TempDir(), "config.json")
	if err := Save(q, Default()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(q); strings.Contains(string(b), "hostTimeouts") {
		t.Fatalf("기본값을 파일에 썼다: %s", b)
	}
}

// 모르는 op 키는 거절하지 않고 무시한다(검토 M1): 뒤 판에서 op가 늘어 새 설정 파일에 그 op가 있어도 옛 실행 파일이
// 설정을 읽는다. 아는 op의 잘못된 기간(파싱 실패·0 이하)만 거절한다.
func TestValidate_hostTimeouts는_아는_op의_잘못된_기간만_거절하고_모르는_op는_무시한다(t *testing.T) {
	for _, bad := range []map[string]string{
		{"recall": "soon"},
		{"recall": "0s"},
		{"recall": "-1s"},
		{"recall": ""},
		{"ingest-session": "1s", "call": "never"},
	} {
		c := Default()
		c.HostTimeouts = bad
		if c.Validate() == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	for _, good := range []map[string]string{
		{"call": "10s", "context": "1ns"},
		{"memory_search": "1s"},
		{"ingest-session": "soon", "ctx": ""},
	} {
		c := Default()
		c.HostTimeouts = good
		if err := c.Validate(); err != nil {
			t.Errorf("%v: %v", good, err)
		}
	}
	// 새 판이 쓴 설정 파일: 모르는 op가 있어도 읽히고, 아는 op의 값은 쓰이고, 저장해도 모르는 op 키가 남는다.
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"hostTimeouts":{"ingest-session":"2s","recall":"3s"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.HostTimeout("recall") != 3*time.Second || c.HostTimeout("call") != 5*time.Second {
		t.Fatalf("loaded = %v", c.HostTimeouts)
	}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), `"ingest-session": "2s"`) {
		t.Fatalf("모르는 op 키를 잃었다: %s", b)
	}
}

// 저장 때 정리(설계 6.1, 계획 D8)의 비슷한 기억 문턱: 기본은 도메인 기본값, 0 < x ≤ 1만 받는다.
// 이 키가 없는 옛 설정 파일은 기본값으로 읽히고, 옛 판 실행 파일은 이 키를 모르는 키로 두고 저장할 때 남긴다.
func TestSaveSimilarMin_기본값과_범위(t *testing.T) {
	if got := Default().SaveSimilarMin; got != memory.DefaultSimilarMin {
		t.Fatalf("default = %v, want %v", got, memory.DefaultSimilarMin)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.SaveSimilarMin != memory.DefaultSimilarMin {
		t.Fatalf("old file = %v %v", c.SaveSimilarMin, err)
	}
	for _, bad := range []float64{0, -0.1, 1.01, 2} {
		c := Default()
		c.SaveSimilarMin = bad
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "saveSimilarMin") {
			t.Errorf("%v accepted or unclear: %v", bad, err)
		}
	}
	for _, good := range []float64{0.01, 0.5, 1} {
		c := Default()
		c.SaveSimilarMin = good
		if err := c.Validate(); err != nil {
			t.Errorf("%v: %v", good, err)
		}
	}
	if err := os.WriteFile(p, []byte(`{"saveSimilarMin":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("saveSimilarMin 0 in the file was accepted")
	}
	if err := os.WriteFile(p, []byte(`{"saveSimilarMin":0.75}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.SaveSimilarMin != 0.75 {
		t.Fatalf("0.75 = %v %v", c.SaveSimilarMin, err)
	}
}

// 가까운 기억 하한(1-6 b6dn): 막지 않는 저장에 붙일 가까운 기억의 비슷함 하한. 기본은 도메인 기본값, 0 < x ≤ 1만 받고,
// 이 키가 없는 옛 설정 파일은 기본값이다.
func TestSaveNearMin_기본값과_범위(t *testing.T) {
	if got := Default().SaveNearMin; got != memory.DefaultNearMin {
		t.Fatalf("default = %v, want %v", got, memory.DefaultNearMin)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.SaveNearMin != memory.DefaultNearMin {
		t.Fatalf("old file = %v %v", c.SaveNearMin, err)
	}
	for _, bad := range []float64{0, -0.1, 1.01, math.NaN()} {
		c := Default()
		c.SaveNearMin = bad
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "saveNearMin") {
			t.Errorf("%v accepted or unclear: %v", bad, err)
		}
	}
	if err := os.WriteFile(p, []byte(`{"saveNearMin":0.3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.SaveNearMin != 0.3 {
		t.Fatalf("0.3 = %v %v", c.SaveNearMin, err)
	}
}

// 끝 기준: claudeMarketplace(Claude Code 플러그인을 받을 마켓 출처)의 기본값은 crispebble/orbit-mem이고, 키가 없는 옛 설정도
// 그 값이다. 빈 값은 기본값으로 읽는다. 옵션처럼 보이거나 앞뒤 빈칸·제어 문자가 든 값은 거절한다(꼴은 install이 자세히 본다).
func TestClaudeMarketplace_기본값과_검증(t *testing.T) {
	if Default().ClaudeMarketplace != "crispebble/orbit-mem" || Default().ClaudeMarketplaceSource() != "crispebble/orbit-mem" {
		t.Fatalf("%+v", Default())
	}
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"inject":"full"}`), 0o600)
	c, err := Load(p)
	if err != nil || c.ClaudeMarketplace != DefaultClaudeMarketplace {
		t.Fatalf("옛 설정 %q %v", c.ClaudeMarketplace, err)
	}
	c.ClaudeMarketplace = "crispebble/orbit-plugins"
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(p); err != nil || got.ClaudeMarketplace != "crispebble/orbit-plugins" {
		t.Fatalf("저장한 값 %q %v", got.ClaudeMarketplace, err)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), `"claudeMarketplace": "crispebble/orbit-plugins"`) {
		t.Fatalf("파일 %s", b)
	}
	c.ClaudeMarketplace = ""
	if c.Validate() != nil || c.ClaudeMarketplaceSource() != DefaultClaudeMarketplace {
		t.Fatalf("빈 값 %q", c.ClaudeMarketplaceSource())
	}
	for _, bad := range []string{"-x", "--scope", " a/b", "a/b ", "a/b\n", "a\tb"} {
		c.ClaudeMarketplace = bad
		if err := c.Validate(); err == nil {
			t.Errorf("%q를 받았습니다", bad)
		}
	}
	for _, good := range []string{"crispebble/orbit-plugins", "https://github.com/crispebble/orbit-plugins.git", "/srv/catalog", "/Users/a/Application Support/catalog", "git@github.com:a/b.git"} {
		c.ClaudeMarketplace = good
		if err := c.Validate(); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}
