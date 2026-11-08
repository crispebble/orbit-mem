package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/contract"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// trackHost collects the services `orbit-mem host` makes so a test can wait for the operation
// goroutines that outlive a timeout.
func trackHost(t *testing.T) func() {
	t.Helper()
	var mu sync.Mutex
	var svcs []*host.Service
	hostCreated = func(s *host.Service) {
		mu.Lock()
		svcs = append(svcs, s)
		mu.Unlock()
	}
	wait := func() {
		mu.Lock()
		list := append([]*host.Service(nil), svcs...)
		mu.Unlock()
		for _, s := range list {
			s.Wait()
		}
	}
	t.Cleanup(func() { wait(); hostCreated = nil })
	return wait
}

func pairStdin(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil { // 문자열이면 날 stdin 글
		return s
	}
	return string(raw)
}

// resolvedStdin is a pair's input with its "$calls[N]..." strings replaced by the earlier calls' outputs.
func resolvedStdin(t *testing.T, raw json.RawMessage, outs []any) string {
	t.Helper()
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return pairStdin(raw)
	}
	if _, isString := v.(string); isString {
		return pairStdin(raw)
	}
	r, err := contract.Resolve(v, outs)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// 계약 짝 파일(contract/host/v1)을 `orbit-mem host <op>`로 그대로 돌려 종료 코드와 출력을 대조한다.
func TestHostContractPairs(t *testing.T) {
	pairs, err := contract.HostV1()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		t.Run(p.Name, func(t *testing.T) {
			home(t)
			wait := trackHost(t)
			if p.Setup.Config != nil {
				b, _ := json.Marshal(p.Setup.Config)
				if err := os.WriteFile(paths().Config(), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// 짝 README의 차례 그대로: 빈 DB는 인자 없는 migrate 한 번(종료 0), 설정은 $ORBIT_HOME/config.json.
			if p.Setup.DB == "empty" || p.Setup.DB == "memories" {
				if out, errOut, code := run(t, "", "migrate"); code != 0 {
					t.Fatalf("migrate: code %d out %q err %q", code, out, errOut)
				}
			}
			var outs []any // 앞선 호출의 출력("$calls[N]..." 자리표가 가리킨다)
			for _, c := range p.Setup.Calls {
				out, errOut, code := run(t, resolvedStdin(t, c.Input, outs), "host", c.Op)
				if code != 0 {
					t.Fatalf("setup call %s: code %d out %q err %q", c.Op, code, out, errOut)
				}
				var v any
				if err := json.Unmarshal([]byte(out), &v); err != nil {
					t.Fatalf("setup call %s: stdout %q", c.Op, out)
				}
				outs = append(outs, v)
			}
			if len(p.Setup.Meta) > 0 {
				// README의 SQL 그대로: DB의 meta(key, value) 표에 직접 쓴다(키가 있으면 값을 바꾼다).
				db := rawConn(t, paths().DB())
				for k, v := range p.Setup.Meta {
					if _, err := db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
						t.Fatal(err)
					}
				}
			}
			out, errOut, code := run(t, resolvedStdin(t, p.Input, outs), "host", p.Op)
			wait()
			if code != p.Exit {
				t.Fatalf("exit %d, want %d (out %q err %q)", code, p.Exit, out, errOut)
			}
			var got any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("stdout is not one JSON value: %q (%v)", out, err)
			}
			if err := contract.Match(p.Output, got); err != nil {
				t.Fatalf("%v\nstdout: %s", err, out)
			}
		})
	}
}

func TestHost_op가_없거나_넘치면_사용법_오류다(t *testing.T) {
	home(t)
	for _, args := range [][]string{{"host"}, {"host", "version", "extra"}} {
		if out, _, code := run(t, `{}`, args...); code != 2 || out != "" {
			t.Fatalf("%v: code %d out %q", args, code, out)
		}
	}
}

func TestHost_오류_글은_언어_설정을_따른다(t *testing.T) {
	h := home(t)
	t.Setenv("ORBIT_LANG", "ko")
	if err := os.WriteFile(filepath.Join(h, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, `{"name":"memory_find","args":{"query":"x"}}`, "host", "call")
	var e host.Error
	if code != 1 || json.Unmarshal([]byte(out), &e) != nil || e.Code != host.CodeNoDB || !strings.Contains(e.Message, "기억 DB") {
		t.Fatalf("code %d out %s", code, out)
	}
}

func TestHost_시간_한도를_넘기면_timeout_오류를_내고_고루틴이_끝난다(t *testing.T) {
	h := home(t)
	wait := trackHost(t)
	seed(t).Close()
	if err := os.WriteFile(paths().Config(), []byte(`{"hostTimeouts":{"context":"1ns"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, `{"session":"s","cwd":"`+h+`"}`, "host", "context")
	wait()
	if code != 1 || !strings.Contains(out, `"timeout"`) {
		t.Fatalf("code %d out %s", code, out)
	}
}

// 옛 이름 inject·tool은 op 위의 별칭이다: 같은 일(활동 기록, activity_find)을 op를 거쳐 한다.
func TestHostAlias_inject는_활동을_남기고_tool은_activity_find를_받는다(t *testing.T) {
	home(t)
	wait := trackHost(t)
	s := seed(t)
	runInject(t, "", "session-start", "--cwd", "/tmp", "--session", "pi-9", "--host", "host-x")
	wait()
	acts, err := s.Activities(memory.ActivityFilter{})
	if err != nil || len(acts) != 1 || acts[0].Host != "host-x" || acts[0].Session != "pi-9" || acts[0].Launcher != "host" {
		t.Fatalf("activities = %+v (%v)", acts, err)
	}
	out, errOut, code := run(t, `{}`, "tool", "activity_find", "--host", "host-x", "--session", "pi-9")
	wait()
	if code != 0 || !strings.Contains(out, `"items"`) || strings.Contains(out, `"name"`) {
		t.Fatalf("tool activity_find: %d %q %q", code, out, errOut)
	}
}

// 별칭은 옛 출력 꼴(skipped·budget 칸 없음)을 지킨다.
func TestHostAlias_inject_출력에_새_칸이_없다(t *testing.T) {
	home(t)
	seed(t)
	out, _, code := run(t, "", "inject", "session-start", "--host", "host-x")
	var m map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil || len(m) != 2 || m["text"] == nil || m["memoryIds"] == nil {
		t.Fatalf("%d %s", code, out)
	}
}

// 입력을 늦게 주는 reader: 첫 읽기 전에 잠시 잔다(명령이 시각을 입력 읽기 전에 찍는지 보인다).
type slowReader struct {
	r     io.Reader
	delay time.Duration
	slept bool
}

func (s *slowReader) Read(p []byte) (int, error) {
	if !s.slept {
		s.slept = true
		time.Sleep(s.delay)
	}
	return s.r.Read(p)
}

func runSlow(t *testing.T, stdin string, delay time.Duration, args ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{stdin: &slowReader{r: strings.NewReader(stdin), delay: delay}, stdout: &out, stderr: &errb}
	if code := a.main(args); code != 0 {
		t.Fatalf("%v: code %d out %q err %q", args, code, out.String(), errb.String())
	}
}

// 조립 지점이 ORBIT_PARENT를 활동 부모로 넣고, 활동 시각은 stdin을 읽기 전에 찍은 시각이다.
func TestHostAssembly_부모와_활동_시각(t *testing.T) {
	const delay = 400 * time.Millisecond
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"host context", `{"host":"test","session":"s-host","cwd":"/tmp"}`, []string{"host", "context"}},
		{"inject session-start", "", []string{"inject", "session-start", "--host", "test", "--session", "s-inj", "--cwd", "/tmp"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home(t)
			wait := trackHost(t)
			t.Setenv(parentEnv, "parent-session-1")
			s := seed(t)
			before := time.Now().UnixMilli()
			runSlow(t, c.stdin, delay, c.args...)
			wait()
			acts, err := s.Activities(memory.ActivityFilter{})
			if err != nil || len(acts) != 1 {
				t.Fatalf("activities = %+v (%v)", acts, err)
			}
			a := acts[0]
			if a.Parent != "parent-session-1" || a.Launcher != "host" {
				t.Errorf("parent %q launcher %q", a.Parent, a.Launcher)
			}
			if a.StartedAt < before || a.StartedAt >= before+delay.Milliseconds()-100 {
				t.Errorf("startedAt %d is not just after %d (stdin came %dms later)", a.StartedAt, before, delay.Milliseconds())
			}
		})
	}
}
