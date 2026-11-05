package memory

// S4-12: 제목이 「A — B」 꼴로 두 주제를 붙인 규칙·결정을 감사 목록에 보인다. 앞뒤가 다른 제품을 말하거나
// 앞뒤에 같은 낱말이 하나도 없으면 두 주장이 한 기억에 붙은 것으로 본다. 같은 주제를 풀어 쓴 꼬리는 보이지 않는다.

import "testing"

func TestTwoTopics_A_B_제목에서_다른_주제를_붙인_규칙과_결정만_보인다(t *testing.T) {
	s := supStore(t)
	save := func(kind, title string) Memory { m, _ := mustSave(t, s, aiLearns(kind, title, title)); return m }
	desk := save(KindDecision, "질문(AskUserQuestion)은 데스크탑 카드로 답하고 봇에는 알림만 보낸다 — 권한은 항상 양쪽 (2026-10-30)")
	prod := save(KindRule, "Atlas 문서는 onidot Space에 쓴다 — Forma 부품은 생성기에서 고친다")
	save(KindDecision, "Orbit DB는 Studio와 상관없는 별도 앱 — Studio 호환·회귀를 기준으로 삼지 않는다 (2026-10-20)") // 같은 낱말 studio
	save(KindRule, "답은 존댓말로 한다")                                                               // 대시 없음
	save(KindLesson, "교훈은 보지 않는다 — 다른 주제 둘")                                                   // 규칙·결정만
	save(KindRule, "Atlas 댓글 — 밑줄")                                                            // 뒤가 한 낱말: 꼬리
	got, err := s.TwoTopicTitles([]string{"atlas", "forma", "orbit-studio"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	why := map[string]string{}
	for _, g := range got {
		why[g.ID] = g.Why
	}
	if why[desk.ID] != TwoTopicsNoSharedWord || why[prod.ID] != TwoTopicsProducts {
		t.Fatalf("why %+v", why)
	}
	a, err := s.Audit(AuditFilter{Products: []string{"atlas", "forma"}})
	if err != nil || len(a.TwoTopics) != 2 {
		t.Fatalf("audit %+v %v", a.TwoTopics, err)
	}
}
