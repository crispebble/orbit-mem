package memory

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/place"
)

// 찾기 점수(결정 D4)의 성질 시험. 점수는 0~1이고, 기억 수와 결과 수에 흔들리지 않으며, 같은 내용이면 높고
// 낱말 하나만 겹치면 낮고, 제목 > 주제·태그 > 본문이며, 기억이 많을 때 흔한 낱말의 몫이 줄어든다.

func saveDoc(t *testing.T, s *Store, title, body, topic string, tags ...string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindFact, Title: title, Body: body, Topic: topic, Tags: tags, Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "rank"}})
	if err != nil {
		t.Fatalf("save %q: %v", title, err)
	}
	return m
}

func scoreOf(t *testing.T, s *Store, q, id string) float64 {
	t.Helper()
	hits, err := s.FindScored(FindInput{Query: q, AllProjects: true, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Memory.ID == id {
			return h.Score
		}
	}
	return 0
}

// filler는 질의 낱말과 거의 겹치지 않는 채움 기억이다. 셋에 하나는 「확인」, 다섯에 하나는 「배포」를 가진다.
func filler(t *testing.T, s *Store, i int) {
	t.Helper()
	heads := []string{"회의 메모", "커밋 제목", "말투 규칙", "포트 구조", "모의 데이터", "화면 밀도", "백업 주기"}
	body := fmt.Sprintf("채움 기억 %d번은 다른 일을 적는다", i)
	if i%3 == 0 {
		body += " 확인"
	}
	if i%5 == 0 {
		body += " 배포"
	}
	saveDoc(t, s, fmt.Sprintf("%s %d", heads[i%len(heads)], i), body, "")
}

func TestFindScore_점수는_0과_1_사이다(t *testing.T) {
	s, _ := newTestStore(t)
	saveDoc(t, s, "배포 스크립트는 드라이런이 먼저", "배포 스크립트를 처음 돌릴 때는 드라이런으로 본다", "배포 절차", "배포", "스크립트")
	saveDoc(t, s, "deploy 전에 smoke test", "deployment smoke deploy deploy", "")
	for i := 0; i < 10; i++ {
		filler(t, s, i)
	}
	for _, q := range []string{"배포", "배포 배포 배포", "deploy", "de", "배포 스크립트 드라이런 확인 절차 smoke", "확인", "채움 기억", "없는낱말"} {
		hits, err := s.FindScored(FindInput{Query: q, AllProjects: true, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			if !(h.Score > 0 && h.Score <= 1) || math.IsNaN(h.Score) {
				t.Errorf("%q → %q 점수 %v (0 초과 1 이하여야 함)", q, h.Memory.Title, h.Score)
			}
		}
	}
}

func TestFindScore_기억이_1_2_3_50건이어도_같은_문서와_같은_질의의_점수가_거의_같다(t *testing.T) {
	const q = "배포 드라이런 확인 절차"
	var scores []float64
	for _, n := range []int{1, 2, 3, 50} {
		s, _ := newTestStore(t)
		target := saveDoc(t, s, "배포 스크립트는 드라이런이 먼저", "처음 돌릴 때는 드라이런으로 본다", "")
		for i := 0; i < n-1; i++ {
			filler(t, s, i)
		}
		sc := scoreOf(t, s, q, target.ID)
		t.Logf("기억 %d건: %.4f", n, sc)
		scores = append(scores, sc)
	}
	lo, hi := scores[0], scores[0]
	for _, v := range scores {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if lo <= 0 || hi-lo > 0.1 {
		t.Fatalf("점수가 기억 수에 흔들린다: %v (차이 %.3f, 상한 0.1)", scores, hi-lo)
	}
}

func TestFindScore_낱말_하나_겹친_무관한_기억은_0점6_아래이고_같은_내용은_0점9_이상이다(t *testing.T) {
	for _, n := range []int{1, 2, 50} {
		s, _ := newTestStore(t)
		u := saveDoc(t, s, "커밋 제목은 영어 명령형", "첫 줄은 동사로 시작한다", "")
		long := saveDoc(t, s, "긴 기억의 제목", strings.Repeat("절차마다 서로 다른 낱말이 이어지는 아주 긴 본문 ", 4)+
			"가나다 라마바 사아자 차카타 파하가 orbit index search ranking normalize tokens bigram prefix weight", "")
		for i := 0; i < n-2; i++ {
			filler(t, s, i)
		}
		t.Logf("기억 %d건: 긴 질의 한 낱말 %.3f, 두 낱말 중 하나 %.3f, 같은 내용 %.3f, 긴 기억 같은 내용 %.3f", n,
			scoreOf(t, s, "배포 전에 스모크 시험을 돈다 배포 전 확인 절차와 커밋 기록", u.ID), scoreOf(t, s, "커밋 배포", u.ID),
			scoreOf(t, s, u.Title+" "+u.Body, u.ID), scoreOf(t, s, long.Title+" "+long.Body, long.ID))
		// 새로 저장하려는 글(제목 + 본문)이 「커밋」 하나만 겹친다.
		if sc := scoreOf(t, s, "배포 전에 스모크 시험을 돈다 배포 전 확인 절차와 커밋 기록", u.ID); sc >= 0.6 {
			t.Errorf("기억 %d건: 긴 질의에서 낱말 하나 겹친 무관한 기억 점수 %.3f ≥ 0.6", n, sc)
		}
		if sc := scoreOf(t, s, "커밋 배포", u.ID); sc >= 0.6 {
			t.Errorf("기억 %d건: 두 낱말 질의에서 하나 겹친 기억 점수 %.3f ≥ 0.6", n, sc)
		}
		if sc := scoreOf(t, s, u.Title+" "+u.Body, u.ID); sc < 0.9 {
			t.Errorf("기억 %d건: 같은 내용 다시 질의 점수 %.3f < 0.9", n, sc)
		}
		// 질의 토큰 상한(60)을 넘는 긴 기억도 같은 내용이면 0.9 이상이다.
		if sc := scoreOf(t, s, long.Title+" "+long.Body, long.ID); sc < 0.9 {
			t.Errorf("기억 %d건: 긴 기억 같은 내용 다시 질의 점수 %.3f < 0.9", n, sc)
		}
	}
}

func TestFindScore_제목_일치가_주제태그_일치보다_주제태그_일치가_본문_일치보다_높다(t *testing.T) {
	s, _ := newTestStore(t)
	title := saveDoc(t, s, "배포 순서", "먼저 본다", "")
	topic := saveDoc(t, s, "순서 하나", "먼저 본다", "배포")
	tag := saveDoc(t, s, "순서 둘", "먼저 본다", "", "배포")
	body := saveDoc(t, s, "순서 셋", "배포 먼저 본다", "")
	for i := 0; i < 5; i++ {
		filler(t, s, i+1) // 1~5: 「배포」는 5번에만
	}
	st, sp, sg, sb := scoreOf(t, s, "배포", title.ID), scoreOf(t, s, "배포", topic.ID), scoreOf(t, s, "배포", tag.ID), scoreOf(t, s, "배포", body.ID)
	t.Logf("제목 %.4f, 주제 %.4f, 태그 %.4f, 본문 %.4f", st, sp, sg, sb)
	if !(st > sp && sp > sb && st > sg && sg > sb) || math.Abs(sp-sg) > 1e-9 {
		t.Fatalf("제목 %.4f, 주제 %.4f, 태그 %.4f, 본문 %.4f: 제목 > 주제 = 태그 > 본문이어야 함", st, sp, sg, sb)
	}
	got, err := s.Find(FindInput{Query: "배포", AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, m := range got {
		pos[m.ID] = i + 1
	}
	if pos[title.ID] != 1 || !(pos[topic.ID] > 1 && pos[tag.ID] > 1 && pos[body.ID] > pos[topic.ID] && pos[body.ID] > pos[tag.ID]) {
		t.Fatalf("순위: %v", titlesOf(got))
	}
}

func TestFindScore_기억이_많으면_흔한_낱말만_맞은_기억의_점수가_낮아진다(t *testing.T) {
	const q = "문서 배포"
	small, _ := newTestStore(t)
	x := saveDoc(t, small, "문서 정리 규칙", "제목을 먼저 쓴다", "")
	saveDoc(t, small, "배포 절차", "스모크 시험을 돈다", "")
	before := scoreOf(t, small, q, x.ID)

	big, _ := newTestStore(t)
	x2 := saveDoc(t, big, "문서 정리 규칙", "제목을 먼저 쓴다", "")
	y2 := saveDoc(t, big, "배포 절차", "스모크 시험을 돈다", "")
	for i := 0; i < 40; i++ {
		saveDoc(t, big, fmt.Sprintf("회의 %d", i), fmt.Sprintf("이 문서는 %d번 회의를 적는다", i), "")
	}
	for i := 0; i < 8; i++ {
		filler(t, big, i*7+1) // 채움 8건(15·50번은 「배포」를 가진다)
	}
	after := scoreOf(t, big, q, x2.ID)
	rare := scoreOf(t, big, q, y2.ID)
	t.Logf("흔한 낱말만 맞음: 기억 2건 %.3f → 기억 %d건 %.3f, 드문 낱말만 맞음 %.3f", before, 50, after, rare)
	if !(after < before-0.2) {
		t.Fatalf("흔한 낱말 점수가 충분히 낮아지지 않았다: %.3f → %.3f", before, after)
	}
	if !(rare > after) {
		t.Fatalf("드문 낱말만 맞은 기억(%.3f)이 흔한 낱말만 맞은 기억(%.3f)보다 낮다", rare, after)
	}
}

func titlesOf(ms []Memory) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Title)
	}
	return out
}

func TestFind_빈_질의는_색인을_거치지_않고_확정_다음_최신_순이며_기호만인_질의는_아무것도_찾지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	q := Source{Kind: SourceQuote, Excerpt: "q"}
	a, _ := s.Save(SaveInput{Kind: KindFact, Title: "옛 확정", Scope: "product:demo", Source: q})
	c.t = c.t.Add(time.Hour)
	b := candidate(t, s, "새 후보")
	c.t = c.t.Add(time.Hour)
	d, _ := s.Save(SaveInput{Kind: KindFact, Title: "새 확정", Scope: ScopeGlobal, Source: q})
	// 색인 표를 지워도 빈 질의는 오류 없이 같은 순서를 낸다(FTS를 거치지 않는다).
	if _, err := s.DB().Exec(`DROP TABLE memory_fts`); err != nil {
		t.Fatal(err)
	}
	here := &place.Place{Products: []string{"demo"}}
	if got, err := s.Find(FindInput{Query: "!!! ??? ---", Place: here}); err != nil || len(got) != 0 {
		t.Fatalf("기호만인 질의(지금과 같이 결과 없음): %v %v", titlesOf(got), err)
	}
	for _, qs := range []string{"", "   \t\n"} {
		got, err := s.Find(FindInput{Query: qs, Place: here})
		if err != nil {
			t.Fatalf("%q: %v", qs, err)
		}
		want := []string{d.ID, a.ID, b.ID} // 확정(새 → 옛) 다음 후보, 지금 제품은 보지 않는다
		var ids []string
		for _, m := range got {
			ids = append(ids, m.ID)
		}
		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Errorf("%q: %v, want 새 확정, 옛 확정, 새 후보", qs, titlesOf(got))
		}
	}
}

func TestFind_같은_점수면_확정_다음_지금_제품_다음_최신이다(t *testing.T) {
	s, c := newTestStore(t)
	q := Source{Kind: SourceQuote, Excerpt: "q"}
	save := func(title, scope string, src Source) Memory {
		c.t = c.t.Add(time.Hour)
		m, err := s.Save(SaveInput{Kind: KindFact, Title: title, Scope: scope, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	ses := Source{Kind: SourceSession, Ref: "x"}
	candHere := save("배포 순서 가", "product:demo", ses)
	confGlobalOld := save("배포 순서 나", ScopeGlobal, q)
	confHere := save("배포 순서 다", "product:demo", q)
	confGlobalNew := save("배포 순서 라", ScopeGlobal, q)
	got, err := s.Find(FindInput{Query: "배포 순서", Place: &place.Place{Products: []string{"demo"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{confHere.ID, confGlobalNew.ID, confGlobalOld.ID, candHere.ID}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("순위 %v, want 다(확정·여기), 라(확정·새), 나(확정·옛), 가(후보)", titlesOf(got))
	}
}

func TestFind_한글은_조사가_달라도_찾는다(t *testing.T) {
	s, _ := newTestStore(t)
	a := saveDoc(t, s, "배포를 먼저 한다", "", "")
	b := saveDoc(t, s, "배포", "", "")
	saveDoc(t, s, "커밋 규칙", "제목은 명령형", "")
	has := func(q string, ids ...string) {
		t.Helper()
		got, err := s.Find(FindInput{Query: q, AllProjects: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			found := false
			for _, m := range got {
				found = found || m.ID == id
			}
			if !found {
				t.Errorf("%q로 %s를 못 찾음: %v", q, id, titlesOf(got))
			}
		}
		for _, m := range got {
			if m.Title == "커밋 규칙" {
				t.Errorf("%q가 무관한 기억을 찾음", q)
			}
		}
	}
	has("배포", a.ID, b.ID)     // 「배포를」 저장 → 「배포」로 찾음
	has("배포를", a.ID, b.ID)    // 「배포」 저장 → 「배포를」로 찾음
	has("배포가", a.ID, b.ID)    // 조사가 다른 질의
	has("배포는 언제", a.ID, b.ID) // 어절 여럿
}

func TestFind_라틴_접두_일치는_찾되_정확한_일치보다_낮다(t *testing.T) {
	s, _ := newTestStore(t)
	exact := saveDoc(t, s, "deploy 절차", "", "")
	prefix := saveDoc(t, s, "deployment 절차", "", "")
	saveDoc(t, s, "redeploy 절차", "", "") // 접두가 아니면 안 찾는다
	got, err := s.FindScored(FindInput{Query: "Deploy", AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Memory.ID != exact.ID || got[1].Memory.ID != prefix.ID || !(got[0].Score > got[1].Score) {
		var ts []string
		for _, h := range got {
			ts = append(ts, fmt.Sprintf("%s=%.3f", h.Memory.Title, h.Score))
		}
		t.Fatalf("got %v, want deploy 절차 > deployment 절차", ts)
	}
}

func TestFind_FTS_문법_글자가_든_질의도_오류_없이_찾는다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveDoc(t, s, "배포 OR 순서", "NEAR(a b) \"따옴표\" col:val -x ^y", "")
	for _, q := range []string{`"배포`, `OR`, `NEAR(a b)`, `col:val`, `-x ^y *`, `"따옴표"`, `배포 AND NOT 순서`} {
		got, err := s.Find(FindInput{Query: q, AllProjects: true})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(got) != 1 || got[0].ID != m.ID {
			t.Errorf("%q: %v", q, titlesOf(got))
		}
	}
}

// 옛 실행 파일(색인을 모름)이 SQL로 고친 기억은 새 글로 찾고 옛 글로는 찾지 않는다. 늦은 기억도 색인된 기억과
// 같은 점수 함수를 받는다: 채운 뒤에도 점수가 같다.
func TestFind_늦은_기억은_훑어서_새_글로_찾고_옛_글로는_못_찾으며_점수는_색인된_뒤와_같다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := saveDoc(t, s, "원래 제목 기억", "처음 본문 alphaword", "")
	saveDoc(t, s, "다른 기억 하나", "본문 legacyedit 없음 처음", "")
	s.Close()
	added := oldBinaryWrites(t, path, changed.ID, 3)

	r, _, err := OpenRead(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := r.StaleIndexCount(); n != 4 {
		t.Fatalf("늦은 기억 %d, want 4", n)
	}
	find := func(st *Store, q string) []Hit {
		t.Helper()
		hits, err := st.FindScored(FindInput{Query: q, AllProjects: true})
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}
	if hits := find(r, "alphaword"); len(hits) != 0 {
		t.Fatalf("옛 글(alphaword)로 고친 기억을 찾음: %+v", hits)
	}
	hits := find(r, "legacyedit") // 고친 기억과, 본문에 처음부터 legacyedit가 있던 기억(순서는 저장 시각이 가른다)
	if len(hits) != 2 || hits[0].Memory.ID != changed.ID && hits[1].Memory.ID != changed.ID {
		t.Fatalf("새 글(legacyedit)로 못 찾음: %+v", hits)
	}
	staleScores := map[string]float64{}
	for _, q := range []string{"legacyedit", "옛 실행 파일 기억 1", "처음 본문"} {
		for _, h := range find(r, q) {
			staleScores[q+"|"+h.Memory.ID] = h.Score
		}
	}
	if got := find(r, "옛 실행 파일 기억 1"); len(got) < 3 || got[0].Memory.ID != added[1] {
		t.Fatalf("옛 실행 파일이 더한 기억을 못 찾음: %+v", got)
	}
	if n, _ := r.StaleIndexCount(); n != 4 {
		t.Fatalf("읽기가 색인을 채웠다: 늦은 기억 %d", n)
	}
	r.Close()

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.FillIndex(0); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"legacyedit", "옛 실행 파일 기억 1", "처음 본문"} {
		for _, h := range find(w, q) {
			if v, ok := staleScores[q+"|"+h.Memory.ID]; !ok || math.Abs(v-h.Score) > 1e-12 {
				t.Errorf("%q %s: 늦을 때 %.6f, 색인 뒤 %.6f", q, h.Memory.Title, v, h.Score)
			}
		}
	}
}

func TestFind_색인_표가_없는_판4_DB는_모두_훑어서_찾는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	v4DB(t, path, "배포를 먼저 한다", "커밋 규칙")
	r, _, err := OpenRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if ok, _ := r.IndexReady(); ok {
		t.Fatal("색인 표가 있다")
	}
	got, err := r.Find(FindInput{Query: "배포", AllProjects: true})
	if err != nil || len(got) != 1 || got[0].Title != "배포를 먼저 한다" {
		t.Fatalf("got %v %v", titlesOf(got), err)
	}
}

func TestFindScored_범위를_주면_그_범위의_기억만_점수와_함께_낸다(t *testing.T) {
	s, _ := newTestStore(t)
	q := Source{Kind: SourceSession, Ref: "x"}
	in, _ := s.Save(SaveInput{Kind: KindFact, Title: "배포 순서", Scope: "product:demo", Source: q})
	s.Save(SaveInput{Kind: KindFact, Title: "배포 순서 전역", Scope: ScopeGlobal, Source: q})
	s.Save(SaveInput{Kind: KindFact, Title: "배포 순서 다른", Scope: "product:other", Source: q})
	for _, f := range []FindInput{
		{Query: "배포 순서", Scope: "product:demo", Limit: 3},
		{Query: "배포 순서", Scope: "product:demo", AllProjects: true, Limit: 3},
		{Query: "배포 순서", Scope: "product:demo", Place: &place.Place{Products: []string{"other"}}, Limit: 3},
	} {
		got, err := s.FindScored(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Memory.ID != in.ID || got[0].Score < 0.9 {
			t.Fatalf("%+v: got %+v", f, got)
		}
	}
}
