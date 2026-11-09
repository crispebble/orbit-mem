package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// M2: 훅 조립 지점만 launcher가 human이다. host CLI와 inject 별칭은 계약을 거친 호스트라 host다.
func TestHostLauncher_host_CLI와_inject는_host이고_훅은_human이다(t *testing.T) {
	cases := []struct {
		name, stdin string
		args        []string
		want        string
	}{
		{"host context", `{"host":"orbit-code","session":"oc-1","cwd":"/tmp"}`, []string{"host", "context"}, "host"},
		{"inject prompt", "금요일 배포", []string{"inject", "prompt", "--host", "orbit-code", "--session", "oc-2", "--cwd", "/tmp"}, "host"},
		{"inject session-start", "", []string{"inject", "session-start", "--host", "orbit-code", "--session", "oc-3", "--cwd", "/tmp"}, "host"},
		{"hook", `{"session_id":"oc-4","cwd":"/tmp","source":"startup"}`, []string{"hook", "session-start", "--host", "claude"}, "human"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home(t)
			wait := trackHost(t)
			s := seed(t)
			if _, _, code := run(t, c.stdin, c.args...); code != 0 {
				t.Fatalf("code %d", code)
			}
			wait()
			acts, err := s.Activities(memory.ActivityFilter{})
			if err != nil || len(acts) != 1 || acts[0].Launcher != c.want {
				t.Fatalf("activities = %+v (%v), want launcher %q", acts, err, c.want)
			}
		})
	}
}

func shortInputWait(t *testing.T) chan struct{} {
	t.Helper()
	old := hookInputWait
	hookInputWait = 50 * time.Millisecond
	t.Cleanup(func() { hookInputWait = old })
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	return never
}

func runBlocked(t *testing.T, never chan struct{}, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{stdin: blockingReader{never}, stdout: &out, stderr: &errb}
	code := a.main(args)
	return out.String(), errb.String(), code
}

// L2: 끝나지 않는 stdin은 훅과 같은 입력 대기 한도에서 포기한다.
func TestHostInput_끝나지_않는_입력은_한도에서_오류로_끝난다(t *testing.T) {
	home(t)
	seed(t)
	never := shortInputWait(t)

	out, _, code := runBlocked(t, never, "host", "context")
	var e host.Error
	if code != 1 || json.Unmarshal([]byte(out), &e) != nil || e.Code != host.CodeTimeout || e.Message != i18n.T("cli.host.err.input_wait") {
		t.Fatalf("host: code %d out %q", code, out)
	}
	if _, errOut, code := runBlocked(t, never, "inject", "prompt", "--host", "h", "--session", "s"); code != 1 || !strings.Contains(errOut, i18n.T("cli.host.err.input_wait")) {
		t.Fatalf("inject: code %d err %q", code, errOut)
	}
	out, _, code = runBlocked(t, never, "tool", "memory_find", "--host", "h", "--session", "s")
	var te map[string]string
	if code != 1 || json.Unmarshal([]byte(out), &te) != nil || te["error"] != i18n.T("cli.host.err.input_wait") {
		t.Fatalf("tool: code %d out %q", code, out)
	}
}

// L3: 읽지 못하는 판의 DB. delivered는 DB 없음처럼 조용히 0, session-start·prompt는 stdout 그대로에 stderr 한 줄.
func TestInject_판이_맞지_않는_DB의_출력과_종료_코드(t *testing.T) {
	shapes := map[string]struct {
		prep func(t *testing.T, h string)
		key  string
	}{
		"schema-newer": {func(t *testing.T, h string) {
			if _, err := rawConn(t, h+"/memory.db").Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('schema.version', '99')`); err != nil {
				t.Fatal(err)
			}
		}, "cli.inject.schema_newer"},
		"schema-older": {func(t *testing.T, h string) {
			if _, err := rawConn(t, h+"/memory.db").Exec(`DROP TABLE audit_acks; DROP TABLE audit_flags; DROP INDEX retros_session;
				ALTER TABLE retros DROP COLUMN session; DELETE FROM meta WHERE key='schema.version'; PRAGMA user_version=3`); err != nil {
				t.Fatal(err)
			}
		}, "cli.inject.schema_older"},
	}
	for name, sh := range shapes {
		t.Run(name, func(t *testing.T) {
			h := failHome(t)
			sh.prep(t, h)
			out, errOut, code := run(t, "", "inject", "delivered", "--host", "claude", "--session", "s1", "--ids", "m-1")
			if code != 0 || out != "" || errOut != "" {
				t.Fatalf("delivered: code %d out %q err %q", code, out, errOut)
			}
			for _, args := range [][]string{{"inject", "session-start", "--host", "h", "--session", "s1", "--cwd", "/tmp"}, {"inject", "prompt", "--host", "h", "--session", "s1", "--cwd", "/tmp"}} {
				out, errOut, code := run(t, "답은 존댓말", args...)
				var v struct {
					Text      string
					MemoryIDs []string
				}
				if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v.Text != "" || len(v.MemoryIDs) != 0 || !strings.Contains(out, `"memoryIds": []`) {
					t.Fatalf("%v: code %d out %q", args, code, out)
				}
				if want := "orbit-mem inject: " + i18n.T(sh.key) + "\n"; errOut != want {
					t.Fatalf("%v: stderr %q, want %q", args, errOut, want)
				}
			}
		})
	}
	// DB가 아직 없으면(기억 없음) stderr도 없다.
	home(t)
	if out, errOut, code := run(t, "", "inject", "session-start", "--host", "h", "--session", "s1"); code != 0 || errOut != "" || out == "" {
		t.Fatalf("no db: %d %q %q", code, out, errOut)
	}
}

// L5: 사용법·계약 오류 글은 언어 설정의 카탈로그 글이고 포트의 영어 Message가 섞이지 않는다.
func TestHost_사용법_계약_오류_글은_두_벌_카탈로그에서_나온다(t *testing.T) {
	cases := []struct {
		name, stdin string
		args        []string
		key         string
		args2       []any
	}{
		{"input", `[1]`, []string{"host", "context"}, "cli.host.err.usage.input", nil},
		{"unknown-op", `{}`, []string{"host", "nonsense"}, "cli.host.err.usage.unknown_op", []any{"nonsense"}},
		{"contract", `{"contract":7}`, []string{"host", "context"}, "cli.host.err.contract", []any{"7", "1"}},
		{"bad-value", `{"host":"h","session":"s","source":"zzz"}`, []string{"host", "context"}, "cli.host.err.usage.bad_value", []any{"source", "zzz"}},
		{"missing", `{}`, []string{"host", "call"}, "cli.host.err.usage.missing", []any{"name"}},
	}
	for _, lang := range []string{"", "ko"} {
		for _, c := range cases {
			t.Run(c.name+" "+lang, func(t *testing.T) {
				home(t)
				t.Setenv("ORBIT_LANG", lang)
				if err := os.WriteFile(paths().Config(), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
				out, _, code := run(t, c.stdin, c.args...)
				var e host.Error
				if json.Unmarshal([]byte(out), &e) != nil || (code != 1 && code != 2) {
					t.Fatalf("code %d out %q", code, out)
				}
				want := i18n.T(c.key, c.args2...)
				if strings.Contains(want, c.key) || e.Message != want {
					t.Fatalf("message %q, want %q", e.Message, want)
				}
			})
		}
	}
}

// L5: 쓰기 도구의 timeout 글에는 반영됐을 수 있다는 말이 붙는다. 읽기 도구에는 없다.
func TestHost_쓰기_도구_timeout_글에_반영됐을_수_있음이_붙는다(t *testing.T) {
	home(t)
	wait := trackHost(t)
	seed(t).Close()
	if err := os.WriteFile(paths().Config(), []byte(`{"hostTimeouts":{"call":"1ns"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, `{"name":"memory_save","args":{"kind":"decision","title":"t","body":"b"}}`, "host", "call")
	wait()
	var e host.Error
	if code != 1 || json.Unmarshal([]byte(out), &e) != nil || !e.MayHaveApplied || !strings.Contains(e.Message, i18n.T("cli.host.err.may_have_applied")) {
		t.Fatalf("write: code %d out %s", code, out)
	}
	out, _, _ = run(t, `{"name":"memory_find","args":{"query":"x"}}`, "host", "call")
	wait()
	if strings.Contains(out, i18n.T("cli.host.err.may_have_applied")) {
		t.Fatalf("read: %s", out)
	}
}
