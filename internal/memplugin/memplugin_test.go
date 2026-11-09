package memplugin

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type hookFile struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
			Async   bool   `json:"async"`
			// Codex 전용: 모델에 내는 글을 토큰 임계값에서 디스크로 넘기지 않게 하는 보호. Claude Code 꼴에는 없다.
			ContextLimit *int `json:"additionalContextLimit"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func mustJSON(t *testing.T, files Files, path string, v any) {
	t.Helper()
	b, ok := files[path]
	if !ok {
		t.Fatalf("%s가 없습니다: %v", path, files.Paths())
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	if !strings.HasSuffix(string(b), "}\n") || strings.Contains(string(b), "\\u00"+"26") {
		t.Fatalf("%s 모양:\n%s", path, b)
	}
}

// 끝 기준: Claude 꼴은 plugin.json(name orbit-mem, 판, 설명), .mcp.json(서버 orbit-mem: orbit-mem mcp),
// hooks/hooks.json(세 훅), skills/memory·proposals다. 절대경로를 박지 않는다.
func TestClaude_꼴(t *testing.T) {
	files, err := Claude("0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude-plugin/plugin.json", ".mcp.json", "hooks/hooks.json", "skills/memory/SKILL.md", "skills/proposals/SKILL.md"}
	if got := files.Paths(); !slices.Equal(got, want) {
		t.Fatalf("파일 %v", got)
	}
	var pj map[string]any
	mustJSON(t, files, ".claude-plugin/plugin.json", &pj)
	if pj["name"] != "orbit-mem" || pj["version"] != "0.4.0" || pj["description"] == "" || pj["hooks"] != nil {
		t.Fatalf("plugin.json %v", pj) // hooks/hooks.json은 Claude가 스스로 읽는다: 다시 적으면 중복 오류
	}
	var mcp struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	mustJSON(t, files, ".mcp.json", &mcp)
	srv := mcp.MCPServers["orbit-mem"]
	if len(mcp.MCPServers) != 1 || srv["command"] != "orbit-mem" || !slices.Equal(toStrings(srv["args"]), []string{"mcp"}) {
		t.Fatalf(".mcp.json %v", mcp)
	}
	checkHooks(t, files, "hooks/hooks.json", "claude", "0.4.0")
	if s := string(files["skills/proposals/SKILL.md"]); !strings.Contains(s, "name: proposals") || !strings.Contains(s, "orbit-mem proposals") || strings.Contains(s, "orb proposals") {
		t.Fatalf("proposals 스킬:\n%s", s)
	}
	if s := string(files["skills/memory/SKILL.md"]); !strings.Contains(s, "name: memory") || strings.Contains(s, "orbit-code") {
		t.Fatalf("memory 스킬:\n%s", s)
	}
	for p, b := range files {
		if strings.Contains(string(b), "/Users/") || strings.Contains(string(b), "{{") {
			t.Errorf("%s에 절대경로나 자리표시자가 있습니다", p)
		}
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func checkHooks(t *testing.T, files Files, path, host, version string) {
	t.Helper()
	var h hookFile
	mustJSON(t, files, path, &h)
	var events []string
	for e := range h.Hooks {
		events = append(events, e)
	}
	slices.Sort(events)
	// 서브에이전트 시작 훅은 두 호스트 모두에 건다(설계 11절: Codex 공식 훅 문서에 SubagentStart가 있다).
	want := []string{"PreToolUse", "SessionEnd", "SessionStart", "SubagentStart", "UserPromptSubmit"}
	if !slices.Equal(events, want) {
		t.Fatalf("%s 훅 %v", path, events)
	}
	// 글자 상한(inject.Max*Runes) × 2 토큰: 한글은 글자당 토큰이 1을 넘을 수 있어 상한을 지키는 글이 잘리지 않게 한다.
	for e, want := range map[string]struct {
		name    string
		timeout int
		limit   int // Codex additionalContextLimit(토큰), 0이면 칸이 없다
	}{"SessionStart": {"session-start", 10, 12000}, "UserPromptSubmit": {"prompt", 5, 5000}, "PreToolUse": {"pre-tool", 5, 5000},
		"SubagentStart": {"subagent-start", 5, 6000}, "SessionEnd": {"session-end", 3, 0}} {
		g := h.Hooks[e]
		if len(g) != 1 || len(g[0].Hooks) != 1 {
			t.Fatalf("%s %s: %+v", path, e, g)
		}
		hk := g[0].Hooks[0]
		call := "orbit-mem hook " + want.name + " --host " + host + " --plugin-version " + version
		if hk.Type != "command" || hk.Timeout != want.timeout || hk.Async || !strings.Contains(hk.Command, call) || !strings.HasPrefix(hk.Command, "if command -v orbit-mem ") {
			t.Errorf("%s %s: %+v", path, e, hk)
		}
		if host == "codex" && want.limit > 0 {
			if hk.ContextLimit == nil || *hk.ContextLimit != want.limit {
				t.Errorf("%s %s: additionalContextLimit %v (want %d)", path, e, hk.ContextLimit, want.limit)
			}
		} else if hk.ContextLimit != nil {
			t.Errorf("%s %s: additionalContextLimit %d (이 호스트·사건에는 칸이 없다)", path, e, *hk.ContextLimit)
		}
	}
	if h.Hooks["SessionStart"][0].Matcher != "startup|resume|clear|compact|fork" || h.Hooks["UserPromptSubmit"][0].Matcher != "" || h.Hooks["PreToolUse"][0].Matcher != "" {
		t.Errorf("%s matcher %+v", path, h.Hooks)
	}
}

// 끝 기준: Codex 꼴은 하네스 빌드와 같은 꼴이다: .codex-plugin/plugin.json(skills·hooks·mcpServers·interface),
// codex/mcp.json(env_vars ORBIT_HOME), hooks/codex.json(--host codex).
func TestCodex_꼴(t *testing.T) {
	files, err := Codex("0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".codex-plugin/plugin.json", "codex/mcp.json", "hooks/codex.json", "skills/memory/SKILL.md", "skills/proposals/SKILL.md"}
	if got := files.Paths(); !slices.Equal(got, want) {
		t.Fatalf("파일 %v", got)
	}
	var pj struct {
		Name, Version, Description, Skills, Hooks, MCPServers string
		Interface                                             map[string]string
	}
	mustJSON(t, files, ".codex-plugin/plugin.json", &pj)
	if pj.Name != "orbit-mem" || pj.Version != "0.4.0" || pj.Skills != "./skills/" || pj.Hooks != "./hooks/codex.json" || pj.MCPServers != "./codex/mcp.json" ||
		pj.Interface["displayName"] != "orbit-mem" || pj.Interface["category"] != "Productivity" || pj.Interface["shortDescription"] != pj.Description {
		t.Fatalf("codex plugin.json %+v", pj)
	}
	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			EnvVars []string `json:"env_vars"`
		} `json:"mcpServers"`
	}
	mustJSON(t, files, "codex/mcp.json", &mcp)
	if s := mcp.MCPServers["orbit-mem"]; s.Command != "orbit-mem" || !slices.Equal(s.Args, []string{"mcp"}) || !slices.Equal(s.EnvVars, []string{"ORBIT_HOME"}) {
		t.Fatalf("codex mcp %+v", mcp)
	}
	checkHooks(t, files, "hooks/codex.json", "codex", "0.4.0")
}

// 끝 기준: 저장소 꼴은 루트 마켓(.claude-plugin/marketplace.json, source ./plugin)과 plugin/의 Claude 꼴뿐이다.
// 로컬 꼴은 여기에 Codex 꼴과 Codex 로컬 마켓(.agents/plugins/marketplace.json)을 더한다.
func TestRepo와_Local_마켓(t *testing.T) {
	repo, err := Repo("0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name    string
		Owner   map[string]string
		Plugins []struct{ Name, Source, Version, Description string }
	}
	mustJSON(t, repo, ".claude-plugin/marketplace.json", &m)
	if m.Name != "orbit-mem" || m.Owner["name"] == "" || len(m.Plugins) != 1 || m.Plugins[0].Name != "orbit-mem" || m.Plugins[0].Source != "./plugin" || m.Plugins[0].Version != "0.4.0" {
		t.Fatalf("marketplace %+v", m)
	}
	for _, p := range repo.Paths() {
		if p != ".claude-plugin/marketplace.json" && !strings.HasPrefix(p, "plugin/") {
			t.Errorf("저장소 꼴에 %s", p)
		}
		if strings.Contains(p, "codex") {
			t.Errorf("저장소 꼴에 Codex 파일 %s", p)
		}
	}
	local, err := Local("0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range repo.Paths() {
		if string(local[p]) != string(repo[p]) {
			t.Errorf("로컬 꼴의 %s가 저장소 꼴과 다릅니다", p)
		}
	}
	var cm struct {
		Name    string
		Plugins []struct {
			Name   string
			Source struct{ Source, Path string }
			Policy struct{ Installation, Authentication string }
		}
	}
	mustJSON(t, local, ".agents/plugins/marketplace.json", &cm)
	if cm.Name != "orbit-mem" || len(cm.Plugins) != 1 || cm.Plugins[0].Name != "orbit-mem" || cm.Plugins[0].Source.Source != "local" || cm.Plugins[0].Source.Path != "./plugin" ||
		cm.Plugins[0].Policy.Installation != "AVAILABLE" {
		t.Fatalf("codex marketplace %+v", cm)
	}
	for _, p := range []string{"plugin/.codex-plugin/plugin.json", "plugin/hooks/codex.json", "plugin/codex/mcp.json", "plugin/hooks/hooks.json"} {
		if _, ok := local[p]; !ok {
			t.Errorf("로컬 꼴에 %s가 없습니다", p)
		}
	}
}

// 끝 기준: Write는 바뀐 파일만 쓰고 plugin/ 안의 남은 파일을 지운다. Diff는 다시 만들 파일과 남은 파일을 알린다.
func TestWrite와_Diff(t *testing.T) {
	dir := t.TempDir()
	files, _ := Repo("0.4.0")
	if d := Diff(dir, files); len(d) != len(files) {
		t.Fatalf("빈 폴더 차이 %v", d)
	}
	os.MkdirAll(filepath.Join(dir, "plugin", "skills", "old"), 0o755)
	os.WriteFile(filepath.Join(dir, "plugin", "skills", "old", "SKILL.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("keep"), 0o644)
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	if d := Diff(dir, files); len(d) != 0 {
		t.Fatalf("쓴 뒤 차이 %v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin", "skills", "old", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("남은 파일을 지우지 않았습니다")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) != "keep" {
		t.Fatal("plugin/ 밖을 건드렸습니다")
	}
	os.WriteFile(filepath.Join(dir, "plugin", "hooks", "extra.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "plugin", ".mcp.json"), []byte("{}"), 0o644)
	d := Diff(dir, files)
	if len(d) != 2 || !strings.Contains(strings.Join(d, "\n"), "plugin/hooks/extra.json") || !strings.Contains(strings.Join(d, "\n"), "plugin/.mcp.json") {
		t.Fatalf("차이 %v", d)
	}
}

// 끝 기준: 훅 명령은 sh에서 PATH의 orbit-mem을 부른다. 없으면 시작 훅만 설치 안내 JSON을 내고 모두 종료 0,
// 있으면 그 orbit-mem을 인자와 함께 부르고, 그 orbit-mem이 실패해도 안내가 섞이지 않는다.
func TestHook_sh_꼴(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh 꼴")
	}
	files, err := Claude("0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	var h hookFile
	mustJSON(t, files, "hooks/hooks.json", &h)
	start := h.Hooks["SessionStart"][0].Hooks[0].Command
	prompt := h.Hooks["UserPromptSubmit"][0].Hooks[0].Command
	end := h.Hooks["SessionEnd"][0].Hooks[0].Command
	sh := func(pathDir, cmd string) (string, int) {
		c := exec.Command("/bin/sh", "-c", cmd)
		c.Env = []string{"PATH=" + pathDir, "HOME=" + t.TempDir()}
		c.Stdin = strings.NewReader(`{"session_id":"s"}`)
		out, err := c.Output()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	empty := t.TempDir()
	out, code := sh(empty, start)
	var v struct {
		HookSpecificOutput struct{ HookEventName, AdditionalContext string }
	}
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.HookSpecificOutput.HookEventName != "SessionStart" ||
		!strings.Contains(v.HookSpecificOutput.AdditionalContext, "orbit-mem install") || !strings.Contains(v.HookSpecificOutput.AdditionalContext, "https://github.com/crispebble/orbit-mem") {
		t.Fatalf("없을 때 시작 훅 %d %q", code, out)
	}
	for _, c := range []string{prompt, end} {
		if out, code := sh(empty, c); code != 0 || out != "" {
			t.Fatalf("없을 때 %q → %d %q", c, code, out)
		}
	}
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "args")
	os.WriteFile(filepath.Join(dir, "orbit-mem"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >>'"+log+"'\nread x\nprintf 'IN=%s\\n' \"$x\"\n"), 0o755)
	if out, code := sh(dir, start); code != 0 || out != "IN={\"session_id\":\"s\"}\n" {
		t.Fatalf("있을 때 %d %q", code, out)
	}
	sh(dir, prompt)
	got, _ := os.ReadFile(log)
	if string(got) != "hook session-start --host claude --plugin-version 0.4.0\nhook prompt --host claude --plugin-version 0.4.0\n" {
		t.Fatalf("인자 %q", got)
	}
	os.WriteFile(filepath.Join(dir, "orbit-mem"), []byte("#!/bin/sh\nexit 3\n"), 0o755)
	if out, code := sh(dir, start); out != "" || code != 3 {
		t.Fatalf("실패한 orbit-mem 뒤에 안내가 섞였습니다: %d %q", code, out)
	}
}

// 끝 기준: 원본 검사. Codex는 SessionEnd 훅을 3초로 자르므로 더 길면 빌드가 멈춘다. 모르는 칸·사건도 멈춘다.
func TestValidate(t *testing.T) {
	ok := func() Manifest {
		m, err := loadManifest()
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := ok()
	m.Hooks["SessionEnd"][0].Timeout = 4
	if err := m.validate("0.4.0"); err == nil || !strings.Contains(err.Error(), "SessionEnd") {
		t.Fatalf("Codex SessionEnd 4초: %v", err)
	}
	m = ok()
	m.Hooks["Bogus"] = m.Hooks["SessionEnd"]
	if err := m.validate("0.4.0"); err == nil {
		t.Fatal("모르는 사건")
	}
	m = ok()
	m.Hooks["SessionStart"][0].Missing = "it's"
	if err := m.validate("0.4.0"); err == nil {
		t.Fatal("작은따옴표는 sh 꼴을 깬다")
	}
	if err := ok().validate("0.4"); err == nil {
		t.Fatal("판 꼴")
	}
	if _, err := parseManifest([]byte(`{"name":"orbit-mem","bogus":1}`)); err == nil {
		t.Fatal("모르는 칸")
	}
}

// 설계 11절: 서브에이전트 시작 훅은 두 호스트 꼴 모두에 있고, 각자 --host를 단다.
func TestSubagentStart_훅은_두_호스트_꼴에_있다(t *testing.T) {
	for host, build := range map[string]func(string) (Files, error){"claude": Claude, "codex": Codex} {
		files, err := build("0.4.0")
		if err != nil {
			t.Fatal(err)
		}
		path := map[string]string{"claude": "hooks/hooks.json", "codex": "hooks/codex.json"}[host]
		var hf hookFile
		mustJSON(t, files, path, &hf)
		groups := hf.Hooks["SubagentStart"]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 || !strings.Contains(groups[0].Hooks[0].Command, "orbit-mem hook subagent-start --host "+host) {
			t.Fatalf("%s SubagentStart %+v", host, groups)
		}
	}
}

// Codex 전용 글 상한 칸: 양의 정수이고, 글을 받는 사건(SessionStart·UserPromptSubmit·PreToolUse·PostToolUse·SubagentStart)에만 쓴다.
func TestValidate_codexContextLimit(t *testing.T) {
	load := func() Manifest {
		m, err := loadManifest()
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := load()
	m.Hooks["SessionEnd"][0].CodexContextLimit = 100
	if err := m.validate("0.4.0"); err == nil || !strings.Contains(err.Error(), "SessionEnd") {
		t.Fatalf("글을 받지 않는 사건에 칸: %v", err)
	}
	m = load()
	m.Hooks["Stop"] = []Hook{{Hook: "stop", CodexContextLimit: 100}}
	if err := m.validate("0.4.0"); err == nil {
		t.Fatal("Stop에 칸")
	}
	m = load()
	m.Hooks["SubagentStart"][0].CodexContextLimit = -1
	if err := m.validate("0.4.0"); err == nil {
		t.Fatal("음수")
	}
	m = load()
	m.Hooks["PostToolUse"] = []Hook{{Hook: "post-tool", CodexContextLimit: 100}}
	if err := m.validate("0.4.0"); err != nil {
		t.Fatalf("PostToolUse는 글을 받는 사건: %v", err)
	}
}
