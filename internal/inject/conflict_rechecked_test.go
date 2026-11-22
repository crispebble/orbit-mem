package inject

// 자동 모드가 꺼진 기본 상태에서 서버가 다시 확인한 검증 사실도 사용자 말 사실을 대체하지 않는다: 회상에서 옛 사실 줄 밑에
// 어긋나는 새 사실과 확인 지시가 붙는다.

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestRecallLine_다시_확인된_검증_사실과_어긋난_사용자_말_사실에_새_사실과_확인_지시가_붙는다(t *testing.T) {
	s := store(t)
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: conflictOldTitle, Body: "릴리스는 금요일 오후 배포 창에서만 나간다", Topic: "배포 일정", Scope: here,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: conflictOldTitle}})
	if err != nil {
		t.Fatal(err)
	}
	nw, rep, err := s.SaveWith(memory.SaveInput{Kind: memory.KindFact, Title: conflictNewTitle, Body: "오전 배포 창으로 바뀌었다", Topic: "배포 일정", Scope: here,
		Who:    memory.Who{Actor: memory.ActorAI},
		Source: memory.Source{Kind: memory.SourceVerification, Ref: "/repo/RELEASING.md", Excerpt: "배포 창: 화요일 오전", Rechecked: true}})
	if err != nil || len(rep.Superseded) != 0 || len(rep.Conflicts) != 1 || rep.Conflicts[0] != old.ID {
		t.Fatalf("다시 확인된 사실이 대체했거나 어긋남이 없습니다: %v %+v", err, rep)
	}
	if m, _ := s.Get(old.ID); m.State != memory.StateConfirmed {
		t.Fatalf("옛 사실 %+v", m)
	}
	got := PromptInjectionFor(s, config.Default(), here, conflictPrompt, "claude", "A", Options{})
	line := lineOf(got.Text, old.ID)
	if !strings.Contains(line, sideMark+": 「"+conflictNewTitle+"」("+nw.ID+")") || !strings.Contains(line, askMark) {
		t.Fatalf("옛 기억 줄에 새 사실과 확인 지시가 없습니다:\n%s", got.Text)
	}
}
