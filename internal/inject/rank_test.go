package inject

// 순위 함수(명세 R-1·R-3): 점수 차가 문턱보다 크면 점수가 앞서고, 문턱 안이면 확정이 앞서고, 점수와 확정이 같으면 재검토
// 후보가 뒤에 온다. 기본 설정은 스위치(rankByScore)가 켜져 있고(문턱 0.1), 끄면 확정, 점수, 맞은 비율, ... 순이다.

import (
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

// ranked is the default settings with the score-first rank on and margin 0.1, spelled out so a test of the
// on rule does not lean on the default.
func ranked() config.Config {
	cfg := config.Default()
	cfg.RankByScore = true
	cfg.RankScoreMargin = 0.1
	return cfg
}

// rankedOff is the default settings with the score-first rank switched off: confirmed first, then score. A test
// of the off rule uses it instead of the default, which is on.
func rankedOff() config.Config {
	cfg := config.Default()
	cfg.RankByScore = false
	cfg.RankScoreMargin = 0.1 // 문턱이 있어도 스위치가 꺼져 있으면 쓰지 않는다
	return cfg
}

// saveRank saves a global memory: confirmed (a user's words) or a candidate (a session). With reviewDue its
// review day is one day after it was saved and the clock is moved past it.
func saveRank(t *testing.T, s *memory.Store, title string, confirmed, reviewDue bool) memory.Memory {
	t.Helper()
	later(s, time.Minute)
	src := memory.Source{Kind: memory.SourceSession, Ref: "x"}
	if confirmed {
		src = memory.Source{Kind: memory.SourceQuote, Excerpt: title}
	}
	in := memory.SaveInput{Kind: memory.KindLesson, Title: title, Body: title, Scope: memory.ScopeGlobal, Topic: title, Source: src}
	if reviewDue {
		in.ReviewAfterDays = 1
	}
	m, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func rankIDs(t *testing.T, s *memory.Store, cfg config.Config, hits []memory.RecallHit) []string {
	t.Helper()
	vw, _ := s.Viewer()
	var ids []string
	for _, h := range rankHits(s, cfg, vw, place.Place{}, "", hits) {
		ids = append(ids, h.Memory.ID)
	}
	return ids
}

func TestRank_점수_차가_문턱보다_크면_후보가_약한_확정보다_앞이다(t *testing.T) {
	s := store(t)
	weak := saveRank(t, s, "약하게 걸린 확정 기억", true, false)
	strong := saveRank(t, s, "잘 맞는 후보 기억", false, false)
	hits := []memory.RecallHit{{Memory: weak, Score: 0.4, Coverage: 0.5}, {Memory: strong, Score: 0.8, Coverage: 0.5}}
	if got, want := rankIDs(t, s, ranked(), hits), []string{strong.ID, weak.ID}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestRank_문턱_안이면_확정이_앞이다(t *testing.T) {
	s := store(t)
	confirmed := saveRank(t, s, "조금 덜 맞는 확정 기억", true, false)
	cand := saveRank(t, s, "조금 더 맞는 후보 기억", false, false)
	hits := []memory.RecallHit{{Memory: cand, Score: 0.8, Coverage: 0.5}, {Memory: confirmed, Score: 0.72, Coverage: 0.5}}
	if got, want := rankIDs(t, s, ranked(), hits), []string{confirmed.ID, cand.ID}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	// 차가 문턱과 같으면 큰 차가 아니다(문턱보다 커야 점수가 앞선다).
	hits = []memory.RecallHit{{Memory: cand, Score: 0.8}, {Memory: confirmed, Score: 0.7}}
	if got, want := rankIDs(t, s, ranked(), hits), []string{confirmed.ID, cand.ID}; !slices.Equal(got, want) {
		t.Fatalf("at margin: order %v, want %v", got, want)
	}
}

func TestRank_점수와_확정이_같으면_재검토_후보가_뒤다(t *testing.T) {
	s := store(t)
	// 재검토 후보가 더 늦게 고쳐졌다: 마지막 판정(고친 시각)이 재검토 표시를 가리지 않는다는 시험이다.
	fresh := saveRank(t, s, "최근 확정 기억", true, false)
	old := saveRank(t, s, "오래된 확정 기억", true, true)
	later(s, 48*time.Hour)
	vw, _ := s.Viewer()
	if !vw.Of(old).ReviewDue || vw.Of(fresh).ReviewDue {
		t.Fatalf("review due old=%v fresh=%v", vw.Of(old).ReviewDue, vw.Of(fresh).ReviewDue)
	}
	hits := []memory.RecallHit{{Memory: old, Score: 0.6, Coverage: 0.5}, {Memory: fresh, Score: 0.6, Coverage: 0.5}}
	for _, cfg := range []config.Config{ranked(), rankedOff()} { // 스위치와 관계없이 같은 조건의 둘이다
		if got, want := rankIDs(t, s, cfg, slices.Clone(hits)), []string{fresh.ID, old.ID}; !slices.Equal(got, want) {
			t.Fatalf("byScore %v: order %v, want %v", cfg.RankByScore, got, want)
		}
	}
	// 점수가 다르면 재검토 후보라도 점수가 앞선다(점수가 같을 때만 뒤로 간다).
	hits = []memory.RecallHit{{Memory: fresh, Score: 0.5}, {Memory: old, Score: 0.6}}
	if got, want := rankIDs(t, s, ranked(), hits), []string{old.ID, fresh.ID}; !slices.Equal(got, want) {
		t.Fatalf("unequal scores: order %v, want %v", got, want)
	}
}

func TestRank_rankByScore를_끄면_지금_순위와_같다(t *testing.T) {
	s := store(t)
	weak := saveRank(t, s, "약하게 걸린 확정 기억", true, false)
	strong := saveRank(t, s, "잘 맞는 후보 기억", false, false)
	strong2 := saveRank(t, s, "더 잘 맞는 후보 기억", false, false)
	hits := []memory.RecallHit{{Memory: strong, Score: 0.8, Coverage: 0.5}, {Memory: strong2, Score: 0.9, Coverage: 0.5}, {Memory: weak, Score: 0.4, Coverage: 0.5}}
	cfg := rankedOff()
	// 꺼짐의 순위: 확정이 먼저, 그다음 점수.
	if got, want := rankIDs(t, s, cfg, hits), []string{weak.ID, strong2.ID, strong.ID}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// 기본 설정(입력 문 켬)에서 두 기억이 모두 문턱을 넘으면 도구 직전 순위도 같은 규칙이다: 입력과 훨씬 잘 맞는 후보 교훈이
// 약하게 맞는 확정 교훈보다 앞선다. 입력 문이 약한 쪽을 거르지 않았다는 것도 함께 본다.
func TestRank_입력_문을_지난_두_기억도_같은_순위_규칙을_쓴다(t *testing.T) {
	s := store(t)
	weak := saveOn(t, s, memory.KindLesson, "빗금 하나로 결과가 갈리는 일이 있다", "경로 끝 대상을 살핀다", []string{"Bash"}, true)
	strong := saveOn(t, s, memory.KindLesson, "rsync 원본 경로 끝에 빗금을 붙인다", "빗금이 없으면 폴더째 복사된다", []string{"Bash"}, false)
	cfg := config.Default()
	if !cfg.ToolInputGate {
		t.Fatal("toolInputGate is off by default")
	}
	input := "rsync -av 원본 경로 빗금 대상"
	got := ToolInjection(s, cfg, here, "claude", "S", "Bash", input).MemoryIDs
	if want := []string{strong.ID, weak.ID}; !slices.Equal(got, want) {
		t.Fatalf("gate on: order %v, want %v", got, want)
	}
}

// 도구 직전 회상도 같은 순위 규칙이다: 확정 규칙이 아닌 기억끼리는 입력과 훨씬 잘 맞는 후보 교훈이 약하게 맞는 확정 교훈보다
// 앞서고(점수 차가 문턱보다 큼), rankByScore를 끄거나 입력이 없으면 확정이 앞선 지금 순서다.
func TestRank_도구_직전_회상도_같은_순위_규칙을_쓴다(t *testing.T) {
	s := store(t)
	weak := saveOn(t, s, memory.KindLesson, "빗금 하나로 결과가 갈리는 일이 있다", "경로 끝을 살핀다", []string{"Bash"}, true)
	strong := saveOn(t, s, memory.KindLesson, "rsync 원본 경로 끝에 빗금을 붙인다", "빗금이 없으면 폴더째 복사된다", []string{"Bash"}, false)
	n := 0
	order := func(cfg config.Config, input string) []string {
		cfg.ToolInputGate = false // 이 시험은 순위만 본다: 입력 문이 약한 기억을 먼저 거르지 않게 끈다
		n++                       // 호출마다 다른 세션이라 앞 호출의 표시가 남지 않는다
		return ToolInjection(s, cfg, here, "claude", "S"+string(rune('a'+n)), "Bash", input).MemoryIDs
	}
	const input = "rsync -av 원본 경로 빗금 대상"
	on := ranked()
	if got, want := order(on, input), []string{strong.ID, weak.ID}; !slices.Equal(got, want) {
		t.Fatalf("on: order %v, want %v", got, want)
	}
	if got, want := order(config.Default(), input), []string{strong.ID, weak.ID}; !slices.Equal(got, want) {
		t.Fatalf("default: order %v, want %v", got, want)
	}
	if got, want := order(rankedOff(), input), []string{weak.ID, strong.ID}; !slices.Equal(got, want) {
		t.Fatalf("off: order %v, want %v", got, want)
	}
	if got, want := order(on, ""), []string{weak.ID, strong.ID}; !slices.Equal(got, want) {
		t.Fatalf("no input: order %v, want %v", got, want)
	}
}

// 기본 설정(켬, 문턱 0.1)의 순서: 점수 차가 문턱보다 크면 약하게 걸린 확정보다 잘 맞는 후보가 앞서고, 차가 문턱 안이면
// 확정이 앞선다. 재검토 지남은 점수와 확정이 같을 때만 뒤로 간다.
func TestRank_기본_설정은_점수_차가_문턱보다_클_때만_후보가_확정_앞이다(t *testing.T) {
	if cfg := config.Default(); !cfg.RankByScore || cfg.RankScoreMargin != 0.1 {
		t.Fatalf("default rankByScore %v margin %v, want on and 0.1", cfg.RankByScore, cfg.RankScoreMargin)
	}
	s := store(t)
	weak := saveRank(t, s, "약하게 걸린 확정 기억", true, false)
	strong := saveRank(t, s, "잘 맞는 후보 기억", false, false)
	near := saveRank(t, s, "조금 덜 맞는 확정 기억", true, false)
	cand := saveRank(t, s, "조금 더 맞는 후보 기억", false, false)
	def := config.Default()
	// 점수 차 0.4가 문턱 0.1보다 크다: 후보가 앞선다.
	hits := []memory.RecallHit{{Memory: weak, Score: 0.4, Coverage: 0.5}, {Memory: strong, Score: 0.8, Coverage: 0.5}}
	if got, want := rankIDs(t, s, def, hits), []string{strong.ID, weak.ID}; !slices.Equal(got, want) {
		t.Fatalf("large gap: order %v, want %v", got, want)
	}
	// 점수 차 0.08이 문턱 안이다: 확정이 앞선다.
	hits = []memory.RecallHit{{Memory: cand, Score: 0.8, Coverage: 0.5}, {Memory: near, Score: 0.72, Coverage: 0.5}}
	if got, want := rankIDs(t, s, def, hits), []string{near.ID, cand.ID}; !slices.Equal(got, want) {
		t.Fatalf("small gap: order %v, want %v", got, want)
	}
}
