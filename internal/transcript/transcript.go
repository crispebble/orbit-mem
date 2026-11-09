// Package transcript finds the hosts' data directories and the memory files the hosts keep
// themselves (Claude Code's automatic memory, Codex's memories), for the host-memory check.
// Reading conversation logs for the retrospectives was removed (2026-11-08, ORBIT-13).
package transcript

import (
	"os"
	"path/filepath"
	"sort"
)

// Roots are the hosts' data directories (~/.claude and ~/.codex).
type Roots struct{ Claude, Codex string }

// DefaultRoots uses CLAUDE_CONFIG_DIR and CODEX_HOME when set.
func DefaultRoots() Roots {
	home, _ := os.UserHomeDir()
	r := Roots{Claude: filepath.Join(home, ".claude"), Codex: filepath.Join(home, ".codex")}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		r.Claude = v
	}
	if v := os.Getenv("CODEX_HOME"); v != "" {
		r.Codex = v
	}
	return r
}

// HostMemoryFiles lists the hosts' own memory files: Claude's automatic memory
// (projects/*/memory/*.md) and Codex's memories (memories/*.md).
func HostMemoryFiles(r Roots) []string {
	var files []string
	if r.Claude != "" {
		m, _ := filepath.Glob(filepath.Join(r.Claude, "projects", "*", "memory", "*.md"))
		files = append(files, m...)
	}
	if r.Codex != "" {
		m, _ := filepath.Glob(filepath.Join(r.Codex, "memories", "*.md"))
		files = append(files, m...)
	}
	sort.Strings(files)
	return files
}
