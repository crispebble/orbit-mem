package install

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Codex hides update_plan since 0.152 unless [tools.update_plan] enabled = true
// (기억 m-9f5b8a2fb097). Orbit's work graph reads that tool on Codex.

var (
	tomlTable   = regexp.MustCompile(`^\s*\[([^\[\]]+)\]\s*(#.*)?$`)
	tomlEnabled = regexp.MustCompile(`^\s*([A-Za-z0-9_.]*?)enabled\s*=\s*(true|false)\s*(#.*)?$`)
)

// CodexPlanEnabled says whether ~/.codex/config.toml turns update_plan on.
func CodexPlanEnabled(codexHome string) bool {
	b, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		return false
	}
	table := ""
	on := false
	for _, line := range strings.Split(string(b), "\n") {
		if m := tomlTable.FindStringSubmatch(line); m != nil {
			table = strings.TrimSpace(m[1])
			continue
		}
		m := tomlEnabled.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := strings.TrimSuffix(m[1], ".")
		full := strings.Trim(table+"."+key, ".")
		if full == "tools.update_plan" {
			on = m[2] == "true"
		}
	}
	return on
}

// EnableCodexPlan turns update_plan on in config.toml after keeping a copy of
// the file. Call it only when the person asked (oni install --enable-codex-plan).
func EnableCodexPlan(codexHome string) error {
	if CodexPlanEnabled(codexHome) {
		return nil
	}
	path := filepath.Join(codexHome, "config.toml")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	if len(b) > 0 {
		if err := os.WriteFile(path+".oni-"+time.Now().Format("20060102T150405"), b, 0o600); err != nil {
			return err
		}
	}
	lines := strings.Split(string(b), "\n")
	table := ""
	for i, line := range lines {
		if m := tomlTable.FindStringSubmatch(line); m != nil {
			table = strings.TrimSpace(m[1])
			continue
		}
		if m := tomlEnabled.FindStringSubmatch(line); m != nil && strings.Trim(table+"."+strings.TrimSuffix(m[1], "."), ".") == "tools.update_plan" {
			lines[i] = strings.Replace(line, "false", "true", 1)
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
		}
	}
	out := strings.TrimRight(string(b), "\n")
	if out != "" {
		out += "\n\n"
	}
	out += "# Orbit 작업 그래프가 Codex 계획 도구를 읽는다(oni install --enable-codex-plan)\n[tools.update_plan]\nenabled = true\n"
	return os.WriteFile(path, []byte(out), 0o600)
}
