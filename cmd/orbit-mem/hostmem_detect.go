package main

// 겹치는 기억 판정(설계 · orbit-mem 2판 17.2): hostMemoryOverlaps 하나를 host-memory·doctor·install·세션 시작
// 훅이 함께 쓴다. Claude Code는 설정 우선순위(code.claude.com/docs/en/settings 「Settings precedence」:
// 관리 > 명령줄 > 프로젝트 로컬 > 공유 프로젝트 > 사용자)대로 실제로 적용되는 값과 그 파일을 고른다.
// 명령줄(--settings)과 MDM·서버 관리 설정은 파일로 볼 수 없어 보지 않는다.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/install"
)

// knownMemory is a memory system known to keep memories apart from orbit-mem. Only
// identifiers checked against the project's own files or documents are listed, each with
// where it was checked (2026-11-04). Plugins match by plugin name (the part before @, in any
// marketplace); MCP servers match by a text in their command, args or url, since people
// name the server entry as they like.
type knownMemory struct {
	Name    string
	Plugins []string
	MCP     []string
	Store   string // where it keeps memories, under HOME
}

var knownMemories = []knownMemory{
	// github.com/thedotmack/claude-mem: .claude-plugin/marketplace.json(claude-mem·claude-mem-cowork @thedotmack),
	// docs.claude-mem.ai/configuration(~/.claude-mem).
	{Name: "claude-mem", Plugins: []string{"claude-mem", "claude-mem-cowork"}, Store: ".claude-mem"},
	// github.com/supermemoryai/claude-supermemory: .claude-plugin/marketplace.json(supermemory·옛 claude-supermemory
	// @supermemory-plugins); supermemory.ai/docs/supermemory-mcp/mcp(https://mcp.supermemory.ai/mcp). 기억은 클라우드, 로컬은 설정만.
	{Name: "supermemory", Plugins: []string{"supermemory", "claude-supermemory"}, MCP: []string{"mcp.supermemory.ai"}, Store: ".supermemory-claude"},
	// github.com/mem0ai/mem0: .claude-plugin/marketplace.json(mem0@mem0-plugins); docs.mem0.ai/integrations/claude-code;
	// pypi.org/project/mem0-mcp-server(uvx mem0-mcp-server). 기억은 Mem0 Platform, 로컬은 ~/.mem0.
	{Name: "mem0", Plugins: []string{"mem0"}, MCP: []string{"mem0-mcp-server"}, Store: ".mem0"},
	// github.com/basicmachines-co/basic-memory README: uvx basic-memory mcp, 노트 ~/basic-memory.
	{Name: "basic-memory", MCP: []string{"basic-memory"}, Store: "basic-memory"},
	// github.com/modelcontextprotocol/servers src/memory README: npx -y @modelcontextprotocol/server-memory.
	{Name: "server-memory", MCP: []string{"@modelcontextprotocol/server-memory"}},
}

// memoryPluginOf is the known memory system a plugin id belongs to. orbit-mem itself is never one.
func memoryPluginOf(id string) (knownMemory, bool) {
	name, _, _ := strings.Cut(id, "@")
	if name == "orbit-mem" {
		return knownMemory{}, false
	}
	for _, k := range knownMemories {
		if slices.Contains(k.Plugins, name) {
			return k, true
		}
	}
	return knownMemory{}, false
}

// memoryServerOf is the known memory system an MCP server entry (its raw JSON or TOML) runs.
func memoryServerOf(entry string) (knownMemory, bool) {
	for _, k := range knownMemories {
		for _, m := range k.MCP {
			if strings.Contains(entry, m) {
				return k, true
			}
		}
	}
	return knownMemory{}, false
}

// Overlap kinds and ids (17.2).
const (
	overlapClaude       = "claude-auto-memory"
	overlapCodex        = "codex-memories"
	overlapPluginPrefix = "claude-plugin:"
	overlapMCPPrefix    = "claude-mcp:"
	codexPluginPrefix   = "codex-plugin:"
	codexMCPPrefix      = "codex-mcp:"

	kindAutoMemory = "auto-memory"
	kindPlugin     = "plugin"
	kindMCP        = "mcp"
)

// settingValue is one place that sets a value.
type settingValue struct {
	Scope string `json:"scope"`          // user | project | local | managed | env | default
	Path  string `json:"path,omitempty"` // the file, or the environment variable
	Value string `json:"value"`
}

// hostWarning is a settings file the check could not read.
type hostWarning struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// hostOverlap is one memory system besides orbit-mem on this PC.
type hostOverlap struct {
	ID       string         `json:"id"`
	Host     string         `json:"host"` // claude | codex
	Kind     string         `json:"kind"` // auto-memory | plugin | mcp
	Name     string         `json:"name"` // the memory system
	On       bool           `json:"on"`
	Settings string         `json:"settings"`           // the file host-memory off writes
	Source   *settingValue  `json:"source,omitempty"`   // where the value that applies comes from
	Values   []settingValue `json:"values,omitempty"`   // every file that sets it, lowest precedence first
	LockedBy string         `json:"lockedBy,omitempty"` // a file above the user settings keeps it on: off leaves it
	Manual   bool           `json:"manual,omitempty"`   // orbit-mem does not turn it off: Off is the host's own command
	Off      string         `json:"off"`                // the command that turns it off
	Stores   []memoryStore  `json:"stores,omitempty"`

	table string // Codex: the TOML table that holds enabled
	store []string
}

// memoryStore is where an overlap keeps its memories (host-memory status only: it walks folders).
type memoryStore struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

func offCommand(id string) string { return "orbit-mem host-memory off --only " + id }

const keepCommand = "orbit-mem config set hostMemory keep"

// claudeConfigDir is Claude Code's settings folder: $CLAUDE_CONFIG_DIR, else ~/.claude.
func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func claudeSettingsPath() string { return filepath.Join(claudeConfigDir(), "settings.json") }

// claudeStatePath is ~/.claude.json, where Claude Code keeps MCP servers of the user and
// local scopes (inside the config folder when CLAUDE_CONFIG_DIR is set).
func claudeStatePath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// claudeManagedDir is the system folder of managed-settings.json and managed-settings.d
// (code.claude.com/docs/en/managed-settings 「Where each mechanism stores the policy」).
// Tests replace it.
var claudeManagedDir = func() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	case "windows":
		return `C:\Program Files\ClaudeCode`
	}
	return "/etc/claude-code"
}

// hostCwd is the folder whose project settings count when the caller names none. Tests replace it.
var hostCwd = os.Getwd

// projectRoot is the git repository root above dir, where Claude Code keeps
// .claude/settings.local.json; outside a repository, or when the root is HOME, it is dir.
func projectRoot(dir string) string {
	home, _ := os.UserHomeDir()
	for d := dir; ; d = filepath.Dir(d) {
		if lexists(filepath.Join(d, ".git")) {
			if d == home {
				return dir
			}
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// claudeLayer is one Claude Code settings file, in precedence order (lowest first).
type claudeLayer struct {
	Scope, Path string
	Data        []byte
}

// readJSONObject reads a settings file: missing is nothing, anything but a JSON object is a warning.
func readJSONObject(path string, warns *[]hostWarning) ([]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			*warns = append(*warns, hostWarning{Path: path, Reason: err.Error()})
		}
		return nil, false
	}
	if _, err := parseObject(b); err != nil {
		*warns = append(*warns, hostWarning{Path: path, Reason: err.Error()})
		return nil, false
	}
	return b, true
}

// claudeLayers are the settings files Claude Code reads for a session started in cwd.
func claudeLayers(cwd string, warns *[]hostWarning) []claudeLayer {
	var ls []claudeLayer
	add := func(scope, path string) {
		if slices.ContainsFunc(ls, func(l claudeLayer) bool { return l.Path == path }) {
			return
		}
		if b, ok := readJSONObject(path, warns); ok {
			ls = append(ls, claudeLayer{scope, path, b})
		}
	}
	add("user", claudeSettingsPath())
	if cwd != "" {
		root := projectRoot(cwd)
		add("project", filepath.Join(cwd, ".claude", "settings.json"))
		if root != cwd { // 2.1.211 전 판이 시작 폴더에 남긴 로컬 파일: 루트 값이 앞선다
			add("local", filepath.Join(cwd, ".claude", "settings.local.json"))
		}
		add("local", filepath.Join(root, ".claude", "settings.local.json"))
	}
	dir := claudeManagedDir()
	add("managed", filepath.Join(dir, "managed-settings.json"))
	drops, _ := filepath.Glob(filepath.Join(dir, "managed-settings.d", "*.json"))
	slices.Sort(drops)
	for _, d := range drops {
		if !strings.HasPrefix(filepath.Base(d), ".") {
			add("managed", d)
		}
	}
	return ls
}

// effective is the value at path that applies, the layer it comes from, and every layer that sets it.
func effective(ls []claudeLayer, path ...string) (string, *settingValue, []settingValue) {
	var all []settingValue
	for _, l := range ls {
		if v, ok := jsonGet(l.Data, path); ok {
			all = append(all, settingValue{Scope: l.Scope, Path: l.Path, Value: v})
		}
	}
	if len(all) == 0 {
		return "", nil, nil
	}
	top := all[len(all)-1]
	return top.Value, &top, all
}

// lockedBy is the file above the user settings that keeps a value on, if any.
func lockedBy(on bool, src *settingValue) string {
	if on && src != nil && src.Scope != "user" {
		return src.Path
	}
	return ""
}

// hostMemoryOverlaps lists every memory system it knows of whose host is set up here, on
// or off, for a session started in cwd ("" is the current folder), in a fixed order:
// Claude Code's own memory, its plugins, its MCP servers, then Codex's. Files it could not
// read are warnings; they never stop the check.
func hostMemoryOverlaps(cwd string) ([]hostOverlap, []hostWarning) {
	if cwd == "" {
		cwd, _ = hostCwd()
	}
	var out []hostOverlap
	warns := []hostWarning{}
	if _, err := os.Stat(claudeConfigDir()); err == nil {
		out = append(out, claudeOverlaps(cwd, &warns)...)
	}
	if _, err := os.Stat(filepath.Dir(install.CodexConfigPath())); err == nil {
		out = append(out, codexOverlaps(&warns)...)
	}
	return out, warns
}

func claudeOverlaps(cwd string, warns *[]hostWarning) []hostOverlap {
	var out []hostOverlap
	ls := claudeLayers(cwd, warns)
	auto := hostOverlap{ID: overlapClaude, Host: "claude", Kind: kindAutoMemory, Name: "Claude Code auto memory", Settings: claudeSettingsPath(), Off: offCommand(overlapClaude),
		store: []string{filepath.Join(claudeConfigDir(), "projects", "*", "memory")}}
	v, src, all := effective(ls, "autoMemoryEnabled")
	auto.Values = all
	if env := os.Getenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY"); slices.Contains([]string{"1", "true", "yes"}, strings.ToLower(env)) {
		auto.Source = &settingValue{Scope: "env", Path: "CLAUDE_CODE_DISABLE_AUTO_MEMORY", Value: env}
	} else if src == nil {
		auto.On, auto.Source = true, &settingValue{Scope: "default", Value: "true"}
	} else {
		auto.On, auto.Source = v != "false", src
	}
	if d, s, _ := effective(ls, "autoMemoryDirectory"); s != nil {
		var dir string
		if json.Unmarshal([]byte(d), &dir) == nil && dir != "" {
			auto.store = append(auto.store, expandHome(dir))
		}
	}
	auto.LockedBy = lockedBy(auto.On, auto.Source)
	out = append(out, auto)

	var ids []string
	for _, l := range ls {
		var p struct {
			EnabledPlugins map[string]json.RawMessage `json:"enabledPlugins"`
		}
		_ = json.Unmarshal(l.Data, &p)
		for id := range p.EnabledPlugins {
			if _, ok := memoryPluginOf(id); ok && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		k, _ := memoryPluginOf(id)
		v, src, all := effective(ls, "enabledPlugins", id)
		o := hostOverlap{ID: overlapPluginPrefix + id, Host: "claude", Kind: kindPlugin, Name: k.Name, On: v == "true", Settings: claudeSettingsPath(),
			Source: src, Values: all, Off: offCommand(overlapPluginPrefix + id), store: storeOf(k)}
		o.LockedBy = lockedBy(o.On, src)
		out = append(out, o)
	}
	return append(out, claudeMCPOverlaps(cwd, ls, warns)...)
}

// claudeMCPOverlaps finds memory MCP servers in ~/.claude.json (user scope, and local scope
// under the project's path) and the project's .mcp.json. Claude Code turns one off only per
// project in its /mcp panel and keeps that choice in ~/.claude.json, which it rewrites
// itself, so orbit-mem leaves them to the host: Off is `claude mcp remove`.
func claudeMCPOverlaps(cwd string, ls []claudeLayer, warns *[]hostWarning) []hostOverlap {
	type server struct{ name, scope, file, entry string }
	var servers []server
	root := projectRoot(cwd)
	var disabled []string
	if b, ok := readJSONObject(claudeStatePath(), warns); ok {
		var st struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
			Projects   map[string]struct {
				MCPServers         map[string]json.RawMessage `json:"mcpServers"`
				DisabledMCPServers []string                   `json:"disabledMcpServers"`
			} `json:"projects"`
		}
		_ = json.Unmarshal(b, &st)
		for n, e := range st.MCPServers {
			servers = append(servers, server{n, "user", claudeStatePath(), string(e)})
		}
		for _, key := range []string{cwd, root} {
			if p, ok := st.Projects[key]; ok {
				for n, e := range p.MCPServers {
					servers = append(servers, server{n, "local", claudeStatePath(), string(e)})
				}
				disabled = append(disabled, p.DisabledMCPServers...)
			}
		}
	}
	mcpJSON := filepath.Join(root, ".mcp.json")
	if b, ok := readJSONObject(mcpJSON, warns); ok {
		var p struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		_ = json.Unmarshal(b, &p)
		for n, e := range p.MCPServers {
			servers = append(servers, server{n, "project", mcpJSON, string(e)})
		}
		for _, l := range ls { // .mcp.json 서버 승인 거절 목록(disabledMcpjsonServers)
			var d struct {
				Disabled []string `json:"disabledMcpjsonServers"`
			}
			_ = json.Unmarshal(l.Data, &d)
			disabled = append(disabled, d.Disabled...)
		}
	}
	slices.SortFunc(servers, func(a, b server) int { return strings.Compare(a.scope+"\x00"+a.name, b.scope+"\x00"+b.name) })
	var out []hostOverlap
	for _, s := range servers {
		k, ok := memoryServerOf(s.entry)
		if !ok {
			continue
		}
		id := overlapMCPPrefix + s.name
		if slices.ContainsFunc(out, func(o hostOverlap) bool { return o.ID == id }) {
			continue
		}
		out = append(out, hostOverlap{ID: id, Host: "claude", Kind: kindMCP, Name: k.Name, On: !slices.Contains(disabled, s.name), Settings: s.file,
			Source: &settingValue{Scope: s.scope, Path: s.file, Value: s.name}, Manual: true, Off: "claude mcp remove " + s.name + " --scope " + s.scope, store: storeOf(k)})
	}
	return out
}

// codexMemoryKeys are the config.toml values that turn Codex's memories on ([table, key]).
var codexMemoryKeys = [][2]string{{"features", "memories"}, {"memories", "generate_memories"}, {"memories", "use_memories"}}

// codexMemories says whether Codex's memories are turned on in its config.toml
// (features.memories, memories.generate_memories or memories.use_memories true).
// Codex keeps the feature off unless it is turned on.
func codexMemories(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, k := range codexMemoryKeys {
		if v, _ := tomlGet(string(b), k[0], k[1]); v == "true" {
			return true
		}
	}
	return false
}

// codexOverlaps are Codex's own memories, then known memory plugins ([plugins."<id>"]) and
// MCP servers ([mcp_servers.<name>], learn.chatgpt.com/docs/extend/mcp?surface=cli) in its
// config.toml. A plugin or server is on unless its table says enabled = false.
func codexOverlaps(warns *[]hostWarning) []hostOverlap {
	path := install.CodexConfigPath()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		*warns = append(*warns, hostWarning{Path: path, Reason: err.Error()})
	}
	text := string(b)
	out := []hostOverlap{{ID: overlapCodex, Host: "codex", Kind: kindAutoMemory, Name: "Codex memories", On: codexMemories(path), Settings: path, Off: offCommand(overlapCodex),
		store: []string{filepath.Join(filepath.Dir(path), "memories*")}}}
	for _, t := range tomlTables(text) {
		var k knownMemory
		var id string
		switch {
		case strings.HasPrefix(t.Header, "plugins."):
			pid := unquoteTOML(strings.TrimPrefix(t.Header, "plugins."))
			m, ok := memoryPluginOf(pid)
			if !ok {
				continue
			}
			k, id = m, codexPluginPrefix+pid
		case strings.HasPrefix(t.Header, "mcp_servers."):
			m, ok := memoryServerOf(t.Keys["command"] + " " + t.Keys["args"] + " " + t.Keys["url"])
			if !ok {
				continue
			}
			k, id = m, codexMCPPrefix+unquoteTOML(strings.TrimPrefix(t.Header, "mcp_servers."))
		default:
			continue
		}
		kind := kindPlugin
		if strings.HasPrefix(id, codexMCPPrefix) {
			kind = kindMCP
		}
		on := t.Keys["enabled"] != "false"
		out = append(out, hostOverlap{ID: id, Host: "codex", Kind: kind, Name: k.Name, On: on, Settings: path, Off: offCommand(id),
			Source: &settingValue{Scope: "user", Path: path, Value: "[" + t.Header + "]"}, table: t.Header, store: storeOf(k)})
	}
	return out
}

func storeOf(k knownMemory) []string {
	if k.Store == "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	return []string{filepath.Join(home, k.Store)}
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
}

// storesOf finds where an overlap keeps its memories.
func storesOf(o hostOverlap) []memoryStore {
	var out []memoryStore
	for _, g := range o.store {
		st := memoryStore{Path: g}
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			_ = filepath.WalkDir(m, func(_ string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if info, err := d.Info(); err == nil {
					st.Files++
					st.Bytes += info.Size()
				}
				return nil
			})
		}
		if len(matches) > 0 {
			out = append(out, st)
		}
	}
	return out
}
