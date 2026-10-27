package memory

import "testing"

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
