package memory

import (
	"testing"
	"time"
)

// AU-7(ONID-132) 능숙해짐 지표: orbit-code가 넣은 작업 지표(work.metrics)와 작업별 교정 수,
// 그리고 변경 효과 원장 판정에 작업 지표가 근거로 들어가는 것.

func workRec(id string, ended time.Time, m WorkMetrics, sessions ...WorkSession) WorkRecord {
	return WorkRecord{WorkID: id, EndedAt: ended.UnixMilli(), Project: "git:example/textkit", Kind: "add", Size: "small", Status: "completed",
		Sessions: sessions, Metrics: m}
}

func TestWorkMetrics_같은_내용은_다시_쌓지_않고_마지막이_정본(t *testing.T) {
	s, c := newTestStore(t)
	r := workRec("w-1", c.t, WorkMetrics{Asked: 1, FirstPassed: 1, FirstSteps: 2, CostUSD: 0.03, Turns: 9, Minutes: 2.5})
	if n, err := s.RecordWorkMetrics([]WorkRecord{r, r}); err != nil || n != 1 {
		t.Fatalf("recorded %d %v", n, err)
	}
	if n, _ := s.RecordWorkMetrics([]WorkRecord{r}); n != 0 {
		t.Fatalf("같은 내용을 다시 쌓았습니다: %d", n)
	}
	r.Metrics.Repeats = 2
	c.t = c.t.Add(time.Minute)
	if n, _ := s.RecordWorkMetrics([]WorkRecord{r}); n != 1 {
		t.Fatal("바뀐 내용은 쌓는다")
	}
	got, err := s.WorkRecords(0, c.t.Add(time.Hour).UnixMilli())
	if err != nil || len(got) != 1 || got[0].Metrics.Repeats != 2 || got[0].Metrics.Asked != 1 || got[0].Project != "git:example/textkit" {
		t.Fatalf("records = %+v %v", got, err)
	}
	evs, _ := s.Events(EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: EventWorkMetrics})
	if len(evs) != 2 || evs[0].Host != WorkMetricsHost || evs[0].Session != "w-1" {
		t.Fatalf("events = %+v", evs)
	}
	// 끝난 시각이 기간 밖이면 빠진다
	if got, _ := s.WorkRecords(c.t.Add(time.Hour).UnixMilli(), c.t.Add(2*time.Hour).UnixMilli()); len(got) != 0 {
		t.Fatalf("기간 밖 %+v", got)
	}
	if _, err := s.RecordWorkMetrics([]WorkRecord{{WorkID: "../x", EndedAt: 1}}); err == nil {
		t.Fatal("작업 id가 아닌 값은 거절한다")
	}
}

// L2: 교정은 작업 id 세션(작업 회고)이면 언제든, 작업 세션이면 그 창 안에서 일어난 것만 센다.
func TestWorkCorrections_작업_세션_창_안의_교정만_센다(t *testing.T) {
	s, c := newTestStore(t)
	t0 := c.t
	corr := func(host, session string, at time.Time) {
		t.Helper()
		if err := s.RecordCorrection(CorrectionInput{Quote: "그거 아니야", Host: host, Session: session, OccurredAt: at.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	corr(WorkMetricsHost, "w-1", t0.Add(3*time.Hour)) // 작업 회고가 남긴 교정
	corr("claude", "S", t0.Add(30*time.Minute))       // 창 안
	corr("claude", "S", t0.Add(-time.Minute))         // 작업 열리기 전
	corr("claude", "S", t0.Add(2*time.Hour))          // 다음 작업이 열린 뒤
	corr("codex", "S", t0.Add(30*time.Minute))        // 다른 호스트의 같은 세션 id
	corr("claude", "other", t0.Add(30*time.Minute))   // 다른 세션
	refs := []WorkRef{
		{WorkID: "w-1", Sessions: []WorkSession{{Host: "claude", Session: "S", From: t0.UnixMilli(), To: t0.Add(time.Hour).UnixMilli()}}},
		{WorkID: "w-2", Sessions: []WorkSession{{Host: "claude", Session: "S", From: t0.Add(time.Hour).UnixMilli()}}}, // 끝 없음
		{WorkID: "w-3"},
	}
	got, err := s.WorkCorrections(refs)
	if err != nil {
		t.Fatal(err)
	}
	if got["w-1"] != 2 || got["w-2"] != 1 || got["w-3"] != 0 {
		t.Fatalf("corrections = %+v", got)
	}
}

// injectWork puts a memory into a work's step recall (host orbit-code, session = work id).
func injectWork(t *testing.T, s *Store, workID string, at time.Time, ids ...string) {
	t.Helper()
	if err := s.RecordEvent(Event{Kind: EventInjectPrompt, Host: WorkMetricsHost, At: at.UnixMilli(), Day: Day(at), Session: workID}, map[string]any{"memoryIds": ids}); err != nil {
		t.Fatal(err)
	}
}

// L6: 교정·어김 신호가 같을 때(여기서는 없음) 작업당 문제 수가 줄면 도움됨, 늘면 해로움,
// 문제 수가 같으면 첫 검증 통과율로 가른다. 기억이 붙지 않은 작업은 보지 않는다.
func TestLedger_작업_지표가_판정의_근거가_된다(t *testing.T) {
	const window = 7
	day := 24 * time.Hour
	cases := []struct {
		name          string
		before, after WorkMetrics
		verdict       string
	}{
		{"문제가 줄면 도움됨", WorkMetrics{Asked: 2, Repeats: 1, FirstPassed: 1, FirstSteps: 2}, WorkMetrics{Asked: 0, FirstPassed: 1, FirstSteps: 2}, VerdictHelped},
		{"문제가 늘면 해로움", WorkMetrics{FirstPassed: 2, FirstSteps: 2}, WorkMetrics{Overturned: 1, Replans: 1, FirstPassed: 2, FirstSteps: 2}, VerdictHarmful},
		{"같으면 첫 통과율이 오르면 도움됨", WorkMetrics{Asked: 1, FirstPassed: 1, FirstSteps: 2}, WorkMetrics{Asked: 1, FirstPassed: 2, FirstSteps: 2}, VerdictHelped},
		{"모두 같으면 효과 없음", WorkMetrics{Asked: 1, FirstPassed: 1, FirstSteps: 2}, WorkMetrics{Asked: 1, FirstPassed: 1, FirstSteps: 2}, VerdictNoEffect},
		{"가정하고 진행은 문제가 아니다", WorkMetrics{Assumed: 0, FirstPassed: 1, FirstSteps: 1}, WorkMetrics{Assumed: 3, FirstPassed: 1, FirstSteps: 1}, VerdictNoEffect},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, clk := newTestStore(t)
			m := candidate(t, s, "대상 교훈")
			start := clk.t
			// 변경 전·후 각각 같은 지표의 작업 둘(작업 근거는 한쪽에 둘 이상: MinWorksPerSide)
			for i, w := range []string{"w-b1", "w-b2"} {
				b := start.Add(-time.Duration(2+i) * day)
				injectWork(t, s, w, b, m.ID)
				if _, err := s.RecordWorkMetrics([]WorkRecord{workRec(w, b.Add(time.Hour), tc.before)}); err != nil {
					t.Fatal(err)
				}
			}
			// 기억이 붙지 않은 작업: 문제가 많아도 보지 않는다
			if _, err := s.RecordWorkMetrics([]WorkRecord{workRec("w-x", start.Add(-day), WorkMetrics{Asked: 9})}); err != nil {
				t.Fatal(err)
			}
			id := approveEdit(t, s, m.ID, "고친 교훈")
			for i, w := range []string{"w-a1", "w-a2"} {
				a := start.Add(time.Duration(2+i) * day)
				injectWork(t, s, w, a, m.ID)
				if _, err := s.RecordWorkMetrics([]WorkRecord{workRec(w, a.Add(time.Hour), tc.after)}); err != nil {
					t.Fatal(err)
				}
			}
			clk.t = start.Add(window*day + time.Minute)
			if n, err := s.JudgeLedger(window); err != nil || n != 1 {
				t.Fatalf("judged %d %v", n, err)
			}
			e, _ := s.LedgerEntry(id)
			if e.Verdict != tc.verdict || !e.Evidence {
				t.Fatalf("entry = %s evidence=%v %+v", e.Verdict, e.Evidence, e.Observation)
			}
			o := e.Observation
			if o.Before.Works != 2 || o.After.Works != 2 || o.Before.FirstSteps != 2*tc.before.FirstSteps {
				t.Fatalf("observation = %+v", o)
			}
		})
	}
}

// 작업 교정도 문제 수에 든다(작업 회고가 남긴 교정, 세션 = 작업 id).
func TestLedger_작업_교정이_문제_수에_든다(t *testing.T) {
	s, clk := newTestStore(t)
	m := candidate(t, s, "대상")
	start := clk.t
	day := 24 * time.Hour
	for i, w := range []string{"w-b", "w-b2"} {
		at := start.Add(-time.Duration(i+1) * day)
		injectWork(t, s, w, at, m.ID)
		s.RecordWorkMetrics([]WorkRecord{workRec(w, at.Add(time.Hour), WorkMetrics{FirstPassed: 1, FirstSteps: 1})})
	}
	id := approveEdit(t, s, m.ID, "고침")
	for i, w := range []string{"w-a", "w-a2"} {
		at := start.Add(time.Duration(i+1) * day)
		injectWork(t, s, w, at, m.ID)
		s.RecordWorkMetrics([]WorkRecord{workRec(w, at.Add(time.Hour), WorkMetrics{FirstPassed: 1, FirstSteps: 1})})
	}
	// 작업 회고가 w-a에서 교정을 찾았다(기억을 가리키지 않는 교정: 어김 신호가 아니다)
	if err := s.RecordCorrection(CorrectionInput{Quote: "그 방식 말고", Host: WorkMetricsHost, Session: "w-a", OccurredAt: start.Add(day + 2*time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	clk.t = start.Add(8 * day)
	if _, err := s.JudgeLedger(7); err != nil {
		t.Fatal(err)
	}
	e, _ := s.LedgerEntry(id)
	if e.Verdict != VerdictHarmful || e.Observation.After.Trouble != 1 || e.Observation.Before.Trouble != 0 {
		t.Fatalf("entry = %s %+v", e.Verdict, e.Observation)
	}
}

// L6 자동 확정 포함: 증거로 자동 확정한 줄도 작업 지표로 판정되고, 해로우면 스스로 되돌린다.
func TestLedger_자동_확정도_작업_지표로_판정하고_해로우면_되돌린다(t *testing.T) {
	s, clk := newTestStore(t)
	s.Policy.FollowedVerified = 2
	m := candidate(t, s, "회상된 교훈")
	start := clk.t
	day := 24 * time.Hour
	// 후보일 때 붙은 작업 두 개: 문제 없음, 따르고 검증 통과 → 증거 ②로 자동 확정
	for i, w := range []string{"w-1", "w-2"} {
		at := start.Add(time.Duration(i) * time.Hour)
		injectWork(t, s, w, at, m.ID)
		s.RecordWorkMetrics([]WorkRecord{workRec(w, at.Add(30*time.Minute), WorkMetrics{FirstPassed: 2, FirstSteps: 2})})
		clk.t = at.Add(40 * time.Minute)
		if err := s.RecordUsage(UsageInput{MemoryID: m.ID, Host: WorkMetricsHost, Session: w, Verdict: UsageFollowed, Verified: true}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Get(m.ID); got.State != StateConfirmed {
		t.Fatalf("자동 확정되지 않았습니다: %s", got.State)
	}
	all, _ := s.Ledger(LedgerFilter{})
	if len(all) != 1 {
		t.Fatalf("ledger = %+v", all)
	}
	autoLine := all[0].ID
	// 확정된 뒤 붙은 작업은 질문·재계획이 늘고 첫 통과가 떨어졌다
	for i, w := range []string{"w-3", "w-4"} {
		at := clk.t.Add(time.Duration(i+1) * day)
		injectWork(t, s, w, at, m.ID)
		s.RecordWorkMetrics([]WorkRecord{workRec(w, at.Add(time.Hour), WorkMetrics{Asked: 2, Replans: 1, FirstPassed: 0, FirstSteps: 2})})
	}
	clk.t = clk.t.Add(8 * day)
	if _, err := s.JudgeLedger(7); err != nil {
		t.Fatal(err)
	}
	e, _ := s.LedgerEntry(autoLine)
	if e.Verdict != VerdictHarmful || !e.Evidence || e.Observation.Before.Works != 2 || e.Observation.After.Works != 2 {
		t.Fatalf("auto line = %s %+v", e.Verdict, e.Observation)
	}
	reverted, err := s.RevertHarmfulAuto()
	if err != nil || len(reverted) != 1 || reverted[0].LedgerID != autoLine {
		t.Fatalf("reverted = %+v %v", reverted, err)
	}
	if got, _ := s.Get(m.ID); got.State != StateCandidate {
		t.Fatalf("되돌린 뒤 %s", got.State)
	}
}

// 작업이 한쪽에만 있으면 작업 근거가 없다(지금 판정과 같다).
func TestLedger_작업이_한쪽에만_있으면_근거가_아니다(t *testing.T) {
	s, clk := newTestStore(t)
	m := candidate(t, s, "대상")
	start := clk.t
	id := approveEdit(t, s, m.ID, "고침")
	injectWork(t, s, "w-a", start.Add(24*time.Hour), m.ID)
	s.RecordWorkMetrics([]WorkRecord{workRec("w-a", start.Add(25*time.Hour), WorkMetrics{Asked: 3})})
	clk.t = start.Add(8 * 24 * time.Hour)
	s.JudgeLedger(7)
	e, _ := s.LedgerEntry(id)
	if e.Verdict != VerdictNoEffect || e.Evidence {
		t.Fatalf("entry = %s %v %+v", e.Verdict, e.Evidence, e.Observation)
	}
}

// 작업 하나 대 하나는 근거가 아니다(MinWorksPerSide): 큰 작업 하나가 자동 확정을 되돌리지 않게.
func TestLedger_작업이_한쪽에_하나뿐이면_근거가_아니다(t *testing.T) {
	s, clk := newTestStore(t)
	m := candidate(t, s, "대상")
	start := clk.t
	day := 24 * time.Hour
	injectWork(t, s, "w-b", start.Add(-day), m.ID)
	s.RecordWorkMetrics([]WorkRecord{workRec("w-b", start.Add(-day+time.Hour), WorkMetrics{})})
	id := approveEdit(t, s, m.ID, "고침")
	for i, w := range []string{"w-a1", "w-a2"} {
		at := start.Add(time.Duration(i+1) * day)
		injectWork(t, s, w, at, m.ID)
		s.RecordWorkMetrics([]WorkRecord{workRec(w, at.Add(time.Hour), WorkMetrics{Asked: 5})})
	}
	clk.t = start.Add(8 * day)
	s.JudgeLedger(7)
	e, _ := s.LedgerEntry(id)
	if e.Verdict != VerdictNoEffect || e.Evidence || e.Observation.Before.Works != 1 || e.Observation.After.Works != 2 {
		t.Fatalf("entry = %s %v %+v", e.Verdict, e.Evidence, e.Observation)
	}
}

// 입력 검사: 빈 세션, 음수 지표, 통과가 단계보다 많은 값은 거절한다.
func TestWorkMetrics_틀린_입력은_거절한다(t *testing.T) {
	s, c := newTestStore(t)
	for _, r := range []WorkRecord{
		workRec("w-1", c.t, WorkMetrics{}, WorkSession{Session: " ", From: 1}),
		workRec("w-1", c.t, WorkMetrics{Asked: -1}),
		workRec("w-1", c.t, WorkMetrics{FirstPassed: 2, FirstSteps: 1}),
		workRec("w-1", c.t, WorkMetrics{}, WorkSession{Session: "S", From: 5, To: 4}),
	} {
		if _, err := s.RecordWorkMetrics([]WorkRecord{r}); err == nil {
			t.Fatalf("받았습니다: %+v", r)
		}
	}
	// 빈 세션 창은 교정을 끌어오지 않는다
	s.RecordCorrection(CorrectionInput{Quote: "아니", Session: ""})
	got, _ := s.WorkCorrections([]WorkRef{{WorkID: "w-1", Sessions: []WorkSession{{Session: "", From: 0}}}})
	if got["w-1"] != 0 {
		t.Fatalf("corrections = %v", got)
	}
}
