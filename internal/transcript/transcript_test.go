package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func w(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 2026-10-26 KST 낮 = 같은 날 UTC 03시
const claudeLog = `{"type":"user","timestamp":"2026-10-26T03:00:00.000Z","cwd":"/w/app","sessionId":"s1","isSidechain":false,"message":{"role":"user","content":"로그인 버그 고쳐줘<system-reminder>숨은 지시</system-reminder>"}}
{"type":"assistant","timestamp":"2026-10-26T03:00:05.000Z","cwd":"/w/app","sessionId":"s1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"생각"},{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","timestamp":"2026-10-26T03:00:06.000Z","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","content":"도구 출력 1000줄"}]}}
{"type":"user","timestamp":"2026-10-26T03:00:07.000Z","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","content":"The user answered: \"방식?\"=\"A안\""}]}}
{"type":"assistant","timestamp":"2026-10-26T03:01:00.000Z","sessionId":"s1","message":{"role":"assistant","content":[{"type":"text","text":"고쳤습니다. 시험 통과."}]}}
{"type":"user","timestamp":"2026-10-26T03:02:00.000Z","sessionId":"s1","isMeta":true,"message":{"role":"user","content":"Base directory for this skill"}}
{"type":"user","timestamp":"2026-10-26T03:02:00.000Z","sessionId":"s1","isSidechain":true,"message":{"role":"user","content":"서브에이전트 지시"}}
{"type":"user","timestamp":"2026-10-25T03:00:00.000Z","sessionId":"s1","message":{"role":"user","content":"어제 말"}}
not json
`

const codexLog = `{"timestamp":"2026-10-26T04:00:00.000Z","type":"session_meta","payload":{"id":"c1","cwd":"/w/api"}}
{"timestamp":"2026-10-26T04:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /w/api"}]}}
{"timestamp":"2026-10-26T04:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}
{"timestamp":"2026-10-26T04:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"부하 시험은 스팟에서 해"}]}}
{"timestamp":"2026-10-26T04:00:03.000Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"확인하겠습니다"}]}}
{"timestamp":"2026-10-26T04:00:09.000Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"스팟에서 끝냈습니다"}]}}
{"timestamp":"2026-10-26T04:00:10.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<send_user_message_question_reply> 승인</send_user_message_question_reply>"}]}}
`

func roots(t *testing.T) Roots {
	t.Helper()
	r := Roots{Claude: t.TempDir(), Codex: t.TempDir()}
	w(t, filepath.Join(r.Claude, "projects", "-w-app", "s1.jsonl"), claudeLog)
	w(t, filepath.Join(r.Claude, "projects", "-w-app", "s1", "subagents", "a.jsonl"), claudeLog)
	w(t, filepath.Join(r.Codex, "sessions", "2026", "10", "26", "rollout-x.jsonl"), codexLog)
	w(t, filepath.Join(r.Codex, "memories", "rollout_summaries", "2026-10-26T04-00-00-ab-load_test.md"), "요약: 부하 시험")
	w(t, filepath.Join(r.Codex, "memories", "rollout_summaries", "2026-10-20T04-00-00-ab-old.md"), "옛 요약")
	return r
}

var seoul = time.FixedZone("KST", 9*3600)

func TestCollect_사용자_말과_AI_답만_그날_것으로(t *testing.T) {
	r := roots(t)
	got, err := Collect(r, "2026-10-26", seoul, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("sessions = %d: %+v", len(got), got)
	}
	var all strings.Builder
	for _, s := range got {
		for _, turn := range s.Turns {
			all.WriteString(s.Host + "|" + turn.Role + "|" + turn.Text + "\n")
		}
	}
	text := all.String()
	for _, want := range []string{"claude|user|로그인 버그 고쳐줘", "claude|user|The user answered", "claude|ai|고쳤습니다", "codex|user|부하 시험은 스팟에서 해", "codex|ai|스팟에서 끝냈습니다", "codex|user|<send_user_message_question_reply> 승인"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	for _, bad := range []string{"숨은 지시", "도구 출력", "생각", "Base directory", "서브에이전트", "어제 말", "AGENTS.md", "environment_context", "확인하겠습니다"} {
		if strings.Contains(text, bad) {
			t.Errorf("must not contain %q", bad)
		}
	}
	if got[0].Cwd == "" {
		t.Errorf("cwd missing: %+v", got[0])
	}
}

func TestCollect_회고_실행_디렉터리는_뺀다(t *testing.T) {
	r := roots(t)
	got, _ := Collect(r, "2026-10-26", seoul, []string{"/w/api"})
	for _, s := range got {
		if s.Host == "codex" {
			t.Fatal("excluded cwd included")
		}
	}
}

func TestCodexSummaries_그날_요약만(t *testing.T) {
	r := roots(t)
	got := CodexSummaries(r, "2026-10-26")
	if len(got) != 1 || !strings.Contains(got[0].Text, "부하 시험") {
		t.Fatalf("got %+v", got)
	}
}

func TestHostMemoryFiles_두_호스트의_기억_파일(t *testing.T) {
	r := roots(t)
	w(t, filepath.Join(r.Claude, "projects", "-w-app", "memory", "MEMORY.md"), "# idx")
	w(t, filepath.Join(r.Claude, "projects", "-w-app", "memory", "rule.md"), "규칙")
	w(t, filepath.Join(r.Codex, "memories", "MEMORY.md"), "codex")
	files := HostMemoryFiles(r)
	if len(files) != 3 {
		t.Fatalf("files = %v", files)
	}
}

func TestCollect_서브에이전트와_프로그램_세션은_뺀다(t *testing.T) {
	r := Roots{Claude: t.TempDir(), Codex: t.TempDir()}
	w(t, filepath.Join(r.Claude, "projects", "-obs", "x.jsonl"), `{"type":"user","entrypoint":"sdk-ts","timestamp":"2026-10-26T03:00:00Z","sessionId":"x","message":{"role":"user","content":"관찰자"}}`+"\n")
	w(t, filepath.Join(r.Codex, "sessions", "2026", "10", "26", "sub.jsonl"), `{"timestamp":"2026-10-26T04:00:00Z","type":"session_meta","payload":{"id":"c2","cwd":"/w","parent_thread_id":"p"}}`+"\n"+
		`{"timestamp":"2026-10-26T04:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"하위 작업"}]}}`+"\n")
	got, _ := Collect(r, "2026-10-26", seoul, nil)
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestFit_상한을_넘으면_AI_답부터_줄인다(t *testing.T) {
	long := strings.Repeat("가", 1000)
	var ss []Session
	for i := 0; i < 50; i++ {
		ss = append(ss, Session{Host: "claude", ID: "s", Turns: []Turn{{Role: "user", Text: "사용자 " + long}, {Role: "ai", Text: "AI " + long}}})
	}
	fitted, note := Fit(ss, 100<<10)
	if Size(fitted) > 100<<10 {
		t.Fatalf("size %d", Size(fitted))
	}
	if note == "" {
		t.Fatal("must say it was reduced")
	}
	users := 0
	for _, s := range fitted {
		for _, turn := range s.Turns {
			if turn.Role == "user" {
				users++
			}
		}
	}
	if users < 50 {
		t.Fatalf("user turns must be kept first: %d", users)
	}
	same, note := Fit(ss[:2], 1<<20)
	if note != "" || Size(same) != Size(ss[:2]) {
		t.Fatal("small material must stay")
	}
}
