package memory

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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
	m, err := s.MatchQuote(excerpt)
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

// Review Focus 3: 낱말 한두 개짜리 발췌(「응」「그래」)는 그 말만 한 발언과 통째로 같을 때만 맞는다.
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
// 문장부호·대소문자·생략 표시가 다른 것이다. 맞지 않아야 할 발췌는 바꿔 쓰기, 낱말 하나 바꿔 뜻이 달라진 것,
// 앞만 발언이고 뒤는 지어낸 것, 두 발언을 이어 붙인 것, 짧은 일부다.
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
		"커밋 메시지 본문은 한국어로, 제목은 영어 명령형으로",
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

// 두 프로세스가 같은 DB에 처음 발언을 기록해도 솔트는 하나로 정해져, 어느 쪽이 남긴 발언이든 대조된다.
func TestRecordUtterance_동시에_처음_기록해도_솔트는_하나다(t *testing.T) {
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

// S-2(도메인): 거절된(hidden) 기억과 같은 제목은 대조를 통과한 사용자 말만 되살려 확정한다. 그 밖의 저장은
// 아무것도 쓰지 않고 Rejected로 알린다.
func TestSave_거절된_제목은_대조_통과_사용자_말만_되살린다(t *testing.T) {
	s, _ := newTestStore(t)
	in := SaveInput{Kind: KindFact, Title: "로그 보관은 7일", Body: "7일 지나면 지운다", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}}
	m, _ := s.Save(in)
	if _, err := s.SetState(m.ID, StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	hidden, _ := s.Get(m.ID)
	for _, src := range []Source{{Kind: SourceSession, Ref: "s2"}, {Kind: SourceQuote, Excerpt: "로그 보관은 7일"}} {
		again := in
		again.Source = src // 사용자 말이어도 대조 통과(QuoteMatched)가 없으면 되살리지 않는다
		got, rep, err := s.SaveWith(again)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Rejected != m.ID || got.State != StateHidden {
			t.Fatalf("%s: rep=%+v state=%s", src.Kind, rep, got.State)
		}
		now, _ := s.Get(m.ID)
		if now.Version != hidden.Version || len(now.Sources) != len(hidden.Sources) || len(now.Days) != len(hidden.Days) {
			t.Fatalf("%s: 거절된 저장이 무언가 썼다: %+v", src.Kind, now)
		}
	}
	again := in
	again.Source, again.QuoteMatched = Source{Kind: SourceQuote, Excerpt: "로그 보관은 7일"}, true
	got, rep, err := s.SaveWith(again)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != "" || got.ID != m.ID || got.State != StateConfirmed || got.Version != hidden.Version+1 {
		t.Fatalf("되살림: rep=%+v got=%+v", rep, got)
	}
}
