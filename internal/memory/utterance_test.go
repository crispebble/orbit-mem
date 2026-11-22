package memory

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// 사용자 발언 기록과 사용자 말 대조(명세 S-4). 발언과 발췌는 모두 지어낸 중립 문구다.

func mustRecord(t *testing.T, s *Store, text string) {
	t.Helper()
	if err := s.RecordUtterance("host-x", text); err != nil {
		t.Fatal(err)
	}
}

func mustMatch(t *testing.T, s *Store, excerpt string) QuoteMatch {
	t.Helper()
	m, _, err := s.MatchQuote(excerpt)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// dumpRows is every row of every table of the DB as text, for looking for words that must not be kept.
func dumpRows(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	var buf bytes.Buffer
	for _, tb := range tables {
		r, err := s.DB().Query(`SELECT * FROM "` + tb + `"`)
		if err != nil {
			continue // FTS 그림자 표 일부는 직접 읽지 못한다: 파일 바이트 검사가 덮는다
		}
		cols, _ := r.Columns()
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				if b, ok := v.([]byte); ok {
					buf.Write(b)
				} else {
					fmt.Fprint(&buf, v)
				}
				buf.WriteByte('|')
			}
		}
		r.Close()
	}
	return buf.String()
}

func TestRecordUtterance_원문은_남기지_않고_조각_해시만_남긴다(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 다른 곳에 나올 리 없는 지어낸 낱말을 섞는다.
	words := []string{"자두나무숲", "청보리밭길", "Quillfeather", "자두나무숲 청보리밭길"}
	mustRecord(t, s, "자두나무숲 청보리밭길 옆에서 Quillfeather 배포를 금요일 오후에 하지 마")
	mustRecord(t, s, "청보리밭길") // 조각보다 짧은 발언(통째 해시)

	var utterances, shingles int
	if err := s.DB().QueryRow(`SELECT count(*) FROM utterances`).Scan(&utterances); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM utterance_shingles`).Scan(&shingles); err != nil {
		t.Fatal(err)
	}
	if utterances != 2 || shingles == 0 {
		t.Fatalf("utterances=%d shingles=%d", utterances, shingles)
	}
	dump := dumpRows(t, s)
	var files [][]byte
	for _, name := range []string{"memory.db", "memory.db-wal"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		files = append(files, b)
	}
	for _, w := range words {
		if bytes.Contains([]byte(dump), []byte(w)) {
			t.Errorf("표에 원문 낱말 %q가 남았다", w)
		}
		for i, b := range files {
			if bytes.Contains(b, []byte(w)) {
				t.Errorf("파일 %d에 원문 낱말 %q가 남았다", i, w)
			}
		}
	}
	// 해시는 이 DB의 솔트로 만든다: 솔트는 meta에 하나 있다.
	if salt := s.Meta(metaUtteranceSalt); len(salt) < 32 {
		t.Fatalf("salt = %q", salt)
	}
}

func TestMatchQuote_최근_30일_발언의_발췌면_matched다(t *testing.T) {
	s, c := newTestStore(t)
	mustRecord(t, s, "배포는 금요일 오후에 하지 말고 목요일까지 끝내 줘")
	c.t = c.t.Add(29 * 24 * time.Hour)
	for _, ex := range []string{
		"배포는 금요일 오후에 하지 말고",                // 발언의 앞부분
		"「배포는 금요일 오후에 하지 말고, 목요일까지 끝내 줘.」", // 따옴표·문장부호가 더해진 전체
		"금요일 오후에 하지 말고 목요일까지",              // 가운데
	} {
		if got := mustMatch(t, s, ex); got != QuoteMatched {
			t.Errorf("%q = %s", ex, got)
		}
	}
	// 30일이 지난 발언은 대조에 쓰지 않는다. 그 밖에 기록이 없으니 대조하지 못한다.
	c.t = c.t.Add(2 * 24 * time.Hour)
	if got := mustMatch(t, s, "배포는 금요일 오후에 하지 말고"); got != QuoteUnchecked {
		t.Fatalf("31일 뒤 = %s", got)
	}
}

func TestMatchQuote_없는_발췌는_not_found다(t *testing.T) {
	s, _ := newTestStore(t)
	mustRecord(t, s, "배포는 금요일 오후에 하지 말고 목요일까지 끝내 줘")
	mustRecord(t, s, "로그 파일은 7일 지나면 지워도 돼")
	for _, ex := range []string{
		"금요일에는 배포하지 않는다",                       // 뜻만 같은 바꿔 쓰기
		"배포는 금요일 오전에 하지 말고 목요일까지 끝내 줘",         // 낱말 하나를 바꿨다(뜻이 달라진다)
		"배포는 금요일 오후에 하지 말고 월요일 아침에 한꺼번에 올려",    // 앞은 발언, 뒤는 지어냈다
		"로그 파일은 7일 지나면 지워도 돼 배포는 금요일 오후에 하지 마", // 두 발언을 이어 붙였다
	} {
		if got := mustMatch(t, s, ex); got != QuoteNotFound {
			t.Errorf("%q = %s", ex, got)
		}
	}
}

func TestMatchQuote_발언_기록이_없으면_unchecked다(t *testing.T) {
	s, _ := newTestStore(t)
	if got := mustMatch(t, s, "배포는 금요일 오후에 하지 말고"); got != QuoteUnchecked {
		t.Fatalf("got %s", got)
	}
	// 문장부호만인 발췌는 기록이 있어도 대조할 낱말이 없다: 찾지 못한다.
	mustRecord(t, s, "로그 파일은 7일 지나면 지워도 돼")
	if got := mustMatch(t, s, "……!"); got != QuoteNotFound {
		t.Fatalf("문장부호만 = %s", got)
	}
}

// 낱말 한두 개짜리 발췌(「응」「그래」)는 그 말만 한 발언과 통째로 같을 때만 맞는다: 긴 발언 안의 흔한 두 낱말로 통과하지 않는다.
func TestMatchQuote_조각보다_짧은_발췌는_통째로_일치할_때만_matched다(t *testing.T) {
	s, _ := newTestStore(t)
	mustRecord(t, s, "응")
	mustRecord(t, s, "그래 그렇게 해")
	mustRecord(t, s, "시험 이름은 한글로 짓고 시나리오가 읽히게 해 줘")
	for ex, want := range map[string]QuoteMatch{
		"응":          QuoteMatched,
		"「응.」":       QuoteMatched,
		"그래":         QuoteNotFound, // 「그래 그렇게 해」의 일부일 뿐이다
		"해 줘":        QuoteNotFound, // 긴 발언 안의 두 낱말
		"그래 그렇게":     QuoteNotFound,
		"그래 그렇게 해":   QuoteMatched,
		"한글로 짓고":     QuoteNotFound,
		"이름은 한글로 짓고": QuoteMatched, // 세 낱말부터는 조각으로 맞춘다
	} {
		if got := mustMatch(t, s, ex); got != want {
			t.Errorf("%q = %s, want %s", ex, got, want)
		}
	}
}

// 조각 크기(quoteShingleWords)와 통과 비율(quoteMatchMin)을 정한 고정 묶음. 맞아야 할 발췌는 발언 그대로의 부분,
// 문장부호·대소문자가 다른 것, 「…」로 생략을 표시한 것이다. 맞지 않아야 할 발췌는 바꿔 쓰기, 낱말 하나 바꿔 뜻이
// 달라진 것, 앞만 발언이고 뒤는 지어낸 것, 두 발언을 이어 붙인 것, 짧은 일부, 표시 없이 낱말을 뺀 것이다.
func TestMatchQuote_고정_묶음을_조각_크기와_문턱이_가른다(t *testing.T) {
	s, _ := newTestStore(t)
	for _, u := range []string{
		"배포는 금요일 오후에 하지 말고 목요일까지 끝내 줘",
		"앞으로 커밋 메시지 본문은 한국어로 쓰고 제목은 영어 명령형으로 해",
		"응",
		"그래 그렇게 해",
		"시험 이름은 한글로 짓고 시나리오가 읽히게 해 줘. 그리고 시험 자료에는 지어낸 문구만 써",
		"로그 파일은 7일 지나면 지워도 돼",
		"이번 주에는 문서 정리만 하고 코드는 건드리지 마 다음 주에 리팩터링을 시작하자",
		"Use dry-run first when you run the deploy script",
	} {
		mustRecord(t, s, u)
	}
	matched := []string{
		"배포는 금요일 오후에 하지 말고",
		"커밋 메시지 본문은 한국어로 … 제목은 영어 명령형으로",
		"응",
		"시험 이름은 한글로 짓고 … 시험 자료에는 지어낸 문구만 써",
		"그래 그렇게 해",
		"로그 파일은 7일 지나면 지워도 돼.",
		"커밋 메시지 본문은 한국어로 쓰고 제목은 영어 명령형으로",
		"「배포는 금요일 오후에 하지 말고 목요일까지 끝내 줘」",
		"use DRY-RUN first when you run the deploy script",
	}
	notFound := []string{
		"금요일에는 배포하지 않는다",
		"커밋 메시지는 영어로 쓴다",
		"그래",
		"해 줘",
		"배포는 금요일 오후에 하지 말고 월요일 아침에 한꺼번에 올려",
		"로그 파일은 7일 지나면 지워도 돼 그래 그렇게 해",
		"배포는 금요일 오전에 하지 말고 목요일까지 끝내 줘",
		"다음 주에 문서 정리만 하고 리팩터링을 시작하자",
		"Never use dry-run for the deploy script",
		"시험 자료에는 실제 문구를 써",
		"커밋 메시지 본문은 한국어로, 제목은 영어 명령형으로", // 「쓰고」를 표시 없이 뺐다
	}
	for _, ex := range matched {
		if got := mustMatch(t, s, ex); got != QuoteMatched {
			t.Errorf("맞아야 함 %q = %s", ex, got)
		}
	}
	for _, ex := range notFound {
		if got := mustMatch(t, s, ex); got != QuoteNotFound {
			t.Errorf("맞지 않아야 함 %q = %s", ex, got)
		}
	}
}

// 같은 DB 파일을 따로 연 네 연결이 동시에 처음 발언을 기록해도 솔트는 하나로 정해진다: 네 연결이 읽는 솔트가 같고,
// 어느 연결이 남긴 발언이든 한 연결에서 대조된다.
func TestRecordUtterance_여러_연결이_동시에_처음_기록해도_솔트는_하나다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	var stores []*Store
	for range 4 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		stores = append(stores, s)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := s.RecordUtterance("host-x", fmt.Sprintf("발언 번호 %d 는 서로 다른 낱말 묶음 %d 이다", i, i)); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	salt := stores[0].Meta(metaUtteranceSalt)
	for i, s := range stores {
		if got := s.Meta(metaUtteranceSalt); got == "" || got != salt {
			t.Fatalf("연결 %d의 솔트 %q, 첫 연결 %q", i, got, salt)
		}
	}
	for i := range stores {
		if got := mustMatch(t, stores[0], fmt.Sprintf("발언 번호 %d 는 서로 다른 낱말 묶음 %d 이다", i, i)); got != QuoteMatched {
			t.Errorf("발언 %d = %s", i, got)
		}
	}
}

func TestTidy_30일_지난_발언_기록을_지운다(t *testing.T) {
	s, c := newTestStore(t)
	mustRecord(t, s, "로그 파일은 7일 지나면 지워도 돼")
	c.t = c.t.Add(20 * 24 * time.Hour)
	mustRecord(t, s, "배포는 금요일 오후에 하지 말고 목요일까지 끝내 줘")
	c.t = c.t.Add(11 * 24 * time.Hour) // 첫 발언은 31일, 둘째는 11일 지났다
	r := s.Tidy(TidyOptions{})
	if len(r.Errors) != 0 {
		t.Fatalf("errors = %v", r.Errors)
	}
	var utterances, orphans int
	_ = s.DB().QueryRow(`SELECT count(*) FROM utterances`).Scan(&utterances)
	_ = s.DB().QueryRow(`SELECT count(*) FROM utterance_shingles WHERE utterance_id NOT IN (SELECT id FROM utterances)`).Scan(&orphans)
	if utterances != 1 || orphans != 0 {
		t.Fatalf("utterances=%d orphans=%d", utterances, orphans)
	}
	if got := mustMatch(t, s, "배포는 금요일 오후에 하지 말고"); got != QuoteMatched {
		t.Fatalf("남은 발언 = %s", got)
	}
	if got := mustMatch(t, s, "로그 파일은 7일 지나면 지워도 돼"); got != QuoteNotFound {
		t.Fatalf("지운 발언 = %s", got)
	}
	// 지울 것이 없으면 0이다.
	if n, err := s.PruneUtterances(UtteranceDays); err != nil || n != 0 {
		t.Fatalf("다시 = %d %v", n, err)
	}
}

// 거절된(hidden) 기억과 같은 제목은 대조를 통과한 세 낱말 이상의 사용자 말이, 거절한 뒤에 한 발언과 맞을 때만
// 되살려 확정한다(S-2). 그 밖의 저장은 아무것도 쓰지 않고 Rejected와 까닭(NotRevived)으로 알린다.
func TestSave_거절된_제목은_거절_뒤에_한_대조_통과_사용자_말만_되살린다(t *testing.T) {
	s, c := newTestStore(t)
	in := SaveInput{Kind: KindFact, Title: "로그 보관은 7일", Body: "7일 지나면 지운다", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}}
	m, _ := s.Save(in)
	c.t = c.t.Add(time.Hour)
	if _, err := s.SetState(m.ID, StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	rejectedAt := c.t.UnixMilli()
	hidden, _ := s.Get(m.ID)
	c.t = c.t.Add(time.Hour)
	quote := func(excerpt string, matched bool, at int64) SaveInput {
		again := in
		again.Source, again.QuoteMatched, again.QuoteAt = Source{Kind: SourceQuote, Excerpt: excerpt}, matched, at
		return again
	}
	sessionAgain := in
	sessionAgain.Source = Source{Kind: SourceSession, Ref: "s2"}
	for _, c := range []struct {
		name string
		in   SaveInput
		why  ReviveRefusal
	}{
		{"AI가 배운 것", sessionAgain, ReviveNotChecked},
		{"대조하지 않은 사용자 말", quote("로그는 7일 지나면 지워도 돼", false, 0), ReviveNotChecked},
		{"두 낱말 사용자 말", quote("지워도 돼", true, rejectedAt+1), ReviveShortQuote},
		{"거절 전에 한 말", quote("로그는 7일 지나면 지워도 돼", true, rejectedAt-1), ReviveBeforeRejection},
		{"거절한 때와 같은 시각의 말", quote("로그는 7일 지나면 지워도 돼", true, rejectedAt), ReviveBeforeRejection},
	} {
		got, rep, err := s.SaveWith(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Rejected != m.ID || rep.NotRevived != c.why || rep.Revived || got.State != StateHidden {
			t.Fatalf("%s: rep=%+v state=%s", c.name, rep, got.State)
		}
		now, _ := s.Get(m.ID)
		if now.Version != hidden.Version || len(now.Sources) != len(hidden.Sources) || len(now.Days) != len(hidden.Days) {
			t.Fatalf("%s: 거절된 저장이 무언가 썼다: %+v", c.name, now)
		}
	}
	got, rep, err := s.SaveWith(quote("로그는 7일 지나면 지워도 돼", true, rejectedAt+1))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != "" || !rep.Revived || got.ID != m.ID || got.State != StateConfirmed || got.Version != hidden.Version+1 {
		t.Fatalf("되살림: rep=%+v got=%+v", rep, got)
	}
}

// 낱말 바꾸기·떼기·붙이기 표(6~30낱말). 발언은 지어낸 낱말 「낱말N」을 잇고 가운데에 부정 「안」을 둔다. 발췌는 그
// 발언 안쪽의 n낱말이다. 말 그대로와 「…」로 표시한 생략 하나는 맞고, 처음·끝·가운데 낱말 바꾸기, 표시 없는
// 낱말 떼기(부정 떼기 포함), 지어낸 머리·꼬리 붙이기는 길이와 관계없이 맞지 않는다.
func TestMatchQuote_낱말을_바꾸거나_떼거나_붙인_발췌는_길이와_관계없이_not_found다(t *testing.T) {
	word := func(i int) string { return fmt.Sprintf("낱말%d", i) }
	join := func(w []string) string { return strings.Join(w, " ") }
	for n := 6; n <= 30; n++ {
		s, _ := newTestStore(t)
		// 발언: 낱말0 … 낱말(n+3), 가운데(발췌의 가운데)에 「안」.
		var said []string
		for i := 0; i < n+3; i++ {
			said = append(said, word(i))
		}
		mid := 2 + n/2
		said = slices.Insert(said, mid, "안")
		mustRecord(t, s, join(said))
		ex := slices.Clone(said[2 : 2+n]) // 앞뒤 두 낱말을 뺀 안쪽 n낱말(「안」을 품는다)
		neg := slices.Index(ex, "안")
		replace := func(i int) []string { w := slices.Clone(ex); w[i] = "바꾼말"; return w }
		cases := []struct {
			name string
			text string
			want QuoteMatch
		}{
			{"말 그대로", join(ex), QuoteMatched},
			{"생략 하나", join(said[2:2+n/3]) + " … " + join(said[2+n/3+1:2+n+1]), QuoteMatched}, // n+1낱말에서 하나를 「…」로 뺐다
			{"생략 표시만 붙이기", join(ex[:n/3]) + " … " + join(ex[n/3:]), QuoteMatched},
			{"처음 바꾸기", join(replace(0)), QuoteNotFound},
			{"끝 바꾸기", join(replace(n - 1)), QuoteNotFound},
			{"가운데 바꾸기", join(replace(n / 3)), QuoteNotFound},
			{"부정 떼기", join(slices.Delete(slices.Clone(ex), neg, neg+1)), QuoteNotFound},
			{"표시 없이 떼기", join(slices.Delete(slices.Clone(ex), n/3, n/3+1)), QuoteNotFound},
			{"꼬리 붙이기", join(ex) + " 그리고 반대로 해", QuoteNotFound},
			{"머리 붙이기", "그건 틀렸고 " + join(ex), QuoteNotFound},
			{"끝을 부정으로 바꾸기", join(ex[:n-1]) + " 하지 마", QuoteNotFound},
		}
		for _, c := range cases {
			if got := mustMatch(t, s, c.text); got != c.want {
				t.Errorf("%d낱말 %s: %s, want %s (%q)", n, c.name, got, c.want, c.text)
			}
		}
	}
}

// 대조는 저장될 발췌(8KB로 자른 것)로 하고, 조각이 많아도 질의를 나눠 보내 결과를 합친다.
func TestMatchQuote_큰_발췌는_저장될_만큼만_나눠_대조한다(t *testing.T) {
	s, _ := newTestStore(t)
	var said []string
	for i := range 3000 {
		said = append(said, fmt.Sprintf("w%d", i))
	}
	text := strings.Join(said, " ") // 약 17KB: 8KB에서 잘려도 앞부분은 발언 그대로다
	mustRecord(t, s, text)
	if got := mustMatch(t, s, text); got != QuoteMatched {
		t.Fatalf("저장될 앞부분 = %s", got)
	}
	// 지어낸 낱말 34,000개: 변수 한도를 넘지 않고 not_found다.
	var made []string
	for i := range 34000 {
		made = append(made, fmt.Sprintf("x%d", i))
	}
	if got := mustMatch(t, s, strings.Join(made, " ")); got != QuoteNotFound {
		t.Fatalf("지어낸 큰 발췌 = %s", got)
	}
}

// tidy를 건너뛰어 31일 지난 발언이 남아 있어도, 최근 발언이 따로 있으면 옛 발언의 발췌는 not_found다(조각 질의도 30일만 본다).
func TestMatchQuote_tidy가_지우지_않은_옛_발언은_대조에_쓰지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	mustRecord(t, s, "로그 파일은 7일 지나면 지워도 돼")
	c.t = c.t.Add(31 * 24 * time.Hour)
	mustRecord(t, s, "배포는 금요일 오후에 하지 말고 목요일까지 끝내 줘")
	if got := mustMatch(t, s, "로그 파일은 7일 지나면 지워도 돼"); got != QuoteNotFound {
		t.Fatalf("옛 발언 = %s", got)
	}
}

// 4,000낱말을 넘는 발언은 앞 4,000과 끝 4,000낱말을 기록한다: 긴 붙여 넣기 뒤에 쓴 요청도 대조된다.
func TestRecordUtterance_긴_발언은_앞과_끝을_기록한다(t *testing.T) {
	s, _ := newTestStore(t)
	var said []string
	for i := range 9000 {
		said = append(said, fmt.Sprintf("w%d", i))
	}
	mustRecord(t, s, strings.Join(said, " "))
	for _, c := range []struct {
		from, to int
		want     QuoteMatch
	}{
		{0, 10, QuoteMatched},
		{8990, 9000, QuoteMatched},
		{4500, 4510, QuoteNotFound}, // 가운데는 기록하지 않았다
	} {
		if got := mustMatch(t, s, strings.Join(said[c.from:c.to], " ")); got != c.want {
			t.Errorf("%d~%d = %s, want %s", c.from, c.to, got, c.want)
		}
	}
}

// MatchQuote는 맞은 발언 가운데 가장 늦은 것의 기록 시각을 돌려준다(되살리기가 거절 시각과 견준다).
func TestMatchQuote_맞은_발언의_가장_늦은_시각을_준다(t *testing.T) {
	s, c := newTestStore(t)
	mustRecord(t, s, "로그 파일은 7일 지나면 지워도 돼")
	c.t = c.t.Add(time.Hour)
	mustRecord(t, s, "그래 로그 파일은 7일 지나면 지워도 돼")
	want := c.t.UnixMilli()
	c.t = c.t.Add(time.Hour)
	mustRecord(t, s, "배포는 금요일 오후에 하지 말고 목요일까지 끝내 줘")
	m, at, err := s.MatchQuote("로그 파일은 7일 지나면 지워도 돼")
	if err != nil || m != QuoteMatched || at != want {
		t.Fatalf("= %s %d %v, want at %d", m, at, err, want)
	}
	if _, at, _ := s.MatchQuote("금요일에는 배포하지 않는다"); at != 0 {
		t.Fatalf("not_found의 시각 = %d", at)
	}
}
