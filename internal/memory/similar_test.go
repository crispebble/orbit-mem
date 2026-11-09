package memory

import (
	"math"
	"slices"
	"testing"
)

// 저장 때 비슷함의 경계(1-6 b6d 2차, b6db). 낱말을 바꿔 다시 쓴 짧은 제목은 막거나 가까운 기억으로 보이고, 한쪽이 다른
// 쪽에 다 들어 있거나 머리 낱말 하나만 겹치는 짝은 1/3 이하다(기본 막음 문턱 0.69와 0.35 넘게 떨어진다).

type simPair struct {
	name        string
	base, probe [2]string // title, body
}

// 낱말을 바꿔 다시 쓴 짧은 제목: 숫자 표기·서술어·어순·어미가 바뀌고 내용 낱말 절반 남짓만 같다. holdout2 중복 사례 가운데
// 옛 점수(바이그램 산술 평균, 0.422~0.607)로 0.61에 놓친 넷과 같은 꼴을 다른 낱말로 새로 썼다.
var rewordedShortTitles = []simPair{
	{"숫자 표기·서술어", [2]string{"로그 정리는 매주 월요일 02시에 돈다", ""}, [2]string{"로그 정리는 매주 월요일 새벽 2시에 실행된다", ""}},
	{"서술어 하나·본문 더함", [2]string{"배포는 사용자가 시킬 때만 한다", ""},
		[2]string{"배포는 사용자가 요청할 때만 한다", "사람이 시키지 않으면 운영에 올리지 않는다."}},
	{"어순·어미", [2]string{"회의가 길어지면 안건을 줄이지 회의를 늘리지 않는다", ""},
		[2]string{"회의가 길어지면 회의를 늘리지 말고 안건을 줄인다", ""}},
	{"낱말 둘 바꿈", [2]string{"리뷰는 가장 나쁜 경우를 따로 적는다", ""}, [2]string{"리뷰할 때는 최악의 경우를 따로 적어 둔다", ""}},
}

// 비슷하다고 보면 안 되는 짝: 짧은 기억이 긴 다른 글에 다 들어 있음, 머리 낱말(「사용자에게」「배포」「Use … for」)만 겹침.
var oneSidedPairs = []simPair{
	{"짧은 기억이 긴 글에 다 들어 있음", [2]string{"배포 일정", ""}, [2]string{"배포 일정 때문에 미룬 일은 회의록 끝에 모아 둔다",
		"회의에서 배포 일정 때문에 미룬 일은 회의록 끝의 남은 일 칸에 담당자와 함께 모아 두고 다음 회의 첫머리에 다시 본다"}},
	{"「사용자에게」만 겹침", [2]string{"사용자에게 존댓말로 답한다", ""}, [2]string{"사용자에게 먼저 묻는다", ""}},
	{"「배포」만 겹침", [2]string{"배포 스크립트 점검", ""}, [2]string{"배포 일정 공지", ""}},
	{"「Use … for」만 겹침", [2]string{"Use Docker for databases", ""}, [2]string{"Use Homebrew for tools", ""}},
}

func pairScore(t *testing.T, p simPair) float64 {
	t.Helper()
	s, _ := newTestStore(t)
	b := saveDoc(t, s, p.base[0], p.base[1], "")
	return simScore(t, s, checkIn(p.probe[0], p.probe[1], ""), b.ID)
}

// 1-6 b6db에 다시 정의했다. b6d 2차에는 「기본 문턱 이상(막음)」이었다. 그 뜻은 AI가 정한 것이고, 사용자 결정 A(막는
// 판정은 엄격하게, 막지 않을 때 가까운 기억을 붙인다)에서 낱말을 바꿔 쓴 같은 주장은 가까운 기억의 몫이다: 막음을 이
// 짝들까지 내리면 주제 낱말만 같은 새 사실(0.667~0.668)도 막는다(savecheck_strict_test.go). 그래서 기본 판정에서 막히거나
// (Similar) 막지 않고 저장할 때 가까운 기억(Near)으로 보이면 된다. 지금 값: 0.463~0.750, 그 가운데 어순·어미만 막는다.
func TestSaveCheck_낱말을_바꿔_다시_쓴_짧은_제목은_막거나_가까운_기억으로_보인다(t *testing.T) {
	for _, p := range rewordedShortTitles {
		s, _ := newTestStore(t)
		b := saveDoc(t, s, p.base[0], p.base[1], "")
		in := checkIn(p.probe[0], p.probe[1], "")
		sc := simScore(t, s, in, b.ID)
		r := mustCheckMins(t, s, in, CheckMins{})
		t.Logf("%s: %.3f %s", p.name, sc, r.Verdict)
		if !slices.Contains(similarIDs(r), b.ID) && !slices.Contains(nearIDs(r), b.ID) {
			t.Errorf("%s: score %.3f, neither blocked (min %.2f) nor close (floor %.2f): %+v", p.name, sc, DefaultSimilarMin, DefaultNearMin, r)
		}
	}
}

func TestSaveCheck_한쪽만_덮거나_머리_낱말만_겹치는_짝은_3분의_1_이하다(t *testing.T) {
	for _, p := range oneSidedPairs {
		sc := pairScore(t, p)
		t.Logf("%s: %.3f", p.name, sc)
		if sc > 1.0/3+1e-9 {
			t.Errorf("%s: score %.3f > 1/3", p.name, sc)
		}
	}
}

// 두 기억 사이 비슷함은 짝만 보고 순서를 가리지 않는다: 같은 짝은 몇 번을 재도, 어느 쪽을 새 글로 두어도 같은 값이다.
// 같은 낱말 집합이면 1, 같은 낱말이 없으면 0이다.
func TestSimilarity_같은_입력은_같은_값이고_짝의_순서와_무관하다(t *testing.T) {
	for _, p := range append(append([]simPair{}, rewordedShortTitles...), oneSidedPairs...) {
		a := memoryTokens(p.base[0], "", nil, p.base[1])
		b := memoryTokens(p.probe[0], "", nil, p.probe[1])
		first := similarity(a, b)
		for i := 0; i < 3; i++ {
			if got := similarity(memoryTokens(p.base[0], "", nil, p.base[1]), memoryTokens(p.probe[0], "", nil, p.probe[1])); got != first {
				t.Fatalf("%s: %v then %v", p.name, first, got)
			}
		}
		if back := similarity(b, a); math.Abs(back-first) > 1e-12 {
			t.Errorf("%s: a→b %.6f, b→a %.6f", p.name, first, back)
		}
	}
	same := memoryTokens("커밋 메시지는 한글로 쓴다", "", nil, "")
	if sc := similarity(same, memoryTokens("한글로 쓴다, 커밋 메시지를", "", nil, "")); math.Abs(sc-1) > 1e-9 {
		t.Errorf("same words: %.6f, want 1", sc)
	}
	if sc := similarity(same, memoryTokens("Use Docker for databases", "", nil, "")); sc != 0 {
		t.Errorf("no shared word: %.6f, want 0", sc)
	}
	if sc := similarity(same, memoryTokens("", "", nil, "")); sc != 0 {
		t.Errorf("empty: %.6f, want 0", sc)
	}
}

// 같은 낱말의 다른 꼴은 반만 맞는다(1-6 b6db): 영어 낱말은 네 글자 이상인 한쪽이 다른 쪽의 앞부분이면(databases·database),
// 한글 낱말은 첫 글자가 같고 둘째 글자가 받침만 다르면(답한다·답하기, 돌린다·돌리고) 앞 두 글자 맞음과 같이 0.5다. 낱말
// 셋 가운데 하나만 꼴이 바뀐 짧은 제목은 5/6이고, 첫 글자가 다르거나 영어 앞부분이 세 글자 이하면 맞지 않는다.
func TestSimilarity_같은_낱말의_다른_꼴은_반만_맞는다(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"Use Docker for databases", "Use Docker for the database", 5.0 / 6},
		{"사용자에게 존댓말로 답한다", "사용자에게는 존댓말로 답하기", 5.0 / 6},
		{"배포 스크립트 돌린다", "배포 스크립트 돌리고", 5.0 / 6},
		{"Use Docker tools", "User Docker tools", 2.0 / 3}, // use·user: 앞부분 세 글자
		{"사용자에게 존댓말로 답한다", "사용자에게 존댓말로 합하기", 2.0 / 3},      // 첫 글자가 다르다
	} {
		sc := similarity(memoryTokens(c.a, "", nil, ""), memoryTokens(c.b, "", nil, ""))
		if math.Abs(sc-c.want) > 1e-9 {
			t.Errorf("%q · %q: %.6f, want %.6f", c.a, c.b, sc, c.want)
		}
	}
}
