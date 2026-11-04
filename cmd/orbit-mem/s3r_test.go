package main

// S3-R5(명세 2.2·2.10): place --suggest가 첫 표를 내고, place --move가 지도에 맞는 product:? 기억을 옮긴다.
// place는 appliesTo 없이 저장할 때의 기본값을 S3-R1대로 보인다.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
	"github.com/onidot-labs/orbit-mem/internal/project"
)

func TestPlace_suggest는_첫_표를_내고_설정을_쓰지_않는다(t *testing.T) {
	h := home(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	root := filepath.Join(h, "ws")
	for rel, remote := range map[string]string{"onidot/atlas": "https://github.com/onidot-labs/atlas.git", "onidot/forma": "git@github.com:onidot-labs/forma.git", "solo/lab": ""} {
		d := filepath.Join(root, rel)
		if out, err := exec.Command("git", "init", "-q", d).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if remote != "" {
			exec.Command("git", "-C", d, "remote", "add", "origin", remote).Run()
		}
	}
	os.MkdirAll(filepath.Join(root, "notes", "a"), 0o755)
	out, errOut, code := run(t, "", "place", "--suggest", "--root", root, "--json")
	var got struct {
		Products place.Map `json:"products"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if err := got.Products.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"atlas", "forma", "lab", "notes"} {
		if _, ok := got.Products[name]; !ok {
			t.Fatalf("no %s in %+v", name, got.Products)
		}
	}
	if !strings.Contains(strings.Join(got.Products["atlas"].Match, " "), "github.com/onidot-labs/atlas") {
		t.Fatalf("atlas %+v", got.Products["atlas"])
	}
	if _, err := os.Stat(paths().Config()); err == nil {
		if cfg, _ := config.Load(paths().Config()); len(cfg.Products) != 0 {
			t.Fatal("suggest wrote the config")
		}
	}
	if out, _, code := run(t, "", "place", "--suggest", "--root", root); code != 0 || !strings.Contains(out, "atlas") || !strings.Contains(out, "products") {
		t.Fatalf("text: %d %s", code, out)
	}
}

func TestPlace_move는_지도에_맞는_product_물음표를_옮긴다(t *testing.T) {
	h := home(t)
	s := seed(t)
	folder := filepath.ToSlash(filepath.Join(h, "w", "desk"))
	m, err := s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: "레일 유지", Topic: "레일", Scope: place.ScopeUnknown, Project: folder,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "레일"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Products = place.Map{"desk": {Match: []string{folder}}}
	if err := config.Save(paths().Config(), cfg); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "", "place", "--move", "--json")
	var moved []memory.Moved
	if code != 0 || json.Unmarshal([]byte(out), &moved) != nil || len(moved) != 1 || moved[0].ID != m.ID || moved[0].To != "product:desk" {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if got, _ := s.Get(m.ID); got.Scope != "product:desk" {
		t.Fatalf("scope %s", got.Scope)
	}
	if out, _, code := run(t, "", "place", "--move"); code != 0 || out == "" {
		t.Fatalf("second move text: %d %q", code, out)
	}
}

func TestPlace_기본값은_세션_폴더의_제품_저장소_폴더_순이다(t *testing.T) {
	h := home(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	// 시험 홈은 $TMPDIR 아래다. 임시 폴더 판정을 끄고 저장소·폴더 기본값을 본다.
	roots := project.TempRoots
	project.TempRoots = func() []string { return nil }
	t.Cleanup(func() { project.TempRoots = roots })
	repo := filepath.Join(h, "w", "lab")
	exec.Command("git", "init", "-q", repo).Run()
	plain := filepath.Join(h, "w", "notes")
	os.MkdirAll(plain, 0o755)
	realRepo, _ := filepath.EvalSymlinks(repo)
	for dir, want := range map[string]string{repo: "repo:" + filepath.ToSlash(realRepo), plain: "product:?"} {
		out, errOut, code := run(t, "", "place", "--cwd", dir, "--json")
		var got struct {
			Default string `json:"defaultAppliesTo"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Default != want {
			t.Fatalf("%s: %d %s %s want %s", dir, code, out, errOut, want)
		}
	}
}
