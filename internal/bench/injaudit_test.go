package bench_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 명세 2절·F-6: 기록된 주입 사건(inject.*)마다 그 시각에 이미 낡은 기억이 실렸는지 센다. 낡기 전 주입은 세지 않고, 낡은
// 뒤 주입은 낡은 지 얼마나 지났는지(Late)를 함께 낸다. 호스트가 실었다고 알려 온 사건(inject.delivered)은 주입 결정이 아니라
// 세지 않는다. 지금 DB에 없는(지운) 기억은 그 시각 상태를 알 수 없으므로 폐기로 세지 않고 따로 센다. 원본 DB는 바뀌지 않는다.
func TestAuditInjections_주입_시각에_낡은_기억을_센다(t *testing.T) {
	fixHome(t)
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	tick := func(d time.Duration) int64 { clock = clock.Add(d); return clock.UnixMilli() }
	save := func(title string) memory.Memory {
		m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Scope: memory.ScopeGlobal,
			Source: memory.Source{Kind: memory.SourceSession, Ref: "t"}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	inject := func(kind, session string, ids ...string) {
		if err := s.RecordEvent(memory.Event{Kind: kind, Host: "claude-code", Session: session}, map[string]any{"memoryIds": ids, "chars": 10}); err != nil {
			t.Fatal(err)
		}
	}
	a, b, gone := save("배포 창은 화요일"), save("롤백은 이전 태그로"), save("지울 기억")
	tick(time.Hour)
	inject(memory.EventInjectSessionStart, "s1", a.ID, b.ID, gone.ID) // 모두 살아 있을 때
	staleAt := tick(time.Hour)
	if _, err := s.SetState(a.ID, memory.StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(memory.ForgetInput{IDs: []string{gone.ID}, Reason: "시험"}); err != nil {
		t.Fatal(err)
	}
	injectAt := tick(3 * time.Hour)
	inject(memory.EventInjectSessionStart, "s1", a.ID, b.ID, gone.ID) // 재개: 얼린 묶음 그대로
	inject(memory.EventInjectTool, "s1", a.ID)
	inject(memory.EventInjectSubagent, "s2/agent-1", b.ID)
	inject(memory.EventInjectDelivered, "s1", a.ID)
	s.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := bench.AuditInjections(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Events != 4 || got.Injections != 8 || got.Dead != 2 || got.DeadEvents != 2 || got.Unknown != 2 {
		t.Fatalf("audit %+v, want 4 events, 8 injections, 2 dead in 2 events, 2 unknown", got)
	}
	if got.Pairs != 1 || got.Memories != 1 {
		t.Errorf("pairs %d memories %d, want the one (s1, a) pair", got.Pairs, got.Memories)
	}
	if got.ByKind[memory.EventInjectSessionStart] != 1 || got.ByKind[memory.EventInjectTool] != 1 || len(got.ByKind) != 2 {
		t.Errorf("by kind %v", got.ByKind)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items %+v", got.Items)
	}
	for _, it := range got.Items {
		if it.MemoryID != a.ID || it.Session != "s1" || it.Why != bench.DeadStale || it.At != injectAt || it.Late != injectAt-staleAt {
			t.Errorf("item %+v, want a stale for %d ms in s1", it, injectAt-staleAt)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("the audit changed the DB it read")
	}
}
