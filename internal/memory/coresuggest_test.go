package memory

// 처음 핵심 뽑기(S3 명세 2.7c, S3-18): 확정 기억 가운데 한도 안에서 늘 지킬 것을 고른다. 모델은 쓰지 않고 DB는 바꾸지 않는다.

import (
	"strings"
	"testing"
	"time"
)

func TestSuggestCore_교정과_어김이_많은_규칙부터_한도_안에서_고른다(t *testing.T) {
	s := supStore(t)
	save := func(in SaveInput) Memory { m, _ := mustSave(t, s, in); return m }
	body := strings.Repeat("가", 600)
	withBody := func(in SaveInput) SaveInput { in.Body = body; return in }
	corrected := save(withBody(userSays(KindRule, "답은 존댓말", "말투")))
	violated := aiLearns(KindRule, "커밋 경로 명시", "커밋")
	violated.State, violated.Body = StateConfirmed, body
	vm := save(violated)
	followed := save(withBody(userSays(KindRule, "문서는 Atlas에", "문서")))
	quiet := save(withBody(userSays(KindRule, "조용한 규칙", "조용")))
	quiet2 := save(withBody(userSays(KindRule, "조용한 규칙 둘", "조용 둘")))
	tool := userSays(KindRule, "Atlas 발행 위치", "발행")
	tool.Tools = []string{"mcp__atlas__write_page"}
	tm := save(tool)
	domain := userSays(KindRule, "세무 규칙", "세무")
	domain.Tags = []string{DomainPrefix + "세무"}
	dm := save(domain)
	long := userSays(KindRule, "긴 규칙", "긴")
	long.Body = strings.Repeat("나", 1100)
	lm := save(long)
	cand := save(aiLearns(KindRule, "후보 규칙", "후보"))
	dec := save(userSays(KindDecision, "user 결정은 핵심 후보가 아니다", "결정"))
	for i := 0; i < 2; i++ {
		if err := s.RecordCorrection(CorrectionInput{Quote: "존댓말 쓰라고", TargetKind: TargetKindMemory, Target: corrected.ID}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		s.RecordUsage(UsageInput{MemoryID: vm.ID, Verdict: UsageViolated})
	}
	for i := 0; i < 2; i++ {
		s.RecordUsage(UsageInput{MemoryID: followed.ID, Verdict: UsageFollowed})
	}
	got, err := s.SuggestCore(ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range got.Picks {
		ids = append(ids, p.ID)
	}
	// 점수: 어김 3×3=9, 교정 2×3+사용자 말 2=8, 따름 2+2=4, 조용한 둘 2. 각 약 620자라 3,000자에 넷까지.
	want := []string{vm.ID, corrected.ID, followed.ID}
	if len(ids) != 4 || strings.Join(ids[:3], ",") != strings.Join(want, ",") || (ids[3] != quiet.ID && ids[3] != quiet2.ID) {
		t.Fatalf("picks %v, want %v + one quiet", ids, want)
	}
	if got.Limit != UserCoreRunes || got.Total > UserCoreRunes || got.AppliesTo != "user" {
		t.Fatalf("layer %+v", got)
	}
	for _, bad := range []string{tm.ID, dm.ID, lm.ID, cand.ID, dec.ID} {
		for _, id := range ids {
			if id == bad {
				t.Fatalf("picked %s", bad)
			}
		}
	}
	if p := got.Picks[1]; p.Corrections != 2 || !p.Quote || p.Score != 8 || p.Runes != CoreRunes(corrected) {
		t.Fatalf("pick detail %+v", p)
	}
	// 제안은 아무것도 바꾸지 않는다.
	if c, _ := s.Core(ScopeGlobal); len(c) != 0 {
		t.Fatalf("suggest pinned %d", len(c))
	}
	// 이미 핵심인 것이 먼저 자리를 차지한다.
	if _, err := s.Pin(quiet2.ID, Who{}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SuggestCore(ScopeGlobal)
	if len(got.Pinned) != 1 || got.Pinned[0].ID != quiet2.ID || len(got.Picks) != 3 || got.Total > UserCoreRunes {
		t.Fatalf("with a pinned one: %+v", got)
	}
}

func TestSuggestCore_제품_층은_규칙과_결정을_고른다(t *testing.T) {
	s := supStore(t)
	d := userSays(KindDecision, "레일 유지", "레일")
	d.Scope = "product:desk"
	dm, _ := mustSave(t, s, d)
	got, err := s.SuggestCore("product:desk")
	if err != nil || len(got.Picks) != 1 || got.Picks[0].ID != dm.ID || got.Limit != ProductCoreRunes {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.SuggestCore("project:x"); err == nil {
		t.Fatal("repository layer has no core")
	}
}

// 검토 지적(2026-11-04): 동점은 짧은 것, 그다음 최근 것. 재검토일 지남·임시(만료일 있음)·옛 판은 고르지 않는다.
func TestSuggestCore_동점_순서와_빼는_것(t *testing.T) {
	s := supStore(t)
	save := func(in SaveInput) Memory { m, _ := mustSave(t, s, in); return m }
	longer := userSays(KindRule, "긴 동점 규칙", "동점 긴")
	longer.Body = strings.Repeat("가", 200)
	lm := save(longer)
	olderShort := save(userSays(KindRule, "짧은 동점 옛", "동점 옛"))
	newerShort := save(userSays(KindRule, "짧은 동점 새", "동점 새"))
	due := userSays(KindRule, "재검토 지난 규칙", "재검토")
	due.ReviewAfterDays = 1
	dm := save(due)
	temp := userSays(KindRule, "이번 주만 규칙", "이번 주")
	temp.Retention, temp.TTLDays = RetentionVolatile, 7
	tm := save(temp)
	old := save(userSays(KindRule, "보고는 영어로", "보고 언어"))
	save(userSays(KindRule, "보고는 한국어로", "보고 언어")) // 같은 주제의 새 판이 옛 판을 대체한다
	// 재검토일이 지나도록 시계를 옮긴다(supStore는 부를 때마다 1분씩 간다).
	base := s.Now()
	s.Now = func() time.Time { return base.Add(48 * time.Hour) }
	got, err := s.SuggestCore(ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, p := range got.Picks {
		pos[p.ID] = i + 1
	}
	if pos[newerShort.ID] == 0 || pos[olderShort.ID] == 0 || pos[lm.ID] == 0 || !(pos[newerShort.ID] < pos[olderShort.ID] && pos[olderShort.ID] < pos[lm.ID]) {
		t.Fatalf("tie order %v", pos)
	}
	for _, bad := range []string{dm.ID, tm.ID, old.ID} {
		if pos[bad] != 0 {
			t.Fatalf("picked %s: %v", bad, pos)
		}
	}
}
