package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 명세 · O3-5 「스킬 제안과 승인 흐름」: 실험실 대기 제안은 대기 목록에 없고 결정할 수 없다. 같은 제안은 다시 쌓이지 않는다.
// 실험실이 도움됨이면 자동 변경으로 파일이 바뀌고 원장에 남으며(설계 · orbit-mem 2판 18.3), 아니면 사유와 함께 닫힌다.
func TestLab_실험실_대기_제안(t *testing.T) {
	s, _ := newTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "skills", "plain-writing", "SKILL.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("옛 스킬\n"), 0o644)
	in := ProposalInput{Kind: ProposalApplyFile, Title: "스킬 고치기", Lab: true, Payload: Payload{Path: path, Content: "새 스킬\n"}}
	p, err := s.Propose(in)
	if err != nil || p.Status != StatusLab {
		t.Fatalf("실험실 대기 %+v %v", p, err)
	}
	if pending, _ := s.Proposals(StatusPending); len(pending) != 0 || s.PendingCount() != 0 {
		t.Fatalf("대기 목록에 없어야 합니다: %+v", pending)
	}
	if _, err := s.Propose(in); !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("같은 제안은 다시 쌓이지 않습니다: %v", err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); !errors.Is(err, ErrDecided) {
		t.Fatalf("실험실 대기 제안은 결정할 수 없습니다: %v", err)
	}
	if err := s.ReleaseLabProposal(p.ID, "실험 x-1: 도움됨(3회 중 3회)"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProposal(p.ID)
	if got.Status != StatusAuto || got.Payload.Actor != ActorAutomation || !strings.Contains(got.Body, "실험 x-1: 도움됨") {
		t.Fatalf("적용한 제안 %+v", got)
	}
	if s.PendingCount() != 0 {
		t.Fatal("대기 제안이 생겼습니다")
	}
	if b, _ := os.ReadFile(path); string(b) != "새 스킬\n" {
		t.Fatalf("파일 %q", b)
	}

	q, _ := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "다른 고치기", Lab: true, Payload: Payload{Path: path, Content: "다른 스킬\n"}})
	if err := s.CloseLabProposal(q.ID, "실험 x-2: 효과 없음"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetProposal(q.ID); got.Status != StatusRejected || got.Note != "실험 x-2: 효과 없음" {
		t.Fatalf("닫은 제안 %+v", got)
	}
	if err := s.ReleaseLabProposal(q.ID, "x"); err == nil {
		t.Fatal("실험실 대기가 아닌 제안은 올릴 수 없습니다")
	}
}

// 명세 · O3-5 「기록」: 실험과 실행이 남고, 판정이 원장에 실험 줄로 남는다. 실험 줄은 관찰 기간 판정을 거치지 않는다.
func TestLab_실험_기록과_원장_줄(t *testing.T) {
	s, c := newTestStore(t)
	e := LabExperiment{ID: "x-1", TaskID: "t-1", Status: LabRunning, Runs: 3, Files: []string{"skills/plain-writing/SKILL.md"},
		Hashes: map[string]string{"current": "aa", "changed": "bb"}, CreatedAt: s.nowMs()}
	if err := s.SaveLabExperiment(e); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLabRun(LabRun{Experiment: "x-1", Candidate: "current", Round: 1, CostUSD: 0.2, OK: true}); err != nil {
		t.Fatal(err)
	}
	e.Status, e.Verdict, e.CostUSD = LabDone, VerdictHelped, 1.25
	e.Picks = []LabPick{{Round: 1, A: "changed", Pick: "A", Winner: "changed", Reason: "풀어 씀"}}
	id, err := s.RecordLabLedger(LabLedgerInput{ExperimentID: "x-1", Targets: []string{"/src/skills/plain-writing/SKILL.md"}, Change: "diff", Basis: "과제", Verdict: VerdictHelped})
	if err != nil {
		t.Fatal(err)
	}
	e.LedgerID = id
	if err := s.SaveLabExperiment(e); err != nil {
		t.Fatal(err)
	}
	got, err := s.LabExperiment("x-1")
	if err != nil || got.Verdict != VerdictHelped || got.LedgerID != id || len(got.Picks) != 1 || got.Picks[0].Winner != "changed" || got.Hashes["changed"] != "bb" {
		t.Fatalf("실험 %+v %v", got, err)
	}
	runs, _ := s.LabRuns("x-1")
	if len(runs) != 1 || runs[0].CostUSD != 0.2 {
		t.Fatalf("실행 %+v", runs)
	}
	l, _ := s.LedgerEntry(id)
	if l.ProposalKind != LedgerKindLab || l.TargetKind != TargetHarness || l.Verdict != VerdictHelped || !l.Evidence || !strings.Contains(l.Basis, "x-1") {
		t.Fatalf("원장 %+v", l)
	}
	c.t = c.t.AddDate(0, 0, 30)
	if _, err := s.JudgeLedger(7); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.LedgerEntry(id); l.Verdict != VerdictHelped {
		t.Fatalf("실험 판정은 관찰 판정으로 바뀌지 않습니다: %+v", l)
	}
}

// 검토 지적 6: 실험 줄은 실제로 승인한 변경이 아니다. 원장 요약 수에 세지 않고, 같은 파일을 승인한 변경의 판정을 섞임으로 만들지 않는다.
func TestLab_실험_줄은_승인한_변경과_섞이지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "skills", "plain-writing", "SKILL.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("옛\n"), 0o644)
	p, _ := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "스킬", Payload: Payload{Path: path, Content: "새\n"}})
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
	if _, err := s.RecordLabLedger(LabLedgerInput{ExperimentID: "x-9", Targets: []string{path}, Change: "d", Basis: "b", Verdict: VerdictHelped}); err != nil {
		t.Fatal(err)
	}
	sum, _ := s.SummarizeLedger(0, 1<<62)
	if sum.Changes != 1 || sum.Judged[VerdictHelped] != 0 {
		t.Fatalf("원장 요약 %+v", sum)
	}
	c.t = c.t.AddDate(0, 0, 30)
	if _, err := s.JudgeLedger(7); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.LedgerEntry(res.LedgerID); l.Verdict == VerdictMixed || len(l.Observation.MixedWith) != 0 {
		t.Fatalf("실험 줄 때문에 섞임이 되면 안 됩니다: %+v", l)
	}
}
