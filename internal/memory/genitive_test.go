package memory

import (
	"slices"
	"testing"
)

// 「한 글자 명사+의」(1-6 b6rs): 두 글자 어절 「X의」는 낱말만 보고는 「법의」(법+의)인지 「회의」(한 낱말)인지 가를 수 없다.
// 살아 있는 기억의 통째 낱말에 X가 있고 「X의」가 없을 때만 X로 뗀다.

func TestGenitiveBase_두_글자_X의에서_기능어가_아닌_X만_후보다(t *testing.T) {
	for in, want := range map[string]string{
		"법의": "법", "회의": "회", "앱의": "앱", // 후보일 뿐: 뗄지는 저장소가 정한다
		"안의": "", "의의": "", // 기능어 X
		"방법의": "", "의": "", "법을": "", "x의": "", "api의": "", // 두 글자 한글 「X의」가 아님(세 글자 이상은 stemWord가 뗀다)
	} {
		got, ok := genitiveBase(in)
		if ok != (want != "") || got != want {
			t.Errorf("genitiveBase(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	// 낱말만 보는 떼기는 그대로다: 두 글자 「X의」는 떼지 않는다.
	for _, w := range []string{"법의", "회의", "모의"} {
		if got := stemWord(w); got != w {
			t.Errorf("stemWord(%q) = %q", w, got)
		}
	}
}

func TestRecallWords_기억에_한_글자_명사가_있으면_X의를_뗀다(t *testing.T) {
	s, _ := newTestStore(t)
	a := saveDoc(t, s, "법 조항은 원문으로 인용한다", "요약하지 않는다", "")
	saveDoc(t, s, "방법의 순서는 문서에 적는다", "", "") // 「방법의」는 바이그램 「법의」를 갖지만 통째 낱말 「법의」가 아니다
	prompt := "법의 조항 인용 방식"
	words, err := s.RecallWords(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"법", "조항", "인용", "방식"}; !slices.Equal(words, want) {
		t.Fatalf("회상 낱말 %v, want %v", words, want)
	}
	hits, err := s.RecallHits(prompt)
	if err != nil {
		t.Fatal(err)
	}
	var ha *RecallHit
	for i := range hits {
		if hits[i].Memory.ID == a.ID {
			ha = &hits[i]
		}
	}
	if ha == nil || !slices.Equal(ha.Matched, []string{"법", "조항", "인용"}) {
		t.Fatalf("법 기억 %+v", ha)
	}
	if want := scoreOfSkip(t, s, "법 조항 인용 방식", a.ID); ha.Score != want {
		t.Fatalf("회상 점수 %v, 뗀 낱말의 찾기 점수 %v", ha.Score, want)
	}
	// 뗀 X가 요청의 다른 낱말과 같으면 한 번만 센다.
	if words, _ := s.RecallWords("법의 법 조항"); !slices.Equal(words, []string{"법", "조항"}) {
		t.Fatalf("회상 낱말 %v", words)
	}
	// 조사가 붙은 꼴(「법을」)만 있어도 통째 낱말 법이다(뗀 꼴).
	s2, _ := newTestStore(t)
	saveDoc(t, s2, "새 법을 먼저 읽는다", "", "")
	if words, _ := s2.RecallWords("법의 조항"); !slices.Equal(words, []string{"법", "조항"}) {
		t.Fatalf("회상 낱말 %v", words)
	}
}

func TestRecallWords_기억에_X의가_있으면_그대로_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	saveDoc(t, s, "회의 기록은 다음 날 공유한다", "", "")
	saveDoc(t, s, "점검은 회 단위로 센다", "", "")          // X도 있지만 「회의」가 낱말로 있으니 떼지 않는다
	saveDoc(t, s, "모의를 쓰는 시험은 통합 시험이 아니다", "", "") // 뗀 꼴 「모의」(모의를 → 모의)도 낱말이다
	saveDoc(t, s, "모 단위 견적", "", "")
	for prompt, want := range map[string][]string{
		"회의 기록 공유": {"회의", "기록", "공유"},
		"모의 객체 시험": {"모의", "객체", "시험"},
	} {
		words, err := s.RecallWords(prompt)
		if err != nil || !slices.Equal(words, want) {
			t.Fatalf("%q → %v (err %v), want %v", prompt, words, err, want)
		}
	}
}

func TestRecallWords_기억에_X가_없으면_X의는_그대로_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	saveDoc(t, s, "칸막이 너비는 넓게", "", "")      // 「칸」으로 시작하는 낱말이지만 통째 낱말 칸이 아니다
	saveDoc(t, s, "숨김 상태 기억은 안 보인다", "", "") // 낡음·숨김 기억은 살아 있는 기억이 아니다
	hidden := saveDoc(t, s, "칸 너비 규칙", "", "")
	if _, err := s.SetState(hidden.ID, StateHidden, "시험"); err != nil {
		t.Fatal(err)
	}
	words, err := s.RecallWords("칸의 너비 규칙")
	if err != nil || !slices.Equal(words, []string{"칸의", "너비", "규칙"}) {
		t.Fatalf("회상 낱말 %v (err %v)", words, err)
	}
	// 저장소가 비어도 그대로다.
	empty, _ := newTestStore(t)
	if words, err := empty.RecallWords("칸의 너비"); err != nil || !slices.Equal(words, []string{"칸의", "너비"}) {
		t.Fatalf("빈 저장소 회상 낱말 %v (err %v)", words, err)
	}
}

func TestRecallWords_같은_입력에_같은_출력(t *testing.T) {
	s, _ := newTestStore(t)
	saveDoc(t, s, "법 조항은 원문으로 인용한다", "", "")
	saveDoc(t, s, "앱 설정은 한 파일에 둔다", "", "")
	saveDoc(t, s, "회의 기록은 다음 날 공유한다", "", "")
	for i := 0; i < 6; i++ {
		filler(t, s, i)
	}
	prompt := "앱의 설정과 법의 조항, 회의 기록, 칸의 너비"
	first, err := s.RecallWords(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"앱", "설정", "법", "조항", "회의", "기록", "칸의", "너비"}; !slices.Equal(first, want) {
		t.Fatalf("회상 낱말 %v, want %v", first, want)
	}
	h1, _ := s.RecallHits(prompt)
	for i := 0; i < 5; i++ {
		again, _ := s.RecallWords(prompt)
		h2, _ := s.RecallHits(prompt)
		if !slices.Equal(again, first) || len(h2) != len(h1) {
			t.Fatalf("%d번째: %v / %d건, 처음 %v / %d건", i, again, len(h2), first, len(h1))
		}
		for j := range h1 {
			if h1[j].Memory.ID != h2[j].Memory.ID || h1[j].Score != h2[j].Score || !slices.Equal(h1[j].Matched, h2[j].Matched) {
				t.Fatalf("%d번째 회상이 다릅니다: %+v / %+v", i, h1[j], h2[j])
			}
		}
	}
}

func TestTaskRecallWords_위임_글도_같은_규칙으로_뗀다(t *testing.T) {
	s, _ := newTestStore(t)
	a := saveDoc(t, s, "앱 전역 상태는 한 저장소에 둔다", "", "")
	saveDoc(t, s, "회의 기록은 다음 날 공유한다", "", "")
	task := "## 목표\n이 앱의 전역 상태를 옮긴다. 회의 기록도 본다."
	words, err := s.TaskRecallWords(task)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(words, "앱") || slices.Contains(words, "앱의") || !slices.Contains(words, "회의") {
		t.Fatalf("위임 낱말 %v", words)
	}
	hits, err := s.TaskRecallHitsIn(task)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Memory.ID == a.ID && !slices.Contains(h.Matched, "앱") {
			t.Fatalf("앱 기억 맞은 낱말 %v", h.Matched)
		}
	}
}

// wholeWordsAmong은 wholeWords 가운데 want에 든 것과 같다(첫 글자로 걸러 떼기만 줄인다).
func TestWholeWordsAmong_wholeWords와_같다(t *testing.T) {
	text := Normalize("새 법을 읽고 회의를 연다. 법 조항(원문)은 기회를 봐서 모의를 쓰고, 칸막이 법의 앱은 api의 「키」")
	want := map[string]bool{"법": true, "법의": true, "회": true, "회의": true, "모": true, "모의": true, "칸": true, "칸의": true, "앱": true, "키": true, "키의": true}
	got := map[string]bool{}
	wholeWordsAmong(text, want, got)
	all := wholeWords(text)
	for w := range want {
		if got[w] != all[w] {
			t.Errorf("%q: wholeWordsAmong %v, wholeWords %v", w, got[w], all[w])
		}
	}
}
