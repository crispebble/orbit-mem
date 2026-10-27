// Package orbitplugin holds the Orbit plugin source (MCP, hooks and two
// skills) built into each PC's marketplace with this PC's oni path.
package orbitplugin

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed source
var source embed.FS

// Write copies the plugin source into dir.
func Write(dir string) error {
	return fs.WalkDir(source, "source", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "source"), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := source.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// Vars are the build variables for an oni at path: {{oni}} for MCP commands
// and {{onicmd}} for hook command lines (quoted only when the path has spaces,
// so the same line works in sh, cmd and PowerShell).
func Vars(oni string) map[string]string {
	cmd := strings.ReplaceAll(oni, `\`, "/")
	if strings.ContainsAny(cmd, " \t") {
		cmd = `"` + cmd + `"`
	}
	return map[string]string{"oni": oni, "onicmd": cmd}
}
