package place

// 설계 · orbit-mem 2판 S3 명세 2.2·2.3(S3-2): 제품 지도가 폴더를 제품에 묶고, 적용 대상마다 어느 폴더에서 보이는지.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const home = "/Users/u"

// fixture is the product map of the AC-3 table: two products share the parent folder.
func fixture() Map {
	return Map{
		"orbit-code-desktop": {
			Match: []string{"github.com/onidot-labs/orbit-studio", "~/_workspace/onidot/prototypes/orbit-desktop-*", "~/_workspace/onidot"},
			Names: []string{"Orbit 데스크탑", "Orbit Desktop"},
		},
		"atlas": {Match: []string{"github.com/onidot-labs/atlas", "~/_workspace/onidot"}, Names: []string{"Atlas"}},
	}
}

func TestResolve_제품_지도는_가장_구체적인_줄이_정하고_같으면_여러_제품이다(t *testing.T) {
	cases := []struct {
		name, scope string
		want        []string
	}{
		{"제품 저장소(원격)", "project:github.com/onidot-labs/orbit-studio", []string{"orbit-code-desktop"}},
		{"다른 제품 저장소", "project:github.com/onidot-labs/atlas", []string{"atlas"}},
		{"상위 폴더", "project:/Users/u/_workspace/onidot", []string{"atlas", "orbit-code-desktop"}},
		{"날짜별 시안 폴더", "project:/Users/u/_workspace/onidot/prototypes/orbit-desktop-v4-2026-11-03", []string{"orbit-code-desktop"}},
		{"시안 폴더 아래", "project:/Users/u/_workspace/onidot/prototypes/orbit-desktop-v4-2026-11-03/src", []string{"orbit-code-desktop"}},
		{"지도에 없는 시안은 상위 폴더를 따른다", "project:/Users/u/_workspace/onidot/prototypes/db-app-2026-11-03", []string{"atlas", "orbit-code-desktop"}},
		{"무관 저장소", "project:github.com/someone/unrelated", nil},
		{"무관 폴더", "project:/Users/u/elsewhere", nil},
		{"접두만 같은 이웃 폴더", "project:/Users/u/_workspace/onidot-old", nil},
		{"폴더 모름", "", nil},
	}
	for _, c := range cases {
		p := ResolveIn(c.scope, fixture(), home)
		if !reflect.DeepEqual(p.Products, c.want) {
			t.Errorf("%s: products %v, want %v", c.name, p.Products, c.want)
		}
		if p.Multi() != (len(c.want) > 1) {
			t.Errorf("%s: multi %v", c.name, p.Multi())
		}
		if p.Scope != c.scope {
			t.Errorf("%s: scope %q", c.name, p.Scope)
		}
	}
	if got := ResolveIn("project:/Users/u/_workspace/onidot/prototypes/orbit-desktop-v4-2026-11-03", fixture(), home).Product(); got != "orbit-code-desktop" {
		t.Fatalf("single product %q", got)
	}
	if got := ResolveIn("project:/Users/u/_workspace/onidot", fixture(), home).Product(); got != "" {
		t.Fatalf("multi folder has one product %q", got)
	}
}

// AC-3 표의 판정 부분: 적용 대상마다 보이는 폴더.
func TestSees_적용_대상마다_보이는_폴더(t *testing.T) {
	folders := map[string]string{
		"제품 저장소": "project:github.com/onidot-labs/orbit-studio",
		"상위 폴더":  "project:/Users/u/_workspace/onidot",
		"시안 폴더":  "project:/Users/u/_workspace/onidot/prototypes/orbit-desktop-v4-2026-11-03",
		"다른 제품":  "project:github.com/onidot-labs/atlas",
		"무관 저장소": "project:github.com/someone/unrelated",
	}
	type mem struct{ scope, project string }
	want := map[mem][]string{
		{"global", ""}:                     {"제품 저장소", "상위 폴더", "시안 폴더", "다른 제품", "무관 저장소"},
		{"product:orbit-code-desktop", ""}: {"제품 저장소", "상위 폴더", "시안 폴더"},
		{"product:atlas", ""}:              {"상위 폴더", "다른 제품"},
		{"project:github.com/someone/unrelated", "github.com/someone/unrelated"}:           {"무관 저장소"},
		{"product:?", "/Users/u/_workspace/onidot/prototypes/orbit-desktop-v4-2026-11-03"}: {"시안 폴더"},
		{"product:?", "/Users/u/_workspace/onidot"}:                                        {"상위 폴더"},
		{"product:없는-제품", ""}:                                                              nil,
	}
	for m, seenIn := range want {
		for name, scope := range folders {
			p := ResolveIn(scope, fixture(), home)
			got := p.Sees(m.scope, m.project)
			exp := false
			for _, n := range seenIn {
				exp = exp || n == name
			}
			if got != exp {
				t.Errorf("%s(%s) in %s: sees %v, want %v", m.scope, m.project, name, got, exp)
			}
		}
	}
	// 폴더를 모르는 연결은 user만 본다.
	if p := ResolveIn("", fixture(), home); !p.Sees("global", "") || p.Sees("product:?", "") || p.Sees("product:atlas", "") {
		t.Fatal("no folder")
	}
}

func TestAppliesTo_저장_값과_적용_대상을_짝짓는다(t *testing.T) {
	for scope, applies := range map[string]string{
		"global": "user", "project:github.com/a/b": "repo:github.com/a/b", "product:atlas": "product:atlas", "product:?": "product:?",
	} {
		if got := AppliesTo(scope); got != applies {
			t.Errorf("AppliesTo(%s) = %s", scope, got)
		}
		if got, ok := ScopeOf(applies); !ok || got != scope {
			t.Errorf("ScopeOf(%s) = %s %v", applies, got, ok)
		}
	}
	for _, bad := range []string{"", "global", "repo:", "product:", "product:Bad Name", "project:x", "everyone"} {
		if _, ok := ScopeOf(bad); ok {
			t.Errorf("ScopeOf(%q) accepted", bad)
		}
	}
	for _, ok := range []string{"global", "project:x", "product:atlas", "product:?"} {
		if !ValidScope(ok) {
			t.Errorf("ValidScope(%s)", ok)
		}
	}
	for _, bad := range []string{"", "project:", "product:", "product:A B", "user"} {
		if ValidScope(bad) {
			t.Errorf("ValidScope(%q)", bad)
		}
	}
}

func TestNamed_요청_글에_나온_제품(t *testing.T) {
	p := ResolveIn("project:/Users/u/_workspace/onidot", fixture(), home)
	if got := p.Named("orbit desktop 상단바 시안 다시 보자"); !reflect.DeepEqual(got, []string{"orbit-code-desktop"}) {
		t.Fatalf("named %v", got)
	}
	if got := p.Named("ATLAS 문서 위치"); !reflect.DeepEqual(got, []string{"atlas"}) {
		t.Fatalf("named %v", got)
	}
	if got := p.Named("orbit-code-desktop와 atlas 둘 다"); len(got) != 2 {
		t.Fatalf("named %v", got)
	}
	if got := p.Named("아무 제품도 아님"); len(got) != 0 {
		t.Fatalf("named %v", got)
	}
}

func TestValidate_잘못된_제품_지도는_거절한다(t *testing.T) {
	if err := fixture().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Map{
		"대문자 이름": {"Atlas": {Match: []string{"github.com/a/b"}}},
		"빈 이름":   {"": {Match: []string{"github.com/a/b"}}},
		"줄 없음":   {"atlas": {}},
		"빈 줄":    {"atlas": {Match: []string{" "}}},
		"잘못된 글롭": {"atlas": {Match: []string{"~/x/[a"}}},
		"상대 폴더":  {"atlas": {Match: []string{"./x"}}},
		"? 이름":   {"?": {Match: []string{"github.com/a/b"}}},
		"빈 별명":   {"atlas": {Match: []string{"github.com/a/b"}, Names: []string{""}}},
	} {
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "products") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// 검토 지적(2026-11-04): 경로 비교의 가장자리.
func TestResolve_경로_가장자리(t *testing.T) {
	// 같은 마디 수면 글롭보다 그냥 폴더가 구체적이다.
	m := Map{"glob": {Match: []string{"/w/proto/*"}}, "plain": {Match: []string{"/w/proto/desk"}}}
	if got := ResolveIn("project:/w/proto/desk", m, home).Products; !reflect.DeepEqual(got, []string{"plain"}) {
		t.Fatalf("plain vs glob %v", got)
	}
	// 원격이 있는 저장소는 원격 키로만 찾는다: 그 저장소를 품은 폴더 줄은 맞지 않는다(명세 2.2).
	if got := ResolveIn("project:github.com/acme/repo1", Map{"alpha": {Match: []string{"/w/a"}}}, home).Products; got != nil {
		t.Fatalf("folder line matched a remote repo %v", got)
	}
	// ~\ (Windows 꼴)도 홈으로 편다.
	if got := expand(`~\x`, "/Users/u"); got != "/Users/u/x" && got != `/Users/u\x` {
		t.Fatalf("expand %q", got)
	}
	// 설정 줄의 심볼릭 링크를 풀어 실제 경로 범위와 견준다.
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	if got := ResolveIn("project:"+filepath.ToSlash(real)+"/app", Map{"p": {Match: []string{filepath.ToSlash(link)}}}, home).Products; !reflect.DeepEqual(got, []string{"p"}) {
		t.Fatalf("symlinked line %v", got)
	}
	if foldCase {
		if got := ResolveIn("project:/W/Proto/Desk", m, home).Products; !reflect.DeepEqual(got, []string{"plain"}) {
			t.Fatalf("case %v", got)
		}
	}
}
