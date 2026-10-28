package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit/internal/work"
)

type fakeGit struct{ head string }

func (g fakeGit) Toplevel(dir string) string { return "/repo" }
func (g fakeGit) Branch(dir string) string   { return "feat/x" }
func (g fakeGit) Head(dir string) (string, string, []string) {
	if g.head == "" {
		return "", "", nil
	}
	return g.head, "feat: Add X", []string{"a.go", "b_test.go"}
}

func env(host string, vars map[string]string) Env {
	return Env{
		Host:   host,
		Getenv: func(k string) string { return vars[k] },
		Git:    fakeGit{head: "3b9e0c1f7a2d4e6b8c0a1f3e5d7b9c2a4e6f8a0b"},
		Project: func(cwd string) work.Project {
			return work.Project{Scope: "project:github.com/x/repo", Label: "github.com/x/repo"}
		},
		TestCommands: DefaultTestCommands,
		Now:          1000,
	}
}

func probe(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude-2.1.303", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func kinds(p Plan) []string {
	var out []string
	for _, e := range p.Events {
		out = append(out, e.Kind)
	}
	return out
}

func payload[T any](t *testing.T, p Plan, kind string) T {
	t.Helper()
	var v T
	for _, e := range p.Events {
		if e.Kind == kind {
			if err := json.Unmarshal(e.Data, &v); err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	t.Fatalf("%s 사건이 없습니다: %v", kind, kinds(p))
	return v
}

func has(p Plan, kind string) bool {
	for _, e := range p.Events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestSessionStartAndTurns(t *testing.T) {
	p := Translate(probe(t, "SessionStart.1"), env("claude", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "ORBIT_PARENT": "claude:P0:3"}))
	s := payload[work.SessionStart](t, p, work.KindSessionStart)
	if s.Source != "startup" || s.Surface != "headless" || s.Project.Scope != "project:github.com/x/repo" || s.Repo != "/repo" || s.Branch != "feat/x" {
		t.Errorf("세션 시작: %+v", s)
	}
	if s.Parent == nil || s.Parent.Host != "claude" || s.Parent.Session != "P0" || s.Parent.Step != "3" {
		t.Errorf("부모: %+v", s.Parent)
	}
	e := p.Events[0]
	if e.Host != "claude" || e.Session != "ba9cceae-9566-4355-bbc3-e9f153a5018d" || e.Cwd != "/repo" || e.At != 1000 {
		t.Errorf("머리: %+v", e)
	}
	for ep, want := range map[string]string{"cli": "cli", "claude-desktop": "desktop", "sdk-ts": "headless", "": "unknown", "vscode": "unknown"} {
		p := Translate(probe(t, "SessionStart.1"), env("claude", map[string]string{"CLAUDE_CODE_ENTRYPOINT": ep}))
		if got := payload[work.SessionStart](t, p, work.KindSessionStart).Surface; got != want {
			t.Errorf("표면 %q → %s, %s여야 합니다", ep, got, want)
		}
	}
	// 실측(2.1.309, 2026-10-29): 진입점은 부모 것을 물려받지만, 사람이 보는 세션인지는
	// 각 세션이 자기 훅에 CLAUDE_CODE_SESSION_ATTENDED로 새로 넣는다. claude -p는 0이다
	for _, c := range []struct{ ep, attended, want string }{
		{"claude-desktop", "0", "headless"}, // 데스크톱 앱 세션이 띄운 claude -p
		{"cli", "0", "headless"},
		{"claude-desktop", "1", "desktop"},
		{"cli", "1", "cli"},
	} {
		p := Translate(probe(t, "SessionStart.1"), env("claude", map[string]string{"CLAUDE_CODE_ENTRYPOINT": c.ep, "CLAUDE_CODE_SESSION_ATTENDED": c.attended}))
		if got := payload[work.SessionStart](t, p, work.KindSessionStart).Surface; got != c.want {
			t.Errorf("표면 %q·attended %s → %s, %s여야 합니다", c.ep, c.attended, got, c.want)
		}
	}
	p = Translate(probe(t, "UserPromptSubmit.1"), env("claude", nil))
	if got := payload[work.Turn](t, p, work.KindTurnStart).Prompt; got != "d734d3cf-4ce2-4d5a-837d-8632ab1d59be" {
		t.Errorf("요청 id %q", got)
	}
	if strings.Contains(string(p.Events[0].Data), "다음을 순서대로") {
		t.Error("요청 글이 사건에 들어갔습니다")
	}
	p = Translate(probe(t, "Stop.1"), env("claude", nil))
	if got := payload[work.Turn](t, p, work.KindTurnEnd).Prompt; got == "" || p.Events[0].Key == "" {
		t.Errorf("턴 끝: %+v", p.Events)
	}
	p = Translate(probe(t, "SessionEnd.1"), env("claude", nil))
	if got := payload[work.SessionEnd](t, p, work.KindSessionEnd).Reason; got != "other" {
		t.Errorf("세션 끝 이유 %q", got)
	}
}

// 설계 「사전 신호」: TaskCreated·TaskCompleted는 상태 확정에 쓰지 않는다.
func TestPreSignalsIgnored(t *testing.T) {
	for _, n := range []string{"TaskCreated.1", "TaskCompleted.1"} {
		if p := Translate(probe(t, n), env("claude", nil)); len(p.Events) != 0 {
			t.Errorf("%s: %v", n, kinds(p))
		}
	}
}

func TestClaudeTaskTools(t *testing.T) {
	p := Translate(probe(t, "PostToolUse.TaskCreate.1"), env("claude", nil))
	c := payload[work.TaskCreate](t, p, work.KindTaskCreate)
	if c.ID != "1" || c.Subject != "A 준비" || c.List != "ba9cceae-9566-4355-bbc3-e9f153a5018d" {
		t.Errorf("만들기: %+v", c)
	}
	if !p.Sync || !p.ListTool || !has(p, work.KindToolDone) {
		t.Errorf("목록 도구는 동기이고 도구 사후도 남긴다: %+v", p)
	}
	if p.Events[0].Key == "" {
		t.Error("멱등 키가 없습니다")
	}
	p = Translate(probe(t, "PostToolUse.TaskCreate.1"), env("claude", map[string]string{"CLAUDE_CODE_TASK_LIST_ID": "team-list"}))
	if got := payload[work.TaskCreate](t, p, work.KindTaskCreate).List; got != "team-list" {
		t.Errorf("목록 id %q", got)
	}
	// 선행
	u := payload[work.TaskUpdate](t, Translate(probe(t, "PostToolUse.TaskUpdate.1"), env("claude", nil)), work.KindTaskUpdate)
	if u.ID != "3" || strings.Join(u.AddBlockedBy, ",") != "1,2" || u.Status != nil {
		t.Errorf("선행 갱신: %+v", u)
	}
	// 상태와 주인
	u = payload[work.TaskUpdate](t, Translate(probe(t, "PostToolUse.TaskUpdate.2"), env("claude", nil)), work.KindTaskUpdate)
	if u.Status == nil || *u.Status != "in_progress" || u.Owner == nil || *u.Owner != "main" {
		t.Errorf("진행 갱신: %+v", u)
	}
	// metadata.blocked를 넣고 지우기
	u = payload[work.TaskUpdate](t, Translate(probe(t, "PostToolUse.TaskUpdate.3"), env("claude", nil)), work.KindTaskUpdate)
	if u.Blocked == nil || *u.Blocked != "probe" {
		t.Errorf("밝힌 막힘: %+v", u)
	}
	var cleared work.TaskUpdate
	for i := 4; i <= 8; i++ {
		v := payload[work.TaskUpdate](t, Translate(probe(t, "PostToolUse.TaskUpdate."+string(rune('0'+i))), env("claude", nil)), work.KindTaskUpdate)
		if v.ID == "1" && v.Status != nil && *v.Status == "completed" {
			cleared = v
		}
	}
	if cleared.Blocked == nil || *cleared.Blocked != "" {
		t.Errorf("막힘 풀기(metadata.blocked=null): %+v", cleared)
	}
	l := payload[work.TaskState](t, Translate(probe(t, "PostToolUse.TaskList.1"), env("claude", nil)), work.KindTaskState)
	if !l.Full || len(l.Tasks) != 3 || l.Tasks[0].Owner == nil || *l.Tasks[0].Owner != "main" || l.Tasks[1].Owner != nil || l.Tasks[2].Status != "completed" {
		t.Errorf("TaskList: %+v", l)
	}
	g := payload[work.TaskState](t, Translate(probe(t, "PostToolUse.TaskGet.1"), env("claude", nil)), work.KindTaskState)
	if g.Full || len(g.Tasks) != 1 || strings.Join(g.Tasks[0].BlockedBy, ",") != "1,2" {
		t.Errorf("TaskGet: %+v", g)
	}
}

func TestFailedListToolIsNotConfirmed(t *testing.T) {
	raw := strings.Replace(string(probe(t, "PostToolUse.TaskUpdate.2")), `"hook_event_name": "PostToolUse"`, `"hook_event_name": "PostToolUseFailure"`, 1)
	if p := Translate([]byte(raw), env("claude", nil)); has(p, work.KindTaskUpdate) {
		t.Error("실패한 TaskUpdate로 상태를 바꿨습니다")
	}
	raw = strings.Replace(string(probe(t, "PostToolUse.TaskUpdate.2")), `"success": true`, `"success": false`, 1)
	if p := Translate([]byte(raw), env("claude", nil)); has(p, work.KindTaskUpdate) {
		t.Error("success=false인 TaskUpdate로 상태를 바꿨습니다")
	}
}

func TestCommandEvidence(t *testing.T) {
	p := Translate(probe(t, "PostToolUse.Bash.1"), env("claude", nil))
	if has(p, work.KindCommand) || !has(p, work.KindToolDone) || p.Sync {
		t.Errorf("시험 명령이 아닌 echo: %v", kinds(p))
	}
	sub := Translate(probe(t, "PostToolUse.Bash.2"), env("claude", nil))
	if d := payload[work.ToolDone](t, sub, work.KindToolDone); d.Agent != "a3426b33c8357e69d" {
		t.Errorf("서브에이전트 안 도구: %+v", d)
	}
	mk := func(name, cmd string) []byte {
		var m map[string]any
		_ = json.Unmarshal(probe(t, name), &m)
		m["tool_input"].(map[string]any)["command"] = cmd
		b, _ := json.Marshal(m)
		return b
	}
	c := payload[work.Command](t, Translate(mk("PostToolUse.Bash.2", "cd internal && go test ./... -run X"), env("claude", nil)), work.KindCommand)
	if !c.OK || c.Agent != "a3426b33c8357e69d" || c.Text != "cd internal && go test ./... -run X" {
		t.Errorf("성공 시험: %+v", c)
	}
	c = payload[work.Command](t, Translate(mk("PostToolUseFailure.Bash.1", "API_KEY=sk-abcdefghijklmnopqrstuvwx npm test"), env("claude", nil)), work.KindCommand)
	if c.OK || strings.Contains(c.Text, "sk-abc") {
		t.Errorf("실패 시험(비밀값 가림): %+v", c)
	}
	for cmd, want := range map[string]bool{"go test ./...": true, "npm run test": true, "./gradlew test": true, "pytest -q": true, "FOO=1 go test": true,
		"go vet ./...": false, "echo go test": false, "gotest": false, "npm testx": false, "git commit -m 'go test'": false} {
		if got := isTestCommand(cmd, DefaultTestCommands); got != want {
			t.Errorf("%q: %v", cmd, got)
		}
	}
	// 커밋: 성공한 git commit 뒤 HEAD
	cm := payload[work.Commit](t, Translate(mk("PostToolUse.Bash.1", `git add a.go && git commit -m "feat: Add X"`), env("claude", nil)), work.KindCommit)
	if cm.SHA != "3b9e0c1f7a2d4e6b8c0a1f3e5d7b9c2a4e6f8a0b" || cm.Repo != "/repo" || cm.Subject != "feat: Add X" || len(cm.Files) != 2 {
		t.Errorf("커밋: %+v", cm)
	}
	if p := Translate(mk("PostToolUseFailure.Bash.1", `git commit -m x`), env("claude", nil)); has(p, work.KindCommit) {
		t.Error("실패한 git commit을 커밋으로 남겼습니다")
	}
	if p := Translate(mk("PostToolUse.Bash.1", `git log --grep commit`), env("claude", nil)); has(p, work.KindCommit) {
		t.Error("git log를 커밋으로 봤습니다")
	}
	// 출력 본문은 남기지 않는다
	for _, e := range Translate(mk("PostToolUse.Bash.1", "go test ./..."), env("claude", nil)).Events {
		if strings.Contains(string(e.Data), "probe-ok") {
			t.Errorf("명령 출력이 사건에 들어갔습니다: %s", e.Data)
		}
	}
}

func TestSubagents(t *testing.T) {
	s := payload[work.RunStart](t, Translate(probe(t, "SubagentStart.1"), env("claude", nil)), work.KindRunStart)
	if s.Agent != "a3426b33c8357e69d" || s.Type != "general-purpose" || s.Repo != "/repo" {
		t.Errorf("시작: %+v", s)
	}
	rt := payload[work.RunTool](t, Translate(probe(t, "PostToolUse.Agent.1"), env("claude", nil)), work.KindRunTool)
	if rt.Agent != "a3426b33c8357e69d" || !rt.OK || rt.Background || rt.Model != "claude-sonnet-5-5" || rt.Type != "general-purpose" || rt.ToolUse == "" {
		t.Errorf("도구: %+v", rt)
	}
	st := payload[work.RunStop](t, Translate(probe(t, "SubagentStop.1"), env("claude", nil)), work.KindRunStop)
	if st.Agent != "a3426b33c8357e69d" || st.Verdict != nil || !strings.HasSuffix(st.Transcript, "agent-a3426b33c8357e69d.jsonl") {
		t.Errorf("끝: %+v", st)
	}
	var m map[string]any
	_ = json.Unmarshal(probe(t, "SubagentStop.1"), &m)
	m["agent_type"] = "orbit:gate"
	m["last_assistant_message"] = "## 결과\n\n### 전체 판정\n**수정 필요**\n\n### 완료 기준별 판정\n..."
	b, _ := json.Marshal(m)
	st = payload[work.RunStop](t, Translate(b, env("claude", nil)), work.KindRunStop)
	if st.Verdict == nil || st.Verdict.Result != "needs_fix" || st.Verdict.Role != "orbit:gate" {
		t.Errorf("gate 판정: %+v", st.Verdict)
	}
	if strings.Contains(string(Translate(b, env("claude", nil)).Events[0].Data), "완료 기준별") {
		t.Error("마지막 답 본문이 사건에 들어갔습니다")
	}
	// 옛 Claude Code의 서브에이전트 도구 이름 Task는 목록 도구가 아니다
	_ = json.Unmarshal(probe(t, "PostToolUse.Agent.1"), &m)
	m["tool_name"] = "Task"
	b, _ = json.Marshal(m)
	if p := Translate(b, env("claude", nil)); p.ListTool {
		t.Error("Task(서브에이전트)를 목록 도구로 보았습니다")
	} else if rt := payload[work.RunTool](t, p, work.KindRunTool); rt.Agent != "a3426b33c8357e69d" {
		t.Errorf("Task 도구: %+v", rt)
	}
}

// 판정 줄의 모양은 역할 정의가 정한다. 역할 반환 형식을 바꾸면 이 시험이 깨진다(설계 「증거와 검증 결과」).
func TestVerdictMatchesRoleDefinitions(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "orbitplugin", "source", "agents", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	gate, reviewer := read("gate.md"), read("reviewer.md")
	if !strings.Contains(gate, "### 전체 판정\n통과 / 수정 필요 / 사용자 확인 필요") {
		t.Error("gate.md 반환 형식이 바뀌었습니다. 판정 읽기를 함께 고쳐 주세요")
	}
	if !strings.Contains(reviewer, "### 판정\n승인 / 수정 요구") {
		t.Error("reviewer.md 반환 형식이 바뀌었습니다. 판정 읽기를 함께 고쳐 주세요")
	}
	cases := []struct{ role, text, want string }{
		{"orbit:gate", "### 전체 판정\n통과\n", "pass"},
		{"orbit:gate", "### 전체 판정\n\n수정 필요 — 시험 로그가 없습니다", "needs_fix"},
		{"orbit:gate", "### 전체 판정\n사용자 확인 필요\n", "needs_user"},
		{"orbit:gate", "### 전체 판정\n통과 / 수정 필요 / 사용자 확인 필요\n", ""}, // 채우지 않은 틀
		{"orbit:reviewer", "### 판정\n승인", "pass"},
		{"orbit:reviewer", "### 판정\n**수정 요구**", "needs_fix"},
		{"orbit-reviewer", "### 판정\n승인", "pass"},
		{"orbit:worker", "### 판정\n승인", ""},
		{"orbit:gate", "판정 없음", ""},
		// 실제 gate 답(2026-10-28 헤드리스 세션): 틀 대신 한 줄 제목에 판정을 붙였다
		{"orbit:gate", "## 판정: 수정 필요\n\n완료 주장의 세 부분 모두 맞지 않습니다.\n\n### 완료 기준별 판정\n| 기준 | 판정 |", "needs_fix"},
		{"orbit:gate", "**전체 판정:** 통과", "pass"},
		{"orbit:reviewer", "### 판정 — 승인", "pass"},
		{"orbit:gate", "### 완료 기준별 판정\n| a | 충족 |\n\n### 전체 판정\n사용자 확인 필요", "needs_user"},
		{"orbit:gate", "판정을 내리기 어렵습니다", ""},
		// 판정 뒤 설명에 경로가 있어도 판정이다
		{"orbit:reviewer", "### 판정\n수정 요구 (internal/work/x.go 경계)", "needs_fix"},
		{"orbit:gate", "## 판정: 통과 — cmd/oni 시험 로그 확인", "pass"},
		{"orbit:reviewer", "### 판정\n승인 / 수정 요구", ""}, // 채우지 않은 틀
		{"orbit:gate", "### 전체 판정\n수정 필요|통과", ""},
	}
	for _, c := range cases {
		v := parseVerdict(c.role, c.text)
		got := ""
		if v != nil {
			got = v.Result
		}
		if got != c.want {
			t.Errorf("%s %q → %q, %q여야 합니다", c.role, c.text, got, c.want)
		}
	}
}

func TestWaits(t *testing.T) {
	perm := []byte(`{"hook_event_name":"PermissionRequest","session_id":"S1","cwd":"/repo","prompt_id":"P1","tool_name":"Bash","tool_input":{"command":"rm -rf build","description":"x"}}`)
	post := []byte(`{"hook_event_name":"PostToolUse","session_id":"S1","cwd":"/repo","prompt_id":"P1","tool_name":"Bash","tool_use_id":"tu1","tool_input":{"description":"x","command":"rm -rf build"},"tool_response":{"stdout":""}}`)
	w := payload[work.Wait](t, Translate(perm, env("claude", nil)), work.KindWaitOpen)
	d := payload[work.ToolDone](t, Translate(post, env("claude", nil)), work.KindToolDone)
	if w.Key == "" || w.Key != d.Key || w.Prompt != "P1" || w.Tool != "Bash" {
		t.Errorf("기다림 열기·풀기 키: %+v %+v", w, d)
	}
	ask := []byte(`{"hook_event_name":"PreToolUse","session_id":"S1","cwd":"/repo","tool_name":"AskUserQuestion","tool_use_id":"tu2","tool_input":{"questions":[{"question":"어느 쪽?"}]}}`)
	aw := payload[work.Wait](t, Translate(ask, env("claude", nil)), work.KindWaitOpen)
	if aw.Tool != "AskUserQuestion" || strings.Contains(string(Translate(ask, env("claude", nil)).Events[0].Data), "어느 쪽") {
		t.Errorf("질문 기다림(질문 글은 남기지 않음): %+v", aw)
	}
	other := []byte(`{"hook_event_name":"PreToolUse","session_id":"S1","tool_name":"Read","tool_input":{}}`)
	if p := Translate(other, env("claude", nil)); len(p.Events) != 0 {
		t.Errorf("다른 도구 사전 훅: %v", kinds(p))
	}
}

func TestSnapshotTools(t *testing.T) {
	codex := []byte(`{"hook_event_name":"PostToolUse","session_id":"C1","turn_id":"T1","cwd":"/repo","tool_name":"update_plan","tool_use_id":"call_1",
		"tool_input":{"explanation":"계획","plan":[{"step":"A 준비","status":"completed"},{"step":"B 준비","status":"in_progress"},{"step":"C 합치기","status":"pending"}]},"tool_response":"Plan updated"}`)
	p := Translate(codex, env("codex", nil))
	s := payload[work.PlanSnapshot](t, p, work.KindPlanSnapshot)
	if s.List != work.ListCodexPlan || s.ID != "C1" || len(s.Items) != 3 || s.Items[1].Text != "B 준비" || s.Items[1].Status != "in_progress" || !p.Sync {
		t.Errorf("Codex update_plan: %+v", s)
	}
	todo := []byte(`{"hook_event_name":"PostToolUse","session_id":"S1","cwd":"/repo","tool_name":"TodoWrite","tool_use_id":"tu",
		"tool_input":{"todos":[{"content":"A","status":"in_progress","activeForm":"A 하는 중"}]},"tool_response":{}}`)
	s = payload[work.PlanSnapshot](t, Translate(todo, env("claude", nil)), work.KindPlanSnapshot)
	if s.List != work.ListClaudeTodo || s.Items[0].ActiveText != "A 하는 중" {
		t.Errorf("TodoWrite: %+v", s)
	}
	app := []byte(`{"hook_event_name":"PostToolUse","session_id":"D1","cwd":"/repo","tool_name":"mcp__ccd_turn__update_plan","tool_use_id":"tu",
		"tool_input":{"items":[{"content":"A","status":"in_progress"},{"content":"B","status":"pending"}],"scope":"first_task"}}`)
	s = payload[work.PlanSnapshot](t, Translate(app, env("claude", nil)), work.KindPlanSnapshot)
	if s.List != work.ListAppPlan || len(s.Items) != 2 {
		t.Errorf("앱 계획: %+v", s)
	}
	ops := []byte(`{"hook_event_name":"PostToolUse","session_id":"D1","cwd":"/repo","tool_name":"mcp__ccd_turn__update_plan","tool_use_id":"tu2","tool_input":{"done":[2],"start":4}}`)
	o := payload[work.PlanOps](t, Translate(ops, env("claude", nil)), work.KindPlanOps)
	if o.Start != 4 || len(o.Done) != 1 || o.Done[0] != 2 || o.Advance {
		t.Errorf("앱 계획 지시: %+v", o)
	}
	adv := []byte(`{"hook_event_name":"PostToolUse","session_id":"D1","tool_name":"mcp__ccd_turn__update_plan","tool_use_id":"tu3","tool_input":{"advance":true}}`)
	if o := payload[work.PlanOps](t, Translate(adv, env("claude", nil)), work.KindPlanOps); !o.Advance {
		t.Errorf("다음: %+v", o)
	}
	// 실제 데스크톱 앱 세션(2026-10-28): wrap_up이 목록을 닫는다
	wrap := []byte(`{"hook_event_name":"PostToolUse","session_id":"D1","tool_name":"mcp__ccd_turn__wrap_up","tool_use_id":"tu4","tool_input":{"recap":"끝났습니다."}}`)
	p = Translate(wrap, env("claude", nil))
	if o := payload[work.PlanOps](t, p, work.KindPlanOps); !o.Finish || o.Advance || o.List != work.ListAppPlan || o.ID != "D1" || !p.Sync || !p.ListTool {
		t.Errorf("목록 닫기: %+v %+v", o, p)
	}
}

// Codex 표면은 대화 기록 첫 줄(session_meta)의 source·originator로 정한다. 기록이 없을
// 때만 환경 변수로 짐작한다(데스크톱 앱이 띄운 codex exec도 그 변수를 물려받는다).
func TestCodexSurface(t *testing.T) {
	dir := t.TempDir()
	meta := func(name, id, source, originator string) string {
		path := filepath.Join(dir, name)
		line := `{"timestamp":"2026-10-28T22:29:55.071Z","type":"session_meta","payload":{"id":"` + id + `","originator":"` + originator +
			`","cli_version":"0.164.0-alpha.11.1","source":` + source + `,"base_instructions":{"text":"` + strings.Repeat("x", 30000) + `"}}}`
		if err := os.WriteFile(path, []byte(line+"\n"+`{"type":"turn_context"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	start := func(transcript string) []byte {
		b, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": "C1", "transcript_path": transcript, "cwd": "/repo", "source": "startup"})
		return b
	}
	desktopEnv := map[string]string{"CODEX_INTERNAL_ORIGINATOR_OVERRIDE": "Codex Desktop"}
	for _, c := range []struct {
		name, transcript string
		vars             map[string]string
		want             string
	}{
		{"exec", meta("exec.jsonl", "C1", `"exec"`, "codex_exec"), desktopEnv, "headless"},
		{"데스크톱", meta("app.jsonl", "C1", `"vscode"`, "Codex Desktop"), desktopEnv, "desktop"},
		{"TUI", meta("tui.jsonl", "C1", `"cli"`, "codex_cli_rs"), nil, "cli"},
		{"다른 세션의 기록", meta("other.jsonl", "C2", `"exec"`, "codex_exec"), desktopEnv, "desktop"},
		{"기록 없음·데스크톱 변수", filepath.Join(dir, "none.jsonl"), desktopEnv, "desktop"},
		{"기록 없음", "", nil, "unknown"},
	} {
		p := Translate(start(c.transcript), env("codex", c.vars))
		if got := payload[work.SessionStart](t, p, work.KindSessionStart).Surface; got != c.want {
			t.Errorf("%s: %s, %s여야 합니다", c.name, got, c.want)
		}
	}
}

// 목록 도구는 동기 훅이 기록하고 비동기 훅은 건너뛴다(--skip-lists). 동기 훅의
// matcher가 목록 도구를 빠뜨리면 그 도구는 어디에도 기록되지 않는다.
func TestListToolsHaveSyncHook(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "orbitplugin", "source", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var src struct {
		Hooks map[string][]struct {
			Hosts   []string `json:"hosts"`
			Matcher string   `json:"matcher"`
			Command string   `json:"command"`
			Async   bool     `json:"async"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &src); err != nil {
		t.Fatal(err)
	}
	sync := map[string]*regexp.Regexp{}
	for _, h := range src.Hooks["PostToolUse"] {
		if h.Async || strings.Contains(h.Command, "--skip-lists") {
			continue
		}
		for _, host := range h.Hosts {
			sync[host] = regexp.MustCompile("^(?:" + h.Matcher + ")$")
		}
	}
	for host, tools := range map[string][]string{
		"claude": {"TaskCreate", "TaskUpdate", "TaskGet", "TaskList", "TodoWrite", "mcp__ccd_turn__update_plan", "mcp__ccd_turn__wrap_up"},
		"codex":  {"update_plan"},
	} {
		for _, tool := range tools {
			in := []byte(`{"hook_event_name":"PostToolUse","session_id":"S1","tool_name":"` + tool + `","tool_use_id":"tu","tool_input":{}}`)
			if !Translate(in, env(host, nil)).ListTool {
				t.Errorf("%s %s: 목록 도구로 보지 않습니다", host, tool)
			}
			if m := sync[host]; m == nil || !m.MatchString(tool) {
				t.Errorf("%s %s: 동기 PostToolUse 훅 matcher에 없습니다", host, tool)
			}
		}
	}
}

func TestCodexShell(t *testing.T) {
	ok := []byte(`{"hook_event_name":"PostToolUse","session_id":"C1","cwd":"/repo","tool_name":"exec_command","tool_use_id":"c1","tool_input":{"cmd":"go test ./..."},"tool_response":{"exit_code":0}}`)
	c := payload[work.Command](t, Translate(ok, env("codex", nil)), work.KindCommand)
	if !c.OK || c.Text != "go test ./..." {
		t.Errorf("Codex 시험 성공: %+v", c)
	}
	fail := []byte(`{"hook_event_name":"PostToolUse","session_id":"C1","cwd":"/repo","tool_name":"shell","tool_use_id":"c2","tool_input":{"command":["bash","-lc","go test ./..."]},"tool_response":"Exit code: 1\nWall time: 1s"}`)
	c = payload[work.Command](t, Translate(fail, env("codex", nil)), work.KindCommand)
	if c.OK || c.Text != "go test ./..." {
		t.Errorf("Codex 시험 실패: %+v", c)
	}
}

func TestBrokenInputGivesNothing(t *testing.T) {
	for _, raw := range []string{"", "not json", `{"hook_event_name":"PostToolUse"}`, `{"session_id":"S"}`} {
		if p := Translate([]byte(raw), env("claude", nil)); len(p.Events) != 0 {
			t.Errorf("%q: %v", raw, kinds(p))
		}
	}
}
