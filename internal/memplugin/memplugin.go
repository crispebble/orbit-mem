// Package memplugin is orbit-mem's own plugin for Claude Code and Codex: the memory MCP
// server (orbit-mem mcp), the three memory hooks (session start, prompt, session end) and
// the memory and proposals skills. The source is embedded (source/); Go builds both host
// formats from it, so no Node is needed:
//
//   - Repo: the GitHub marketplace this repository is (.claude-plugin/marketplace.json and
//     plugin/ in the Claude format), committed and checked against the source by a test.
//   - Local: the folder orbit-mem install writes to $ORBIT_HOME/plugins/orbit-mem, with the
//     Codex format and Codex's local marketplace (.agents/plugins/marketplace.json) too.
//
// Hooks and the MCP server call orbit-mem on PATH, never an absolute path, so the same
// plugin works on every PC. The orbit-code harness plugin is built by orbit-code (orb); the
// two plugins do not know each other.
package memplugin

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/crispebble/orbit-mem/internal/inject"
)

//go:embed all:source
var sourceFS embed.FS

// Names of the plugin, its marketplace (the same in both hosts) and the GitHub repository.
const (
	Name       = "orbit-mem"
	Market     = "orbit-mem"
	ID         = Name + "@" + Market
	GitHubRepo = "crispebble/orbit-mem"
	// PluginDir is the plugin folder inside a marketplace (Repo and Local).
	PluginDir = "plugin"
)

// MCPServer is a stdio MCP server the plugin registers. EnvVars are passed through on Codex,
// which gives a server few variables (Claude Code passes its whole environment).
type MCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	EnvVars []string `json:"envVars,omitempty"`
}

// Hook is one orbit-mem hook: orbit-mem hook <Hook> --host <host> --plugin-version <v>.
// Missing is the context a session-start hook gives when orbit-mem is not on PATH; other
// hooks stay silent then.
type Hook struct {
	Hook string `json:"hook"`
	// Hosts limits the hook to these hosts (claude, codex); empty is both.
	Hosts   []string `json:"hosts,omitempty"`
	Matcher string   `json:"matcher,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
	Missing string   `json:"missing,omitempty"`
	// CodexContextLimit is Codex's additionalContextLimit for this hook (tokens, > 0): the text the hook
	// hands the model is cut to a preview at that size and the whole goes to disk. orbit-mem keeps to its
	// own character limits (inject.Max*Runes), so this only guards against a cut. Codex only; Claude Code
	// has a fixed 10,000-character limit and gets no field.
	CodexContextLimit int `json:"codexContextLimit,omitempty"`
}

// Manifest is source/plugin.json. The version is orbit-mem's own (internal/version).
type Manifest struct {
	Name        string               `json:"name"`
	DisplayName string               `json:"displayName"`
	Description string               `json:"description"`
	Author      string               `json:"author"`
	Repository  string               `json:"repository"`
	Skills      []string             `json:"skills"`
	MCPServers  map[string]MCPServer `json:"mcpServers"`
	Hooks       map[string][]Hook    `json:"hooks"`
}

func parseManifest(b []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("plugin.json: %w", err)
	}
	return m, nil
}

func loadManifest() (Manifest, error) {
	b, err := sourceFS.ReadFile("source/plugin.json")
	if err != nil {
		return Manifest{}, err
	}
	return parseManifest(b)
}

var (
	nameRE    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	controlRE = regexp.MustCompile("[\x00-\x08\x0b-\x1f\x7f]")
	// hookEvents are the events both hosts know (Claude Code and Codex hooks.json).
	hookEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd", "PreToolUse", "PostToolUse", "SubagentStart"}
)

// contextEvents are the events whose output is model context: the only ones Codex applies
// additionalContextLimit to (it ignores the field on others and warns).
var contextEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "SubagentStart"}

// codexSessionEndMax: Codex cuts a SessionEnd hook's timeout to 3 seconds and warns when it
// loads the hook (the same check as the orbit-code harness build).
const codexSessionEndMax = 3

func (m Manifest) validate(version string) error {
	fail := func(f string, a ...any) error { return fmt.Errorf("plugin.json: "+f, a...) }
	if !nameRE.MatchString(m.Name) {
		return fail("name %q", m.Name)
	}
	if !versionRE.MatchString(version) {
		return fail("version %q (x.y.z)", version)
	}
	if strings.TrimSpace(m.Description) == "" || controlRE.MatchString(m.Description) || strings.ContainsAny(m.Description, "\n\r") {
		return fail("description")
	}
	seen := map[string]bool{}
	for _, s := range m.Skills {
		if !nameRE.MatchString(s) || seen[s] {
			return fail("skill %q", s)
		}
		seen[s] = true
	}
	for name, s := range m.MCPServers {
		if !nameRE.MatchString(name) || s.Command == "" {
			return fail("mcpServers.%s", name)
		}
	}
	for ev, hs := range m.Hooks {
		known := false
		for _, e := range hookEvents {
			known = known || e == ev
		}
		if !known {
			return fail("unknown hook event %s", ev)
		}
		for _, h := range hs {
			if !nameRE.MatchString(h.Hook) || h.Timeout < 0 || h.Timeout > 600 || strings.ContainsAny(h.Matcher, "'\n") {
				return fail("hooks.%s", ev)
			}
			for _, host := range h.Hosts {
				if host != "claude" && host != "codex" {
					return fail("hooks.%s: host %q", ev, host)
				}
			}
			if ev == "SessionEnd" && h.Timeout > codexSessionEndMax {
				return fail("hooks.%s: Codex cuts a SessionEnd hook to %d seconds, not %d", ev, codexSessionEndMax, h.Timeout)
			}
			if h.CodexContextLimit < 0 || (h.CodexContextLimit > 0 && !slices.Contains(contextEvents, ev)) {
				return fail("hooks.%s: codexContextLimit %d (a positive number, only for %s)", ev, h.CodexContextLimit, strings.Join(contextEvents, ", "))
			}
			// Missing goes into a single-quoted sh word.
			if h.Missing != "" && (ev != "SessionStart" || strings.ContainsAny(h.Missing, "'\n") || controlRE.MatchString(h.Missing)) {
				return fail("hooks.%s: missing", ev)
			}
		}
	}
	return nil
}

// Files are generated files by slash path relative to an output folder.
type Files map[string][]byte

// Paths are the file paths in byte order.
func (f Files) Paths() []string {
	out := make([]string, 0, len(f))
	for p := range f {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (f Files) under(dir string, g Files) {
	for p, b := range g {
		f[dir+"/"+p] = b
	}
}

// encode is indented JSON with sorted keys, no HTML escaping and a final newline.
func encode(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err) // maps of strings, numbers and slices only
	}
	return b.Bytes()
}

// hookCommand is the sh line a host runs: orbit-mem on PATH with its arguments, else the
// missing-binary context (session start) or nothing, exit 0 either way. An if, not && ||,
// so a failing orbit-mem never adds the notice after its own output.
func hookCommand(event string, h Hook, host, version string) string {
	call := fmt.Sprintf("orbit-mem hook %s --host %s --plugin-version %s", h.Hook, host, version)
	line := "if command -v orbit-mem >/dev/null 2>&1; then " + call + ";"
	if h.Missing != "" {
		line += " else printf '%s\\n' '" + inject.HookOutput(event, h.Missing) + "';"
	}
	return line + " fi"
}

func (m Manifest) hookFile(host, version string) []byte {
	events := map[string]any{}
	for ev, hs := range m.Hooks {
		var groups []any
		for _, h := range hs {
			if len(h.Hosts) > 0 && !slices.Contains(h.Hosts, host) {
				continue
			}
			hook := map[string]any{"type": "command", "command": hookCommand(ev, h, host, version)}
			if h.Timeout > 0 {
				hook["timeout"] = h.Timeout
			}
			if host == "codex" && h.CodexContextLimit > 0 {
				hook["additionalContextLimit"] = h.CodexContextLimit
			}
			g := map[string]any{"hooks": []any{hook}}
			if h.Matcher != "" {
				g["matcher"] = h.Matcher
			}
			groups = append(groups, g)
		}
		if len(groups) > 0 {
			events[ev] = groups
		}
	}
	return encode(map[string]any{"hooks": events})
}

func (m Manifest) skills() (Files, error) {
	out := Files{}
	for _, s := range m.Skills {
		dir := "source/skills/" + s
		body, err := sourceFS.ReadFile(dir + "/SKILL.md")
		if err != nil {
			return nil, err
		}
		if head, _, _ := strings.Cut(string(body), "\n---"); !strings.Contains(head, "name: "+s) || controlRE.Match(body) {
			return nil, fmt.Errorf("skills/%s/SKILL.md: needs name: %s in the front matter and no control characters", s, s)
		}
		out["skills/"+s+"/SKILL.md"] = body
		refs, err := fs.ReadDir(sourceFS, dir+"/references")
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		for _, r := range refs {
			if r.IsDir() || !strings.HasSuffix(r.Name(), ".md") {
				continue
			}
			b, err := sourceFS.ReadFile(dir + "/references/" + r.Name())
			if err != nil {
				return nil, err
			}
			out["skills/"+s+"/references/"+r.Name()] = b
		}
	}
	return out, nil
}

func load(version string) (Manifest, Files, error) {
	m, err := loadManifest()
	if err != nil {
		return m, nil, err
	}
	if err := m.validate(version); err != nil {
		return m, nil, err
	}
	skills, err := m.skills()
	return m, skills, err
}

func (m Manifest) common(version string) map[string]any {
	return map[string]any{"name": m.Name, "version": version, "description": m.Description, "author": map[string]string{"name": m.Author}}
}

// Claude is the Claude Code plugin (paths relative to the plugin folder). Claude Code reads
// .mcp.json and hooks/hooks.json at the plugin root by itself, and skills/ too.
func Claude(version string) (Files, error) {
	m, out, err := load(version)
	if err != nil {
		return nil, err
	}
	pj := m.common(version)
	if m.Repository != "" {
		pj["repository"] = m.Repository
	}
	out[".claude-plugin/plugin.json"] = encode(pj)
	if len(m.MCPServers) > 0 {
		servers := map[string]any{}
		for name, s := range m.MCPServers {
			srv := map[string]any{"command": s.Command}
			if len(s.Args) > 0 {
				srv["args"] = s.Args
			}
			servers[name] = srv
		}
		out[".mcp.json"] = encode(map[string]any{"mcpServers": servers})
	}
	if len(m.Hooks) > 0 {
		out["hooks/hooks.json"] = m.hookFile("claude", version)
	}
	return out, nil
}

// Codex is the Codex plugin, the same format the orbit-code harness build writes:
// .codex-plugin/plugin.json points at skills/, hooks/codex.json and codex/mcp.json.
func Codex(version string) (Files, error) {
	m, out, err := load(version)
	if err != nil {
		return nil, err
	}
	pj := m.common(version)
	if len(m.Skills) > 0 {
		pj["skills"] = "./skills/"
	}
	if len(m.MCPServers) > 0 {
		servers := map[string]any{}
		for name, s := range m.MCPServers {
			srv := map[string]any{"command": s.Command}
			if len(s.Args) > 0 {
				srv["args"] = s.Args
			}
			if len(s.EnvVars) > 0 {
				srv["env_vars"] = s.EnvVars
			}
			servers[name] = srv
		}
		out["codex/mcp.json"] = encode(map[string]any{"mcpServers": servers})
		pj["mcpServers"] = "./codex/mcp.json"
	}
	if len(m.Hooks) > 0 {
		out["hooks/codex.json"] = m.hookFile("codex", version)
		pj["hooks"] = "./hooks/codex.json"
	}
	display := m.DisplayName
	if display == "" {
		display = m.Name
	}
	pj["interface"] = map[string]string{"displayName": display, "shortDescription": m.Description, "developerName": m.Author, "category": "Productivity"}
	out[".codex-plugin/plugin.json"] = encode(pj)
	return out, nil
}

func claudeMarket(m Manifest, version string) []byte {
	return encode(map[string]any{
		"name":     Market,
		"owner":    map[string]string{"name": m.Author},
		"metadata": map[string]string{"description": m.Description},
		"plugins":  []any{map[string]string{"name": m.Name, "source": "./" + PluginDir, "version": version, "description": m.Description}},
	})
}

// Repo is the GitHub marketplace this repository is: .claude-plugin/marketplace.json and the
// Claude plugin in plugin/. Codex is not in it; orbit-mem install builds Codex's locally.
func Repo(version string) (Files, error) {
	m, err := loadManifest()
	if err != nil {
		return nil, err
	}
	c, err := Claude(version)
	if err != nil {
		return nil, err
	}
	out := Files{".claude-plugin/marketplace.json": claudeMarket(m, version)}
	out.under(PluginDir, c)
	return out, nil
}

// Local is the local marketplace folder for both hosts: Repo plus the Codex plugin in the
// same plugin/ folder and Codex's marketplace (.agents/plugins/marketplace.json).
func Local(version string) (Files, error) {
	out, err := Repo(version)
	if err != nil {
		return nil, err
	}
	m, _ := loadManifest()
	c, err := Codex(version)
	if err != nil {
		return nil, err
	}
	out.under(PluginDir, c)
	display := m.DisplayName
	if display == "" {
		display = m.Name
	}
	out[".agents/plugins/marketplace.json"] = encode(map[string]any{
		"name":      Market,
		"interface": map[string]string{"displayName": display},
		"plugins": []any{map[string]any{
			"name":     m.Name,
			"source":   map[string]string{"source": "local", "path": "./" + PluginDir},
			"policy":   map[string]string{"installation": "AVAILABLE", "authentication": "ON_USE"},
			"category": "Productivity",
		}},
	})
	return out, nil
}

// leftovers are files under dir/plugin that are not in files.
func leftovers(dir string, files Files) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(dir, PluginDir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if rel = filepath.ToSlash(rel); files[rel] == nil {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Diff says what Write would change in dir: files to write again and leftovers in plugin/.
func Diff(dir string, files Files) []string {
	var out []string
	for _, p := range files.Paths() {
		if cur, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p))); err != nil || !bytes.Equal(cur, files[p]) {
			out = append(out, "rebuild: "+p)
		}
	}
	for _, p := range leftovers(dir, files) {
		out = append(out, "leftover: "+p)
	}
	return out
}

// Write writes files into dir: only changed files, and removes leftovers in plugin/ (the
// folder is generated; nothing else in dir is touched).
func Write(dir string, files Files) error {
	for _, p := range leftovers(dir, files) {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			return err
		}
	}
	for _, p := range files.Paths() {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if cur, err := os.ReadFile(full); err == nil && bytes.Equal(cur, files[p]) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, files[p], 0o644); err != nil {
			return err
		}
	}
	return removeEmptyDirs(filepath.Join(dir, PluginDir))
}

func removeEmptyDirs(root string) error {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		if entries, err := os.ReadDir(dirs[i]); err == nil && len(entries) == 0 {
			if err := os.Remove(dirs[i]); err != nil {
				return err
			}
		}
	}
	return nil
}
