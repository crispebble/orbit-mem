// Package hook turns host hook input (Claude Code, Codex) into work ledger
// events. It reads only the hook's stdin JSON, a few environment variables and
// git; it never reads the host's own task files (설계 「원칙」).
package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/work"
)

// Git reads the repository facts evidence and runs carry.
type Git interface {
	Toplevel(dir string) string
	Branch(dir string) string
	Head(dir string) (sha, subject string, files []string)
}

// Env is what Translate may consult besides the hook input.
type Env struct {
	Host         string // claude | codex (훅 인자)
	Getenv       func(string) string
	Git          Git
	Project      func(cwd string) work.Project
	TestCommands []string
	Now          int64  // 밀리초. 훅 프로세스가 입력을 읽기 전에 찍은 시각
	Launcher     string // 세션을 띄운 쪽(origin.launcher). 비우면 남기지 않는다
	NoDecisions  bool   // 질문 도구를 결정 카드로 열지 않는다(orbit-pi가 decision_open으로 연다)
}

// DefaultTestCommands are common test commands (설계 O6). PC 설정이 더한다.
var DefaultTestCommands = []string{
	"go test", "npm test", "npm run test", "pnpm test", "pnpm run test", "yarn test", "bun test",
	"pytest", "python -m pytest", "python3 -m pytest", "gradle test", "./gradlew test", "mvn test", "./mvnw test",
	"cargo test", "swift test", "dotnet test", "flutter test", "dart test",
}

// Plan is what a hook call should record. Sync says the host waits for this
// hook (list tools keep their order within a session).
type Plan struct {
	Events   []work.Event
	Sync     bool
	ListTool bool
}

type input struct {
	Event          string          `json:"hook_event_name"`
	Session        string          `json:"session_id"`
	Cwd            string          `json:"cwd"`
	Transcript     string          `json:"transcript_path"`
	Prompt         string          `json:"prompt_id"`
	Turn           string          `json:"turn_id"`
	Source         string          `json:"source"`
	Reason         string          `json:"reason"`
	Model          json.RawMessage `json:"model"`
	Tool           string          `json:"tool_name"`
	ToolUse        string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	Agent          string          `json:"agent_id"`
	AgentType      string          `json:"agent_type"`
	AgentTrans     string          `json:"agent_transcript_path"`
	LastMessage    string          `json:"last_assistant_message"`
	StopHookActive bool            `json:"stop_hook_active"`
}

func (in input) prompt() string {
	if in.Prompt != "" {
		return in.Prompt
	}
	return in.Turn
}

// Translate reads one hook input. Bad or unknown input gives an empty plan.
func Translate(raw []byte, env Env) Plan {
	var in input
	if json.Unmarshal(raw, &in) != nil || in.Session == "" || in.Event == "" {
		return Plan{}
	}
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	t := translator{in: in, env: env}
	t.run()
	return t.plan
}

type translator struct {
	in   input
	env  Env
	plan Plan
}

func (t *translator) add(kind, key string, payload any) {
	e := work.NewEvent(kind, t.env.Host, t.in.Session, t.env.Now, payload)
	e.Cwd = t.in.Cwd
	if key != "" {
		e.Key = t.env.Host + ":" + t.in.Session + ":" + key
	}
	t.plan.Events = append(t.plan.Events, e)
}

func (t *translator) run() {
	in := t.in
	switch in.Event {
	case "SessionStart":
		t.sessionStart()
	case "UserPromptSubmit":
		t.add(work.KindTurnStart, keyOf("prompt", in.prompt()), work.Turn{Prompt: in.prompt()})
	case "Stop":
		t.add(work.KindTurnEnd, keyOf("stop", in.prompt()), work.Turn{Prompt: in.prompt()})
	case "SessionEnd":
		t.add(work.KindSessionEnd, "", work.SessionEnd{Reason: work.Clean(in.Reason, 80)})
	case "PermissionRequest":
		t.add(work.KindWaitOpen, "", work.Wait{Key: toolKey(in.Tool, in.ToolInput), Prompt: in.prompt(), Tool: in.Tool, Agent: in.Agent})
	case "PreToolUse":
		if isQuestionTool(in.Tool) {
			t.add(work.KindWaitOpen, keyOf("pre", in.ToolUse), work.Wait{Key: toolKey(in.Tool, in.ToolInput), Prompt: in.prompt(), Tool: in.Tool, Agent: in.Agent})
			t.openQuestion()
		}
	case "PostToolUse", "PostToolUseFailure":
		t.postTool(in.Event == "PostToolUseFailure")
	case "SubagentStart":
		dir := in.Cwd
		t.add(work.KindRunStart, keyOf("substart", in.Agent), work.RunStart{Agent: in.Agent, Type: in.AgentType, Repo: t.toplevel(dir), Branch: t.branch(dir)})
	case "SubagentStop":
		stop := work.RunStop{Agent: in.Agent, Type: in.AgentType, Transcript: in.AgentTrans}
		msg := in.LastMessage
		if msg == "" && in.AgentTrans != "" && isVerdictRole(in.AgentType) {
			msg = lastAssistantText(in.AgentTrans)
		}
		stop.Verdict = parseVerdict(in.AgentType, msg)
		t.add(work.KindRunStop, "", stop)
	}
}

func keyOf(kind, id string) string {
	if id == "" {
		return ""
	}
	return kind + ":" + id
}

// ---- 세션 ----

func (t *translator) sessionStart() {
	in := t.in
	p := work.SessionStart{Source: in.Source, Surface: t.surface(), Transcript: in.Transcript, ListID: t.env.Getenv("CLAUDE_CODE_TASK_LIST_ID")}
	if t.env.Host != "claude" {
		p.ListID = ""
	}
	if in.Cwd != "" {
		if t.env.Project != nil {
			p.Project = t.env.Project(in.Cwd)
		}
		p.Repo, p.Branch = t.toplevel(in.Cwd), t.branch(in.Cwd)
	}
	var model string
	if json.Unmarshal(in.Model, &model) != nil {
		var m struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		}
		_ = json.Unmarshal(in.Model, &m)
		model = m.ID
		if model == "" {
			model = m.Slug
		}
	}
	p.Model = work.Clean(model, 80)
	p.Parent = parseParent(t.env.Getenv("ORBIT_PARENT"))
	p.Launcher = t.env.Launcher
	t.add(work.KindSessionStart, "", p)
}

// surface says where the session runs. Entry point variables are inherited
// by a session started from inside another one, so each host's own
// per-session signal comes first.
func (t *translator) surface() string {
	if t.env.Host == "codex" {
		source, originator := codexMeta(t.in.Transcript, t.in.Session)
		switch source {
		case "exec":
			return "headless"
		case "cli":
			return "cli"
		}
		if originator == "" {
			originator = t.env.Getenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE")
		}
		switch v := strings.ToLower(originator); {
		case strings.Contains(v, "exec"):
			return "headless"
		case strings.Contains(v, "desktop"):
			return "desktop"
		case v == "codex_cli_rs":
			return "cli"
		}
		return "unknown" // 모르는 originator는 짐작하지 않는다
	}
	// 각 세션이 자기 훅에 새로 넣는다(물려받지 않음). claude -p는 0
	if t.env.Getenv("CLAUDE_CODE_SESSION_ATTENDED") == "0" {
		return "headless"
	}
	switch ep := t.env.Getenv("CLAUDE_CODE_ENTRYPOINT"); {
	case ep == "cli":
		return "cli"
	case ep == "claude-desktop":
		return "desktop"
	case strings.HasPrefix(ep, "sdk"):
		return "headless"
	}
	return "unknown"
}

// codexMeta reads source and originator from the session_meta line Codex
// writes first in its transcript (before SessionStart). A line from another
// session gives nothing. A resumed session appends to the same transcript and
// keeps that one line (실측 codex-cli 0.164), so it keeps the surface it
// started on even when resumed elsewhere.
func codexMeta(path, session string) (source, originator string) {
	if path == "" {
		return "", ""
	}
	line, err := readFirstLine(path, 1<<20)
	if err != nil {
		return "", ""
	}
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			ID         string          `json:"id"`
			Source     json.RawMessage `json:"source"` // "exec"·"cli"·"vscode"·{"subagent":…}
			Originator string          `json:"originator"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "session_meta" || rec.Payload.ID != session {
		return "", ""
	}
	_ = json.Unmarshal(rec.Payload.Source, &source)
	return source, rec.Payload.Originator
}

// parseParent reads ORBIT_PARENT=<host>:<session>:<step>. host orbit-pi is the
// orchestrator: its step is a plan task id (orbit-pi-plan).
func parseParent(v string) *work.ParentRef {
	parts := strings.SplitN(strings.TrimSpace(v), ":", 3)
	if len(parts) < 2 || parts[1] == "" || (parts[0] != "claude" && parts[0] != "codex" && parts[0] != work.HostOrbitPi) {
		return nil
	}
	p := &work.ParentRef{Host: parts[0], Session: parts[1]}
	if len(parts) == 3 {
		p.Step = parts[2]
	}
	return p
}

// ---- 도구 사후 ----

func (t *translator) postTool(failed bool) {
	in := t.in
	switch {
	case t.env.Host == "claude" && strings.HasPrefix(in.Tool, "Task") && in.Tool != "Task" && in.Tool != "TaskStop" && in.Tool != "TaskOutput":
		t.plan.Sync, t.plan.ListTool = true, true
		if !failed {
			t.claudeTask()
		}
	case in.Tool == "TodoWrite" || in.Tool == "mcp__ccd_turn__update_plan" || in.Tool == "mcp__ccd_turn__wrap_up" || in.Tool == "update_plan":
		t.plan.Sync, t.plan.ListTool = true, true
		if !failed {
			t.snapshot()
		}
	case in.Tool == "Agent" || (in.Tool == "Task" && t.env.Host == "claude"):
		t.agentTool(failed)
	case isShell(in.Tool):
		t.shell(failed)
	case isQuestionTool(in.Tool):
		t.closeQuestion(failed)
	}
	key := keyOf("post", in.ToolUse)
	t.add(work.KindToolDone, key, work.ToolDone{Key: toolKey(in.Tool, in.ToolInput), Tool: in.Tool, Agent: in.Agent})
}

func (t *translator) list() string {
	if id := t.env.Getenv("CLAUDE_CODE_TASK_LIST_ID"); id != "" {
		return id
	}
	return t.in.Session
}

func (t *translator) claudeTask() {
	in := t.in
	key := keyOf("task", in.ToolUse)
	switch in.Tool {
	case "TaskCreate":
		var args struct {
			Subject    string         `json:"subject"`
			ActiveForm string         `json:"activeForm"`
			Owner      string         `json:"owner"`
			Metadata   map[string]any `json:"metadata"`
		}
		var res struct {
			Task struct {
				ID      json.RawMessage `json:"id"`
				Subject string          `json:"subject"`
			} `json:"task"`
		}
		_ = json.Unmarshal(in.ToolInput, &args)
		if json.Unmarshal(in.ToolResponse, &res) != nil || idString(res.Task.ID) == "" {
			return // 성공 응답으로만 확정한다
		}
		subject := res.Task.Subject
		if subject == "" {
			subject = args.Subject
		}
		blocked, _ := args.Metadata["blocked"].(string)
		t.add(work.KindTaskCreate, key, work.TaskCreate{List: t.list(), ID: idString(res.Task.ID), Subject: work.Clean(subject, 200),
			ActiveForm: work.Clean(args.ActiveForm, 200), Owner: work.Clean(args.Owner, 80), Blocked: work.Clean(blocked, 200)})
	case "TaskUpdate":
		var args map[string]json.RawMessage
		var res struct {
			Success *bool           `json:"success"`
			TaskID  json.RawMessage `json:"taskId"`
		}
		_ = json.Unmarshal(in.ToolInput, &args)
		if json.Unmarshal(in.ToolResponse, &res) != nil || res.Success == nil || !*res.Success {
			return
		}
		id := idString(res.TaskID)
		if id == "" {
			id = idString(args["taskId"])
		}
		if id == "" {
			return
		}
		u := work.TaskUpdate{List: t.list(), ID: id}
		u.Status = optString(args["status"], 40)
		u.Subject = optString(args["subject"], 200)
		u.ActiveForm = optString(args["activeForm"], 200)
		u.Owner = optString(args["owner"], 80)
		if raw, ok := args["metadata"]; ok {
			var meta map[string]json.RawMessage
			if json.Unmarshal(raw, &meta) == nil {
				if b, ok := meta["blocked"]; ok {
					var s string
					_ = json.Unmarshal(b, &s) // null이면 빈 글: 풀림
					s = work.Clean(s, 200)
					u.Blocked = &s
				}
			}
		}
		u.AddBlockedBy = idList(args["addBlockedBy"])
		u.AddBlocks = idList(args["addBlocks"])
		t.add(work.KindTaskUpdate, key, u)
	case "TaskList", "TaskGet":
		var res struct {
			Tasks []json.RawMessage `json:"tasks"`
			Task  json.RawMessage   `json:"task"`
		}
		if json.Unmarshal(in.ToolResponse, &res) != nil {
			return
		}
		raws := res.Tasks
		full := in.Tool == "TaskList" && res.Tasks != nil
		if in.Tool == "TaskGet" {
			if len(res.Task) == 0 || string(res.Task) == "null" {
				return
			}
			raws = []json.RawMessage{res.Task}
		}
		st := work.TaskState{List: t.list(), Full: full, Tasks: []work.TaskItem{}}
		for _, raw := range raws {
			var m map[string]json.RawMessage
			if json.Unmarshal(raw, &m) != nil {
				continue
			}
			it := work.TaskItem{ID: idString(m["id"]), Owner: optString(m["owner"], 80)}
			if s := optString(m["subject"], 200); s != nil {
				it.Subject = *s
			}
			if s := optString(m["status"], 40); s != nil {
				it.Status = *s
			}
			if _, ok := m["blockedBy"]; ok {
				it.BlockedBy = nonNil(idList(m["blockedBy"]))
			}
			if it.ID != "" {
				st.Tasks = append(st.Tasks, it)
			}
		}
		t.add(work.KindTaskState, key, st)
	}
}

func (t *translator) snapshot() {
	in := t.in
	key := keyOf("plan", in.ToolUse)
	switch in.Tool {
	case "TodoWrite":
		var args struct {
			Todos []struct {
				Content    string `json:"content"`
				Status     string `json:"status"`
				ActiveForm string `json:"activeForm"`
			} `json:"todos"`
		}
		if json.Unmarshal(in.ToolInput, &args) != nil {
			return
		}
		s := work.PlanSnapshot{List: work.ListClaudeTodo, ID: in.Session, Items: []work.PlanItem{}}
		for _, it := range args.Todos {
			s.Items = append(s.Items, work.PlanItem{Text: work.Clean(it.Content, 200), Status: it.Status, ActiveText: work.Clean(it.ActiveForm, 200)})
		}
		t.add(work.KindPlanSnapshot, key, s)
	case "update_plan":
		var args struct {
			Plan []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		}
		if json.Unmarshal(in.ToolInput, &args) != nil || args.Plan == nil {
			return
		}
		s := work.PlanSnapshot{List: work.ListCodexPlan, ID: in.Session, Items: []work.PlanItem{}}
		for _, it := range args.Plan {
			s.Items = append(s.Items, work.PlanItem{Text: work.Clean(it.Step, 200), Status: it.Status})
		}
		t.add(work.KindPlanSnapshot, key, s)
	case "mcp__ccd_turn__update_plan":
		var args struct {
			Items []struct {
				Content string `json:"content"`
				Status  string `json:"status"`
			} `json:"items"`
			Advance bool  `json:"advance"`
			Done    []int `json:"done"`
			Start   int   `json:"start"`
		}
		if json.Unmarshal(in.ToolInput, &args) != nil {
			return
		}
		if args.Items != nil {
			s := work.PlanSnapshot{List: work.ListAppPlan, ID: in.Session, Items: []work.PlanItem{}}
			for _, it := range args.Items {
				s.Items = append(s.Items, work.PlanItem{Text: work.Clean(it.Content, 200), Status: it.Status})
			}
			t.add(work.KindPlanSnapshot, key, s)
			return
		}
		if args.Advance || len(args.Done) > 0 || args.Start > 0 {
			t.add(work.KindPlanOps, key, work.PlanOps{List: work.ListAppPlan, ID: in.Session, Advance: args.Advance, Done: args.Done, Start: args.Start})
		}
	case "mcp__ccd_turn__wrap_up":
		// 앱 설명: wrap_up이 목록을 체크해 닫으므로 마지막 update_plan이 없다
		t.add(work.KindPlanOps, key, work.PlanOps{List: work.ListAppPlan, ID: in.Session, Finish: true})
	}
}

func (t *translator) agentTool(failed bool) {
	in := t.in
	var args struct {
		Type       string `json:"subagent_type"`
		Background bool   `json:"run_in_background"`
	}
	var res struct {
		Status string `json:"status"`
		Agent  string `json:"agentId"`
		Model  string `json:"resolvedModel"`
	}
	_ = json.Unmarshal(in.ToolInput, &args)
	_ = json.Unmarshal(in.ToolResponse, &res)
	t.add(work.KindRunTool, keyOf("agent", in.ToolUse), work.RunTool{ToolUse: in.ToolUse, Agent: res.Agent, Type: args.Type,
		Background: args.Background || res.Status == "async_launched", OK: !failed, Model: work.Clean(res.Model, 80)})
}

// ---- 명령 ----

func isShell(tool string) bool {
	switch tool {
	case "Bash", "shell", "exec_command", "local_shell", "container.exec", "unified_exec":
		return true
	}
	return false
}

func shellCommand(raw json.RawMessage) string {
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	for _, k := range []string{"command", "cmd"} {
		v, ok := args[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		var parts []string
		if json.Unmarshal(v, &parts) == nil {
			// ["bash","-lc","go test ./..."] 꼴이면 셸에 넘긴 글을 쓴다
			if len(parts) >= 3 && (parts[1] == "-lc" || parts[1] == "-c") {
				return parts[2]
			}
			return strings.Join(parts, " ")
		}
	}
	return ""
}

var exitCodeLine = regexp.MustCompile(`(?m)^\s*(?:Exit code|exit code|exit_code)[:=]\s*(-?\d+)`)

// shellOK decides success. Claude Code reports failure as PostToolUseFailure;
// Codex puts the exit code in the response.
func (t *translator) shellOK(failed bool) bool {
	if failed {
		return false
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(t.in.ToolResponse, &res) == nil {
		for _, k := range []string{"exit_code", "exitCode"} {
			var code int
			if json.Unmarshal(res[k], &code) == nil && res[k] != nil {
				return code == 0
			}
		}
		var interrupted bool
		if json.Unmarshal(res["interrupted"], &interrupted) == nil && interrupted {
			return false
		}
		return true
	}
	var s string
	if json.Unmarshal(t.in.ToolResponse, &s) == nil {
		if m := exitCodeLine.FindStringSubmatch(s); m != nil {
			return m[1] == "0"
		}
	}
	return true
}

func (t *translator) shell(failed bool) {
	cmd := shellCommand(t.in.ToolInput)
	if cmd == "" {
		return
	}
	ok := t.shellOK(failed)
	if isTestCommand(cmd, t.env.TestCommands) {
		t.add(work.KindCommand, keyOf("cmd", t.in.ToolUse), work.Command{Text: work.Clean(cmd, 200), OK: ok, Agent: t.in.Agent})
	}
	if dir, yes := commitDir(cmd, t.in.Cwd); yes && ok && t.env.Git != nil {
		sha, subject, files := t.env.Git.Head(dir)
		if sha != "" {
			t.add(work.KindCommit, keyOf("commit", t.in.ToolUse), work.Commit{Repo: t.toplevel(dir), SHA: sha, Subject: work.Clean(subject, 200), Files: files, Agent: t.in.Agent})
		}
	}
}

// segments splits a shell line at && || ; | and newlines.
func segments(cmd string) []string {
	return regexp.MustCompile(`&&|\|\||;|\||\n`).Split(cmd, -1)
}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*$`)

// words of a segment with leading VAR=value assignments removed.
func words(seg string) []string {
	f := strings.Fields(seg)
	for len(f) > 0 && envAssign.MatchString(f[0]) {
		f = f[1:]
	}
	if len(f) > 0 && (f[0] == "time" || f[0] == "command") {
		f = f[1:]
	}
	return f
}

// isTestCommand says whether any segment starts with one of the test commands.
func isTestCommand(cmd string, list []string) bool {
	for _, seg := range segments(cmd) {
		w := words(seg)
		for _, c := range list {
			cw := strings.Fields(c)
			if len(cw) == 0 || len(w) < len(cw) {
				continue
			}
			match := true
			for i := range cw {
				if w[i] != cw[i] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// commitDir finds a git commit in the line and the directory it ran in.
func commitDir(cmd, cwd string) (string, bool) {
	dir := cwd
	for _, seg := range segments(cmd) {
		w := words(seg)
		if len(w) >= 2 && w[0] == "cd" {
			if filepath.IsAbs(w[1]) {
				dir = w[1]
			} else if dir != "" {
				dir = filepath.Join(dir, w[1])
			}
			continue
		}
		if len(w) == 0 || w[0] != "git" {
			continue
		}
		d := dir
		for i := 1; i < len(w); i++ {
			switch {
			case w[i] == "-C" && i+1 < len(w):
				if filepath.IsAbs(w[i+1]) {
					d = w[i+1]
				} else {
					d = filepath.Join(d, w[i+1])
				}
				i++
			case w[i] == "-c" && i+1 < len(w):
				i++
			case strings.HasPrefix(w[i], "-"):
			case w[i] == "commit":
				return d, true
			default:
				i = len(w)
			}
		}
	}
	return "", false
}

func (t *translator) toplevel(dir string) string {
	if t.env.Git == nil || dir == "" {
		return ""
	}
	return t.env.Git.Toplevel(dir)
}

func (t *translator) branch(dir string) string {
	if t.env.Git == nil || dir == "" {
		return ""
	}
	return t.env.Git.Branch(dir)
}

// ---- 기다림 ----

func isQuestionTool(tool string) bool {
	return tool == "AskUserQuestion" || tool == "request_user_input"
}

// toolKey pairs a permission request or question with its post-call event:
// the same tool with the same input. Hook inputs give no shared id.
func toolKey(tool string, input json.RawMessage) string {
	var v any
	_ = json.Unmarshal(input, &v)
	b, _ := json.Marshal(canonical(v)) // 칸 순서와 상관없이 같은 값
	h := sha256.Sum256(append([]byte(tool+"\x00"), b...))
	return hex.EncodeToString(h[:12])
}

func canonical(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([][2]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, [2]any{k, canonical(x[k])})
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, y := range x {
			out[i] = canonical(y)
		}
		return out
	}
	return v
}

// ---- 판정 ----

func isVerdictRole(role string) bool {
	r := roleBase(role)
	return r == "gate" || r == "reviewer"
}

func roleBase(role string) string {
	r := strings.ToLower(role)
	if i := strings.LastIndex(r, ":"); i >= 0 {
		r = r[i+1:]
	}
	return strings.TrimPrefix(r, "orbit-")
}

// parseVerdict reads the verdict under the role's heading (agents/gate.md
// 「전체 판정」, agents/reviewer.md 「판정」). Models do not always keep the
// template: the verdict may follow the heading on the same line
// ("## 판정: 수정 필요") or on the next non-empty line. The first heading wins;
// "완료 기준별 판정" is a table, not the verdict. A heading that does not read
// as a verdict (an unfilled template, a sentence) gives way to the next one.
func parseVerdict(role, text string) *work.Verdict {
	base := roleBase(role)
	if (base != "gate" && base != "reviewer") || text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		rest, ok := verdictHeading(l)
		if !ok {
			continue
		}
		if rest == "" {
			for _, next := range lines[i+1:] {
				if rest = cleanVerdict(next); rest != "" {
					break
				}
			}
		}
		if v := verdictOf(role, rest); v != nil {
			return v
		}
	}
	return nil
}

func cleanVerdict(s string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "*_`#>:：—-– "))
}

// verdictHeading says whether a line is the verdict line (판정: …, what the
// role definitions ask for) or a verdict heading, and returns what follows it
// on the same line.
func verdictHeading(l string) (string, bool) {
	t := strings.TrimSpace(l)
	marked := strings.HasPrefix(t, "#") || strings.HasPrefix(t, "**")
	t = strings.TrimLeft(t, "#* ")
	for _, h := range []string{"전체 판정", "판정"} {
		rest, ok := strings.CutPrefix(t, h)
		if !ok {
			continue
		}
		if r := strings.TrimSpace(rest); !marked && !strings.HasPrefix(r, ":") && !strings.HasPrefix(r, "：") {
			return "", false // 꾸밈 없는 줄은 판정 줄 모양만 받는다
		}
		return cleanVerdict(rest), true
	}
	return "", false
}

func verdictOf(role, v string) *work.Verdict {
	for _, w := range []struct{ word, result string }{{"사용자 확인 필요", "needs_user"}, {"수정 필요", "needs_fix"}, {"수정 요구", "needs_fix"}, {"통과", "pass"}, {"승인", "pass"}} {
		if rest, ok := strings.CutPrefix(v, w.word); ok {
			if r := strings.TrimSpace(rest); strings.HasPrefix(r, "/") || strings.HasPrefix(r, "|") {
				return nil // 채우지 않은 틀(통과 / 수정 필요 / …). 뒤 설명의 경로는 판정을 막지 않는다
			}
			return &work.Verdict{Role: role, Result: w.result}
		}
	}
	return nil
}

// lastAssistantText reads the subagent transcript's last assistant text when
// SubagentStop did not carry it (실측에서 빠진 적 있음).
func lastAssistantText(path string) string {
	b, err := readTail(path, 1<<20)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(lines[i]), &rec) != nil || rec.Message.Role != "assistant" {
			continue
		}
		var parts []string
		for _, c := range rec.Message.Content {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

// ---- 값 ----

func idString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func idList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		if s := idString(it); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func optString(raw json.RawMessage, n int) *string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	s = work.Clean(s, n)
	return &s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- git ----

// SystemGit runs git with a short timeout.
type SystemGit struct{}

func run(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (SystemGit) Toplevel(dir string) string {
	top := run(dir, "rev-parse", "--show-toplevel")
	if real, err := filepath.EvalSymlinks(top); err == nil && top != "" {
		return real
	}
	return top
}

func (SystemGit) Branch(dir string) string {
	b := run(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	return b // 분리된 HEAD면 비운다
}

func (SystemGit) Head(dir string) (string, string, []string) {
	out := run(dir, "show", "-s", "--format=%H%x00%s", "HEAD")
	sha, subject, _ := strings.Cut(out, "\x00")
	if sha == "" {
		return "", "", nil
	}
	var files []string
	for _, f := range strings.Split(run(dir, "show", "--name-only", "--format=", "HEAD"), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	return sha, subject, files
}
