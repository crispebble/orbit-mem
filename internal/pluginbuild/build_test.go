package pluginbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const manifest = `{
  "name": "harness", "version": "0.1.0", "description": "하네스",
  "skills": ["tdd"],
  "agentsCommon": "agents-common.md",
  "agents": [
    {"id": "worker", "description": "일반 구현", "claude": {"model": "sonnet", "effort": "medium", "maxTurns": 150, "tools": ["Read","Edit","Write","Bash"], "skills": ["tdd"]}, "codex": {"reasoningEffort": "medium", "sandboxMode": "workspace-write"}},
    {"id": "explorer", "description": "위치 찾기", "claude": {"model": "haiku", "effort": "low", "tools": ["Read","Grep"]}, "codex": {"reasoningEffort": "low", "sandboxMode": "read-only"}}
  ]
}`

func fixture(t *testing.T) string {
	src := t.TempDir()
	write(t, src, map[string]string{
		"plugin.json":                    manifest,
		"agents-common.md":               "## 공통\n- 결과만 반환한다.",
		"skills/tdd/SKILL.md":            "---\nname: tdd\ndescription: 시험 먼저\n---\n\n# tdd {{version}}\n",
		"skills/tdd/references/cycle.md": "RED GREEN",
		"agents/worker.md":               "# 작업자\n일한다.",
		"agents/explorer.md":             "# 탐색\n찾는다.",
	})
	return src
}

func TestBuild_Claude와_Codex_두_형식을_함께_만든다(t *testing.T) {
	src, out := fixture(t), t.TempDir()
	res, err := Build(Options{Source: src, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) == 0 {
		t.Fatal("nothing written")
	}
	p := filepath.Join(out, "plugins", "harness")
	var claude, codex map[string]any
	json.Unmarshal([]byte(read(t, filepath.Join(p, ".claude-plugin", "plugin.json"))), &claude)
	json.Unmarshal([]byte(read(t, filepath.Join(p, ".codex-plugin", "plugin.json"))), &codex)
	if claude["name"] != "harness" || claude["version"] != "0.1.0" || codex["skills"] != "./skills/" {
		t.Fatalf("manifests: %v / %v", claude, codex)
	}
	if !strings.Contains(read(t, filepath.Join(p, "skills", "tdd", "SKILL.md")), "# tdd 0.1.0") {
		t.Fatal("version not substituted")
	}
	read(t, filepath.Join(p, "skills", "tdd", "references", "cycle.md"))
	agent := read(t, filepath.Join(p, "agents", "worker.md"))
	for _, want := range []string{"name: worker", "model: sonnet", "effort: medium", "maxTurns: 150", "tools: Read, Edit, Write, Bash", "  - harness:tdd", "# 작업자", "결과만 반환한다"} {
		if !strings.Contains(agent, want) {
			t.Errorf("claude agent missing %q:\n%s", want, agent)
		}
	}
	toml := read(t, filepath.Join(p, "codex", "agents", "harness-explorer.toml"))
	for _, want := range []string{`name = "harness-explorer"`, `sandbox_mode = "read-only"`, `model_reasoning_effort = "low"`, "developer_instructions = '''", "찾는다."} {
		if !strings.Contains(toml, want) {
			t.Errorf("codex agent missing %q:\n%s", want, toml)
		}
	}
	var cm, xm map[string]any
	json.Unmarshal([]byte(read(t, filepath.Join(out, ".claude-plugin", "marketplace.json"))), &cm)
	json.Unmarshal([]byte(read(t, filepath.Join(out, ".agents", "plugins", "marketplace.json"))), &xm)
	if cm["name"] != "harness" || xm["name"] != "harness" {
		t.Fatalf("marketplaces: %v %v", cm, xm)
	}
}

func TestBuild_MCP와_훅은_호스트별로_변수를_채운다(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	write(t, src, map[string]string{
		"plugin.json": `{"name":"orbit","version":"0.1.0","description":"d","skills":["memory"],
		  "mcpServers":{"orbit":{"command":"{{oni}}","args":["mcp"]}},
		  "hooks":{"SessionStart":[{"matcher":"startup|resume","command":"{{oniq}} hook session-start --host {{host}}","timeout":10}],
		           "UserPromptSubmit":[{"command":"{{oniq}} hook prompt --host {{host}}","timeout":5}]}}`,
		"skills/memory/SKILL.md": "---\nname: memory\ndescription: d\n---\nx",
	})
	if _, err := Build(Options{Source: src, Out: out, Vars: map[string]string{"oni": "/opt/oni", "oniq": `"/opt/oni"`}}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(out, "plugins", "orbit")
	mcp := read(t, filepath.Join(p, ".mcp.json"))
	if !strings.Contains(mcp, `"command": "/opt/oni"`) {
		t.Fatal(mcp)
	}
	ch := read(t, filepath.Join(p, "hooks", "claude.json"))
	xh := read(t, filepath.Join(p, "hooks", "codex.json"))
	if !strings.Contains(ch, `hook session-start --host claude`) || !strings.Contains(xh, `hook prompt --host codex`) || !strings.Contains(ch, `\"/opt/oni\"`) {
		t.Fatalf("hooks:\n%s\n%s", ch, xh)
	}
	var codex map[string]any
	json.Unmarshal([]byte(read(t, filepath.Join(p, ".codex-plugin", "plugin.json"))), &codex)
	if codex["mcpServers"] != "./.mcp.json" || codex["hooks"] != "./hooks/codex.json" {
		t.Fatalf("codex manifest %v", codex)
	}
	var claude map[string]any
	json.Unmarshal([]byte(read(t, filepath.Join(p, ".claude-plugin", "plugin.json"))), &claude)
	if claude["hooks"] != "./hooks/claude.json" {
		t.Fatalf("claude manifest %v", claude)
	}
}

func TestBuild_검사모드는_어긋난_생성물과_남은_파일을_알린다(t *testing.T) {
	src, out := fixture(t), t.TempDir()
	Build(Options{Source: src, Out: out})
	if res, err := Build(Options{Source: src, Out: out, Check: true}); err != nil || len(res.Drift) != 0 {
		t.Fatalf("fresh build drift: %v %v", res.Drift, err)
	}
	os.WriteFile(filepath.Join(out, "plugins", "harness", "agents", "old.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(out, "plugins", "harness", "skills", "tdd", "SKILL.md"), []byte("손댐"), 0o644)
	res, _ := Build(Options{Source: src, Out: out, Check: true})
	if len(res.Drift) != 2 {
		t.Fatalf("drift = %v", res.Drift)
	}
	Build(Options{Source: src, Out: out})
	if _, err := os.Stat(filepath.Join(out, "plugins", "harness", "agents", "old.md")); !os.IsNotExist(err) {
		t.Fatal("leftover not removed")
	}
}

func TestBuild_잘못된_원본은_거절한다(t *testing.T) {
	cases := map[string]string{
		"읽기전용 역할에 Write": strings.Replace(manifest, `["Read","Grep"]`, `["Read","Write"]`, 1),
		"모르는 키":          strings.Replace(manifest, `"effort": "low"`, `"effort": "low", "colour": "x"`, 1),
		"없는 스킬 참조":       strings.Replace(manifest, `"skills": ["tdd"]}`, `"skills": ["nope"]}`, 1),
		"잘못된 모델":         strings.Replace(manifest, `"haiku"`, `"gpt"`, 1),
	}
	for name, m := range cases {
		src := fixture(t)
		os.WriteFile(filepath.Join(src, "plugin.json"), []byte(m), 0o644)
		if _, err := Build(Options{Source: src, Out: t.TempDir()}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	src := fixture(t)
	os.WriteFile(filepath.Join(src, "agents", "worker.md"), []byte("a ''' b"), 0o644)
	if _, err := Build(Options{Source: src, Out: t.TempDir()}); err == nil {
		t.Error("''' in body accepted")
	}
}

// 설계 「훅」: 호스트마다 matcher가 다른 훅을 적을 수 있고, 비동기 훅은 두 호스트 모두 async로 낸다.
func TestBuild_훅은_호스트별로_나누고_비동기를_표시한다(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	write(t, src, map[string]string{
		"plugin.json": `{"name":"orbit","version":"0.1.0","description":"d","skills":["memory"],
		  "hooks":{"PostToolUse":[
		     {"hosts":["claude"],"matcher":"TaskCreate|TaskUpdate","command":"oni hook work --host {{host}}","timeout":10},
		     {"hosts":["codex"],"matcher":"update_plan","command":"oni hook work --host {{host}}","timeout":10},
		     {"matcher":".*","command":"oni hook work --host {{host}} --skip-lists","async":true}],
		   "PostToolUseFailure":[{"hosts":["claude"],"matcher":".*","command":"oni hook work --host {{host}}","async":true}],
		   "PermissionRequest":[{"command":"oni hook work --host {{host}}","async":true}]}}`,
		"skills/memory/SKILL.md": "---\nname: memory\ndescription: d\n---\nx",
	})
	if _, err := Build(Options{Source: src, Out: out}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(out, "plugins", "orbit", "hooks")
	type group struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Command string `json:"command"`
			Async   bool   `json:"async"`
		} `json:"hooks"`
	}
	load := func(name string) map[string][]group {
		var f struct {
			Hooks map[string][]group `json:"hooks"`
		}
		if err := json.Unmarshal([]byte(read(t, filepath.Join(p, name))), &f); err != nil {
			t.Fatal(err)
		}
		return f.Hooks
	}
	ch, xh := load("claude.json"), load("codex.json")
	if len(ch["PostToolUse"]) != 2 || ch["PostToolUse"][0].Matcher != "TaskCreate|TaskUpdate" || ch["PostToolUse"][1].Matcher != ".*" || !ch["PostToolUse"][1].Hooks[0].Async {
		t.Errorf("claude PostToolUse: %+v", ch["PostToolUse"])
	}
	if len(xh["PostToolUse"]) != 2 || xh["PostToolUse"][0].Matcher != "update_plan" || ch["PostToolUse"][0].Hooks[0].Async {
		t.Errorf("codex PostToolUse: %+v", xh["PostToolUse"])
	}
	if _, ok := xh["PostToolUseFailure"]; ok || len(ch["PostToolUseFailure"]) != 1 {
		t.Errorf("Claude 전용 사건이 Codex에 들어갔습니다: %+v / %+v", xh, ch["PostToolUseFailure"])
	}
	if !xh["PermissionRequest"][0].Hooks[0].Async || !strings.Contains(xh["PermissionRequest"][0].Hooks[0].Command, "--host codex") {
		t.Errorf("codex PermissionRequest: %+v", xh["PermissionRequest"])
	}
	write(t, src, map[string]string{"plugin.json": `{"name":"orbit","version":"0.1.0","description":"d","skills":["memory"],
		  "hooks":{"Stop":[{"hosts":["gemini"],"command":"x"}]}}`})
	if _, err := Build(Options{Source: src, Out: t.TempDir()}); err == nil {
		t.Error("모르는 호스트를 받았습니다")
	}
}

// Codex는 SessionEnd 훅 timeout을 3초로 줄이고 경고를 띄운다(codex-cli 0.164 "clamping SessionEnd hook timeout to 3s").
// Codex에도 나가는 SessionEnd 훅은 3초를 넘길 수 없고, Claude 전용이면 제한하지 않는다.
func TestBuild_Codex에_나가는_SessionEnd_훅은_3초를_넘지_않는다(t *testing.T) {
	hooked := func(spec string) string {
		return strings.Replace(manifest, `"skills": ["tdd"],`, `"skills": ["tdd"], "hooks": {"SessionEnd": [`+spec+`]},`, 1)
	}
	cases := map[string]struct {
		spec string
		ok   bool
	}{
		"두 호스트 5초":    {`{"command": "x", "timeout": 5}`, false},
		"Codex만 4초":   {`{"command": "x", "timeout": 4, "hosts": ["codex"]}`, false},
		"두 호스트 3초":    {`{"command": "x", "timeout": 3}`, true},
		"timeout 없음":  {`{"command": "x"}`, true},
		"Claude만 10초": {`{"command": "x", "timeout": 10, "hosts": ["claude"]}`, true},
	}
	for name, c := range cases {
		src := fixture(t)
		os.WriteFile(filepath.Join(src, "plugin.json"), []byte(hooked(c.spec)), 0o644)
		_, err := Build(Options{Source: src, Out: t.TempDir()})
		if c.ok && err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "3초")) {
			t.Errorf("%s: want 3초 error, got %v", name, err)
		}
	}
}
