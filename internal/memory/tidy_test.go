package memory

import (
	"testing"
	"time"
)

// 기획 · orbit-mem 4.3 H3: 하루 한 번 표시는 meta에 그날을 먼저 쓰는 쪽이 이긴다. 다음 날은 다시 이긴다.
func TestClaimDay_같은_날은_한_번만_이긴다(t *testing.T) {
	s, _ := newTestStore(t)
	if ok, err := s.ClaimDay(MetaTidyDay, "2026-11-08"); err != nil || !ok {
		t.Fatalf("첫 번째 = %v %v", ok, err)
	}
	if ok, err := s.ClaimDay(MetaTidyDay, "2026-11-08"); err != nil || ok {
		t.Fatalf("같은 날 두 번째 = %v %v", ok, err)
	}
	if got := s.Meta(MetaTidyDay); got != "2026-11-08" {
		t.Fatalf("meta = %q", got)
	}
	if ok, err := s.ClaimDay(MetaTidyDay, "2026-11-09"); err != nil || !ok {
		t.Fatalf("다음 날 = %v %v", ok, err)
	}
}

// 두 세션이 동시에 열려도 그날 정리는 한 번만 이긴다.
func TestClaimDay_동시에_불러도_하나만_이긴다(t *testing.T) {
	s, _ := newTestStore(t)
	const n = 8
	wins := make(chan bool, n)
	for range n {
		go func() {
			ok, err := s.ClaimDay(MetaTidyDay, "2026-11-08")
			if err != nil {
				t.Error(err)
			}
			wins <- ok
		}()
	}
	won := 0
	for range n {
		if <-wins {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("이긴 수 = %d", won)
	}
}

// 기획 · orbit-mem 4.3 H1 ④: 28일 쓰지 않은 후보 낡음은 회상을 넣을 때(inject full)만 돈다.
func TestTidy_후보_기한_낡음은_ExpireCandidates일_때만_돈다(t *testing.T) {
	s, c := newTestStore(t)
	start := c.t
	injectAt(t, s, "s0", start, "m-none")
	old := candidate(t, s, "안 쓴 후보")
	for d := 10; d < 17; d++ {
		injectAt(t, s, "busy", start.Add(time.Duration(d)*24*time.Hour), "m-none")
	}
	c.t = start.Add(29 * 24 * time.Hour)

	r := s.Tidy(TidyOptions{LedgerWindowDays: 7, CandidateExpiryDays: 28, ExpireCandidates: false})
	if len(r.Errors) != 0 || r.Expired != 0 {
		t.Fatalf("끈 정리 = %+v", r)
	}
	if got, _ := s.Get(old.ID); got.State != StateCandidate {
		t.Fatalf("끈 정리 뒤 = %s", got.State)
	}

	r = s.Tidy(TidyOptions{LedgerWindowDays: 7, CandidateExpiryDays: 28, ExpireCandidates: true})
	if len(r.Errors) != 0 || r.Expired != 1 {
		t.Fatalf("켠 정리 = %+v", r)
	}
	if got, _ := s.Get(old.ID); got.State != StateStale {
		t.Fatalf("켠 정리 뒤 = %s", got.State)
	}
}

// 기획 · orbit-mem 4.3 H1 ⑥: 30일 지난 세션 묶음을 지운다.
func TestTidy_오래된_세션_묶음을_지운다(t *testing.T) {
	s, c := newTestStore(t)
	if err := s.SaveSessionBundle("claude", "old", "문맥", nil); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add((SessionStateDays - 1) * 24 * time.Hour)
	if err := s.SaveSessionBundle("claude", "new", "문맥", nil); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * 24 * time.Hour)
	if r := s.Tidy(TidyOptions{LedgerWindowDays: 7}); len(r.Errors) != 0 {
		t.Fatalf("errors = %v", r.Errors)
	}
	if _, _, ok := s.SessionBundle("claude", "old"); ok {
		t.Fatal("30일 지난 묶음이 남았다")
	}
	if _, _, ok := s.SessionBundle("claude", "new"); !ok {
		t.Fatal("30일 안의 묶음이 지워졌다")
	}
}

// 실제 PC 첫 정리(2026-11-08)에서 찾음: 증거가 생긴 후보가 사용자 말 출처를 품으면 자동 확정은 보호에 막힌다.
// 그것은 사람을 기다리는 것이지 정리의 실패가 아니다: 건너뛰고 오류로 세지 않는다(후보 기한 낡음과 같게).
func TestTidy_사용자_말을_품은_후보의_자동_확정은_오류가_아니다(t *testing.T) {
	s, _ := newTestStore(t)
	m, _ := s.Save(SaveInput{Kind: KindLesson, Title: "사용자 말 후보", Source: Source{Kind: SourceSession}})
	if err := s.AddSource(m.ID, Source{Kind: SourceQuote, Excerpt: "이건 사용자 말"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(m.ID); got.State != StateCandidate {
		t.Skipf("인용 출처를 더하면 확정되는 판: %s", got.State)
	}
	for _, sess := range []string{"a", "b"} { // 증거 ②: 회상돼 따랐고 검증 통과
		if err := s.RecordUsage(UsageInput{MemoryID: m.ID, Verdict: UsageFollowed, Verified: true, Session: sess}); err != nil {
			t.Fatal(err)
		}
	}
	if ev, _ := s.EvidenceFor(m.ID); ev == "" {
		t.Skip("증거가 생기지 않는 판")
	}
	n, err := s.ConfirmCandidates()
	if err != nil || n != 0 {
		t.Fatalf("ConfirmCandidates = %d %v", n, err)
	}
	if r := s.Tidy(TidyOptions{LedgerWindowDays: 7}); len(r.Errors) != 0 {
		t.Fatalf("tidy errors = %v", r.Errors)
	}
	if got, _ := s.Get(m.ID); got.State != StateCandidate {
		t.Fatalf("state = %s", got.State)
	}
}
