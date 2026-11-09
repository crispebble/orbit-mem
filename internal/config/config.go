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
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

// Injection levels (설계 「PC 설정」의 기억 주입 수준).
const (
	InjectFull  = "full"  // 규칙 전문, 최근 결정, 요청마다 관련 기억
	InjectRules = "rules" // 규칙 전문만
	InjectOff   = "off"   // 아무것도 넣지 않음(MCP로만 씀)
)

// Recall modes (설계 보완 T-2): the new key `recall`. The old key `inject` reads as the same meaning
// (full=auto, rules=on_demand, off=off).
const (
	RecallAuto     = "auto"      // 기본: 규칙·B1 회상·요청마다 회상
	RecallOnDemand = "on_demand" // B1 없음(옛 inject=rules와 같은 뜻)
	RecallOff      = "off"       // 아무것도 넣지 않음
)

var recallRank = map[string]int{RecallAuto: 0, RecallOnDemand: 1, RecallOff: 2}

// InjectOfRecall maps a recall mode to the old inject value, which an older executable still reads.
func InjectOfRecall(recall string) string {
	switch recall {
	case RecallOnDemand:
		return InjectRules
	case RecallOff:
		return InjectOff
	}
	return InjectFull
}

// RecallMode is the recall setting: `recall` and the old `inject` read alike; when they differ the
// more restrictive one wins (off > on_demand > auto). An empty recall (the default, or a file from
// before the key) follows inject.
func (c Config) RecallMode() string {
	mode := RecallAuto
	switch c.Inject {
	case InjectRules:
		mode = RecallOnDemand
	case InjectOff:
		mode = RecallOff
	}
	if r, ok := recallRank[c.Recall]; ok && r > recallRank[mode] {
		mode = c.Recall
	}
	return mode
}

// Config is this PC's settings. Keys of earlier versions that are gone (the retrospective
// settings retroHost·sessionRetro·graphTidy*·legacy*·applyTargets·pluginSource… removed
// 2026-11-08, and orbit-code's old work settings) are ignored when read and kept as they
// were in the file when saved.
type Config struct {
	Language         string   `json:"language,omitempty"` // en(기본) | ko: orbit-mem이 사람에게 보이는 글의 언어(ORBIT_LANG이 앞선다)
	Inject           string   `json:"inject"`             // full | rules | off (옛 키: recall과 같은 뜻으로 읽는다)
	Recall           string   `json:"recall,omitempty"`   // auto | on_demand | off (비면 inject를 따른다)
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
	// 저장 때 정리(설계 6.1, 계획 D8): memory_save가 같은 범위의 기억을 「비슷하다」고 보고 저장 전에 고르게 하는
	// 저장 때 비슷함 점수(0~1, 두 기억 사이 낱말 두 방향 덮음의 기하 평균)의 문턱. 0 < x ≤ 1. 이 키가 없는 옛 파일은 기본값(memory.DefaultSimilarMin)이다.
	SaveSimilarMin float64 `json:"saveSimilarMin"`
	// 가까운 기억(1-6 b6dn): memory_save가 막지 않고 저장할 때 같은 범위의 기억 가운데 이 점수(같은 비슷함) 이상인 것을
	// 최대 3개 id·제목 한 줄로 붙인다. 0 < x ≤ 1. saveSimilarMin 이상이면 붙는 것이 없다. 이 키가 없는 옛 파일은 기본값(memory.DefaultNearMin)이다.
	SaveNearMin float64 `json:"saveNearMin"`
	// 요청 회상의 두 문턱(설계 · 지연 회상·호스트 계약·어댑터 5.1 4단계): 기억은 찾기 점수(0~1, memory.RecallQuery로 찾은
	// 점수)가 recallMinScore 이상이고 요청의 뜻 있는 낱말 중 맞은 비율(0~1)이 recallMinCoverage 이상일 때만 요청 회상에 든다.
	// 0 < x ≤ 1. 이 키가 없는 옛 파일은 기본값이다.
	// recallMinCoverage는 맞은 낱말이 둘 이상인 기억의 비율 문턱이고, 맞은 낱말이 하나뿐인 기억은 recallMinCoverageOneWord를
	// 넘어야 한다(설계 보완, 계획 1-6 b6 R1): 「낱말 넷 중 하나」처럼 흔한 낱말 하나로 맞은 주제 밖 기억을 막는다.
	RecallMinScore           float64 `json:"recallMinScore"`
	RecallMinCoverage        float64 `json:"recallMinCoverage"`
	RecallMinCoverageOneWord float64 `json:"recallMinCoverageOneWord"`
	// 요청 회상의 상대 문턱(설계 보완, 계획 1-6 b6): 두 문턱을 넘은 기억 가운데 찾기 점수가 1위의 recallMinRelative배
	// 미만인 것은 뺀다. 확정을 앞에 두는 순서 때문에 약하게 맞은 기억이 3줄에 끼는 것을 막는다. 0 < x ≤ 1. 이 키가 없는 옛
	// 파일은 기본값이다.
	RecallMinRelative float64 `json:"recallMinRelative"`
	// 위임 회상의 문턱(설계 보완, 계획 1-6 b6 T1): 위임 글은 길고 여러 주제라 맞은 비율을 쓰지 않는다. 위임 글의 내용 낱말
	// (명령형 어미·위임 머리 낱말·항목 번호를 뺀 것, memory.TaskWords) 가운데 taskRecallMinWords개 이상이 맞고, 찾기 점수가
	// taskRecallMinScore 이상이며, 1위 점수의 taskRecallMinRelative배 이상인 기억만 넣는다. 점수·배는 0 < x ≤ 1, 낱말 수는
	// 2~40이다(MinTaskRecallMinWords). 이 키가 없는 옛 파일은 기본값이다.
	TaskRecallMinScore    float64 `json:"taskRecallMinScore"`
	TaskRecallMinWords    int     `json:"taskRecallMinWords"`
	TaskRecallMinRelative float64 `json:"taskRecallMinRelative"`
	// 위임 회상의 제목 몫 문턱(설계 보완, 계획 1-6 b7f): 위의 세 문턱을 넘어도, 내용 낱말 가운데 그 기억의 제목·주제에 나온
	// 낱말의 드묾(저장소 IDF를 한 기억에만 있는 낱말의 IDF로 나눈 합, 「드문 낱말 몇 개 몫」)을 내용 낱말 수로 나눈 값
	// (memory.RecallHit.TitleShare)이 taskRecallMinTitleShare 미만이면 넣지 않는다. 0 < x ≤ 1. 이 키가 없는 옛 파일은 기본값이다.
	TaskRecallMinTitleShare float64 `json:"taskRecallMinTitleShare"`
}

// 회상 문턱의 기본값(계획 1-6 b6 2차). 공개 평가 묶음 전체(internal/bench/testdata/public_set.json, 학습용과 옛 판정용 모두
// 개발 자료)로 골랐다. 지금 기본값으로 잰 공개 묶음 전체는 요청 적중 81/91·틀린 회상 7/104·넣지 않음 42/46, 위임 적중
// 5/21·틀린 0/19·넣지 않음 6/6이다. 고를 때는 요청 적중 77/91·위임 적중 4/21이었고, deploy-fact를 6개 사례의 허용으로
// 낮춘 라벨 변경(2026-11-09, 사용자 결정)과 위임 제목 몫 문턱(b7f)을 거쳐 지금 값이 됐다. 아래 비교 값은 고를 때의 것이다.
//   - recallMinScore 0.3: 저장소가 모르는 낱말을 빼고 재는 회상 점수에서 0.2~0.35가 같은 결과인 평탄 구간의 가운데다(0.4부터
//     적중이 준다).
//   - recallMinCoverage 0.25·recallMinCoverageOneWord 0.3: 한 낱말 비율을 0.25로 내리면 틀린 회상이 14.7%로, 0.35로 올리면
//     적중이 73/91로 바뀐다.
//   - recallMinRelative 0.9: 0.85와 같은 결과다.
//   - 위임 taskRecallMinWords 3: 2면 틀린 회상 35%, 4면 적중 1/21. taskRecallMinScore 0.1(0.05~0.15 같음)·
//     taskRecallMinRelative 0.3(0.25~0.35 같음).
//   - 위임 taskRecallMinTitleShare 0.05(계획 1-6 b7f): 위 세 문턱은 기억 207개짜리 공개 묶음에서 골라, 기억 1,326개 저장소의
//     긴 위임 글(내용 낱말 36~40개)에서는 아무 기억이나 통과했다. 재생 묶음 학습용(사람 미검토 정답표 초안)에서 위임 회상
//     맞음 24·틀림 309/334 → 맞음 17·틀림 4/25(적중 5/43 → 2/43, 넣지 않음 0/24 → 22/24). 0.045면 틀림 8/34, 0.055면 맞음
//     14. 학습용을 세션 반반으로 나눠도 두 쪽 모두 틀림이 줄었다(194 → 0, 109 → 4). 공개 묶음 전체는 결과가 같다.
const (
	DefaultRecallMinScore           = 0.3
	DefaultRecallMinCoverage        = 0.25
	DefaultRecallMinCoverageOneWord = 0.3
	DefaultRecallMinRelative        = 0.9
	DefaultTaskRecallMinScore       = 0.1
	DefaultTaskRecallMinWords       = 3
	DefaultTaskRecallMinRelative    = 0.3
	DefaultTaskRecallMinTitleShare  = 0.05
)

// MinTaskRecallMinWords and MaxTaskRecallMinWords bound taskRecallMinWords. The least is 2: a task, like a
// request, needs two words or more for any recall (one word scores every memory that has it alike), so 1 would
// act as 2 for a one-word task and as 1 for a longer one. The most is 40: a request has at most 40 meaningful words.
const (
	MinTaskRecallMinWords = 2
	MaxTaskRecallMinWords = 40
)

// MaxPromptRecallChars is the length of the work recall of one request (설계 4절 B1 ≤500자). recallChars can only lower it.
const MaxPromptRecallChars = 500

// PromptRecallChars is the length the work recall of one request may use: recallChars, at most MaxPromptRecallChars.
func (c Config) PromptRecallChars() int {
	if c.RecallChars <= 0 {
		return MaxPromptRecallChars
	}
	return min(c.RecallChars, MaxPromptRecallChars)
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

// maxRecallChars is the largest recallChars a file may hold (files written before the work recall
// was bounded keep loading, the old default 1000 among them). The work recall itself uses PromptRecallChars,
// MaxPromptRecallChars at most, which is also the default.
const maxRecallChars = 9000

// Default is the starting configuration.
func Default() Config {
	return Config{Language: string(i18n.EN), Inject: InjectFull, RecentDecisions: 5, BackupKeep: 14, AutoBackup: true, AutoTidy: true, Hosts: []string{"claude", "codex"},
		RecallChars: MaxPromptRecallChars, LedgerWindowDays: 7,
		ConfirmSeenDays: 2, ConfirmFollowedVerified: 2, CorrectionRepeatSessions: 3, CandidateExpiryDays: 28, UpdateCheck: true,
		SaveSimilarMin: memory.DefaultSimilarMin, SaveNearMin: memory.DefaultNearMin, RecallMinScore: DefaultRecallMinScore, RecallMinCoverage: DefaultRecallMinCoverage,
		RecallMinCoverageOneWord: DefaultRecallMinCoverageOneWord, RecallMinRelative: DefaultRecallMinRelative,
		TaskRecallMinScore: DefaultTaskRecallMinScore, TaskRecallMinWords: DefaultTaskRecallMinWords, TaskRecallMinRelative: DefaultTaskRecallMinRelative,
		TaskRecallMinTitleShare: DefaultTaskRecallMinTitleShare}
}

// Validate checks values a person may have typed by hand.
func (c Config) Validate() error {
	if _, ok := i18n.Parse(c.Language); c.Language != "" && !ok {
		return i18n.Errorf("config.err.language", c.Language)
	}
	if c.Inject != InjectFull && c.Inject != InjectRules && c.Inject != InjectOff {
		return i18n.Errorf("config.err.inject", c.Inject)
	}
	if _, ok := recallRank[c.Recall]; c.Recall != "" && !ok {
		return i18n.Errorf("config.err.recall", c.Recall)
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
	if !(c.SaveSimilarMin > 0 && c.SaveSimilarMin <= 1) { // NaN도 거절한다
		return i18n.Errorf("config.err.save_similar_min", c.SaveSimilarMin)
	}
	if !(c.SaveNearMin > 0 && c.SaveNearMin <= 1) { // NaN도 거절한다
		return i18n.Errorf("config.err.save_near_min", c.SaveNearMin)
	}
	if !(c.RecallMinScore > 0 && c.RecallMinScore <= 1) { // NaN도 거절한다
		return i18n.Errorf("config.err.recall_min_score", c.RecallMinScore)
	}
	if !(c.RecallMinCoverage > 0 && c.RecallMinCoverage <= 1) {
		return i18n.Errorf("config.err.recall_min_coverage", c.RecallMinCoverage)
	}
	if !(c.RecallMinRelative > 0 && c.RecallMinRelative <= 1) {
		return i18n.Errorf("config.err.recall_min_relative", c.RecallMinRelative)
	}
	if !(c.RecallMinCoverageOneWord > 0 && c.RecallMinCoverageOneWord <= 1) {
		return i18n.Errorf("config.err.recall_min_coverage_one_word", c.RecallMinCoverageOneWord)
	}
	if !(c.TaskRecallMinScore > 0 && c.TaskRecallMinScore <= 1) {
		return i18n.Errorf("config.err.task_recall_min_score", c.TaskRecallMinScore)
	}
	if c.TaskRecallMinWords < MinTaskRecallMinWords || c.TaskRecallMinWords > MaxTaskRecallMinWords {
		return i18n.Errorf("config.err.task_recall_min_words", MinTaskRecallMinWords, MaxTaskRecallMinWords, c.TaskRecallMinWords)
	}
	if !(c.TaskRecallMinRelative > 0 && c.TaskRecallMinRelative <= 1) {
		return i18n.Errorf("config.err.task_recall_min_relative", c.TaskRecallMinRelative)
	}
	if !(c.TaskRecallMinTitleShare > 0 && c.TaskRecallMinTitleShare <= 1) {
		return i18n.Errorf("config.err.task_recall_min_title_share", c.TaskRecallMinTitleShare)
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
	// 두 키를 같은 뜻으로 쓴다: 옛 실행 파일은 recall을 모르고 inject대로 돈다.
	c.Recall = c.RecallMode()
	c.Inject = InjectOfRecall(c.Recall)
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
