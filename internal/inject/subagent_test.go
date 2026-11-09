package inject

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const subTask = "결제 모듈 동시성 시험을 고친다. 경합을 실제로 일으키는 시험을 쓴다"

func subSave(t *testing.T, s *memory.Store, in memory.SaveInput) memory.Memory {
	t.Helper()
	if in.Source.Kind == "" {
		in.Source = memory.Source{Kind: memory.SourceQuote, Excerpt: in.Title}
	}
	if in.Scope == "" {
		in.Scope = memory.ScopeGlobal
	}
	m, err := s.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func subPin(t *testing.T, s *memory.Store, title, body string) memory.Memory {
	t.Helper()
	m := subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: title, Body: body, Topic: title})
	if _, err := s.Pin(m.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	return m
}

// 서브에이전트 시작(설계 4.1, L-4): 맞는 기억이 없으면 상시 핵심의 제목 줄(id와 함께, 본문 없음)만 600자 안이다.
func TestSubagentStart_맞는_것이_없으면_핵심_제목_줄만_600자_안이다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	if inj := SubagentStart(s, cfg, memory.ScopeGlobal, ""); inj.Text != "" || len(inj.MemoryIDs) != 0 {
		t.Fatalf("no core, still:\n%s", inj.Text)
	}
	core := subPin(t, s, "답은 존댓말", "반말을 쓰지 않는다는 본문")
	subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "핵심이 아닌 확정 규칙", Topic: "아님"})
	for _, task := range []string{"", "오늘 점심 메뉴를 골라 줘", subTask} {
		inj := SubagentStart(s, cfg, memory.ScopeGlobal, task)
		if !strings.HasPrefix(inj.Text, RoleHead) || !strings.Contains(inj.Text, "- 답은 존댓말 — "+core.ID) ||
			strings.Contains(inj.Text, "반말을 쓰지") || strings.Contains(inj.Text, "핵심이 아닌") ||
			!slices.Equal(inj.MemoryIDs, []string{core.ID}) || inj.Chars > maxRoleRunes {
			t.Fatalf("task %q:\n%s\n%v", task, inj.Text, inj.MemoryIDs)
		}
	}
}

// 핵심이 많아도 제목 줄은 600자 안이고, 넣지 못한 핵심 수를 한 줄로 알린다.
func TestSubagentStart_핵심_제목_줄은_600자를_넘지_않는다(t *testing.T) {
	s := store(t)
	var ids []string
	for i := range 12 {
		ids = append(ids, subPin(t, s, fmt.Sprintf("핵심 규칙 %02d: %s", i, strings.Repeat("긴 제목 ", 12)), "본문").ID)
	}
	inj := SubagentStart(s, config.Default(), memory.ScopeGlobal, "")
	if inj.Chars > maxRoleRunes || len(inj.MemoryIDs) == 0 || len(inj.MemoryIDs) == len(ids) ||
		!strings.Contains(inj.Text, fmt.Sprintf("상시 핵심 %d개 더", len(ids)-len(inj.MemoryIDs))) {
		t.Fatalf("%d chars, %d ids:\n%s", inj.Chars, len(inj.MemoryIDs), inj.Text)
	}
}

// 위임 글로 B1과 같은 찾기·문턱을 돌려 고른 기억: 제목·id·고른 까닭·요약(본문 앞부분). 그 뒤 핵심 제목 줄. 주입 id는 글 차례다.
func TestSubagentStart_위임_글에_맞는_기억을_까닭과_요약으로_준다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	core := subPin(t, s, "답은 존댓말", "반말 금지")
	hit := subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "결제 모듈 동시성 시험은 경합을 실제로 일으킨다", Topic: "결제 동시성 시험",
		Body: "결제 모듈의 동시성 시험은 고루틴 여럿을 한꺼번에 풀어 경합을 실제로 일으킨다. 한 번 통과로 끝내지 않는다."})
	subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "배포 노트는 한국어로", Topic: "배포 노트", Body: "배포 노트"})
	inj := SubagentStart(s, cfg, memory.ScopeGlobal, subTask)
	if !strings.HasPrefix(inj.Text, taskRecallHead) || !strings.Contains(inj.Text, hit.ID) ||
		!strings.Contains(inj.Text, "고른 까닭: 맞은 낱말 ") || !strings.Contains(inj.Text, "주제 결제 동시성 시험") ||
		!strings.Contains(inj.Text, "고루틴 여럿을") || strings.Contains(inj.Text, "배포 노트") ||
		!strings.Contains(inj.Text, "\n"+RoleHead) || !slices.Equal(inj.MemoryIDs, []string{hit.ID, core.ID}) || inj.Chars > MaxSubagentRunes {
		t.Fatalf("%v\n%s", inj.MemoryIDs, inj.Text)
	}
	if again := SubagentStart(s, cfg, memory.ScopeGlobal, subTask); again.Text != inj.Text || !slices.Equal(again.MemoryIDs, inj.MemoryIDs) {
		t.Fatal("같은 입력에 다른 출력")
	}
	if inj.marks[memory.MarkShown] == nil || !slices.Contains(inj.marks[memory.MarkShown], hit.ID) {
		t.Fatalf("marks %v", inj.marks)
	}
}

// 맞는 기억이 많아도 5개까지, 요약은 500자까지, 위임 회상은 2,400자, 합계는 3,000자 안이다(L-4).
func TestSubagentStart_위임_회상은_5개_2400자_합계_3000자_안이다(t *testing.T) {
	s := store(t)
	for i := range 4 {
		subPin(t, s, fmt.Sprintf("핵심 %d: %s", i, strings.Repeat("늘 지키는 규칙 ", 8)), "본문")
	}
	for _, w := range []string{"배포 노트는 한국어로", "커밋 메시지는 짧게", "로그는 파일로 둔다", "화면은 스크린샷으로", "비밀값은 출력하지 않는다", "브랜치에서 일한다"} {
		subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: w, Topic: w, Body: w + " 본문"})
	}
	var hits []string
	for i := range 9 {
		hits = append(hits, subSave(t, s, memory.SaveInput{Kind: memory.KindDecision, Topic: fmt.Sprintf("결제 동시성 %d", i),
			Title: fmt.Sprintf("결제 모듈 동시성 시험은 경합을 실제로 일으켜 고친다 %d", i), Body: strings.Repeat("결제 모듈 동시성 시험에서 경합을 일으키는 방법 ", 40)}).ID)
	}
	inj := SubagentStart(s, config.Default(), memory.ScopeGlobal, subTask)
	recall, role, _ := strings.Cut(inj.Text, "\n"+RoleHead)
	role = RoleHead + role
	n := 0
	for _, id := range inj.MemoryIDs {
		if slices.Contains(hits, id) {
			n++
		}
	}
	if n != maxTaskRecall || runes(recall)+1 > maxTaskRecallRunes || runes(role) > maxRoleRunes || inj.Chars > MaxSubagentRunes {
		t.Fatalf("recall %d ids, %d chars, role %d chars, total %d:\n%s", n, runes(recall), runes(role), inj.Chars, inj.Text)
	}
	for _, line := range strings.Split(recall, "\n") {
		if runes(line) > taskSummaryRunes+4 {
			t.Fatalf("summary line %d chars: %s", runes(line), line)
		}
	}
}

// 낡거나 대체된 옛 판이 위임 글에 맞았으면 「옛 판 N개 뺌」 한 줄만 두고, 옛 판 id는 글에도 주입 id에도 없다.
func TestSubagentStart_옛_판은_한_줄로_알리고_id는_넣지_않는다(t *testing.T) {
	s := store(t)
	old := subSave(t, s, memory.SaveInput{Kind: memory.KindDecision, Topic: "결제 동시성 시험 방식",
		Title: "결제 모듈 동시성 시험은 모의 경합으로 고친다", Body: "결제 모듈 동시성 시험을 모의 경합으로 고친다"})
	cur := subSave(t, s, memory.SaveInput{Kind: memory.KindDecision, Topic: "결제 동시성 시험 방식",
		Title: "결제 모듈 동시성 시험은 경합을 실제로 일으킨다", Body: "결제 모듈 동시성 시험은 경합을 실제로 일으킨다"})
	if got, _ := s.Get(old.ID); got.State != memory.StateStale {
		t.Fatalf("old state %s", got.State)
	}
	inj := SubagentStart(s, config.Default(), memory.ScopeGlobal, subTask)
	if !strings.Contains(inj.Text, "옛 판 1개 뺌") || strings.Contains(inj.Text, old.ID) || slices.Contains(inj.MemoryIDs, old.ID) ||
		!slices.Contains(inj.MemoryIDs, cur.ID) {
		t.Fatalf("%v\n%s", inj.MemoryIDs, inj.Text)
	}
}

// 읽기 범위는 부모 세션의 폴더를 따른다: 다른 저장소의 기억은 위임 글에 맞아도 넣지 않는다. recall off면 아무것도 없다.
func TestSubagentStart_부모의_읽기_범위와_recall_off를_따른다(t *testing.T) {
	s := store(t)
	subPin(t, s, "답은 존댓말", "반말 금지")
	other := subSave(t, s, memory.SaveInput{Kind: memory.KindRule, Scope: "project:github.com/x/other", Topic: "결제 동시성",
		Title: "결제 모듈 동시성 시험은 경합을 실제로 일으킨다", Body: "결제 모듈 동시성 시험"})
	cfg := config.Default()
	if inj := SubagentStart(s, cfg, "project:github.com/x/here", subTask); strings.Contains(inj.Text, other.ID) {
		t.Fatalf("다른 저장소 기억:\n%s", inj.Text)
	}
	if inj := SubagentStart(s, cfg, "project:github.com/x/other", subTask); !strings.Contains(inj.Text, other.ID) {
		t.Fatalf("그 저장소에서는 맞는다:\n%s", inj.Text)
	}
	cfg.Recall = config.RecallOff
	if inj := SubagentStart(s, cfg, "project:github.com/x/other", subTask); inj.Text != "" {
		t.Fatalf("off:\n%s", inj.Text)
	}
}
