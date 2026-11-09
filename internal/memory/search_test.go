package memory

import (
	"testing"
)

// 옛 Knowledge(TS)의 점수 규칙을 그대로 옮겼는지 확인한다.
func TestRelevance_한글_조사가_붙어도_앞부분_두글자로_반점을_준다(t *testing.T) {
	doc := Doc{Title: "배포 전 전체 시험은 단독으로", Body: "무거운 작업과 겹치지 않게 돌린다"}
	r := Relevance(doc, "시험을")
	if r.Score != 0.5 {
		t.Fatalf("score = %v, want 0.5", r.Score)
	}
	if r.Title != 0.5 {
		t.Fatalf("title = %v, want 0.5", r.Title)
	}
}

func TestRelevance_완전일치는_1점_두글자_한글어는_부분점수_없음(t *testing.T) {
	doc := Doc{Title: "codex exec 백그라운드", Body: "stdin을 닫아야 함"}
	if got := Relevance(doc, "codex").Score; got != 1 {
		t.Fatalf("score = %v, want 1", got)
	}
	if got := Relevance(doc, "배포").Score; got != 0 {
		t.Fatalf("two-char hangul term must not match partially, got %v", got)
	}
}

func TestRelevance_여러_단어가_붙어서_나오면_1점을_더한다(t *testing.T) {
	doc := Doc{Title: "x", Body: "codex exec 를 쓴다"}
	if got := Relevance(doc, "codex exec").Score; got != 3 {
		t.Fatalf("score = %v, want 3", got)
	}
}

func TestRelevance_태그와_NFKC_소문자를_본다(t *testing.T) {
	doc := Doc{Title: "제목", Body: "", Tags: []string{"Ｇｏ"}}
	if got := Relevance(doc, "go").Score; got != 1 {
		t.Fatalf("score = %v, want 1", got)
	}
}

func TestTerms_구분자와_중복과_상한(t *testing.T) {
	got := Terms("a, b;c|a  d", 16)
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("terms = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("terms = %v", got)
		}
	}
}
