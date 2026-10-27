package metrics

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit/internal/memory"
)

func store(t *testing.T, now time.Time) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return now }
	t.Cleanup(func() { s.Close() })
	return s
}

func at(s *memory.Store, day string, hour int) {
	d, _ := time.ParseInLocation("2006-01-02", day, time.Local)
	t := d.Add(time.Duration(hour) * time.Hour)
	s.Now = func() time.Time { return t }
}

func TestCompute_기간의_지표를_계산한다(t *testing.T) {
	s := store(t, time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local))
	rec := func(day string, e memory.Event, data any) {
		at(s, day, 10)
		if err := s.RecordEvent(e, data); err != nil {
			t.Fatal(err)
		}
	}
	// 기간 밖
	rec("2026-10-10", memory.Event{Kind: memory.EventCorrection}, map[string]any{"memoryId": "m-r", "injected": "yes"})
	// 세션 시작 2번
	rec("2026-10-21", memory.Event{Kind: memory.EventInjectSessionStart, Session: "a"}, map[string]any{"memoryIds": []string{"m-r"}, "chars": 3000, "titleOnly": 2})
	rec("2026-10-22", memory.Event{Kind: memory.EventInjectSessionStart, Session: "b"}, map[string]any{"memoryIds": []string{"m-r"}, "chars": 1000, "titleOnly": 0})
	rec("2026-10-22", memory.Event{Kind: memory.EventInjectSessionStart, Session: "b"}, map[string]any{"memoryIds": []string{"m-r"}, "chars": 2000, "titleOnly": 0}) // 같은 세션 재개
	// 요청 4번 중 1번 적중
	for i := 0; i < 3; i++ {
		rec("2026-10-21", memory.Event{Kind: memory.EventInjectPrompt, Session: "a"}, map[string]any{"memoryIds": []string{}, "chars": 0})
	}
	rec("2026-10-21", memory.Event{Kind: memory.EventInjectPrompt, Session: "a"}, map[string]any{"memoryIds": []string{"m-l"}, "chars": 80})
	// 검색 3번 중 2번 빈 결과
	rec("2026-10-22", memory.Event{Kind: "tool.memory_find"}, map[string]any{"ok": true, "results": 0, "query": "배포 순서"})
	rec("2026-10-22", memory.Event{Kind: "tool.memory_find"}, map[string]any{"ok": true, "results": 0, "query": "배포 순서"})
	rec("2026-10-22", memory.Event{Kind: "tool.memory_find"}, map[string]any{"ok": true, "results": 2, "query": "존댓말"})
	rec("2026-10-22", memory.Event{Kind: "tool.memory_save"}, map[string]any{"ok": true, "sourceKind": "quote"})
	rec("2026-10-22", memory.Event{Kind: "tool.memory_save"}, map[string]any{"ok": true, "sourceKind": "session"})
	rec("2026-10-22", memory.Event{Kind: "tool.memory_save"}, map[string]any{"ok": false})
	// 교정: 반복 3(넣음·못넣음·모름) + 같은 기억 2번, 새 교정 1
	rec("2026-10-23", memory.Event{Kind: memory.EventCorrection}, map[string]any{"memoryId": "m-r", "injected": "yes", "quote": "존댓말"})
	rec("2026-10-23", memory.Event{Kind: memory.EventCorrection}, map[string]any{"memoryId": "m-r", "injected": "unknown"})
	rec("2026-10-24", memory.Event{Kind: memory.EventCorrection}, map[string]any{"memoryId": "m-l", "injected": "no"})
	rec("2026-10-24", memory.Event{Kind: memory.EventCorrection}, map[string]any{"memoryId": "", "quote": "표는 짧게"})
	// 제안 결정
	rec("2026-10-24", memory.Event{Kind: memory.EventProposalDecided}, map[string]any{"decision": "approve"})
	rec("2026-10-24", memory.Event{Kind: memory.EventProposalDecided}, map[string]any{"decision": "edit"})
	rec("2026-10-24", memory.Event{Kind: memory.EventProposalDecided}, map[string]any{"decision": "reject"})

	at(s, "2026-10-20", 9)
	r, _ := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "존댓말로"}})
	s.DB().Exec(`UPDATE memories SET id='m-r' WHERE id=?`, r.ID)
	m, _ := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "대기 후보", Source: memory.Source{Kind: memory.SourceSession}})
	s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "대기", MemoryIDs: []string{m.ID}})
	// 회고: 완료 2(60초, 120초), 빈 1, 실패 1
	day := func(d string, h int) int64 { at(s, d, h); return s.Now().UnixMilli() }
	s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at) VALUES('r1','daily','2026-10-21','done',?,?)`, day("2026-10-22", 4), day("2026-10-22", 4)+60000)
	s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at) VALUES('r2','daily','2026-10-22','done',?,?)`, day("2026-10-23", 4), day("2026-10-23", 4)+120000)
	s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at) VALUES('r3','daily','2026-10-23','empty',?,?)`, day("2026-10-24", 4), day("2026-10-24", 4))
	s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at,error) VALUES('r4','daily','2026-10-24','failed',?,?,'x')`, day("2026-10-25", 4), day("2026-10-25", 4))
	at(s, "2026-10-27", 12)

	rep, err := Compute(s, "2026-10-21", "2026-10-27")
	if err != nil {
		t.Fatal(err)
	}
	c := rep.Corrections
	if c.Repeated != 3 || c.Injected != 1 || c.NotInjected != 1 || c.Unknown != 1 || c.New != 1 {
		t.Errorf("corrections = %+v", c)
	}
	if len(c.Top) == 0 || c.Top[0].ID != "m-r" || c.Top[0].Count != 2 || c.Top[0].Title != "답은 존댓말" {
		t.Errorf("top = %+v", c.Top)
	}
	if rep.Prompts.Total != 4 || rep.Prompts.Hits != 1 || rep.Prompts.HitRate != 0.25 {
		t.Errorf("prompts = %+v", rep.Prompts)
	}
	if rep.Find.Total != 3 || rep.Find.Empty != 2 || len(rep.Find.EmptyQueries) != 1 || rep.Find.EmptyQueries[0].Query != "배포 순서" || rep.Find.EmptyQueries[0].Count != 2 {
		t.Errorf("find = %+v", rep.Find)
	}
	if rep.Saves.Total != 2 || rep.Saves.Quote != 1 || rep.Saves.Other != 1 {
		t.Errorf("saves = %+v", rep.Saves)
	}
	if rep.SessionStart.Sessions != 2 || rep.SessionStart.Starts != 3 || rep.SessionStart.AvgChars != 2000 || rep.SessionStart.TitleOnlyMax != 2 {
		t.Errorf("session start = %+v", rep.SessionStart)
	}
	p := rep.Proposals
	if p.Approved != 1 || p.Edited != 1 || p.Rejected != 1 || p.Pending != 1 || p.OldestPendingDays != 7 {
		t.Errorf("proposals = %+v", p)
	}
	if rep.Retros.Runs != 4 || rep.Retros.Done != 2 || rep.Retros.Empty != 1 || rep.Retros.Failed != 1 || rep.Retros.AvgSeconds != 90 {
		t.Errorf("retros = %+v", rep.Retros)
	}
	if rep.Tools["memory_find"] != 3 || rep.Tools["memory_save"] != 3 {
		t.Errorf("tools = %+v", rep.Tools)
	}

	text := Render(rep)
	for _, want := range []string{"2026-10-21 ~ 2026-10-27", "반복 교정 3건", "넣었는데 어김 1", "답은 존댓말 (m-r) 2회", "빈 검색 2/3", "배포 순서", "실패 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("render missing %q:\n%s", want, text)
		}
	}
}

func TestCompute_사건이_없어도_빈_보고를_낸다(t *testing.T) {
	s := store(t, time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local))
	rep, err := Compute(s, "2026-10-21", "2026-10-27")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Prompts.HitRate != 0 || rep.Find.EmptyRate != 0 {
		t.Fatalf("rates must be 0: %+v", rep)
	}
	if !strings.Contains(Render(rep), "반복 교정 0건") {
		t.Fatal(Render(rep))
	}
}
