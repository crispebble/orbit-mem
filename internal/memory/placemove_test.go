package memory

// S3-R5(명세 2.10): 제품 지도가 생기거나 바뀌면 맞는 product:? 기억을 제품으로 옮긴다. 자동 변경으로 원장·감사 목록에
// 남고 되돌릴 수 있으며, 되돌린 옮기기는 다시 하지 않는다. 사용자 말 기억도 범위만 옮긴다.

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/place"
)

func TestMoveUnknown_지도에_맞는_product_물음표_기억을_제품으로_옮긴다(t *testing.T) {
	s := supStore(t)
	const home = "/Users/u"
	unknown := func(in SaveInput, folder string) Memory {
		in.Scope, in.Project = place.ScopeUnknown, folder
		m, _ := mustSave(t, s, in)
		return m
	}
	draft := unknown(userSays(KindDecision, "상단바는 레일", "상단바"), "/Users/u/w/onidot/prototypes/desk-v4")
	parentOne := unknown(aiLearns(KindDecision, "Atlas 문서 위치는 onidot Space", "문서 위치"), "/Users/u/w/onidot")
	parentBoth := unknown(aiLearns(KindLesson, "atlas와 desk 둘 다 다크 모드", ""), "/Users/u/w/onidot")
	stray := unknown(aiLearns(KindFact, "다른 폴더 사실", ""), "/Users/u/elsewhere")
	m := place.Map{
		"desk":  {Match: []string{"~/w/onidot/prototypes/desk-*", "~/w/onidot"}},
		"atlas": {Match: []string{"~/w/onidot"}, Names: []string{"Atlas"}},
	}
	moved, err := s.MoveUnknownIn(m, home)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, mv := range moved {
		got[mv.ID] = mv.To
		if mv.LedgerID == "" || mv.From != place.ScopeUnknown {
			t.Fatalf("move %+v", mv)
		}
	}
	want := map[string]string{draft.ID: "product:desk", parentOne.ID: "product:atlas"}
	if len(got) != len(want) || got[draft.ID] != want[draft.ID] || got[parentOne.ID] != want[parentOne.ID] {
		t.Fatalf("moved %v, want %v", got, want)
	}
	for id, scope := range map[string]string{draft.ID: "product:desk", parentOne.ID: "product:atlas", parentBoth.ID: place.ScopeUnknown, stray.ID: place.ScopeUnknown} {
		if m := mustGet(t, s, id); m.Scope != scope || (scope != place.ScopeUnknown && m.Project != "") {
			t.Fatalf("%s: %s %q", id, m.Scope, m.Project)
		}
	}
	// 내용은 그대로다.
	if m := mustGet(t, s, draft.ID); m.Title != draft.Title || m.Body != draft.Body {
		t.Fatalf("content changed %+v", m)
	}
	// 감사 목록에 보인다.
	a, err := s.Audit(AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, c := range a.Changes {
		if c.Kind == ProposalEdit {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("audit changes %+v", a.Changes)
	}
	// 되돌리면 product:?로 돌아오고, 다시 돌려도 옮기지 않는다.
	var ledger string
	for _, mv := range moved {
		if mv.ID == draft.ID {
			ledger = mv.LedgerID
		}
	}
	if _, err := s.RevertChange(ledger, Who{}, "틀린 제품"); err != nil {
		t.Fatal(err)
	}
	if m := mustGet(t, s, draft.ID); m.Scope != place.ScopeUnknown || m.Project != draft.Project {
		t.Fatalf("reverted %+v", m)
	}
	again, err := s.MoveUnknownIn(m, home)
	if err != nil || len(again) != 0 {
		t.Fatalf("moved again %+v %v", again, err)
	}
}

// 범위만 옮기는 예외는 product:? → product:<이름>에만 있다. 사용자 말 기억의 다른 자동 고치기는 여전히 막힌다.
func TestAutoApply_지도_옮기기_예외는_범위만이다(t *testing.T) {
	s := supStore(t)
	in := userSays(KindRule, "답은 존댓말", "말투")
	in.Scope, in.Project = place.ScopeUnknown, "/x"
	m, _ := mustSave(t, s, in)
	for _, pl := range []Payload{{Scope: "product:a", Title: "다른 제목"}, {Scope: ScopeGlobal}} {
		if _, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "x", MemoryIDs: []string{m.ID}, Payload: pl, MapMove: true}); err == nil {
			t.Fatalf("map move accepted %+v", pl)
		}
	}
	if _, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "x", MemoryIDs: []string{m.ID}, Payload: Payload{Scope: "product:a"}}); err == nil {
		t.Fatal("protected memory edited without the map move")
	}
	if _, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "x", MemoryIDs: []string{m.ID}, Payload: Payload{Scope: "product:a"}, MapMove: true}); err != nil {
		t.Fatal(err)
	}
}

// 검토 지적(2026-11-04): 낱말 일치(추측)로는 사용자 말 기억을 옮기지 않고, 같은 제품·주제에 현재값이 있으면 옮기지 않는다.
func TestMoveUnknown_추측과_현재값_충돌은_옮기지_않는다(t *testing.T) {
	s := supStore(t)
	const home = "/Users/u"
	m := place.Map{
		"desk":  {Match: []string{"~/w/onidot/prototypes/desk-*", "~/w/onidot"}},
		"atlas": {Match: []string{"~/w/onidot"}},
	}
	unknown := func(in SaveInput, folder string) Memory {
		in.Scope, in.Project = place.ScopeUnknown, folder
		mm, _ := mustSave(t, s, in)
		return mm
	}
	quoteGuess := unknown(userSays(KindDecision, "배포는 main 머지 (atlas 운영 기준)", "배포"), "/Users/u/w/onidot")
	aiGuess := unknown(aiLearns(KindFact, "atlas 빌드는 make", ""), "/Users/u/w/onidot")
	cur := userSays(KindDecision, "상단바는 탭줄", "상단바")
	cur.Scope = "product:desk"
	mustSave(t, s, cur)
	clash := unknown(userSays(KindDecision, "상단바는 레일", "상단바"), "/Users/u/w/onidot/prototypes/desk-v4")
	moved, err := s.MoveUnknownIn(m, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].ID != aiGuess.ID || moved[0].To != "product:atlas" {
		t.Fatalf("moved %+v", moved)
	}
	for _, id := range []string{quoteGuess.ID, clash.ID} {
		if got := mustGet(t, s, id); got.Scope != place.ScopeUnknown {
			t.Fatalf("%s moved to %s", got.Title, got.Scope)
		}
	}
}

func TestAutoApply_지도_옮기기는_자동화가_범위만_바꾼다(t *testing.T) {
	s := supStore(t)
	in := aiLearns(KindFact, "빌드는 make", "")
	in.Scope, in.Project = place.ScopeUnknown, "/x"
	m, _ := mustSave(t, s, in)
	if _, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "x", MemoryIDs: []string{m.ID}, Payload: Payload{Scope: "product:a", Project: "/y"}, MapMove: true}); err == nil {
		t.Fatal("map move changed the project")
	}
	if _, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "x", MemoryIDs: []string{m.ID}, Payload: Payload{Scope: "product:a"}, MapMove: true, Who: Who{Actor: ActorAI}}); err == nil {
		t.Fatal("map move by the AI")
	}
}
