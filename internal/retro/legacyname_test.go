package retro

import (
	"github.com/onidot-labs/orbit-mem/internal/transcript"
	"strconv"
	"testing"
	"time"
)

// 옛 이름(LegacyWorkHost)으로 쌓인 작업 회고 줄도 같은 작업 끝으로 본다: 업그레이드 뒤 끝난 작업이 다시 돌지 않고,
// 옛 이름의 죽은 실행 줄은 새 판이 실패로 닫고 다시 돈다.

func legacyKey(in WorkIn) string {
	return LegacyWorkHost + ":" + in.Ref + "#" + strconv.FormatInt(in.EndedAt, 10)
}

func TestWork_옛_이름으로_끝난_작업_회고는_다시_돌지_않는다(t *testing.T) {
	if WorkHost != "orbit-code" || LegacyWorkHost == WorkHost {
		t.Fatalf("WorkHost = %q, legacy %q", WorkHost, LegacyWorkHost)
	}
	e := setup(t, workEnd.Add(time.Hour))
	in := workIn(t, "w-legacy", workEnd, "옛 판이 회고한 작업")
	p, _ := e.doneWork(t, in, Submission{})
	// 옛 판이 쓴 줄로 되돌린다.
	if _, err := e.s.DB().Exec(`UPDATE retros SET session=? WHERE id=?`, legacyKey(in), p.ID); err != nil {
		t.Fatal(err)
	}
	if again := e.prepWork(t, in); again.Status != StatusSkipped || workRows(t, e) != 1 {
		t.Fatalf("legacy done work ran again: %+v rows %d", again, workRows(t, e))
	}
}

func TestWork_옛_이름의_오래된_실행_줄은_실패로_닫고_다시_돈다(t *testing.T) {
	start := workEnd.Add(time.Hour)
	e := setup(t, start)
	in := workIn(t, "w-legacy-stale", workEnd, "옛 판 준비 뒤 orb가 죽은 작업")
	old := e.prepWork(t, in)
	if _, err := e.s.DB().Exec(`UPDATE retros SET session=? WHERE id=?`, legacyKey(in), old.ID); err != nil {
		t.Fatal(err)
	}
	e.r.Now = func() time.Time { return start.Add(workStaleAfter - time.Minute) }
	if p := e.prepWork(t, in); p.Status != StatusSkipped {
		t.Fatalf("fresh legacy running = %+v", p)
	}
	e.r.Now = func() time.Time { return start.Add(workStaleAfter + time.Minute) }
	p := e.prepWork(t, in)
	if p.Status != StatusRunning || p.ID == old.ID {
		t.Fatalf("after stale limit = %+v", p)
	}
	if st, _ := getStatus(e.s, old.ID); st.Status != StatusFailed {
		t.Fatalf("legacy row = %+v", st)
	}
	if p.Session != WorkHost+":"+in.Ref {
		t.Fatalf("new session = %q", p.Session)
	}
}

// 업그레이드 전에 준비한 작업 회고 재료(호스트 LegacyWorkHost)와 새 이름 참조 「orbit-code:w-…」, 또는 그 반대도
// 같은 작업 세션으로 찾는다.
func TestWork_옛_이름과_새_이름의_작업_세션_참조는_같은_세션이다(t *testing.T) {
	turn := []transcript.Turn{{Role: "user", Text: "그 방식 말고 표로 써", At: workEnd}}
	for _, c := range []struct{ host, ref string }{
		{LegacyWorkHost, WorkHost + ":w-1"},
		{WorkHost, LegacyWorkHost + ":w-1"},
		{WorkHost, WorkHost + ":w-1"},
	} {
		mat := Material{Sessions: []transcript.Session{{Host: c.host, ID: "w-1", Turns: turn}}}
		if _, _, ok := findQuote(mat, c.ref, squash("표로 써")); !ok {
			t.Errorf("session host %q, ref %q: not found", c.host, c.ref)
		}
	}
	other := Material{Sessions: []transcript.Session{{Host: "claude", ID: "w-1", Turns: turn}}}
	if _, _, ok := findQuote(other, WorkHost+":w-1", squash("표로 써")); ok {
		t.Error("another host's session with the same id matched")
	}
}
