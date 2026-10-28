// Package pluginbuild turns one plugin source into the Claude Code and Codex
// plugin formats and their marketplaces (oni plugin build).
//
// Source layout:
//
//	plugin.json                     manifest (see Manifest)
//	agents-common.md                optional; appended to every agent body
//	skills/<name>/SKILL.md          + optional skills/<name>/references/*.md
//	agents/<id>.md                  agent body without frontmatter
//
// Output layout (under Out):
//
//	.claude-plugin/marketplace.json
//	.agents/plugins/marketplace.json
//	plugins/<name>/.claude-plugin/plugin.json, .codex-plugin/plugin.json,
//	  skills/, agents/ (Claude), codex/agents/<name>-<id>.toml (Codex),
//	  .mcp.json and hooks/{claude,codex}.json when declared
package pluginbuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Manifest is the source plugin.json.
type Manifest struct {
	Name         string                `json:"name"`
	Version      string                `json:"version"`
	Description  string                `json:"description"`
	DisplayName  string                `json:"displayName,omitempty"`
	Skills       []string              `json:"skills"`
	AgentsCommon string                `json:"agentsCommon,omitempty"`
	Agents       []Agent               `json:"agents,omitempty"`
	MCPServers   map[string]MCPServer  `json:"mcpServers,omitempty"`
	Hooks        map[string][]HookSpec `json:"hooks,omitempty"`
}

// Agent is one role. Unknown keys are rejected so a typo cannot drop a permission.
type Agent struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Claude      ClaudeAgent `json:"claude"`
	Codex       CodexAgent  `json:"codex"`
}

// ClaudeAgent is the Claude Code side of a role.
type ClaudeAgent struct {
	Model           string   `json:"model"`
	Effort          string   `json:"effort"`
	MaxTurns        int      `json:"maxTurns,omitempty"`
	Tools           []string `json:"tools,omitempty"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	Skills          []string `json:"skills,omitempty"`
}

// CodexAgent is the Codex side of a role. Codex inherits the parent model.
type CodexAgent struct {
	ReasoningEffort string `json:"reasoningEffort"`
	SandboxMode     string `json:"sandboxMode"`
}

// MCPServer is a stdio MCP server the plugin registers.
type MCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// HookSpec is one hook. {{host}} in Command becomes claude or codex.
// Hosts limits it to some hosts (비우면 둘 다); Async runs it without making
// the host wait (both hosts accept "async" on command hooks).
type HookSpec struct {
	Hosts   []string `json:"hosts,omitempty"`
	Matcher string   `json:"matcher,omitempty"`
	Command string   `json:"command"`
	Timeout int      `json:"timeout,omitempty"`
	Async   bool     `json:"async,omitempty"`
}

// Options are the build inputs.
type Options struct {
	Source      string            // 원본 디렉터리(plugin.json이 있는 곳)
	Out         string            // 출력 루트(마켓 파일과 plugins/<name>)
	Marketplace string            // 마켓 이름. 비우면 플러그인 이름
	Vars        map[string]string // {{키}} 치환(훅·MCP·본문)
	Check       bool              // 쓰지 않고 어긋남만 알린다
}

// Result lists what was written, or in check mode what differs.
type Result struct {
	Written []string
	Removed []string
	Drift   []string
}

var (
	nameRE     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	toolRE     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_:*().-]*$`)
	versionRE  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	controlRE  = regexp.MustCompile("[\x00-\x08\x0b-\x1f\x7f]")
	models     = []string{"opus", "sonnet", "haiku", "inherit"}
	efforts    = []string{"low", "medium", "high", "xhigh", "max"}
	codexEff   = []string{"low", "medium", "high", "xhigh"}
	sandboxes  = []string{"read-only", "workspace-write"}
	writeTools = []string{"Write", "Edit", "NotebookEdit"}
	hookEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "SubagentStart", "SubagentStop"}
	hookHosts  = []string{"claude", "codex"}

	codexSessionEndMax = 3
)

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func strictDecode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

func (m *Manifest) validate() error {
	if !nameRE.MatchString(m.Name) {
		return fmt.Errorf("name %q", m.Name)
	}
	if !versionRE.MatchString(m.Version) {
		return fmt.Errorf("version %q는 x.y.z 형식이어야 합니다", m.Version)
	}
	if strings.TrimSpace(m.Description) == "" || controlRE.MatchString(m.Description) {
		return errors.New("description")
	}
	seen := map[string]bool{}
	for _, s := range m.Skills {
		if !nameRE.MatchString(s) || seen[s] {
			return fmt.Errorf("skill %q", s)
		}
		seen[s] = true
	}
	ids := map[string]bool{}
	for _, a := range m.Agents {
		fail := func(why string) error { return fmt.Errorf("agent %s: %s", a.ID, why) }
		if !nameRE.MatchString(a.ID) || ids[a.ID] {
			return fail("id")
		}
		ids[a.ID] = true
		if strings.TrimSpace(a.Description) == "" || controlRE.MatchString(a.Description) || strings.ContainsAny(a.Description, "\n\r") {
			return fail("description")
		}
		c := a.Claude
		if !has(models, c.Model) {
			return fail("claude.model " + c.Model)
		}
		if !has(efforts, c.Effort) {
			return fail("claude.effort " + c.Effort)
		}
		if c.MaxTurns < 0 || c.MaxTurns > 1000 {
			return fail("claude.maxTurns")
		}
		if (c.Tools == nil) == (c.DisallowedTools == nil) {
			return fail("claude.tools 또는 claude.disallowedTools 중 하나만 씁니다")
		}
		for _, t := range append(append([]string{}, c.Tools...), c.DisallowedTools...) {
			if !toolRE.MatchString(t) {
				return fail("tool " + t)
			}
		}
		for _, s := range c.Skills {
			if !seen[s] {
				return fail("모르는 스킬 " + s)
			}
		}
		if !has(codexEff, a.Codex.ReasoningEffort) || !has(sandboxes, a.Codex.SandboxMode) {
			return fail("codex 설정")
		}
		if a.Codex.SandboxMode == "read-only" {
			for _, w := range writeTools {
				if has(c.Tools, w) || (c.DisallowedTools != nil && !has(c.DisallowedTools, w)) {
					return fail("읽기 전용 역할이 Claude에서 " + w + "를 쓸 수 있습니다")
				}
			}
		}
	}
	for name, s := range m.MCPServers {
		if !nameRE.MatchString(name) || s.Command == "" {
			return fmt.Errorf("mcpServers.%s", name)
		}
	}
	for ev, specs := range m.Hooks {
		if !has(hookEvents, ev) {
			return fmt.Errorf("hooks: 모르는 이벤트 %s", ev)
		}
		for _, h := range specs {
			if h.Command == "" || h.Timeout < 0 || h.Timeout > 600 {
				return fmt.Errorf("hooks.%s", ev)
			}
			for _, host := range h.Hosts {
				if !has(hookHosts, host) {
					return fmt.Errorf("hooks.%s: 모르는 호스트 %s", ev, host)
				}
			}
			// Codex는 SessionEnd 훅 timeout을 3초로 줄이고 훅을 불러올 때 경고한다.
			if ev == "SessionEnd" && h.Timeout > codexSessionEndMax && (len(h.Hosts) == 0 || has(h.Hosts, "codex")) {
				return fmt.Errorf("hooks.SessionEnd: Codex는 timeout을 %d초까지만 받습니다(%d초)", codexSessionEndMax, h.Timeout)
			}
		}
	}
	return nil
}

type builder struct {
	o       Options
	m       Manifest
	outputs map[string][]byte
}

func (b *builder) subst(s string) string {
	s = strings.ReplaceAll(s, "{{version}}", b.m.Version)
	for k, v := range b.o.Vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

func (b *builder) put(rel string, content []byte) { b.outputs[filepath.ToSlash(rel)] = content }

func jsonBytes(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return buf.Bytes()
}

func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(b)
	if strings.Contains(s, "'''") || controlRE.MatchString(s) {
		return "", fmt.Errorf("%s: ''' 또는 제어 문자가 들어 있습니다", path)
	}
	return s, nil
}

// Build generates both formats. In check mode nothing is written.
func Build(o Options) (Result, error) {
	raw, err := os.ReadFile(filepath.Join(o.Source, "plugin.json"))
	if err != nil {
		return Result{}, err
	}
	var m Manifest
	if err := strictDecode(raw, &m); err != nil {
		return Result{}, fmt.Errorf("plugin.json: %w", err)
	}
	if err := m.validate(); err != nil {
		return Result{}, fmt.Errorf("plugin.json: %w", err)
	}
	b := &builder{o: o, m: m, outputs: map[string][]byte{}}
	if err := b.generate(); err != nil {
		return Result{}, err
	}
	return b.flush()
}

func (b *builder) generate() error {
	m, base := b.m, "plugins/"+b.m.Name
	// skills
	for _, s := range m.Skills {
		dir := filepath.Join(b.o.Source, "skills", s)
		body, err := readText(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			return err
		}
		if !strings.Contains(strings.SplitN(body, "\n---", 2)[0], "name: "+s) {
			return fmt.Errorf("skills/%s/SKILL.md frontmatter의 name이 %s가 아닙니다", s, s)
		}
		b.put(base+"/skills/"+s+"/SKILL.md", []byte(b.subst(body)))
		refs, _ := os.ReadDir(filepath.Join(dir, "references"))
		for _, r := range refs {
			if r.IsDir() || !strings.HasSuffix(r.Name(), ".md") {
				continue
			}
			t, err := readText(filepath.Join(dir, "references", r.Name()))
			if err != nil {
				return err
			}
			b.put(base+"/skills/"+s+"/references/"+r.Name(), []byte(b.subst(t)))
		}
	}
	// agents
	common := ""
	if m.AgentsCommon != "" {
		t, err := readText(filepath.Join(b.o.Source, m.AgentsCommon))
		if err != nil {
			return err
		}
		common = strings.TrimSpace(t)
	}
	for _, a := range m.Agents {
		body, err := readText(filepath.Join(b.o.Source, "agents", a.ID+".md"))
		if err != nil {
			return err
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("agents/%s.md가 비었습니다", a.ID)
		}
		text := strings.TrimSpace(body)
		if common != "" {
			text += "\n\n" + common
		}
		text = b.subst(text) + "\n"
		desc, _ := json.Marshal(a.Description) // JSON 문자열은 YAML 큰따옴표 문자열로도 유효하다
		front := []string{"name: " + a.ID, "description: " + string(desc), "model: " + a.Claude.Model, "effort: " + a.Claude.Effort}
		if a.Claude.MaxTurns > 0 {
			front = append(front, fmt.Sprintf("maxTurns: %d", a.Claude.MaxTurns))
		}
		if a.Claude.Tools != nil {
			front = append(front, "tools: "+strings.Join(a.Claude.Tools, ", "))
		}
		if a.Claude.DisallowedTools != nil {
			front = append(front, "disallowedTools: "+strings.Join(a.Claude.DisallowedTools, ", "))
		}
		if len(a.Claude.Skills) > 0 {
			front = append(front, "skills:")
			for _, s := range a.Claude.Skills {
				front = append(front, "  - "+m.Name+":"+s)
			}
		}
		b.put(base+"/agents/"+a.ID+".md", []byte("---\n"+strings.Join(front, "\n")+"\n---\n\n"+text))
		name := m.Name + "-" + a.ID
		q := func(s string) string { j, _ := json.Marshal(s); return string(j) }
		toml := strings.Join([]string{"name = " + q(name), "description = " + q(a.Description),
			"model_reasoning_effort = " + q(a.Codex.ReasoningEffort), "sandbox_mode = " + q(a.Codex.SandboxMode),
			"developer_instructions = '''", text + "'''", ""}, "\n")
		b.put(base+"/codex/agents/"+name+".toml", []byte(toml))
	}
	// mcp
	if len(m.MCPServers) > 0 {
		servers := map[string]MCPServer{}
		for name, s := range m.MCPServers {
			args := make([]string, len(s.Args))
			for i, a := range s.Args {
				args[i] = b.subst(a)
			}
			servers[name] = MCPServer{Command: b.subst(s.Command), Args: args}
		}
		b.put(base+"/.mcp.json", jsonBytes(map[string]any{"mcpServers": servers}))
	}
	// hooks
	hookFile := func(host string) []byte {
		events := map[string]any{}
		for ev, specs := range m.Hooks {
			var groups []any
			for _, h := range specs {
				if len(h.Hosts) > 0 && !has(h.Hosts, host) {
					continue
				}
				hook := map[string]any{"type": "command", "command": strings.ReplaceAll(b.subst(h.Command), "{{host}}", host)}
				if h.Timeout > 0 {
					hook["timeout"] = h.Timeout
				}
				if h.Async {
					hook["async"] = true
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
		return jsonBytes(map[string]any{"hooks": events})
	}
	common2 := map[string]any{"name": m.Name, "version": m.Version, "description": m.Description, "author": map[string]string{"name": "orbit"}}
	claude := map[string]any{}
	codex := map[string]any{}
	for k, v := range common2 {
		claude[k], codex[k] = v, v
	}
	if len(m.Skills) > 0 {
		claude["skills"], codex["skills"] = "./skills/", "./skills/"
	}
	if len(m.Hooks) > 0 {
		b.put(base+"/hooks/claude.json", hookFile("claude"))
		b.put(base+"/hooks/codex.json", hookFile("codex"))
		claude["hooks"], codex["hooks"] = "./hooks/claude.json", "./hooks/codex.json"
	}
	if len(m.MCPServers) > 0 {
		codex["mcpServers"] = "./.mcp.json" // Claude Code는 플러그인 루트의 .mcp.json을 스스로 읽는다
	}
	display := m.DisplayName
	if display == "" {
		display = m.Name
	}
	codex["interface"] = map[string]any{"displayName": display, "shortDescription": m.Description, "developerName": "orbit", "category": "Productivity"}
	b.put(base+"/.claude-plugin/plugin.json", jsonBytes(claude))
	b.put(base+"/.codex-plugin/plugin.json", jsonBytes(codex))
	market := b.o.Marketplace
	if market == "" {
		market = m.Name
	}
	b.put(".claude-plugin/marketplace.json", jsonBytes(map[string]any{"name": market, "owner": map[string]string{"name": "orbit"}, "metadata": map[string]string{"description": m.Description},
		"plugins": []any{map[string]any{"name": m.Name, "source": "./" + base, "version": m.Version, "description": m.Description}}}))
	b.put(".agents/plugins/marketplace.json", jsonBytes(map[string]any{"name": market, "interface": map[string]string{"displayName": display},
		"plugins": []any{map[string]any{"name": m.Name, "source": map[string]string{"source": "local", "path": "./" + base},
			"policy": map[string]string{"installation": "AVAILABLE", "authentication": "ON_USE"}, "category": "Productivity"}}}))
	return nil
}

// managed are the generated-only directories: anything there not generated this run is a leftover.
func (b *builder) managed() []string {
	base := "plugins/" + b.m.Name
	return []string{base + "/agents", base + "/skills", base + "/codex/agents", base + "/hooks"}
}

func (b *builder) flush() (Result, error) {
	var res Result
	var leftovers []string
	for _, dir := range b.managed() {
		root := filepath.Join(b.o.Out, filepath.FromSlash(dir))
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(b.o.Out, p)
			if _, ok := b.outputs[filepath.ToSlash(rel)]; !ok {
				leftovers = append(leftovers, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Strings(leftovers)
	paths := make([]string, 0, len(b.outputs))
	for p := range b.outputs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if b.o.Check {
		for _, p := range paths {
			cur, err := os.ReadFile(filepath.Join(b.o.Out, filepath.FromSlash(p)))
			if err != nil || !bytes.Equal(cur, b.outputs[p]) {
				res.Drift = append(res.Drift, "다시 만들어야 함: "+p)
			}
		}
		for _, p := range leftovers {
			res.Drift = append(res.Drift, "남은 생성물: "+p)
		}
		return res, nil
	}
	for _, p := range leftovers {
		if err := os.Remove(filepath.Join(b.o.Out, filepath.FromSlash(p))); err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, p)
	}
	for _, p := range paths {
		full := filepath.Join(b.o.Out, filepath.FromSlash(p))
		if cur, err := os.ReadFile(full); err == nil && bytes.Equal(cur, b.outputs[p]) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(full, b.outputs[p], 0o644); err != nil {
			return res, err
		}
		res.Written = append(res.Written, p)
	}
	return res, nil
}

// CodexAgents lists the generated Codex agent files of a built plugin directory
// (plugins/<name>), which a Codex install copies into ~/.codex/agents.
func CodexAgents(pluginDir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(pluginDir, "codex", "agents", "*.toml"))
	sort.Strings(matches)
	return matches, err
}
