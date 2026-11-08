package mcpserver

// S3 범위 규칙 고침(명세 2.2b~2.4, 조사 0s4gere91nvht 8.2): 세션 폴더 기준 기본값(S3-R1), 여러 제품 폴더 좁히기(S3-R2b),
// 규칙은 적용 대상 필수(S3-R3), 찾기도 상위 폴더 기억을 본다(S3-R2).

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectIn is a host session started in dir, whose project scope is scope.
func connectIn(t *testing.T, s *memory.Store, scope, dir string, git, temp bool) *mcp.ClientSession {
	t.Helper()
	return connectServer(t, New(s, Options{Project: scope, ProjectLabel: strings.TrimPrefix(scope, "project:"), Dir: dir, Git: git, Temp: temp,
		Version: "test", Products: s3Map()}))
}

func TestSave_기본값은_세션_폴더의_제품_저장소_폴더_순이다(t *testing.T) {
	s := s3Store(t)
	cases := []struct {
		name, scope, dir string
		git, temp        bool
		applies, project string
		warn             string
	}{
		{"한 제품 저장소", deskRepo, "/w/onidot/orbit-studio", true, false, "product:orbit-code-desktop", "", ""},
		{"제품 없는 원격 저장소", other, "/w/other/unrelated/src", true, false, "repo:github.com/someone/unrelated", "github.com/someone/unrelated", ""},
		{"제품 없는 원격 없는 저장소의 하위 폴더", "project:/w/lab/repo", "/w/lab/repo/sub", true, false, "repo:/w/lab/repo", "/w/lab/repo", ""},
		{"git 밖 폴더", "project:/w/notes/2026", "/w/notes/2026", false, false, "product:?", "/w/notes/2026", "폴더에 묶음"},
		{"임시 폴더", "project:/tmp/x", "/tmp/x/y", true, true, "product:?", "/tmp/x/y", "임시 폴더"},
	}
	for i, c := range cases {
		var out MemoryOut
		res := call(t, connectIn(t, s, c.scope, c.dir, c.git, c.temp), "memory_save",
			map[string]any{"kind": "fact", "title": "사실 " + string(rune('가'+i)), "source": quote("x")}, &out)
		if res.IsError || out.Memory.AppliesTo != c.applies || out.Memory.Project != c.project {
			t.Fatalf("%s: %v %s %+v", c.name, res.IsError, text(res), out.Memory)
		}
		w := strings.Join(out.Warnings, " ")
		if c.warn == "" && w != "" || c.warn != "" && !strings.Contains(w, c.warn) {
			t.Fatalf("%s: warnings %v", c.name, out.Warnings)
		}
	}
}

func TestSave_여러_제품_폴더는_글에_이름이_하나면_그_제품이다(t *testing.T) {
	s := s3Store(t)
	for title, want := range map[string]string{
		"Atlas 문서는 onidot Space에 쓴다":          "product:atlas",
		"orbit-code-desktop 상단바는 레일":          "product:orbit-code-desktop",
		"atlas와 orbit-code-desktop 둘 다 다크 모드": "product:?",
		"이 폴더의 빌드 순서":                         "product:?",
		"atlases는 제품 이름이 아니다":                 "product:?",
	} {
		var out MemoryOut
		call(t, connectIn(t, s, parent, "/w/onidot", false, false), "memory_save", map[string]any{"kind": "decision", "title": title, "topic": title, "source": quote("x")}, &out)
		if out.Memory.AppliesTo != want {
			t.Fatalf("%q: %s %v", title, out.Memory.AppliesTo, out.Warnings)
		}
	}
	// 본문의 이름도 본다. appliesTo product도 같은 좁히기를 쓴다.
	var out MemoryOut
	call(t, connectIn(t, s, parent, "/w/onidot", false, false), "memory_save",
		map[string]any{"kind": "fact", "appliesTo": "product", "title": "문서 위치", "body": "atlas에 둔다", "source": quote("x")}, &out)
	if out.Memory.AppliesTo != "product:atlas" {
		t.Fatalf("body: %s", out.Memory.AppliesTo)
	}
}

func TestSave_규칙은_적용_대상이_없으면_거절하고_고를_것을_보인다(t *testing.T) {
	s := s3Store(t)
	res := call(t, connectIn(t, s, parent, "/w/onidot", false, false), "memory_save",
		map[string]any{"kind": "rule", "title": "답은 존댓말", "topic": "말투", "source": quote("존댓말")}, nil)
	msg := text(res)
	for _, want := range []string{"appliesTo", "user", "product:<이름>", "repo", "product:?", "product:atlas", "product:orbit-code-desktop"} {
		if !res.IsError || !strings.Contains(msg, want) {
			t.Fatalf("missing %q: %v %s", want, res.IsError, msg)
		}
	}
	if all, _ := s.List(); len(all) != 0 {
		t.Fatalf("saved %d", len(all))
	}
	// 적용 대상을 주거나 옛 scope를 주면 저장된다. 다른 종류는 기본값을 따른다.
	for _, in := range []map[string]any{
		{"kind": "rule", "appliesTo": "user", "title": "답은 존댓말", "topic": "말투", "source": quote("존댓말")},
		{"kind": "rule", "scope": "global", "title": "보고는 한국어", "topic": "보고", "source": quote("한국어")},
		{"kind": "lesson", "title": "배운 것", "source": quote("배움")},
	} {
		if res := call(t, connectIn(t, s, parent, "/w/onidot", false, false), "memory_save", in, nil); res.IsError {
			t.Fatalf("%v: %s", in, text(res))
		}
	}
}

func TestFind_here는_상위_폴더_기억을_아래_저장소에서도_찾는다(t *testing.T) {
	s := s3Store(t)
	call(t, connectIn(t, s, parent, "/w/onidot", false, false), "memory_save", map[string]any{"kind": "fact", "title": "onidot 빌드는 make", "source": quote("x")}, nil)
	if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "onidot 옛 폴더 키 사실 make", Scope: parent, Project: "/w/onidot", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}}); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		scope, dir string
		want       int
	}{
		"상위 폴더":      {parent, "/w/onidot", 2},
		"제품 저장소":     {deskRepo, "/w/onidot/orbit-studio", 2},
		"그 worktree": {deskRepo, "/w/onidot/orbit-studio/.claude/worktrees/x", 2},
		"시안 폴더":      {draft, "", 2},
		"다른 폴더 저장소":  {other, "/w/other/unrelated", 0},
	} {
		var out FindOut
		call(t, connectIn(t, s, c.scope, c.dir, true, false), "memory_find", map[string]any{"query": "make"}, &out)
		if len(out.Items) != c.want {
			t.Fatalf("%s: %+v", name, out.Items)
		}
	}
}

func TestUpdate_appliesTo_product는_기억의_글로_좁힌다(t *testing.T) {
	s := s3Store(t)
	var out MemoryOut
	cs := connectIn(t, s, parent, "/w/onidot", false, false)
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "atlas 배포는 main 머지", "appliesTo": "user", "source": quote("x")}, &out)
	var up MemoryOut
	if res := call(t, cs, "memory_update", map[string]any{"id": out.Memory.ID, "appliesTo": "product", "userQuote": "제품으로"}, &up); res.IsError || up.Memory.AppliesTo != "product:atlas" {
		t.Fatalf("%v %s %+v", res.IsError, text(res), up.Memory)
	}
}
