package claudelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readBasic(t *testing.T) Session {
	t.Helper()
	s, err := ReadSession(filepath.Join("testdata", "projects", "-work-alpha", "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReadSession_세션_머리와_깨진_줄_셈(t *testing.T) {
	s := readBasic(t)
	if s.ID != "s1" || s.Project != "-work-alpha" || s.Cwd != "/work/alpha" {
		t.Fatalf("head: %+v", s)
	}
	if !s.Started.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("started: %v", s.Started)
	}
	if s.BrokenLines != 1 || s.HookContexts != 1 {
		t.Fatalf("broken=%d hook=%d", s.BrokenLines, s.HookContexts)
	}
}

func TestReadSession_단계_순서와_제외(t *testing.T) {
	s := readBasic(t)
	var got []string
	for _, st := range s.Steps {
		got = append(got, string(st.Kind)+":"+string(st.Origin))
	}
	want := []string{
		"request:user", "tool:", "delegate:", // 첫 요청, Read, Agent
		"request:user",   // 슬래시 명령
		"request:system", // caveat(isMeta)
		"request:system", // 명령 출력
		"request:agent",  // 작업 알림
		"request:system", // 중단 표지
		"request:agent",  // sdk 주입
		"request:user",   // 이미지 붙은 요청
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("steps:\n got %v\nwant %v", got, want)
	}
}

func TestReadSession_슬래시_명령_글_정리(t *testing.T) {
	s := readBasic(t)
	st := s.Steps[3]
	if st.Text != "/review src/a.go" {
		t.Fatalf("text=%q", st.Text)
	}
	if !contains(st.Evidence.Markers, "command-name") {
		t.Fatalf("markers=%v", st.Evidence.Markers)
	}
}

func TestReadSession_위임과_도구(t *testing.T) {
	s := readBasic(t)
	if s.Steps[1].Tool != "Read" {
		t.Fatalf("tool: %+v", s.Steps[1])
	}
	d := s.Steps[2]
	if d.Tool != "Agent" || d.Task != "원인을 조사하라" || d.SubagentType != "debugger" || d.Description != "원인 조사" {
		t.Fatalf("delegate: %+v", d)
	}
}

func TestReadSession_근거_칸(t *testing.T) {
	s := readBasic(t)
	e := s.Steps[0].Evidence
	if e.TurnOrigin != "human" || e.OriginKind != "human" || e.PromptSource != "typed" {
		t.Fatalf("evidence: %+v", e)
	}
	if !s.Steps[4].Evidence.IsMeta {
		t.Fatal("isMeta 근거 없음")
	}
	if s.Steps[6].Evidence.OriginKind != "task-notification" || !contains(s.Steps[6].Evidence.Markers, "task-notification") {
		t.Fatalf("task: %+v", s.Steps[6].Evidence)
	}
	if !contains(s.Steps[5].Evidence.Markers, "local-command-stdout") {
		t.Fatalf("stdout: %+v", s.Steps[5].Evidence)
	}
	if !contains(s.Steps[7].Evidence.Markers, "interrupted") {
		t.Fatalf("interrupted: %+v", s.Steps[7].Evidence)
	}
	if s.Steps[9].Text != "다음 요청" {
		t.Fatalf("text=%q", s.Steps[9].Text)
	}
}

func TestReadSession_큰_줄도_읽는다(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("가", 3<<20)
	line := fmt.Sprintf(`{"type":"user","sessionId":"b","cwd":"/work/x","timestamp":"2026-11-01T10:00:00Z","message":{"role":"user","content":%q}}`, big)
	p := filepath.Join(dir, "p", "b.jsonl")
	writeFile(t, p, line+"\n")
	s, err := ReadSession(p)
	if err != nil || len(s.Steps) != 1 || len(s.Steps[0].Text) != len(big) {
		t.Fatalf("err=%v steps=%d", err, len(s.Steps))
	}
}

func TestReadSession_없는_파일은_오류(t *testing.T) {
	if _, err := ReadSession(filepath.Join(t.TempDir(), "none.jsonl")); err == nil {
		t.Fatal("오류가 와야 한다")
	}
}

func TestDefaultRoot_설정_폴더_환경변수(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	if got := DefaultRoot(); got != "/cfg/projects" {
		t.Fatalf("got %q", got)
	}
}

// makeSession은 사람 요청 n개짜리 세션 파일을 쓴다.
func makeSession(t *testing.T, root, project, id, cwd string, day, n int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-11-%02dT10:%02d:00Z","message":{"role":"user","content":"요청 %d"}}`+"\n", id, cwd, day, i, i)
	}
	writeFile(t, filepath.Join(root, project, id+".jsonl"), b.String())
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(ss []Session) string {
	var o []string
	for _, s := range ss {
		o = append(o, s.ID)
	}
	return strings.Join(o, ",")
}

func TestSelect_임시_폴더와_요청_없는_세션은_뺀다(t *testing.T) {
	root := t.TempDir()
	makeSession(t, root, "pa", "ok", "/work/a", 5, 2)
	makeSession(t, root, "pa", "tmp1", "/private/tmp/x", 6, 2)
	makeSession(t, root, "pa", "tmp2", "/var/folders/ab/c", 6, 2)
	makeSession(t, root, "pa", "tmp3", "/tmp", 6, 2)
	writeFile(t, filepath.Join(root, "pa", "empty.jsonl"),
		`{"type":"user","sessionId":"empty","cwd":"/work/a","timestamp":"2026-11-07T10:00:00Z","isMeta":true,"message":{"role":"user","content":"x"}}`+"\n")
	// 서브에이전트 기록은 읽지 않는다.
	writeFile(t, filepath.Join(root, "pa", "ok", "subagents", "agent-1.jsonl"),
		`{"type":"user","sessionId":"ok","cwd":"/work/a","timestamp":"2026-11-08T10:00:00Z","message":{"role":"user","content":"하위"}}`+"\n")
	got, err := Select([]string{root}, Options{MinSessions: 1, MinRequests: 1, MaxSessions: 10, MaxRequests: 100, MaxRequestsPerSession: 12})
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "ok" {
		t.Fatalf("got %s", ids(got))
	}
}

func TestSelect_최근부터_프로젝트를_돌아가며_결정적으로(t *testing.T) {
	root := t.TempDir()
	makeSession(t, root, "pa", "a1", "/w/a", 9, 1)
	makeSession(t, root, "pa", "a2", "/w/a", 8, 1)
	makeSession(t, root, "pa", "a3", "/w/a", 7, 1)
	makeSession(t, root, "pb", "b1", "/w/b", 6, 1)
	makeSession(t, root, "pb", "b2", "/w/b", 5, 1)
	o := Options{MinSessions: 4, MinRequests: 1, MaxSessions: 10, MaxRequests: 100, MaxRequestsPerSession: 12}
	first, err := Select([]string{root}, o)
	if err != nil {
		t.Fatal(err)
	}
	if ids(first) != "a1,b1,a2,b2" {
		t.Fatalf("got %s", ids(first))
	}
	second, _ := Select([]string{root}, o)
	if ids(first) != ids(second) {
		t.Fatal("결정적이지 않음")
	}
}

func TestSelect_세션당_요청_상한과_멈춤_조건(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		makeSession(t, root, "p", fmt.Sprintf("s%d", i), "/w/a", 10-i, 20)
	}
	got, _ := Select([]string{root}, Options{MinSessions: 2, MinRequests: 20, MaxSessions: 10, MaxRequests: 100, MaxRequestsPerSession: 12})
	if ids(got) != "s0,s1" {
		t.Fatalf("got %s", ids(got))
	}
	if n := CountRequests(got[0]); n != 12 {
		t.Fatalf("상한 12인데 %d", n)
	}
}

func TestSelect_전체_상한(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		makeSession(t, root, "p", fmt.Sprintf("s%d", i), "/w/a", 10-i, 3)
	}
	got, _ := Select([]string{root}, Options{MinSessions: 100, MinRequests: 100, MaxSessions: 3, MaxRequests: 100, MaxRequestsPerSession: 12})
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}
