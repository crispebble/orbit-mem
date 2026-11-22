package memory

import (
	"testing"
	"time"
)

// L-1(명세 · orbit-mem 기억 정확성과 회상 품질 개선): 마지막 확인 뒤 90일이 지나면 재검토 후보다. 확인은 생성, 수정,
// 다른 세션에서 다시 나옴, 사용자 확인이다. 같은 세션에서 다시 나온 것·조회·주입, 그리고 사용자 말 없는 AI의 수정은 확인이
// 아니다. 마지막 확인은 재검토일(review_at)이 기억한다: 확인일 때만 재검토일이 밀린다.

const reviewWindow = 90 * 24 * time.Hour

func reviewAtOf(t *testing.T, s *Store, id string) int64 {
	t.Helper()
	return stateOf(t, s, id).ReviewAt
}

func TestFreshness_마지막_확인_90일_뒤면_보통_기억도_재검토_후보다(t *testing.T) {
	s, c := newTestStore(t)
	cand, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-a"))
	conf, _ := mustSave(t, s, userSays(KindRule, "답은 한글 존댓말로", ""))
	if cand.Retention != RetentionNormal || conf.Retention != RetentionNormal {
		t.Fatal("보통 기억이어야 합니다")
	}
	for _, m := range []Memory{cand, conf} {
		got := stateOf(t, s, m.ID)
		if got.Freshness(c.t.Add(reviewWindow-time.Hour)) != FreshOK {
			t.Fatalf("%s: 90일 전인데 재검토 후보입니다", m.ID)
		}
		if got.Freshness(c.t.Add(reviewWindow+time.Hour)) != FreshReviewDue {
			t.Fatalf("%s: 90일이 지났는데 재검토 후보가 아닙니다", m.ID)
		}
	}
	// 휘발 기억은 재검토 대상이 아니다(만료가 있다).
	vol := aiLearns(KindFact, "오늘만 열린 배포 창", "")
	vol.Retention = RetentionVolatile
	v, _ := mustSave(t, s, vol)
	if got := v.Freshness(c.t.Add(reviewWindow + time.Hour)); got != FreshExpired {
		t.Fatalf("휘발 기억 = %q", got)
	}
	// 재검토일이 비어 있는 기억(후보·확정이 아닌 상태에서 돌아온 것)은 마지막으로 고친 시각 + 90일이 기본이다.
	if _, err := s.db.Exec(`UPDATE memories SET review_at=0 WHERE id=?`, conf.ID); err != nil {
		t.Fatal(err)
	}
	got := stateOf(t, s, conf.ID)
	if got.ReviewAt != 0 || got.Freshness(c.t.Add(reviewWindow-time.Hour)) != FreshOK || got.Freshness(c.t.Add(reviewWindow+time.Hour)) != FreshReviewDue {
		t.Fatalf("재검토일이 없을 때의 기본값: review=%d", got.ReviewAt)
	}
	// 시각을 모르는 기억(UpdatedAt 0)은 낡았다고 하지 않는다.
	if (Memory{}).Freshness(time.Now()) != FreshOK {
		t.Fatal("빈 기억이 재검토 후보입니다")
	}
}

func TestSight_같은_세션에서_다시_나온_것은_확인이_아니다(t *testing.T) {
	s, c := newTestStore(t)
	in := aiLearns(KindLesson, "배포 전에 린트를 돌린다", "")
	first, _ := mustSave(t, s, inSession(in, "sess-a"))
	want := reviewAtOf(t, s, first.ID)
	for i, session := range []string{"sess-a", "sess-a/agent-1", ""} { // 같은 세션, 그 세션의 하위 에이전트, 세션을 모르는 저장
		c.t = c.t.Add(40 * 24 * time.Hour)
		mustSave(t, s, inSession(in, session))
		if got := reviewAtOf(t, s, first.ID); got != want {
			t.Fatalf("%d번째(%q): 재검토일이 밀렸습니다: %d → %d", i, session, want, got)
		}
	}
	// 사용자 말로 만들어진 확정 기억을 만든 세션에서 AI가 다시 저장해도 같은 세션이다.
	q := userSays(KindRule, "커밋 메시지는 영어로 쓴다", "")
	q.Session = "sess-q"
	conf, _ := mustSave(t, s, q)
	wantQ := reviewAtOf(t, s, conf.ID)
	c.t = c.t.Add(10 * 24 * time.Hour)
	again := aiLearns(KindRule, "커밋 메시지는 영어로 쓴다", "")
	mustSave(t, s, inSession(again, "sess-q"))
	if got := reviewAtOf(t, s, conf.ID); got != wantQ {
		t.Fatalf("만든 세션의 재등장이 확인으로 셌습니다: %d → %d", wantQ, got)
	}
	// 조회는 기억을 바꾸지 않는다.
	if _, err := s.Get(conf.ID); err != nil {
		t.Fatal(err)
	}
	if got := reviewAtOf(t, s, conf.ID); got != wantQ {
		t.Fatalf("조회가 재검토일을 밀었습니다: %d → %d", wantQ, got)
	}
}

func TestSight_다른_세션에서_다시_나오면_재검토일이_밀린다(t *testing.T) {
	s, c := newTestStore(t)
	in := aiLearns(KindLesson, "배포 전에 린트를 돌린다", "")
	first, _ := mustSave(t, s, inSession(in, "sess-a"))
	c.t = c.t.Add(50 * 24 * time.Hour)
	mustSave(t, s, inSession(in, "sess-b"))
	if got, want := reviewAtOf(t, s, first.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("재검토일 %d, want %d", got, want)
	}
	// 같은 새 세션이 또 나오면 더 밀리지 않는다.
	again := reviewAtOf(t, s, first.ID)
	c.t = c.t.Add(20 * 24 * time.Hour)
	mustSave(t, s, inSession(in, "sess-b"))
	if got := reviewAtOf(t, s, first.ID); got != again {
		t.Fatalf("같은 세션의 재등장이 다시 밀었습니다: %d, want %d", got, again)
	}
	// 옛 DB가 하나로 묶은 세션(legacy)은 다른 세션이 아니다: 옛 기록만 있는 기억에 세션을 모르는 저장이 와도 밀리지 않는다.
	old, _ := mustSave(t, s, aiLearns(KindLesson, "머지 전에 시험을 돌린다", ""))
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO sighting_sessions(memory_id, session, day, at) VALUES(?,?,?,0)`, old.ID, legacySession, "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	before := reviewAtOf(t, s, old.ID)
	c.t = c.t.Add(5 * 24 * time.Hour)
	mustSave(t, s, aiLearns(KindLesson, "머지 전에 시험을 돌린다", ""))
	if got := reviewAtOf(t, s, old.ID); got != before {
		t.Fatalf("세션을 모르는 저장이 확인으로 셌습니다: %d → %d", before, got)
	}
}

func TestUpdate_사용자_말로_고치면_재검토일이_밀린다(t *testing.T) {
	s, c := newTestStore(t)
	m, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-a"))
	start := reviewAtOf(t, s, m.ID)

	// 사용자 말 없는 AI의 수정(별칭·태그·도구 칸·본문)은 확인이 아니다: 90일 시계를 되감지 못한다.
	c.t = c.t.Add(30 * 24 * time.Hour)
	aliases, tags, tools, body := []string{"lint"}, []string{"배포"}, []string{"Bash"}, "배포 전 린트와 포맷을 돌린다"
	for _, u := range []UpdateInput{
		{ID: m.ID, Aliases: &aliases, Who: Who{Actor: ActorAI}},
		{ID: m.ID, Tags: &tags, Who: Who{Actor: ActorAI}},
		{ID: m.ID, Tools: &tools, Who: Who{Actor: ActorAI}},
		{ID: m.ID, Body: &body, Who: Who{Actor: ActorAI}},
	} {
		if _, err := s.Update(u); err != nil {
			t.Fatal(err)
		}
		if got := reviewAtOf(t, s, m.ID); got != start {
			t.Fatalf("AI의 수정이 재검토일을 밀었습니다: %d → %d", start, got)
		}
	}
	if got := stateOf(t, s, m.ID); got.Version < 5 || got.UpdatedAt != c.t.UnixMilli() {
		t.Fatalf("버전과 고친 시각은 올라야 합니다: v%d %d", got.Version, got.UpdatedAt)
	}

	// 사용자 말(함께 준 말, 사람의 직접 수정)이 붙은 수정은 확인이다.
	c.t = c.t.Add(30 * 24 * time.Hour)
	title := "배포 전에 린트와 포맷을 돌린다"
	if _, err := s.Update(UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI, UserQuote: "린트와 포맷 둘 다 돌려"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := reviewAtOf(t, s, m.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("사용자 말 수정: 재검토일 %d, want %d", got, want)
	}
	c.t = c.t.Add(10 * 24 * time.Hour)
	body2 := "린트와 포맷, 그다음 시험"
	if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body2, Who: Who{Actor: ActorUser}}); err != nil {
		t.Fatal(err)
	}
	if got, want := reviewAtOf(t, s, m.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("사람의 수정: 재검토일 %d, want %d", got, want)
	}
	// 사용자 말 출처로 다시 저장해도 확인이다(다른 세션이 아니어도).
	c.t = c.t.Add(20 * 24 * time.Hour)
	again := userSays(KindLesson, "배포 전에 린트와 포맷을 돌린다", "")
	again.Session = "sess-a"
	mustSave(t, s, again)
	if got, want := reviewAtOf(t, s, m.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("사용자 말 재저장: 재검토일 %d, want %d", got, want)
	}
}

func TestProposal_감사_목록에서_유지로_정하면_재검토일이_밀린다(t *testing.T) {
	s, c := newTestStore(t)
	old, _ := mustSave(t, s, userSays(KindFact, "릴리스는 금요일 오후에 나간다", "릴리스 요일"))
	cand, _ := mustSave(t, s, inSession(aiLearns(KindFact, "릴리스는 화요일에 나간다", "릴리스 화요일"), "sess-a"))
	start := reviewAtOf(t, s, old.ID)
	c.t = c.t.Add(60 * 24 * time.Hour)
	flag, err := s.FlagConflict(cand.ID, old.ID, "시험", Who{Actor: ActorAI})
	if err != nil {
		t.Fatal(err)
	}
	// 사람이 아닌 쪽이 사용자 말 없이 닫으면 확인이 아니다.
	if err := s.ResolveFlag(flag.ID, "dismissed", Who{Actor: ActorAI}); err != nil {
		t.Fatal(err)
	}
	if got := reviewAtOf(t, s, old.ID); got != start {
		t.Fatalf("AI가 닫은 표시가 재검토일을 밀었습니다: %d → %d", start, got)
	}
	flag2, err := s.FlagConflict(cand.ID, old.ID, "또", Who{Actor: ActorAI})
	if err != nil {
		t.Fatal(err)
	}
	// 사용자가 유지로 닫으면 유지하기로 한 쪽(어긋나는 새 것이 아닌 옛 기억)의 재검토일이 밀린다.
	candBefore := reviewAtOf(t, s, cand.ID)
	if err := s.ResolveFlag(flag2.ID, "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	if got, want := reviewAtOf(t, s, old.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("유지로 정함: 재검토일 %d, want %d", got, want)
	}
	if got := reviewAtOf(t, s, cand.ID); got != candBefore {
		t.Fatal("어긋나는 새 후보까지 확인으로 셌습니다")
	}
}

func TestProposal_낡음_제안을_거절하면_그_기억을_유지한_것이라_재검토일이_밀린다(t *testing.T) {
	s, c := newTestStore(t)
	m, _ := mustSave(t, s, userSays(KindFact, "릴리스는 금요일 오후에 나간다", ""))
	c.t = c.t.Add(60 * 24 * time.Hour)
	p, err := s.Propose(ProposalInput{Kind: ProposalStale, Title: "낡았나", MemoryIDs: []string{m.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "reject", Note: "아직 맞다"}); err != nil {
		t.Fatal(err)
	}
	if got, want := reviewAtOf(t, s, m.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("재검토일 %d, want %d", got, want)
	}
	if got := stateNow(t, s, m.ID); got != StateConfirmed {
		t.Fatalf("상태 %s", got)
	}
}

// 상태를 바꾸는 기계적 쓰기(낡음 표시, 되살림)는 확인이 아니다.
func TestReviewAt_기계적_쓰기는_재검토일을_밀지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	m, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-a"))
	start := reviewAtOf(t, s, m.ID)
	c.t = c.t.Add(40 * 24 * time.Hour)
	if _, err := s.SetState(m.ID, StateStale, "시험"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(10 * 24 * time.Hour)
	if _, err := s.SetState(m.ID, StateCandidate, "되살림"); err != nil {
		t.Fatal(err)
	}
	if got := reviewAtOf(t, s, m.ID); got != start {
		t.Fatalf("상태 전이가 재검토일을 밀었습니다: %d → %d", start, got)
	}
}

// L-3: 세션 시작의 개수 줄이 가리키는 감사 목록에 재검토 후보가 보인다. 확인하면 빠진다.
func TestAudit_재검토_후보가_감사_목록에_보인다(t *testing.T) {
	s, c := newTestStore(t)
	a, _ := mustSave(t, s, userSays(KindRule, "답은 한글 존댓말로", ""))
	if got, err := s.Audit(AuditFilter{}); err != nil || got.ReviewDueCount != 0 || len(got.ReviewDue) != 0 {
		t.Fatalf("0건이어야 합니다: %+v %v", got.ReviewDue, err)
	}
	c.t = c.t.Add(reviewWindow + time.Hour)
	got, err := s.Audit(AuditFilter{})
	if err != nil || got.ReviewDueCount != 1 || len(got.ReviewDue) != 1 || got.ReviewDue[0].ID != a.ID || got.ReviewDue[0].ReviewAt != a.ReviewAt {
		t.Fatalf("재검토 후보 목록 %+v %v", got.ReviewDue, err)
	}
	if _, err := s.Reviewed(a.ID, Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Audit(AuditFilter{}); got.ReviewDueCount != 0 {
		t.Fatalf("확인한 뒤에도 남았습니다: %+v", got.ReviewDue)
	}
}
