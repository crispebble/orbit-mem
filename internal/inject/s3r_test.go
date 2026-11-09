package inject

// S3-R2(명세 2.3, 조사 0s4gere91nvht 8.2) AC-3 행 추가: 상위 폴더에 묶인 기억은 그 아래 모든 폴더·저장소에서 보인다.
// 세션 폴더를 받는 세 회상 경로(세션 시작·요청·도구 직전)가 같은 판정을 쓴다. 실제 git 저장소로 훅과 같은 길을 탄다.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

func gitRepo(t *testing.T, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestAC3_상위_폴더_기억은_아래_모든_폴더와_저장소에서_보인다(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 없음")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	root = filepath.ToSlash(root)
	onidot := root + "/onidot"
	gitRepo(t, onidot+"/orbit-studio", "git@github.com:onidot-labs/orbit-studio.git")
	gitRepo(t, onidot+"/atlas", "https://github.com/onidot-labs/atlas.git")
	gitRepo(t, root+"/other/unrelated", "git@github.com:someone/unrelated.git")
	draftDir := onidot + "/prototypes/orbit-desktop-v4-2026-11-03"
	for _, d := range []string{draftDir, onidot + "/orbit-studio/src"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Products = place.Map{
		"orbit-code-desktop": {Match: []string{"github.com/onidot-labs/orbit-studio", onidot + "/prototypes/orbit-desktop-*", onidot}},
		"atlas":              {Match: []string{"github.com/onidot-labs/atlas", onidot}},
	}
	s := store(t)
	folderMem := saveIn(t, s, memory.KindDecision, "빌드 순서 결정은 make 먼저", "product:?", onidot, "빌드 순서")
	oldKey := saveIn(t, s, memory.KindFact, "빌드 순서 결정 옛 폴더 키 사실", "project:"+onidot, onidot, "")
	rule := saveIn(t, s, memory.KindRule, "onidot 폴더 규칙: 커밋 전 make check", "project:"+onidot, onidot, "커밋 전 검사")
	toolRule, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "onidot 문서는 Atlas에", Topic: "문서 위치", Scope: "product:?", Project: onidot,
		Tools: []string{"mcp__atlas__write_page"}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "Atlas에"}})
	if err != nil {
		t.Fatal(err)
	}
	inherited := []memory.Memory{folderMem, oldKey}
	sessions := map[string]bool{
		"상위 폴더": true, "제품 저장소": true, "제품 저장소 하위 폴더": true, "다른 제품 저장소": true, "시안 폴더": true, "무관 저장소": false,
	}
	dirs := map[string]string{
		"상위 폴더": onidot, "제품 저장소": onidot + "/orbit-studio", "제품 저장소 하위 폴더": onidot + "/orbit-studio/src",
		"다른 제품 저장소": onidot + "/atlas", "시안 폴더": draftDir, "무관 저장소": root + "/other/unrelated",
	}
	for name, want := range sessions {
		dir := dirs[name]
		// 확정 결정은 세션 시작 「최근 결정」 줄(A층)이나 요청 회상(B1)으로 온다(1-6 b6r).
		text := SessionStart(s, cfg, dir) + "\n" + PromptInjection(s, cfg, dir, "빌드 순서 결정 다시 보자").Text
		for _, m := range inherited {
			if strings.Contains(text, m.ID) != want {
				t.Errorf("%s 요청: %s shown %v, want %v\n%s", name, m.Title, !want, want, text)
			}
		}
		// 물려받은 규칙은 세션 시작 묶음에는 없고(S3-R6) 요청에 맞으면 회상으로 온다(S3-R7).
		if strings.Contains(SessionStartInjection(s, cfg, dir).Text, rule.ID) {
			t.Errorf("%s 세션 시작: folder rule in the bundle", name)
		}
		if got := strings.Contains(PromptInjection(s, cfg, dir, "커밋 전 make check 규칙").Text, rule.ID); got != want {
			t.Errorf("%s 요청: folder rule %v, want %v", name, got, want)
		}
		if got := strings.Contains(ToolInjection(s, cfg, dir, "h", "s-"+name, "mcp__atlas__write_page").Text, toolRule.ID); got != want {
			t.Errorf("%s 도구 직전: folder tool rule %v, want %v", name, got, want)
		}
	}
}
