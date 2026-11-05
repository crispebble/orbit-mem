package place

// S3-R5(명세 2.2): 제품 지도 첫 표 제안.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSuggest_저장소_원격과_폴더로_첫_표를_만든다(t *testing.T) {
	h, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(h, "_workspace")
	remotes := map[string]string{}
	repo := func(rel, remote string) {
		d := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		remotes[filepath.ToSlash(d)] = remote
	}
	dir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repo("onidot/atlas", "github.com/onidot-labs/atlas")
	repo("onidot/orbit-studio", "github.com/onidot-labs/orbit-studio")
	repo("onidot/orbit-studio/.claude/worktrees/x", "github.com/onidot-labs/orbit-studio") // 숨김: 보지 않는다
	repo("onidot/doraft-plugins", "github.com/onidot-labs/onidot-plugins")
	dir("onidot/prototypes/orbit-desktop-v4")
	repo("personal/claude-code", "github.com/acme/claude-code")
	repo("labs/demo", "")                           // 원격 없음
	repo("personal/atlas", "github.com/acme/atlas") // 같은 이름, 다른 원격: 소유자를 붙인다
	dir("wayviza/docs")
	dir("archive/old")
	repo("archive/legacy", "github.com/x/legacy")
	dir("tmp/x")
	dir("inbox")
	got := Suggest([]string{root}, func(d string) string { return remotes[filepath.ToSlash(d)] }, filepath.ToSlash(h))
	want := Map{
		"atlas":          {Match: []string{"github.com/onidot-labs/atlas", "~/_workspace/onidot"}},
		"orbit-studio":   {Match: []string{"github.com/onidot-labs/orbit-studio", "~/_workspace/onidot"}},
		"onidot-plugins": {Match: []string{"github.com/onidot-labs/onidot-plugins", "~/_workspace/onidot"}},
		"claude-code":    {Match: []string{"github.com/acme/claude-code", "~/_workspace/personal"}},
		"acme-atlas":     {Match: []string{"github.com/acme/atlas", "~/_workspace/personal"}},
		"demo":           {Match: []string{"~/_workspace/labs/demo"}},
		"wayviza":        {Match: []string{"~/_workspace/wayviza"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suggest\n got %+v\nwant %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	// 제안 표로 보면 상위 폴더는 여러 제품, 저장소는 한 제품이다.
	if p := ResolveIn("project:"+filepath.ToSlash(root)+"/onidot", got, filepath.ToSlash(h)); len(p.Products) != 3 {
		t.Fatalf("parent %v", p.Products)
	}
}

func TestProductName_저장소_이름을_제품_이름으로(t *testing.T) {
	for in, want := range map[string]string{"Orbit Studio": "orbit-studio", "orbit_mem": "orbit_mem", "-x-": "x", "한글": ""} {
		if got := productName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
