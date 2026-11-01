package retro

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// AU-6(ONID-131) 끝 기준을 회고 쪽에서: 회고는 저장 계층 API를 쓰고 그 규칙을 그대로 받는다.
// 사용자 인용 기억을 회고가 고치려 하면 적용되지 않고(사용자 결정 대기), 스킬 교정은 대상별로 세며,
// 검증을 통과한 일에서 따른 후보는 증거로 확정된다.
func TestAU6_회고는_저장_계층_규칙을_따른다(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, kst)
	e := setup(t, now)
	e.s.Policy.FollowedVerified = 1
	e.s.Now = func() time.Time { return now.AddDate(0, 0, -3) }
	rule, _ := e.s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 한글 존댓말", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "답은 한글 존댓말로 해"}})
	cand, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "배포 전에 dry-run을 돌린다", Source: memory.Source{Kind: memory.SourceSession}})
	e.s.Now = func() time.Time { return now }
	e.s.RecordEvent(memory.Event{Kind: memory.EventInjectPrompt, Host: "claude", Session: "sess-x"}, map[string]any{"memoryIds": []string{cand.ID}})
	path := e.session(t, "sess-x",
		claudeTurn("2026-10-27", "13:00", "user", "배포해줘. 시험 먼저 돌리라고 tdd 스킬대로"),
		claudeTurn("2026-10-27", "13:05", "ai", "dry-run 뒤 go test ./... ok, 배포했습니다"))
	if _, err := Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "sess-x", Transcript: path, Cwd: "/w/app"}); err != nil {
		t.Fatal(err)
	}
	var report string
	e.ai = func(id string) error {
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{Summary: "배포",
			Corrections: []CorrectionIn{{Quote: "시험 먼저 돌리라고 tdd 스킬대로", SessionRef: "claude:sess-x", Skill: "orbit:tdd", Note: "시험을 나중에 씀"}},
			Usage:       []UsageIn{{MemoryID: cand.ID, Verdict: "followed", Verified: true, Note: "go test ok"}},
		})
		return err
	}
	if _, err := e.r.DrainSessions(context.Background()); err != nil {
		t.Fatalf("%v (%s)", err, report)
	}
	if got, _ := e.s.Get(cand.ID); got.State != memory.StateConfirmed {
		t.Fatalf("검증 통과·따름 증거로 확정되지 않았습니다: %s (%s)", got.State, report)
	}
	all, _ := e.s.RepeatedCorrections(true)
	if len(all) != 1 || all[0].TargetKind != memory.TargetKindSkill || all[0].Target != "orbit:tdd" {
		t.Fatalf("스킬 교정 = %+v", all)
	}

	// 하루 회고가 사용자 인용 기억을 낡음으로 정리하려 하면 적용되지 않고 사용자 결정을 기다린다.
	e.r.Now = func() time.Time { return now.AddDate(0, 0, 1) }
	e.talk(t, "2026-10-27", claudeTurn("2026-10-27", "16:00", "user", "오늘 정리"))
	e.ai = func(id string) error {
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{Summary: "정리", Journal: &JournalIn{Title: "정리", Body: "-"},
			Proposals: []ProposalIn{{Kind: "stale", Title: "옛 말투 규칙 정리", MemoryIDs: []string{rule.ID}, Body: "안 쓰는 것 같다"}}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-27"); err != nil || res.Status != StatusDone {
		t.Fatalf("daily %+v %v", res, err)
	}
	if got, _ := e.s.Get(rule.ID); got.State != memory.StateConfirmed || got.Version != rule.Version {
		t.Fatalf("사용자 인용 기억이 바뀌었습니다: %+v", got)
	}
	if p := pendingOf(t, e, memory.ProposalStale); len(p) != 1 || p[0].MemoryIDs[0] != rule.ID {
		t.Fatalf("사용자 결정 대기 제안 = %+v (%s)", p, report)
	}
	if !strings.Contains(report, "결정을 기다리는 것 1건") {
		t.Fatalf("보고: %s", report)
	}
	if _, err := e.s.AutoApply(memory.AutoInput{Kind: memory.ProposalStale, Title: "직접", MemoryIDs: []string{rule.ID}}); !errors.Is(err, memory.ErrProtected) {
		t.Fatalf("자동화 직접 호출: %v", err)
	}
}
