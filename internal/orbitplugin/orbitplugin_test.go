package orbitplugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit/internal/pluginbuild"
	"github.com/onidot-labs/orbit/internal/version"
)

func TestOrbit플러그인_두_호스트로_빌드된다(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	if err := Write(src); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginbuild.Build(pluginbuild.Options{Source: src, Out: out, Vars: Vars("/Users/me/.local/bin/oni")}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"agents/worker.md", "agents/kotlin-spring-dev.md", "codex/agents/orbit-gate.toml", "skills/tdd/SKILL.md", "skills/git-commit/SKILL.md", "skills/memory/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(out, "plugins", "orbit", f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	b, _ := os.ReadFile(filepath.Join(out, "plugins", "orbit", "hooks", "codex.json"))
	if !strings.Contains(string(b), "/Users/me/.local/bin/oni hook session-start --host codex") || !strings.Contains(string(b), `"SessionEnd"`) ||
		!strings.Contains(string(b), "/Users/me/.local/bin/oni hook session-end --host codex") {
		t.Fatalf("codex hooks: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(out, "plugins", "orbit", "hooks", "claude.json"))
	if !strings.Contains(string(b), "/Users/me/.local/bin/oni hook session-end --host claude") {
		t.Fatalf("claude hooks: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(out, "plugins", "orbit", ".mcp.json"))
	if !strings.Contains(string(b), `"/Users/me/.local/bin/oni"`) {
		t.Fatalf("mcp: %s", b)
	}
}

func TestVars_공백이_있는_경로만_따옴표(t *testing.T) {
	if Vars(`C:\Users\Kim Lee\oni.exe`)["onicmd"] != `"C:/Users/Kim Lee/oni.exe"` {
		t.Fatal(Vars(`C:\Users\Kim Lee\oni.exe`))
	}
	if Vars("/a/oni")["onicmd"] != "/a/oni" {
		t.Fatal("quoted without spaces")
	}
}

func TestOrbit플러그인_버전은_oni_버전과_같다(t *testing.T) {
	b, _ := source.ReadFile("source/plugin.json")
	if !strings.Contains(string(b), `"version": "`+version.Version+`"`) {
		t.Fatalf("plugin.json version must be %s", version.Version)
	}
}

// 설계 · Orbit 2차 「하네스 문장」: plan·delegate·verify는 호스트 작업 목록 사용법만 말하고
// "Orbit에 기록하라"는 절차를 넣지 않는다. 예외는 작업 제목(work_title) 한 줄이다.
func TestHarnessSentencesForWorkGraph(t *testing.T) {
	read := func(skill string) string {
		b, err := os.ReadFile(filepath.Join("source", "skills", skill, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	plan, delegate, verify := read("plan"), read("delegate"), read("verify")
	for _, want := range []string{"addBlockedBy", "owner", "실행 순서대로", "work_title"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan에 %q가 없습니다", want)
		}
	}
	if !strings.Contains(delegate, "ORBIT_PARENT=") || !strings.Contains(delegate, "진행으로 바꾸고") {
		t.Error("delegate에 맡기기 전 진행·하위 세션 ORBIT_PARENT 문장이 없습니다")
	}
	// 셸에서 자기 세션 id를 주는 변수(실측: Claude Code 2.1.303, codex-cli 0.164)
	for _, v := range []string{"CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID"} {
		if !strings.Contains(delegate, v) {
			t.Errorf("delegate에 부모 세션 id 변수 %s가 없습니다", v)
		}
	}
	if !strings.Contains(verify, "커밋이 생겼는지") {
		t.Error("verify에 판정 뒤 커밋 확인 문장이 없습니다")
	}
	for name, body := range map[string]string{"plan": plan, "delegate": delegate, "verify": verify} {
		for _, bad := range []string{"Orbit에 기록", "work_list", "work_get"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s에 기록 절차 %q가 들어갔습니다", name, bad)
			}
		}
		if strings.Count(body, "work_title") > 1 {
			t.Errorf("%s에 work_title 문장이 여럿입니다", name)
		}
	}
}
