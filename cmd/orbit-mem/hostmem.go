package main

// 호스트 자체 기억 점검(설계 · orbit-mem 2판 9절): 기억은 orbit-mem 하나만 쓴다. Claude Code auto memory나
// Codex memories가 켜져 있으면 doctor가 알리고 끄는 법을 보인다.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// claudeConfigDir is Claude Code's settings folder: $CLAUDE_CONFIG_DIR, else ~/.claude.
func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// claudeAutoMemory says whether Claude Code's own auto memory is on and in which
// settings file: it is on unless the user settings turn it off (autoMemoryEnabled
// false) or CLAUDE_CODE_DISABLE_AUTO_MEMORY is set. Without a settings folder Claude
// Code is not set up here, so there is nothing to say.
func claudeAutoMemory() (string, bool) {
	dir := claudeConfigDir()
	if _, err := os.Stat(dir); err != nil {
		return "", false
	}
	switch strings.ToLower(os.Getenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY")) {
	case "1", "true", "yes":
		return "", false
	}
	path := filepath.Join(dir, "settings.json")
	var v struct {
		AutoMemoryEnabled *bool `json:"autoMemoryEnabled"`
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &v)
	}
	return path, v.AutoMemoryEnabled == nil || *v.AutoMemoryEnabled
}

// codexMemories says whether Codex's memories are turned on in its config.toml
// (features.memories, memories.generate_memories or memories.use_memories true).
// Codex keeps the feature off unless it is turned on.
func codexMemories(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	section := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[] ")
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(val) != "true" {
			continue
		}
		key = strings.TrimSpace(key)
		if section != "" {
			key = section + "." + key
		}
		switch key {
		case "features.memories", "memories.generate_memories", "memories.use_memories":
			return true
		}
	}
	return false
}
