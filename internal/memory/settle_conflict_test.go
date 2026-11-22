package memory

// 사용자 말로 어긋남 쌍을 풀 때(settleConflictsAfterQuote)의 약속: 옛 쪽을 대체하는 것은 같은 자리(범위·주제)일 때뿐이고,
// 사용자가 옛 값을 유지한 쌍의 새 쪽은 스스로 확정하지 않으며, 가려진 옛 값은 사용자 말로 되살아난다.

import (
	"slices"
	"testing"
	"time"
)

const userWords = "그건 내가 정한 거야"

// quoteUpdate adds the user's words to memory id (an edit that carries a quote source).
func quoteUpdate(t *testing.T, s *Store, id string) {
	t.Helper()
	body := "사용자가 확인한 본문"
	if _, err := s.Update(UpdateInput{ID: id, Body: &body, Who: Who{Actor: ActorAI, UserQuote: userWords},
		Sources: []Source{{Kind: SourceQuote, Excerpt: userWords}}}); err != nil {
		t.Fatal(err)
	}
}

// flagOf reads the (open or closed) conflict mark of a pair.
func flagOf(t *testing.T, s *Store, newID, oldID string) (resolvedAt int64, resolution string, found bool) {
	t.Helper()
	err := s.DB().QueryRow(`SELECT resolved_at, resolution FROM audit_flags WHERE kind=? AND memory_id=? AND other_id=? ORDER BY at DESC, rowid DESC LIMIT 1`,
		FlagConflict, newID, oldID).Scan(&resolvedAt, &resolution)
	return resolvedAt, resolution, err == nil
}

// 다른 프로젝트 범위의 후보가 conflictsWith로 사용자 핵심 규칙을 가리켰을 때, 그 후보에 사용자 말이 붙어도 핵심 규칙은
// 낡지 않는다: 표시만 닫고 둘 다 그대로 둔다.
func TestSettle_새_쪽이_다른_자리면_옛_쪽을_대체하지_않고_표시만_닫는다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "답은 한글 존댓말로", Topic: "말투", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "답은 한글 존댓말로"}, Who: Who{Actor: ActorAI}})
	if _, err := s.Pin(rule.ID, Who{UserQuote: "늘 지켜"}); err != nil {
		t.Fatal(err)
	}
	cand, _ := mustSave(t, s, SaveInput{Kind: KindFact, Title: "이 프로젝트는 반말로 답한다", Topic: "말투 예외", Scope: replScope,
		ConflictsWith: []string{rule.ID}, Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	if !hasConflictFlag(t, s, cand.ID, rule.ID) {
		t.Fatal("어긋남 표시가 열리지 않았습니다")
	}
	quoteUpdate(t, s, cand.ID)
	got := stateOf(t, s, rule.ID)
	if got.State != StateConfirmed || got.SupersededBy != "" || !got.Pinned {
		t.Fatalf("핵심 규칙이 사라졌습니다: %+v", got)
	}
	if hasConflictFlag(t, s, cand.ID, rule.ID) {
		t.Fatal("표시가 닫히지 않았습니다")
	}
	if at, res, ok := flagOf(t, s, cand.ID, rule.ID); !ok || at == 0 || res != "user_words" {
		t.Fatalf("닫힌 표시 %d %q %v", at, res, ok)
	}
}

// 한 기억에 열린 쌍이 둘이면 같은 자리인 쪽만 낡고, 주제가 다른 상대는 그대로 현재 값이다.
func TestSettle_열린_쌍이_둘이면_같은_자리인_쪽만_낡는다(t *testing.T) {
	s := supStore(t)
	same, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
	other, _ := mustSave(t, s, userFact("점검은 월요일에 한다", "점검 요일"))
	cand, _ := mustSave(t, s, SaveInput{Kind: KindFact, Title: "배포는 화요일에 한다", Topic: "배포 요일", Scope: replScope,
		ConflictsWith: []string{other.ID}, Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	if !hasConflictFlag(t, s, cand.ID, same.ID) || !hasConflictFlag(t, s, cand.ID, other.ID) {
		t.Fatal("쌍 둘이 열리지 않았습니다")
	}
	quoteUpdate(t, s, cand.ID)
	if got := stateOf(t, s, same.ID); got.State != StateStale || got.SupersededBy != cand.ID {
		t.Fatalf("같은 자리: %+v", got)
	}
	if got := stateOf(t, s, other.ID); got.State != StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("주제가 다른 상대가 낡았습니다: %+v", got)
	}
	if got := stateOf(t, s, cand.ID); got.State != StateConfirmed {
		t.Fatalf("새 쪽 %+v", got)
	}
}

// 같은 자리 대체에서 옛 쪽이 핵심(pin)이었으면 새 쪽이 그 자리를 이어받는다: 핵심 규칙이 조용히 빠지지 않는다.
func TestSettle_같은_자리_대체는_옛_핵심의_자리를_새_쪽이_이어받는다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "커밋 메시지는 영어로", Topic: "커밋 언어", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "커밋 메시지는 영어로"}, Who: Who{Actor: ActorAI}})
	if _, err := s.Pin(rule.ID, Who{UserQuote: "늘 지켜"}); err != nil {
		t.Fatal(err)
	}
	cand, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "커밋 메시지는 한글로", Topic: "커밋 언어", Scope: ScopeGlobal,
		Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	if !hasConflictFlag(t, s, cand.ID, rule.ID) {
		t.Fatal("어긋남 표시가 열리지 않았습니다")
	}
	quoteUpdate(t, s, cand.ID)
	if got := stateOf(t, s, rule.ID); got.State != StateStale || got.SupersededBy != cand.ID {
		t.Fatalf("옛 규칙 %+v", got)
	}
	if got := stateOf(t, s, cand.ID); got.State != StateConfirmed || !got.Pinned {
		t.Fatalf("새 규칙이 핵심을 이어받지 못했습니다: %+v", got)
	}
}

// 대체가 실제로 막히면(여기서는 낡음으로 쓰는 쓰기가 실패) 쌍은 열려 있고 둘 다 그대로다.
func TestSettle_대체가_막히면_쌍을_닫지_않는다(t *testing.T) {
	s := supStore(t)
	old, nw := conflictPair(t, s, "배포 요일")
	if _, err := s.DB().Exec(`CREATE TRIGGER no_stale BEFORE UPDATE ON memories WHEN NEW.state='stale' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	quoteUpdate(t, s, nw.ID)
	if !hasConflictFlag(t, s, nw.ID, old.ID) {
		t.Fatal("막혔는데 쌍이 닫혔습니다")
	}
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("old %+v", got)
	}
}

// 같은 자리 쌍에서 옛 쪽에 사용자 말이 붙어 닫히면 사용자가 옛 값을 유지한 것이라, 새 쪽은 다른 날·다른 세션에 다시 나와도
// 스스로 확정하지 않는다. 그 새 쪽에 나중에 사용자 말이 붙으면(마음을 바꿈) 새 쪽이 이긴다.
func TestSettle_옛_값을_유지한_쌍의_새_쪽은_스스로_확정하지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	old, _ := mustSave(t, s, userFact("배포는 금요일에 한다", "배포 요일"))
	nin := SaveInput{Kind: KindFact, Title: "배포는 화요일에 한다", Topic: "배포 요일", Scope: replScope, Who: Who{Actor: ActorAI},
		Source: Source{Kind: SourceSession, Ref: "s"}, Session: "a"}
	nw, _ := mustSave(t, s, nin)
	if !hasConflictFlag(t, s, nw.ID, old.ID) {
		t.Fatal("어긋남 표시가 열리지 않았습니다")
	}
	quoteUpdate(t, s, old.ID)
	if at, res, ok := flagOf(t, s, nw.ID, old.ID); !ok || at == 0 || res != resolutionKeepOld {
		t.Fatalf("닫힌 표시 %d %q %v", at, res, ok)
	}
	if why, err := s.confirmBlocked(nw.ID); err != nil || why != blockKeptOld {
		t.Fatalf("confirmBlocked %q %v", why, err)
	}
	for _, session := range []string{"b", "c"} { // 다른 날·다른 세션에 두 번 다시 저장
		c.t = c.t.Add(24 * time.Hour)
		nin.Session = session
		again, _ := mustSave(t, s, nin)
		if again.State != StateCandidate {
			t.Fatalf("세션 %s: 옛 값을 유지한 쌍의 새 쪽이 스스로 확정됐습니다: %+v", session, again)
		}
	}
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("사용자가 다시 확인한 옛 값이 가려졌습니다: %+v", got)
	}
	// 마음을 바꿔 새 쪽에도 사용자 말을 붙이면 1번의 길로 새 쪽이 이긴다.
	quoteUpdate(t, s, nw.ID)
	if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != nw.ID {
		t.Fatalf("old %+v", got)
	}
	if got := stateOf(t, s, nw.ID); got.State != StateConfirmed {
		t.Fatalf("new %+v", got)
	}
}

// 다른 자리 쌍은 둘 다 현재 값이라 옛 쪽에 사용자 말이 붙어도 새 쪽을 막지 않는다.
func TestSettle_다른_자리_쌍은_옛_쪽을_풀어도_새_쪽을_막지_않는다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, SaveInput{Kind: KindRule, Title: "답은 한글 존댓말로", Topic: "말투", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "답은 한글 존댓말로"}, Who: Who{Actor: ActorAI}})
	cand, _ := mustSave(t, s, SaveInput{Kind: KindFact, Title: "이 프로젝트는 반말로 답한다", Topic: "말투 예외", Scope: replScope,
		ConflictsWith: []string{rule.ID}, Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	quoteUpdate(t, s, rule.ID)
	if at, res, ok := flagOf(t, s, cand.ID, rule.ID); !ok || at == 0 || res != "user_words" {
		t.Fatalf("닫힌 표시 %d %q %v", at, res, ok)
	}
	if why, err := s.confirmBlocked(cand.ID); err != nil || why != "" {
		t.Fatalf("다른 자리 쌍이 새 쪽을 막았습니다 %q %v", why, err)
	}
}

// 자동 대체를 되돌렸는데 다른 현재 값이 옛 쪽을 가린 쌍에서 옛 쪽에 사용자 말이 붙으면, 가리는 쪽이 옛 쪽으로 대체되어
// 옛 쪽이 현재 값이 된다.
func TestSettle_가려진_옛_쪽에_사용자_말이_붙으면_가리는_쪽이_대체된다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	mid, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	head, _ := mustSave(t, s, userFact("빌드 명령은 task build", "빌드 명령")) // mid를 대체해 머리가 된다
	revertAuto(t, s, old.ID)
	if !hasConflictFlag(t, s, head.ID, old.ID) {
		t.Fatalf("가려짐 쌍이 열리지 않았습니다 (mid %s)", mid.ID)
	}
	quoteUpdate(t, s, old.ID)
	if got := stateOf(t, s, head.ID); got.State != StateStale || got.SupersededBy != old.ID {
		t.Fatalf("가리던 쪽 %+v", got)
	}
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("옛 쪽이 현재 값이 되지 못했습니다: %+v", got)
	}
	if hasConflictFlag(t, s, head.ID, old.ID) {
		t.Fatal("쌍이 닫히지 않았습니다")
	}
}

// 대체가 막히면 가려짐 쌍도 닫지 않는다.
func TestSettle_가려진_옛_쪽의_대체가_막히면_쌍을_닫지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	head, _ := mustSave(t, s, userFact("빌드 명령은 task build", "빌드 명령"))
	revertAuto(t, s, old.ID)
	if _, err := s.DB().Exec(`CREATE TRIGGER no_stale BEFORE UPDATE ON memories WHEN NEW.state='stale' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	quoteUpdate(t, s, old.ID)
	if !hasConflictFlag(t, s, head.ID, old.ID) {
		t.Fatal("막혔는데 쌍이 닫혔습니다")
	}
	if got := stateOf(t, s, head.ID); got.State != StateConfirmed {
		t.Fatalf("head %+v", got)
	}
}

// 가려짐 쌍은 가리는 쪽이 실제 현재 값(주제 머리)일 때만 연다: 후보는 확정을 가리지 못한다.
func TestRevert_가리는_쪽이_후보면_가려짐_쌍을_열지_않는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	cand, _ := mustSave(t, s, SaveInput{Kind: KindFact, Title: "빌드 명령은 task build", Topic: "빌드 명령", Scope: replScope,
		Who: Who{Actor: ActorAI}, Source: Source{Kind: SourceSession, Ref: "s"}})
	if cand.State != StateCandidate {
		t.Fatalf("후보여야 합니다: %+v", cand)
	}
	revertAuto(t, s, old.ID)
	flags, err := s.OpenFlags()
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(flags, func(f Flag) bool { return f.OtherID == old.ID && f.MemoryID == cand.ID }); i >= 0 {
		t.Fatalf("후보가 확정을 가린다며 쌍을 열었습니다: %+v", flags[i])
	}
}

// 알림 floor는 읽기 전에 잡은 시각으로 옮긴다. 시각을 부르는 순간 다른 쪽이 자동 대체를 썼다고 하면(그 쓰기가 시각보다 먼저다),
// 읽기 뒤에 시각을 잡는 구현은 그 줄을 floor 밑으로 묻어 알림이 영영 나오지 않는다.
func TestAutoSupersedesUnnoticed_시각은_읽기_전에_잡는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	base, armed := s.Now, true
	s.Now = func() time.Time {
		if armed {
			armed = false
			mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령")) // 자동 대체 한 줄이 원장에 쓰인다
		}
		return base()
	}
	got, err := s.AutoSupersedesUnnoticed(func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OldID != old.ID {
		t.Fatalf("알림 %+v: 시각을 부르는 사이에 쓰인 자동 대체가 묻혔습니다", got)
	}
}
