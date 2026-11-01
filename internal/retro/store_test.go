package retro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

func retroRuns(t *testing.T, e *env, day string) []memory.Event {
	t.Helper()
	evs, err := e.s.Events(memory.EventFilter{FromDay: day, ToDay: day, KindPrefix: memory.EventRetroRun})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrepare_재료를_남기고_실행_중으로_적는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	p, err := e.r.Prepare(KindDaily, "2026-10-26", "codex")
	if err != nil || p.Status != StatusRunning || !strings.HasPrefix(p.ID, "r-") {
		t.Fatalf("prepare = %+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(e.r.Paths.RetroDir(), p.ID, "material.json")); err != nil {
		t.Fatalf("material.json: %v", err)
	}
	st, _ := getStatus(e.s, p.ID)
	if st.Status != StatusRunning || st.Host != "codex" || st.Kind != KindDaily || st.Period != "2026-10-26" {
		t.Fatalf("row = %+v", st)
	}
	want := `{"status":"running","id":"` + p.ID + `","kind":"daily","period":"2026-10-26","session":"","sourceChecks":0}`
	if got := marshal(t, p); got != want {
		t.Fatalf("json\n got %s\nwant %s", got, want)
	}
	// 같은 날을 다시 준비하면(실행 중·실패) 새 실행이다. 끝난 날은 건너뛴다.
	e.r.Now = func() time.Time { return time.Date(2026, 10, 27, 4, 1, 0, 0, kst) }
	e.ai = func(id string) error {
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "인사", Journal: &JournalIn{Title: "인사", Body: "-"}})
		return err
	}
	if _, err := e.r.Run(nil, KindDaily, "2026-10-26"); err != nil {
		t.Fatal(err)
	}
	skipped, err := e.r.Prepare(KindDaily, "2026-10-26", "claude")
	if err != nil || marshal(t, skipped) != `{"status":"skipped","kind":"daily","period":"2026-10-26"}` {
		t.Fatalf("skipped = %s %v", marshal(t, skipped), err)
	}
	empty, err := e.r.Prepare(KindDaily, "2026-10-25", "claude")
	if err != nil || marshal(t, empty) != `{"status":"empty","id":"`+empty.ID+`","kind":"daily","period":"2026-10-25"}` {
		t.Fatalf("empty = %s %v", marshal(t, empty), err)
	}
	if st, _ := getStatus(e.s, empty.ID); st.Host != "" || st.FinishedAt != st.StartedAt || st.Summary != i18n.T("retro.no_material") {
		t.Fatalf("empty row = %+v", st)
	}
}

func TestPrepare_날짜와_종류를_검사한다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst)) // 화요일
	for _, tc := range []struct{ kind, period, key string }{
		{KindDaily, "2026-10-27", "retro.err.daily_until_yesterday"},
		{KindWeekly, "2026-10-20", "retro.err.weekly_monday"}, // 이번 주: 아직 안 끝남
		{KindWeekly, "2026-10-21", "retro.err.weekly_monday"}, // 월요일이 아님
		{KindDaily, "10/26", "retro.err.date_format"},
		{"monthly", "2026-10-26", "retro.err.kind"},
	} {
		_, err := e.r.Prepare(tc.kind, tc.period, "claude")
		want := i18n.Errorf(tc.key, tc.period).Error()
		if tc.key == "retro.err.kind" {
			want = i18n.T(tc.key)
		}
		if err == nil || err.Error() != want {
			t.Errorf("%s %s: err = %v, want %s", tc.kind, tc.period, err, want)
		}
	}
	if p, err := e.r.Prepare(KindWeekly, "2026-10-19", "claude"); err != nil || p.Status != StatusEmpty {
		t.Fatalf("last week = %+v %v", p, err)
	}
}

// 제출하지 않은 실행: 실행 오류 + 답의 끝 2000바이트 + 로그인 안내를 오류로 남기고, 재료는 지우고, 비용 사건은 하나다.
func TestFinish_제출하지_않은_실행은_실패와_오류_문장을_남긴다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	p, err := e.r.Prepare(KindDaily, "2026-10-26", "codex")
	if err != nil {
		t.Fatal(err)
	}
	long := "앞" + strings.Repeat("x", 2500) + "끝  "
	f, err := e.r.Finish(p.ID, HostResult{Model: "gpt-x", ExecError: "exit status 1", Text: long, SignedOut: true})
	if err != nil {
		t.Fatal(err)
	}
	tail := strings.TrimSpace(long)
	tail = tail[len(tail)-2000:]
	msg := i18n.T("retro.host_no_result") + ": exit status 1\n" + tail + "\n" + i18n.T("retro.signed_out_note")
	if f.Status != StatusFailed || f.Error != msg || !f.SignedOut {
		t.Fatalf("finish = %+v", f)
	}
	b, _ := json.Marshal(f)
	var shape map[string]any
	json.Unmarshal(b, &shape)
	if len(shape) != 6 || shape["status"] != "failed" || shape["signedOut"] != true || shape["error"] != msg || shape["kind"] != "daily" || shape["period"] != "2026-10-26" || shape["id"] != p.ID {
		t.Fatalf("json = %s", b)
	}
	st, _ := getStatus(e.s, p.ID)
	if st.Status != StatusFailed || st.Error != msg || st.FinishedAt == 0 {
		t.Fatalf("row = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(e.r.Paths.RetroDir(), p.ID)); !os.IsNotExist(err) {
		t.Fatalf("material left after a failed run: %v", err)
	}
	evs := retroRuns(t, e, "2026-10-27")
	if len(evs) != 1 || evs[0].Host != "codex" {
		t.Fatalf("events = %+v", evs)
	}
	var data map[string]any
	json.Unmarshal(evs[0].Data, &data)
	if len(data) != 6 || data["retroId"] != p.ID || data["retroKind"] != "daily" || data["model"] != "gpt-x" || data["ok"] != false || data["costKnown"] != false || data["sourceChecks"] != float64(0) {
		t.Fatalf("event data = %s", evs[0].Data)
	}
	// 실행 오류도 답도 없으면 첫 문장만.
	p2, _ := e.r.Prepare(KindDaily, "2026-10-26", "claude")
	if f, _ := e.r.Finish(p2.ID, HostResult{Text: "  \n"}); f.Error != i18n.T("retro.host_no_result") || f.SignedOut {
		t.Fatalf("bare = %+v", f)
	}
}

func TestFinish_두_번_불러도_사건은_하나고_결과는_같다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	p, _ := e.r.Prepare(KindDaily, "2026-10-26", "claude")
	first, err := e.r.Finish(p.ID, HostResult{ExecError: "exit status 1", SignedOut: true})
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.r.Finish(p.ID, HostResult{})
	if err != nil || again != first {
		t.Fatalf("again = %+v %v, first = %+v", again, err, first)
	}
	if evs := retroRuns(t, e, "2026-10-27"); len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	// 끝난(done) 회고를 다시 마쳐도 실패로 바뀌지 않는다.
	p2, _ := e.r.Prepare(KindDaily, "2026-10-26", "claude")
	Submit(e.s, e.r.Paths, p2.ID, Submission{Summary: "인사", Journal: &JournalIn{Title: "인사", Body: "-"}})
	d1, _ := e.r.Finish(p2.ID, HostResult{CostKnown: true, CostUSD: 0.01, InputTokens: 1, OutputTokens: 2, Model: "haiku"})
	d2, _ := e.r.Finish(p2.ID, HostResult{ExecError: "late"})
	if d1.Status != StatusDone || d2.Status != StatusDone || marshal(t, d1) != `{"id":"`+p2.ID+`","kind":"daily","period":"2026-10-26","status":"done"}` {
		t.Fatalf("done = %+v %+v", d1, d2)
	}
	evs := retroRuns(t, e, "2026-10-27")
	if len(evs) != 2 || !strings.Contains(string(evs[1].Data), `"costUsd":0.01`) || !strings.Contains(string(evs[1].Data), `"ok":true`) {
		t.Fatalf("events = %+v", evs)
	}
}

func TestFinish_없거나_호스트에_가지_않은_회고는_마칠_수_없다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	if _, err := e.r.Finish("r-none", HostResult{}); err == nil {
		t.Fatal("unknown id finished")
	}
	empty, _ := e.r.Prepare(KindDaily, "2026-10-26", "claude")
	if _, err := e.r.Finish(empty.ID, HostResult{}); err == nil {
		t.Fatal("an empty run finished")
	}
	if evs := retroRuns(t, e, "2026-10-27"); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
}

// 호스트가 끝났을 때 제출이 아직 쓰이는 중(submitting)이면 잠깐 기다렸다가 다시 읽는다.
func TestFinish_제출_중이면_기다렸다가_다시_읽는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	old := submittingWait
	submittingWait = 300 * time.Millisecond
	t.Cleanup(func() { submittingWait = old })
	p, _ := e.r.Prepare(KindDaily, "2026-10-26", "claude")
	e.s.DB().Exec(`UPDATE retros SET status='submitting' WHERE id=?`, p.ID)
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.s.DB().Exec(`UPDATE retros SET status='done' WHERE id=?`, p.ID)
	}()
	if f, err := e.r.Finish(p.ID, HostResult{}); err != nil || f.Status != StatusDone {
		t.Fatalf("finish = %+v %v", f, err)
	}
}

func TestState_끝난_날과_오늘_옛_기억_판정_수(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 31, 4, 0, 0, 0, kst))
	db := e.s.DB()
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-1','daily','2026-10-25','done',1)`)
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-2','daily','2026-10-23','empty',2)`)
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-3','daily','2026-10-24','failed',3)`)
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-4','daily','2026-10-25','empty',4)`)
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-5','weekly','2026-10-19','done',5)`)
	// 오늘 옛 기억 판정: 끝남 1, 그냥 실패 1(몫을 씀), 로그인 실패 2(한국어·영어, 몫에서 뺌), 어제 1(셈하지 않음)
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-l1','legacy','2026-10-31','done',6)`)
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at,error) VALUES('r-l2','legacy','2026-10-31','failed',7,'no result')`)
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at,error) VALUES('r-l3','legacy','2026-10-31','failed',8,?)`, "x\n"+i18n.TIn(i18n.KO, "retro.signed_out_note"))
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at,error) VALUES('r-l4','legacy','2026-10-31','failed',9,?)`, "x\n"+i18n.TIn(i18n.EN, "retro.signed_out_note"))
	db.Exec(`INSERT INTO retros(id,kind,period,status,started_at) VALUES('r-l5','legacy','2026-10-30','done',10)`)
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-1"})
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "codex", Session: "s-2"})
	MarkEnd(e.s, SessionEnd{Host: "codex", Session: "s-2", EndedAt: e.s.Now().UnixMilli()}, StatusSkipped)
	st, err := e.r.State()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"today":"2026-10-31","finishedDaily":["2026-10-23","2026-10-25"],"legacyRunsToday":2,"queue":1,"pendingProposals":0,"legacy":{"total":0,"judged":0,"left":0}}`
	if got := marshal(t, st); got != want {
		t.Fatalf("state\n got %s\nwant %s", got, want)
	}
	empty := setup(t, time.Date(2026, 10, 31, 4, 0, 0, 0, kst))
	if st, _ := empty.r.State(); marshal(t, st.FinishedDaily) != `[]` {
		t.Fatalf("finishedDaily must be [] not null: %s", marshal(t, st))
	}
}

func TestBackup_작업_기록_백업이_실패해도_기억_백업은_남는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 28, 9, 0, 0, 0, kst))
	if err := os.WriteFile(e.r.Paths.WorkDB(), []byte("SQLite가 아닌 파일"), 0o600); err != nil {
		t.Fatal(err)
	}
	mem, work, errs, err := e.r.Backup()
	if err != nil || work != "" || len(errs) != 1 || !strings.Contains(errs[0], "work log") {
		t.Fatalf("backup = %q %q %v %v", mem, work, errs, err)
	}
	if _, err := os.Stat(mem); err != nil {
		t.Fatalf("memory backup: %v", err)
	}
}
