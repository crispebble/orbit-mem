package memory

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEvent_기록하고_기간과_종류로_읽는다(t *testing.T) {
	s, c := newTestStore(t)
	if err := s.RecordEvent(Event{Kind: EventInjectPrompt, Host: "claude", Session: "s1"}, map[string]any{"memoryIds": []string{"m-a"}, "chars": 10}); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24 * time.Hour)
	s.RecordEvent(Event{Kind: "tool.memory_find"}, map[string]any{"results": 0})
	s.RecordEvent(Event{Kind: EventCorrection, Day: "2026-10-20"}, nil) // 하루 회고는 대상 날짜로 남긴다

	all, err := s.Events(EventFilter{FromDay: "2026-10-27", ToDay: "2026-10-28"})
	if err != nil || len(all) != 2 {
		t.Fatalf("events = %+v %v", all, err)
	}
	if all[0].Kind != EventInjectPrompt || all[0].Day != "2026-10-27" || all[0].Host != "claude" || all[0].Session != "s1" {
		t.Fatalf("first = %+v", all[0])
	}
	var d struct {
		MemoryIDs []string `json:"memoryIds"`
		Chars     int      `json:"chars"`
	}
	if err := json.Unmarshal(all[0].Data, &d); err != nil || d.Chars != 10 || d.MemoryIDs[0] != "m-a" {
		t.Fatalf("data = %s", all[0].Data)
	}
	tools, _ := s.Events(EventFilter{FromDay: "2026-10-01", ToDay: "2026-10-31", KindPrefix: "tool."})
	if len(tools) != 1 || tools[0].Kind != "tool.memory_find" {
		t.Fatalf("tools = %+v", tools)
	}
	old, _ := s.Events(EventFilter{FromDay: "2026-10-20", ToDay: "2026-10-20"})
	if len(old) != 1 || old[0].Kind != EventCorrection {
		t.Fatalf("by day = %+v", old)
	}
}

func TestEvent_세션에_그_기억을_넣었는지_안다(t *testing.T) {
	s, _ := newTestStore(t)
	s.RecordEvent(Event{Kind: EventInjectSessionStart, Session: "s1"}, map[string]any{"memoryIds": []string{"m-a"}})
	s.RecordEvent(Event{Kind: EventInjectPrompt, Session: "s1"}, map[string]any{"memoryIds": []string{"m-b"}})
	s.RecordEvent(Event{Kind: EventInjectPrompt, Session: "s2"}, map[string]any{"memoryIds": []string{}})
	cases := []struct {
		session, id     string
		injected, known bool
	}{
		{"s1", "m-a", true, true},
		{"s1", "m-b", true, true},
		{"s1", "m-c", false, true},
		{"s2", "m-a", false, true},
		{"s3", "m-a", false, false}, // 주입 사건이 없는 세션
		{"", "m-a", false, false},
	}
	for _, c := range cases {
		injected, known := s.InjectedIn(c.session, c.id)
		if injected != c.injected || known != c.known {
			t.Errorf("%s/%s = %v,%v", c.session, c.id, injected, known)
		}
	}
}

func TestDecide_결정을_사건으로_남긴다(t *testing.T) {
	s, c := newTestStore(t)
	a, _ := s.Save(SaveInput{Kind: KindFact, Title: "가", Source: Source{Kind: SourceSession}})
	b, _ := s.Save(SaveInput{Kind: KindFact, Title: "나", Source: Source{Kind: SourceSession}})
	pa, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "가 확정", MemoryIDs: []string{a.ID}})
	pb, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "나 확정", MemoryIDs: []string{b.ID}})
	c.t = c.t.Add(2 * time.Hour)
	title := "고친 가"
	if _, err := s.Decide(DecideInput{ID: pa.ID, Decision: "approve", Title: &title}); err != nil {
		t.Fatal(err)
	}
	empty := ""
	cm, _ := s.Save(SaveInput{Kind: KindFact, Title: "다", Source: Source{Kind: SourceSession}})
	pc, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "다 확정", MemoryIDs: []string{cm.ID}})
	if _, err := s.Decide(DecideInput{ID: pc.ID, Decision: "approve", Body: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: pb.ID, Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Events(EventFilter{FromDay: "2026-10-27", ToDay: "2026-10-27", KindPrefix: EventProposalDecided})
	if len(evs) != 3 {
		t.Fatalf("events = %+v", evs)
	}
	var d struct {
		ProposalKind string `json:"proposalKind"`
		Decision     string `json:"decision"`
		WaitMs       int64  `json:"waitMs"`
	}
	json.Unmarshal(evs[0].Data, &d)
	if d.Decision != "edit" || d.ProposalKind != ProposalConfirm || d.WaitMs != 2*3600*1000 {
		t.Fatalf("first = %s", evs[0].Data)
	}
	json.Unmarshal(evs[1].Data, &d)
	if d.Decision != "approve" {
		t.Fatalf("empty title is not an edit: %s", evs[1].Data)
	}
	json.Unmarshal(evs[2].Data, &d)
	if d.Decision != "reject" {
		t.Fatalf("third = %s", evs[2].Data)
	}
}
