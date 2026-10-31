package orbitplugin

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/onidot-labs/openorbit/internal/pluginbuild"
)

// WriteRoles writes the role sources orbit-pi reads (ORBIT_ROLES_DIR, 설계 · OpenOrbit 4차
// R4-5) into dir: agents/<id>.md, agents-common.md and the field-knowledge packs
// knowledge/ the roles point at. Template variables are filled as the Claude Code
// build fills them, with {{knowledgeDir}} at dir/knowledge. A file whose content
// is already the same is not written again; role files no longer in the source
// are removed from dir/agents.
func WriteRoles(dir string) error {
	var m struct {
		Version string `json:"version"`
	}
	b, err := source.ReadFile("source/plugin.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	knowledge := filepath.ToSlash(filepath.Join(abs, "knowledge"))
	keep := map[string]bool{}
	put := func(rel string, content []byte) error {
		target := filepath.Join(abs, filepath.FromSlash(rel))
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, content) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	}
	if b, err := source.ReadFile("source/agents-common.md"); err == nil {
		if err := put("agents-common.md", []byte(pluginbuild.Subst(string(b), m.Version, knowledge, nil))); err != nil {
			return err
		}
	}
	for _, root := range []string{"source/agents", "source/knowledge"} {
		err := fs.WalkDir(source, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := source.ReadFile(p)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(p, "source/")
			if path.Dir(rel) == "agents" {
				keep[path.Base(rel)] = true
			}
			if strings.HasSuffix(p, ".md") {
				b = []byte(pluginbuild.Subst(string(b), m.Version, knowledge, nil))
			}
			return put(rel, b)
		})
		if err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(filepath.Join(abs, "agents"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && !keep[e.Name()] {
			if err := os.Remove(filepath.Join(abs, "agents", e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
