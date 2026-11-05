package memory

// S4 독립 검토(2026-11-05) 지적을 고정하는 시험: 계획 적용의 자리 겹침·미리 보기 일치(A1), 사용자 말 판정과
// 엄격한 자르기(A2), 대체의 판 확인(A3), 되돌린 뒤 자리 겹침 표시(A4), 일지 되돌림의 되돌림(A5),
// 한 트랜잭션(A6), 옮기기는 제목·본문·태그를 건드리지 않음(A7), 옛 꼴 DB의 summary 열.

import (
	"path/filepath"
	"strings"
	"testing"
)

type planOutcome struct{ id, action, skip string }

func outcomes(rep PlanReport) []planOutcome {
	var out []planOutcome
	for _, list := range [][]PlanResult{rep.Applied, rep.Skipped, rep.Conflicts} {
		for _, r := range list {
			out = append(out, planOutcome{r.ID, r.Action, r.Skip})
		}
	}
	return out
}

func liveHolders(t *testing.T, s *Store, scope, topic string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE scope=? AND topic=? AND state IN ('candidate','confirmed')`, scope, topic).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func samePlanRun(t *testing.T, s *Store, plan Plan) PlanReport {
	t.Helper()
	dry, err := s.ApplyPlan(plan, planMap, true)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.ApplyPlan(plan, planMap, false)
	if err != nil {
		t.Fatal(err)
	}
	d, r := outcomes(dry), outcomes(rep)
	if len(d) != len(r) {
		t.Fatalf("dry run %+v differs from the run %+v", d, r)
	}
	for i := range d {
		if d[i] != r[i] {
			t.Fatalf("dry run %+v differs from the run %+v", d, r)
		}
	}
	return rep
}

func TestReviewA1_막힌_대체의_자리로는_옮기지_않고_미리_보기와_같다(t *testing.T) {
	s := supStore(t)
	in := userSays(KindDecision, "Atlas 댓글은 밑줄", "atlas 댓글")
	in.Scope = "product:atlas"
	old, _ := mustSave(t, s, in)
	if _, err := s.Pin(old.ID, Who{}); err != nil {
		t.Fatal(err)
	}
	nw, _ := mustSave(t, s, onidotFolder(userSays(KindDecision, "Atlas 댓글은 밑줄과 여백 카드", "새 댓글")))
	plan := Plan{Items: []PlanItem{
		{ID: nw.ID, Version: nw.Version, AppliesTo: "product:atlas", Topic: "atlas 댓글", Basis: "ai"},
		{ID: old.ID, Version: old.Version, SupersededBy: nw.ID, Basis: "ai"},
	}}
	rep := samePlanRun(t, s, plan)
	if n := liveHolders(t, s, "product:atlas", "atlas 댓글"); n != 1 {
		t.Fatalf("%d current values in one slot: %+v", n, rep)
	}
	if len(rep.Applied) != 0 || len(rep.Conflicts) != 1 || !strings.HasPrefix(rep.Conflicts[0].Skip, "topic taken by "+old.ID) {
		t.Fatalf("report %+v", rep)
	}
}

func TestReviewA1_같은_자리를_두_항목이_받으면_뒤의_것은_어긋남이고_미리_보기도_같다(t *testing.T) {
	s := supStore(t)
	a, _ := mustSave(t, s, onidotFolder(aiLearns(KindDecision, "Atlas 문서는 Space에", "")))
	b, _ := mustSave(t, s, onidotFolder(aiLearns(KindDecision, "Atlas 문서 위치는 Space", "")))
	plan := Plan{Items: []PlanItem{
		{ID: a.ID, Version: a.Version, AppliesTo: "product:atlas", Topic: "atlas 문서 위치", Basis: "ai"},
		{ID: b.ID, Version: b.Version, AppliesTo: "product:atlas", Topic: "atlas 문서 위치", Basis: "ai"},
	}}
	rep := samePlanRun(t, s, plan)
	if len(rep.Applied) != 1 || len(rep.Conflicts) != 1 || liveHolders(t, s, "product:atlas", "atlas 문서 위치") != 1 {
		t.Fatalf("report %+v", rep)
	}
}

func TestReviewA2_출처_시각이_0인_사용자_말도_사용자_말이다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userSays(KindDecision, "문서는 Atlas에", "문서 저장처"))
	if _, err := s.db.Exec(`UPDATE sources SET at=0 WHERE memory_id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	nw, _ := mustSave(t, s, aiLearns(KindDecision, "문서는 Wiki에", "다른 주제"))
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: old.ID, SupersededBy: nw.ID, Basis: "ai"}}}, planMap, false)
	if err != nil || len(rep.Applied) != 0 || len(rep.Conflicts) != 1 || !strings.HasPrefix(rep.Conflicts[0].Skip, "user quote") {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestReviewA2_본문에_든_사용자_말은_안쪽_공백까지_그대로_잘라야_한다(t *testing.T) {
	s := supStore(t)
	a, b := "가나다 라마바 "+strings.Repeat("사아자 ", 100), "차카타 파하 "+strings.Repeat("거너더 ", 100)
	in := userSays(KindDecision, "사용자 결정 묶음", "결정 묶음")
	in.Body = a + "\n\n" + b
	in.Source.Excerpt = in.Body
	m, _ := mustSave(t, s, in)
	part := func(body string) []PlanPart {
		return []PlanPart{{Title: "앞", Topic: "앞 주제", Body: body}, {Title: "뒤", Topic: "뒤 주제", Body: b}}
	}
	for name, p := range map[string][]PlanPart{
		"안쪽 공백 뺌":  part(strings.Replace(a, "가나다 라마바", "가나다라마바", 1)),
		"안쪽 공백 넣음": part(strings.Replace(a, "가나다 라마바", "가나다  라마바", 1)),
		"순서 바꿈":    {{Title: "뒤", Topic: "뒤 주제", Body: b}, {Title: "앞", Topic: "앞 주제", Body: a}},
	} {
		rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Split: p}}}, planMap, false)
		if err != nil || len(rep.Applied) != 0 || len(rep.Conflicts) != 1 || !strings.HasPrefix(rep.Conflicts[0].Skip, "user quote") {
			t.Fatalf("%s: %+v %v", name, rep, err)
		}
	}
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Split: part("  " + a + "\n")}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("exact cut: %+v %v", rep, err)
	}
}

// 사용자 결정(2026-11-05 Q2): 사용자 말이 본문에 없으면(옮기기 승인 말만 출처에 있음) AI가 주장 하나씩 다시 쓴다.
// 다만 참고 줄(원래 출처·PAGE·URL)만 담은 조각은 만들지 않는다.
func TestReviewQ2_본문에_없는_사용자_말은_다시_쓰되_참고_줄만_담은_조각은_거절한다(t *testing.T) {
	s := supStore(t)
	in := userSays(KindDecision, "Studio 결정 묶음", "studio 묶음")
	in.Body = strings.Repeat("결정 하나. ", 80) + "\n\n" + strings.Repeat("결정 둘. ", 80)
	in.Source.Excerpt = "기억 범위 정리 (추천): 다른 제품 전용인 기억을 제품 범위로 옮깁니다."
	m, _ := mustSave(t, s, in)
	refs := PlanPart{Title: "출처", Topic: "출처 목록", Body: "## 원래 출처\n- PAGE: 0rw3728p48x8j — 제품 기획 정본 Rev16\n- URL: https://app.onidot.dev/w/x/"}
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Split: []PlanPart{{Title: "결정 하나", Topic: "결정 하나", Body: "Studio는 결정 하나를 따른다."}, refs}}}}, planMap, false)
	if err != nil || len(rep.Applied) != 0 || len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0].Skip, "only references") {
		t.Fatalf("reference piece: %+v %v", rep, err)
	}
	rep, err = s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Split: []PlanPart{{Title: "결정 하나", Topic: "결정 하나", Body: "Studio는 결정 하나를 따른다."}, {Title: "결정 둘", Topic: "결정 둘", Body: "Studio는 결정 둘을 따른다(근거: PAGE 0rw3728p48x8j)."}}}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("rewrite: %+v %v", rep, err)
	}
}

func TestReviewA3_대체도_계획_뒤_바뀐_기억은_건너뛰고_같은_항목의_옮기기는_걸리지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, onidotFolder(aiLearns(KindLesson, "옛 교훈", "")))
	nw, _ := mustSave(t, s, onidotFolder(aiLearns(KindLesson, "새 교훈", "")))
	stale := old.Version
	if _, err := s.Update(UpdateInput{ID: old.ID, Body: ptr("고침")}); err != nil {
		t.Fatal(err)
	}
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: old.ID, Version: stale, SupersededBy: nw.ID, Basis: "ai"}}}, planMap, false)
	if err != nil || len(rep.Applied) != 0 || len(rep.Skipped) != 1 || !strings.HasPrefix(rep.Skipped[0].Skip, "changed") {
		t.Fatalf("changed: %+v %v", rep, err)
	}
	cur := mustGet(t, s, old.ID)
	rep, err = s.ApplyPlan(Plan{Items: []PlanItem{{ID: old.ID, Version: cur.Version, AppliesTo: "product:atlas", SupersededBy: nw.ID, Basis: "ai"}}}, planMap, false)
	if err != nil || len(rep.Applied) != 2 || mustGet(t, s, old.ID).SupersededBy != nw.ID {
		t.Fatalf("move and supersede: %+v %v", rep, err)
	}
}

func TestReviewA4_되돌려서_한_자리에_현재값이_둘이면_감사_목록에_어긋남으로_남긴다(t *testing.T) {
	s := supStore(t)
	a, _ := mustSave(t, s, onidotFolder(aiLearns(KindDecision, "Atlas 댓글 밑줄", "")))
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: a.ID, Version: a.Version, Topic: "댓글 표시", Basis: "ai"}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := s.Update(UpdateInput{ID: a.ID, Topic: ptr("다른 주제")}); err != nil {
		t.Fatal(err)
	}
	b, _ := mustSave(t, s, onidotFolder(aiLearns(KindDecision, "Atlas 댓글 배경", "댓글 표시")))
	// 사람이 a의 판 4를 고쳐서 원장 줄은 그대로 되돌릴 수 없다: 판을 맞춘 다른 경우로 본다.
	c, _ := mustSave(t, s, onidotFolder(aiLearns(KindDecision, "Forma 버튼", "")))
	rep, err = s.ApplyPlan(Plan{Items: []PlanItem{{ID: c.ID, Version: c.Version, Topic: "버튼 모양", Basis: "ai"}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	_ = b
	// c를 옮긴 뒤 같은 자리에 같은 주제가 하나 더 생겼다가, c의 주제 주기를 되돌렸다가 다시 되돌리면 둘이 된다.
	d, _ := mustSave(t, s, onidotFolder(aiLearns(KindDecision, "Forma 버튼 둥글게", "")))
	if _, err := s.RevertChange(rep.Applied[0].LedgerID, Who{}, "되돌림"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(UpdateInput{ID: d.ID, Topic: ptr("버튼 모양")}); err != nil {
		t.Fatal(err)
	}
	var revertLine string
	if err := s.db.QueryRow(`SELECT id FROM ledger WHERE revert_of=?`, rep.Applied[0].LedgerID).Scan(&revertLine); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevertChange(revertLine, Who{}, "되돌림의 되돌림"); err != nil {
		t.Fatal(err)
	}
	if liveHolders(t, s, c.Scope, "버튼 모양") != 2 {
		t.Fatal("setup: expected two current values")
	}
	au, err := s.Audit(AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range au.Conflicts {
		found = found || (f.MemoryID == c.ID && f.OtherID == d.ID) || (f.MemoryID == d.ID && f.OtherID == c.ID)
	}
	if !found {
		t.Fatalf("no conflict in the audit list: %+v", au.Conflicts)
	}
}

func TestReviewA5_일지_옮기기를_되돌린_것을_되돌리면_활동_줄이_돌아온다(t *testing.T) {
	s := supStore(t)
	j := journal(t, s, SaveInput{Title: "2026-11-03 하루 회고", Body: "정리만 했다", Source: Source{Kind: SourceSession, Ref: "r"}, Who: Who{Actor: ActorAI}})
	rep, err := s.JournalsToActivity(false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	res, err := s.RevertChange(rep.Applied[0].LedgerID, Who{}, "되돌림")
	if err != nil || s.ActivityCount() != 0 || mustGet(t, s, j.ID).State == StateStale {
		t.Fatalf("revert: %v %d", err, s.ActivityCount())
	}
	if _, err := s.RevertChange(res.LedgerID, Who{}, "되돌림의 되돌림"); err != nil {
		t.Fatal(err)
	}
	acts, _ := s.Activities(ActivityFilter{})
	if mustGet(t, s, j.ID).State != StateStale || len(acts) != 1 || acts[0].Session != JournalSession(j.ID) || acts[0].Summary != j.Body || acts[0].Host != JournalHost {
		t.Fatalf("revert of revert: %s %+v", mustGet(t, s, j.ID).State, acts)
	}
}

func TestReviewA6_원장을_못_쓰면_나누기와_일지_옮기기는_아무것도_남기지_않는다(t *testing.T) {
	s := supStore(t)
	in := aiLearns(KindDecision, "긴 결정", "긴 결정")
	in.Body = strings.Repeat("가", 600) + "\n\n" + strings.Repeat("나", 600)
	m, _ := mustSave(t, s, in)
	j := journal(t, s, SaveInput{Title: "하루", Body: "일", Source: Source{Kind: SourceSession, Ref: "r"}, Who: Who{Actor: ActorAI}})
	count := func(q string) int {
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	memories, proposals := count(`SELECT COUNT(*) FROM memories`), count(`SELECT COUNT(*) FROM proposals`)
	// 원장 쓰기만 실패하게 한다(읽기는 된다).
	if _, err := s.db.Exec(`CREATE TRIGGER ledger_fail BEFORE INSERT ON ledger BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Split: []PlanPart{{Title: "가", Topic: "가 주제", Body: "가 결정"}, {Title: "나", Topic: "나 주제", Body: "나 결정"}}}}}, planMap, false)
	if err == nil {
		t.Fatal("split without a ledger succeeded")
	}
	if _, err := s.JournalsToActivity(false); err == nil {
		t.Fatal("journal move without a ledger succeeded")
	}
	if count(`SELECT COUNT(*) FROM memories`) != memories || count(`SELECT COUNT(*) FROM proposals`) != proposals || s.ActivityCount() != 0 ||
		mustGet(t, s, m.ID).State == StateStale || mustGet(t, s, j.ID).State == StateStale {
		t.Fatal("half state left")
	}
}

func TestReviewA7_옮기기는_지저분한_태그와_긴_제목을_건드리지_않는다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, onidotFolder(aiLearns(KindLesson, "짧은 제목", "")))
	long := strings.Repeat("긴제목", 80)
	if _, err := s.db.Exec(`UPDATE memories SET title=?, tags=? WHERE id=?`, long, `["  Foo ","foo","Bar"]`, m.ID); err != nil {
		t.Fatal(err)
	}
	rep, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: m.ID, Version: m.Version, AppliesTo: "product:atlas", Basis: "ai"}}}, planMap, false)
	if err != nil || len(rep.Applied) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	var title, tags, scope string
	if err := s.db.QueryRow(`SELECT title,tags,scope FROM memories WHERE id=?`, m.ID).Scan(&title, &tags, &scope); err != nil {
		t.Fatal(err)
	}
	if title != long || tags != `["  Foo ","foo","Bar"]` || scope != "product:atlas" {
		t.Fatalf("title or tags touched: %q %s %s", title[:20], tags, scope)
	}
}

func TestOldShape_summary_열이_없는_DB를_열면_열을_더하고_옛_꼴_INSERT도_된다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE activity DROP COLUMN summary`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO activity(host,session,project_scope,project_label,cwd,launcher,parent,started_at,last_at,ended_at,prompts,first_prompt,last_prompt)
		VALUES('claude','s1','','','','human','',1,1,0,1,'첫 요청','첫 요청')`); err != nil {
		t.Fatal(err)
	}
	acts, err := s.Activities(ActivityFilter{})
	if err != nil || len(acts) != 1 || acts[0].Summary != "" {
		t.Fatalf("%+v %v", acts, err)
	}
}
