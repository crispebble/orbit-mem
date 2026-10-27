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
