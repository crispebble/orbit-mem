package memory

import (
	"testing"
	"time"
)

// orbit-code가 옛 이름을 버렸다(2026-11-05). 작업 사건은 새 호스트 이름으로 쓰고, 이미 쌓인 옛 이름 사건도 같은 작업의 것으로 읽는다.

func TestWorkHost_새_작업_사건은_orbit_code로_쓴다(t *testing.T) {
	if WorkMetricsHost != "orbit-code" {
		t.Fatalf("WorkMetricsHost = %q", WorkMetricsHost)
	}
	if LegacyWorkMetricsHost == WorkMetricsHost {
		t.Fatal("legacy host must differ")
	}
	s, c := newTestStore(t)
	if _, err := s.RecordWorkMetrics([]WorkRecord{workRec("w-1", c.t, WorkMetrics{})}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Events(EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: EventWorkMetrics})
	if len(evs) != 1 || evs[0].Host != "orbit-code" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestWorkCorrections_옛_호스트_이름의_교정도_같은_호스트로_센다(t *testing.T) {
	s, c := newTestStore(t)
	t0 := c.t
	corr := func(host, session string, at time.Time) {
		t.Helper()
		if err := s.RecordCorrection(CorrectionInput{Quote: "그거 아니야", Host: host, Session: session, OccurredAt: at.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	corr(LegacyWorkMetricsHost, "S", t0.Add(10*time.Minute)) // 옛 orb가 남긴 교정, 새 창
	corr(WorkMetricsHost, "T", t0.Add(10*time.Minute))       // 새 판이 남긴 교정, 옛 창
	corr("claude", "S", t0.Add(10*time.Minute))              // 다른 호스트는 여전히 다르다
	refs := []WorkRef{
		{WorkID: "w-new", Sessions: []WorkSession{{Host: WorkMetricsHost, Session: "S", From: t0.UnixMilli()}}},
		{WorkID: "w-old", Sessions: []WorkSession{{Host: LegacyWorkMetricsHost, Session: "T", From: t0.UnixMilli()}}},
	}
	got, err := s.WorkCorrections(refs)
	if err != nil {
		t.Fatal(err)
	}
	if got["w-new"] != 1 || got["w-old"] != 1 {
		t.Fatalf("corrections = %+v", got)
	}
}
