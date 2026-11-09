package memory

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// 저장 전 판정(설계 6.1, T-26). SaveCheck는 같은 제목 → 같은 주제 → 같은 scope의 비슷한 기억 순으로 보고,
// 읽기만 한다. 내부 Save는 이 판정을 하지 않는다.

func checkIn(title, body, topic string, tags ...string) SaveInput {
	return SaveInput{Kind: KindFact, Title: title, Body: body, Topic: topic, Tags: tags, Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "check"}}
}

func mustCheck(t *testing.T, s *Store, in SaveInput, min float64) SaveCheckResult {
	t.Helper()
	r, err := s.SaveCheck(in, min)
	if err != nil {
		t.Fatalf("save check %q: %v", in.Title, err)
	}
	return r
}

func similarIDs(r SaveCheckResult) []string {
	var ids []string
	for _, h := range r.Similar {
		ids = append(ids, h.ID)
	}
	return ids
}

func TestSaveCheck_같은_종류_범위_제목이면_단축_판정이고_그_기억을_가리킨다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveDoc(t, s, "배포 스크립트는 드라이런이 먼저", "처음 돌릴 때는 드라이런으로 본다", "")
	// 제목 표기 차이(공백·대소문자)는 TitleKey가 같으면 같은 제목이다.
	r := mustCheck(t, s, checkIn("  배포   스크립트는 드라이런이 먼저 ", "다른 본문", "배포 절차"), 0)
	if r.Verdict != CheckSameTitle || len(r.Existing) != 1 || r.Existing[0] != m.ID || len(r.Similar) != 0 {
		t.Fatalf("same title: %+v", r)
	}
	// 거절된(hidden) 판도 Save가 단축하니 같은 제목이다.
	if _, err := s.SetState(m.ID, StateHidden, "거절"); err != nil {
		t.Fatal(err)
	}
	if r := mustCheck(t, s, checkIn("배포 스크립트는 드라이런이 먼저", "", ""), 0); r.Verdict != CheckSameTitle || r.Existing[0] != m.ID {
		t.Fatalf("hidden same title: %+v", r)
	}
	// 종류가 다르면 같은 제목 단축이 아니다(Save도 새로 만든다).
	in := checkIn("배포 스크립트는 드라이런이 먼저", "", "")
	in.Kind = KindLesson
	if r := mustCheck(t, s, in, 0); r.Verdict == CheckSameTitle {
		t.Fatalf("other kind: %+v", r)
	}
	// 들여오기(LegacyID)는 Save가 단축하지 않는다.
	in = checkIn("배포 스크립트는 드라이런이 먼저", "", "")
	in.LegacyID = "old-1"
	if r := mustCheck(t, s, in, 0); r.Verdict == CheckSameTitle {
		t.Fatalf("legacy: %+v", r)
	}
}

func TestSaveCheck_같은_범위_주제의_현재값이_있으면_대체_판정이다(t *testing.T) {
	s, _ := newTestStore(t)
	old := saveDoc(t, s, "배포는 금요일에 하지 않는다", "금요일 배포 금지", "배포 요일")
	r := mustCheck(t, s, checkIn("배포는 목요일까지만 한다", "목요일까지", " 배포  요일 "), 0)
	if r.Verdict != CheckSameTopic || len(r.Existing) != 1 || r.Existing[0] != old.ID || len(r.Similar) != 0 {
		t.Fatalf("same topic: %+v", r)
	}
	// 휘발 기억은 주제를 대체하지 않는다(Save와 같은 조건): 대체 판정이 아니다.
	in := checkIn("이번 주만 배포 쉼", "이번 주만", "배포 요일")
	in.Retention = RetentionVolatile
	if r := mustCheck(t, s, in, 0); r.Verdict == CheckSameTopic {
		t.Fatalf("volatile: %+v", r)
	}
	// 다른 범위의 같은 주제는 대체하지 않는다.
	in = checkIn("배포는 수요일", "수요일", "배포 요일")
	in.Scope = "project:onidot"
	if r := mustCheck(t, s, in, 0); r.Verdict == CheckSameTopic {
		t.Fatalf("other scope: %+v", r)
	}
	// 낡은 판만 남은 주제는 현재값이 없다.
	if _, err := s.SetState(old.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if r := mustCheck(t, s, checkIn("배포는 아무 날", "", "배포 요일"), 0); r.Verdict == CheckSameTopic {
		t.Fatalf("stale topic: %+v", r)
	}
}

func TestSaveCheck_같은_제목이_같은_주제보다_먼저다(t *testing.T) {
	s, _ := newTestStore(t)
	a := saveDoc(t, s, "말투는 존댓말", "늘 존댓말", "말투")
	saveDoc(t, s, "말투 예외 없음", "예외 없음", "말투 예외")
	r := mustCheck(t, s, checkIn("말투는 존댓말", "다시", "말투 예외"), 0)
	if r.Verdict != CheckSameTitle || r.Existing[0] != a.ID {
		t.Fatalf("title first: %+v", r)
	}
}

func TestSaveCheck_비슷한_기억은_같은_범위의_후보_확정에서_점수_순_상위_3이고_문턱_아래는_뺀다(t *testing.T) {
	s, _ := newTestStore(t)
	body := "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"
	var near []Memory
	for i, ti := range []string{"배포 스크립트는 드라이런이 먼저", "배포 스크립트 드라이런 먼저 돌리기", "드라이런 먼저, 배포 스크립트", "배포 스크립트 첫 실행은 드라이런"} {
		m := saveDoc(t, s, ti, body, "")
		if i == 1 { // 다른 종류도 본다: 같은 내용이 규칙·사실로 갈라지는 중복
			kind := KindLesson
			if _, err := s.Update(UpdateInput{ID: m.ID, Kind: &kind}); err != nil {
				t.Fatal(err)
			}
		}
		near = append(near, m)
	}
	far := saveDoc(t, s, "커밋 메시지는 한글로", "커밋 메시지 제목과 본문은 한글로 쓰고 배포 이야기는 넣지 않는다", "")
	other := saveDocIn(t, s, "project:onidot", "배포 스크립트는 드라이런이 먼저다", body)
	stale := saveDoc(t, s, "배포 스크립트 드라이런", body, "")
	if _, err := s.SetState(stale.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	r := mustCheck(t, s, checkIn("배포 스크립트는 드라이런을 먼저 돌린다", body, ""), 0.3)
	if r.Verdict != CheckSimilar || len(r.Similar) != SimilarLimit || len(r.Existing) != 0 {
		t.Fatalf("similar: %+v", r)
	}
	for i, h := range r.Similar {
		if h.Score < 0.3 || h.Score > 1 || (i > 0 && h.Score > r.Similar[i-1].Score) {
			t.Fatalf("scores not ranked: %+v", r.Similar)
		}
		if h.ID == far.ID || h.ID == other.ID || h.ID == stale.ID {
			t.Fatalf("far, other scope or stale memory in similar: %+v", r.Similar)
		}
		if h.Title == "" || h.Kind == "" || h.Summary == "" {
			t.Fatalf("similar item: %+v", h)
		}
	}
	// 문턱을 높이면 문턱 아래는 빠지고, 아무도 넘지 못하면 새 기억 판정이다.
	// 낱말 하나(배포)만 겹치는 새 기억: 낮은 문턱에는 걸리고 높은 문턱·기본 문턱(0 이하)에는 걸리지 않는다.
	one := checkIn("배포 일정 공지", "다음 주 배포 일정을 팀에 알린다", "")
	if r := mustCheck(t, s, one, 0.01); r.Verdict != CheckSimilar {
		t.Fatalf("one shared word, low threshold: %+v", r)
	}
	for _, min := range []float64{0.65, 0, -1} {
		if r := mustCheck(t, s, one, min); r.Verdict != CheckNew || len(r.Similar) != 0 {
			t.Fatalf("below threshold %v: %+v", min, r)
		}
	}
	_ = near
}

func saveDocIn(t *testing.T, s *Store, scope, title, body string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindFact, Title: title, Body: body, Scope: scope, Project: strings.TrimPrefix(scope, "project:"),
		Source: Source{Kind: SourceSession, Ref: "rank"}})
	if err != nil {
		t.Fatalf("save %q: %v", title, err)
	}
	return m
}

func TestSaveCheck_요약은_200룬_이하_한_줄이고_주제를_싣는다(t *testing.T) {
	s, _ := newTestStore(t)
	long := strings.Repeat("드라이런 배포 스크립트\n먼저 ", 60)
	m := saveDoc(t, s, "드라이런 배포 스크립트 먼저", long, "드라이런")
	r := mustCheck(t, s, checkIn("드라이런 배포 스크립트를 먼저", long, ""), 0.3)
	if r.Verdict != CheckSimilar || len(r.Similar) != 1 || r.Similar[0].ID != m.ID {
		t.Fatalf("similar: %+v", r)
	}
	h := r.Similar[0]
	if n := utf8.RuneCountInString(h.Summary); n > 200 || n < 150 || strings.Contains(h.Summary, "\n") || h.Topic != "드라이런" {
		t.Fatalf("summary %d runes %q topic %q", n, h.Summary, h.Topic)
	}
}

func TestSaveCheck_판정은_읽기만_한다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	body := "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 먼저 본다"
	saveDoc(t, w, "배포 스크립트는 드라이런이 먼저", body, "")
	saveDoc(t, w, "배포 요일", "금요일 배포 금지", "배포 요일")
	w.Close()
	oldBinaryWrites(t, path, "", 3) // 늦은 줄 셋: 판정이 색인을 채우지 않는지 본다
	// 쓰기가 모두 실패하는 연결에서도 판정은 된다.
	ro, _, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	for _, in := range []SaveInput{checkIn("배포 스크립트는 드라이런이 먼저", "", ""), checkIn("배포 요일 바꿈", "", "배포 요일"),
		checkIn("배포 스크립트 드라이런 먼저 돌린다", body, ""), checkIn("전혀 다른 것", "", "")} {
		if _, err := ro.SaveCheck(in, 0.3); err != nil {
			t.Fatalf("read-only check %q: %v", in.Title, err)
		}
	}
	rw, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	before := [4]int{countRows(t, rw, "memories"), countRows(t, rw, "memory_versions"), countRows(t, rw, "sources"), staleCount(t, rw)}
	if before[3] == 0 {
		t.Fatal("fixture has no late index rows")
	}
	if r := mustCheck(t, rw, checkIn("배포 스크립트 드라이런 먼저 돌린다", body, ""), 0.3); r.Verdict != CheckSimilar {
		t.Fatalf("check: %+v", r)
	}
	mustCheck(t, rw, checkIn("배포 스크립트는 드라이런이 먼저", "", ""), 0.3)
	after := [4]int{countRows(t, rw, "memories"), countRows(t, rw, "memory_versions"), countRows(t, rw, "sources"), staleCount(t, rw)}
	if before != after {
		t.Fatalf("check wrote: before %v after %v", before, after)
	}
}

func TestSaveCheck_입력이_틀리면_Save와_같은_오류다(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.SaveCheck(SaveInput{Kind: "nope", Title: "x", Source: Source{Kind: SourceSession}}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err %v", err)
	}
}

func TestSave_내부_저장은_비슷한_기억이_있어도_바로_저장한다(t *testing.T) {
	s, _ := newTestStore(t)
	body := "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"
	a := saveDoc(t, s, "배포 스크립트는 드라이런이 먼저", body, "")
	in := checkIn("배포 스크립트는 드라이런을 먼저 돌린다", body, "")
	if r := mustCheck(t, s, in, 0.3); r.Verdict != CheckSimilar || r.Similar[0].ID != a.ID {
		t.Fatalf("check: %+v", r)
	}
	b, err := s.Save(in)
	if err != nil || b.ID == a.ID {
		t.Fatalf("internal save %+v %v", b, err)
	}
	if n, _ := s.Count(); n != 2 {
		t.Fatalf("count %d", n)
	}
}

type scoreCase struct {
	name        string
	base, probe [2]string // title, body
	similar     bool
}

var scoreCases = []scoreCase{
	{"ko 조사 바꿈", [2]string{"배포 스크립트는 드라이런을 먼저 돌린다", "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"},
		[2]string{"배포 스크립트는 드라이런이 먼저다", "운영 배포 스크립트는 처음 돌릴 때 드라이런으로 바뀌는 것을 먼저 본 뒤에 실제로 돌린다"}, true},
	{"ko 어순", [2]string{"배포 스크립트는 드라이런을 먼저 돌린다", "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"},
		[2]string{"드라이런을 먼저 돌린다, 배포 스크립트는", "실제로 돌리기 전에 드라이런으로 바뀔 것을 먼저 본다, 운영 배포 스크립트를 처음 돌릴 때는"}, true},
	{"ko 낱말 둘 바꿈", [2]string{"배포 스크립트는 드라이런을 먼저 돌린다", "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"},
		[2]string{"배포 스크립트는 시험 실행을 먼저 돌린다", "운영 배포 스크립트를 처음 돌릴 때는 시험 실행으로 바뀔 것을 먼저 확인하고 나서 실제로 돌린다"}, true},
	{"ko 말투 규칙", [2]string{"사용자에게는 늘 존댓말로 답한다", "사용자가 반말로 말해도 짧은 확인이나 오류 보고에서도 존댓말 어미를 생략하지 않는다"},
		[2]string{"사용자에게 항상 존댓말로 답한다", "사용자가 반말을 써도 짧은 확인·오류 보고에서 존댓말 어미를 빼지 않는다"}, true},
	{"en reword", [2]string{"Run the deploy script with dry-run first", "When running the production deploy script for the first time, run it with dry-run to see what changes before the real run"},
		[2]string{"Run the deploy script in dry-run mode first", "When you run the production deploy script the first time, use dry-run to see what changes before the real run"}, true},
	{"en order", [2]string{"Run the deploy script with dry-run first", "When running the production deploy script for the first time, run it with dry-run to see what changes before the real run"},
		[2]string{"Dry-run first when you run the deploy script", "Before the real run, see what changes with dry-run when running the production deploy script for the first time"}, true},
	{"en two words", [2]string{"Write commit messages in English", "Commit message subjects and bodies are written in English, in the imperative mood"},
		[2]string{"Write commit messages in Korean", "Commit message subjects and bodies are written in Korean, in the plain style"}, true},
	{"ko 다른 기억 배포", [2]string{"배포 스크립트는 드라이런을 먼저 돌린다", "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"},
		[2]string{"배포 뒤에는 스모크 시험을 돌린다", "배포 직후 스모크 시험으로 첫 화면과 로그인이 되는지 확인한다"}, false},
	{"ko 다른 기억 무관", [2]string{"배포 스크립트는 드라이런을 먼저 돌린다", "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"},
		[2]string{"커밋 메시지는 한글로 쓴다", "커밋 제목과 본문은 한글로 쓰고 영어 낱말은 그대로 둔다"}, false},
	{"ko 다른 기억 같은 낱말 여럿", [2]string{"사용자에게는 늘 존댓말로 답한다", "사용자가 반말로 말해도 짧은 확인이나 오류 보고에서도 존댓말 어미를 생략하지 않는다"},
		[2]string{"사용자에게 묻기 전에 먼저 찾아본다", "사용자에게 확인을 묻기 전에 저장소와 문서에서 답을 먼저 찾아본다"}, false},
	{"en 다른 기억 deploy", [2]string{"Run the deploy script with dry-run first", "When running the production deploy script for the first time, run it with dry-run to see what changes before the real run"},
		[2]string{"Check the deploy logs after a release", "After each release, read the deploy logs for errors and warnings before closing the ticket"}, false},
	{"en 다른 기억 commit", [2]string{"Write commit messages in English", "Commit message subjects and bodies are written in English, in the imperative mood"},
		[2]string{"Never commit to the main branch", "Create a feature branch first; commits go to main only through a reviewed pull request"}, false},
	{"ko 짧은 바꿈", [2]string{"사용자에게 존댓말로 답한다", ""}, [2]string{"사용자에게는 존댓말로 답하기", ""}, true},
	{"en 짧은 바꿈", [2]string{"Use Docker for databases", ""}, [2]string{"Use Docker for the database", ""}, true},
	{"ko 짧은 다른 기억", [2]string{"배포 스크립트 점검", ""}, [2]string{"배포 일정 공지", ""}, false},
	{"ko 짧은 다른 기억2", [2]string{"사용자에게 존댓말로 답한다", ""}, [2]string{"사용자에게 먼저 묻는다", ""}, false},
	{"en 짧은 다른 기억", [2]string{"Use Docker for databases", ""}, [2]string{"Use Homebrew for tools", ""}, false},
}

// 기본 문턱(DefaultSimilarMin)을 정한 사례다. 같은 내용을 조금 바꿔 다시 저장한 것(조사·어순·한두 낱말, 한글·영어,
// 긴 본문과 제목뿐)은 문턱 이상이고, 낱말이 겹쳐도 다른 기억은 문턱 아래다. 기억 1건과 51건 DB에서 함께 본다.
// 잰 값(2026-11-09): 바꿔 쓴 것 0.691~0.893, 다른 기억 0.000~0.662(짧은 제목이 머리 낱말 「사용자에게」를 나눔).
func TestSaveCheck_기본_문턱은_바꿔_쓴_기억과_다른_기억을_가른다(t *testing.T) {
	for _, n := range []int{0, 50} {
		for _, c := range scoreCases {
			s, _ := newTestStore(t)
			for i := 0; i < n; i++ {
				filler(t, s, i)
			}
			b := saveDoc(t, s, c.base[0], c.base[1], "")
			low, err := s.SaveCheck(checkIn(c.probe[0], c.probe[1], ""), 1e-9)
			if err != nil {
				t.Fatal(err)
			}
			score := 0.0
			for _, h := range low.Similar {
				if h.ID == b.ID {
					score = h.Score
				}
			}
			t.Logf("기억 %d건 %s: %.3f", n+1, c.name, score)
			r := mustCheck(t, s, checkIn(c.probe[0], c.probe[1], ""), 0)
			if got := slices.Contains(similarIDs(r), b.ID); got != c.similar {
				t.Errorf("기억 %d건 %s: score %.3f, similar %v, want %v (min %.2f)", n+1, c.name, score, got, c.similar, DefaultSimilarMin)
			}
		}
	}
}
