package memory

import (
	"strings"
	"testing"
)

func TestDecideMany_여럿을_결정하고_실패는_이유와_함께_돌려준다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "가")
	b := candidate(t, s, "나")
	c := candidate(t, s, "다")
	pa, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "가 확정", MemoryIDs: []string{a.ID}})
	pb, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "나 확정", MemoryIDs: []string{b.ID}})
	pc, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "다 확정", MemoryIDs: []string{c.ID}})
	if _, err := s.Decide(DecideInput{ID: pb.ID, Decision: "reject"}); err != nil {
		t.Fatal(err)
	}

	res := s.DecideMany([]string{pa.ID, pb.ID, "p-없음", pc.ID, pa.ID}, "approve", "묶음 승인")
	if len(res.Decided) != 2 {
		t.Fatalf("decided = %+v", res.Decided)
	}
	if len(res.Failed) != 2 {
		t.Fatalf("failed = %+v", res.Failed)
	}
	reasons := map[string]string{}
	for _, f := range res.Failed {
		reasons[f.ID] = f.Error
	}
	if !strings.Contains(reasons[pb.ID], "이미 결정") || !strings.Contains(reasons["p-없음"], "제안이 없습니다") {
		t.Fatalf("reasons = %v", reasons)
	}
	for _, id := range []string{a.ID, c.ID} {
		if m, _ := s.Get(id); m.State != StateConfirmed {
			t.Fatalf("%s state = %s", id, m.State)
		}
	}
	if p, _ := s.GetProposal(pc.ID); p.Status != StatusApproved || p.Note != "묶음 승인" {
		t.Fatalf("pc = %+v", p)
	}
}

func TestDecideMany_결정은_approve나_reject만_받는다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "가")
	p, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "가 확정", MemoryIDs: []string{a.ID}})
	res := s.DecideMany([]string{p.ID}, "edit", "")
	if len(res.Decided) != 0 || len(res.Failed) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if got, _ := s.GetProposal(p.ID); got.Status != StatusPending {
		t.Fatalf("status = %s", got.Status)
	}
}
