package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 설계 · orbit-mem 2판 18절 「사람 승인 대기열 없애기」: 자동화가 낸 변경은 바로 적용하거나(원장에 남아
// 되돌릴 수 있다) 적용하지 않고 까닭을 돌려준다. 대기 제안(status pending)은 만들지 않는다.

func noPending(t *testing.T, s *Store) {
	t.Helper()
	if n := s.PendingCount(); n != 0 {
		ps, _ := s.Proposals(StatusPending)
		t.Fatalf("대기 제안이 생겼습니다(%d): %+v", n, ps)
	}
}

// Q-1: 증거 없는 확정은 제안을 만들지 않고 후보로 남는다.
func TestQ1_증거_없는_확정은_후보로_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	c := candidate(t, s, "배운 것")
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalConfirm, Title: "확정", MemoryIDs: []string{c.ID}}, Who{Actor: ActorAutomation})
	if err != nil || out.Applied || out.AskUser || out.Reason == "" {
		t.Fatalf("out %+v %v", out, err)
	}
	if got, _ := s.Get(c.ID); got.State != StateCandidate {
		t.Fatalf("state = %s", got.State)
	}
	noPending(t, s)
}

// Q-2: 사용자 말 기억에 닿는 변경은 적용되지 않고 askUser와 까닭을 돌려준다. 사용자 말을 주면 적용된다.
func TestQ2_사용자_말_기억은_적용하지_않고_사용자에게_묻게_한다(t *testing.T) {
	s, _ := newTestStore(t)
	rule := quoted(t, s, "사용자 규칙")
	for _, in := range []ProposalInput{
		{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{rule.ID}},
		{Kind: ProposalEdit, Title: "고치기", MemoryIDs: []string{rule.ID}, Payload: Payload{Title: "새 제목"}},
		{Kind: ProposalReclassify, Title: "재분류", MemoryIDs: []string{rule.ID}, Payload: Payload{Kind: KindDecision}},
	} {
		out, err := s.SubmitChange(in, Who{Actor: ActorAutomation})
		if err != nil || out.Applied || !out.AskUser || !strings.Contains(out.Reason, "userQuote") {
			t.Fatalf("%s: %+v %v", in.Kind, out, err)
		}
	}
	if got, _ := s.Get(rule.ID); got.State != StateConfirmed || got.Title != "사용자 규칙" || got.Kind != KindRule {
		t.Fatalf("보호 기억이 바뀌었습니다: %+v", got)
	}
	noPending(t, s)
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalEdit, Title: "고치기", MemoryIDs: []string{rule.ID}, Payload: Payload{Title: "새 제목"}},
		Who{Actor: ActorAI, UserQuote: "제목을 새 제목으로 바꿔"})
	if err != nil || !out.Applied {
		t.Fatalf("사용자 말과 함께: %+v %v", out, err)
	}
	if got, _ := s.Get(rule.ID); got.Title != "새 제목" {
		t.Fatalf("title = %s", got.Title)
	}
}

// Q-3: 보호 기억이 아닌 고치기·재분류는 바로 적용되고 원장에 남아 되돌릴 수 있다.
func TestQ3_고치기와_재분류는_바로_적용되고_되돌린다(t *testing.T) {
	s, _ := newTestStore(t)
	c := candidate(t, s, "옛 제목")
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalEdit, Title: "고치기", MemoryIDs: []string{c.ID}, Payload: Payload{Title: "새 제목"}}, Who{Actor: ActorAutomation})
	if err != nil || !out.Applied || out.LedgerID == "" {
		t.Fatalf("edit %+v %v", out, err)
	}
	if got, _ := s.Get(c.ID); got.Title != "새 제목" || got.State != StateCandidate {
		t.Fatalf("edit: %+v", got)
	}
	if _, err := s.RevertChange(out.LedgerID, Who{}, "틀림"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(c.ID); got.Title != "옛 제목" {
		t.Fatalf("되돌린 edit: %+v", got)
	}
	out, err = s.SubmitChange(ProposalInput{Kind: ProposalReclassify, Title: "재분류", MemoryIDs: []string{c.ID}, Payload: Payload{Kind: KindFact}}, Who{Actor: ActorAutomation})
	if err != nil || !out.Applied || out.LedgerID == "" {
		t.Fatalf("reclassify %+v %v", out, err)
	}
	if got, _ := s.Get(c.ID); got.Kind != KindFact {
		t.Fatalf("reclassify: %+v", got)
	}
	noPending(t, s)
}

// Q-4: 방향은 받지 않고, 지침 파일 반영은 묻게 하고, 되돌리기는 자동 변경만 한다.
func TestQ4_방향_지침파일_되돌리기(t *testing.T) {
	s, _ := newTestStore(t)
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalDirection, Title: "다음 주엔 시험부터"}, Who{Actor: ActorAutomation})
	if err != nil || out.Applied || out.AskUser || out.Reason == "" {
		t.Fatalf("direction %+v %v", out, err)
	}
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	os.WriteFile(path, []byte("옛 지침\n"), 0o644)
	out, err = s.SubmitChange(ProposalInput{Kind: ProposalApplyFile, Title: "지침 고치기", Payload: Payload{Path: path, Content: "새 지침\n"}}, Who{Actor: ActorAutomation})
	if err != nil || out.Applied || !out.AskUser || out.Reason == "" {
		t.Fatalf("apply_file %+v %v", out, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "옛 지침\n" {
		t.Fatalf("지침 파일이 바뀌었습니다: %q", b)
	}

	c := candidate(t, s, "가")
	stale, err := s.SubmitChange(ProposalInput{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{c.ID}}, Who{Actor: ActorAutomation})
	if err != nil || !stale.Applied {
		t.Fatal(stale, err)
	}
	out, err = s.SubmitChange(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: stale.LedgerID}}, Who{Actor: ActorAutomation})
	if err != nil || !out.Applied {
		t.Fatalf("revert auto %+v %v", out, err)
	}
	if got, _ := s.Get(c.ID); got.State != StateCandidate {
		t.Fatalf("되돌린 낡음: %s", got.State)
	}
	userLine := approveEdit(t, s, c.ID, "사람이 고친 가")
	out, err = s.SubmitChange(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: userLine}}, Who{Actor: ActorAutomation})
	if err != nil || out.Applied || !out.AskUser {
		t.Fatalf("revert human %+v %v", out, err)
	}
	noPending(t, s)
}

// Q-4: 실험실이 도움됨으로 판정한 하네스 변경은 바로 적용되고 원장에 남는다.
func TestQ4_실험실_도움됨은_자동_적용(t *testing.T) {
	s, _ := newTestStore(t)
	path := filepath.Join(t.TempDir(), "skills", "plain-writing", "SKILL.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("옛 스킬\n"), 0o644)
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalApplyFile, Title: "스킬 고치기", Lab: true, Payload: Payload{Path: path, Content: "새 스킬\n"}}, Who{Actor: ActorAutomation})
	if err != nil || out.Applied || out.Proposal.Status != StatusLab {
		t.Fatalf("lab %+v %v", out, err)
	}
	if err := s.ReleaseLabProposal(out.Proposal.ID, "실험 x-1: 도움됨"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "새 스킬\n" {
		t.Fatalf("파일 %q", b)
	}
	got, _ := s.GetProposal(out.Proposal.ID)
	if got.Status != StatusAuto || !strings.Contains(got.Body, "실험 x-1: 도움됨") {
		t.Fatalf("proposal %+v", got)
	}
	e := ledgerOf(t, s, got.ID)
	if _, err := s.RevertChange(e.ID, Who{}, "되돌림"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "옛 스킬\n" {
		t.Fatalf("되돌린 파일 %q", b)
	}
	noPending(t, s)
}

// Q-5: 재검토일이 지나도 대기 제안을 만들지 않는다. 날 지난 휘발 기억은 자동 낡음, 사용자 말이면 그대로 둔다.
func TestQ5_재검토와_휘발은_대기_제안을_만들지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	v, _ := s.Save(SaveInput{Kind: KindFact, Title: "이번 주 배포 동결", Retention: RetentionVolatile, TTLDays: 1, Source: Source{Kind: SourceSession}})
	d, _ := s.Save(SaveInput{Kind: KindRule, Title: "릴리스는 지시가 있을 때만", Topic: "릴리스", Retention: RetentionDurable, ReviewAfterDays: 5,
		Source: Source{Kind: SourceQuote, Excerpt: "릴리스는 내가 지시할게"}})
	vq, _ := s.Save(SaveInput{Kind: KindRule, Title: "오늘만 영어로", Topic: "답 언어", Retention: RetentionVolatile, TTLDays: 1,
		Source: Source{Kind: SourceQuote, Excerpt: "오늘만 영어로 답해"}})
	c.t = c.t.Add(6 * 24 * time.Hour)
	got, err := s.ReviewDue()
	if err != nil {
		t.Fatal(err)
	}
	if got.Expired != 1 || got.Held != 1 {
		t.Fatalf("got %+v", got)
	}
	if m, _ := s.Get(v.ID); m.State != StateStale {
		t.Fatalf("volatile = %s", m.State)
	}
	for _, id := range []string{d.ID, vq.ID} {
		if m, _ := s.Get(id); m.State != StateConfirmed {
			t.Fatalf("%s = %s", id, m.State)
		}
	}
	noPending(t, s)
}

// Q-7: 기한 동안 쓰지 않은 비보호 후보는 자동 낡음 + 원장. 쓴 후보·보호 후보·확정은 그대로. 0이면 끈다.
func TestQ7_오래_쓰지_않은_후보는_낡음(t *testing.T) {
	s, c := newTestStore(t)
	start := c.t
	injectAt(t, s, "s0", start, "m-none") // 사건 기록이 기한보다 오래됐다
	old := candidate(t, s, "안 쓴 후보")
	used := candidate(t, s, "쓴 후보")
	conf := quoted(t, s, "사용자 규칙")
	prot, _ := s.Save(SaveInput{Kind: KindLesson, Title: "사용자 말 후보", Source: Source{Kind: SourceSession}})
	if err := s.AddSource(prot.ID, Source{Kind: SourceQuote, Excerpt: "이건 사용자 말"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(prot.ID); got.State != StateCandidate {
		t.Skipf("인용 출처를 더하면 확정되는 판: %s", got.State)
	}
	for d := 10; d < 17; d++ { // 기한 안에 이 PC를 여러 날 썼다
		injectAt(t, s, "busy", start.Add(time.Duration(d)*24*time.Hour), "m-none")
	}
	c.t = start.Add(20 * 24 * time.Hour)
	injectAt(t, s, "s1", c.t, used.ID)
	c.t = start.Add(29 * 24 * time.Hour)

	if n, err := s.ExpireCandidates(0); err != nil || n != 0 {
		t.Fatalf("꺼짐: %d %v", n, err)
	}
	n, err := s.ExpireCandidates(28)
	if err != nil || n != 1 {
		t.Fatalf("n = %d %v", n, err)
	}
	if got, _ := s.Get(old.ID); got.State != StateStale {
		t.Fatalf("old = %s", got.State)
	}
	for _, id := range []string{used.ID, prot.ID} {
		if got, _ := s.Get(id); got.State != StateCandidate {
			t.Fatalf("%s = %s", id, got.State)
		}
	}
	if got, _ := s.Get(conf.ID); got.State != StateConfirmed {
		t.Fatalf("conf = %s", got.State)
	}
	all, _ := s.Proposals(StatusAuto)
	if len(all) != 1 || all[0].Kind != ProposalStale || all[0].MemoryIDs[0] != old.ID {
		t.Fatalf("auto = %+v", all)
	}
	e := ledgerOf(t, s, all[0].ID)
	if _, err := s.RevertChange(e.ID, Who{}, "아직 맞음"); err != nil {
		t.Fatal(err)
	}
	// 되돌린 자동 변경은 다시 하지 않는다.
	if n, err := s.ExpireCandidates(28); err != nil || n != 0 {
		t.Fatalf("again = %d %v", n, err)
	}
	noPending(t, s)
}

// Q-7: 사건 기록이 기한보다 짧으면(새 설치) 「안 썼다」고 하지 않는다.
func TestQ7_사건_기록이_짧으면_하지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	candidate(t, s, "후보")
	injectAt(t, s, "s0", c.t.Add(25*24*time.Hour), "m-none")
	c.t = c.t.Add(30 * 24 * time.Hour)
	if n, err := s.ExpireCandidates(28); err != nil || n != 0 {
		t.Fatalf("n = %d %v", n, err)
	}
}

// Q-8: 쌓인 대기 제안을 모두 닫는다. 자동 규칙에 맞으면 적용하고(settled), 아니면 메모를 남겨 닫는다(dropped). 행은 남는다.
func TestQ8_쌓인_대기를_정리한다(t *testing.T) {
	s, _ := newTestStore(t)
	rule := quoted(t, s, "사용자 규칙")
	a := candidate(t, s, "가")
	b := candidate(t, s, "나")
	pConfirm, _ := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "가 확정", MemoryIDs: []string{a.ID}})
	pLink, _ := s.Propose(ProposalInput{Kind: ProposalLink, Title: "잇기", MemoryIDs: []string{a.ID, b.ID}, Payload: Payload{Note: "basis: 같은 결정"}})
	pStale, _ := s.Propose(ProposalInput{Kind: ProposalStale, Title: "규칙 낡음", MemoryIDs: []string{rule.ID}})
	pDir, _ := s.Propose(ProposalInput{Kind: ProposalDirection, Title: "방향"})
	pRe, _ := s.Propose(ProposalInput{Kind: ProposalReclassify, Title: "나 재분류", MemoryIDs: []string{b.ID}, Payload: Payload{Kind: KindFact}})
	before, _ := s.Proposals("")

	res, err := s.DrainPending()
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 2 || res.Dropped != 3 || len(res.Items) != 5 {
		t.Fatalf("res %+v", res)
	}
	noPending(t, s)
	want := map[string]string{pConfirm.ID: StatusDropped, pLink.ID: StatusSettled, pStale.ID: StatusDropped, pDir.ID: StatusDropped, pRe.ID: StatusSettled}
	for id, st := range want {
		p, _ := s.GetProposal(id)
		if p.Status != st || !strings.HasPrefix(p.Note, "대기열 정리") {
			t.Fatalf("%s(%s): %s %q", id, p.Kind, p.Status, p.Note)
		}
	}
	if got, _ := s.Get(a.ID); got.State != StateCandidate || len(got.Links) != 1 {
		t.Fatalf("a %+v", got)
	}
	if got, _ := s.Get(b.ID); got.Kind != KindFact {
		t.Fatalf("b %+v", got)
	}
	if got, _ := s.Get(rule.ID); got.State != StateConfirmed {
		t.Fatalf("rule %+v", got)
	}
	after, _ := s.Proposals("")
	if len(after) != len(before)+2 { // 옛 행은 남고, 적용한 둘은 자동 변경 행이 새로 생긴다
		t.Fatalf("rows %d → %d", len(before), len(after))
	}
	again, err := s.DrainPending()
	if err != nil || len(again.Items) != 0 {
		t.Fatalf("again %+v %v", again, err)
	}
}

// Q-8: 태그 묶기는 사용자 말을 담은 기억만 건너뛰고 나머지에 적용한다.
func TestQ8_태그_묶기는_보호_기억만_건너뛴다(t *testing.T) {
	s, _ := newTestStore(t)
	rule, _ := s.Save(SaveInput{Kind: KindRule, Title: "시험 먼저", Topic: "시험 순서", Tags: []string{"testing"}, Source: Source{Kind: SourceQuote, Excerpt: "시험 먼저 써"}})
	a, _ := s.Save(SaveInput{Kind: KindLesson, Title: "가", Tags: []string{"testing"}, Source: Source{Kind: SourceSession}})
	p, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "시험 태그 묶기", Payload: Payload{FromTags: []string{"testing"}, ToTags: []string{"test"}}})
	if err != nil || len(p.MemoryIDs) != 2 {
		t.Fatalf("retag %+v %v", p, err)
	}
	res, err := s.DrainPending()
	if err != nil || res.Applied != 1 {
		t.Fatalf("res %+v %v", res, err)
	}
	if got, _ := s.Get(a.ID); !hasTagKey(got.Tags, "test") {
		t.Fatalf("a tags %v", got.Tags)
	}
	if got, _ := s.Get(rule.ID); !hasTagKey(got.Tags, "testing") {
		t.Fatalf("rule tags %v", got.Tags)
	}
}

// Q-2: 여러 기억을 낡음으로 할 때 사용자 말을 담은 기억만 빼고 나머지는 적용한다.
func TestQ2_여러_기억_낡음은_보호_기억만_뺀다(t *testing.T) {
	s, _ := newTestStore(t)
	rule := quoted(t, s, "사용자 규칙")
	a := candidate(t, s, "가")
	b := candidate(t, s, "나")
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalStale, Title: "낡음 셋", MemoryIDs: []string{a.ID, rule.ID, b.ID}}, Who{Actor: ActorAutomation})
	if err != nil || !out.Applied || !out.AskUser || !strings.Contains(out.Reason, rule.ID) {
		t.Fatalf("out %+v %v", out, err)
	}
	for _, id := range []string{a.ID, b.ID} {
		if got, _ := s.Get(id); got.State != StateStale {
			t.Fatalf("%s = %s", id, got.State)
		}
	}
	if got, _ := s.Get(rule.ID); got.State != StateConfirmed {
		t.Fatalf("rule = %s", got.State)
	}
	if len(out.Proposal.MemoryIDs) != 2 {
		t.Fatalf("applied ids %v", out.Proposal.MemoryIDs)
	}
	noPending(t, s)
}

// 검토 지적 1: 미리 보기는 파일을 쓰는 되돌리기를 돌리지 않는다(사본 DB라도 파일은 실제 것이다).
func TestQ8_미리_보기는_파일을_되돌리지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	path := filepath.Join(t.TempDir(), "skills", "x", "SKILL.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("옛\n"), 0o644)
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalApplyFile, Title: "스킬", Lab: true, Payload: Payload{Path: path, Content: "새\n"}}, Who{Actor: ActorAutomation})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseLabProposal(out.Proposal.ID, "도움됨"); err != nil {
		t.Fatal(err)
	}
	line := ledgerOf(t, s, out.Proposal.ID)
	rp, err := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: line.ID}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.DrainPendingWith(DrainOptions{Preview: true})
	if err != nil || len(res.Items) != 1 || res.Items[0].Status != DrainSkipped {
		t.Fatalf("res %+v %v", res, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "새\n" {
		t.Fatalf("미리 보기가 파일을 바꿨습니다: %q", b)
	}
	if p, _ := s.GetProposal(rp.ID); p.Status != StatusPending {
		t.Fatalf("미리 보기에서 건너뛴 것은 대기로 남는다: %+v", p)
	}
}

// 검토 지적 9: 대기 제안을 낸 뒤 바뀐 기억은 옛 고치기로 덮지 않는다.
func TestQ8_그_뒤_바뀐_기억은_옛_고치기로_덮지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	m := candidate(t, s, "옛 제목")
	p, _ := s.Propose(ProposalInput{Kind: ProposalEdit, Title: "고치기", MemoryIDs: []string{m.ID}, Payload: Payload{Title: "제안 제목"}})
	c.t = c.t.Add(time.Hour)
	title := "사람이 고친 제목"
	if _, err := s.Update(UpdateInput{ID: m.ID, Title: &title, Reason: "손으로"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.DrainPending()
	if err != nil || res.Dropped != 1 {
		t.Fatalf("res %+v %v", res, err)
	}
	if got, _ := s.Get(m.ID); got.Title != title {
		t.Fatalf("덮었습니다: %s", got.Title)
	}
	if got, _ := s.GetProposal(p.ID); got.Status != StatusDropped {
		t.Fatalf("p %+v", got)
	}
}

// 검토 지적 5: 실험실이 판정한 뒤 파일이 바뀌었으면 덮지 않고 실험실 대기로 남긴다(다시 실험할 수 있다).
func TestQ4_실험실_도움됨이라도_파일이_바뀌었으면_대기로_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	path := filepath.Join(t.TempDir(), "skills", "x", "SKILL.md")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("옛\n"), 0o644)
	out, _ := s.SubmitChange(ProposalInput{Kind: ProposalApplyFile, Title: "스킬", Lab: true, Payload: Payload{Path: path, Content: "새\n"}}, Who{Actor: ActorAutomation})
	os.WriteFile(path, []byte("그 사이 고침\n"), 0o644)
	if err := s.ReleaseLabProposal(out.Proposal.ID, "도움됨"); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "그 사이 고침\n" {
		t.Fatalf("덮었습니다: %q", b)
	}
	if p, _ := s.GetProposal(out.Proposal.ID); p.Status != StatusLab {
		t.Fatalf("p %+v", p)
	}
}

// 검토 지적 3: 이 PC를 기한 동안 거의 쓰지 않았으면(쉬다 돌아옴) 후보를 낡음으로 하지 않는다.
func TestQ7_쉬다_돌아오면_하지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	start := c.t
	injectAt(t, s, "s0", start, "m-none")
	candidate(t, s, "후보")
	c.t = start.Add(60 * 24 * time.Hour)
	injectAt(t, s, "s1", c.t, "m-none") // 오늘 하루만 썼다
	if n, err := s.ExpireCandidates(28); err != nil || n != 0 {
		t.Fatalf("n = %d %v", n, err)
	}
}

// 검토 지적 6: 자동 변경을 되돌리려는데 그 기억이 그 사이 사용자 말을 담게 됐으면 「사람이 한 변경」이 아니라 사용자 말 까닭을 준다.
func TestQ4_자동_변경_되돌리기가_보호_기억에_막히면_사용자_말_까닭(t *testing.T) {
	s, _ := newTestStore(t)
	c := candidate(t, s, "가")
	stale, err := s.SubmitChange(ProposalInput{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{c.ID}}, Who{Actor: ActorAutomation})
	if err != nil || !stale.Applied {
		t.Fatal(stale, err)
	}
	if err := s.AddSource(c.ID, Source{Kind: SourceQuote, Excerpt: "가는 맞아"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.SubmitChange(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: stale.LedgerID}}, Who{Actor: ActorAutomation})
	if err != nil || out.Applied || !out.AskUser || !strings.Contains(out.Reason, c.ID) || !strings.Contains(out.Reason, "userQuote") {
		t.Fatalf("out %+v %v", out, err)
	}
}
