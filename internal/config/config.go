// Package config holds this PC's Orbit settings and file locations.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Injection levels (설계 「PC 설정」의 기억 주입 수준).
const (
	InjectFull  = "full"  // 규칙 전문, 최근 결정, 회고 한 줄, 요청마다 관련 기억
	InjectRules = "rules" // 규칙 전문만
	InjectOff   = "off"   // 아무것도 넣지 않음(MCP로만 씀)
)

// Config is this PC's settings.
type Config struct {
	RetroHost       string   `json:"retroHost"`              // claude | codex
	RetroModel      string   `json:"retroModel"`             // 비우면 호스트 기본값
	RetroHour       int      `json:"retroHour"`              // 하루 회고 시각(0-23)
	Inject          string   `json:"inject"`                 // full | rules | off
	RecentDecisions int      `json:"recentDecisions"`        // 세션 시작에 넣을 최근 결정 제목 수
	BackupKeep      int      `json:"backupKeep"`             // 남길 일일 백업 수
	Hosts           []string `json:"hosts"`                  // 연결한 호스트(claude, codex)
	ClaudePath      string   `json:"claudePath,omitempty"`   // 예약 실행에서 쓸 claude 절대경로
	CodexPath       string   `json:"codexPath,omitempty"`    // 예약 실행에서 쓸 codex 절대경로
	ApplyTargets    []string `json:"applyTargets,omitempty"` // 회고가 반영 제안을 낼 수 있는 파일·디렉터리(플러그인 원본, 내 지침 파일)
	PluginSource    string   `json:"pluginSource,omitempty"` // Orbit 플러그인을 바이너리 내장 원본 대신 이 디렉터리에서 빌드한다(스킬을 고쳐 쓰는 PC)
	// 작업 그래프(설계 · Orbit 2차 「구성」 PC 설정). 이름은 제안이다.
	Work             string   `json:"work,omitempty"`             // on(기본) | off: 작업 기록 켜기
	WorkTestCommands []string `json:"workTestCommands,omitempty"` // 기본 시험 명령 목록에 더할 명령(설계 O6)
	WorkStaleAfter   string   `json:"workStaleAfter,omitempty"`   // 응답 없음으로 볼 시간(예: 20m). 비우면 계산하지 않는다(설계 O5)
}

// Work recording switch values.
const (
	WorkOnValue = "on"
	WorkOff     = "off"
)

// WorkOn says whether hooks record the work graph.
func (c Config) WorkOn() bool { return c.Work != WorkOff }

// StaleAfter is the no-response time, or 0 when it is not set.
func (c Config) StaleAfter() time.Duration {
	d, err := time.ParseDuration(c.WorkStaleAfter)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// Default is the starting configuration.
func Default() Config {
	return Config{RetroHost: "claude", RetroHour: 4, Inject: InjectFull, RecentDecisions: 5, BackupKeep: 14, Hosts: []string{"claude", "codex"}}
}

// Validate checks values a person may have typed by hand.
func (c Config) Validate() error {
	if c.RetroHost != "claude" && c.RetroHost != "codex" {
		return fmt.Errorf("retroHost는 claude 또는 codex입니다: %q", c.RetroHost)
	}
	if c.RetroHour < 0 || c.RetroHour > 23 {
		return fmt.Errorf("retroHour는 0-23입니다: %d", c.RetroHour)
	}
	if c.Inject != InjectFull && c.Inject != InjectRules && c.Inject != InjectOff {
		return fmt.Errorf("inject는 full|rules|off입니다: %q", c.Inject)
	}
	if c.Work != "" && c.Work != WorkOnValue && c.Work != WorkOff {
		return fmt.Errorf("work는 on|off입니다: %q", c.Work)
	}
	if c.WorkStaleAfter != "" {
		if d, err := time.ParseDuration(c.WorkStaleAfter); err != nil || d <= 0 {
			return fmt.Errorf("workStaleAfter는 20m 같은 양의 시간입니다: %q", c.WorkStaleAfter)
		}
	}
	if c.RecentDecisions < 0 || c.RecentDecisions > 20 || c.BackupKeep < 1 {
		return errors.New("recentDecisions(0-20)·backupKeep(1 이상)을 확인해 주세요")
	}
	return nil
}

// Home is Orbit's data directory: ORBIT_HOME, else the OS config directory + /orbit.
func Home() string {
	if h := os.Getenv("ORBIT_HOME"); h != "" {
		return h
	}
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
func (p Paths) WorkDB() string      { return filepath.Join(p.Home, "work.db") }
func (p Paths) WorkSpool() string   { return filepath.Join(p.Home, "work", "spool") }

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
		return c, fmt.Errorf("설정 파일을 읽을 수 없습니다(%s): %w", path, err)
	}
	return c, c.Validate()
}

// Save writes the configuration.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
