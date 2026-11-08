package memory

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// AU-6(ONID-131) 증거 확정: 사용자 인용 보호, 증거 ①②③, 자동 변경의 원장과 되돌리기,
// 어긋남 표시, 반복 교정 집계, 감사 목록.

func quoted(t *testing.T, s *Store, title string) Memory {
	t.Helper()
	m, err := s.Save(SaveInput{Kind: KindRule, Title: title, Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: title + " 해"}})
	if err != nil || m.State != StateConfirmed {
		t.Fatalf("quoted %+v %v", m, err)
	}
	return m
}

func ledgerOf(t *testing.T, s *Store, proposalID string) LedgerEntry {
	t.Helper()
	all, err := s.Ledger(LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.ProposalID == proposalID {
			return e
		}
	}
	t.Fatalf("원장에 %s 줄이 없습니다", proposalID)
	return LedgerEntry{}
}

func auto(in AutoInput) AutoInput {
	in.Who = Who{Actor: ActorAutomation}
	return in
}

// AC-1: 사용자 인용 기억을 자동화가 고치려 하면 거절된다.
func TestAC1_사용자_인용_기억은_자동화가_고치지_못한다(t *testing.T) {
	s, _ := newTestStore(t)
	rule := quoted(t, s, "답은 한글 존댓말로")
	other := quoted(t, s, "커밋은 경로를 명시해")
	cand := candidate(t, s, "AI가 배운 것")
	title := "고친 제목"

	for _, actor := range []string{ActorAI, ActorAutomation} {
		if _, err := s.Update(UpdateInput{ID: rule.ID, Title: &title, Who: Who{Actor: actor}}); !errors.Is(err, ErrProtected) {
			t.Fatalf("%s 고치기: %v", actor, err)
		}
		stale := StateStale
		if _, err := s.Update(UpdateInput{ID: rule.ID, State: &stale, Who: Who{Actor: actor}}); !errors.Is(err, ErrProtected) {
			t.Fatalf("%s 낡음: %v", actor, err)
		}
		if _, err := s.SupersedeAs(rule.ID, cand.ID, "대체", Who{Actor: actor}); !errors.Is(err, ErrProtected) {
			t.Fatalf("%s 대체: %v", actor, err)
		}
	}
	cases := []AutoInput{
		{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{rule.ID}},
		{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{rule.ID, cand.ID}, Payload: Payload{MergedTitle: "합친 것", MergedBody: "본문"}},
		{Kind: ProposalSupersede, Title: "대체", MemoryIDs: []string{rule.ID, cand.ID}},
		{Kind: ProposalLink, Title: "연결", MemoryIDs: []string{rule.ID, other.ID}, Payload: Payload{Note: "같은 결정"}},
		{Kind: ProposalRetag, Title: "태그", MemoryIDs: []string{rule.ID}, Payload: Payload{ToTags: []string{"말투"}}},
	}
	for _, c := range cases {
		if _, err := s.AutoApply(auto(c)); !errors.Is(err, ErrProtected) {
			t.Fatalf("%s: %v", c.Kind, err)
		}
	}
	got, _ := s.Get(rule.ID)
	if got.Version != rule.Version || got.State != StateConfirmed || got.Title != rule.Title || len(got.Links) != 0 || len(got.Tags) != 0 {
		t.Fatalf("보호된 기억이 바뀌었습니다: %+v", got)
	}
	if n := len(mustLedger(t, s)); n != 0 {
		t.Fatalf("거절된 자동 변경이 원장에 %d줄 남았습니다", n)
	}

	// 사용자 말을 함께 주면 허용되고, 그 말이 새 출처로 남는다.
	m, err := s.Update(UpdateInput{ID: rule.ID, Title: &title, Who: Who{Actor: ActorAI, UserQuote: "제목을 고친 제목으로 바꿔", Ref: "claude:s1"}})
	if err != nil || m.Title != title {
		t.Fatalf("사용자 말로 고치기: %+v %v", m, err)
	}
	if !slices.ContainsFunc(m.Sources, func(src Source) bool {
		return src.Kind == SourceQuote && src.Excerpt == "제목을 고친 제목으로 바꿔"
	}) {
		t.Fatalf("사용자 말 출처가 없습니다: %+v", m.Sources)
	}
	// 사람(user)은 그대로 고친다.
	body := "사람이 고친 본문"
	if _, err := s.Update(UpdateInput{ID: rule.ID, Body: &body}); err != nil {
		t.Fatal(err)
	}
}

func TestAC1_AI는_사용자_말_없이_후보를_확정하지_못한다(t *testing.T) {
	s, _ := newTestStore(t)
	cand := candidate(t, s, "후보")
	confirmed := StateConfirmed
	if _, err := s.Update(UpdateInput{ID: cand.ID, State: &confirmed, Who: Who{Actor: ActorAI}}); !errors.Is(err, ErrConfirmNeedsWords) {
		t.Fatalf("AI 확정: %v", err)
	}
	m, err := s.Update(UpdateInput{ID: cand.ID, State: &confirmed, Who: Who{Actor: ActorAI, UserQuote: "그거 맞아, 기억해"}})
	if err != nil || m.State != StateConfirmed {
		t.Fatalf("사용자 말로 확정: %+v %v", m, err)
	}
}

func TestAC1_자동화_태그_묶기는_사용자_인용_기억을_건너뛴다(t *testing.T) {
	s, _ := newTestStore(t)
	rule, _ := s.Save(SaveInput{Kind: KindRule, Title: "보호 규칙", Tags: []string{"ui"}, Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: "보호 규칙이야"}})
	learned := taggedMemory(t, s, "배운 것", "ui")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalRetag, Title: "UI 태그 묶기", Payload: Payload{FromTags: []string{"ui"}, ToTags: []string{"화면"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Changed, []string{learned.ID}) {
		t.Fatalf("changed = %v", res.Changed)
	}
	if got, _ := s.Get(rule.ID); !slices.Equal(got.Tags, []string{"ui"}) {
		t.Fatalf("보호된 기억 태그가 바뀌었습니다: %v", got.Tags)
	}
}

func mustLedger(t *testing.T, s *Store) []LedgerEntry {
	t.Helper()
	all, err := s.Ledger(LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// AC-2 ①: 서로 다른 날 다시 나오면 확정되고 원장에 남으며, 되돌리면 후보로 돌아온다.
func TestAC2_다른_날_다시_나온_후보는_확정되고_원장에_남고_되돌리면_후보로(t *testing.T) {
	s, c := newTestStore(t)
	in := SaveInput{Kind: KindLesson, Title: "codex exec는 stdin을 닫는다", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "claude:a"}}
	first, _ := s.Save(in)
	c.t = c.t.Add(24 * time.Hour)
	again, err := s.Save(in)
	if err != nil || again.State != StateConfirmed {
		t.Fatalf("다음 날: %+v %v", again, err)
	}
	all := mustLedger(t, s)
	if len(all) != 1 || all[0].ProposalKind != ProposalConfirm || !slices.Equal(all[0].Targets, []string{first.ID}) {
		t.Fatalf("원장 = %+v", all)
	}
	p, err := s.GetProposal(all[0].ProposalID)
	if err != nil || p.Status != StatusAuto || p.Payload.Evidence != EvidenceSeenDays || p.Payload.Actor != ActorAutomation {
		t.Fatalf("자동 변경 기록 = %+v %v", p, err)
	}

	res, err := s.RevertChange(all[0].ID, Who{}, "틀린 확정")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(first.ID); got.State != StateCandidate {
		t.Fatalf("되돌린 뒤 상태 %s", got.State)
	}
	if e, _ := s.LedgerEntry(all[0].ID); e.RevertedBy != res.LedgerID {
		t.Fatalf("reverted_by = %q, revert line %q", e.RevertedBy, res.LedgerID)
	}
	// 사람이 되돌린 자동 확정은 다시 자동으로 확정하지 않는다.
	c.t = c.t.Add(24 * time.Hour)
	third, _ := s.Save(in)
	if third.State != StateCandidate {
		t.Fatalf("되돌린 기억이 다시 확정됐습니다: %s", third.State)
	}
	if n, err := s.ConfirmCandidates(); err != nil || n != 0 {
		t.Fatalf("sweep %d %v", n, err)
	}
}

// AC-2 ②: 회상돼 따랐고 검증을 통과했으며 교정이 없었던 세션이 기준 횟수 이상이면 확정.
func TestAC2_따르고_검증_통과한_세션이_기준_이상이면_확정(t *testing.T) {
	s, c := newTestStore(t)
	s.Policy.FollowedVerified = 2
	m := candidate(t, s, "회상된 교훈")
	use := func(session, verdict string, verified bool) {
		t.Helper()
		if err := s.RecordUsage(UsageInput{MemoryID: m.ID, Session: session, Verdict: verdict, Verified: verified}); err != nil {
			t.Fatal(err)
		}
	}
	use("s1", UsageFollowed, true)
	use("s2", UsageFollowed, false) // 검증이 없는 세션은 세지 않는다
	// 교정이 있었던 세션은 세지 않는다(회고는 교정을 사용 판정보다 먼저 남긴다).
	if err := s.RecordCorrection(CorrectionInput{Quote: "그거 아니야", Session: "s3", TargetKind: TargetKindMemory, Target: m.ID}); err != nil {
		t.Fatal(err)
	}
	use("s3", UsageFollowed, true)
	use("s5", UsageViolated, false)
	use("s5", UsageFollowed, true) // 같은 세션에 어김이 있으면 세지 않는다
	if got, _ := s.Get(m.ID); got.State != StateCandidate {
		t.Fatalf("기준 전인데 확정: %s", got.State)
	}
	c.t = c.t.Add(time.Hour)
	use("s4", UsageFollowed, true)
	got, _ := s.Get(m.ID)
	if got.State != StateConfirmed {
		t.Fatalf("기준을 넘었는데 %s", got.State)
	}
	e := mustLedger(t, s)[0]
	if p, _ := s.GetProposal(e.ProposalID); p.Payload.Evidence != EvidenceFollowedVerified {
		t.Fatalf("evidence = %q", p.Payload.Evidence)
	}
}

// AC-2 ③: 작업 검증이 직접 확인한 프로젝트 사실은 바로 확정된다(원장에 남는다).
func TestAC2_작업_검증이_확인한_프로젝트_사실은_확정(t *testing.T) {
	s, _ := newTestStore(t)
	m, err := s.Save(SaveInput{Kind: KindFact, Title: "이 저장소 시험은 go test ./...", Scope: "project:github.com/o/r",
		Source: Source{Kind: SourceVerification, Ref: "work:w-1", Excerpt: "go test ./... ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != StateConfirmed {
		t.Fatalf("state = %s", m.State)
	}
	if e := mustLedger(t, s); len(e) != 1 {
		t.Fatalf("원장 %d줄", len(e))
	}
	// 전역 범위나 사실이 아닌 종류는 검증 출처만으로 확정하지 않는다.
	g, _ := s.Save(SaveInput{Kind: KindLesson, Title: "검증된 교훈", Scope: "project:github.com/o/r", Source: Source{Kind: SourceVerification, Ref: "work:w-1"}})
	if g.State != StateCandidate {
		t.Fatalf("교훈 state = %s", g.State)
	}
}

// AC-5: 사용자 규칙과 어긋난다고 표시된 후보는 자동 확정하지 않고 감사 목록에 보인다.
func TestAC5_어긋남_표시된_후보는_자동_확정하지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	rule := quoted(t, s, "배포는 직전 승인")
	in := SaveInput{Kind: KindLesson, Title: "배포는 묻지 않고 한다", Scope: ScopeGlobal, Source: Source{Kind: SourceSession}}
	m, _ := s.Save(in)
	f, err := s.FlagConflict(m.ID, rule.ID, "사용자 규칙과 반대", Who{Actor: ActorAutomation})
	if err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24 * time.Hour)
	if again, _ := s.Save(in); again.State != StateCandidate {
		t.Fatalf("어긋난 후보가 확정됐습니다")
	}
	a, err := s.Audit(AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Conflicts) != 1 || a.Conflicts[0].ID != f.ID || a.Conflicts[0].MemoryID != m.ID || a.Conflicts[0].OtherID != rule.ID {
		t.Fatalf("conflicts = %+v", a.Conflicts)
	}
	// 사람이 표시를 풀면 다음 증거 확인에서 확정된다.
	if err := s.ResolveFlag(f.ID, "dismissed", Who{}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ConfirmCandidates(); err != nil || n != 1 {
		t.Fatalf("sweep %d %v", n, err)
	}
	if a, _ := s.Audit(AuditFilter{}); len(a.Conflicts) != 0 {
		t.Fatalf("풀린 표시가 남았습니다: %+v", a.Conflicts)
	}
}

// AC-3: 반복 교정이 기준을 넘으면 표시되고, 스킬·역할이면 하네스 개선 후보로 읽힌다.
func TestAC3_반복_교정이_기준을_넘으면_표시되고_스킬은_하네스_개선_후보(t *testing.T) {
	s, c := newTestStore(t)
	s.Policy.CorrectionSessions = 3
	m := quoted(t, s, "모든 답변 한글")
	add := func(session, kind, target string) {
		t.Helper()
		if err := s.RecordCorrection(CorrectionInput{Quote: "한글로 하라고", Session: session, TargetKind: kind, Target: target}); err != nil {
			t.Fatal(err)
		}
	}
	add("s1", TargetKindMemory, m.ID)
	add("s1", TargetKindMemory, m.ID) // 같은 세션은 한 번
	add("s2", TargetKindMemory, m.ID)
	add("s1", TargetKindSkill, "orbit-code:tdd")
	add("s2", TargetKindSkill, "orbit-code:tdd")
	if over, _ := s.RepeatedCorrections(false); len(over) != 0 {
		t.Fatalf("기준 전인데 표시: %+v", over)
	}
	c.t = c.t.Add(time.Hour)
	add("s3", TargetKindMemory, m.ID)
	add("s3", TargetKindSkill, "orbit-code:tdd")
	add("s4", TargetKindRole, "orbit-code:reviewer")
	over, err := s.RepeatedCorrections(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(over) != 2 {
		t.Fatalf("over = %+v", over)
	}
	byTarget := map[string]RepeatedCorrection{}
	for _, r := range over {
		byTarget[r.Target] = r
	}
	if r := byTarget[m.ID]; r.Sessions != 3 || r.Harness || r.Title != m.Title || !r.Over {
		t.Fatalf("memory = %+v", r)
	}
	if r := byTarget["orbit-code:tdd"]; r.Sessions != 3 || !r.Harness || r.TargetKind != TargetKindSkill {
		t.Fatalf("skill = %+v", r)
	}
	// 확인함을 누르면 그 뒤 교정만 다시 센다.
	if err := s.Acknowledge(byTarget[m.ID].Key, Who{}); err != nil {
		t.Fatal(err)
	}
	if over, _ := s.RepeatedCorrections(false); len(over) != 1 || over[0].Target != "orbit-code:tdd" {
		t.Fatalf("확인 뒤 over = %+v", over)
	}
}

// 옛 교정 사건(대상 없이 memoryId만)도 기억 대상으로 센다.
func TestAC3_옛_교정_사건도_기억_대상으로_센다(t *testing.T) {
	s, _ := newTestStore(t)
	s.Policy.CorrectionSessions = 2
	m := quoted(t, s, "옛 규칙")
	for _, sess := range []string{"a", "b"} {
		if err := s.RecordEvent(Event{Kind: EventCorrection, Session: sess}, map[string]any{"quote": "아니야", "memoryId": m.ID}); err != nil {
			t.Fatal(err)
		}
	}
	over, err := s.RepeatedCorrections(false)
	if err != nil || len(over) != 1 || over[0].TargetKind != TargetKindMemory || over[0].Target != m.ID {
		t.Fatalf("over = %+v %v", over, err)
	}
}

// AC-4: 효과 원장이 해로움으로 판정한 자동 변경은 스스로 되돌리고 알린다.
func TestAC4_해로움_판정_자동_변경은_되돌리고_알린다(t *testing.T) {
	s, c := newTestStore(t)
	a := candidate(t, s, "가")
	b := candidate(t, s, "나")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음: 가", MemoryIDs: []string{a.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	userLine := approveEdit(t, s, b.ID, "사람이 고친 나")
	if _, err := s.DB().Exec(`UPDATE ledger SET verdict=? WHERE id IN (?,?)`, VerdictHarmful, res.LedgerID, userLine); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(time.Hour)
	reverted, err := s.RevertHarmfulAuto()
	if err != nil {
		t.Fatal(err)
	}
	if len(reverted) != 1 || reverted[0].LedgerID != res.LedgerID || reverted[0].Error != "" {
		t.Fatalf("reverted = %+v", reverted)
	}
	if got, _ := s.Get(a.ID); got.State != StateCandidate {
		t.Fatalf("되돌린 뒤 %s", got.State)
	}
	if e, _ := s.LedgerEntry(userLine); e.RevertedBy != "" {
		t.Fatal("사람이 승인한 변경은 자동으로 되돌리지 않는다")
	}
	evs, _ := s.Events(EventFilter{FromDay: "2000-01-01", ToDay: "2100-01-01", KindPrefix: EventAutoReverted})
	if len(evs) != 1 {
		t.Fatalf("알림 사건 %d건", len(evs))
	}
	audit, _ := s.Audit(AuditFilter{})
	found := false
	for _, ch := range audit.Changes {
		if ch.LedgerID == res.LedgerID && ch.RevertedBy != "" && ch.AutoReverted {
			found = true
		}
	}
	if !found {
		t.Fatalf("감사 목록에 자동 되돌림이 없습니다: %+v", audit.Changes)
	}
	// 두 번 돌려도 다시 되돌리지 않는다.
	if again, _ := s.RevertHarmfulAuto(); len(again) != 0 {
		t.Fatalf("again = %+v", again)
	}
}

// 자동 변경 종류마다 원장에 남고 되돌릴 수 있다.
func TestAutoApply_정리_변경은_원장에_남고_되돌린다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "가")
	b := candidate(t, s, "나")
	cc := candidate(t, s, "다")

	link, err := s.AutoApply(auto(AutoInput{Kind: ProposalLink, Title: "잇기", MemoryIDs: []string{a.ID, b.ID}, Payload: Payload{Note: "basis: 같은 결정"}}))
	if err != nil || link.Link == "" || link.LedgerID == "" {
		t.Fatalf("link %+v %v", link, err)
	}
	sup, err := s.AutoApply(auto(AutoInput{Kind: ProposalSupersede, Title: "다를 나가 대체", MemoryIDs: []string{cc.ID, b.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(cc.ID); got.State != StateStale || got.SupersededBy != b.ID {
		t.Fatalf("supersede: %+v", got)
	}
	if _, err := s.RevertChange(sup.LedgerID, Who{}, "틀림"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(cc.ID); got.State != StateCandidate || got.SupersededBy != "" {
		t.Fatalf("되돌린 supersede: %+v", got)
	}
	if _, err := s.RevertChange(link.Proposal.ID, Who{}, "틀림"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(a.ID); len(got.Links) != 0 {
		t.Fatalf("되돌린 link가 남았습니다: %+v", got.Links)
	}
	// 증거 없는 자동 확정은 받지 않는다.
	if _, err := s.AutoApply(auto(AutoInput{Kind: ProposalConfirm, Title: "확정", MemoryIDs: []string{a.ID}})); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("증거 없는 확정: %v", err)
	}
	// 파일 반영은 자동 종류가 아니다(실험실이 도움됨으로 판정한 하네스 변경만 따로 적용된다).
	if _, err := s.AutoApply(auto(AutoInput{Kind: ProposalApplyFile, Title: "반영", Payload: Payload{Path: "/x/AGENTS.md", Content: "x"}})); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply_file: %v", err)
	}
	// 자동화는 사람이 승인한 변경을 되돌리지 못한다.
	userLine := approveEdit(t, s, a.ID, "사람이 고친 가")
	if _, err := s.RevertChange(userLine, Who{Actor: ActorAutomation}, "자동"); !errors.Is(err, ErrProtected) {
		t.Fatalf("자동화가 사람 변경을 되돌림: %v", err)
	}
}

// AC-6: 회상에서 후보는 확정 뒤에 온다(memory_find 정렬).
func TestAC6_찾기에서_같은_점수면_확정이_후보보다_앞선다(t *testing.T) {
	s, c := newTestStore(t)
	cand := candidate(t, s, "배포 순서 메모")
	c.t = c.t.Add(time.Hour)
	_ = cand
	conf := quoted(t, s, "배포 순서 규칙")
	c.t = c.t.Add(time.Hour)
	if _, err := s.Update(UpdateInput{ID: cand.ID, Body: ptr("최근 고침")}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Find(FindInput{Query: "배포 순서"})
	if err != nil || len(got) != 2 {
		t.Fatalf("find %+v %v", got, err)
	}
	if got[0].ID != conf.ID {
		t.Fatalf("확정이 먼저가 아닙니다: %s, %s", got[0].Title, got[1].Title)
	}
}

func ptr[T any](v T) *T { return &v }

// AC-8: 스키마 3 → 4 이전은 행을 그대로 두고 새 표를 더한다.
func TestAC8_스키마_3을_4로_올린다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := s.Save(SaveInput{Kind: KindFact, Title: "하나", Scope: ScopeGlobal, Source: Source{Kind: SourceQuote, Excerpt: "하나"}})
	_ = s.RecordEvent(Event{Kind: EventCorrection, Session: "x"}, map[string]any{"memoryId": m.ID})
	if _, err := s.DB().Exec(`DROP TABLE audit_flags; DROP TABLE audit_acks; PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, n int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 4 {
		t.Fatalf("user_version %d %v", v, err)
	}
	for _, table := range []string{"audit_flags", "audit_acks"} {
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
	}
	if c, _ := s.Count(); c != 1 {
		t.Fatalf("기억 수 %d", c)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("사건 수 %d %v", n, err)
	}
	var check string
	if err := s.DB().QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity %q %v", check, err)
	}
}

// 검토 H-1: 사람이 후보로 내린 기억은 자동화가 다시 확정하지 않는다.
func TestReview_사람이_후보로_내린_기억은_다시_자동_확정하지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	in := SaveInput{Kind: KindLesson, Title: "두 날 본 교훈", Scope: ScopeGlobal, Source: Source{Kind: SourceSession}}
	m, _ := s.Save(in)
	c.t = c.t.Add(24 * time.Hour)
	if got, _ := s.Save(in); got.State != StateConfirmed {
		t.Fatalf("state %s", got.State)
	}
	cand := StateCandidate
	if _, err := s.Update(UpdateInput{ID: m.ID, State: &cand, Action: "restore"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ConfirmCandidates(); err != nil || n != 0 {
		t.Fatalf("다시 확정 %d %v", n, err)
	}
	if a, _ := s.Audit(AuditFilter{}); len(a.Conflicts) != 0 {
		t.Fatalf("보류 표시는 어긋남 목록에 보이지 않는다: %+v", a.Conflicts)
	}
	// 사람이 확정하면 보류는 풀린다.
	conf := StateConfirmed
	if got, err := s.Update(UpdateInput{ID: m.ID, State: &conf}); err != nil || got.State != StateConfirmed {
		t.Fatalf("사람 확정 %+v %v", got, err)
	}
}

// 검토 H-2: 저장과 같은 호출로 단 어긋남 표시는 증거 확인보다 먼저 걸린다(같은 제목이 다른 날 다시 나와도).
func TestReview_저장의_어긋남_표시는_확정보다_먼저(t *testing.T) {
	s, c := newTestStore(t)
	rule := quoted(t, s, "배포는 직전 승인")
	in := SaveInput{Kind: KindLesson, Title: "배포는 묻지 않는다", Scope: ScopeGlobal, Source: Source{Kind: SourceSession}}
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(24 * time.Hour)
	in.ConflictsWith, in.Who = []string{rule.ID}, Who{Actor: ActorAutomation}
	got, err := s.Save(in)
	if err != nil || got.State != StateCandidate {
		t.Fatalf("어긋남 표시보다 확정이 먼저였습니다: %+v %v", got, err)
	}
	if fl, _ := s.OpenFlags(); len(fl) != 1 || fl[0].OtherID != rule.ID || fl[0].Actor != ActorAutomation {
		t.Fatalf("flags = %+v", fl)
	}
}

// 검토 M-4: 사용자 말 없는 AI 확정은 확정 전용 오류로 알린다.
func TestReview_AI_확정_거절은_확정_오류로(t *testing.T) {
	s, _ := newTestStore(t)
	cand := candidate(t, s, "후보")
	conf := StateConfirmed
	_, err := s.Update(UpdateInput{ID: cand.ID, State: &conf, Who: Who{Actor: ActorAI}})
	if !errors.Is(err, ErrConfirmNeedsWords) || errors.Is(err, ErrProtected) {
		t.Fatalf("err = %v", err)
	}
}

// 검토 L-2: 자동 대체를 되돌릴 때 바뀌지 않은 쪽(대신하는 기억)이 사용자 인용이어도 막히지 않는다.
func TestReview_대신하는_기억이_인용이어도_자동_대체를_되돌린다(t *testing.T) {
	s, _ := newTestStore(t)
	old := candidate(t, s, "옛 후보")
	rule := quoted(t, s, "사용자 규칙")
	res, err := s.AutoApply(auto(AutoInput{Kind: ProposalSupersede, Title: "대체", MemoryIDs: []string{old.ID, rule.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevertChange(res.LedgerID, Who{Actor: ActorAutomation}, "해로움"); err != nil {
		t.Fatalf("자동 되돌림: %v", err)
	}
	if got, _ := s.Get(old.ID); got.State != StateCandidate {
		t.Fatalf("state %s", got.State)
	}
}

// 검토 Nit: 사람·AI가 되돌린 자동 변경은 「해로움 자동 되돌림」이 아니다.
func TestReview_사람이_되돌린_것은_자동_되돌림이_아니다(t *testing.T) {
	s, _ := newTestStore(t)
	a := candidate(t, s, "가")
	res, _ := s.AutoApply(auto(AutoInput{Kind: ProposalStale, Title: "낡음", MemoryIDs: []string{a.ID}}))
	if _, err := s.RevertChange(res.LedgerID, Who{Actor: ActorAI}, "사용자가 되돌리라 함"); err != nil {
		t.Fatal(err)
	}
	au, _ := s.Audit(AuditFilter{})
	for _, ch := range au.Changes {
		if ch.LedgerID == res.LedgerID && (ch.RevertedBy == "" || ch.AutoReverted) {
			t.Fatalf("change = %+v", ch)
		}
	}
}
