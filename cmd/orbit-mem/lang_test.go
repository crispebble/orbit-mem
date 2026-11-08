package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/crispebble/orbit-mem/internal/memory"
)

func hasHangul(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Hangul, r) })
}

func TestLang_새_설치는_영어로_말한다(t *testing.T) {
	onTerminal(t) // 사람용 글을 본다(--json은 언제나 JSON)
	home(t)
	t.Setenv("ORBIT_LANG", "")
	_, errOut, code := run(t, "")
	if code != 2 || !strings.Contains(errOut, "Commands:") || hasHangul(errOut) {
		t.Fatalf("usage: %d %q", code, errOut)
	}
	out, _, _ := run(t, "", "search", "없는말")
	if strings.TrimSpace(out) != "No memories found." {
		t.Fatalf("find: %q", out)
	}
	_, errOut, code = run(t, "", "nope")
	if code != 2 || !strings.Contains(errOut, "Unknown command: nope") {
		t.Fatalf("unknown: %q", errOut)
	}
	_, errOut, _ = run(t, "", "stats", "-h")
	if !strings.Contains(errOut, "-days") || hasHangul(errOut) {
		t.Fatalf("flag help: %q", errOut)
	}
	_, errOut, code = run(t, "", "config", "set", "nokey", "1")
	if code != 1 || !strings.Contains(errOut, "unknown key") || hasHangul(errOut) {
		t.Fatalf("config error: %d %q", code, errOut)
	}
}

func TestLang_설정으로_한국어로_바꾼다(t *testing.T) {
	onTerminal(t) // 사람용 글을 본다(--json은 언제나 JSON)
	home(t)
	t.Setenv("ORBIT_LANG", "")
	out, errOut, code := run(t, "", "config", "set", "language", "ko")
	if code != 0 || !strings.Contains(out, "language = ko") {
		t.Fatalf("set: %d %q %q", code, out, errOut)
	}
	out, _, _ = run(t, "", "search", "없는말")
	if strings.TrimSpace(out) != "찾은 기억이 없습니다." {
		t.Fatalf("find: %q", out)
	}
	_, errOut, _ = run(t, "", "nope")
	if !strings.Contains(errOut, "모르는 명령입니다: nope") || !strings.Contains(errOut, "명령:") {
		t.Fatalf("unknown: %q", errOut)
	}
	// 환경 변수가 설정보다 앞선다.
	t.Setenv("ORBIT_LANG", "en")
	out, _, _ = run(t, "", "search", "없는말")
	if strings.TrimSpace(out) != "No memories found." {
		t.Fatalf("env first: %q", out)
	}
	t.Setenv("ORBIT_LANG", "")
	out, _, _ = run(t, "", "config", "set", "language", "en")
	if !strings.Contains(out, "language = en") || hasHangul(out) {
		t.Fatalf("back to en: %q", out)
	}
}

func TestLang_환경_변수만으로도_바뀐다(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_LANG", "ko")
	_, errOut, _ := run(t, "")
	if !strings.Contains(errOut, "명령:") {
		t.Fatalf("usage: %q", errOut)
	}
}

func TestLang_지원하지_않는_언어는_받지_않는다(t *testing.T) {
	home(t)
	t.Setenv("ORBIT_LANG", "")
	_, errOut, code := run(t, "", "config", "set", "language", "ja")
	if code != 1 || !strings.Contains(errOut, "language") || !strings.Contains(errOut, "en") || !strings.Contains(errOut, "ko") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestLang_JSON_출력은_언어와_무관하다(t *testing.T) {
	onTerminal(t) // 사람용 글을 본다(--json은 언제나 JSON)
	home(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	t.Setenv("ORBIT_LANG", "en")
	en, _, _ := run(t, "", "search", "--all", "--json")
	t.Setenv("ORBIT_LANG", "ko")
	ko, _, _ := run(t, "", "search", "--all", "--json")
	if en != ko || !strings.Contains(en, `"kind": "rule"`) {
		t.Fatalf("en %q\nko %q", en, ko)
	}
	t.Setenv("ORBIT_LANG", "en")
	out, _, _ := run(t, "", "search", "--all")
	if !strings.Contains(out, "rule·confirmed") {
		t.Fatalf("names: %q", out)
	}
	t.Setenv("ORBIT_LANG", "ko")
	out, _, _ = run(t, "", "search", "--all")
	if !strings.Contains(out, "규칙·확정") {
		t.Fatalf("names ko: %q", out)
	}
}

// 명령 코드의 문자열에 한글을 두지 않는다. 사람이 읽는 글은 카탈로그(internal/i18n/locales)에 둔다.
// AI가 읽는 글이나 저장하는 데이터처럼 일부러 남기는 줄은 i18n:keep 주석을 단다.
func TestLang_명령_코드에_한글_문자열이_없다(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || !hasHangul(lit.Value) {
				return true
			}
			pos := fset.Position(lit.Pos())
			if !strings.Contains(lines[pos.Line-1], "i18n:keep") {
				t.Errorf("%s: %s", pos, lit.Value)
			}
			return true
		})
	}
}

func TestLang_정리_사유는_보는_언어로_보인다(t *testing.T) {
	home(t)
	s := seed(t)
	a, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "가", Source: memory.Source{Kind: "session"}})
	b, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "나", Source: memory.Source{Kind: "session"}})
	p, err := s.Propose(memory.ProposalInput{Kind: memory.ProposalLink, Title: "잇기", MemoryIDs: []string{a.ID, b.ID}, Payload: memory.Payload{Note: "근거"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link(memory.LinkInput{From: a.ID, To: b.ID, Origin: "user"}); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "", "proposals", "approve", p.ID)
	if code != 0 || !strings.Contains(out, "settled. already linked.") || hasHangul(out) {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if got, _ := s.GetProposal(p.ID); got.Note != "이미 이어짐" {
		t.Fatalf("저장되는 사유는 한국어로 남는다: %q", got.Note)
	}
}
