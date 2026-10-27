package retro

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/transcript"
)

var kst = time.FixedZone("KST", 9*3600)

type env struct {
	r     *Runner
	s     *memory.Store
	roots transcript.Roots
	calls []Cmd
	ai    func(id string) error // 호스트 대신 도는 가짜 AI
}

func w(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T, now time.Time) *env {
	t.Helper()
	home := t.TempDir()
	paths := config.Paths{Home: home}
	s, err := memory.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.Now = func() time.Time { return now }
	e := &env{s: s, roots: transcript.Roots{Claude: t.TempDir(), Codex: t.TempDir()}}
	e.r = &Runner{Store: s, Cfg: config.Default(), Paths: paths, Roots: e.roots, Loc: kst, Now: func() time.Time { return now }, Oni: "/opt/oni",
		Exec: func(ctx context.Context, c Cmd) (string, error) {
			e.calls = append(e.calls, c)
			if e.ai == nil {
				return "", nil
			}
			id := ""
			for _, kv := range c.Env {
				if strings.HasPrefix(kv, "ORBIT_RETRO_ID=") {
					id = strings.TrimPrefix(kv, "ORBIT_RETRO_ID=")
				}
			}
			return "", e.ai(id)
		}}
	return e
}

func claudeTurn(day, hhmm, role, text string) string {
	ts := day + "T" + hhmm + ":00+09:00"
	if role == "user" {
		return `{"type":"user","timestamp":"` + ts + `","cwd":"/w/app","sessionId":"s","message":{"role":"user","content":` + q(text) + `}}` + "\n"
	}
	return `{"type":"assistant","timestamp":"` + ts + `","cwd":"/w/app","sessionId":"s","message":{"role":"assistant","content":[{"type":"text","text":` + q(text) + `}]}}` + "\n"
}

func q(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func (e *env) talk(t *testing.T, day string, lines ...string) {
	t.Helper()
	w(t, filepath.Join(e.roots.Claude, "projects", "-w-app", "s-"+day+".jsonl"), strings.Join(lines, ""))
}

func TestDaily_재료가_없으면_호스트를_부르지_않고_끝낸다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusEmpty || len(e.calls) != 0 {
		t.Fatalf("res=%+v calls=%d", res, len(e.calls))
	}
	again, _ := e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if again.Status != StatusSkipped {
		t.Fatalf("second run = %+v", again)
	}
}

func TestDaily_재료를_읽고_제출하면_일지_후보_제안이_남는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26",
		claudeTurn("2026-10-26", "10:00", "user", "커밋은 항상 경로를 명시해서 해. git add -A 쓰지마"),
		claudeTurn("2026-10-26", "10:01", "ai", "알겠습니다"),
		claudeTurn("2026-10-26", "11:00", "user", "로그인 버그 고쳐줘"),
		claudeTurn("2026-10-26", "11:30", "ai", "고쳤습니다. 원인은 세션 만료 처리였습니다."))
	old, _ := e.s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: "세션 만료는 미들웨어에서", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceSession}, Day: "2026-10-25"})

	e.ai = func(id string) error {
		var all strings.Builder
		for n := 1; ; n++ {
			page, total, err := ReadPage(e.s, e.r.Paths, id, n)
			if err != nil {
				return err
			}
			all.WriteString(page)
			if n >= total {
				break
			}
		}
		text := all.String()
		if !strings.Contains(text, "커밋은 항상 경로를") || !strings.Contains(text, old.ID) {
			return errors.New("material incomplete: " + text)
		}
		_, err := Submit(e.s, e.r.Paths, id, Submission{
			Summary: "로그인 버그 수정",
			Journal: &JournalIn{Title: "로그인 버그 수정", Body: "한 일: 세션 만료 버그 수정\n막힌 것: 없음\n이어 갈 것: 배포"},
			Memories: []MemoryIn{
				{Kind: "rule", Title: "커밋은 경로를 명시한다", Body: "git add -A 금지", Quote: "커밋은 항상 경로를 명시해서 해.", Scope: "global"},
				{Kind: "lesson", Title: "로그인 오류는 세션 만료부터 본다", Scope: "project", Project: "/w/app"},
				{Kind: "lesson", Title: "세션 만료는 미들웨어에서", SameAs: old.ID},
				{Kind: "rule", Title: "가짜 인용", Quote: "사용자가 하지 않은 말"},
			},
			Flags:     []string{"같은 버그를 두 번 고침"},
			Proposals: []ProposalIn{{Kind: "direction", Title: "시험을 먼저 쓰자", Body: "버그 재발"}},
		})
		return err
	}
	res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDone || len(e.calls) != 1 {
		t.Fatalf("res=%+v calls=%d", res, len(e.calls))
	}
	all, _ := e.s.List()
	byTitle := map[string]memory.Memory{}
	for _, m := range all {
		byTitle[m.Title] = m
	}
	if m := byTitle["커밋은 경로를 명시한다"]; m.State != memory.StateConfirmed {
		t.Errorf("verified quote must confirm: %+v", m)
	}
	if m := byTitle["가짜 인용"]; m.State != memory.StateCandidate {
		t.Errorf("unverified quote must stay candidate: %+v", m)
	}
	if m := byTitle["로그인 오류는 세션 만료부터 본다"]; m.State != memory.StateCandidate || !strings.HasPrefix(m.Scope, "project:") {
		t.Errorf("project candidate: %+v", m)
	}
	if m, _ := e.s.Get(old.ID); m.State != memory.StateConfirmed {
		t.Errorf("seen on second day must confirm: %+v", m)
	}
	var journal memory.Memory
	for _, m := range all {
		if m.Kind == memory.KindJournal {
			journal = m
		}
	}
	if !strings.Contains(journal.Title, "2026-10-26") {
		t.Errorf("journal = %+v", journal)
	}
	pending, _ := e.s.Proposals(memory.StatusPending)
	kinds := map[string]int{}
	for _, p := range pending {
		kinds[p.Kind]++
	}
	if kinds["confirm"] != 2 || kinds["direction"] != 1 {
		t.Errorf("proposals = %v", kinds)
	}
	st, _ := Status(e.s, KindDaily, "2026-10-26")
	if st.Status != StatusDone || !strings.Contains(strings.Join(st.Flags, ","), "두 번") {
		t.Errorf("status = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(e.r.Paths.RetroDir(), res.ID)); !os.IsNotExist(err) {
		t.Errorf("material must be removed after success")
	}
}

func TestDaily_제출하지_않으면_실패로_남고_다음에_다시_돈다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if err == nil || res.Status != StatusFailed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	e.ai = func(id string) error {
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "인사", Journal: &JournalIn{Title: "인사만", Body: "-"}})
		return err
	}
	res, err = e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if err != nil || res.Status != StatusDone {
		t.Fatalf("retry res=%+v err=%v", res, err)
	}
}

func TestSubmit_한번만_받는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	var second error
	e.ai = func(id string) error {
		sub := Submission{Summary: "x", Journal: &JournalIn{Title: "x", Body: "x"}}
		if _, err := Submit(e.s, e.r.Paths, id, sub); err != nil {
			return err
		}
		_, second = Submit(e.s, e.r.Paths, id, sub)
		return nil
	}
	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if second == nil {
		t.Fatal("second submit accepted")
	}
}

func TestDaily_호스트_기억_변경은_한번만_재료가_된다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	w(t, filepath.Join(e.roots.Claude, "projects", "-w-app", "memory", "pnpm.md"), "이 앱은 pnpm을 쓴다")
	var seen []string
	e.ai = func(id string) error {
		page, _, _ := ReadPage(e.s, e.r.Paths, id, 1)
		seen = append(seen, page)
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "흡수", Journal: &JournalIn{Title: "흡수", Body: "-"}})
		return err
	}
	if res, _ := e.r.Run(context.Background(), KindDaily, "2026-10-26"); res.Status != StatusDone {
		t.Fatalf("first = %+v", res)
	}
	if !strings.Contains(seen[0], "이 앱은 pnpm을 쓴다") {
		t.Fatalf("host memory not in material: %s", seen[0])
	}
	if res, _ := e.r.Run(context.Background(), KindDaily, "2026-10-27"); res.Status != StatusEmpty {
		t.Fatalf("unchanged host memory must not be material again: %+v", res)
	}
}

func TestDaily_출처_파일이_바뀐_기억은_정리_제안이_된다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "rule.md")
	w(t, f, "v1")
	m, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "파일 근거", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceFile, Ref: f, Hash: memory.FileHash(f)}, State: memory.StateConfirmed})
	w(t, f, "v2")
	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	pending, _ := e.s.Proposals(memory.StatusPending)
	if len(pending) != 1 || pending[0].Kind != memory.ProposalStale || pending[0].MemoryIDs[0] != m.ID {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestCatchUp_밀린_날을_따라잡고_주간도_돈다(t *testing.T) {
	// 2026-10-27은 화요일. 지난주(10-19~10-25)의 주간 회고가 돌아야 한다.
	e := setup(t, time.Date(2026, 10, 27, 9, 0, 0, 0, kst))
	e.s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at) VALUES('r-old','daily','2026-10-23','done',1,1)`)
	e.s.DB().Exec(`INSERT INTO retros(id,kind,period,status,started_at,finished_at,journal_id) VALUES('r-j','daily','2026-10-22','done',1,1,'m-j')`)
	e.talk(t, "2026-10-25", claudeTurn("2026-10-25", "10:00", "user", "일요일 작업"))
	e.ai = func(id string) error {
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "ok", Journal: &JournalIn{Title: "t", Body: "b"}})
		return err
	}
	results, err := e.r.CatchUp(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range results {
		got = append(got, r.Kind+":"+r.Period+":"+r.Status)
	}
	want := "daily:2026-10-24:empty,daily:2026-10-25:done,daily:2026-10-26:empty,weekly:2026-10-19:done"
	if strings.Join(got, ",") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
	if _, err := os.Stat(filepath.Join(e.r.Paths.Backups())); err != nil {
		t.Fatalf("daily backup missing: %v", err)
	}
}

func TestCatchUp_처음이면_어제만(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 28, 9, 0, 0, 0, kst)) // 수요일
	results, _ := e.r.CatchUp(context.Background())
	if len(results) != 2 || results[0].Period != "2026-10-27" || results[1].Kind != KindWeekly {
		t.Fatalf("results = %+v", results)
	}
}

func TestLock_동시에_두_번_돌지_않는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 9, 0, 0, 0, kst))
	unlock, err := e.r.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := e.r.CatchUp(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
}

func TestHostCommand_Claude와_Codex(t *testing.T) {
	e := setup(t, time.Now())
	c := e.r.hostCommand("r-1", "/tmp/run", "프롬프트")
	joined := strings.Join(c.Args, " ")
	for _, want := range []string{"-p", "--strict-mcp-config", "--no-session-persistence", "mcp__orbit-retro__retro_submit", `"--retro"`, "ORBIT_HOME"} {
		if !strings.Contains(joined, want) {
			t.Errorf("claude args missing %q: %s", want, joined)
		}
	}
	e.r.Cfg.RetroHost, e.r.Cfg.RetroModel = "codex", "gpt-x"
	c = e.r.hostCommand("r-1", "/tmp/run", "프롬프트")
	joined = strings.Join(c.Args, " ")
	for _, want := range []string{"exec", "--ephemeral", "--skip-git-repo-check", "-m gpt-x", "mcp_servers.orbit-retro.command", "-C /tmp/run"} {
		if !strings.Contains(joined, want) {
			t.Errorf("codex args missing %q: %s", want, joined)
		}
	}
}
