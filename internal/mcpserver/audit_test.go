package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// AU-6 AC-7: MCP가 쓰는 쪽(actor)으로 사용자 인용 보호를 걸고, 감사 목록과 되돌리기·확인함을 준다.

func connectAs(t *testing.T, actor string) (*mcp.ClientSession, *memory.Store) {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return connectServer(t, New(s, Options{Project: "project:github.com/me/app", ProjectLabel: "github.com/me/app", Version: "test", Actor: actor})), s
}

func quotedRule(t *testing.T, s *memory.Store, title string) memory.Memory {
	t.Helper()
	m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: title, Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAC7_AI는_사용자_말_없이_사용자_인용_기억을_고치지_못한다(t *testing.T) {
	cs, s := connectAs(t, "")
	rule := quotedRule(t, s, "답은 한글 존댓말")
	res := call(t, cs, "memory_update", map[string]any{"id": rule.ID, "title": "답은 영어"}, nil)
	if !res.IsError || !strings.Contains(text(res), "사용자") {
		t.Fatalf("보호되지 않았습니다: %s", text(res))
	}
	res = call(t, cs, "memory_update", map[string]any{"id": rule.ID, "action": "stale", "reason": "낡음"}, nil)
	if !res.IsError {
		t.Fatal("낡음 표시가 거절되지 않았습니다")
	}
	var upd MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": rule.ID, "title": "답은 한글 존댓말로만", "userQuote": "그 규칙 제목을 '답은 한글 존댓말로만'으로 바꿔"}, &upd)
	if upd.Memory.Title != "답은 한글 존댓말로만" {
		t.Fatalf("사용자 말로 고치기: %+v", upd.Memory)
	}
	// 후보 확정도 사용자 말이 있어야 한다.
	cand, _ := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "배운 것", Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession}})
	if res := call(t, cs, "memory_update", map[string]any{"id": cand.ID, "action": "confirm"}, nil); !res.IsError {
		t.Fatal("AI가 증거·사용자 말 없이 확정했습니다")
	}
}

func TestAC7_사람_연결은_사용자_인용_기억을_고친다(t *testing.T) {
	cs, s := connectAs(t, memory.ActorUser)
	rule := quotedRule(t, s, "답은 한글 존댓말")
	var upd MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": rule.ID, "title": "답은 한글"}, &upd)
	if upd.Memory.Title != "답은 한글" {
		t.Fatalf("사람이 고치지 못했습니다: %+v", upd.Memory)
	}
}

func TestAC7_감사_목록과_되돌리기와_확인함(t *testing.T) {
	cs, s := connectAs(t, memory.ActorUser)
	clock := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return clock }
	rule := quotedRule(t, s, "배포는 직전 승인")
	in := memory.SaveInput{Kind: memory.KindLesson, Title: "launchd plist는 지우고 다시 건다", Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession}}
	m, _ := s.Save(in)
	clock = clock.Add(24 * time.Hour)
	if got, _ := s.Save(in); got.State != memory.StateConfirmed {
		t.Fatalf("증거 ① 확정 안 됨: %s", got.State)
	}
	other, _ := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "배포는 묻지 않는다", Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession}})
	flag, err := s.FlagConflict(other.ID, rule.ID, "사용자 규칙과 반대", memory.Who{Actor: memory.ActorAutomation})
	if err != nil {
		t.Fatal(err)
	}
	s.Policy.CorrectionSessions = 2
	for _, sess := range []string{"a", "b"} {
		if err := s.RecordCorrection(memory.CorrectionInput{Quote: "테스트 먼저 쓰라고", Session: sess, TargetKind: memory.TargetKindSkill, Target: "orbit:tdd"}); err != nil {
			t.Fatal(err)
		}
	}

	var out ProposalsOut
	call(t, cs, "proposals", map[string]any{"status": "audit"}, &out)
	a := out.Audit
	if a == nil || len(a.Changes) != 1 || a.Changes[0].Kind != memory.ProposalConfirm || a.Changes[0].Evidence != memory.EvidenceSeenDays {
		t.Fatalf("changes = %+v", a)
	}
	if len(a.Conflicts) != 1 || a.Conflicts[0].ID != flag.ID || a.Conflicts[0].OtherTitle != rule.Title {
		t.Fatalf("conflicts = %+v", a.Conflicts)
	}
	if len(a.Corrections) != 1 || !a.Corrections[0].Harness {
		t.Fatalf("corrections = %+v", a.Corrections)
	}

	var dec DecideOut
	call(t, cs, "proposal_decide", map[string]any{"id": a.Changes[0].ProposalID, "decision": "revert", "note": "아직 이르다"}, &dec)
	if got, _ := s.Get(m.ID); got.State != memory.StateCandidate {
		t.Fatalf("되돌린 뒤 %s (%+v)", got.State, dec)
	}
	call(t, cs, "proposal_decide", map[string]any{"id": flag.ID, "decision": "dismiss"}, nil)
	call(t, cs, "proposal_decide", map[string]any{"id": a.Corrections[0].Key, "decision": "dismiss"}, nil)
	call(t, cs, "proposals", map[string]any{"status": "audit"}, &out)
	if len(out.Audit.Conflicts) != 0 || len(out.Audit.Corrections) != 0 || out.Audit.Changes[0].RevertedBy == "" {
		t.Fatalf("after = %+v", out.Audit)
	}
}

func TestAC7_memory_save의_conflictsWith는_어긋남을_표시한다(t *testing.T) {
	cs, s := connectAs(t, "")
	rule := quotedRule(t, s, "배포는 직전 승인")
	var saved MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "배포는 묻지 않는다", "conflictsWith": []string{rule.ID},
		"source": map[string]any{"kind": "session", "ref": "claude:s"}}, &saved)
	flags, _ := s.OpenFlags()
	if len(flags) != 1 || flags[0].MemoryID != saved.Memory.ID || flags[0].OtherID != rule.ID || flags[0].Actor != memory.ActorAI {
		t.Fatalf("flags = %+v", flags)
	}
}

func TestResolveActor_서버_옵션과_환경(t *testing.T) {
	cases := []struct {
		flag, env, retro, want string
	}{
		{"", "", "", memory.ActorAI},
		{"user", "", "", memory.ActorUser},
		{"", "automation", "", memory.ActorAutomation},
		{"", "", "r-1", memory.ActorAutomation},
		{"user", "", "r-1", memory.ActorAutomation}, // 회고 실행 안에서는 언제나 자동화
		{"", "bogus", "", memory.ActorAI},
	}
	for _, c := range cases {
		if got := ResolveActor(c.flag, c.env, c.retro, memory.ActorAI); got != c.want {
			t.Errorf("ResolveActor(%q,%q,%q) = %q, want %q", c.flag, c.env, c.retro, got, c.want)
		}
	}
}

var _ = context.Background

// 검토 M-1: 사람이 아닌 쪽(AI)은 사용자 말 없이 사용자 인용 기억에 닿는 제안을 승인하거나 어긋남 표시를 닫지 못한다.
func TestReview_AI_결정은_보호된_것에_사용자_말이_필요하다(t *testing.T) {
	cs, s := connectAs(t, "")
	rule := quotedRule(t, s, "답은 한글 존댓말")
	p, err := s.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: "낡음", MemoryIDs: []string{rule.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if res := call(t, cs, "proposal_decide", map[string]any{"id": p.ID, "decision": "approve"}, nil); !res.IsError {
		t.Fatal("사용자 말 없이 승인됐습니다")
	}
	var many DecideOut
	call(t, cs, "proposal_decide", map[string]any{"ids": []string{p.ID}, "decision": "approve"}, &many)
	if len(many.Failed) != 1 {
		t.Fatalf("묶음 승인: %+v", many)
	}
	call(t, cs, "proposal_decide", map[string]any{"id": p.ID, "decision": "approve", "userQuote": "그 규칙 이제 안 써, 정리해"}, nil)
	if got, _ := s.Get(rule.ID); got.State != memory.StateStale {
		t.Fatalf("사용자 말로 승인: %s", got.State)
	}

	cand, _ := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "후보", Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession}})
	other := quotedRule(t, s, "다른 규칙")
	f, _ := s.FlagConflict(cand.ID, other.ID, "어긋남", memory.Who{Actor: memory.ActorAutomation})
	if res := call(t, cs, "proposal_decide", map[string]any{"id": f.ID, "decision": "dismiss"}, nil); !res.IsError {
		t.Fatal("사용자 말 없이 어긋남을 닫았습니다")
	}
	call(t, cs, "proposal_decide", map[string]any{"id": f.ID, "decision": "dismiss", "userQuote": "그건 어긋나는 거 아니야"}, nil)
	if fl, _ := s.OpenFlags(); len(fl) != 0 {
		t.Fatalf("flags = %+v", fl)
	}
}
