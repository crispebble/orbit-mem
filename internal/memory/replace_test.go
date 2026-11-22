package memory

// 대체 정책 표(명세 C-1·C-3·C-4·C-7·C-8·C-9, E-4): 저장한 새 기억이 같은 자리의 옛 기억을 만났을 때 대체·자동 대체·어긋남을
// 종류 × 새 근거 × 옛 근거로 가르고, 자동 대체는 원장에 남아 되돌릴 수 있으며 되돌린 쌍은 다시 대체되지 않는다.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const replScope = "project:github.com/o/r"

// userFact is a fact the user said, in a repository scope (what C-1 may replace).
func userFact(title, topic string) SaveInput {
	in := userSays(KindFact, title, topic)
	in.Scope = replScope
	return in
}

// checkedFact is a fact whose verification source the server read again (Rechecked), in the same scope.
func checkedFact(title, topic string) SaveInput {
	return SaveInput{Kind: KindFact, Title: title, Topic: topic, Scope: replScope, Who: Who{Actor: ActorAI},
		Source: Source{Kind: SourceVerification, Ref: "/repo/Makefile", Excerpt: "build: go build ./...", Rechecked: true}}
}

// statedFact is a fact whose verification source names a command and its output: the server cannot read it again.
func statedFact(title, topic string) SaveInput {
	return SaveInput{Kind: KindFact, Title: title, Topic: topic, Scope: replScope, Who: Who{Actor: ActorAI},
		Source: Source{Kind: SourceVerification, Ref: "go test ./...", Excerpt: "ok  github.com/o/r  0.4s"}}
}

// autoReplaceLines are the ledger lines of automatic replacements (a supersede proposal kept as auto) whose
// old memory is old.
func autoReplaceLines(t *testing.T, s *Store, old string) []LedgerEntry {
	t.Helper()
	var out []LedgerEntry
	for _, e := range mustLedger(t, s) {
		if e.ProposalKind == ProposalSupersede && slices.Equal(e.Targets, []string{old}) && s.isAuto(e) {
			out = append(out, e)
		}
	}
	return out
}

func hasConflictFlag(t *testing.T, s *Store, newID, oldID string) bool {
	t.Helper()
	flags, err := s.OpenFlags()
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(flags, func(f Flag) bool { return f.Kind == FlagConflict && f.MemoryID == newID && f.OtherID == oldID })
}

// 표의 줄마다 대표 입력 하나 이상: 자동 모드 끔·켬의 결과를 본다. 마지막에 모든 줄이 한 번 이상 걸렸는지 본다(줄을 더하거나
// 순서를 바꾸면 여기서 먼저 드러난다).
func TestReplace_표의_각_줄을_자동_모드_켬과_끔으로_가른다(t *testing.T) {
	const (
		S = OutcomeSupersede
		A = OutcomeAutoSupersede
		C = OutcomeConflict
	)
	cases := []struct {
		name     string
		kind     string
		nw       NewGround
		old      OldGround
		away     bool // 같은 범위·주제가 아님(supersedes로 다른 자리를 지목)
		reverted bool
		off, on  Outcome
	}{
		{"사용자 말은 user 범위 사용자 말도 대체", KindRule, GroundQuote, OldQuoteUserScope, false, false, S, S},
		{"사용자 말은 사용자 말 사실을 대체", KindFact, GroundQuote, OldQuote, false, false, S, S},
		{"사용자 말은 그 밖도 대체", KindLesson, GroundQuote, OldOther, false, false, S, S},
		{"사용자 말은 믿을 수 없는 옛 기억도 대체", KindDecision, GroundQuote, OldUntrusted, false, false, S, S},
		{"user 범위 사실은 재확인 사실도 못 바꿈", KindFact, GroundRechecked, OldQuoteUserScope, false, false, C, C},
		{"user 범위 사실은 근거 적힌 사실도 못 바꿈", KindFact, GroundStated, OldQuoteUserScope, false, false, C, C},
		{"user 범위 교훈은 대화로 못 바꿈", KindLesson, GroundTalk, OldQuoteUserScope, false, false, C, C},
		{"규칙은 재확인 출처여도 어긋남", KindRule, GroundRechecked, OldQuote, false, false, C, C},
		{"결정은 근거 적힌 출처여도 어긋남", KindDecision, GroundStated, OldQuote, false, false, C, C},
		{"재확인 사실은 자동 모드에서만 자동 대체", KindFact, GroundRechecked, OldQuote, false, false, C, A},
		{"근거 적힌 사실은 자동 모드에서만 자동 대체", KindFact, GroundStated, OldQuote, false, false, C, A},
		{"대화로 배운 사실은 어긋남", KindFact, GroundTalk, OldQuote, false, false, C, C},
		{"믿을 수 없는 사실은 어긋남", KindFact, GroundUntrusted, OldQuote, false, false, C, C},
		{"사실이 아닌 교훈은 재확인이어도 어긋남", KindLesson, GroundRechecked, OldQuote, false, false, C, C},
		{"종류가 다른 쌍은 재확인이어도 어긋남", kindMixed, GroundRechecked, OldQuote, false, false, C, C},
		{"믿을 수 없는 것끼리는 대체", KindFact, GroundUntrusted, OldUntrusted, false, false, S, S},
		{"믿을 수 없는 새 기억은 그 밖을 못 바꿈", KindFact, GroundUntrusted, OldOther, false, false, C, C},
		{"재확인 사실은 그 밖을 대체", KindFact, GroundRechecked, OldOther, false, false, S, S},
		{"근거 적힌 사실은 믿을 수 없는 옛 기억을 대체", KindFact, GroundStated, OldUntrusted, false, false, S, S},
		{"대화로 배운 규칙은 그 밖을 대체", KindRule, GroundTalk, OldOther, false, false, S, S},
		{"다른 자리의 사용자 말 사실은 재확인 사실도 못 바꿈", KindFact, GroundRechecked, OldQuote, true, false, C, C},
		{"다른 자리의 사용자 말 사실은 근거 적힌 사실도 못 바꿈", KindFact, GroundStated, OldQuote, true, false, C, C},
		{"다른 자리여도 사용자 말은 대체", KindFact, GroundQuote, OldQuote, true, false, S, S},
		{"다른 자리의 그 밖은 지금처럼 대체", KindFact, GroundRechecked, OldOther, true, false, S, S},
		{"되돌린 옛 기억은 재확인 사실도 어긋남", KindFact, GroundRechecked, OldQuote, false, true, C, C},
		{"되돌린 옛 기억은 그 밖끼리도 어긋남", KindFact, GroundTalk, OldOther, false, true, C, C},
		{"되돌린 옛 기억도 사용자 말은 대체", KindFact, GroundQuote, OldQuote, false, true, S, S},
	}
	hit := make([]bool, len(replaceTable))
	for _, c := range cases {
		for _, auto := range []bool{false, true} {
			want := c.off
			if auto {
				want = c.on
			}
			if got := decideReplace(c.kind, c.nw, c.old, !c.away, auto, c.reverted); got != want {
				t.Errorf("%s (자동 모드 %v): %v, want %v", c.name, auto, got, want)
			}
		}
		if !c.reverted {
			i := replaceRowOf(c.kind, c.nw, c.old, !c.away)
			if i < 0 {
				t.Errorf("%s: 걸린 줄이 없다", c.name)
				continue
			}
			hit[i] = true
		}
	}
	for i, ok := range hit {
		if !ok {
			t.Errorf("표의 %d번째 줄 %+v를 시험하지 않았다", i+1, replaceTable[i])
		}
	}
	// 표 밖의 조합(모르는 근거)은 어긋남이다: 표에 없는 것으로 사용자 말이 밀려나지 않는다.
	if got := decideReplace(KindFact, NewGround(99), OldQuote, true, true, false); got != OutcomeConflict {
		t.Errorf("표 밖 = %v", got)
	}
}

func TestSave_자동_모드_켬이면_서버가_다시_확인한_검증_사실은_사용자_말_사실을_자동_대체한다(t *testing.T) {
	s := supStore(t)
	s.Policy.AutoMode = true
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	cur, rep := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	got := stateOf(t, s, old.ID)
	if got.State != StateStale || got.SupersededBy != cur.ID || !slices.Contains(rep.Superseded, old.ID) || len(rep.Conflicts) != 0 {
		t.Fatalf("old %+v rep %+v", got, rep)
	}
	// 옛 기억은 지워지지 않고 버전 이력에 남는다(C-1).
	h, _ := s.History(old.ID)
	if last := h[len(h)-1]; last.Action != "supersede" || !strings.Contains(last.Reason, cur.Title) {
		t.Fatalf("history %+v", last)
	}
	// 재확인한 검증 사실은 어긋남이 없으면 바로 확정된다(E-4).
	if c := stateOf(t, s, cur.ID); c.State != StateConfirmed {
		t.Fatalf("new state %s", c.State)
	}
	// 원장 줄: 옛 기억만 Before·After에 들고, 새 id는 제안의 payload에 있다.
	lines := autoReplaceLines(t, s, old.ID)
	if len(lines) != 1 {
		t.Fatalf("자동 대체 원장 %d줄", len(lines))
	}
	e := lines[0]
	if len(e.Before.Memories) != 1 || e.Before.Memories[0].ID != old.ID || e.Before.Memories[0].State != StateConfirmed ||
		len(e.Before.After) != 1 || e.Before.After[old.ID] != got.Version || len(e.Before.Created) != 0 {
		t.Fatalf("before %+v", e.Before)
	}
	p, err := s.GetProposal(e.ProposalID)
	if err != nil || p.Status != StatusAuto || p.Payload.NewID != cur.ID || !slices.Equal(p.MemoryIDs, []string{old.ID, cur.ID}) || p.Payload.Actor != ActorAI {
		t.Fatalf("proposal %+v %v", p, err)
	}
	if hasConflictFlag(t, s, cur.ID, old.ID) {
		t.Fatal("자동 대체에 어긋남 표시가 붙었다")
	}
}

func TestSave_자동_모드_끔이면_명령_출력_근거의_검증_사실은_어긋남만_남긴다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("시험은 make test", "시험 명령"))
	cur, rep := mustSave(t, s, statedFact("시험은 go test", "시험 명령"))
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.Version != old.Version || !slices.Contains(rep.Conflicts, old.ID) || len(rep.Superseded) != 0 {
		t.Fatalf("old %+v rep %+v", got, rep)
	}
	if !hasConflictFlag(t, s, cur.ID, old.ID) {
		t.Fatal("어긋남 표시가 없다")
	}
	if n := len(autoReplaceLines(t, s, old.ID)); n != 0 {
		t.Fatalf("원장 %d줄", n)
	}
	if c := stateOf(t, s, cur.ID); c.State != StateCandidate {
		t.Fatalf("new state %s", c.State)
	}
	// 저장 하나의 자동 모드 끔은 저장소의 켬보다 앞선다(설정을 저장마다 다시 읽는 서버).
	s.Policy.AutoMode = true
	in := statedFact("시험은 go test -race", "시험 명령")
	in.AutoMode = ptr(false)
	if _, rep := mustSave(t, s, in); !slices.Contains(rep.Conflicts, old.ID) || stateOf(t, s, old.ID).State != StateConfirmed {
		t.Fatalf("저장의 끔이 무시됨 %+v", rep)
	}
}

func TestSave_자동_모드_켬이면_근거_적힌_검증_사실이_대체한다(t *testing.T) {
	for _, c := range []struct {
		name  string
		store bool
		save  *bool
	}{{"저장소 정책", true, nil}, {"저장 하나의 설정", false, ptr(true)}} {
		t.Run(c.name, func(t *testing.T) {
			s := supStore(t)
			s.Policy.AutoMode = c.store
			old, _ := mustSave(t, s, userFact("시험은 make test", "시험 명령"))
			in := statedFact("시험은 go test", "시험 명령")
			in.AutoMode = c.save
			cur, rep := mustSave(t, s, in)
			if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != cur.ID || !slices.Contains(rep.Superseded, old.ID) {
				t.Fatalf("old %+v rep %+v", got, rep)
			}
			if n := len(autoReplaceLines(t, s, old.ID)); n != 1 {
				t.Fatalf("원장 %d줄", n)
			}
			// 서버가 다시 확인하지 못한 근거는 즉시 확정되지 않는다(E-4).
			if st := stateOf(t, s, cur.ID).State; st != StateCandidate {
				t.Fatalf("new state %s", st)
			}
		})
	}
}

func TestSave_사용자_범위_사실은_자동_대체하지_않는다(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint("자동 모드 ", auto), func(t *testing.T) {
			s := supStore(t)
			s.Policy.AutoMode = auto
			old, _ := mustSave(t, s, userSays(KindFact, "주말에는 일하지 않는다", "주말 일정")) // user 범위(global)
			in := checkedFact("주말에도 일한다", "주말 일정")
			in.Scope = ScopeGlobal
			cur, rep := mustSave(t, s, in)
			if got := stateOf(t, s, old.ID); got.State != StateConfirmed || !slices.Contains(rep.Conflicts, old.ID) {
				t.Fatalf("old %+v rep %+v", got, rep)
			}
			if !hasConflictFlag(t, s, cur.ID, old.ID) {
				t.Fatal("어긋남 표시가 없다")
			}
			// 저장소 범위의 새 사실이 supersedes로 user 범위 사실을 지목해도 같다.
			ex := checkedFact("주말에는 배포만 한다", "배포 요일")
			ex.Supersedes = []string{old.ID}
			if _, rep := mustSave(t, s, ex); !slices.Contains(rep.Conflicts, old.ID) || stateOf(t, s, old.ID).State != StateConfirmed {
				t.Fatalf("explicit supersedes %+v", rep)
			}
			if n := len(autoReplaceLines(t, s, old.ID)); n != 0 {
				t.Fatalf("원장 %d줄", n)
			}
		})
	}
}

func TestSave_규칙_결정은_검증_출처여도_상태가_바뀌지_않는다(t *testing.T) {
	for _, kind := range []string{KindRule, KindDecision} {
		for _, auto := range []bool{false, true} {
			for _, src := range []func(string, string) SaveInput{checkedFact, statedFact} {
				s := supStore(t)
				s.Policy.AutoMode = auto
				in := userSays(kind, "배포 전에 점검표를 본다", "배포 점검")
				in.Scope = replScope
				old, _ := mustSave(t, s, in)
				nw := src("배포 전에 점검표를 보지 않는다", "배포 점검")
				nw.Kind = kind
				cur, rep := mustSave(t, s, nw)
				got := stateOf(t, s, old.ID)
				if got.State != StateConfirmed || got.Version != old.Version || !slices.Contains(rep.Conflicts, old.ID) || len(rep.Superseded) != 0 {
					t.Fatalf("%s 자동 모드 %v: old %+v rep %+v", kind, auto, got, rep)
				}
				if !hasConflictFlag(t, s, cur.ID, old.ID) {
					t.Fatalf("%s 자동 모드 %v: 어긋남 쌍이 없다", kind, auto)
				}
				// 같은 주제의 새 사실도 사용자 말 규칙·결정을 대체하지 않는다(종류가 다른 쌍).
				f := src("배포 점검표는 없다", "배포 점검")
				if _, rep := mustSave(t, s, f); !slices.Contains(rep.Conflicts, old.ID) || stateOf(t, s, old.ID).State != StateConfirmed {
					t.Fatalf("%s 자동 모드 %v: 사실이 규칙을 대체 %+v", kind, auto, rep)
				}
			}
		}
	}
}

func TestSave_세션_출처_사실은_사용자_말_사실을_대체하지_않는다(t *testing.T) {
	for _, auto := range []bool{false, true} {
		s := supStore(t)
		s.Policy.AutoMode = auto
		old, _ := mustSave(t, s, userFact("배포 서버는 둘이다", "배포 서버 수"))
		in := aiLearns(KindFact, "배포 서버는 셋이다", "배포 서버 수")
		in.Scope = replScope
		cur, rep := mustSave(t, s, in)
		if got := stateOf(t, s, old.ID); got.State != StateConfirmed || !slices.Contains(rep.Conflicts, old.ID) || !hasConflictFlag(t, s, cur.ID, old.ID) {
			t.Fatalf("자동 모드 %v: old %+v rep %+v", auto, got, rep)
		}
		// 근거 없는 검증 출처(ref·발췌가 빈 것)도 대화로 배운 것과 같다.
		bare := SaveInput{Kind: KindFact, Title: "배포 서버는 넷이다", Topic: "배포 서버 수", Scope: replScope, Who: Who{Actor: ActorAI},
			Source: Source{Kind: SourceVerification}}
		if _, rep := mustSave(t, s, bare); !slices.Contains(rep.Conflicts, old.ID) || stateOf(t, s, old.ID).State != StateConfirmed {
			t.Fatalf("자동 모드 %v: 빈 검증 출처 %+v", auto, rep)
		}
	}
}

// 발언 기록이 없는 호스트(대조 못 함)의 사용자 말 출처는 지금처럼 사용자 말 근거다(명세 S-4 「지금 동작 유지」). 기록에서 찾지
// 못한 말은 이 저장에 오기 전에 이미 session으로 낮춰졌다.
func TestSave_대조_못한_사용자_말은_지금처럼_옛_사용자_말을_대체한다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userFact("저장소 기본 가지는 main", "기본 가지"))
	in := userFact("저장소 기본 가지는 trunk", "기본 가지")
	in.QuoteMatched = false
	cur, rep := mustSave(t, s, in)
	if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != cur.ID || !slices.Contains(rep.Superseded, old.ID) {
		t.Fatalf("old %+v rep %+v", got, rep)
	}
	// 사용자 말의 대체는 자동 변경이 아니다: 원장에 남기지 않는다(지금 동작).
	if n := len(autoReplaceLines(t, s, old.ID)); n != 0 {
		t.Fatalf("원장 %d줄", n)
	}
}

func TestSave_예전에_사용자_말이_붙은_기억을_AI가_다시_저장해도_사용자_규칙을_대체하지_않는다(t *testing.T) {
	s := supStore(t)
	rule, _ := mustSave(t, s, userSays(KindRule, "답은 존댓말로", "답 말투")) // user 범위 사용자 규칙
	fact, _ := mustSave(t, s, userFact("저장소 언어는 Go", "저장소 언어"))
	// 예전에 사용자 말로 저장된 기억 x(같은 종류·범위)를 AI가 대화 출처로 다시 저장하며 사용자 규칙과 사실을 지목한다.
	x := userSays(KindRule, "답은 짧게", "답 길이")
	mustSave(t, s, x)
	again := aiLearns(KindRule, "답은 짧게", "답 길이")
	again.Supersedes = []string{rule.ID}
	if _, rep := mustSave(t, s, again); !slices.Contains(rep.Conflicts, rule.ID) || len(rep.Superseded) != 0 {
		t.Fatalf("rule rep %+v", rep)
	}
	if got := stateOf(t, s, rule.ID); got.State != StateConfirmed || got.Version != rule.Version {
		t.Fatalf("rule %+v", got)
	}
	// 사실 쪽: 예전에 사용자 말이 붙은 사실을 대화 출처로 다시 저장해도 사용자 말 사실을 대체하지 않는다.
	y := userFact("저장소 빌드는 make", "저장소 빌드")
	mustSave(t, s, y)
	againFact := aiLearns(KindFact, "저장소 빌드는 make", "저장소 빌드")
	againFact.Scope = replScope
	againFact.Supersedes = []string{fact.ID}
	if _, rep := mustSave(t, s, againFact); !slices.Contains(rep.Conflicts, fact.ID) || stateOf(t, s, fact.ID).State != StateConfirmed {
		t.Fatalf("fact rep %+v", rep)
	}
}

// revertAuto undoes the one automatic replacement of old as the person (the audit list's revert).
func revertAuto(t *testing.T, s *Store, old string) DecideResult {
	t.Helper()
	lines := autoReplaceLines(t, s, old)
	if len(lines) != 1 {
		t.Fatalf("자동 대체 원장 %d줄", len(lines))
	}
	res, err := s.RevertChange(lines[0].ID, Who{}, "틀린 대체")
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	return res
}

func TestRevert_자동_사실_대체를_되돌리면_옛_것은_확정_새_것은_후보이고_다시_대체되지_않는다(t *testing.T) {
	for _, c := range []struct {
		name string
		auto bool
		in   func(string, string) SaveInput
	}{{"자동 모드의 재확인 사실", true, checkedFact}, {"자동 모드의 근거 적힌 사실", true, statedFact}} {
		t.Run(c.name, func(t *testing.T) {
			s := supStore(t)
			s.Policy.AutoMode = c.auto
			old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
			cur, _ := mustSave(t, s, c.in("빌드 명령은 go build", "빌드 명령"))
			line := autoReplaceLines(t, s, old.ID)[0]
			wasConfirmed := stateOf(t, s, cur.ID).State == StateConfirmed
			res := revertAuto(t, s, old.ID)
			if !slices.Contains(res.Changed, old.ID) || slices.Contains(res.Changed, cur.ID) != wasConfirmed {
				t.Fatalf("changed %v (new was confirmed %v)", res.Changed, wasConfirmed)
			}
			if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.SupersededBy != "" {
				t.Fatalf("old %+v", got)
			}
			if got := stateOf(t, s, cur.ID); got.State != StateCandidate {
				t.Fatalf("new %+v", got)
			}
			var ledgerID string
			if err := s.DB().QueryRow(`SELECT ledger_id FROM supersede_reverts WHERE old_id=? AND new_id=?`, old.ID, cur.ID).Scan(&ledgerID); err != nil || ledgerID != line.ID {
				t.Fatalf("되돌린 쌍 %q %v", ledgerID, err)
			}
			// 그 새 기억은 증거로도(검증 사실 즉시 확정) 다시 확정되지 않는다.
			if _, ok, err := s.ConfirmByEvidence(cur.ID); ok || err != nil {
				t.Fatalf("confirm ok=%v err=%v", ok, err)
			}
			if why, _ := s.confirmBlocked(cur.ID); why != blockRevertedReplace {
				t.Fatalf("막는 까닭 %q", why)
			}
			if _, err := s.ConfirmCandidates(); err != nil || stateOf(t, s, cur.ID).State != StateCandidate {
				t.Fatalf("일괄 확정 뒤 %s %v", stateOf(t, s, cur.ID).State, err)
			}
			// 같은 새 기억을 같은 근거로 다시 저장하며 옛 기억을 지목해도 다시 대체하지 않는다.
			again := c.in("빌드 명령은 go build", "빌드 명령")
			again.Supersedes = []string{old.ID}
			if m, rep := mustSave(t, s, again); m.ID != cur.ID || !slices.Contains(rep.Conflicts, old.ID) || len(rep.Superseded) != 0 {
				t.Fatalf("다시 저장 %+v %+v", m, rep)
			}
			if got := stateOf(t, s, old.ID); got.State != StateConfirmed || stateOf(t, s, cur.ID).State != StateCandidate {
				t.Fatalf("다시 저장 뒤 old %s new %s", got.State, stateOf(t, s, cur.ID).State)
			}
			// 되돌림을 다시 되돌리면 옛 기억은 다시 대체된 채가 되고 쌍 기록이 빠진다. 새 기억은 그 사이 다시 저장돼 버전이
			// 바뀌었어도 막지 않는다(새 기억은 되돌림 원장의 버전 검사에 들지 않는다).
			rev := mustLedger(t, s)[0]
			if rev.RevertOf != line.ID {
				t.Fatalf("되돌림 줄 %+v", rev)
			}
			if _, err := s.RevertChange(rev.ID, Who{}, "되돌림 취소"); err != nil {
				t.Fatalf("되돌림 되돌리기: %v", err)
			}
			if got := stateOf(t, s, old.ID); got.State != StateStale || got.SupersededBy != cur.ID {
				t.Fatalf("되돌림 되돌린 뒤 old %+v", got)
			}
			var n int
			if err := s.DB().QueryRow(`SELECT COUNT(*) FROM supersede_reverts WHERE new_id=?`, cur.ID).Scan(&n); err != nil || n != 0 {
				t.Fatalf("쌍 기록 %d %v", n, err)
			}
		})
	}
}

func TestRevert_즉시_확정과_같은_제목_재저장_뒤에도_되돌리기가_성공한다(t *testing.T) {
	s := supStore(t)
	s.Policy.AutoMode = true
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	cur, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
	if c := stateOf(t, s, cur.ID); c.State != StateConfirmed || c.Version < 2 {
		t.Fatalf("즉시 확정 %+v", c)
	}
	again := checkedFact("빌드 명령은 go build", "빌드 명령")
	again.Session = "s-2"
	if m, _ := mustSave(t, s, again); m.ID != cur.ID || m.Version <= 2 {
		t.Fatalf("재저장 %+v", m)
	}
	before := stateOf(t, s, cur.ID).Version
	revertAuto(t, s, old.ID)
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed {
		t.Fatalf("old %+v", got)
	}
	if got := stateOf(t, s, cur.ID); got.State != StateCandidate || got.Version != before+1 {
		t.Fatalf("new %+v (before v%d)", got, before)
	}
}

func TestEvidence_서버가_다시_확인_못한_검증_사실은_즉시_확정하지_않는다(t *testing.T) {
	s := supStore(t)
	stated, _ := mustSave(t, s, statedFact("시험은 go test ./...", ""))
	if stated.State != StateCandidate {
		t.Fatalf("state %s", stated.State)
	}
	if ev, err := s.EvidenceFor(stated.ID); ev != "" || err != nil {
		t.Fatalf("증거 %q %v", ev, err)
	}
	checked, _ := mustSave(t, s, checkedFact("빌드는 go build ./...", ""))
	if checked.State != StateConfirmed {
		t.Fatalf("checked state %s", checked.State)
	}
	var rechecked int
	if err := s.DB().QueryRow(`SELECT rechecked FROM sources WHERE memory_id=? AND kind=?`, checked.ID, SourceVerification).Scan(&rechecked); err != nil || rechecked != 1 {
		t.Fatalf("rechecked %d %v", rechecked, err)
	}
	if err := s.DB().QueryRow(`SELECT rechecked FROM sources WHERE memory_id=?`, stated.ID).Scan(&rechecked); err != nil || rechecked != 0 {
		t.Fatalf("stated rechecked %d %v", rechecked, err)
	}
	// 재확인한 검증 출처가 뒤에 붙으면 그때 확정된다.
	if _, err := s.Save(checkedFact("시험은 go test ./...", "")); err != nil {
		t.Fatal(err)
	}
	if st := stateOf(t, s, stated.ID).State; st != StateConfirmed {
		t.Fatalf("재확인 뒤 state %s", st)
	}
	// user 범위 사실은 재확인이어도 즉시 확정하지 않는다(지금 동작).
	g := checkedFact("편집기는 vim", "")
	g.Scope = ScopeGlobal
	if m, _ := mustSave(t, s, g); m.State != StateCandidate {
		t.Fatalf("user fact state %s", m.State)
	}
}

// 두 연결이 같은 사용자 말 사실을 재확인 사실로 동시에 바꾼다: 옛 사실의 자동 대체 원장 줄은 하나이고, 주제의 현재값은 하나다.
func TestSave_두_연결이_같은_사용자_말_사실을_동시에_자동_대체해도_원장_줄은_하나다(t *testing.T) {
	for round := range 5 {
		path := filepath.Join(t.TempDir(), "memory.db")
		var stores []*Store
		for range 2 {
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.Policy.AutoMode = true
			stores = append(stores, s)
		}
		old, _ := mustSave(t, stores[0], userFact("빌드 명령은 make all", "빌드 명령"))
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for w, s := range stores {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, _, err := s.SaveWith(checkedFact(fmt.Sprintf("빌드 명령은 go build %d", w), "빌드 명령")); err != nil {
					errs <- err
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		if n := len(autoReplaceLines(t, stores[0], old.ID)); n != 1 {
			t.Fatalf("round %d: 자동 대체 원장 %d줄", round, n)
		}
		var live int
		if err := stores[0].DB().QueryRow(`SELECT COUNT(*) FROM memories WHERE topic=? AND state IN ('candidate','confirmed')`, "빌드 명령").Scan(&live); err != nil || live != 1 {
			t.Fatalf("round %d: 현재값 %d %v", round, live, err)
		}
	}
}

func TestRecheckExcerpt_없는_파일과_상대경로는_false다(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo") // 작업 폴더(저장소 루트)
	outside := filepath.Join(base, "elsewhere")
	for _, d := range []string{filepath.Join(root, "sub"), filepath.Join(root, ".git"), filepath.Join(root, "cfg", ".hidden"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const body = "all:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n"
	write := func(path string) string {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	file := write(filepath.Join(root, "Makefile"))
	write(filepath.Join(root, "sub", "Makefile"))
	out := write(filepath.Join(outside, "Makefile"))
	dotted := write(filepath.Join(root, ".git", "config"))
	dotFile := write(filepath.Join(root, ".env"))
	deepDot := write(filepath.Join(root, "cfg", ".hidden", "app.conf"))
	roots := []string{root}
	if !RecheckExcerpt(roots, file, "test:\n\tgo test ./...") {
		t.Fatal("있는 발췌를 못 찾았다")
	}
	if !RecheckExcerpt(roots, file, "  test:   go test   ./... ") { // 공백 차이는 같다고 본다
		t.Fatal("공백만 다른 발췌를 못 찾았다")
	}
	if !RecheckExcerpt(roots, filepath.Join(root, "sub", "Makefile"), "go build ./...") {
		t.Fatal("루트 아래 폴더의 파일을 못 읽었다")
	}
	inLink := filepath.Join(root, "inlink")
	if err := os.Symlink(file, inLink); err != nil {
		t.Fatal(err)
	}
	if !RecheckExcerpt(roots, inLink, "go build ./...") { // 루트 안을 가리키는 링크는 푼 경로로 본다
		t.Fatal("루트 안을 가리키는 링크")
	}
	t.Chdir(root)
	outLink := filepath.Join(root, "outlink")
	if err := os.Symlink(out, outLink); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "outdir")
	if err := os.Symlink(outside, outDir); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(root, "big.log")
	if err := os.WriteFile(big, append([]byte("go build ./... ok\n"), make([]byte, recheckMaxBytes)...), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, path, excerpt string
		roots               []string
	}{
		{"없는 파일", filepath.Join(root, "none"), "go build ./...", roots},
		{"상대경로(지금 폴더에 있어도)", "Makefile", "go build ./...", roots},
		{"디렉터리", root, "go build ./...", roots},
		{"루트 밖 절대경로", out, "go build ./...", roots},
		{"루트 밖을 가리키는 파일 링크", outLink, "go build ./...", roots},
		{"루트 밖을 가리키는 중간 디렉터리 링크", filepath.Join(outDir, "Makefile"), "go build ./...", roots},
		{"..으로 루트 밖", filepath.Join(root, "sub", "..", "..", "elsewhere", "Makefile"), "go build ./...", roots},
		{"점 폴더 아래", dotted, "go build ./...", roots},
		{"점 파일", dotFile, "go build ./...", roots},
		{"깊은 점 폴더 아래", deepDot, "go build ./...", roots},
		{"루트를 모름", file, "go build ./...", nil},
		{"빈 루트", file, "go build ./...", []string{""}},
		{"상대 루트", file, "go build ./...", []string{"repo"}},
		{"아주 큰 파일", big, "go build ./... ok", roots},
		{"파일에 없는 발췌", file, "go vet ./... passes", roots},
		{"짧은 발췌", file, "all:", roots},
		{"빈 발췌", file, "   ", roots},
		{"빈 경로", "", "go build ./...", roots},
	} {
		if RecheckExcerpt(c.roots, c.path, c.excerpt) {
			t.Errorf("%s: true", c.name)
		}
	}
	// 홈 폴더나 그 위를 루트로 주면 읽지 않는다(홈에서 연 세션이 홈 전체를 근거로 삼지 못하게).
	t.Setenv("HOME", root)
	if RecheckExcerpt(roots, file, "go build ./...") || RecheckExcerpt([]string{base}, file, "go build ./...") {
		t.Error("홈이나 그 위를 루트로 읽었다")
	}
	t.Setenv("HOME", outside)
	recheckSpecialFiles(t, root) // FIFO·읽을 권한 없는 파일(유닉스만)
	// 시간 한도: 여는 데서 멈춘 파일은 한도 안에 false로 돌아온다.
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	open, limit := recheckOpen, recheckTimeout
	recheckOpen = func(string) (*os.File, error) { <-block; return nil, errors.New("closed") }
	recheckTimeout = 50 * time.Millisecond
	t.Cleanup(func() { recheckOpen, recheckTimeout = open, limit })
	start := time.Now()
	if RecheckExcerpt(roots, file, "go build ./...") || time.Since(start) > time.Second {
		t.Errorf("멈춘 읽기: %v", time.Since(start))
	}
}

// 자동 대체는 같은 범위·주제의 사용자 말 사실만이다(C-1): supersedes로 다른 주제나 다른 저장소의 사용자 말 사실을 지목해도,
// 주제 없는 사실끼리여도 어긋남이다. 사용자 말끼리의 대체는 지금처럼 자리를 가리지 않는다.
func TestSave_다른_주제나_범위의_사용자_말_사실은_supersedes로_지목해도_자동_대체하지_않는다(t *testing.T) {
	for _, auto := range []bool{false, true} {
		s := supStore(t)
		s.Policy.AutoMode = auto
		otherTopic, _ := mustSave(t, s, userFact("배포는 화요일", "배포 요일"))
		in := userFact("빌드 서버는 하나", "빌드 서버")
		in.Scope = "project:github.com/o/other"
		otherScope, _ := mustSave(t, s, in)
		noTopic, _ := mustSave(t, s, userFact("저장소 언어는 Go", ""))
		for _, mk := range []func(string, string) SaveInput{checkedFact, statedFact} {
			ex := mk(fmt.Sprintf("빌드 서버는 둘 %v", auto), "빌드 서버")
			ex.Supersedes = []string{otherTopic.ID, otherScope.ID}
			if _, rep := mustSave(t, s, ex); len(rep.Superseded) != 0 || !slices.Contains(rep.Conflicts, otherTopic.ID) || !slices.Contains(rep.Conflicts, otherScope.ID) {
				t.Fatalf("자동 모드 %v: %+v", auto, rep)
			}
			bare := mk(fmt.Sprintf("저장소 언어는 Go와 셸 %v", auto), "")
			bare.Supersedes = []string{noTopic.ID}
			if _, rep := mustSave(t, s, bare); len(rep.Superseded) != 0 || !slices.Contains(rep.Conflicts, noTopic.ID) {
				t.Fatalf("주제 없음 자동 모드 %v: %+v", auto, rep)
			}
		}
		for _, m := range []Memory{otherTopic, otherScope, noTopic} {
			if got := stateOf(t, s, m.ID); got.State != StateConfirmed {
				t.Fatalf("자동 모드 %v: %s %+v", auto, m.Title, got)
			}
			if n := len(autoReplaceLines(t, s, m.ID)); n != 0 {
				t.Fatalf("원장 %d줄", n)
			}
		}
		// 사용자 말은 다른 주제의 사용자 말도 지금처럼 대체한다.
		q := userFact(fmt.Sprintf("배포는 수요일 %v", auto), "다른 주제")
		q.Supersedes = []string{otherTopic.ID}
		if _, rep := mustSave(t, s, q); !slices.Contains(rep.Superseded, otherTopic.ID) {
			t.Fatalf("사용자 말 대체 %+v", rep)
		}
	}
}

// 사람이 한 번 되돌린 옛 기억은 제목만 바꾼 새 기억(새 id)으로도 사용자 말이 아닌 근거로는 다시 자동 대체하지 않는다(C-3).
// 사용자 말은 대체한다(C-6).
func TestRevert_되돌린_옛_기억은_제목을_바꾼_새_기억으로도_다시_자동_대체되지_않는다(t *testing.T) {
	for _, auto := range []bool{true} { // 되돌릴 자동 대체가 있으려면 자동 모드가 켜져 있어야 한다
		s := supStore(t)
		s.Policy.AutoMode = auto
		old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
		first, _ := mustSave(t, s, checkedFact("빌드 명령은 go build", "빌드 명령"))
		revertAuto(t, s, old.ID)
		for i, mk := range []func(string, string) SaveInput{checkedFact, statedFact} {
			again, rep := mustSave(t, s, mk(fmt.Sprintf("빌드 명령은 go build ./... %d", i), "빌드 명령"))
			if slices.Contains(rep.Superseded, old.ID) || !slices.Contains(rep.Conflicts, old.ID) {
				t.Fatalf("자동 모드 %v, %d: %+v", auto, i, rep)
			}
			if got := stateOf(t, s, old.ID); got.State != StateConfirmed {
				t.Fatalf("old %+v", got)
			}
			// 새 기억의 상태를 기록한다(즉시 확정이 되는지 보고용).
			t.Logf("자동 모드 %v, %d: 새 기억 %s, 앞 새 기억 %s", auto, i, stateOf(t, s, again.ID).State, stateOf(t, s, first.ID).State)
		}
		if n := len(autoReplaceLines(t, s, old.ID)); n != 1 {
			t.Fatalf("원장 %d줄", n)
		}
		var live int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memories WHERE topic=? AND state='confirmed'`, "빌드 명령").Scan(&live); err != nil || live != 1 {
			t.Fatalf("확정된 현재값 %d %v", live, err)
		}
		// 사용자 말이면 대체한다.
		if _, rep := mustSave(t, s, userFact("빌드 명령은 make release", "빌드 명령")); !slices.Contains(rep.Superseded, old.ID) {
			t.Fatalf("사용자 말 %+v", rep)
		}
	}
}

// 종류가 다른 쌍(옛 것이 사용자 말 교훈·결정, 새 것이 재확인 사실, 또는 그 반대)은 같은 주제여도 어긋남만 남는다.
func TestSave_종류가_다른_사용자_말_기억은_재확인_사실로도_대체하지_않는다(t *testing.T) {
	for _, c := range []struct{ oldKind, newKind string }{{KindLesson, KindFact}, {KindDecision, KindFact}, {KindFact, KindLesson}} {
		for _, auto := range []bool{false, true} {
			s := supStore(t)
			s.Policy.AutoMode = auto
			in := userFact("배포 전에 점검표를 본다", "배포 점검")
			in.Kind = c.oldKind
			old, _ := mustSave(t, s, in)
			nw := checkedFact("배포 점검표는 없다", "배포 점검")
			nw.Kind = c.newKind
			if _, rep := mustSave(t, s, nw); len(rep.Superseded) != 0 || !slices.Contains(rep.Conflicts, old.ID) || stateOf(t, s, old.ID).State != StateConfirmed {
				t.Fatalf("%s ← %s 자동 모드 %v: %+v", c.oldKind, c.newKind, auto, rep)
			}
		}
	}
}

// 자동 대체의 원장 줄을 쓰지 못하면 저장 전체가 되돌아간다: 옛 기억은 확정 그대로이고 새 기억·제안은 남지 않는다.
func TestSave_자동_대체의_원장을_쓰지_못하면_저장_전체가_되돌아간다(t *testing.T) {
	s := supStore(t)
	s.Policy.AutoMode = true
	old, _ := mustSave(t, s, userFact("빌드 명령은 make all", "빌드 명령"))
	if _, err := s.DB().Exec(`CREATE TRIGGER ledger_down BEFORE INSERT ON ledger BEGIN SELECT RAISE(ABORT, 'ledger down'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SaveWith(checkedFact("빌드 명령은 go build", "빌드 명령")); err == nil || !strings.Contains(err.Error(), "ledger down") {
		t.Fatalf("err %v", err)
	}
	if got := stateOf(t, s, old.ID); got.State != StateConfirmed || got.Version != old.Version || got.SupersededBy != "" {
		t.Fatalf("old %+v", got)
	}
	var mems, props int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&mems); err != nil || mems != 1 {
		t.Fatalf("기억 %d %v", mems, err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM proposals`).Scan(&props); err != nil || props != 0 {
		t.Fatalf("제안 %d %v", props, err)
	}
}
