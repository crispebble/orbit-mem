package memory

// S4(설계 · orbit-mem 2판 11절): 1판 기억을 2판 자리로 옮기는 계획 적용. 판단(어느 제품·어떤 주제·무엇이 무엇을
// 대체하나)은 실행 파일 밖(AI)이 계획 파일로 만들고, 실행 파일은 그 계획을 결정적으로 적용하며 바꾼 것마다
// 원장 한 줄을 남겨 감사 목록에서 되돌리게 한다. 내용(제목·본문·태그·출처)은 바꾸지 않는다.

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/place"
)

var planMap = place.Map{
	"atlas": {Match: []string{"github.com/o/atlas", "~/w/onidot"}},
	"forma": {Match: []string{"github.com/o/forma", "~/w/onidot"}},
}

func onidotFolder(in SaveInput) SaveInput {
	in.Scope, in.Project = "project:/Users/u/w/onidot", "/Users/u/w/onidot"
	return in
}

func sourceCount(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sources WHERE memory_id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyPlan_사용자_말_기억도_자리와_주제만_옮기고_원장으로_되돌린다(t *testing.T) {
	s := supStore(t)
	quote, _ := mustSave(t, s, onidotFolder(userSays(KindLesson, "Atlas 댓글은 밑줄로 표시한다", "")))
	ai, _ := mustSave(t, s, aiLearns(KindLesson, "Forma 부품은 생성기에서 고친다", ""))
	srcBefore := sourceCount(t, s, quote.ID)
	plan := Plan{Name: "t", Items: []PlanItem{
		{ID: quote.ID, Version: quote.Version, AppliesTo: "product:atlas", Topic: "Atlas 댓글 표시", Basis: "rule1:atlas"},
		{ID: ai.ID, Version: ai.Version, AppliesTo: "product:forma", Basis: "ai:Forma 생성기 교훈"},
	}}
	rep, err := s.ApplyPlan(plan, planMap, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 2 || len(rep.Skipped) != 0 || len(rep.Conflicts) != 0 {
		t.Fatalf("report %+v", rep)
	}
	got := mustGet(t, s, quote.ID)
	if got.Scope != "product:atlas" || got.Project != "" || got.Topic != "atlas 댓글 표시" {
		t.Fatalf("moved %+v", got)
	}
	if got.Title != quote.Title || got.Body != quote.Body || strings.Join(got.Tags, ",") != strings.Join(quote.Tags, ",") || sourceCount(t, s, quote.ID) != srcBefore {
		t.Fatalf("content or sources changed: %+v", got)
	}
	if m := mustGet(t, s, ai.ID); m.Scope != "product:forma" || m.Topic != "" {
		t.Fatalf("ai moved %+v", m)
	}
	for _, r := range rep.Applied {
		if r.LedgerID == "" {
			t.Fatalf("no ledger line %+v", r)
		}
	}
	if _, err := s.RevertChange(rep.Applied[0].LedgerID, Who{}, "틀린 제품"); err != nil {
		t.Fatal(err)
	}
	back := mustGet(t, s, quote.ID)
	if back.Scope != quote.Scope || back.Project != quote.Project || back.Topic != "" {
		t.Fatalf("reverted %+v, want scope %s project %s no topic", back, quote.Scope, quote.Project)
	}
	// 되돌린 옮기기는 다시 돌려도 하지 않고, 이미 옮긴 것은 건너뛴다(같은 계획을 두 번 돌려도 안전하다).
	again, err := s.ApplyPlan(plan, planMap, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Applied) != 0 {
		t.Fatalf("applied again %+v", again.Applied)
	}
	for _, r := range again.Skipped {
		if r.ID == ai.ID && r.Skip != "already there" {
			t.Fatalf("second run of a done item: %q", r.Skip)
		}
	}
	if m := mustGet(t, s, quote.ID); m.Scope != quote.Scope {
		t.Fatalf("reverted move redone: %s", m.Scope)
	}
}

func TestApplyPlan_틀릴_수_있는_항목은_건너뛰고_이유를_남긴다(t *testing.T) {
	s := supStore(t)
	pinned, _ := mustSave(t, s, userSays(KindRule, "시험은 실패 0개", "시험 실패 0개"))
	if _, err := s.Pin(pinned.ID, Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	pinned = mustGet(t, s, pinned.ID)
	holder, _ := mustSave(t, s, func() SaveInput {
		in := userSays(KindDecision, "Atlas 로고는 접힌 A", "atlas 로고")
		in.Scope = "product:atlas"
		return in
	}())
	other, _ := mustSave(t, s, onidotFolder(aiLearns(KindLesson, "Atlas 로고 파일 위치", "")))
	changed, _ := mustSave(t, s, onidotFolder(aiLearns(KindLesson, "바뀐 기억", "")))
	stray, _ := mustSave(t, s, onidotFolder(aiLearns(KindLesson, "지도에 없는 제품", "")))
	oldVersion := changed.Version
	if _, err := s.Update(UpdateInput{ID: changed.ID, Title: ptr("바뀐 기억 2판")}); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Items: []PlanItem{
		{ID: pinned.ID, Version: pinned.Version, AppliesTo: "product:atlas", Basis: "ai"},
		{ID: other.ID, Version: other.Version, AppliesTo: "product:atlas", Topic: "atlas 로고", Basis: "ai"},
		{ID: changed.ID, Version: oldVersion, AppliesTo: "product:atlas", Basis: "ai"},
		{ID: stray.ID, Version: stray.Version, AppliesTo: "product:desk", Basis: "ai"},
	}}
	rep, err := s.ApplyPlan(plan, planMap, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 0 {
		t.Fatalf("applied %+v", rep.Applied)
	}
	why := map[string]string{}
	for _, r := range append(rep.Skipped, rep.Conflicts...) {
		why[r.ID] = r.Skip
	}
	want := map[string]string{pinned.ID: "pinned", other.ID: "topic taken", changed.ID: "changed", stray.ID: "unknown product"}
	for id, w := range want {
		if !strings.HasPrefix(why[id], w) {
			t.Fatalf("%s: skip %q, want %q (all %v)", id, why[id], w, why)
		}
	}
	if m := mustGet(t, s, pinned.ID); m.Scope != place.ScopeGlobal || !m.Pinned {
		t.Fatalf("pinned moved %+v", m)
	}
	if m := mustGet(t, s, holder.ID); m.State != StateConfirmed || m.SupersededBy != "" {
		t.Fatalf("holder touched %+v", m)
	}
}

func TestApplyPlan_대체는_출처_우선순위를_따른다(t *testing.T) {
	s := supStore(t)
	older, _ := mustSave(t, s, userSays(KindLesson, "문서 저장처는 Wiki", ""))
	newer, _ := mustSave(t, s, userSays(KindLesson, "문서 저장처는 Atlas", ""))
	aiOld, _ := mustSave(t, s, aiLearns(KindLesson, "문서는 로컬에", ""))
	aiNew, _ := mustSave(t, s, aiLearns(KindLesson, "배포는 묻지 않는다", ""))
	userLast, _ := mustSave(t, s, userSays(KindLesson, "마지막 사용자 말", ""))
	plan := Plan{Items: []PlanItem{
		// 더 새 사용자 말이 옛 사용자 말을 대체한다.
		{ID: older.ID, Version: older.Version, Topic: "문서 저장처", SupersededBy: newer.ID, Basis: "ai:같은 주제"},
		{ID: newer.ID, Version: newer.Version, Topic: "문서 저장처", Basis: "ai:같은 주제"},
		// 사용자 말은 AI가 배운 것을 대체한다.
		{ID: aiOld.ID, Version: aiOld.Version, SupersededBy: newer.ID, Basis: "ai"},
		// 옛 사용자 말은 더 새 사용자 말을 대체하지 못한다: 어긋남으로 사람이 본다.
		{ID: userLast.ID, Version: userLast.Version, SupersededBy: newer.ID, Basis: "ai"},
		// AI가 배운 것은 사용자 말을 대체하지 못한다.
		{ID: newer.ID, SupersededBy: aiNew.ID, Basis: "ai"},
	}}
	dry, err := s.ApplyPlan(plan, planMap, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Applied) == 0 {
		t.Fatalf("dry run applied nothing: %+v", dry)
	}
	if m := mustGet(t, s, older.ID); m.Topic != "" || m.State == StateStale {
		t.Fatalf("dry run changed %+v", m)
	}
	rep, err := s.ApplyPlan(plan, planMap, false)
	if err != nil {
		t.Fatal(err)
	}
	for id, by := range map[string]string{older.ID: newer.ID, aiOld.ID: newer.ID} {
		if m := mustGet(t, s, id); m.State != StateStale || m.SupersededBy != by {
			t.Fatalf("%s not superseded by %s: %+v (report %+v)", id, by, m, rep)
		}
	}
	for _, id := range []string{userLast.ID, newer.ID} {
		if m := mustGet(t, s, id); m.State == StateStale {
			t.Fatalf("%s superseded against precedence: %+v", id, m)
		}
	}
	why := map[string]string{}
	for _, c := range rep.Conflicts {
		why[c.ID] = c.Skip
	}
	if !strings.HasPrefix(why[userLast.ID], "newer") || !strings.HasPrefix(why[newer.ID], "user quote") || len(rep.Conflicts) != 2 {
		t.Fatalf("conflicts %+v", rep.Conflicts)
	}
	// 막힌 대체의 주제는 주지 않는다: 같은 자리·주제의 현재값은 하나다.
	blocked, _ := mustSave(t, s, userSays(KindLesson, "늦게 저장된 옛 말", ""))
	if _, err := s.db.Exec(`UPDATE sources SET at=at+100000000 WHERE memory_id=?`, blocked.ID); err != nil {
		t.Fatal(err)
	}
	rep3, err := s.ApplyPlan(Plan{Items: []PlanItem{{ID: blocked.ID, Version: blocked.Version, Topic: "문서 저장처", SupersededBy: newer.ID, Basis: "ai"}}}, planMap, false)
	if err != nil {
		t.Fatal(err)
	}
	if m := mustGet(t, s, blocked.ID); m.Topic != "" || len(rep3.Conflicts) != 2 {
		t.Fatalf("blocked supersession still took the topic: %+v %+v", m, rep3)
	}
}

func TestRevert_폴더로_옮겨서_핵심에서_빠진_기억은_되돌리면_핵심도_돌아온다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, userSays(KindRule, "새 화면은 키트로", "화면 키트"))
	if _, err := s.Pin(m.ID, Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	res, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "옮김", MemoryIDs: []string{m.ID}, Payload: Payload{Scope: "project:/Users/u/w/onidot", Project: "/Users/u/w/onidot"}, Who: Who{Actor: ActorUser}})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, m.ID); got.Pinned {
		t.Fatalf("still pinned after leaving the user layer")
	}
	if _, err := s.RevertChange(res.LedgerID, Who{}, "되돌림"); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, m.ID); got.Scope != place.ScopeGlobal || !got.Pinned {
		t.Fatalf("revert lost the pin: %+v", got)
	}
}
