package inject

import (
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 요청 회상의 문(설계 보완, 1-6 b6 R1): 맞은 낱말이 하나뿐인 기억은 recallMinCoverageOneWord 이상, 둘 이상이면
// recallMinCoverage 이상이어야 든다. 「낱말 넷 중 하나」(0.25)는 흔한 낱말 하나로 맞은 주제 밖 기억과 가를 수 없다.
func TestB1_한_낱말만_맞은_기억은_한_낱말_비율_문턱을_넘어야_든다(t *testing.T) {
	s := store(t)
	one := save(t, s, memory.KindFact, "캐시는 매주 비운다", "", here, true)
	two := save(t, s, memory.KindFact, "배포 노트는 짧게 쓴다", "", here, true)
	in := func(cfg config.Config, prompt, id string) bool {
		return strings.Contains(PromptInjection(s, cfg, here, prompt).Text, id)
	}
	cfg := config.Default()
	cfg.RecallMinScore, cfg.RecallMinRelative = 0.01, 0.01
	if in(cfg, "캐시 정리 순서 확인", one.ID) {
		t.Fatal("낱말 넷 중 하나만 맞은 기억(0.25)이 기본값(0.3)에서 들었습니다")
	}
	if !in(cfg, "캐시 정리 순서", one.ID) {
		t.Fatal("낱말 셋 중 하나(0.33)는 들어야 합니다")
	}
	cfg.RecallMinCoverageOneWord = 0.25
	if !in(cfg, "캐시 정리 순서 확인", one.ID) {
		t.Fatal("한 낱말 비율 문턱을 0.25로 내리면 들어야 합니다")
	}
	// 맞은 낱말이 둘 이상이면 recallMinCoverage(0.25)를 쓴다: 여덟 중 둘.
	cfg = config.Default()
	cfg.RecallMinScore, cfg.RecallMinRelative = 0.01, 0.01
	prompt := "배포 노트 화면 버튼 캐시 정리 순서 확인"
	if !in(cfg, prompt, two.ID) {
		t.Fatal("낱말 여덟 중 둘(0.25)은 들어야 합니다")
	}
	cfg.RecallMinCoverage = 0.26
	if in(cfg, prompt, two.ID) {
		t.Fatal("recallMinCoverage를 올리면 빠져야 합니다")
	}
}

// 위임 회상의 문(설계 보완, 1-6 b6 T1): 맞은 비율을 쓰지 않고, 시키는 말(명령형 어미·위임 머리 낱말·항목 번호)을 뺀 내용
// 낱말 taskRecallMinWords개 이상이 맞고 점수가 taskRecallMinScore 이상이며 1위의 taskRecallMinRelative배 이상이면 든다.
func TestSubagentStart_위임_회상은_내용_낱말_수로_문을_연다(t *testing.T) {
	s := store(t)
	task := "계획: p 항목: m2. 결제 모듈 동시성 시험의 경합 원인을 조사하라. 로그 화면 버튼 배포 순서 캐시 설정 문서 초안 정리 기준 범위를 확인하고 보고하라."
	three := subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "결제 모듈 경합은 실제로 일으킨다", Topic: "결제 경합"})
	twoPlus := subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "검토 요청과 조사 보고는 결제 문서로 하라", Topic: "보고 방식"})
	if c, _ := memory.Coverage(three, memory.MeaningfulWords(task)); c >= config.DefaultRecallMinCoverage {
		t.Fatalf("재료: 요청 문턱으로는 빠져야 하는 비율이어야 함 %v", c)
	}
	cfg := config.Default()
	inj := SubagentStart(s, cfg, memory.ScopeGlobal, task)
	if !slices.Contains(inj.MemoryIDs, three.ID) || slices.Contains(inj.MemoryIDs, twoPlus.ID) {
		t.Fatalf("내용 낱말 셋이 맞은 기억만 들어야 함: %v\n%s", inj.MemoryIDs, inj.Text)
	}
	if strings.Contains(inj.Text, "조사하라") || !strings.Contains(inj.Text, "맞은 낱말 결제·모듈·경합") {
		t.Fatalf("까닭 줄은 내용 낱말만:\n%s", inj.Text)
	}
	cfg.TaskRecallMinWords = 4
	if inj := SubagentStart(s, cfg, memory.ScopeGlobal, task); slices.Contains(inj.MemoryIDs, three.ID) {
		t.Fatalf("taskRecallMinWords 4면 빠져야 함:\n%s", inj.Text)
	}
	cfg = config.Default()
	cfg.TaskRecallMinWords, cfg.TaskRecallMinRelative = 2, 0.01
	if inj := SubagentStart(s, cfg, memory.ScopeGlobal, task); !slices.Contains(inj.MemoryIDs, twoPlus.ID) {
		t.Fatalf("taskRecallMinWords 2면 결제·문서 두 낱말로 들어야 함:\n%s", inj.Text)
	}
	cfg = config.Default()
	cfg.TaskRecallMinScore = 1
	if inj := SubagentStart(s, cfg, memory.ScopeGlobal, task); slices.Contains(inj.MemoryIDs, three.ID) {
		t.Fatalf("taskRecallMinScore 1이면 빠져야 함:\n%s", inj.Text)
	}
}
