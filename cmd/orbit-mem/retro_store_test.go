package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/retro"
)

// storeHome is an installed orbit-mem home with empty host transcript roots.
func storeHome(t *testing.T) (string, string) {
	t.Helper()
	h := home(t)
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", t.TempDir())
	seed(t)
	return h, claude
}

// talk writes a Claude session with one user turn at noon of day.
func talk(t *testing.T, claude, day, session, text string) string {
	t.Helper()
	d, _ := time.ParseInLocation("2006-01-02", day, time.Local)
	ts := d.Add(12 * time.Hour).Format(time.RFC3339)
	line := `{"type":"user","timestamp":"` + ts + `","cwd":"/w/app","sessionId":"` + session + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	p := filepath.Join(claude, "projects", "-w-app", session+".jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// storeJSON runs retro-store and decodes its one JSON object.
func storeJSON(t *testing.T, stdin string, args ...string) map[string]any {
	t.Helper()
	out, errOut, code := run(t, stdin, append([]string{"retro-store"}, args...)...)
	if code != 0 {
		t.Fatalf("retro-store %v: code=%d err=%q", args, code, errOut)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("retro-store %v: not one JSON object: %q", args, out)
	}
	return v
}

func keys(v map[string]any) string {
	var ks []string
	for k := range v {
		ks = append(ks, k)
	}
	return strings.Join(sortStrings(ks), ",")
}

func sortStrings(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func dayBefore(n int) string { return time.Now().AddDate(0, 0, -n).Format("2006-01-02") }

// K5: prepare → 호스트 대신 retro_submit(같은 저장 함수) → finish가 예전 흐름과 같은 줄과 사건을 남긴다.
func TestRetroStore_준비_제출_마침이_예전_흐름과_같은_기록을_남긴다(t *testing.T) {
	h, claude := storeHome(t)
	yesterday := dayBefore(1)
	talk(t, claude, yesterday, "s-1", "배포 전에 시험을 돌려 줘")
	p := storeJSON(t, "", "prepare", "--kind", "daily", "--period", yesterday, "--host", "codex")
	if keys(p) != "id,kind,period,session,sourceChecks,status" || p["status"] != "running" || p["kind"] != "daily" || p["period"] != yesterday || p["session"] != "" || p["sourceChecks"] != float64(0) {
		t.Fatalf("prepare = %v", p)
	}
	id := p["id"].(string)
	if _, err := os.Stat(filepath.Join(h, "retro", id, "material.json")); err != nil {
		t.Fatalf("material: %v", err)
	}
	s := seed(t)
	page, _, err := retro.ReadPage(s, paths(), id, 1)
	if err != nil || !strings.Contains(page, "배포 전에 시험을 돌려 줘") {
		t.Fatalf("material page: %v\n%s", err, page)
	}
	if _, err := retro.Submit(s, paths(), id, retro.Submission{Summary: "배포 준비", Journal: &retro.JournalIn{Title: "배포 준비", Body: "-"}}); err != nil {
		t.Fatal(err)
	}
	f := storeJSON(t, `{"model":"haiku","costKnown":true,"costUsd":0.01,"inputTokens":1,"outputTokens":2,"execError":"","text":"끝","signedOut":false}`, "finish", "--id", id)
	if keys(f) != "id,kind,period,status" || f["status"] != "done" || f["id"] != id || f["kind"] != "daily" || f["period"] != yesterday {
		t.Fatalf("finish = %v", f)
	}
	if _, err := os.Stat(filepath.Join(h, "retro", id)); !os.IsNotExist(err) {
		t.Fatalf("material left: %v", err)
	}
	st, _ := retro.Status(s, "daily", yesterday)
	if st.Status != "done" || st.Host != "codex" || st.JournalID == "" || st.Summary != "배포 준비" {
		t.Fatalf("row = %+v", st)
	}
	today := time.Now().Format("2006-01-02")
	evs, _ := s.Events(memory.EventFilter{FromDay: today, ToDay: today, KindPrefix: memory.EventRetroRun})
	if len(evs) != 1 || evs[0].Host != "codex" {
		t.Fatalf("events = %+v", evs)
	}
	var data map[string]any
	json.Unmarshal(evs[0].Data, &data)
	if keys(data) != "costKnown,costUsd,inputTokens,model,ok,outputTokens,retroId,retroKind,sourceChecks" || data["ok"] != true || data["model"] != "haiku" || data["costUsd"] != 0.01 {
		t.Fatalf("event data = %s", evs[0].Data)
	}
	// 끝난 날은 건너뛴다.
	if again := storeJSON(t, "", "prepare", "--kind", "daily", "--period", yesterday); keys(again) != "kind,period,status" || again["status"] != "skipped" {
		t.Fatalf("again = %v", again)
	}
	// 재료 없는 날은 빈 줄을 남기고 끝난다.
	if empty := storeJSON(t, "", "prepare", "--kind", "daily", "--period", dayBefore(2)); keys(empty) != "id,kind,period,status" || empty["status"] != "empty" {
		t.Fatalf("empty = %v", empty)
	}
}

func TestRetroStore_실패한_마침은_결과이고_로그인_안내를_붙인다(t *testing.T) {
	_, claude := storeHome(t)
	yesterday := dayBefore(1)
	talk(t, claude, yesterday, "s-1", "안녕")
	id := storeJSON(t, "", "prepare", "--kind", "daily", "--period", yesterday)["id"].(string)
	f := storeJSON(t, `{"model":"","costKnown":false,"execError":"exit status 1","text":"Not logged in · Please run /login\n","signedOut":true}`, "finish", "--id", id)
	want := i18n.T("retro.host_no_result") + ": exit status 1\nNot logged in · Please run /login\n" + i18n.T("retro.signed_out_note")
	if keys(f) != "error,id,kind,period,signedOut,status" || f["status"] != "failed" || f["error"] != want || f["signedOut"] != true {
		t.Fatalf("finish = %v\nwant error %q", f, want)
	}
	s := seed(t)
	if st, _ := retro.Status(s, "daily", yesterday); st.Status != "failed" || st.Error != want || st.Host != "claude" {
		t.Fatalf("row = %+v", st)
	}
	// 잘못된 입력·없는 id는 명령 실패(종료 1, stderr 한 줄)다.
	if _, errOut, code := run(t, "not json", "retro-store", "finish", "--id", id); code != 1 || strings.Count(strings.TrimSpace(errOut), "\n") != 0 {
		t.Fatalf("bad stdin: code=%d err=%q", code, errOut)
	}
	if _, _, code := run(t, "{}", "retro-store", "finish", "--id", "r-none"); code != 1 {
		t.Fatalf("unknown id: code=%d", code)
	}
}

func TestRetroStore_준비_검사_오류는_종료_1이다(t *testing.T) {
	storeHome(t)
	for _, args := range [][]string{
		{"prepare", "--kind", "daily", "--period", time.Now().Format("2006-01-02")},
		{"prepare", "--kind", "weekly", "--period", dayBefore(1)},
		{"prepare", "--kind", "daily", "--period", "yesterday"},
		{"prepare", "--kind", "monthly", "--period", dayBefore(1)},
		{"prepare", "--kind", "session", "--session-host", "claude"},
	} {
		out, errOut, code := run(t, "", append([]string{"retro-store"}, args...)...)
		if code == 0 || out != "" || strings.TrimSpace(errOut) == "" {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out, errOut)
		}
	}
}

func TestRetroStore_세션_끝_준비와_대기열(t *testing.T) {
	_, claude := storeHome(t)
	today := time.Now().Format("2006-01-02")
	tr := talk(t, claude, today, "sess-a", "세션에서 한 일")
	if q := storeJSON(t, "", "queue", "list"); marshalString(t, q) != `{"items":[]}` {
		t.Fatalf("empty queue = %v", q)
	}
	if q := storeJSON(t, "", "queue", "add", "--host", "claude", "--session", "sess-a", "--transcript", tr, "--cwd", "/w/app", "--ended-at", "1000"); q["queued"] != true {
		t.Fatalf("add = %v", q)
	}
	storeJSON(t, "", "queue", "add", "--host", "claude", "--session", "sess-empty", "--transcript", "/nope.jsonl", "--ended-at", "2000")
	list := storeJSON(t, "", "queue", "list")["items"].([]any)
	if len(list) != 2 || keys(list[0].(map[string]any)) != "cwd,endedAt,host,session,transcript" || list[0].(map[string]any)["session"] != "sess-a" {
		t.Fatalf("list = %v", list)
	}
	p := storeJSON(t, "", "prepare", "--kind", "session", "--host", "codex", "--session-host", "claude", "--session", "sess-a", "--transcript", tr, "--cwd", "/w/app", "--ended-at", "1000")
	if p["status"] != "running" || p["session"] != "claude:sess-a" || p["kind"] != "session" || p["period"] != today {
		t.Fatalf("session prepare = %v", p)
	}
	if m := storeJSON(t, "", "queue", "mark", "--host", "claude", "--session", "sess-a", "--ended-at", "1000", "--as", p["id"].(string)); marshalString(t, m) != `{"ok":true}` {
		t.Fatalf("mark = %v", m)
	}
	// 읽을 기록이 없는 세션은 대기열을 닫고 빈 결과다.
	if e := storeJSON(t, "", "prepare", "--kind", "session", "--session-host", "claude", "--session", "sess-empty", "--transcript", "/nope.jsonl", "--ended-at", "2000"); keys(e) != "kind,period,status" || e["status"] != "empty" {
		t.Fatalf("empty session = %v", e)
	}
	if q := storeJSON(t, "", "queue", "list"); marshalString(t, q) != `{"items":[]}` {
		t.Fatalf("queue after = %v", q)
	}
	// 하루 상한: 오늘 호스트에 간 세션 회고가 이미 1개면 capped로 닫는다.
	tr2 := talk(t, claude, today, "sess-b", "둘째 세션")
	storeJSON(t, "", "queue", "add", "--host", "claude", "--session", "sess-b", "--transcript", tr2, "--ended-at", "3000")
	if c := storeJSON(t, "", "prepare", "--kind", "session", "--session-host", "claude", "--session", "sess-b", "--transcript", tr2, "--ended-at", "3000", "--daily-max", "1"); c["status"] != "capped" {
		t.Fatalf("capped = %v", c)
	}
	s := seed(t)
	var marked string
	s.DB().QueryRow(`SELECT retro_id FROM session_ends WHERE session='sess-b'`).Scan(&marked)
	if marked != "capped" {
		t.Fatalf("sess-b marked %q", marked)
	}
}

func marshalString(t *testing.T, v any) string {
	t.Helper()
	b, _ := json.Marshal(v)
	return string(b)
}

func TestRetroStore_상태_목록_세션_백업_판정_정리(t *testing.T) {
	_, claude := storeHome(t)
	yesterday := dayBefore(1)
	talk(t, claude, yesterday, "s-1", "어제 일")
	st := storeJSON(t, "", "state")
	if keys(st) != "finishedDaily,legacy,legacyRunsToday,pendingProposals,queue,today" || st["today"] != time.Now().Format("2006-01-02") ||
		marshalString(t, st["finishedDaily"]) != `[]` || marshalString(t, st["legacy"]) != `{"judged":0,"left":0,"total":0}` {
		t.Fatalf("state = %v", st)
	}
	if l := storeJSON(t, "", "list"); marshalString(t, l) != `{"pendingProposals":0,"queue":0,"retros":[]}` {
		t.Fatalf("empty list = %v", l)
	}
	storeJSON(t, "", "prepare", "--kind", "daily", "--period", dayBefore(2)) // 빈 날
	if l := storeJSON(t, "", "list", "--n", "1"); len(l["retros"].([]any)) != 1 || l["retros"].([]any)[0].(map[string]any)["status"] != "empty" {
		t.Fatalf("list = %v", l)
	}
	if st := storeJSON(t, "", "state"); marshalString(t, st["finishedDaily"]) != `["`+dayBefore(2)+`"]` {
		t.Fatalf("finishedDaily = %v", st["finishedDaily"])
	}
	ss := storeJSON(t, "", "sessions", "--day", yesterday)
	if marshalString(t, ss) != `{"codexSummaries":0,"sessions":[{"cwd":"/w/app","host":"claude","id":"s-1","turns":1}]}` {
		t.Fatalf("sessions = %v", ss)
	}
	b := storeJSON(t, "", "backup")
	if keys(b) != "errors,memory,work" || b["work"] != "" || marshalString(t, b["errors"]) != `[]` {
		t.Fatalf("backup = %v", b)
	}
	if _, err := os.Stat(b["memory"].(string)); err != nil {
		t.Fatalf("memory backup: %v", err)
	}
	if j := storeJSON(t, "", "judge"); marshalString(t, j) != `{"errors":[]}` {
		t.Fatalf("judge = %v", j)
	}
	if s := storeJSON(t, "", "settle"); marshalString(t, s) != `{"errors":[]}` {
		t.Fatalf("settle = %v", s)
	}
}

func TestRetroStore_설치_전이면_기억_DB를_만들지_않는다(t *testing.T) {
	h := home(t)
	for _, args := range [][]string{{"state"}, {"list"}, {"queue", "list"}, {"queue", "add", "--host", "claude", "--session", "s"}, {"backup"}, {"judge"}, {"settle"},
		{"prepare", "--kind", "tidy"}, {"prepare", "--kind", "work"}, {"finish", "--id", "r-1"}, {"sessions", "--day", dayBefore(1)}} {
		out, errOut, code := run(t, "{}", append([]string{"retro-store"}, args...)...)
		if code != 1 || out != "" || strings.Count(strings.TrimSpace(errOut), "\n") != 0 || errOut == "" {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(h, "memory.db")); !os.IsNotExist(err) {
		t.Fatalf("memory.db created: %v", err)
	}
}

func TestRetroStore_정리와_옛_기억_준비(t *testing.T) {
	storeHome(t)
	today := time.Now().Format("2006-01-02")
	if tidy := storeJSON(t, "", "prepare", "--kind", "tidy"); tidy["status"] != "empty" || tidy["kind"] != "tidy" || tidy["period"] != today || tidy["id"] == nil {
		t.Fatalf("tidy = %v", tidy)
	}
	if legacy := storeJSON(t, "", "prepare", "--kind", "legacy"); marshalString(t, legacy) != `{"kind":"legacy","period":"`+today+`","status":"empty"}` {
		t.Fatalf("legacy = %v", legacy)
	}
	if l := storeJSON(t, "", "list"); len(l["retros"].([]any)) != 1 {
		t.Fatalf("legacy with nothing to judge must keep no row: %v", l)
	}
}

// AU-5: 작업 회고 준비는 stdin의 작업 기록 JSON 하나를 읽는다.
func TestRetroStore_작업_회고_준비(t *testing.T) {
	storeHome(t)
	cwd := t.TempDir()
	ended := time.Now().Add(-time.Hour)
	day := ended.Format("2006-01-02")
	rec := func(ref string, endedAt int64, text string) string {
		b, _ := json.Marshal(map[string]any{"ref": ref, "cwd": cwd, "title": "로그인 고치기", "status": "completed", "endedAt": endedAt, "text": text,
			"userWords": []map[string]any{{"ref": "d-1", "text": "세션 만료도 봐 줘", "at": endedAt, "correction": false}},
			"evidence":  []map[string]any{{"ref": "step s1", "text": "npm test → ok: Tests 5 passed"}}, "recalled": []string{}, "verified": true})
		return string(b)
	}
	p := storeJSON(t, rec("w-1", ended.UnixMilli(), "로그인을 고쳤다"), "prepare", "--kind", "work", "--host", "codex")
	if keys(p) != "id,kind,period,session,sourceChecks,status" || p["status"] != "running" || p["kind"] != "work" || p["period"] != day || p["session"] != "openorbit:w-1" {
		t.Fatalf("prepare = %v", p)
	}
	s := seed(t)
	page, _, err := retro.ReadPage(s, paths(), p["id"].(string), 1)
	if err != nil || !strings.Contains(page, "Tests 5 passed") || !strings.Contains(page, "세션 만료도 봐 줘") {
		t.Fatalf("material: %v\n%s", err, page)
	}
	if again := storeJSON(t, rec("w-1", ended.UnixMilli(), "로그인을 고쳤다"), "prepare", "--kind", "work"); marshalString(t, again) != `{"kind":"work","period":"`+day+`","status":"skipped"}` {
		t.Fatalf("again = %v", again)
	}
	if empty := storeJSON(t, rec("w-2", ended.UnixMilli(), " "), "prepare", "--kind", "work"); keys(empty) != "id,kind,period,status" || empty["status"] != "empty" {
		t.Fatalf("empty = %v", empty)
	}
	bad := func(mut func(map[string]any)) string {
		var v map[string]any
		json.Unmarshal([]byte(rec("w-3", ended.UnixMilli(), "기록")), &v)
		mut(v)
		b, _ := json.Marshal(v)
		return string(b)
	}
	for name, stdin := range map[string]string{
		"not json":    "not json",
		"array":       "[]",
		"two objects": rec("w-3", ended.UnixMilli(), "기록") + rec("w-4", ended.UnixMilli(), "기록"),
		"no ref":      bad(func(v map[string]any) { v["ref"] = "" }),
		"relative":    bad(func(v map[string]any) { v["cwd"] = "app" }),
		"endedAt":     bad(func(v map[string]any) { v["endedAt"] = 0 }),
		"status":      bad(func(v map[string]any) { v["status"] = "done" }),
	} {
		out, errOut, code := run(t, stdin, "retro-store", "prepare", "--kind", "work")
		if code != 1 || out != "" || strings.TrimSpace(errOut) == "" || strings.Count(strings.TrimSpace(errOut), "\n") != 0 {
			t.Errorf("%s: code=%d out=%q err=%q", name, code, out, errOut)
		}
	}
	if _, errOut, code := run(t, rec("w-5", ended.UnixMilli(), "기록"), "retro-store", "prepare", "--kind", "work", "--host", "pi"); code != 1 || errOut == "" {
		t.Errorf("bad host: code=%d", code)
	}
	if l := storeJSON(t, "", "list"); len(l["retros"].([]any)) != 2 {
		t.Fatalf("rows = %v", l)
	}
}
