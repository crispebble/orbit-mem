package mcpserver

// S4 명세 1.1·3.1: memory_update가 적용 대상을 옮기면 원장에 한 줄을 남겨 감사 목록에 보이고
// audit revert로 되돌릴 수 있다. 옮겨서 상시 핵심에서 빠지면 응답 경고에 그렇게 적는다.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
)

func TestS4_적용_대상_옮기기는_원장에_남고_되돌리면_핵심도_돌아온다(t *testing.T) {
	cs, s := connectAs(t, "")
	rule := quotedRule(t, s, "화면 일은 그 시스템의 UI 키트로 만든다")
	if _, err := s.Pin(rule.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	var up MemoryOut
	res := call(t, cs, "memory_update", map[string]any{"id": rule.ID, "appliesTo": "repo", "userQuote": "그 규칙은 이 저장소로 옮겨"}, &up)
	if res.IsError || up.Memory.Pinned || up.Memory.Scope == memory.ScopeGlobal {
		t.Fatalf("move: %v %s %+v", res.IsError, text(res), up.Memory)
	}
	warned := false
	for _, w := range up.Warnings {
		warned = warned || strings.Contains(w, "상시 핵심에서 빠짐")
	}
	if !warned {
		t.Fatalf("no core warning: %v", up.Warnings)
	}
	lines, err := s.Ledger(memory.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var line memory.LedgerEntry
	for _, l := range lines {
		if len(l.Targets) == 1 && l.Targets[0] == rule.ID {
			line = l
		}
	}
	if line.ID == "" || !strings.Contains(line.Change, "적용 대상") && !strings.Contains(line.Change, "범위") {
		t.Fatalf("no ledger line for the move: %+v", lines)
	}
	if _, err := s.RevertChange(line.ID, memory.Who{}, "옮기기 되돌림"); err != nil {
		t.Fatal(err)
	}
	back, _ := s.Get(rule.ID)
	if back.Scope != memory.ScopeGlobal || !back.Pinned {
		t.Fatalf("revert: scope %s pinned %v", back.Scope, back.Pinned)
	}
}

func TestS4_사용자_말_없는_AI의_옮기기는_감사_목록에_자동_변경으로_나온다(t *testing.T) {
	cs, s := connectAs(t, "")
	m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "빌드는 make로 한다", Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession, Excerpt: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res := call(t, cs, "memory_update", map[string]any{"id": m.ID, "appliesTo": "repo"}, nil); res.IsError {
		t.Fatal(text(res))
	}
	a, err := s.Audit(memory.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range a.Changes {
		found = found || len(c.Targets) == 1 && c.Targets[0] == m.ID && c.Actor == memory.ActorAI
	}
	if !found {
		t.Fatalf("move not in the audit list: %+v", a.Changes)
	}
	// 옮기지 않은 고치기(제목만)는 예전처럼 원장에 남지 않는다.
	n := len(mustLedger(t, s))
	if res := call(t, cs, "memory_update", map[string]any{"id": m.ID, "title": "빌드는 make로만 한다"}, nil); res.IsError {
		t.Fatal(text(res))
	}
	if len(mustLedger(t, s)) != n {
		t.Fatal("title edit wrote a ledger line")
	}
}

func mustLedger(t *testing.T, s *memory.Store) []memory.LedgerEntry {
	t.Helper()
	l, err := s.Ledger(memory.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// S4 검토 A10(b): MCP 감사 목록의 「A — B」 검사도 설정의 제품 지도를 쓴다.
func TestS4_MCP_감사의_두_주제_검사는_제품_지도를_쓴다(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	cs := connectServer(t, New(s, Options{Project: "project:github.com/me/app", ProjectLabel: "github.com/me/app", Version: "test",
		Products: place.Map{"atlas": {Match: []string{"github.com/o/atlas"}}, "forma": {Match: []string{"github.com/o/forma"}}}}))
	m := quotedRule(t, s, "Atlas 문서 위치는 Space — Forma 문서 위치는 사이트")
	var out ProposalsOut
	if res := call(t, cs, "proposals", map[string]any{"status": "audit"}, &out); res.IsError || out.Audit == nil {
		t.Fatalf("%s", text(res))
	}
	for _, x := range out.Audit.TwoTopics {
		if x.ID == m.ID && x.Why == memory.TwoTopicsProducts {
			return
		}
	}
	t.Fatalf("not listed: %+v", out.Audit.TwoTopics)
}
