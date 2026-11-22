package memory

import (
	"errors"
	"slices"
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
		// 만든 때부터 90일이 지나면 고친 시각이 아무리 늦어도 재검토 후보다.
		if stateOf(t, s, m.ID).Freshness(time.UnixMilli(start).Add(time.Hour)) != FreshReviewDue {
			t.Fatal("AI의 수정 뒤 90일 시계가 되감겼습니다")
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

// L-1: 재검토함(Reviewed)은 사용자 확인이다. 사람이 아니고 사용자 말도 없으면 거절하고, 사용자 말 기억이어도 마찬가지다.
func TestReviewed_사람이_아니고_사용자_말도_없으면_거절한다(t *testing.T) {
	s, c := newTestStore(t)
	m, _ := mustSave(t, s, userSays(KindFact, "릴리스는 금요일 오후에 나간다", ""))
	start := reviewAtOf(t, s, m.ID)
	c.t = c.t.Add(95 * 24 * time.Hour)
	if _, err := s.Reviewed(m.ID, Who{Actor: ActorAI}); !errors.Is(err, ErrConfirmNeedsWords) {
		t.Fatalf("err = %v, want ErrConfirmNeedsWords", err)
	}
	if got := stateOf(t, s, m.ID); got.ReviewAt != start || got.Freshness(c.t) != FreshReviewDue {
		t.Fatalf("거절했는데 재검토일이 밀렸습니다: %d → %d", start, got.ReviewAt)
	}
	if _, err := s.Reviewed(m.ID, Who{Actor: ActorAI, UserQuote: "아직 맞아"}); err != nil {
		t.Fatalf("사용자 말이 있으면 된다: %v", err)
	}
	if got, want := reviewAtOf(t, s, m.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("재검토일 %d, want %d", got, want)
	}
}

// 낡음 제안 거절은 지금 규칙대로 되고, 유지로 치는 것(재검토일을 미는 것)은 사람이거나 사용자 말이 있을 때뿐이다.
func TestProposal_낡음_제안_거절은_AI가_사용자_말_없이_하면_재검토일을_밀지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	m, _ := mustSave(t, s, userSays(KindFact, "릴리스는 금요일 오후에 나간다", ""))
	start := reviewAtOf(t, s, m.ID)
	c.t = c.t.Add(60 * 24 * time.Hour)
	p, err := s.Propose(ProposalInput{Kind: ProposalStale, Title: "낡았나", MemoryIDs: []string{m.ID}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "reject", Note: "아니다", Who: Who{Actor: ActorAI}})
	if err != nil || res.Proposal.Status != StatusRejected {
		t.Fatalf("거절 자체는 그대로여야 합니다: %v %+v", err, res.Proposal)
	}
	if got := reviewAtOf(t, s, m.ID); got != start {
		t.Fatalf("AI의 거절이 재검토일을 밀었습니다: %d → %d", start, got)
	}
	m2, _ := mustSave(t, s, userSays(KindFact, "배포는 화요일에도 한다", ""))
	c.t = c.t.Add(40 * 24 * time.Hour)
	p2, err := s.Propose(ProposalInput{Kind: ProposalStale, Title: "또 낡았나", MemoryIDs: []string{m2.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: p2.ID, Decision: "reject", Who: Who{Actor: ActorAI, UserQuote: "아직 맞아"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := reviewAtOf(t, s, m2.ID), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("사용자 말이 있는 거절: %d, want %d", got, want)
	}
}

// 재검토일이 비어 있는 살아 있는 기억(되살린 것, 옛 버전에서 가져온 것)도 AI의 수정으로 시계가 되감기지 않는다: 쓰기 전에
// 이전 고친 시각 + 90일로 굳힌다.
func TestUpdate_재검토일이_없는_기억은_AI_수정이_시계를_되감지_못한다(t *testing.T) {
	s, c := newTestStore(t)
	m, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-a"))
	origin := c.t
	if _, err := s.db.Exec(`UPDATE memories SET review_at=0 WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	c.t = origin.Add(60 * 24 * time.Hour)
	aliases := []string{"lint"}
	if _, err := s.Update(UpdateInput{ID: m.ID, Aliases: &aliases, Who: Who{Actor: ActorAI}}); err != nil {
		t.Fatal(err)
	}
	got := stateOf(t, s, m.ID)
	if got.ReviewAt != origin.UnixMilli()+90*day {
		t.Fatalf("재검토일 %d, want 만든 때 + 90일", got.ReviewAt)
	}
	if got.Freshness(origin.Add(reviewWindow+time.Hour)) != FreshReviewDue {
		t.Fatal("AI의 수정이 90일 시계를 되감았습니다")
	}
	// 같은 세션의 재등장(확인 아님)도 마찬가지다.
	if _, err := s.db.Exec(`UPDATE memories SET review_at=0 WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	prev := stateOf(t, s, m.ID).UpdatedAt
	c.t = origin.Add(70 * 24 * time.Hour)
	mustSave(t, s, inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-a"))
	if got := reviewAtOf(t, s, m.ID); got != prev+90*day {
		t.Fatalf("재등장: 재검토일 %d, want %d", got, prev+90*day)
	}
}

// 사용자 말로 확정 생성된 기억이 후보로 내려온 뒤에도 만든 세션 줄은 확정 증거 ①(서로 다른 세션·날 두 번)로 세지 않는다.
func TestEvidence_만든_세션_줄은_확정_증거로_세지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	// 사용자 말 출처의 후보는 자동 확정되지 않으므로(사람을 기다린다), 만든 세션 줄이 생기는 다른 길인 확정 상태로 만든 AI 기억으로 본다.
	q := inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-a")
	q.State = StateConfirmed
	m, _ := mustSave(t, s, q)
	// 확정을 낡음으로 했다가 되살리면 후보다(사람이 확정을 후보로 내리면 보류가 남아 증거 시험이 되지 않는다).
	if _, err := s.SetState(m.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(m.ID, StateCandidate, "되살림"); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24 * time.Hour)
	mustSave(t, s, inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-b"))
	if got := stateNow(t, s, m.ID); got != StateCandidate {
		t.Fatalf("만든 세션 줄이 증거로 셌습니다: %s", got)
	}
	// 실제로 다른 세션·다른 날 두 번째가 오면 확정된다.
	c.t = c.t.Add(24 * time.Hour)
	mustSave(t, s, inSession(aiLearns(KindLesson, "배포 전에 린트를 돌린다", ""), "sess-c"))
	if got := stateNow(t, s, m.ID); got != StateConfirmed {
		t.Fatalf("다른 세션·다른 날 두 번인데 확정되지 않았습니다: %s", got)
	}
}

// 합치기 승인으로 만든 새 기억: 자동 적용이면 원본의 가장 이른 재검토일을 잇고, 사람이 승인하면 확인이라 새 90일이다.
func TestProposal_합치기로_만든_기억의_재검토일은_자동이면_원본에서_잇고_사람이_승인하면_새로_센다(t *testing.T) {
	s, c := newTestStore(t)
	a, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "첫째 교훈은 린트다", ""), "s1"))
	c.t = c.t.Add(20 * 24 * time.Hour)
	b, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "둘째 교훈은 시험이다", ""), "s1"))
	x, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "셋째 교훈은 포맷이다", ""), "s1"))
	y, _ := mustSave(t, s, inSession(aiLearns(KindLesson, "넷째 교훈은 벤치다", ""), "s1"))
	c.t = c.t.Add(40 * 24 * time.Hour)
	res, err := s.AutoApply(AutoInput{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{a.ID, b.ID},
		Payload: Payload{MergedTitle: "린트와 시험", MergedBody: "합친 본문"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := reviewAtOf(t, s, res.Created), reviewAtOf(t, s, a.ID); got != want || want >= c.t.UnixMilli()+90*day {
		t.Fatalf("자동 합치기: 재검토일 %d, want 원본의 가장 이른 %d", got, want)
	}
	p, _ := s.Propose(ProposalInput{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{x.ID, y.ID},
		Payload: Payload{MergedTitle: "포맷과 벤치", MergedBody: "합친 본문"}})
	res, err = s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := reviewAtOf(t, s, res.Created), c.t.UnixMilli()+90*day; got != want {
		t.Fatalf("사람의 승인: 재검토일 %d, want %d", got, want)
	}
}

// 사용자가 어긋남을 유지로 닫은 쌍의 옛 사용자 사실은, 서버가 다시 확인한 검증 사실이 같은 자리에 다른 제목으로 와도 자동으로
// 대체되지 않는다(되돌린 옛 기억과 같은 취급). 사용자 말은 그대로 대체한다.
func TestReplace_사용자가_유지한_쌍의_옛_사실은_다시_자동_대체되지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	old, _ := mustSave(t, s, userFact("빌드는 make로 한다", "빌드 명령"))
	talk, _ := mustSave(t, s, SaveInput{Kind: KindFact, Title: "빌드는 go build로 한다", Topic: "빌드 명령", Scope: replScope, Who: Who{Actor: ActorAI},
		Source: Source{Kind: SourceSession, Ref: "s"}})
	flag, err := s.FlagConflict(talk.ID, old.ID, "시험", Who{Actor: ActorAI})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveFlag(flag.ID, "dismissed", Who{Actor: ActorUser}); err != nil {
		t.Fatal(err)
	}
	checked, rep := mustSave(t, s, checkedFact("빌드는 go build ./...로 한다", "빌드 명령"))
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || slices.Contains(rep.Superseded, old.ID) {
		t.Fatalf("유지한 옛 사실이 자동 대체됐습니다: %s %+v", got.State, rep)
	}
	if !hasConflictFlag(t, s, checked.ID, old.ID) {
		t.Fatal("어긋남으로 남아야 합니다")
	}
	// 사용자 말은 그대로 대체한다.
	said, rep := mustSave(t, s, userFact("빌드는 task로 한다", "빌드 명령"))
	if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != said.ID {
		t.Fatalf("사용자 말이 대체하지 못했습니다: %+v %+v", got, rep)
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
