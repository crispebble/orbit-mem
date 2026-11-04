// Package config holds this PC's orbit-mem settings and file locations.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// Injection levels (설계 「PC 설정」의 기억 주입 수준).
const (
	InjectFull  = "full"  // 규칙 전문, 최근 결정, 회고 한 줄, 요청마다 관련 기억
	InjectRules = "rules" // 규칙 전문만
	InjectOff   = "off"   // 아무것도 넣지 않음(MCP로만 씀)
)

// Config is this PC's settings.
type Config struct {
	Language        string   `json:"language,omitempty"`     // en(기본) | ko: orbit-mem이 사람에게 보이는 글의 언어(ORBIT_LANG이 앞선다)
	RetroHost       string   `json:"retroHost"`              // claude | codex
	RetroModel      string   `json:"retroModel"`             // 비우면 호스트 기본값
	RetroHour       int      `json:"retroHour"`              // 하루 회고 시각(0-23)
	Inject          string   `json:"inject"`                 // full | rules | off
	RecentDecisions int      `json:"recentDecisions"`        // 세션 시작에 넣을 최근 결정 제목 수
	BackupKeep      int      `json:"backupKeep"`             // 남길 하루 사본 수(넘는 memory-YYYYMMDD·work-YYYYMMDD 사본만 지운다)
	AutoBackup      bool     `json:"autoBackup"`             // 세션 시작 훅이 마지막 사본이 24시간보다 오래됐으면 뒤에서 사본을 만든다
	Hosts           []string `json:"hosts"`                  // 연결한 호스트(claude, codex)
	ClaudePath      string   `json:"claudePath,omitempty"`   // 예약 실행에서 쓸 claude 절대경로
	CodexPath       string   `json:"codexPath,omitempty"`    // 예약 실행에서 쓸 codex 절대경로
	ApplyTargets    []string `json:"applyTargets,omitempty"` // 회고가 반영 제안을 낼 수 있는 파일·디렉터리(플러그인 원본, 내 지침 파일)
	PluginSource    string   `json:"pluginSource,omitempty"` // OpenOrbit 플러그인을 바이너리 내장 원본 대신 이 디렉터리에서 빌드한다(스킬을 고쳐 쓰는 PC)
	// 옛 작업 기록 설정(work·workTestCommands·workStaleAfter)은 작업 기록이 OpenOrbit으로 옮겨 갔고
	// OpenOrbit이 같은 config.json에서 아직 읽는다. 모르는 키는 읽을 때 무시하고, 저장할 때는 파일에 있던 그대로 보존한다.

	SessionRetro         bool   `json:"sessionRetro"`                // 세션이 끝나면 그 세션만 짧게 회고한다
	SessionRetroModel    string `json:"sessionRetroModel,omitempty"` // 비우면 claude는 haiku, codex는 호스트 기본
	SessionRetroDailyMax int    `json:"sessionRetroDailyMax"`        // 하루 세션 끝 회고 실행 상한, 0은 상한 없음
	RecallChars          int    `json:"recallChars"`                 // 요청마다 넣는 작업 회상의 글자 수 상한
	LedgerWindowDays     int    `json:"ledgerWindowDays"`            // 변경 효과를 보는 변경 전·후 기간(일)

	// 기억 그래프 정리(설계 · Orbit 3차 O3-2). 하루 회고에 붙는다. 0이면 그 부분을 끈다.
	// 기본값은 첫 실행 실측(기억 40+20개를 보면 제안 18~20건, $0.28~0.38)을 보고 사용자가 정했다(2026-10-30, 회당 20건).
	GraphTidyMemories  int `json:"graphTidyMemories"`  // 회당 연결 후보를 볼 클러스터 기억 수
	GraphTidyUntagged  int `json:"graphTidyUntagged"`  // 회당 태그를 달 주제 태그 없는 기억 수
	GraphTidyProposals int `json:"graphTidyProposals"` // 회당 받는 정리 제안(link·retag) 수 상한, 0은 상한 없음

	// 옛 기억 정리(설계 · Orbit 3차 O3-3). 옮겨 온 옛 기억(imported:*)을 묶음마다 판정한다. OpenOrbit 예약 회고(orb retro)가 하루에
	// LegacyBatches 묶음까지 돌리고, 0이면 끈다(orb retro legacy는 그래도 돈다). 판정할 옛 기억이 없으면 돌지 않는다.
	// 첫 실행 실측(30개 묶음 2회, 약 50초·$0.27~0.40)을 보고 사용자가 정했다(2026-10-30, 남은 것은 한 번에 판정하고 예약은 하루 1묶음).
	LegacyMemories int      `json:"legacyMemories"`         // 한 묶음에 판정할 옛 기억 수
	LegacyBatches  int      `json:"legacyBatches"`          // 예약 회고가 하루에 돌리는 묶음 수
	RetiredNames   []string `json:"retiredNames,omitempty"` // 보관한 저장소·옛 제품 이름(낡음 판정의 근거로 보인다)

	// 확정 규칙(A2, 계획 · AU-6). 후보가 스스로 확정되는 증거의 횟수와 반복 교정 표시 기준.
	// confirmFollowedVerified·correctionRepeatSessions는 E2: 이 PC 기억 DB 사본(2026-10-26~11-01)으로 잰
	// 추천값이며 사용자 결정 대기다. 근거는 Atlas 계획 · AU-6 「E2 측정」.
	ConfirmSeenDays          int `json:"confirmSeenDays"`          // ① 서로 다른 날 다시 나온 날 수(지금 규칙 2)
	ConfirmFollowedVerified  int `json:"confirmFollowedVerified"`  // ② 회상돼 따랐고 검증 통과·교정 없는 세션 수
	CorrectionRepeatSessions int `json:"correctionRepeatSessions"` // 같은 대상 교정이 나온 세션 수가 이 이상이면 표시

	// 후보 기한(설계 · orbit-mem 2판 18.4): 이 날 수 동안 넣지도 찾지도 고치지도 않은 후보는 회고 정리 단계가 낡음으로
	// 돌린다(원장에 남아 되돌릴 수 있다). 사용자 말 출처 기억은 대상이 아니다. 0이면 끈다.
	CandidateExpiryDays int `json:"candidateExpiryDays"`

	// 겹치는 기억(설계 · orbit-mem 2판 17절): ask(기본, 비어 있어도 같음)면 install·세션 시작 훅이 사용자에게 끌지 묻게 하고,
	// keep이면 사용자가 유지하기로 정한 것이라 더 묻지 않는다.
	HostMemory string `json:"hostMemory,omitempty"`

	// 새 판 알림: 세션 시작 훅이 하루 한 번 이하로 최신 판을 확인해 새 판이 있으면 문맥 끝에 안내 한 줄을 붙인다. false면 요청하지 않는다.
	UpdateCheck bool `json:"updateCheck"`
}

// HostMemory values.
const (
	HostMemoryAsk  = "ask"
	HostMemoryKeep = "keep"
)

// maxRecallChars keeps the per-request context under the hosts' hook context
// limit (about 10,000 characters in Claude Code).
const maxRecallChars = 9000

// Default is the starting configuration.
func Default() Config {
	return Config{Language: string(i18n.EN), RetroHost: "claude", RetroHour: 4, Inject: InjectFull, RecentDecisions: 5, BackupKeep: 14, AutoBackup: true, Hosts: []string{"claude", "codex"},
		SessionRetro: true, RecallChars: 1000, LedgerWindowDays: 7, GraphTidyMemories: 40, GraphTidyUntagged: 20, GraphTidyProposals: 20,
		LegacyMemories: 30, LegacyBatches: 1,
		ConfirmSeenDays: 2, ConfirmFollowedVerified: 2, CorrectionRepeatSessions: 3, CandidateExpiryDays: 28, UpdateCheck: true}
}

// Validate checks values a person may have typed by hand.
func (c Config) Validate() error {
	if _, ok := i18n.Parse(c.Language); c.Language != "" && !ok {
		return i18n.Errorf("config.err.language", c.Language)
	}
	if c.RetroHost != "claude" && c.RetroHost != "codex" {
		return i18n.Errorf("config.err.retro_host", c.RetroHost)
	}
	if c.RetroHour < 0 || c.RetroHour > 23 {
		return i18n.Errorf("config.err.retro_hour", c.RetroHour)
	}
	if c.Inject != InjectFull && c.Inject != InjectRules && c.Inject != InjectOff {
		return i18n.Errorf("config.err.inject", c.Inject)
	}
	if c.RecentDecisions < 0 || c.RecentDecisions > 20 || c.BackupKeep < 1 {
		return i18n.Errorf("config.err.recent_backup")
	}
	if c.RecallChars < 100 || c.RecallChars > maxRecallChars {
		return i18n.Errorf("config.err.recall_chars", maxRecallChars, c.RecallChars)
	}
	if c.LedgerWindowDays < 1 || c.LedgerWindowDays > 90 || c.SessionRetroDailyMax < 0 {
		return i18n.Errorf("config.err.ledger_session")
	}
	if c.GraphTidyMemories < 0 || c.GraphTidyMemories > 200 || c.GraphTidyUntagged < 0 || c.GraphTidyUntagged > 200 || c.GraphTidyProposals < 0 || c.GraphTidyProposals > 20 {
		return i18n.Errorf("config.err.graph_tidy")
	}
	if c.LegacyMemories < 1 || c.LegacyMemories > 100 || c.LegacyBatches < 0 || c.LegacyBatches > 20 {
		return i18n.Errorf("config.err.legacy")
	}
	if c.HostMemory != "" && c.HostMemory != HostMemoryAsk && c.HostMemory != HostMemoryKeep {
		return i18n.Errorf("config.err.host_memory", c.HostMemory)
	}
	if c.CandidateExpiryDays < 0 || c.CandidateExpiryDays > 365 {
		return i18n.Errorf("config.err.candidate_expiry", c.CandidateExpiryDays)
	}
	for _, n := range []int{c.ConfirmSeenDays, c.ConfirmFollowedVerified, c.CorrectionRepeatSessions} {
		if n < 1 || n > 30 {
			return i18n.Errorf("config.err.evidence")
		}
	}
	return nil
}

// Language is the language setting of the configuration file, read even when
// the rest of the file does not validate (so its errors are in that language).
func Language(path string) string {
	var c struct {
		Language string `json:"language"`
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c.Language
}

// Home is OpenOrbit's data directory: ORBIT_HOME, else the OS config directory + /orbit.
func Home() string {
	if h := os.Getenv("ORBIT_HOME"); h != "" {
		return h
	}
	return DefaultHome()
}

// DefaultHome is the home without ORBIT_HOME: orbit under the user config folder.
func DefaultHome() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "orbit")
}

// Paths are the files under Home.
type Paths struct{ Home string }

func (p Paths) Config() string      { return filepath.Join(p.Home, "config.json") }
func (p Paths) DB() string          { return filepath.Join(p.Home, "memory.db") }
func (p Paths) Backups() string     { return filepath.Join(p.Home, "backups") }
func (p Paths) Marketplace() string { return filepath.Join(p.Home, "plugin") }
func (p Paths) RetroDir() string    { return filepath.Join(p.Home, "retro") }
func (p Paths) Logs() string        { return filepath.Join(p.Home, "logs") }
func (p Paths) WorkDB() string      { return filepath.Join(p.Home, "work.db") } // 옛 작업 기록(읽기·하루 백업만)

// Load reads the configuration; a missing file gives the defaults.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, i18n.Errorf("config.err.read", path, err)
	}
	if l, ok := i18n.Parse(c.Language); ok {
		c.Language = string(l) // 손으로 쓴 "KO"·"ko_KR"도 받는다
	}
	return c, c.Validate()
}

// Save writes the configuration. Keys in the file that Config does not know (OpenOrbit's
// work settings, a newer version's) are kept as they were; the known keys are overwritten.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cur, _ := json.Marshal(c)
	var mine map[string]json.RawMessage
	_ = json.Unmarshal(cur, &mine)
	out := map[string]json.RawMessage{}
	if old, err := os.ReadFile(path); err == nil {
		var prev map[string]json.RawMessage
		if json.Unmarshal(old, &prev) == nil {
			known := knownKeys()
			for k, v := range prev {
				if !known[k] {
					out[k] = v
				}
			}
		}
	}
	for k, v := range mine {
		out[k] = v
	}
	b, _ := json.MarshalIndent(out, "", "  ") // 맵은 키 순서로 쓴다
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// knownKeys are the JSON keys of Config's fields (also those left out when empty).
func knownKeys() map[string]bool {
	known := map[string]bool{}
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			known[name] = true
		}
	}
	return known
}
