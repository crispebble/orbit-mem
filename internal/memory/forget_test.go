package memory

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func forgetQuote(t *testing.T, s *Store, title string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindRule, Title: title, Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: title}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func countOf(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// eventsOf counts the correction and memory.used events that point at id, as cleanVerifiedUses finds them.
func eventsOf(t *testing.T, s *Store, id string) int {
	t.Helper()
	return countOf(t, s, `SELECT COUNT(*) FROM events WHERE kind IN (?,?) AND (data LIKE ? OR data LIKE ?)`,
		EventMemoryUsed, EventCorrection, `%"memoryId":"`+id+`"%`, `%"target":"`+id+`"%`)
}

func mustExist(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := s.Get(id); err != nil {
			t.Fatalf("memory %s is gone: %v", id, err)
		}
	}
}

func TestForget_사용자_말과_함께_지우면_기억과_딸린_행이_모두_없다(t *testing.T) {
	s, _ := newTestStore(t)
	rule := forgetQuote(t, s, "커밋 메시지는 영어로")
	other := forgetQuote(t, s, "답은 한글 존댓말로")
	m, err := s.Save(SaveInput{Kind: KindFact, Title: "orbit-cc는 살아 있는 일이다", Body: "폐기한 일", Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "claude:abc"}, LegacyID: "legacy-forget-1"})
	if err != nil {
		t.Fatal(err)
	}
	cand, err := s.Save(SaveInput{Kind: KindFact, Title: "다른 후보", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	body := "고친 본문"
	if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body}); err != nil { // 판 이력 2줄
		t.Fatal(err)
	}
	if err := s.AddSource(m.ID, Source{Kind: SourceSession, Ref: "claude:def"}); err != nil {
		t.Fatal(err)
	}
	// 뗀 연결과 살아 있는 연결
	if _, _, err := s.Link(LinkInput{From: m.ID, To: other.ID, Origin: LinkOriginAI}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unlink(m.ID, other.ID, LinkOriginAI, "뗌"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link(LinkInput{From: other.ID, To: m.ID, Origin: LinkOriginUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordJudgment(Judgment{MemoryID: m.ID, Title: m.Title, Verdict: VerdictKeep}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlagConflict(m.ID, rule.ID, "규칙과 어긋남", Who{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlagConflict(cand.ID, m.ID, "지울 기억과 어긋남", Who{}); err != nil { // other_id 쪽
		t.Fatal(err)
	}
	if err := s.MarkSession("claude", "S1", MarkShown, m.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := insertJournalActivity(s.DB(), m); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCorrection(CorrectionInput{Quote: "그건 끝난 일이야", TargetKind: TargetKindMemory, Target: m.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsage(UsageInput{MemoryID: m.ID, Verdict: UsageViolated}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUsage(UsageInput{MemoryID: other.ID, Verdict: UsageFollowed}); err != nil {
		t.Fatal(err)
	}
	var rowid int64
	if err := s.DB().QueryRow(`SELECT rowid FROM memory_index WHERE memory_id=?`, m.ID).Scan(&rowid); err != nil {
		t.Fatal(err)
	}
	// 준비가 실제로 행을 만들었는지 먼저 본다: 0행에서 0행은 증거가 아니다.
	for _, c := range forgetTables {
		if c.how == forgetCustom || c.table == "memory_links" { // 연결은 id 순으로 a·b에 놓여 한쪽에만 있을 수 있다
			continue
		}
		if n := countOf(t, s, `SELECT COUNT(*) FROM `+c.table+` WHERE `+c.column+`=?`, m.ID); n == 0 {
			t.Fatalf("setup left no row in %s.%s", c.table, c.column)
		}
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM memory_links WHERE a=? OR b=?`, m.ID, m.ID); n != 2 {
		t.Fatalf("setup links %d, want a removed and a live one", n)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM memory_fts WHERE rowid=?`, rowid); n != 1 {
		t.Fatalf("setup fts rows %d", n)
	}
	if eventsOf(t, s, m.ID) != 2 || countOf(t, s, `SELECT COUNT(*) FROM activity WHERE host=? AND session=?`, JournalHost, JournalSession(m.ID)) != 1 {
		t.Fatal("setup lacks the events or the journal activity row")
	}

	res, err := s.Forget(ForgetInput{IDs: []string{m.ID}, Reason: "폐기한 일", Who: Who{Actor: ActorAI, UserQuote: "지워"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Forgotten, []string{m.ID}) || len(res.Touched) != 0 {
		t.Fatalf("result %+v", res)
	}
	if _, err := s.Get(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after forget: %v", err)
	}
	for _, c := range forgetTables {
		if n := countOf(t, s, `SELECT COUNT(*) FROM `+c.table+` WHERE `+c.column+`=?`, m.ID); n != 0 {
			t.Errorf("%s.%s still has %d rows", c.table, c.column, n)
		}
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM memory_fts WHERE rowid=?`, rowid); n != 0 {
		t.Errorf("fts rows %d", n)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM activity WHERE host=? AND session=?`, JournalHost, JournalSession(m.ID)); n != 0 {
		t.Errorf("journal activity rows %d", n)
	}
	if n := eventsOf(t, s, m.ID); n != 0 {
		t.Errorf("correction and memory.used events %d", n)
	}
	// 다른 기억의 행은 남는다.
	if eventsOf(t, s, other.ID) != 1 || countOf(t, s, `SELECT COUNT(*) FROM session_marks WHERE memory_id=?`, other.ID) != 1 {
		t.Error("the other memory's rows went too")
	}
	got, err := s.Get(other.ID)
	if err != nil || len(got.Links) != 0 {
		t.Fatalf("other memory %+v %v", got.Links, err)
	}
	found, err := s.Find(FindInput{Query: "orbit-cc 살아 있는 일", AllProjects: true, States: States})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if f.ID == m.ID {
			t.Fatal("find returns the forgotten memory")
		}
	}
	exp, err := s.Export(States...)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range exp {
		if e.ID == m.ID {
			t.Fatal("export has the forgotten memory")
		}
	}
	var legacy, quote, actor, reason string
	if err := s.DB().QueryRow(`SELECT COALESCE(legacy_id,''), quote, actor, reason FROM forgotten WHERE id=?`, m.ID).Scan(&legacy, &quote, &actor, &reason); err != nil {
		t.Fatal(err)
	}
	if legacy != m.LegacyID || legacy == "" || quote != "지워" || actor != ActorAI || reason != "폐기한 일" {
		t.Fatalf("forgotten row %q %q %q %q", legacy, quote, actor, reason)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM forgotten`); n != 1 {
		t.Fatalf("forgotten rows %d", n)
	}
	for _, c := range []struct{ id, legacy string }{{m.ID, ""}, {"m-다른", m.LegacyID}} {
		if ok, err := s.IsForgotten(c.id, c.legacy); err != nil || !ok {
			t.Errorf("IsForgotten(%q, %q) = %v %v", c.id, c.legacy, ok, err)
		}
	}
	if ok, err := s.IsForgotten(other.ID, ""); err != nil || ok {
		t.Errorf("IsForgotten(other) = %v %v", ok, err)
	}
}

func TestForget_AI가_말_없이_부르면_거절하고_아무것도_안_지운다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "지우면 안 되는 규칙")
	for _, q := range []string{"", "  "} {
		_, err := s.Forget(ForgetInput{IDs: []string{m.ID}, Who: Who{Actor: ActorAI, UserQuote: q}})
		if !errors.Is(err, ErrForgetNeedsWords) || !errors.Is(err, ErrProtected) {
			t.Fatalf("quote %q: %v", q, err)
		}
		if strings.Contains(err.Error(), ErrProtected.Error()) {
			t.Errorf("quote %q: the message speaks of a quote memory: %v", q, err)
		}
	}
	mustExist(t, s, m.ID)
	if n := countOf(t, s, `SELECT COUNT(*) FROM forgotten`); n != 0 {
		t.Fatalf("forgotten rows %d", n)
	}
}

func TestForget_자동화는_말이_있어도_거절한다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "지우면 안 되는 규칙")
	if _, err := s.Forget(ForgetInput{IDs: []string{m.ID}, Who: Who{Actor: ActorAutomation, UserQuote: "지워"}}); !errors.Is(err, ErrForgetAutomation) {
		t.Fatalf("got %v", err)
	}
	mustExist(t, s, m.ID)
}

func TestForget_알_수_없는_actor는_말을_요구한다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "지우면 안 되는 규칙")
	if _, err := s.Forget(ForgetInput{IDs: []string{m.ID}, Who: Who{Actor: "x"}}); !errors.Is(err, ErrForgetNeedsWords) {
		t.Fatalf("got %v", err)
	}
	mustExist(t, s, m.ID)
}

func TestForget_사람은_말_없이_지운다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "지울 규칙")
	if _, err := s.Forget(ForgetInput{IDs: []string{m.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after forget: %v", err)
	}
	var actor string
	if err := s.DB().QueryRow(`SELECT actor FROM forgotten WHERE id=?`, m.ID).Scan(&actor); err != nil || actor != ActorUser {
		t.Fatalf("actor %q %v", actor, err)
	}
}

func TestForget_없는_id가_섞이면_아무것도_안_지우고_그_id를_알린다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "남을 규칙")
	_, err := s.Forget(ForgetInput{IDs: []string{m.ID, "m-없음"}})
	var miss *ForgetMissingError
	if !errors.As(err, &miss) || !slices.Equal(miss.IDs, []string{"m-없음"}) {
		t.Fatalf("got %v", err)
	}
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "m-없음") {
		t.Fatalf("not found %v", err)
	}
	mustExist(t, s, m.ID)
	if n := countOf(t, s, `SELECT COUNT(*) FROM forgotten`); n != 0 {
		t.Fatalf("forgotten rows %d", n)
	}
}

func TestForget_id가_0개나_21개면_거절한다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "남을 규칙")
	many := make([]string, 21)
	for i := range many {
		many[i] = m.ID // 중복을 빼기 전에 센다
	}
	for _, ids := range [][]string{nil, {}, {"", " "}, many} {
		if _, err := s.Forget(ForgetInput{IDs: ids}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%d ids: %v", len(ids), err)
		}
	}
	mustExist(t, s, m.ID)
}

func TestForget_같은_id를_두_번_주면_한_번만_지운다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "지울 규칙")
	res, err := s.Forget(ForgetInput{IDs: []string{m.ID, m.ID}})
	if err != nil || len(res.Forgotten) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestForget_새_판을_지우면_옛_판의_대체_표시가_비고_낡음은_그대로다(t *testing.T) {
	s, _ := newTestStore(t)
	a := forgetQuote(t, s, "배포는 금요일에 한다")
	b := forgetQuote(t, s, "배포는 월요일에 한다")
	if _, err := s.Supersede(a.ID, b.ID, "요일을 바꿈"); err != nil {
		t.Fatal(err)
	}
	versions := countOf(t, s, `SELECT COUNT(*) FROM memory_versions WHERE memory_id=?`, a.ID)
	res, err := s.Forget(ForgetInput{IDs: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Touched, []string{a.ID}) {
		t.Fatalf("touched %v", res.Touched)
	}
	got, err := s.Get(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SupersededBy != "" || got.State != StateStale || got.StaleReason != forgetSuccessorGone {
		t.Fatalf("old version %+v", got)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM memory_versions WHERE memory_id=?`, a.ID); n != versions {
		t.Fatalf("versions %d, want %d", n, versions)
	}
	// 같은 트랜잭션에서 색인을 맞췄으면 늦은 표시가 남지 않는다.
	if n := countOf(t, s, `SELECT COUNT(*) FROM memory_index_late WHERE memory_id=?`, a.ID); n != 0 {
		t.Fatalf("late mark %d", n)
	}
	found, err := s.Find(FindInput{Query: "배포 금요일", AllProjects: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if f.ID == a.ID {
			t.Fatal("the old version is recalled again")
		}
	}
}

func TestForget_대체_사슬_가운데를_지우면_앞쪽만_고친다(t *testing.T) {
	s, _ := newTestStore(t)
	a := forgetQuote(t, s, "배포는 금요일에 한다")
	b := forgetQuote(t, s, "배포는 월요일에 한다")
	c := forgetQuote(t, s, "배포는 화요일에 한다")
	if _, err := s.Supersede(a.ID, b.ID, "요일을 바꿈"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Supersede(b.ID, c.ID, "또 바꿈"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Forget(ForgetInput{IDs: []string{b.ID}})
	if err != nil || !slices.Equal(res.Touched, []string{a.ID}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got, err := s.Get(a.ID); err != nil || got.SupersededBy != "" || got.State != StateStale {
		t.Fatalf("a %+v %v", got, err)
	}
	after, err := s.Get(c.ID)
	if err != nil || after.Version != before.Version || after.UpdatedAt != before.UpdatedAt || after.State != before.State {
		t.Fatalf("c changed: %+v → %+v %v", before, after, err)
	}
}

func TestForget_대체한_쪽과_된_쪽을_함께_지우면_Touched가_비어_있다(t *testing.T) {
	s, _ := newTestStore(t)
	a := forgetQuote(t, s, "배포는 금요일에 한다")
	b := forgetQuote(t, s, "배포는 월요일에 한다")
	if _, err := s.Supersede(a.ID, b.ID, "요일을 바꿈"); err != nil {
		t.Fatal(err)
	}
	res, err := s.Forget(ForgetInput{IDs: []string{b.ID, a.ID}})
	if err != nil || len(res.Touched) != 0 || len(res.Forgotten) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestForget_서로_이은_두_기억을_함께_지운다(t *testing.T) {
	s, _ := newTestStore(t)
	a := forgetQuote(t, s, "배포는 금요일에 한다")
	b := forgetQuote(t, s, "백업은 목요일에 한다")
	if _, _, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ForgetInput{IDs: []string{a.ID, b.ID}}); err != nil {
		t.Fatal(err)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM memory_links`); n != 0 {
		t.Fatalf("links %d", n)
	}
}

func TestForget_상시_핵심_기억을_지우면_핵심_목록에서_빠진다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "답은 한글 존댓말로")
	keep := forgetQuote(t, s, "커밋 전에 시험을 돌린다")
	for _, id := range []string{m.ID, keep.ID} {
		if _, err := s.Pin(id, Who{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Forget(ForgetInput{IDs: []string{m.ID}}); err != nil {
		t.Fatal(err)
	}
	core, err := s.Core(ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if len(core) != 1 || core[0].ID != keep.ID {
		t.Fatalf("core %v", core)
	}
}

func TestForget_말이나_이유에_비밀값이_있으면_거절한다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "남을 규칙")
	secret := "ghp_abcdefghijklmnopqrstuvwxyz0123"
	for _, in := range []ForgetInput{
		{IDs: []string{m.ID}, Who: Who{Actor: ActorAI, UserQuote: "지워 " + secret}},
		{IDs: []string{m.ID}, Reason: "토큰 " + secret},
	} {
		_, err := s.Forget(in)
		var rej *RejectError
		if !errors.As(err, &rej) || rej.Reason != RejectSecret {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	for _, in := range []ForgetInput{
		{IDs: []string{m.ID}, Reason: strings.Repeat("가", 501)},
		{IDs: []string{m.ID}, Who: Who{Actor: ActorAI, UserQuote: strings.Repeat("가", 501)}},
	} {
		if _, err := s.Forget(in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("501 characters: %v", err)
		}
	}
	mustExist(t, s, m.ID)
	// 500자는 받는다.
	if _, err := s.Forget(ForgetInput{IDs: []string{m.ID}, Reason: strings.Repeat("가", 500)}); err != nil {
		t.Fatal(err)
	}
}

func TestForget_이미_forgotten에_있는_id를_다시_지워도_성공한다(t *testing.T) {
	s, _ := newTestStore(t)
	m := forgetQuote(t, s, "옛 판이 다시 들여온 기억")
	// 이 기능을 모르는 옛 판 실행 파일로 들여와 forgotten에 이미 있는 id의 기억이 돌아온 경우다.
	if _, err := s.DB().Exec(`INSERT INTO forgotten(id, legacy_id, at, actor, reason, quote) VALUES(?, 'legacy-old', 1, 'user', '처음', '')`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ForgetInput{IDs: []string{m.ID}, Reason: "다시"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after forget: %v", err)
	}
	var legacy, reason string
	if err := s.DB().QueryRow(`SELECT COALESCE(legacy_id,''), reason FROM forgotten WHERE id=?`, m.ID).Scan(&legacy, &reason); err != nil {
		t.Fatal(err)
	}
	if legacy != "legacy-old" || reason != "다시" { // 새 기억에 legacy_id가 없어도 옛 legacy_id는 남는다
		t.Fatalf("forgotten row %q %q", legacy, reason)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM forgotten`); n != 1 {
		t.Fatalf("forgotten rows %d", n)
	}
}

// 행을 다 지운 뒤 마지막 쓰기(forgotten 기록)가 실패하는 경우다: 기록 없이 지우면 되살아남을 막지 못하므로 전부 되돌린다.
func TestForget_마지막_기록이_실패하면_아무것도_안_지운다(t *testing.T) {
	s, _ := newTestStore(t)
	a := forgetQuote(t, s, "배포는 금요일에 한다")
	b := forgetQuote(t, s, "배포는 월요일에 한다")
	c := forgetQuote(t, s, "백업은 목요일에 한다")
	if _, err := s.Supersede(a.ID, b.ID, "요일을 바꿈"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link(LinkInput{From: b.ID, To: c.ID, Origin: LinkOriginUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP TABLE forgotten`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ForgetInput{IDs: []string{b.ID, c.ID}}); err == nil {
		t.Fatal("forget without the forgotten table succeeded")
	}
	mustExist(t, s, a.ID, b.ID, c.ID)
	if got, _ := s.Get(a.ID); got.SupersededBy != b.ID {
		t.Fatalf("the old version lost its successor: %+v", got)
	}
	if got, _ := s.Get(b.ID); len(got.Links) != 1 || len(got.Sources) != 1 {
		t.Fatalf("b lost rows: links %v sources %v", got.Links, got.Sources)
	}
	if found, err := s.Find(FindInput{Query: "배포 월요일", AllProjects: true}); err != nil || len(found) == 0 || found[0].ID != b.ID {
		t.Fatalf("b is not found any more: %v %v", found, err)
	}
}

func TestForgottenTx_표가_없으면_거짓이다(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.DB().Exec(`DROP TABLE forgotten`); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.IsForgotten("m-x", "legacy-x"); err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}
}
