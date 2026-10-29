package memory

import (
	"errors"
	"strings"
	"testing"
)

func proposeLink(t *testing.T, s *Store, a, b Memory) Proposal {
	t.Helper()
	p, err := s.Propose(ProposalInput{Kind: ProposalLink, Title: "같은 결정의 근거", Body: "두 기억이 같은 설계 결정을 말합니다", MemoryIDs: []string{a.ID, b.ID},
		Payload: Payload{Note: "같은 결정 token=sk-abcdefghijklmnop1234"}, RetroID: "r-1"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// 끝 기준: 연결 제안은 서로 다른 기억 둘이 필요하고, 이미 이어진 쌍은 제안하지 않는다.
func TestProposeLink_입력(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	for _, ids := range [][]string{{a.ID}, {a.ID, a.ID}, {a.ID, b.ID, saved(t, s, "C 기억").ID}} {
		if _, err := s.Propose(ProposalInput{Kind: ProposalLink, Title: "잇기", MemoryIDs: ids}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", ids, err)
		}
	}
	if _, _, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginAI}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Propose(ProposalInput{Kind: ProposalLink, Title: "잇기", MemoryIDs: []string{b.ID, a.ID}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("이미 이어진 쌍: %v", err)
	}
}

// 끝 기준: 승인하면 회고가 만든 연결이 생기고 기억 판은 그대로다. 원장에 그 연결이 남는다.
func TestDecideLink_승인(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	p := proposeLink(t, s, a, b)
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	ls, _ := s.LinksOf(a.ID)
	if len(ls) != 1 || ls[0].MemoryID != b.ID || ls[0].Origin != LinkOriginRetro || strings.Contains(ls[0].Note, "sk-abcdefghijklmnop1234") || ls[0].Note == "" {
		t.Fatalf("연결: %+v", ls)
	}
	if res.Link != ls[0].ID || len(res.Changed) != 0 {
		t.Errorf("결과: %+v", res)
	}
	var ref string
	_ = s.db.QueryRow(`SELECT origin_ref FROM memory_links WHERE id=?`, res.Link).Scan(&ref)
	if ref != p.ID {
		t.Errorf("origin_ref: %q", ref)
	}
	for _, m := range []Memory{a, b} {
		if got, _ := s.Get(m.ID); got.Version != m.Version {
			t.Errorf("판이 바뀌었습니다: %d → %d", m.Version, got.Version)
		}
	}
	e, err := s.LedgerEntry(res.LedgerID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Before.Link != res.Link || len(e.Targets) != 2 || !strings.Contains(e.Change, "연결") || len(e.Before.Memories) != 0 {
		t.Errorf("원장: %+v", e)
	}
}

// 끝 기준: 고쳐서 승인할 수 없다. 거절한 쌍은 다시 제안되지 않는다.
func TestDecideLink_고침_거절(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	p := proposeLink(t, s, a, b)
	title := "다른 제목"
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Title: &title}); !errors.Is(err, ErrInvalid) {
		t.Errorf("고쳐서 승인: %v", err)
	}
	if ls, _ := s.LinksOf(a.ID); len(ls) != 0 {
		t.Errorf("고쳐서 승인하려다 연결이 생겼습니다: %+v", ls)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "reject", Note: "상관없음"}); err != nil {
		t.Fatal(err)
	}
	if ls, _ := s.LinksOf(a.ID); len(ls) != 0 {
		t.Errorf("거절했는데 연결: %+v", ls)
	}
	if _, err := s.Propose(ProposalInput{Kind: ProposalLink, Title: "다시", MemoryIDs: []string{b.ID, a.ID}}); !errors.Is(err, ErrDuplicateProposal) {
		t.Errorf("거절한 쌍: %v", err)
	}
}

// 끝 기준: 연결 승인을 되돌리면 그 연결만 떼고(retro_revert) 기억 판은 그대로다.
func TestRevertLink(t *testing.T) {
	s, c := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	res, err := s.Decide(DecideInput{ID: proposeLink(t, s, a, b).ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(60e9)
	rp, err := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "연결 되돌리기", Payload: Payload{LedgerID: res.LedgerID}})
	if err != nil {
		t.Fatal(err)
	}
	rr, err := s.Decide(DecideInput{ID: rp.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if ls, _ := s.LinksOf(a.ID); len(ls) != 0 {
		t.Errorf("연결이 남았습니다: %+v", ls)
	}
	var origin string
	_ = s.db.QueryRow(`SELECT removed_origin FROM memory_links WHERE id=?`, res.Link).Scan(&origin)
	if origin != LinkOriginRetroRevert || rr.Link != res.Link || len(rr.Changed) != 0 {
		t.Errorf("뗀 쪽 %q, 결과 %+v", origin, rr)
	}
	for _, m := range []Memory{a, b} {
		if got, _ := s.Get(m.ID); got.Version != m.Version {
			t.Errorf("되돌리기가 기억 판을 바꿨습니다: %d → %d", m.Version, got.Version)
		}
	}
	e, _ := s.LedgerEntry(rr.LedgerID)
	if e.RevertOf != res.LedgerID || len(e.Targets) != 2 || !strings.Contains(e.Change, "연결") {
		t.Errorf("되돌림 원장: %+v", e)
	}
	if orig, _ := s.LedgerEntry(res.LedgerID); orig.RevertedBy != rr.LedgerID {
		t.Errorf("원래 줄: %+v", orig)
	}
}

// 끝 기준: 사용자가 이미 뗀 연결을 되돌려도 실패하지 않는다. 그 사이 AI가 다시 이은 연결은 떼지 않는다.
func TestRevertLink_이미_뗀_연결(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	res, _ := s.Decide(DecideInput{ID: proposeLink(t, s, a, b).ID, Decision: "approve"})
	if _, err := s.UnlinkID(res.Link, LinkOriginUser, "직접 뗌"); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginAI})
	if err != nil {
		t.Fatal(err)
	}
	rp, _ := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: res.LedgerID}})
	if _, err := s.Decide(DecideInput{ID: rp.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if ls, _ := s.LinksOf(a.ID); len(ls) != 1 || ls[0].ID != again.ID {
		t.Errorf("AI가 다시 이은 연결: %+v", ls)
	}
}

// 끝 기준: 기다리는 연결 제안의 쌍이 다른 길로 이어지면 정리된다.
func TestCloseSettled_연결(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := saved(t, s, "A 기억"), saved(t, s, "B 기억")
	p := proposeLink(t, s, a, b)
	if _, _, err := s.Link(LinkInput{From: a.ID, To: b.ID, Origin: LinkOriginAI}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CloseSettled(); err != nil || n != 1 {
		t.Fatalf("정리: %d %v", n, err)
	}
	if got, _ := s.GetProposal(p.ID); got.Status != StatusSettled {
		t.Errorf("상태: %s", got.Status)
	}
}
