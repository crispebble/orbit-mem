package inject

// 도구 직전 회상의 순위(명세 R-1·R-3, T-2): 재검토 지남인 확정 규칙은 같은 조건의 다른 확정 규칙 뒤로 가고, 스위치를 켜도
// 입력에 잘 맞는 다른 기억이 확정 규칙을 본문 밖으로 밀어내지 않는다.

import (
	"slices"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// saveRuleOn saves a confirmed global rule triggered by tool; with reviewDue its review day is one day after
// it was saved.
func saveRuleOn(t *testing.T, s *memory.Store, title, tool string, reviewDue bool) memory.Memory {
	t.Helper()
	later(s, time.Minute)
	in := memory.SaveInput{Kind: memory.KindRule, Title: title, Body: title, Scope: memory.ScopeGlobal, Topic: title, Tools: []string{tool},
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}}
	if reviewDue {
		in.ReviewAfterDays = 1
	}
	m, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 재검토 지남인 규칙이 더 늦게 고쳐졌다: 지금 순서(고친 시각)였다면 그쪽이 앞이다. 스위치와 입력이 없어도 재검토 지남이 뒤다.
func TestRank_도구_직전에서_재검토_지남인_확정_규칙은_같은_조건의_규칙_뒤다(t *testing.T) {
	s := store(t)
	fresh := saveRuleOn(t, s, "커밋 전에 시험을 돌린다", "Bash", false)
	due := saveRuleOn(t, s, "푸시 전에 사람에게 묻는다", "Bash", true)
	later(s, 48*time.Hour)
	got := ToolInjection(s, config.Default(), here, "claude", "S", "Bash", "").MemoryIDs
	if want := []string{fresh.ID, due.ID}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// 스위치를 켜도 입력에 잘 맞는 후보 둘이 무관한 확정 규칙을 본문(호출당 2개) 밖으로 밀어내지 않는다(T-2).
func TestRank_도구_직전에서_확정_규칙은_점수_단계에서_밀리지_않는다(t *testing.T) {
	s := store(t)
	rule := saveOn(t, s, memory.KindRule, "셸 명령은 한 줄에 하나만 쓴다", "여러 명령을 이어 붙이지 않는다", []string{"Bash"}, true)
	saveOn(t, s, memory.KindLesson, "rsync 원본 경로 끝에 빗금을 붙인다", "빗금이 없으면 폴더째 복사된다", []string{"Bash"}, false)
	saveOn(t, s, memory.KindLesson, "rsync 대상 경로 빗금 여부로 결과가 갈린다", "원본 경로와 대상 경로를 함께 본다", []string{"Bash"}, false)
	cfg := gated()
	cfg.RankByScore, cfg.RankScoreMargin = true, 0.1
	got := ToolInjection(s, cfg, here, "claude", "S", "Bash", "rsync -av 원본 경로 빗금 대상")
	if len(got.MemoryIDs) < 2 || got.MemoryIDs[0] != rule.ID {
		t.Fatalf("ids %v, want the rule %s first", got.MemoryIDs, rule.ID)
	}
}
