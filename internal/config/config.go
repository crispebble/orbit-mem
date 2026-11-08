// Package config holds this PC's orbit-mem settings and file locations.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/place"
)

// Injection levels (설계 「PC 설정」의 기억 주입 수준).
const (
	InjectFull  = "full"  // 규칙 전문, 최근 결정, 요청마다 관련 기억
	InjectRules = "rules" // 규칙 전문만
	InjectOff   = "off"   // 아무것도 넣지 않음(MCP로만 씀)
)

// Config is this PC's settings. Keys of earlier versions that are gone (the retrospective
// settings retroHost·sessionRetro·graphTidy*·legacy*·applyTargets·pluginSource… removed
// 2026-11-08, and orbit-code's old work settings) are ignored when read and kept as they
// were in the file when saved.
type Config struct {
	Language         string   `json:"language,omitempty"` // en(기본) | ko: orbit-mem이 사람에게 보이는 글의 언어(ORBIT_LANG이 앞선다)
	Inject           string   `json:"inject"`             // full | rules | off
	RecentDecisions  int      `json:"recentDecisions"`    // 세션 시작에 넣을 최근 결정 제목 수
	BackupKeep       int      `json:"backupKeep"`         // 남길 하루 사본 수(넘는 memory-YYYYMMDD 사본만 지운다)
	AutoBackup       bool     `json:"autoBackup"`         // 세션 시작 훅이 마지막 사본이 24시간보다 오래됐으면 뒤에서 사본을 만든다
	AutoTidy         bool     `json:"autoTidy"`           // 세션 시작 훅이 하루 한 번 뒤에서 모델 없는 정리(orbit-mem tidy auto)를 돌린다
	Hosts            []string `json:"hosts"`              // 연결한 호스트(claude, codex)
	RecallChars      int      `json:"recallChars"`        // 요청마다 넣는 작업 회상의 글자 수 상한
	LedgerWindowDays int      `json:"ledgerWindowDays"`   // 변경 효과를 보는 변경 전·후 기간(일)

	// 확정 규칙(A2, 계획 · AU-6). 후보가 스스로 확정되는 증거의 횟수와 반복 교정 표시 기준.
	// confirmFollowedVerified·correctionRepeatSessions는 E2: 이 PC 기억 DB 사본(2026-10-26~11-01)으로 잰
	// 추천값이며 사용자 결정 대기다. 근거는 Atlas 계획 · AU-6 「E2 측정」.
	ConfirmSeenDays          int `json:"confirmSeenDays"`          // ① 서로 다른 날 다시 나온 날 수(지금 규칙 2)
	ConfirmFollowedVerified  int `json:"confirmFollowedVerified"`  // ② 회상돼 따랐고 검증 통과·교정 없는 세션 수
	CorrectionRepeatSessions int `json:"correctionRepeatSessions"` // 같은 대상 교정이 나온 세션 수가 이 이상이면 표시

	// 후보 기한(설계 · orbit-mem 2판 18.4): 이 날 수 동안 넣지도 찾지도 고치지도 않은 후보는 하루 정리(tidy)가 낡음으로
	// 돌린다(원장에 남아 되돌릴 수 있다). 사용자 말 출처 기억은 대상이 아니다. 0이면 끈다.
	CandidateExpiryDays int `json:"candidateExpiryDays"`

	// 겹치는 기억(설계 · orbit-mem 2판 17절): ask(기본, 비어 있어도 같음)면 install·세션 시작 훅이 사용자에게 끌지 묻게 하고,
	// keep이면 사용자가 유지하기로 정한 것이라 더 묻지 않는다.
	HostMemory string `json:"hostMemory,omitempty"`

	// 새 판 알림: 세션 시작 훅이 하루 한 번 이하로 최신 판을 확인해 새 판이 있으면 문맥 끝에 안내 한 줄을 붙인다. false면 요청하지 않는다.
	UpdateCheck bool `json:"updateCheck"`
	// 제품 지도(설계 · orbit-mem 2판 4.1, S3 명세 2.2): 제품 이름 → 그 제품의 저장소 원격 키·폴더(글롭 가능)와 요청 글에서
	// 그 제품을 가리키는 말. 제품 범위 기억(product:<이름>)이 어느 폴더에서 보이는지를 정한다. 비어 있으면 제품이 없다.
	Products place.Map `json:"products,omitempty"`
	// host 계약의 시간 한도(설계 · 지연 회상·호스트 계약 10.1): op 이름 → Go 기간 글(예 "1.5s"). 빠진 op는 기본값이다.
	// 이 판이 모르는 op 키(뒤 판의 op)는 검사하지 않고 무시하며, 저장할 때 그대로 남는다.
	HostTimeouts map[string]string `json:"hostTimeouts,omitempty"`
}

// defaultHostTimeouts are the time limits of the host contract operations (설계 10.1 op 표).
// Its keys are the operations whose hostTimeouts value is checked; other keys are ignored.
var defaultHostTimeouts = map[string]time.Duration{
	"version":      500 * time.Millisecond,
	"context":      1500 * time.Millisecond,
	"recall":       time.Second,
	"tool-context": 500 * time.Millisecond,
	"call":         5 * time.Second,
	"delivered":    500 * time.Millisecond,
	"reset":        500 * time.Millisecond,
	"session-end":  500 * time.Millisecond,
}

// DefaultHostTimeouts is a copy of the default time limit of each host contract operation.
func DefaultHostTimeouts() map[string]time.Duration {
	out := make(map[string]time.Duration, len(defaultHostTimeouts))
	for op, d := range defaultHostTimeouts {
		out[op] = d
	}
	return out
}

// HostTimeout is the time limit of a host contract operation: hostTimeouts, else the
// default. An operation with no default and no valid setting gets 0.
func (c Config) HostTimeout(op string) time.Duration {
	if v, ok := c.HostTimeouts[op]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultHostTimeouts[op]
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
	return Config{Language: string(i18n.EN), Inject: InjectFull, RecentDecisions: 5, BackupKeep: 14, AutoBackup: true, AutoTidy: true, Hosts: []string{"claude", "codex"},
		RecallChars: 1000, LedgerWindowDays: 7,
		ConfirmSeenDays: 2, ConfirmFollowedVerified: 2, CorrectionRepeatSessions: 3, CandidateExpiryDays: 28, UpdateCheck: true}
}

// Validate checks values a person may have typed by hand.
func (c Config) Validate() error {
	if _, ok := i18n.Parse(c.Language); c.Language != "" && !ok {
		return i18n.Errorf("config.err.language", c.Language)
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
	if c.LedgerWindowDays < 1 || c.LedgerWindowDays > 90 {
		return i18n.Errorf("config.err.ledger_window")
	}
	if c.HostMemory != "" && c.HostMemory != HostMemoryAsk && c.HostMemory != HostMemoryKeep {
		return i18n.Errorf("config.err.host_memory", c.HostMemory)
	}
	if c.CandidateExpiryDays < 0 || c.CandidateExpiryDays > 365 {
		return i18n.Errorf("config.err.candidate_expiry", c.CandidateExpiryDays)
	}
	for op, v := range c.HostTimeouts {
		if _, ok := defaultHostTimeouts[op]; !ok {
			continue // 뒤 판의 op: 이 판은 쓰지 않으니 거절하지 않는다(옛 실행 파일이 새 설정 파일을 읽는다)
		}
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			return i18n.Errorf("config.err.host_timeout", op, v)
		}
	}
	if err := c.Products.Validate(); err != nil {
		return err
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

// Home is orbit-mem's data directory: ORBIT_HOME, else the OS config directory + /orbit.
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
func (p Paths) Logs() string        { return filepath.Join(p.Home, "logs") }

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

// Save writes the configuration. Keys in the file that Config does not know (orbit-code's
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
