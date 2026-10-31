package orbitplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/onidot-labs/openorbit/internal/pluginbuild"
)

// 오케스트레이터 스킬(설계 · OpenOrbit 4차 R4-5): 이름과 호출 방식은 널리 쓰이는 키워드·명령을 따르고
// (ultrawork·ulw-plan·ulw-execute), 본문은 OpenOrbit이 새로 쓴다. 영어 기본·한국어 지원이다.
// description(트리거)은 영어와 한국어를 함께 써서 한국어로 말해도 걸린다.
var orchestraSkills = map[string][]string{
	"ultrawork":   {"orb run --ultrawork", "orb work get", "--max-cost", "--max-turns", "--max-minutes", "--json", "--events", "exit code", "decision card", "three times", "ulw-plan", "## 한국어 요약"},
	"ulw-plan":    {"orb run --plan", "orb work get", "does not change", "orbit:ulw-execute", "## 한국어 요약"},
	"ulw-execute": {"orb run --resume", "orb work list --cwd . --state open --launcher orbit-pi", `origin.host`, "orbit-pi", "## 한국어 요약"},
}

// 트리거 낱말(대소문자 무시). ultrawork는 ulw-plan과 구분됨을 밝힌다.
var orchestraTriggers = map[string][]string{
	"ultrawork":   {"ulw", "ultrawork", "끝까지", "끝까지 해", "all the way", "ulw-plan"},
	"ulw-plan":    {"ulw plan", "ulw-plan", "계획만", "plan first"},
	"ulw-execute": {"ulw-execute", "이어서 해", "resume", "continue the plan"},
}

var hangul = regexp.MustCompile(`[가-힣]`)

func frontDescription(t *testing.T, body string) string {
	t.Helper()
	front := strings.SplitN(strings.TrimPrefix(body, "---\n"), "\n---", 2)[0]
	for _, l := range strings.Split(front, "\n") {
		if v, ok := strings.CutPrefix(l, "description: "); ok {
			var s string
			if err := json.Unmarshal([]byte(v), &s); err != nil {
				t.Fatalf("description은 따옴표 문자열이어야 합니다(YAML에서 ': '가 깨지지 않게): %s", v)
			}
			return s
		}
	}
	t.Fatal("description이 없습니다")
	return ""
}

func TestOrchestraSkills_영어_기본_한국어_트리거로_두_호스트에_빌드된다(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	if err := Write(src); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Skills []string `json:"skills"`
	}
	b, _ := os.ReadFile(filepath.Join(src, "plugin.json"))
	_ = json.Unmarshal(b, &m)
	listed := strings.Join(m.Skills, ",")
	if _, err := pluginbuild.Build(pluginbuild.Options{Source: src, Out: out, Vars: Vars("/x/orb")}); err != nil {
		t.Fatal(err)
	}
	for name, wants := range orchestraSkills {
		if !strings.Contains(","+listed+",", ","+name+",") {
			t.Errorf("plugin.json skills에 %s가 없습니다", name)
		}
		built, err := os.ReadFile(filepath.Join(out, "plugins", "orbit", "skills", name, "SKILL.md"))
		if err != nil {
			t.Errorf("%s: 빌드 결과가 없습니다: %v", name, err)
			continue
		}
		body := string(built)
		d := frontDescription(t, body)
		if !strings.HasPrefix(d, "Use when") || !hangul.MatchString(d) || !strings.Contains(d, "Triggers") {
			t.Errorf("%s description은 영어로 시작하고 한국어와 Triggers를 함께 담아야 합니다: %s", name, d)
		}
		for _, w := range orchestraTriggers[name] {
			if !strings.Contains(strings.ToLower(d), strings.ToLower(w)) {
				t.Errorf("%s description에 트리거 %q가 없습니다", name, w)
			}
		}
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s에 %q가 없습니다", name, w)
			}
		}
		if !strings.Contains(body, "merge") || !strings.Contains(body, "push") {
			t.Errorf("%s에 병합·푸시를 하지 않는다는 문장이 없습니다", name)
		}
	}
	// Codex도 같은 원본(./skills/)을 읽는다
	cx, _ := os.ReadFile(filepath.Join(out, "plugins", "orbit", ".codex-plugin", "plugin.json"))
	if !strings.Contains(string(cx), `"./skills/"`) {
		t.Errorf("codex 매니페스트에 skills가 없습니다: %s", cx)
	}
	// 앞 이름(run·all-in·resume)은 확정 이름에 흡수했다
	for _, old := range []string{"run", "all-in", "resume"} {
		if strings.Contains(","+listed+",", ","+old+",") {
			t.Errorf("옛 스킬 이름 %s가 남았습니다", old)
		}
	}
}
