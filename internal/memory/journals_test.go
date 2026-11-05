package memory

// S4-8(설계 · orbit-mem 2판 11절 5): 일지 기억은 기억이 아니라 하루 기록이다. 살아 있는 일지마다 활동 기록에
// 한 줄(제목·본문 그대로)로 옮기고 원래 기억은 이유와 함께 낡음으로 표시한다. 일지마다 원장 한 줄이고,
// 되돌리면 기억이 돌아오고 옮긴 활동 줄은 빠진다. 다시 돌려도 더 바뀌지 않는다.

import (
	"strings"
	"testing"
)

func journal(t *testing.T, s *Store, in SaveInput) Memory {
	t.Helper()
	in.Kind = KindJournal
	m, _ := mustSave(t, s, in)
	return m
}

func TestJournals_일지는_활동_기록으로_옮기고_원장으로_되돌린다(t *testing.T) {
	s := supStore(t)
	j1 := journal(t, s, SaveInput{Title: "2026-11-03 하루 회고", Body: "Atlas 댓글 밑줄을 고쳤다.\n시험 실패 0", Source: Source{Kind: SourceSession, Ref: "r"}, Who: Who{Actor: ActorAI}})
	j2 := journal(t, s, onidotFolder(SaveInput{Title: "2026-11-04 하루 회고", Body: "사용자가 말한 그대로: 오늘은 정리만", Source: Source{Kind: SourceQuote, Excerpt: "오늘은 정리만"}, Who: Who{Actor: ActorAI}}))
	old := journal(t, s, SaveInput{Title: "옛 일지", Body: "x", Source: Source{Kind: SourceSession, Ref: "r"}, Who: Who{Actor: ActorAI}})
	if _, err := s.SetState(old.ID, StateStale, "이미 낡음"); err != nil {
		t.Fatal(err)
	}
	rule, _ := mustSave(t, s, aiLearns(KindLesson, "일지 아닌 교훈", ""))

	dry, err := s.JournalsToActivity(true)
	if err != nil || len(dry.Applied) != 2 || dry.Applied[0].LedgerID != "" {
		t.Fatalf("dry %+v %v", dry, err)
	}
	if n := s.ActivityCount(); n != 0 || mustGet(t, s, j1.ID).State == StateStale {
		t.Fatalf("dry run changed something: %d", n)
	}
	rep, err := s.JournalsToActivity(false)
	if err != nil || len(rep.Applied) != 2 {
		t.Fatalf("apply %+v %v", rep, err)
	}
	acts, err := s.Activities(ActivityFilter{Limit: 10})
	if err != nil || len(acts) != 2 {
		t.Fatalf("activities %+v %v", acts, err)
	}
	for _, j := range []Memory{j1, j2} {
		var a Activity
		for _, x := range acts {
			if x.Session == "journal:"+j.ID {
				a = x
			}
		}
		if a.Host != JournalHost || a.Launcher != JournalHost || a.FirstPrompt != j.Title || a.Summary != j.Body || a.StartedAt != j.CreatedAt {
			t.Fatalf("activity of %s: %+v", j.ID, a)
		}
		got := mustGet(t, s, j.ID)
		if got.State != StateStale || !strings.Contains(got.StaleReason, "활동 기록으로 옮김") || got.Body != j.Body {
			t.Fatalf("journal %s after: %+v", j.ID, got)
		}
	}
	if a, _ := s.Activities(ActivityFilter{ProjectScope: j2.Scope}); len(a) != 1 {
		t.Fatalf("folder journal keeps its folder: %+v", a)
	}
	if a, _ := s.Activities(ActivityFilter{Query: "밑줄"}); len(a) != 1 || a[0].Session != "journal:"+j1.ID {
		t.Fatalf("summary is searchable: %+v", a)
	}
	if mustGet(t, s, rule.ID).State == StateStale || mustGet(t, s, old.ID).StaleReason != "이미 낡음" {
		t.Fatal("touched a memory that is not a live journal")
	}
	// 다시 돌리면 0건.
	again, err := s.JournalsToActivity(false)
	if err != nil || len(again.Applied) != 0 || s.ActivityCount() != 2 {
		t.Fatalf("again %+v %v", again, err)
	}
	// 되돌리면 기억이 돌아오고 활동 줄은 빠진다. 사람이 되돌린 것은 다시 옮기지 않는다.
	var lid string
	for _, r := range rep.Applied {
		if r.ID == j1.ID {
			lid = r.LedgerID
		}
	}
	if _, err := s.RevertChange(lid, Who{}, "되돌림"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, j1.ID); got.State == StateStale || s.ActivityCount() != 1 {
		t.Fatalf("revert: %s, %d activities", got.State, s.ActivityCount())
	}
	third, err := s.JournalsToActivity(false)
	if err != nil || len(third.Applied) != 0 || len(third.Skipped) != 1 || s.ActivityCount() != 1 {
		t.Fatalf("after revert %+v %v (%d)", third, err, s.ActivityCount())
	}
}
