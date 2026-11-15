package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func forgetHuman(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	if _, err := s.Forget(ForgetInput{IDs: ids, Who: Who{}}); err != nil {
		t.Fatal(err)
	}
}

// auditText joins every column of the ledger and the proposals, to look for a text that must be gone.
func auditText(t *testing.T, s *Store) string {
	t.Helper()
	var l, p string
	if err := s.DB().QueryRow(`SELECT COALESCE(group_concat(targets||observe||change||basis||before, char(10)),'') FROM ledger`).Scan(&l); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COALESCE(group_concat(title||body||payload||memory_ids||note, char(10)),'') FROM proposals`).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return l + "\n" + p
}

func TestForgetAudit_지운_기억의_원장_줄은_남고_글이_비워진다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "비밀 제목 가")
	b := candidate(t, s, "남는 제목 나")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalMerge, Title: "합치기: 비밀 제목 가", Body: "비밀 본문",
		MemoryIDs: []string{a.ID, b.ID}, Payload: Payload{MergedTitle: "합친 것", MergedBody: "본문"}}))
	if err != nil {
		t.Fatal(err)
	}
	lines := countOf(t, s, `SELECT COUNT(*) FROM ledger`)
	forgetHuman(t, s, a.ID)

	if got := countOf(t, s, `SELECT COUNT(*) FROM ledger`); got != lines {
		t.Fatalf("원장 줄 %d → %d", lines, got)
	}
	e, err := s.LedgerEntry(res.LedgerID)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(forgetLedgerChange, a.ID)
	if e.Change != want || e.Basis != want {
		t.Fatalf("change=%q basis=%q", e.Change, e.Basis)
	}
	for _, m := range e.Before.Memories {
		if m.ID == a.ID {
			t.Fatal("지운 기억의 스냅숏이 남았습니다")
		}
	}
	if len(e.Before.Memories) != 1 || e.Before.Memories[0].ID != b.ID {
		t.Fatalf("남은 스냅숏 = %+v", e.Before.Memories)
	}
	if len(e.Before.Created) != 1 || !slices.Contains(e.Targets, a.ID) {
		t.Fatalf("created=%v targets=%v", e.Before.Created, e.Targets)
	}
	if !slices.Equal(e.Before.Forgotten, []string{a.ID}) {
		t.Fatalf("forgotten = %v", e.Before.Forgotten)
	}
	p, err := s.GetProposal(e.ProposalID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != fmt.Sprintf(forgetProposalTitle, a.ID) || p.Body != "" {
		t.Fatalf("title=%q body=%q", p.Title, p.Body)
	}
	if mustJSON(t, p.Payload) != "{}" {
		t.Fatalf("payload = %+v", p.Payload)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM proposals WHERE id=? AND payload='{}'`, e.ProposalID); n != 1 {
		t.Fatal("payload가 빈 JSON 객체가 아닙니다")
	}
	if text := auditText(t, s); strings.Contains(text, "비밀 제목 가") || strings.Contains(text, "비밀 본문") {
		t.Fatalf("글이 남았습니다: %s", text)
	}
}

func TestForgetAudit_대체_표시만_담긴_원장_줄도_찾는다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "옛 판 에이")
	b := candidate(t, s, "대체한 비")
	if _, err := s.AutoApply(auto(AutoInput{Kind: ProposalSupersede, Title: "에이를 비가 대체", MemoryIDs: []string{a.ID, b.ID}})); err != nil {
		t.Fatal(err)
	}
	edit := approveEdit(t, s, a.ID, "고친 에이") // 이 줄의 스냅숏 A에만 supersededBy가 B로 담긴다
	e, _ := s.LedgerEntry(edit)
	if !slices.Equal(e.Targets, []string{a.ID}) || !strings.Contains(mustJSON(t, e.Before), `"supersededBy":"`+b.ID+`"`) {
		t.Fatalf("준비가 어긋났습니다: targets=%v before=%s", e.Targets, mustJSON(t, e.Before))
	}
	forgetHuman(t, s, b.ID)

	e, _ = s.LedgerEntry(edit)
	if raw := mustJSON(t, e.Before); strings.Contains(raw, "supersededBy") || len(e.Before.Memories) != 0 {
		t.Fatalf("대체 표시가 남았습니다: %s", raw)
	}
	if !slices.Contains(e.Before.Forgotten, b.ID) {
		t.Fatalf("forgotten = %v", e.Before.Forgotten)
	}
	// 옛 판 A의 version은 B를 지워도 그대로다: 되돌려 superseded_by=B가 되살아나면 안 된다.
	n := countOf(t, s, `SELECT COUNT(*) FROM proposals`)
	if _, err := s.RevertChange(edit, Who{}, "되돌려"); !errors.Is(err, ErrLedgerForgotten) {
		t.Fatalf("RevertChange = %v", err)
	}
	if got := countOf(t, s, `SELECT COUNT(*) FROM proposals`); got != n {
		t.Fatal("거절했는데 제안 줄이 늘었습니다")
	}
	if got, _ := s.Get(a.ID); got.SupersededBy != "" {
		t.Fatalf("superseded_by = %q", got.SupersededBy)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestForgetAudit_되돌림_제안과_이어진_원장_줄도_비운다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "되돌릴 비밀 제목")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음: 되돌릴 비밀 제목", MemoryIDs: []string{a.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := s.RevertChange(res.LedgerID, Who{}, "비밀 제목 사유")
	if err != nil {
		t.Fatal(err)
	}
	forgetHuman(t, s, a.ID)

	if text := auditText(t, s); strings.Contains(text, "되돌릴 비밀 제목") {
		t.Fatalf("제목이 남았습니다: %s", text)
	}
	for _, id := range []string{res.LedgerID, rev.LedgerID} {
		e, err := s.LedgerEntry(id)
		if err != nil || e.Change != fmt.Sprintf(forgetLedgerChange, a.ID) {
			t.Fatalf("%s: %q %v", id, e.Change, err)
		}
	}
	if first, _ := s.LedgerEntry(res.LedgerID); first.RevertedBy != rev.LedgerID {
		t.Fatalf("reverted_by = %q", first.RevertedBy)
	}
}

func TestForgetAudit_감사_목록에_지운_기억의_제목과_본문이_없다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "감사 비밀 제목")
	b := candidate(t, s, "감사 남는 제목")
	if _, err := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음: 감사 비밀 제목", Body: "감사 비밀 본문", MemoryIDs: []string{a.ID}})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음: 남는", MemoryIDs: []string{b.ID}})); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Audit(AuditFilter{})
	forgetHuman(t, s, a.ID)
	after, err := s.Audit(AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Changes) != len(before.Changes) || len(after.Changes) != 2 {
		t.Fatalf("항목 %d → %d", len(before.Changes), len(after.Changes))
	}
	raw := mustJSON(t, after)
	if strings.Contains(raw, "감사 비밀 제목") || strings.Contains(raw, "감사 비밀 본문") {
		t.Fatalf("감사 목록에 글이 남았습니다: %s", raw)
	}
}

func TestForgetAudit_자동_되돌림_사건의_제목을_비우고_ledgerId는_둔다(t *testing.T) {
	s, c := newTestStore(t)
	a := candidate(t, s, "사건 비밀 제목")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음: 사건 비밀 제목", MemoryIDs: []string{a.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE ledger SET verdict=? WHERE id=?`, VerdictHarmful, res.LedgerID); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
	if r, err := s.RevertHarmfulAuto(); err != nil || len(r) != 1 || r[0].Error != "" {
		t.Fatalf("RevertHarmfulAuto = %+v %v", r, err)
	}
	forgetHuman(t, s, a.ID)

	evs, err := s.Events(EventFilter{FromDay: "2000-01-01", ToDay: "2100-01-01", KindPrefix: EventAutoReverted})
	if err != nil || len(evs) != 1 {
		t.Fatalf("사건 %d건 %v", len(evs), err)
	}
	var data struct {
		LedgerID string `json:"ledgerId"`
		Title    string `json:"title"`
	}
	if err := json.Unmarshal([]byte(mustJSON(t, evs[0].Data)), &data); err != nil {
		t.Fatal(err)
	}
	if data.LedgerID != res.LedgerID || strings.Contains(data.Title, "사건 비밀 제목") {
		t.Fatalf("data = %+v", data)
	}
}

func TestForgetAudit_지운_기억이_든_원장_줄을_되돌리면_까닭을_알리고_제안_줄이_안_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "거절 대상")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{a.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	forgetHuman(t, s, a.ID)
	n := countOf(t, s, `SELECT COUNT(*) FROM proposals`)
	_, err = s.RevertChange(res.LedgerID, Who{}, "되돌려")
	if !errors.Is(err, ErrLedgerForgotten) || !strings.Contains(err.Error(), a.ID) {
		t.Fatalf("err = %v", err)
	}
	if got := countOf(t, s, `SELECT COUNT(*) FROM proposals`); got != n {
		t.Fatalf("제안 줄 %d → %d", n, got)
	}
}

func TestForgetAudit_관찰_대상으로만_담긴_원장_줄은_되돌릴_수_있다(t *testing.T) {
	s, _ := newTestStore(t)
	m := candidate(t, s, "관찰만 하는 기억")
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("옛 지침\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: "지침 고치기: 관찰만 하는 기억", MemoryIDs: []string{m.ID}, Payload: Payload{Path: path, Content: "새 지침\n"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := s.LedgerEntry(res.LedgerID); !slices.Equal(e.Observe, []string{m.ID}) || slices.Contains(e.Targets, m.ID) {
		t.Fatalf("준비가 어긋났습니다: targets=%v observe=%v", e.Targets, e.Observe)
	}
	forgetHuman(t, s, m.ID)

	e, _ := s.LedgerEntry(res.LedgerID)
	if e.Change != fmt.Sprintf(forgetLedgerChange, m.ID) || len(e.Before.Forgotten) != 0 {
		t.Fatalf("change=%q forgotten=%v", e.Change, e.Before.Forgotten)
	}
	if _, err := s.RevertChange(res.LedgerID, Who{}, "되돌려"); err != nil {
		t.Fatalf("RevertChange = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "옛 지침\n" {
		t.Fatalf("파일 = %q", got)
	}
}

func TestForgetAudit_하루_정리는_끝까지_돈다(t *testing.T) {
	s, c := newTestStore(t)
	a := candidate(t, s, "정리 대상")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{a.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE ledger SET verdict=? WHERE id=?`, VerdictHarmful, res.LedgerID); err != nil {
		t.Fatal(err)
	}
	// 30일 지난 저장 재시도 줄: Tidy의 마지막 단계가 지우면 뒤 단계까지 돈 것이다.
	if _, err := s.DB().Exec(`INSERT INTO save_requests(key, args_hash, memory_id, action, at) VALUES('k','h','x','save','1')`); err != nil {
		t.Fatal(err)
	}
	forgetHuman(t, s, a.ID)
	c.t = c.t.Add(time.Hour)

	r := s.Tidy(TidyOptions{})
	if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], res.LedgerID) {
		t.Fatalf("Errors = %v", r.Errors)
	}
	if n := countOf(t, s, `SELECT COUNT(*) FROM save_requests`); n != 0 {
		t.Fatalf("뒤 단계가 돌지 않았습니다: save_requests %d", n)
	}
	if again := s.Tidy(TidyOptions{}); len(again.Errors) != 0 {
		t.Fatalf("같은 줄 오류가 되풀이됩니다: %v", again.Errors)
	}
}

func TestForgetAudit_여러_기억에_걸친_제안은_남은_기억의_감사_항목이_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "함께 비밀 제목")
	b := candidate(t, s, "함께 남는 제목")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalLink, Title: "잇기: 함께 비밀 제목", MemoryIDs: []string{a.ID, b.ID}, Payload: Payload{Note: "같은 결정"}}))
	if err != nil {
		t.Fatal(err)
	}
	forgetHuman(t, s, a.ID)

	audit, err := s.Audit(AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ch := range audit.Changes {
		if ch.LedgerID == res.LedgerID {
			found = slices.Contains(ch.Targets, b.ID) && !strings.Contains(ch.Title, "함께 비밀 제목")
		}
	}
	if !found {
		t.Fatalf("남은 기억의 감사 항목이 없습니다: %+v", audit.Changes)
	}
	if got := countOf(t, s, `SELECT COUNT(*) FROM proposals WHERE id=? AND instr(memory_ids, ?)>0 AND instr(memory_ids, ?)>0`, res.Proposal.ID, a.ID, b.ID); got != 1 {
		t.Fatal("제안의 memory_ids에서 id가 사라졌습니다")
	}
}

func TestForgetAudit_대기_중인_제안은_닫는다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "대기 비밀 제목")
	b := candidate(t, s, "대기 다른 제목")
	pa, err := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정: 대기 비밀 제목", MemoryIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정: 대기 다른 제목", MemoryIDs: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	forgetHuman(t, s, a.ID)

	got, err := s.GetProposal(pa.ID)
	if err != nil || got.Status != StatusDropped || got.Title != fmt.Sprintf(forgetProposalTitle, a.ID) {
		t.Fatalf("pa = %+v %v", got, err)
	}
	if other, _ := s.GetProposal(pb.ID); other.Status != StatusPending || other.Title != "확정: 대기 다른 제목" {
		t.Fatalf("다른 제안이 건드려졌습니다: %+v", other)
	}
}
