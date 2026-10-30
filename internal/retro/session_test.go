package retro

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/inject"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/transcript"
)

func (e *env) session(t *testing.T, id string, lines ...string) string {
	t.Helper()
	path := filepath.Join(e.roots.Claude, "projects", "-w-app", id+".jsonl")
	w(t, path, strings.Join(lines, ""))
	return path
}

func readAll(t *testing.T, e *env, id string) string {
	t.Helper()
	var all strings.Builder
	for n := 1; ; n++ {
		page, total, err := ReadPage(e.s, e.r.Paths, id, n)
		if err != nil {
			t.Fatal(err)
		}
		all.WriteString(page)
		if n >= total {
			return all.String()
		}
	}
}

func TestEnqueue_세션마다_한_줄이고_Orbit_폴더_아래는_넣지_않는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 15, 0, 0, 0, kst))
	ok, err := Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s1", Transcript: "/x/s1.jsonl", Cwd: "/w/app"})
	if err != nil || !ok {
		t.Fatalf("enqueue %v %v", ok, err)
	}
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s1", Transcript: "/x/s1b.jsonl", Cwd: "/w/app"})
	if ok, _ := Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s2", Cwd: filepath.Join(e.r.Paths.Home, "retro", "run")}); ok {
		t.Fatal("retro run directory enqueued")
	}
	if ok, _ := Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude"}); ok {
		t.Fatal("no session id enqueued")
	}
	pending, err := Pending(e.s)
	if err != nil || len(pending) != 1 || pending[0].Transcript != "/x/s1b.jsonl" {
		t.Fatalf("pending = %+v %v", pending, err)
	}
}

func TestSession_세션_발화만_읽어_요약_판정_교정_사용판정을_남기고_다음_세션에_주입된다(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, kst)
	e := setup(t, now)
	e.s.Now = func() time.Time { return now.AddDate(0, 0, -3) } // 기억은 교정보다 먼저 있었다
	rule, _ := e.s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 한글 존댓말", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "존댓말"}})
	lesson, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "커밋은 경로를 명시한다", Body: "git add -A 금지", Source: memory.Source{Kind: memory.SourceSession}})
	other, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "넣지 않은 기억", Source: memory.Source{Kind: memory.SourceSession}})
	e.s.Now = func() time.Time { return now }
	e.s.RecordJudgment(memory.Judgment{Kind: memory.KindFact, Title: "그날만 맞는 경로", Verdict: memory.VerdictReject, Reason: "일회성"})
	e.s.RecordEvent(memory.Event{Kind: memory.EventInjectSessionStart, Host: "claude", Session: "sess-x"}, map[string]any{"memoryIds": []string{rule.ID}})
	e.s.RecordEvent(memory.Event{Kind: memory.EventInjectPrompt, Host: "claude", Session: "sess-x"}, map[string]any{"memoryIds": []string{lesson.ID}})
	path := e.session(t, "sess-x",
		claudeTurn("2026-10-27", "13:00", "user", "배포 스크립트는 항상 dry-run부터 돌려줘"),
		claudeTurn("2026-10-27", "13:01", "ai", "dry-run으로 돌렸습니다"),
		claudeTurn("2026-10-27", "13:10", "user", "또 git add -A 썼네. 경로 명시하라고"))
	e.session(t, "sess-other", claudeTurn("2026-10-27", "14:00", "user", "다른 세션의 말"))
	if _, err := Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "sess-x", Transcript: path, Cwd: "/w/app"}); err != nil {
		t.Fatal(err)
	}
	var report string
	e.ai = func(id string) error {
		text := readAll(t, e, id)
		for _, want := range []string{"dry-run부터", "## 이 세션에 넣은 기억", rule.ID, lesson.ID, "git add -A 금지", "## 최근 거절", "일회성"} {
			if !strings.Contains(text, want) {
				return errors.New("material missing " + want + ":\n" + text)
			}
		}
		if strings.Contains(text, "다른 세션의 말") {
			return errors.New("other session leaked")
		}
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{
			Summary: "배포 dry-run과 커밋 경로",
			Memories: []MemoryIn{
				{Kind: "rule", Title: "배포 스크립트는 dry-run부터", Quote: "배포 스크립트는 항상 dry-run부터 돌려줘"},
				{Kind: "lesson", Title: "배포 전에 dry-run 결과를 보여 준다", Verdict: "keep", Reason: "다시 쓸 것"},
				{Kind: "fact", Title: "오늘 배포 대상 경로", Verdict: "hold", Reason: "한 번만 나옴"},
				{Kind: "fact", Title: "오늘 쓴 임시 브랜치 이름", Verdict: "reject", Reason: "일회성"},
			},
			Corrections: []CorrectionIn{{Quote: "경로 명시하라고", SessionRef: "claude:sess-x", MemoryID: lesson.ID, Note: "git add -A"}},
			Usage: []UsageIn{
				{MemoryID: rule.ID, Verdict: "followed"},
				{MemoryID: lesson.ID, Verdict: "followed", Note: "회고가 잘못 봄"},
				{MemoryID: other.ID, Verdict: "irrelevant"},
			},
		})
		return err
	}
	results, err := e.r.DrainSessions(context.Background())
	if err != nil || len(results) != 1 || results[0].Status != StatusDone || results[0].Kind != KindSession {
		t.Fatalf("results = %+v err = %v report = %s", results, err, report)
	}
	if joined := strings.Join(e.calls[0].Args, " "); !strings.Contains(joined, "--model haiku") {
		t.Fatalf("session retro model: %s", joined)
	}
	st, _ := SessionStatus(e.s, "claude:sess-x")
	if st.Status != StatusDone || st.Summary != "배포 dry-run과 커밋 경로" || st.Period != "2026-10-27" {
		t.Fatalf("status = %+v", st)
	}
	// 사용 판정: 넣지 않은 기억은 버리고, 같은 세션 교정이 가리킨 기억은 어김.
	evs, _ := e.s.Events(memory.EventFilter{FromDay: "2026-10-27", ToDay: "2026-10-27", KindPrefix: memory.EventMemoryUsed})
	got := map[string]string{}
	for _, ev := range evs {
		var d struct{ MemoryID, Verdict string }
		json.Unmarshal(ev.Data, &d)
		got[d.MemoryID] = d.Verdict + "@" + ev.Session
	}
	if len(got) != 2 || got[rule.ID] != "followed@sess-x" || got[lesson.ID] != "violated@sess-x" {
		t.Fatalf("usage = %v (%s)", got, report)
	}
	// 쓰기 판정: 거절은 기억 없이, 보류는 제안 없이.
	all, _ := e.s.List()
	byTitle := map[string]memory.Memory{}
	for _, m := range all {
		byTitle[m.Title] = m
	}
	if _, ok := byTitle["오늘 쓴 임시 브랜치 이름"]; ok {
		t.Fatal("rejected candidate saved")
	}
	hold, keep := byTitle["오늘 배포 대상 경로"], byTitle["배포 전에 dry-run 결과를 보여 준다"]
	if hold.State != memory.StateCandidate || keep.State != memory.StateCandidate {
		t.Fatalf("hold=%+v keep=%+v", hold, keep)
	}
	pending, _ := e.s.Proposals(memory.StatusPending)
	for _, p := range pending {
		if p.MemoryIDs[0] == hold.ID {
			t.Fatal("hold candidate got a confirm proposal")
		}
	}
	if len(pending) != 1 || pending[0].MemoryIDs[0] != keep.ID {
		t.Fatalf("pending = %+v", pending)
	}
	if js, _ := e.s.JudgmentsFor(keep.ID); len(js) != 1 || js[0].ProposalID != pending[0].ID || js[0].Session != "claude:sess-x" {
		t.Fatalf("keep judgment = %+v", js)
	}
	rej, _ := e.s.RecentRejections(5)
	if len(rej) != 2 || rej[0].Title != "오늘 쓴 임시 브랜치 이름" {
		t.Fatalf("rejections = %+v", rej)
	}
	// 다음 세션: 인용으로 확정된 규칙은 고정 묶음에, 후보는 작업 회상에.
	bundle := inject.SessionBundle(e.s, config.Default(), "", "claude", "sess-next")
	if !strings.Contains(bundle.Text, "배포 스크립트는 dry-run부터") {
		t.Fatalf("next session bundle:\n%s", bundle.Text)
	}
	recall := inject.Prompt(e.s, config.Default(), "", "배포 전에 dry-run 결과 보여 줘")
	if !strings.Contains(recall, keep.ID) {
		t.Fatalf("next prompt recall: %q", recall)
	}
	// 같은 세션이 다시 끝나도 새 발화가 없으면 빈 회고.
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "sess-x", Transcript: path, Cwd: "/w/app"})
	results, _ = e.r.DrainSessions(context.Background())
	if len(results) != 1 || results[0].Status != StatusEmpty || len(e.calls) != 1 {
		t.Fatalf("second drain = %+v calls=%d", results, len(e.calls))
	}
	// 하루 회고: 세션 끝 회고 요약은 들어가고, 읽힌 발화는 다시 읽지 않는다.
	e.r.Now = func() time.Time { return now.Add(14 * time.Hour) }
	e.s.Now = e.r.Now
	var daily string
	e.ai = func(id string) error {
		daily = readAll(t, e, id)
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "하루", Journal: &JournalIn{Title: "하루", Body: "-"}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-27"); err != nil || res.Status != StatusDone {
		t.Fatalf("daily = %+v %v", res, err)
	}
	if !strings.Contains(daily, "## 세션 끝 회고") || !strings.Contains(daily, "배포 dry-run과 커밋 경로") ||
		strings.Contains(daily, "dry-run부터 돌려줘") || !strings.Contains(daily, "다른 세션의 말") {
		t.Fatalf("daily material:\n%s", daily)
	}
}

func TestSession_하루_회고가_먼저_읽은_발화는_다시_읽지_않는다(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e := setup(t, now)
	path := e.session(t, "sess-y",
		claudeTurn("2026-10-26", "23:00", "user", "어제 밤에 한 말"),
		claudeTurn("2026-10-27", "09:00", "user", "아침에 이어서 한 말"))
	e.ai = func(id string) error {
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "하루", Journal: &JournalIn{Title: "하루", Body: "-"}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil || res.Status != StatusDone {
		t.Fatalf("daily %+v %v", res, err)
	}
	e.r.Now = func() time.Time { return now.Add(6 * time.Hour) }
	e.s.Now = e.r.Now
	var text string
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "아침"})
		return err
	}
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "sess-y", Transcript: path})
	if res, err := e.r.DrainSessions(context.Background()); err != nil || len(res) != 1 || res[0].Status != StatusDone {
		t.Fatalf("session %+v %v", res, err)
	}
	if strings.Contains(text, "어제 밤에 한 말") || !strings.Contains(text, "아침에 이어서 한 말") {
		t.Fatalf("session material:\n%s", text)
	}
}

func TestSession_하루_상한을_넘으면_돌지_않고_하루_회고가_읽는다(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, kst)
	e := setup(t, now)
	e.r.Cfg.SessionRetroDailyMax = 1
	e.ai = func(id string) error {
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "세션"})
		return err
	}
	p1 := e.session(t, "s-1", claudeTurn("2026-10-27", "10:00", "user", "첫 세션"))
	p2 := e.session(t, "s-2", claudeTurn("2026-10-27", "11:00", "user", "둘째 세션"))
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-1", Transcript: p1})
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-2", Transcript: p2})
	results, err := e.r.DrainSessions(context.Background())
	if err != nil || len(results) != 2 || results[0].Status != StatusDone || results[1].Status != StatusCapped || len(e.calls) != 1 {
		t.Fatalf("results = %+v calls = %d err = %v", results, len(e.calls), err)
	}
	if p, _ := Pending(e.s); len(p) != 0 {
		t.Fatalf("capped session stays pending: %+v", p)
	}
	e.r.Now = func() time.Time { return now.Add(14 * time.Hour) }
	e.s.Now = e.r.Now
	var daily string
	e.ai = func(id string) error {
		daily = readAll(t, e, id)
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "하루", Journal: &JournalIn{Title: "하루", Body: "-"}})
		return err
	}
	e.r.Run(context.Background(), KindDaily, "2026-10-27")
	if strings.Contains(daily, "첫 세션") || !strings.Contains(daily, "둘째 세션") {
		t.Fatalf("daily material:\n%s", daily)
	}
}

func TestDrainSessions_잠금이_바쁘면_조용히_끝난다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 15, 0, 0, 0, kst))
	p := e.session(t, "s-1", claudeTurn("2026-10-27", "10:00", "user", "말"))
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-1", Transcript: p})
	unlock, err := e.r.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if res, err := e.r.DrainSessions(context.Background()); err != nil || len(res) != 0 || len(e.calls) != 0 {
		t.Fatalf("busy drain = %+v %v", res, err)
	}
	if pending, _ := Pending(e.s); len(pending) != 1 {
		t.Fatal("pending session lost")
	}
}

func TestCatchUp_먼저_세션_대기열을_비운다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	p := e.session(t, "s-1", claudeTurn("2026-10-26", "22:00", "user", "밤에 끝낸 세션"))
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-1", Transcript: p})
	var kinds []string
	e.ai = func(id string) error {
		st, _ := getStatus(e.s, id)
		kinds = append(kinds, st.Kind)
		sub := Submission{Summary: "x"}
		if st.Kind == KindDaily {
			sub.Journal = &JournalIn{Title: "x", Body: "-"}
		}
		_, err := Submit(e.s, e.r.Paths, id, sub)
		return err
	}
	if _, err := e.r.CatchUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(kinds) < 2 || kinds[0] != KindSession || kinds[1] != KindDaily {
		t.Fatalf("order = %v", kinds)
	}
}

func TestHostOutput_Claude_JSON에서_비용과_결과를_읽는다(t *testing.T) {
	// claude 2.1.303 실제 출력 모양: type이 맨 앞이 아니고 한 줄짜리 객체다.
	out := "경고 한 줄\n" + `{"duration_api_ms":1078,"stop_reason":"end_turn","total_cost_usd":0.0123,"usage":{"input_tokens":1200,"cache_creation_input_tokens":0,"cache_read_input_tokens":300,"output_tokens":45,"iterations":[{"input_tokens":1}]},"result":"제출했습니다","type":"result","is_error":false}` + "\n"
	r := parseHostOutput(out)
	if !r.Known || r.CostUSD != 0.0123 || r.InputTokens != 1500 || r.OutputTokens != 45 || r.Text != "제출했습니다" {
		t.Fatalf("parsed = %+v", r)
	}
	if r := parseHostOutput("codex 출력"); r.Known || r.Text != "codex 출력" {
		t.Fatalf("plain = %+v", r)
	}
	e := setup(t, time.Now())
	c := e.r.hostCommand("r-1", "/tmp/run", "프롬프트", "")
	if !strings.Contains(strings.Join(c.Args, " "), "--output-format json") {
		t.Fatalf("args: %v", c.Args)
	}
}

func TestRun_회고마다_비용_사건을_남긴다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	e.r.Exec = func(ctx context.Context, c Cmd) (string, error) {
		id := strings.TrimPrefix(c.Env[1], "ORBIT_RETRO_ID=")
		Submit(e.s, e.r.Paths, id, Submission{Summary: "인사", Journal: &JournalIn{Title: "인사", Body: "-"}})
		return `{"type":"result","result":"끝","total_cost_usd":0.5,"usage":{"input_tokens":10,"output_tokens":2}}`, nil
	}
	if _, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil {
		t.Fatal(err)
	}
	evs, _ := e.s.Events(memory.EventFilter{FromDay: "2026-10-27", ToDay: "2026-10-27", KindPrefix: memory.EventRetroRun})
	if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"costUsd":0.5`) || !strings.Contains(string(evs[0].Data), `"retroKind":"daily"`) {
		t.Fatalf("events = %+v", evs)
	}
}

func TestWeekly_정리_재료와_교훈_제출(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e := setup(t, now)
	old := now.AddDate(0, 0, -40)
	e.s.Now = func() time.Time { return old }
	unused, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "오래 안 쓴 사실", Source: memory.Source{Kind: memory.SourceSession}})
	e.s.RecordEvent(memory.Event{Kind: memory.EventInjectSessionStart, At: old.UnixMilli(), Day: memory.Day(old)}, map[string]any{"memoryIds": []string{}})
	a, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "회고 예약은 launchd로 건다", Source: memory.Source{Kind: memory.SourceSession}})
	b, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "launchd로 회고 예약을 건다", Source: memory.Source{Kind: memory.SourceSession}})
	e.s.Now = func() time.Time { return now }
	e.s.RecordEvent(memory.Event{Kind: memory.EventInjectPrompt, Session: "s1"}, map[string]any{"memoryIds": []string{a.ID, b.ID}})
	week := now.AddDate(0, 0, -5)
	for i, v := range []string{"irrelevant", "irrelevant", "violated"} {
		id := a.ID
		if v == "violated" {
			id = b.ID
		}
		e.s.RecordEvent(memory.Event{Kind: memory.EventMemoryUsed, At: week.UnixMilli(), Day: memory.Day(week), Session: "s" + string(rune('0'+i))},
			map[string]any{"memoryId": id, "verdict": v, "occurredAt": week.UnixMilli()})
	}
	// 판정된 원장 줄: 증거 있는 도움됨.
	e.s.Now = func() time.Time { return now.AddDate(0, 0, -10) }
	e.s.RecordEvent(memory.Event{Kind: memory.EventCorrection, At: now.AddDate(0, 0, -11).UnixMilli(), Day: memory.Day(now.AddDate(0, 0, -11))},
		map[string]any{"memoryId": a.ID, "occurredAt": now.AddDate(0, 0, -11).UnixMilli()})
	p, _ := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalEdit, Title: "문구", MemoryIDs: []string{a.ID}, Payload: memory.Payload{Body: "분명한 본문"}})
	dr, _ := e.s.Decide(memory.DecideInput{ID: p.ID, Decision: "approve"})
	e.s.Now = func() time.Time { return now }
	if _, err := e.s.JudgeLedger(7); err != nil {
		t.Fatal(err)
	}
	j, _ := e.s.Save(memory.SaveInput{Kind: memory.KindJournal, Title: "2026-10-21 일지: x", State: memory.StateConfirmed, Source: memory.Source{Kind: memory.SourceSession}})
	e.s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at,journal_id) VALUES('r-d','daily','2026-10-21','done',1,2,?)`, j.ID)
	var text, report string
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{Summary: "주간",
			Proposals: []ProposalIn{
				{Kind: "edit", Title: "범위 좁히기", MemoryIDs: []string{a.ID}, NewScope: "project", Project: "/w/app"},
				{Kind: "revert", Title: "되돌리기", LedgerID: dr.LedgerID},
				{Kind: "link", Title: "서로 보완하는 기억", MemoryIDs: []string{a.ID, b.ID}, Relation: "sequence", Note: "같은 배포 절차의 앞뒤"},
			},
			Lessons: []LessonIn{
				{LedgerID: dr.LedgerID, Signal: "회고 예약 문구", Lesson: "예약 기억은 명령까지 적는다", Basis: "교정 1→0", Confidence: "low"},
				{LedgerID: "l-없음", Signal: "x", Lesson: "y", Basis: "z", Confidence: "low"},
			}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindWeekly, "2026-10-19"); err != nil || res.Status != StatusDone {
		t.Fatalf("weekly %+v %v", res, err)
	}
	if !strings.Contains(text, a.ID+" ↔ "+b.ID) && !strings.Contains(text, b.ID+" ↔ "+a.ID) {
		t.Errorf("pair missing")
	}
	for _, want := range []string{"## 오래 쓰지 않은 기억", unused.ID, "## 겹칠 수 있는 기억", "## 사용 판정", "상관없음 2", "어김 1",
		"## 효과 판정", dr.LedgerID, "도움됨"} {
		if !strings.Contains(text, want) {
			t.Errorf("weekly material missing %q", want)
		}
	}
	if t.Failed() {
		t.Fatalf("material:\n%s", text)
	}
	pending, _ := e.s.Proposals(memory.StatusPending)
	kinds := map[string]int{}
	for _, p := range pending {
		kinds[p.Kind]++
	}
	if kinds["edit"] != 1 || kinds["revert"] != 1 || kinds["link"] != 1 {
		t.Fatalf("proposals = %v (%s)", kinds, report)
	}
	for _, p := range pending {
		if p.Kind == "link" && (p.Payload.Note != "앞뒤 절차: 같은 배포 절차의 앞뒤" || len(p.MemoryIDs) != 2) {
			t.Errorf("link 제안: %+v", p)
		}
	}
	entry, _ := e.s.LedgerEntry(dr.LedgerID)
	lesson, err := e.s.Get(entry.LessonID)
	if err != nil || lesson.Kind != memory.KindLesson || lesson.State != memory.StateCandidate || !strings.Contains(lesson.Body, "적용 신호: 회고 예약 문구") {
		t.Fatalf("lesson = %+v %v (%s)", lesson, err, report)
	}
	if !strings.Contains(report, "l-없음") {
		t.Fatalf("bad lesson not reported: %s", report)
	}
}

func TestFitSession_한_세션이_예산을_넘으면_앞부분만_읽는다(t *testing.T) {
	var sess transcript.Session
	sess.Host, sess.ID = "claude", "long"
	base := time.Date(2026, 10, 27, 9, 0, 0, 0, kst)
	for i := 0; i < 20; i++ {
		sess.Turns = append(sess.Turns, transcript.Turn{Role: "user", Text: strings.Repeat("가", 100), At: base.Add(time.Duration(i) * time.Minute)})
	}
	got, note := fitSession(sess, 1500)
	if len(got.Turns) == 0 || len(got.Turns) >= 20 || transcript.Size([]transcript.Session{got}) > 1500 || got.Turns[0].At != base || note == "" {
		t.Fatalf("turns=%d size=%d note=%q", len(got.Turns), transcript.Size([]transcript.Session{got}), note)
	}
	rr := readRanges([]transcript.Session{got})
	if rr[0].To >= base.Add(19*time.Minute).UnixMilli() {
		t.Fatalf("coverage must stop at the last read turn: %+v", rr)
	}
}

func TestSession_훅이_준_경로가_틀리면_세션_id로_찾는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 15, 0, 0, 0, kst))
	e.session(t, "sess-z", claudeTurn("2026-10-27", "10:00", "user", "경로가 틀려도 찾아 줘"))
	e.ai = func(id string) error {
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "찾음"})
		return err
	}
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "sess-z", Transcript: "/없는/경로.jsonl"})
	if res, err := e.r.DrainSessions(context.Background()); err != nil || len(res) != 1 || res[0].Status != StatusDone {
		t.Fatalf("res = %+v %v", res, err)
	}
	// 읽을 기록이 없는 세션은 회고 줄을 남기지 않고 대기열만 닫는다.
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "sess-none", Transcript: "/없는/경로.jsonl"})
	e.r.DrainSessions(context.Background())
	var n int
	e.s.DB().QueryRow(`SELECT COUNT(*) FROM retros WHERE session LIKE '%sess-none'`).Scan(&n)
	if p, _ := Pending(e.s); len(p) != 0 || n != 0 {
		t.Fatalf("pending=%v rows=%d", p, n)
	}
}

func TestDrainSessions_호스트가_로그인되지_않았으면_대기열을_남기고_다음에_다시_돈다(t *testing.T) {
	// 2026-10-29 macOS 재시동: 로그아웃 중 앱이 닫히며 세션 끝 훅이 회고를 띄웠고,
	// 키체인을 못 읽은 claude -p가 아래 출력으로 끝났다(2.1.303 실제 출력에서 줄임).
	claudeOut := `{"total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0},"terminal_reason":"api_error","is_error":true,"result":"Not logged in · Please run /login","type":"result"}` + "\n"
	codexOut := "ERROR: unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: https://api.openai.com/v1/responses\n"
	for _, tc := range []struct{ host, out string }{{"claude", claudeOut}, {"codex", codexOut}} {
		t.Run(tc.host, func(t *testing.T) {
			e := setup(t, time.Date(2026, 10, 29, 9, 10, 45, 0, kst))
			e.r.Cfg.RetroHost = tc.host
			p1 := e.session(t, "s-1", claudeTurn("2026-10-29", "09:00", "user", "첫 세션"))
			p2 := e.session(t, "s-2", claudeTurn("2026-10-29", "09:05", "user", "둘째 세션"))
			Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-1", Transcript: p1})
			Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-2", Transcript: p2})
			calls := 0
			e.r.Exec = func(ctx context.Context, c Cmd) (string, error) {
				calls++
				return tc.out, errors.New("exit status 1")
			}
			results, err := e.r.DrainSessions(context.Background())
			if !errors.Is(err, ErrHostSignedOut) || calls != 1 || len(results) != 1 || results[0].Status != StatusFailed {
				t.Fatalf("signed out drain = %+v calls = %d err = %v", results, calls, err)
			}
			if p, _ := Pending(e.s); len(p) != 2 {
				t.Fatalf("signed-out sessions left the queue: %+v", p)
			}
			// 다시 로그인된 뒤(재시동 뒤 다음 세션 끝이나 04시 예약 회고)에는 남은 세션이 돈다.
			e.r.Exec = func(ctx context.Context, c Cmd) (string, error) {
				calls++
				id := ""
				for _, kv := range c.Env {
					if strings.HasPrefix(kv, "ORBIT_RETRO_ID=") {
						id = strings.TrimPrefix(kv, "ORBIT_RETRO_ID=")
					}
				}
				_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "세션"})
				return "", err
			}
			results, err = e.r.DrainSessions(context.Background())
			if err != nil || len(results) != 2 || results[0].Status != StatusDone || results[1].Status != StatusDone {
				t.Fatalf("retry drain = %+v err = %v", results, err)
			}
			if p, _ := Pending(e.s); len(p) != 0 {
				t.Fatalf("pending after retry: %+v", p)
			}
		})
	}
}

func TestDrainSessions_다른_실패는_대기열을_닫고_하루_회고에_맡긴다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 29, 9, 0, 0, 0, kst))
	p1 := e.session(t, "s-1", claudeTurn("2026-10-29", "08:00", "user", "첫 세션"))
	p2 := e.session(t, "s-2", claudeTurn("2026-10-29", "08:30", "user", "둘째 세션"))
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-1", Transcript: p1})
	Enqueue(e.s, e.r.Paths, SessionEnd{Host: "claude", Session: "s-2", Transcript: p2})
	calls := 0
	e.r.Exec = func(ctx context.Context, c Cmd) (string, error) {
		calls++
		return "모델이 제출하지 않고 끝남", errors.New("exit status 1")
	}
	results, err := e.r.DrainSessions(context.Background())
	if err == nil || errors.Is(err, ErrHostSignedOut) || calls != 2 || len(results) != 2 {
		t.Fatalf("drain = %+v calls = %d err = %v", results, calls, err)
	}
	if p, _ := Pending(e.s); len(p) != 0 {
		t.Fatalf("failed sessions stay pending: %+v", p)
	}
}

func TestHostSignedOut_모델에_닿은_실행은_로그인_실패로_보지_않는다(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		want      bool
	}{
		{"claude 미로그인", `{"total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0},"is_error":true,"result":"Not logged in · Please run /login","type":"result"}`, true},
		{"claude 답에 문구가 든 실행", `{"total_cost_usd":0.02,"usage":{"input_tokens":5000,"output_tokens":300},"is_error":false,"result":"세션 요약: Not logged in · Please run /login 결함을 고쳤다","type":"result"}`, false},
		{"codex 미로그인", "ERROR: Reconnecting... 5/5\nERROR: unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: https://api.openai.com/v1/responses\n", true},
		{"codex 답에 문구가 든 실행", "codex\n결함: Missing bearer or basic authentication 이 나던 일을 고쳤다\ntokens used\n1,234\n", false},
		{"다른 실패", "모델이 제출하지 않고 끝남", false},
	} {
		if got := hostSignedOut(tc.out); got != tc.want {
			t.Errorf("%s: hostSignedOut = %v", tc.name, got)
		}
	}
}
