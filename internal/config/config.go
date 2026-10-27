// Package config holds this PC's Orbit settings and file locations.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Injection levels (설계 「PC 설정」의 기억 주입 수준).
const (
	InjectFull  = "full"  // 규칙 전문, 최근 결정, 회고 한 줄, 요청마다 관련 기억
	InjectRules = "rules" // 규칙 전문만
	InjectOff   = "off"   // 아무것도 넣지 않음(MCP로만 씀)
)

// Config is this PC's settings.
type Config struct {
	RetroHost       string   `json:"retroHost"`       // claude | codex
	RetroModel      string   `json:"retroModel"`      // 비우면 호스트 기본값
	RetroHour       int      `json:"retroHour"`       // 하루 회고 시각(0-23)
	Inject          string   `json:"inject"`          // full | rules | off
	RecentDecisions int      `json:"recentDecisions"` // 세션 시작에 넣을 최근 결정 제목 수
	BackupKeep      int      `json:"backupKeep"`      // 남길 일일 백업 수
	Hosts           []string `json:"hosts"`           // 연결한 호스트(claude, codex)
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
