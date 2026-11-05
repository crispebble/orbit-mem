package memory

// S4 재검토(2026-11-05) 지적 N1·N3·N4·N5를 고정하는 시험.

import (
	"strings"
	"testing"
)

// N1: 사람이 되돌린 옮기기를 계획이 다시 하려 하면 그 항목은 건너뛰고, 그 기억의 자리는 비지 않는다.
func TestReviewN1_되돌린_옮기기를_다시_하는_항목은_자리를_비우지_않고_미리_보기와_같다(t *testing.T) {
	s := supStore(t)
	in := aiLearns(KindDecision, "Atlas 칸 결정", "atlas 칸")
	in.Scope = "product:atlas"
	m, _ := mustSave(t, s, in)
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Version: m.Version, Topic: "atlas 새 칸", Basis: "ai"}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := s.RevertChange(rep.Applied[0].LedgerID, Who{}, "사람이 되돌림"); err != nil {
		t.Fatal(err)
	}
	n, _ := mustSave(t, s, onidotFolder(aiLearns(KindDecision, "Atlas 칸 다른 결정", "")))
	cur := mustGet(t, s, m.ID)
	rep = samePlanRun(t, s, Plan{Items: []PlanItem{
		{ID: m.ID, Version: cur.Version, Topic: "atlas 새 칸", Basis: "ai"},
		{ID: n.ID, Version: n.Version, AppliesTo: "product:atlas", Topic: "atlas 칸", Basis: "ai"},
	}})
	if liveHolders(t, s, "product:atlas", "atlas 칸") != 1 || len(rep.Applied) != 0 {
		t.Fatalf("report %+v", rep)
	}
}

// N3: 같은 옛 기억을 두 항목이 대체하면 미리 보기도 실제처럼 두 번째를 「not live」로 본다.
func TestReviewN3_같은_기억을_두_번_대체하는_계획도_미리_보기와_같다(t *testing.T) {
	s := supStore(t)
	a, _ := mustSave(t, s, aiLearns(KindLesson, "옛 교훈", ""))
	b, _ := mustSave(t, s, aiLearns(KindLesson, "새 교훈 하나", ""))
	c, _ := mustSave(t, s, aiLearns(KindLesson, "새 교훈 둘", ""))
	rep := samePlanRun(t, s, Plan{Items: []PlanItem{{ID: a.ID, SupersededBy: b.ID, Basis: "ai"}, {ID: a.ID, SupersededBy: c.ID, Basis: "ai"}}})
	if len(rep.Applied) != 1 || len(rep.Skipped) != 1 || !strings.HasPrefix(rep.Skipped[0].Skip, "not live") {
		t.Fatalf("%+v", rep)
	}
}

func openFlags(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_flags WHERE memory_id=? AND resolved_at=0`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// N4: 일지 옮기기와 나누기로 낡음이 된 기억에 열린 감사 표시가 남지 않는다.
func TestReviewN4_옮긴_일지와_나눈_원래_기억의_열린_감사_표시는_닫힌다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, userSays(KindRule, "답은 존댓말", "말투"))
	j := journal(t, s, SaveInput{Title: "하루", Body: "일", Source: Source{Kind: SourceSession, Ref: "r"}, Who: Who{Actor: ActorAI}})
	in := aiLearns(KindDecision, "긴 결정", "긴 결정")
	in.Body = strings.Repeat("가", 600) + "\n\n" + strings.Repeat("나", 600)
	m, _ := mustSave(t, s, in)
	for _, id := range []string{j.ID, m.ID} {
		if _, err := s.FlagConflict(id, rule.ID, "어긋남", Who{}); err != nil {
			t.Fatal(err)
		}
	}
	if rep, err := s.JournalsToActivity(false); err != nil || len(rep.Applied) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Split: []PlanPart{{Title: "가", Topic: "가 주제", Body: "가 결정"}, {Title: "나", Topic: "나 주제", Body: "나 결정"}}}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if openFlags(t, s, j.ID) != 0 || openFlags(t, s, m.ID) != 0 {
		t.Fatalf("open flags: journal %d, split %d", openFlags(t, s, j.ID), openFlags(t, s, m.ID))
	}
}

// N5: 나누기 직전(트랜잭션 밖에서 읽은 뒤) 원래 기억이 바뀌면 나누지 않고 「changed」로 건너뛴다.
func TestReviewN5_나누기_직전에_바뀐_원래_기억은_나누지_않는다(t *testing.T) {
	s := supStore(t)
	in := aiLearns(KindDecision, "긴 결정", "긴 결정")
	in.Body = strings.Repeat("가", 600) + "\n\n" + strings.Repeat("나", 600)
	m, _ := mustSave(t, s, in)
	splitTxHook = func() {
		if _, err := s.Update(UpdateInput{ID: m.ID, Body: ptr("다른 사람이 고친 본문")}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { splitTxHook = nil })
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Split: []PlanPart{{Title: "가", Topic: "가 주제", Body: "가 결정"}, {Title: "나", Topic: "나 주제", Body: "나 결정"}}}}}, planMap, false)
	if err != nil || len(rep.Applied) != 0 || len(rep.Skipped) != 1 || !strings.HasPrefix(rep.Skipped[0].Skip, "changed") {
		t.Fatalf("%+v %v", rep, err)
	}
	if got := mustGet(t, s, m.ID); got.State == StateStale || got.Body != "다른 사람이 고친 본문" {
		t.Fatalf("original overwritten: %+v", got)
	}
}
