package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// 훅 fail-open(설계 10.2, 15절 L-10, ORBIT-23 D5): 시간 초과·DB 잠금·판 불일치·옛 꼴 DB에서도 훅은 빈 문맥으로
// 성공해 끝난다(Claude Code에서 종료 2는 요청·도구를 막는다). 훅 사건마다, 두 호스트 모두 본다.

type hookCase struct {
	name, event, input, hookName string
	ops                          []string // 이 훅이 부르는 op(시간 한도의 합)
}

var hookCases = []hookCase{
	{"session-start", "session-start", `{"session_id":"F1","cwd":"/tmp","source":"startup"}`, "SessionStart", []string{host.OpContext}},
	{"session-start compact", "session-start", `{"session_id":"F1","cwd":"/tmp","source":"compact"}`, "SessionStart", []string{host.OpReset, host.OpContext}},
	{"prompt", "prompt", `{"session_id":"F1","cwd":"/tmp","prompt":"답은 존댓말로 해 줘"}`, "UserPromptSubmit", []string{host.OpRecall}},
	{"pre-tool", "pre-tool", `{"session_id":"F1","cwd":"/tmp","tool_name":"Bash"}`, "PreToolUse", []string{host.OpToolContext}},
	{"subagent-start", "subagent-start", `{"session_id":"F1","cwd":"/tmp","agent_id":"a1"}`, "SubagentStart", []string{host.OpContext}},
	{"session-end", "session-end", `{"session_id":"F1","cwd":"/tmp"}`, "SessionEnd", []string{host.OpSessionEnd}},
}

var hookHosts = []string{"claude", "codex"}

// limit is the time a hook may take: the sum of its operations' default limits.
func (c hookCase) limit() time.Duration {
	var d time.Duration
	for _, op := range c.ops {
		d += config.Default().HostTimeout(op)
	}
	return d
}

// slack is the time of the process around the operations (flags, the input, printing). It is wide
// on purpose: a lock the test holds for the whole test is far longer than any limit, so "the hook
// ends at its limit, long before the lock goes" holds with room for a loaded machine (-race, parallel).
const slack = time.Second

// failHome is a home with a memory DB that gives every recall hook a context: a pinned user rule.
func failHome(t *testing.T) string {
	t.Helper()
	h := home(t)
	trackHost(t) // 시간 한도를 넘긴 op 고루틴이 끝난 뒤에 시험이 돌아온다
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	pinSave(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Topic: "말투", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "존댓말로"}})
	s.Close()
	return h
}

// checkHookOut says the output is nothing or one hook JSON line of the event.
func checkHookOut(t *testing.T, out, hookName string) {
	t.Helper()
	if out == "" {
		return
	}
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("not one line: %q", out)
	}
	var v struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.HookSpecificOutput.HookEventName != hookName || v.HookSpecificOutput.AdditionalContext == "" {
		t.Fatalf("not a %s hook output: %q (%v)", hookName, out, err)
	}
}

func eachHook(t *testing.T, f func(t *testing.T, c hookCase, hostName string)) {
	for _, c := range hookCases {
		for _, hn := range hookHosts {
			t.Run(c.name+" "+hn, func(t *testing.T) { f(t, c, hn) })
		}
	}
}

func rawConn(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestHook_시간_한도를_넘기면_빈_출력으로_성공한다(t *testing.T) {
	h := failHome(t)
	timeouts := map[string]string{}
	for _, op := range host.Ops {
		timeouts[op] = "1ns"
	}
	b, _ := json.Marshal(map[string]any{"hostTimeouts": timeouts})
	if err := os.WriteFile(filepath.Join(h, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	eachHook(t, func(t *testing.T, c hookCase, hn string) {
		start := time.Now()
		out, errOut, code := run(t, c.input, "hook", c.event, "--host", hn)
		if code != 0 || out != "" {
			t.Fatalf("code %d out %q", code, out)
		}
		if !strings.Contains(errOut, i18n.T("cli.hook.timeout")) {
			t.Fatalf("stderr %q", errOut)
		}
		if d := time.Since(start); d > slack {
			t.Fatalf("took %v", d)
		}
	})
}

// lock holds the DB the way another process may: exclusive locking mode with a write (no other
// connection reads, sqlite.org/wal.html §9), or a WAL write transaction (BEGIN IMMEDIATE: reads go on).
func lock(t *testing.T, path, how string) {
	t.Helper()
	db := rawConn(t, path)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	q := []string{`BEGIN IMMEDIATE`, `INSERT OR REPLACE INTO meta(key, value) VALUES('lock.test', '1')`}
	if how == "exclusive" {
		q = []string{`PRAGMA locking_mode=EXCLUSIVE`, `INSERT OR REPLACE INTO meta(key, value) VALUES('lock.test', '1')`}
	}
	for _, s := range q {
		if _, err := conn.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	// 잠금이 정말 잡혔는지: 다른 연결의 쓰기는 막히고, exclusive면 읽기도 막힌다.
	other, err := sql.Open("sqlite", sqlitedb.FileURI(path)+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('lock.other', '1')`); err == nil {
		t.Fatal("the lock does not hold writes")
	}
	var n int
	readErr := other.QueryRow(`SELECT count(*) FROM memories`).Scan(&n)
	if (how == "exclusive") != (readErr != nil) {
		t.Fatalf("%s lock: read error %v", how, readErr)
	}
}

func TestHook_DB_잠금에도_한도_안에_끝나고_빈_출력이나_올바른_훅_JSON이다(t *testing.T) {
	for _, how := range []string{"exclusive", "immediate"} {
		t.Run(how, func(t *testing.T) {
			h := failHome(t)
			lock(t, filepath.Join(h, "memory.db"), how)
			eachHook(t, func(t *testing.T, c hookCase, hn string) {
				start := time.Now()
				out, _, code := run(t, c.input, "hook", c.event, "--host", hn)
				d := time.Since(start)
				if code != 0 {
					t.Fatalf("code %d", code)
				}
				if d > c.limit()+slack {
					t.Fatalf("took %v, limit %v", d, c.limit())
				}
				checkHookOut(t, out, c.hookName)
				// WAL의 쓰기 잠금은 읽기를 막지 않는다: 시작 문맥은 나온다(표시·기록만 건너뛴다).
				if how == "immediate" && (c.event == "session-start" || c.event == "subagent-start") && !strings.Contains(out, "답은 존댓말") {
					t.Fatalf("no context under a write lock: %q", out)
				}
			})
		})
	}
}

func TestHook_DB_판이_실행_파일보다_높으면_빈_출력으로_성공하고_DB를_바꾸지_않는다(t *testing.T) {
	h := failHome(t)
	path := filepath.Join(h, "memory.db")
	if _, err := rawConn(t, path).Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('schema.version', '99')`); err != nil {
		t.Fatal(err)
	}
	before := homeShape(t, h)
	eachHook(t, func(t *testing.T, c hookCase, hn string) {
		out, _, code := run(t, c.input, "hook", c.event, "--host", hn)
		if code != 0 || out != "" {
			t.Fatalf("code %d out %q", code, out)
		}
	})
	if after := homeShape(t, h); after != before {
		t.Fatalf("changed\nbefore %s\nafter  %s", before, after)
	}
}

// 옛 꼴 DB(0.16 이하가 남긴 꼴: meta 판 없음, user_version 3, 감사 표·retros.session 없음)에 훅을 돌려도
// 이전하지 않고 사본도 만들지 않는다(읽기 op는 이전하지 않는다, D1).
func TestHook_옛_꼴_DB를_이전하지도_사본을_만들지도_않는다(t *testing.T) {
	h := failHome(t)
	path := filepath.Join(h, "memory.db")
	if _, err := rawConn(t, path).Exec(`DROP TABLE audit_acks; DROP TABLE audit_flags; DROP INDEX retros_session;
		ALTER TABLE retros DROP COLUMN session; DELETE FROM meta WHERE key='schema.version'; PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	before := homeShape(t, h)
	if !strings.Contains(before, "user_version=3") || strings.Contains(before, "audit_acks") {
		t.Fatalf("not the old shape: %s", before)
	}
	eachHook(t, func(t *testing.T, c hookCase, hn string) {
		out, _, code := run(t, c.input, "hook", c.event, "--host", hn)
		if code != 0 || out != "" {
			t.Fatalf("code %d out %q", code, out)
		}
	})
	if after := homeShape(t, h); after != before {
		t.Fatalf("changed\nbefore %s\nafter  %s", before, after)
	}
}

// homeShape is what a migration or a copy would change: the DB's schema objects (tables, columns,
// indexes), user_version and meta rows, and the files of the home (copies under backups/).
func homeShape(t *testing.T, h string) string {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(filepath.Join(h, "memory.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var objs, meta string
	var uv int
	if err := db.QueryRow(`SELECT ifnull(group_concat(x, char(10)), '') FROM (SELECT type || ' ' || name || ' ' || ifnull(sql, '') AS x FROM sqlite_master ORDER BY 1)`).Scan(&objs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT ifnull(group_concat(key || '=' || value, ','), '') FROM (SELECT key, value FROM meta ORDER BY key)`).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.WalkDir(h, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(h, p)
		if !strings.HasPrefix(rel, "memory.db") { // 본 DB와 그 -wal·-shm은 위에서 본다
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return objs + "\nuser_version=" + strconv.Itoa(uv) + "\nmeta=" + meta + "\nfiles=" + strings.Join(files, ",")
}

// 설정을 읽지 못해도 훅은 빈 출력으로 성공하고 stderr에 한 줄만 남긴다.
func TestHook_설정을_읽지_못하면_빈_출력으로_성공한다(t *testing.T) {
	h := failHome(t)
	if err := os.WriteFile(filepath.Join(h, "config.json"), []byte(`{"hostTimeouts":{"context":"soon"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	eachHook(t, func(t *testing.T, c hookCase, hn string) {
		out, errOut, code := run(t, c.input, "hook", c.event, "--host", hn)
		if code != 0 || out != "" || strings.Count(errOut, "\n") != 1 || !strings.HasPrefix(errOut, "orbit-mem hook: ") {
			t.Fatalf("code %d out %q err %q", code, out, errOut)
		}
	})
}

// 훅 입력이 끝나지 않아도(호스트가 stdin을 닫지 않음) 훅은 한도 안에 빈 출력으로 끝난다.
func TestHook_입력이_끝나지_않아도_한도_안에_빈_출력으로_성공한다(t *testing.T) {
	failHome(t)
	old := hookInputWait
	hookInputWait = 50 * time.Millisecond
	t.Cleanup(func() { hookInputWait = old })
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	var out, errb strings.Builder
	a := &app{stdin: blockingReader{never}, stdout: &out, stderr: &errb}
	start := time.Now()
	if code := a.main([]string{"hook", "prompt", "--host", "claude"}); code != 0 || out.Len() != 0 {
		t.Fatalf("code %d out %q", code, out.String())
	}
	if d := time.Since(start); d > time.Second || !strings.Contains(errb.String(), i18n.T("cli.hook.timeout")) {
		t.Fatalf("took %v, stderr %q", d, errb.String())
	}
}

type blockingReader struct{ done chan struct{} }

func (b blockingReader) Read(p []byte) (int, error) { <-b.done; return 0, os.ErrClosed }

type panickingReader struct{}

func (panickingReader) Read(p []byte) (int, error) { panic("stdin") }

// 훅 안의 panic(여기서는 입력을 읽다가)도 빈 출력·종료 0이다.
func TestHook_panic에도_빈_출력으로_성공한다(t *testing.T) {
	failHome(t)
	for _, c := range hookCases {
		var out, errb strings.Builder
		a := &app{stdin: panickingReader{}, stdout: &out, stderr: &errb}
		if code := a.main([]string{"hook", c.event, "--host", "claude"}); code != 0 || out.Len() != 0 {
			t.Fatalf("%s: code %d out %q", c.name, code, out.String())
		}
	}
}

// 시작 문맥 뒤의 안내 줄(새 판 확인을 뒤에서 띄움)은 유지보수(사본·정리를 뒤에서 띄움)보다 먼저다: 옛 훅의 차례이고,
// 둘이 같은 때 돌지 않는다(race 검사기로도 본다).
func TestHook_시작_안내_줄이_유지보수보다_먼저다(t *testing.T) {
	h := failHome(t)
	writeCache(t, h, time.Now().Add(-25*time.Hour), nextVersion()) // 낡은 캐시: 안내 줄이 갱신을 띄운다
	var mu sync.Mutex
	var calls []string
	old := startBackground
	startBackground = func(args ...string) error {
		time.Sleep(5 * time.Millisecond) // 같은 때 돌면 차례가 뒤섞이게
		mu.Lock()
		calls = append(calls, strings.Join(args, " "))
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { startBackground = old })
	for i := 0; i < 5; i++ {
		mu.Lock()
		calls = nil
		mu.Unlock()
		os.RemoveAll(filepath.Join(h, "backups"))
		out, _, code := run(t, `{"session_id":"O`+strconv.Itoa(i)+`","cwd":"/tmp","source":"startup"}`, "hook", "session-start", "--host", "claude")
		if code != 0 || !strings.Contains(out, "orbit-mem update") {
			t.Fatalf("code %d out %q", code, out)
		}
		mu.Lock()
		got := slices.Clone(calls)
		mu.Unlock()
		if len(got) < 2 || got[0] != "update --refresh-cache" || got[1] != "backup auto" {
			t.Fatalf("order %v", got)
		}
	}
}
