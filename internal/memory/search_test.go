package memory

import (
	"fmt"
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

func TestStrongMatch_약하면_아무것도_고르지_않는다(t *testing.T) {
	docs := []Doc{
		{ID: "1", Title: "codex exec를 백그라운드로 돌릴 때 stdin을 닫는다", Body: "codex exec < /dev/null"},
		{ID: "2", Title: "배포 전 전체 시험은 단독으로 돌린다", Body: "make check"},
		{ID: "3", Title: "한국어 검색 bigram 실측", Body: "pg_trgm"},
	}
	if got := StrongMatches(docs, "오늘 점심 뭐 먹지", 3); len(got) != 0 {
		t.Fatalf("weak prompt matched %v", got)
	}
	if got := StrongMatches(docs, "그 파일 좀 열어줘 codex", 3); len(got) != 0 {
		t.Fatalf("single-word overlap must be weak, got %v", got)
	}
	got := StrongMatches(docs, "codex exec를 백그라운드에서 돌렸더니 멈춰요", 3)
	if len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("strong prompt = %v", got)
	}
}

func TestStrongMatch_최대_개수를_넘지_않는다(t *testing.T) {
	var docs []Doc
	for i := 0; i < 6; i++ {
		docs = append(docs, Doc{ID: string(rune('a' + i)), Title: "launchd 회고 예약 설정", Body: "launchd plist"})
	}
	if got := StrongMatches(docs, "launchd 회고 예약을 다시 설정", 3); len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
}

// 흔들리는 시험(r6_test.go:64)의 원인: 요청 「확정 규칙 7」의 「7」은 한 글자라 낱말에서 빠져서
// 제목이 「확정 규칙 N」인 30개가 같은 점수가 되고, 순서가 무작위 id에 걸렸다. 요청에 제목이
// 그대로 나오면(또는 요청 전체가 제목에 나오면) 그 기억이 앞이고, 같은 점수의 순서는 id와 무관하게 정해진다.
func TestStrong_요청에_제목이_그대로_나오면_그_기억이_앞이다(t *testing.T) {
	var docs []Doc
	for i := 0; i < 30; i++ {
		docs = append(docs, Doc{ID: fmt.Sprintf("m-%02d", 29-i), Title: fmt.Sprintf("확정 규칙 %d", i), Body: fmt.Sprintf("규칙 %d", i)})
	}
	for q, want := range map[string]string{"확정 규칙 7": "확정 규칙 7", "확정 규칙 7 다시 봐 줘": "확정 규칙 7", "확정 규칙 27": "확정 규칙 27"} {
		hits := Strong(docs, q)
		if len(hits) == 0 || hits[0].Doc.Title != want || hits[1].Score == hits[0].Score {
			t.Fatalf("%q: first %v", q, hits)
		}
	}
	// 같은 점수끼리는 제목 순(같으면 id 순)이다: 입력 순서를 뒤집어도 결과가 같다.
	a := Strong(docs, "확정 규칙 고치기")
	rev := append([]Doc(nil), docs...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	b := Strong(rev, "확정 규칙 고치기")
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("len %d %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Doc.ID != b[i].Doc.ID {
			t.Fatalf("order depends on input order at %d: %s vs %s", i, a[i].Doc.ID, b[i].Doc.ID)
		}
	}
}
