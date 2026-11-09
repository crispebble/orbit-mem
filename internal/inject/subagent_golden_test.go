package inject

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

var updateSubagentGolden = flag.Bool("update-subagent-golden", false, "rewrite internal/inject/testdata/subagent_golden.txt")

// subagentGoldenWorld is a store where delegated tasks meet live memories, stale and superseded versions,
// memories of another repository, a journal and the user core: every branch of the task recall and its
// old-version line. labels maps each memory id to a stable name (ids are random).
func subagentGoldenWorld(t *testing.T) (*memory.Store, map[string]string) {
	t.Helper()
	s := store(t)
	labels := map[string]string{}
	add := func(label string, in memory.SaveInput) {
		t.Helper()
		labels[subSave(t, s, in).ID] = label
	}
	core := subPin(t, s, "답은 존댓말", "반말을 쓰지 않는다")
	labels[core.ID] = "core"
	add("pay-old", memory.SaveInput{Kind: memory.KindDecision, Topic: "결제 동시성 시험 방식",
		Title: "결제 모듈 동시성 시험은 모의 경합으로 고친다", Body: "결제 모듈 동시성 시험을 모의 경합으로 고친다"})
	add("pay", memory.SaveInput{Kind: memory.KindDecision, Topic: "결제 동시성 시험 방식",
		Title: "결제 모듈 동시성 시험은 경합을 실제로 일으킨다", Body: "결제 모듈 동시성 시험은 고루틴 여럿으로 경합을 실제로 일으킨다. " + strings.Repeat("경합 재현 순서를 적는다. ", 40)})
	add("pay-retry", memory.SaveInput{Kind: memory.KindRule, Topic: "결제 재시도",
		Title: "결제 재시도는 멱등 키로 막는다", Body: "결제 모듈 재시도는 멱등 키가 같으면 한 번만 처리한다"})
	add("pay-other-repo", memory.SaveInput{Kind: memory.KindRule, Scope: "project:github.com/x/other", Topic: "결제 동시성",
		Title: "결제 모듈 동시성 시험은 다른 저장소 규칙", Body: "결제 모듈 동시성 시험"})
	add("deploy-old", memory.SaveInput{Kind: memory.KindRule, Topic: "배포 롤백",
		Title: "배포 스크립트 롤백은 손으로 한다", Body: "배포 스크립트 롤백 절차"})
	add("deploy", memory.SaveInput{Kind: memory.KindRule, Topic: "배포 롤백",
		Title: "배포 스크립트 롤백 절차는 문서로 남긴다", Body: "배포 스크립트가 실패하면 롤백 절차 문서를 따른다"})
	add("deploy-journal", memory.SaveInput{Kind: memory.KindJournal, Topic: "배포 기록",
		Title: "배포 스크립트 롤백 기록", Body: "배포 스크립트 롤백을 했다"})
	add("migrate", memory.SaveInput{Kind: memory.KindFact, Topic: "마이그레이션 백업",
		Title: "스키마 마이그레이션 전에 백업 사본을 만든다", Body: "스키마 마이그레이션은 백업 사본을 먼저 만든다"})
	expired := subSave(t, s, memory.SaveInput{Kind: memory.KindFact, Topic: "마이그레이션 임시 사본",
		Title: "스키마 마이그레이션 임시 사본은 이번 주만 둔다", Body: "스키마 마이그레이션 임시 사본"})
	labels[expired.ID] = "migrate-expired"
	if _, err := s.DB().Exec(`UPDATE memories SET expires_at=? WHERE id=?`, s.Now().Add(-time.Hour).UnixMilli(), expired.ID); err != nil {
		t.Fatal(err) // 산 기억이지만 기한이 지났다: 옛 판으로 센다
	}
	for i := range 12 { // 낱말이 겹치는 잡음
		add(fmt.Sprintf("noise-%02d", i), memory.SaveInput{Kind: memory.KindFact, Topic: fmt.Sprintf("잡음 %02d", i),
			Title: fmt.Sprintf("모듈 %02d 시험 메모", i), Body: fmt.Sprintf("모듈 %02d 시험은 따로 돈다. 문서와 절차", i)})
	}
	return s, labels
}

// 서브에이전트 시작 글의 골든(계획 1-6 b10 6번): 위임 회상과 옛 판 줄을 고쳐도 같은 입력에 같은 글·같은 주입 id다.
func TestSubagentStart_골든과_같은_글이다(t *testing.T) {
	s, labels := subagentGoldenWorld(t)
	tasks := []struct{ scope, task string }{
		{memory.ScopeGlobal, subTask},
		{memory.ScopeGlobal, "배포 스크립트의 롤백 절차를 문서로 정리한다"},
		{memory.ScopeGlobal, "결제 모듈 재시도 멱등 키와 동시성 시험을 함께 고친다. 배포 스크립트 롤백 절차도 본다"},
		{memory.ScopeGlobal, "스키마 마이그레이션 백업 사본을 확인한다"},
		{memory.ScopeGlobal, "오늘 점심 메뉴를 골라 줘"},
		{"project:github.com/x/other", subTask},
		{"project:github.com/x/here", subTask},
	}
	var b strings.Builder
	for _, tc := range tasks {
		inj := SubagentStart(s, config.Default(), tc.scope, tc.task)
		text := inj.Text
		ids := make([]string, len(inj.MemoryIDs))
		for i, id := range inj.MemoryIDs {
			ids[i] = labels[id]
		}
		for id, label := range labels {
			text = strings.ReplaceAll(text, id, "<"+label+">")
		}
		fmt.Fprintf(&b, "=== %s | %s\nids: %s\nchars: %d\n%s\n", tc.scope, tc.task, strings.Join(ids, ","), inj.Chars, text)
	}
	path := filepath.Join("testdata", "subagent_golden.txt")
	if *updateSubagentGolden {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (make it with: go test ./internal/inject -run TestSubagentStart_골든 -update-subagent-golden)", err)
	}
	if got := b.String(); got != string(want) {
		t.Fatalf("골든과 다르다\n--- got\n%s\n--- want\n%s", got, want)
	}
}
