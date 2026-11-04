package place

// S3 범위 규칙 고침(명세 2.3·2.4, 조사 0s4gere91nvht 8.2): 폴더 계층 상속(R2), 여러 제품 폴더 좁히기(R2b).

import (
	"reflect"
	"testing"
)

// 세션 폴더(checkout 폴더)에서 홈 바로 아래까지 위로 올라가며 상위 폴더에 묶인 기억을 본다.
func TestSees_상위_폴더_기억은_아래_모든_폴더와_저장소에서_보인다(t *testing.T) {
	const onidot = "/Users/u/_workspace/onidot"
	sessions := map[string]Place{
		"제품 저장소(원격)":   AtIn("project:github.com/onidot-labs/orbit-studio", onidot+"/orbit-studio", fixture(), home),
		"그 worktree":   AtIn("project:github.com/onidot-labs/orbit-studio", onidot+"/orbit-studio/.claude/worktrees/x/src", fixture(), home),
		"상위 폴더":        AtIn("project:"+onidot, onidot, fixture(), home),
		"시안 폴더":        ResolveIn("project:"+onidot+"/prototypes/orbit-desktop-v4-2026-11-03", fixture(), home),
		"다른 제품":        AtIn("project:github.com/onidot-labs/atlas", onidot+"/atlas", fixture(), home),
		"무관 저장소":       AtIn("project:github.com/someone/unrelated", "/Users/u/_workspace/other/unrelated", fixture(), home),
		"원격 키만 아는 연결":  ResolveIn("project:github.com/onidot-labs/atlas", fixture(), home),
		"홈 밖":          AtIn("project:/opt/work/app", "/opt/work/app", fixture(), home),
		"홈":            AtIn("project:/Users/u", "/Users/u", fixture(), home),
		"접두만 같은 이웃 폴더": AtIn("project:/Users/u/_workspace/onidot-old/x", "/Users/u/_workspace/onidot-old/x", fixture(), home),
	}
	type mem struct{ scope, project string }
	all := []string{"제품 저장소(원격)", "그 worktree", "상위 폴더", "시안 폴더", "다른 제품"}
	want := map[mem][]string{
		{"project:" + onidot, onidot}:                                     all,
		{"product:?", onidot}:                                             all,
		{"project:/Users/u/_workspace", "/Users/u/_workspace"}:            append(append([]string{}, all...), "무관 저장소", "접두만 같은 이웃 폴더"),
		{"project:/Users/u", "/Users/u"}:                                  {"홈"},
		{"project:/opt", "/opt"}:                                          nil,
		{"project:/opt/work", "/opt/work"}:                                {"홈 밖"},
		{"project:" + onidot + "/orbit-studio", onidot + "/orbit-studio"}: {"제품 저장소(원격)", "그 worktree"},
		{"project:github.com/onidot-labs/orbit-studio", "github.com/onidot-labs/orbit-studio"}: {"제품 저장소(원격)", "그 worktree"},
	}
	for m, seenIn := range want {
		for name, p := range sessions {
			exp := false
			for _, n := range seenIn {
				exp = exp || n == name
			}
			if got := p.Sees(m.scope, m.project); got != exp {
				t.Errorf("%s(%s) in %s: sees %v, want %v", m.scope, m.project, name, got, exp)
			}
		}
	}
}

// 원격이 없는 저장소의 worktree가 주 저장소 밖에 있어도 같은 키의 기억을 본다(R4).
func TestSees_주_저장소_키는_밖에_있는_worktree에서도_보인다(t *testing.T) {
	p := AtIn("project:/Users/u/_workspace/studio", "/Users/u/wt/studio-feature", nil, home)
	if !p.Sees("project:/Users/u/_workspace/studio", "/Users/u/_workspace/studio") {
		t.Fatal("main repo key")
	}
	if p.Sees("project:/Users/u/_workspace", "/Users/u/_workspace") {
		t.Fatal("ancestors of the key are not the worktree's ancestors")
	}
}

func TestResolve_세션_폴더나_범위_키를_받는다(t *testing.T) {
	p := ResolveIn("project:/Users/u/_workspace/onidot/prototypes/x", fixture(), home)
	if p.Dir != "/Users/u/_workspace/onidot/prototypes/x" {
		t.Fatalf("dir from folder key %q", p.Dir)
	}
	if p := ResolveIn("project:github.com/a/b", fixture(), home); p.Dir != "" {
		t.Fatalf("remote key has no dir %q", p.Dir)
	}
}

func TestNarrow_여러_제품_폴더에서_이름이_하나면_그_제품(t *testing.T) {
	m := fixture()
	m["forma"] = Product{Match: []string{"~/_workspace/onidot"}}
	p := ResolveIn("project:/Users/u/_workspace/onidot", m, home)
	for text, want := range map[string]string{
		"Atlas 문서는 onidot Space에":  "atlas",
		"Orbit 데스크탑 상단바는 레일":       "orbit-code-desktop",
		"Atlas와 Orbit Desktop 둘 다": "",
		"아무 제품도 아닌 이야기":            "",
		"출력 format을 바꾼다":           "",
		"forma 단추 크기":              "forma",
		"atlas.example.com 주소":      "atlas",
	} {
		if got := p.Narrow(text); got != want {
			t.Errorf("Narrow(%q) = %q, want %q", text, got, want)
		}
	}
	if got := p.Named("format forma"); !reflect.DeepEqual(got, []string{"forma"}) {
		t.Fatalf("named %v", got)
	}
}
