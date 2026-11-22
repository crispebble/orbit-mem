package inject

// 90일 재검토(명세 L-2·L-3): 마지막 확인 뒤 90일이 지난 기억은 회상에서 빼지 않고 「재검토 후보」로 표시하며 순위만 뒤로
// 간다. 세션 시작에는 지금 자리에서 보이는 재검토 후보의 개수 한 줄만 알리고, 0건이면 줄이 없다.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const (
	reviewTitle = "배포 창은 금요일 오후다"
	reviewAsk   = "배포 창 금요일 오후 일정을 알려 줘"
)

func saveReview(t *testing.T, s *memory.Store, title, scope string, tools ...string) memory.Memory {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Body: title + ". 릴리스는 이 창에서만 나간다", Scope: scope, Tools: tools,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRecallLine_재검토_후보는_빼지_않고_표시만_한다(t *testing.T) {
	s := store(t)
	m := saveReview(t, s, reviewTitle, memory.ScopeGlobal, "Bash")
	cfg := config.Default()
	// 90일 안에는 표시가 없다.
	if got := PromptInjectionFor(s, cfg, here, reviewAsk, "claude", "A", Options{}); !strings.Contains(got.Text, m.ID) || strings.Contains(got.Text, reviewMark) {
		t.Fatalf("90일 안인데 표시가 있거나 기억이 없습니다:\n%s", got.Text)
	}
	later(s, 91*24*time.Hour)
	// 요청 회상: 빠지지 않고, 상태는 확정 그대로이며, 표시가 붙는다.
	got := PromptInjectionFor(s, cfg, here, reviewAsk, "claude", "B", Options{})
	line := lineOf(got.Text, m.ID)
	if line == "" || !strings.Contains(line, "확정 · "+reviewMark) {
		t.Fatalf("요청 회상 줄:\n%s", got.Text)
	}
	if strings.Contains(got.Text, "재검토일 지남") {
		t.Fatalf("옛 표시가 남았습니다:\n%s", got.Text)
	}
	// 도구 직전 회상도 같다.
	tool := ToolInjection(s, cfg, here, "claude", "C", "Bash", "")
	if line := lineOf(tool.Text, m.ID); line == "" || !strings.Contains(line, "확정 · "+reviewMark) {
		t.Fatalf("도구 직전 줄:\n%s", tool.Text)
	}
	// 위임(서브에이전트 시작)도 같다.
	sub := SubagentStart(s, cfg, here, reviewAsk)
	if line := lineOf(sub.Text, m.ID); line == "" || !strings.Contains(line, reviewMark) {
		t.Fatalf("위임 줄:\n%s", sub.Text)
	}
	// 사용자 말로 다시 확인하면 표시가 사라진다.
	if _, err := s.Update(memory.UpdateInput{ID: m.ID, Reason: "다시 확인", Who: memory.Who{Actor: memory.ActorUser},
		Body: func() *string { b := "릴리스는 금요일 오후 창에서만 나간다"; return &b }()}); err != nil {
		t.Fatal(err)
	}
	if got := PromptInjectionFor(s, cfg, here, reviewAsk, "claude", "D", Options{}); strings.Contains(lineOf(got.Text, m.ID), reviewMark) {
		t.Fatalf("확인한 뒤에도 표시가 있습니다:\n%s", got.Text)
	}
}

// 진단의 재검토 메모는 재검토일 계산을 따른다: 재검토일이 비어 대비책으로 후보가 된 기억도 1970-01-01을 찍지 않는다.
func TestDiagnose_재검토일이_없는_기억도_재검토일을_계산해_보인다(t *testing.T) {
	s := store(t)
	m := saveReview(t, s, reviewTitle, memory.ScopeGlobal)
	if _, err := s.DB().Exec(`UPDATE memories SET review_at=0 WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	saved := s.Now()
	later(s, 91*24*time.Hour)
	d := Diagnose(s, config.Default(), DiagnoseInput{ID: m.ID, Scope: here, Prompt: reviewAsk})
	var got string
	for _, n := range d.Notes {
		if n.Code == "review_due" {
			got = n.Args[0]
		}
	}
	if want := saved.Add(90 * 24 * time.Hour).Local().Format("2006-01-02"); got != want {
		t.Fatalf("재검토일 메모 %q, want %q (notes %+v)", got, want, d.Notes)
	}
}

// 순위 코드를 고치지 않아도 기본 재검토일(마지막 확인 + 90일)만으로 점수·확정이 같은 재검토 후보가 뒤로 간다.
func TestRank_기본_재검토일만으로도_재검토_후보가_뒤로_간다(t *testing.T) {
	s := store(t)
	old := saveRank(t, s, "오래 확인하지 않은 확정 기억", true, false)
	fresh := saveRank(t, s, "최근 확인한 확정 기억", true, false)
	later(s, 91*24*time.Hour)
	b := "다시 확인한 본문"
	if _, err := s.Update(memory.UpdateInput{ID: fresh.ID, Body: &b, Who: memory.Who{Actor: memory.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	fresh, _ = s.Get(fresh.ID)
	vw, _ := s.Viewer()
	if !vw.Of(old).ReviewDue || vw.Of(fresh).ReviewDue {
		t.Fatalf("review due old=%v fresh=%v", vw.Of(old).ReviewDue, vw.Of(fresh).ReviewDue)
	}
	hits := []memory.RecallHit{{Memory: old, Score: 0.6, Coverage: 0.5}, {Memory: fresh, Score: 0.6, Coverage: 0.5}}
	if got, want := rankIDs(t, s, config.Default(), hits), []string{fresh.ID, old.ID}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestSessionStart_재검토_후보_개수_줄은_0건이면_없다(t *testing.T) {
	const countMark = reviewMark + " "
	s := store(t)
	cfg := config.Default()
	own := saveReview(t, s, "이 앱의 배포 창은 금요일 오후다", here)
	saveReview(t, s, "전역 배포 규칙은 금요일 오후다", memory.ScopeGlobal)
	saveReview(t, s, "다른 앱의 배포 창은 화요일이다", otherHere)
	if text := SessionBundleWith(s, cfg, here, "claude", "A", nil).Text; strings.Contains(text, countMark) {
		t.Fatalf("0건인데 줄이 있습니다:\n%s", text)
	}
	later(s, 91*24*time.Hour)
	// 이 자리에서 보이는 것만 센다: 이 앱 것과 전역 것, 둘이다(다른 앱 것은 개수에도 들지 않는다).
	text := SessionBundleWith(s, cfg, here, "claude", "B", nil).Text
	if !strings.Contains(text, countMark+"2건") || strings.Contains(text, "다른 앱") {
		t.Fatalf("2건 줄이 없거나 다른 범위가 샜습니다:\n%s", text)
	}
	if other := SessionBundleWith(s, cfg, otherHere, "claude", "C", nil).Text; !strings.Contains(other, countMark+"2건") {
		t.Fatalf("다른 앱 자리의 개수:\n%s", other)
	}
	// 줄은 세션 묶음에 실려 같은 세션의 재개에도 같다.
	if again := SessionBundleWith(s, cfg, here, "claude", "B", nil).Text; again != text {
		t.Fatalf("재개한 묶음이 다릅니다:\n%s", again)
	}
	// 확인하면(사용자 말로 고침) 하나가 빠진다. 낡은 기억(대체돼 폐기됨)은 세지 않는다.
	b := "금요일 오후"
	if _, err := s.Update(memory.UpdateInput{ID: own.ID, Body: &b, Who: memory.Who{Actor: memory.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	if got := SessionBundleWith(s, cfg, here, "claude", "D", nil).Text; !strings.Contains(got, countMark+"1건") {
		t.Fatalf("확인한 뒤 1건이어야 합니다:\n%s", got)
	}
	if _, err := s.SetState(own.ID, memory.StateStale, "시험"); err != nil {
		t.Fatal(err)
	}
	// 남은 전역 기억을 폐기하면 0건이다.
	all, _ := s.List(memory.StateConfirmed)
	for _, m := range all {
		if _, err := s.SetState(m.ID, memory.StateStale, "시험"); err != nil {
			t.Fatal(err)
		}
	}
	if got := SessionBundleWith(s, cfg, here, "claude", "E", nil).Text; strings.Contains(got, countMark) {
		t.Fatalf("0건인데 줄이 있습니다:\n%s", got)
	}
	// 회상을 끄면 기억도 줄도 없다.
	off := config.Default()
	off.Recall = config.RecallOff
	if got := SessionBundleWith(s, off, here, "claude", "F", nil).Text; strings.Contains(got, countMark) {
		t.Fatalf("끈 회상에 줄이 있습니다:\n%s", got)
	}
}

// 길이가 모자라면 뒤 안내 줄부터 잘리고, 개수 줄이 들어간 묶음에서도 한도를 넘지 않는다.
func TestSessionStart_재검토_후보_개수_줄은_한도_안에서_뒤_줄부터_잘린다(t *testing.T) {
	s := store(t)
	saveReview(t, s, reviewTitle, memory.ScopeGlobal)
	later(s, 91*24*time.Hour)
	long := strings.Repeat("가", MaxSessionStartRunes) // 한 줄이 한도를 다 채운다
	got := SessionBundleWith(s, config.Default(), here, "claude", "A", func() []string { return []string{long} }).Text
	if !strings.Contains(got, reviewMark+" 1건") {
		t.Fatalf("개수 줄이 뒤 안내 줄보다 먼저 잘렸습니다")
	}
	if strings.Contains(got, long) || runes(got) > MaxSessionStartRunes {
		t.Fatalf("한도 %d룬을 넘었습니다: %d", MaxSessionStartRunes, runes(got))
	}
}
