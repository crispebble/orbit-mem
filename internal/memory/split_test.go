package memory

// S4-9(설계 · orbit-mem 2판 5-1): 1,000자 넘는 규칙·결정은 계획의 split으로 주장 하나씩 N개로 나눈다. 새 기억은
// 같은 출처를 가지고, 사용자 말 기억은 사용자 말을 그대로 자르기만 한다(더하거나 빼지 않는다). 원래 기억은
// 대체(낡음)되고 원장 한 줄이 남으며, 되돌리면 원래가 돌아오고 나눈 기억은 낡음이 된다. 다시 돌려도 그대로다.

import (
	"strings"
	"testing"
)

func longBody(parts ...string) string { return strings.Join(parts, "\n\n") }

func TestSplit_긴_사용자_말_규칙을_주장_하나씩_나누고_원장으로_되돌린다(t *testing.T) {
	s := supStore(t)
	a, b := "화면 일은 그 시스템의 UI 키트로 만든다. "+strings.Repeat("키트 설명. ", 50), "키트끼리 가져다 쓰지 않는다. "+strings.Repeat("이유 설명. ", 50)
	in := onidotFolder(userSays(KindRule, "UI 키트 규칙 묶음", "ui 키트"))
	in.Body, in.Tags = longBody(a, b), []string{"ui"}
	orig, _ := mustSave(t, s, in)
	srcs := sourceCount(t, s, orig.ID)
	plan := Plan{Items: []PlanItem{{ID: orig.ID, Version: orig.Version, AppliesTo: "product:forma", Basis: "ai:두 주장",
		Split: []PlanPart{{Title: "화면은 그 시스템의 UI 키트로 만든다", Topic: "ui 키트 사용", Body: a}, {Title: "UI 키트끼리 가져다 쓰지 않는다", Topic: "ui 키트 간 의존", Body: "  " + b + "\n"}}}}}
	dry, err := s.ApplyPlan(plan, planMap, true)
	if err != nil || len(dry.Applied) != 1 || mustGet(t, s, orig.ID).State == StateStale {
		t.Fatalf("dry %+v %v", dry, err)
	}
	rep, err := s.ApplyPlan(plan, planMap, false)
	if err != nil || len(rep.Applied) != 1 || rep.Applied[0].LedgerID == "" || len(rep.Applied[0].Parts) != 2 {
		t.Fatalf("apply %+v %v", rep, err)
	}
	for i, id := range rep.Applied[0].Parts {
		m := mustGet(t, s, id)
		want := strings.TrimSpace([]string{a, b}[i]) // 조각 본문은 앞뒤 공백을 뺀다
		if m.Body != want || m.Kind != KindRule || m.State != orig.State || m.Scope != "product:forma" || m.Topic != plan.Items[0].Split[i].Topic ||
			strings.Join(m.Tags, ",") != "ui" || sourceCount(t, s, id) != srcs {
			t.Fatalf("part %d: %+v (sources %d)", i, m, sourceCount(t, s, id))
		}
	}
	old := mustGet(t, s, orig.ID)
	if old.State != StateStale || old.SupersededBy != rep.Applied[0].Parts[0] || !strings.Contains(old.StaleReason, "나눔") || old.Body != orig.Body {
		t.Fatalf("original %+v", old)
	}
	again, err := s.ApplyPlan(plan, planMap, false)
	if err != nil || len(again.Applied) != 0 || len(again.Skipped) != 1 || again.Skipped[0].Skip != "already there" {
		t.Fatalf("again %+v %v", again, err)
	}
	if _, err := s.RevertChange(rep.Applied[0].LedgerID, Who{}, "되돌림"); err != nil {
		t.Fatal(err)
	}
	if m := mustGet(t, s, orig.ID); m.State != orig.State || m.Scope != orig.Scope || m.Topic != orig.Topic {
		t.Fatalf("revert original %+v", m)
	}
	for _, id := range rep.Applied[0].Parts {
		if mustGet(t, s, id).State != StateStale {
			t.Fatalf("revert part %s still live", id)
		}
	}
	third, err := s.ApplyPlan(plan, planMap, false)
	if err != nil || len(third.Applied) != 0 {
		t.Fatalf("reverted split redone %+v %v", third, err)
	}
}

func TestSplit_사용자_말을_바꾸거나_틀린_나누기는_하지_않는다(t *testing.T) {
	s := supStore(t)
	a, b := strings.Repeat("가나다 ", 120), strings.Repeat("라마바 ", 120)
	in := userSays(KindDecision, "사용자 결정 묶음", "결정 묶음")
	in.Body = longBody(a, b)
	in.Source.Excerpt = in.Body // 사용자 말이 본문에 있다: 자르기만 한다(Q2)
	quoted, _ := mustSave(t, s, in)
	ai := aiLearns(KindDecision, "AI 결정 묶음", "ai 묶음")
	ai.Body = longBody(a, b)
	learned, _ := mustSave(t, s, ai)
	holder, _ := mustSave(t, s, aiLearns(KindDecision, "주제를 가진 기억", "이미 있는 주제"))
	part := func(title, topic, body string) PlanPart { return PlanPart{Title: title, Topic: topic, Body: body} }
	for name, c := range map[string]struct {
		it   PlanItem
		skip string
	}{
		"사용자 말을 고쳐 씀": {PlanItem{ID: quoted.ID, Split: []PlanPart{part("앞", "앞 주제", a+" 덧붙임"), part("뒤", "뒤 주제", b)}}, "user quote"},
		"사용자 말을 뺌":    {PlanItem{ID: quoted.ID, Split: []PlanPart{part("앞", "앞 주제", a), part("뒤", "뒤 주제", b[:30])}}, "user quote"},
		"하나뿐":         {PlanItem{ID: learned.ID, Split: []PlanPart{part("하나", "하나 주제", a)}}, "split:"},
		"1000자 넘음":    {PlanItem{ID: learned.ID, Split: []PlanPart{part("앞", "앞 주제", strings.Repeat("가", 1001)), part("뒤", "뒤 주제", b)}}, "split:"},
		"주제 없음":       {PlanItem{ID: learned.ID, Split: []PlanPart{part("앞", "", a), part("뒤", "뒤 주제", b)}}, "split:"},
		"주제 겹침":       {PlanItem{ID: learned.ID, Split: []PlanPart{part("앞", "이미 있는 주제", a), part("뒤", "뒤 주제", b)}}, "topic taken by " + holder.ID},
		"계획 뒤 바뀜":     {PlanItem{ID: learned.ID, Version: learned.Version + 1, Split: []PlanPart{part("앞", "앞 주제", a), part("뒤", "뒤 주제", b)}}, "changed"},
	} {
		rep, err := s.ApplyPlan(Plan{Items: []PlanItem{c.it}}, planMap, false)
		got := append(rep.Skipped, rep.Conflicts...)
		if err != nil || len(rep.Applied) != 0 || len(got) != 1 || !strings.HasPrefix(got[0].Skip, c.skip) {
			t.Fatalf("%s: %+v %v", name, rep, err)
		}
	}
	// AI가 배운 것은 다시 써도 된다(사용자 말이 아니다).
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: learned.ID, Split: []PlanPart{part("앞", "앞 주제", "가나다를 줄여 씀"), part("뒤", "뒤 주제", "라마바를 줄여 씀")}}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("ai rewrite %+v %v", rep, err)
	}
}
