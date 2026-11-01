package memory

import (
	"testing"
	"time"
)

// AU-5(ONID-130): 「procedure」 태그 교훈(절차)이 서로 다른 작업에서 기준 수 이상 나오면 하네스 개선 후보다.
// 작업은 출처 종류 work의 ref로 센다. 확인함 뒤에는 그 뒤에 붙은 출처만 센다.
func TestHarness_절차_교훈은_서로_다른_작업_기준_수부터_하네스_후보다(t *testing.T) {
	s, _ := newTestStore(t)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	tick := func() { now = now.Add(time.Minute) }
	s.Now = func() time.Time { return now }
	work := func(ref string) Source { return Source{Kind: SourceWork, Ref: ref} }

	proc, err := s.Save(SaveInput{Kind: KindLesson, Title: "배포 전에 dry-run", Tags: []string{"procedure"}, Source: work("w-1")})
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := s.Save(SaveInput{Kind: KindLesson, Title: "태그 없는 교훈", Source: work("w-1")})
	gone, _ := s.Save(SaveInput{Kind: KindLesson, Title: "낡은 절차", Tags: []string{"procedure"}, Source: work("w-1")})
	fact, _ := s.Save(SaveInput{Kind: KindFact, Title: "절차 태그 사실", Tags: []string{"procedure"}, Source: work("w-1")})
	for _, ref := range []string{"w-2", "w-2", "w-3"} {
		tick()
		for _, id := range []string{plain.ID, gone.ID, fact.ID} {
			s.AddSource(id, work(ref))
		}
		if ref == "w-3" {
			break
		}
		s.AddSource(proc.ID, work(ref))
	}
	s.AddSource(proc.ID, Source{Kind: SourceSession, Ref: "claude:s-9"}) // 작업이 아닌 출처는 세지 않는다
	if _, err := s.SetState(gone.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	if hc, _ := s.HarnessCandidates(); len(hc) != 0 {
		t.Fatalf("작업 둘(같은 작업 두 번 포함)은 후보가 아닙니다: %+v", hc)
	}
	tick()
	s.AddSource(proc.ID, work("w-3"))
	hc, err := s.HarnessCandidates()
	if err != nil || len(hc) != 1 {
		t.Fatalf("harness = %+v %v", hc, err)
	}
	c := hc[0]
	if c.TargetKind != TargetKindProcedure || c.Target != proc.ID || c.Title != proc.Title || c.Sessions != 3 || c.Threshold != 3 || !c.Over || !c.Harness ||
		c.Key != "correction:procedure:"+proc.ID {
		t.Fatalf("candidate = %+v", c)
	}
	// 감사 목록에도 보인다(스킬·역할 교정과 함께).
	au, _ := s.Audit(AuditFilter{})
	if len(au.Corrections) != 1 || au.Corrections[0].Key != c.Key {
		t.Fatalf("audit corrections = %+v", au.Corrections)
	}
	// 확인함 뒤에는 그 뒤에 붙은 작업만 센다.
	tick()
	if err := s.Acknowledge(c.Key, Who{}); err != nil {
		t.Fatal(err)
	}
	if hc, _ := s.HarnessCandidates(); len(hc) != 0 {
		t.Fatalf("after ack = %+v", hc)
	}
	for _, ref := range []string{"w-4", "w-5"} {
		tick()
		s.AddSource(proc.ID, work(ref))
	}
	if hc, _ := s.HarnessCandidates(); len(hc) != 0 {
		t.Fatalf("two newer works are not over: %+v", hc)
	}
	tick()
	s.AddSource(proc.ID, work("w-1")) // 확인함 앞에 센 작업도 뒤에 다시 나오면 센다
	hc, _ = s.HarnessCandidates()
	if len(hc) != 1 || hc[0].Sessions != 3 || hc[0].AckedAt == 0 {
		t.Fatalf("after ack and three newer works = %+v", hc)
	}
	// 기준을 사용자가 바꾸면 그 기준을 따른다.
	s.Policy.CorrectionSessions = 4
	if hc, _ := s.HarnessCandidates(); len(hc) != 0 {
		t.Fatalf("threshold 4 = %+v", hc)
	}
}
