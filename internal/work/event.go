package work

import "encoding/json"

// Event kinds kept in the ledger. A hook turns host input into one of these;
// the reducer applies it to the graph tables. Events carry everything the
// reducer needs (git values included), so the graph can be rebuilt from the
// ledger alone.
const (
	KindSessionStart = "session.start"
	KindTurnStart    = "turn.start"
	KindTurnEnd      = "turn.end"
	KindSessionEnd   = "session.end"
	KindTaskCreate   = "task.create"   // Claude Code TaskCreate 성공 응답
	KindTaskUpdate   = "task.update"   // Claude Code TaskUpdate 성공 응답
	KindTaskState    = "task.state"    // Claude Code TaskList·TaskGet 응답(현재 상태)
	KindPlanSnapshot = "plan.snapshot" // 전체 목록 스냅숏(TodoWrite, Codex update_plan, 앱 계획 items)
	KindPlanOps      = "plan.ops"      // 데스크톱 앱 계획 도구의 다음·완료 지시
	KindWaitOpen     = "wait.open"     // 권한 요청, 사용자 질문
	KindToolDone     = "tool.done"     // 도구 사후(성공·실패). 기다림 해소와 활동
	KindRunStart     = "run.start"     // SubagentStart
	KindRunTool      = "run.tool"      // 서브에이전트 도구(Agent)의 사후 응답
	KindRunStop      = "run.stop"      // SubagentStop
	KindCommand      = "evidence.command"
	KindCommit       = "evidence.commit"
	KindTitle        = "title.set" // work_title
	KindEdgeDropped  = "edge.dropped"
)

// List kinds (계약 origin.list.kind).
const (
	ListClaudeTask = "claude-task"
	ListClaudeTodo = "claude-todo"
	ListAppPlan    = "claude-app-plan"
	ListCodexPlan  = "codex-plan"
)

// Event is one ledger entry. Data holds the kind's payload.
type Event struct {
	Seq     int64           `json:"seq,omitempty"`
	Kind    string          `json:"kind"`
	Host    string          `json:"host"`
	Session string          `json:"session"`
	At      int64           `json:"at"`            // 밀리초. 훅 프로세스가 입력을 읽기 전에 찍는다
	Key     string          `json:"key,omitempty"` // 멱등 키. 같은 키는 한 번만 적용한다
	Cwd     string          `json:"cwd,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// NewEvent builds an event with a payload.
func NewEvent(kind, host, session string, at int64, payload any) Event {
	e := Event{Kind: kind, Host: host, Session: session, At: at}
	if payload != nil {
		e.Data, _ = json.Marshal(payload)
	}
	return e
}

// Project is a work's project scope (계약 project).
type Project struct {
	Scope string `json:"scope"`
	Label string `json:"label"`
}

// SessionStart is the payload of session.start.
type SessionStart struct {
	Source     string     `json:"source,omitempty"` // startup | resume | clear | compact
	Surface    string     `json:"surface,omitempty"`
	Project    Project    `json:"project,omitempty"`
	Repo       string     `json:"repo,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	Model      string     `json:"model,omitempty"`
	Transcript string     `json:"transcript,omitempty"`
	ListID     string     `json:"listId,omitempty"` // CLAUDE_CODE_TASK_LIST_ID
	Parent     *ParentRef `json:"parent,omitempty"`
	Launcher   string     `json:"launcher,omitempty"` // 세션을 띄운 쪽(계약 origin.launcher)
}

// ParentRef is ORBIT_PARENT: the session and step a child session works for.
type ParentRef struct {
	Host    string `json:"host"`
	Session string `json:"session"`
	Step    string `json:"step,omitempty"` // 호스트 단계 번호(Task id) 또는 목록에서의 1부터 센 위치
}

// Turn is the payload of turn.start and turn.end.
type Turn struct {
	Prompt string `json:"prompt,omitempty"` // 호스트의 요청·턴 id
}

// SessionEnd is the payload of session.end.
type SessionEnd struct {
	Reason string `json:"reason,omitempty"`
}

// TaskCreate is a successful Claude Code TaskCreate.
type TaskCreate struct {
	List       string `json:"list"`
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	ActiveForm string `json:"activeForm,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Blocked    string `json:"blocked,omitempty"`
}

// TaskUpdate is a successful Claude Code TaskUpdate. Nil fields did not change.
type TaskUpdate struct {
	List         string   `json:"list"`
	ID           string   `json:"id"`
	Status       *string  `json:"status,omitempty"` // pending | in_progress | completed | deleted
	Subject      *string  `json:"subject,omitempty"`
	ActiveForm   *string  `json:"activeForm,omitempty"`
	Owner        *string  `json:"owner,omitempty"`
	Blocked      *string  `json:"blocked,omitempty"` // metadata.blocked. 빈 글은 풀림
	AddBlockedBy []string `json:"addBlockedBy,omitempty"`
	AddBlocks    []string `json:"addBlocks,omitempty"`
}

// TaskItem is one task in a TaskList or TaskGet response.
type TaskItem struct {
	ID        string   `json:"id"`
	Subject   string   `json:"subject,omitempty"`
	Status    string   `json:"status,omitempty"`
	Owner     *string  `json:"owner,omitempty"`
	BlockedBy []string `json:"blockedBy"`
}

// TaskState is the current state the host reported.
type TaskState struct {
	List  string     `json:"list"`
	Full  bool       `json:"full"` // TaskList: 목록 전체. 빠진 항목은 지워진 것
	Tasks []TaskItem `json:"tasks"`
}

// PlanItem is one entry of a snapshot list.
type PlanItem struct {
	Text       string `json:"text"`
	Status     string `json:"status"` // pending | in_progress | completed
	ActiveText string `json:"activeText,omitempty"`
}

// PlanSnapshot is a whole list.
type PlanSnapshot struct {
	List  string     `json:"list"` // 목록 종류
	ID    string     `json:"id"`   // 목록 id(세션 id)
	Items []PlanItem `json:"items"`
}

// PlanOps are the desktop app plan tool's advance/done/start moves. Finish is
// the app's wrap_up: it checks off the step in progress and starts no other.
type PlanOps struct {
	List    string `json:"list"`
	ID      string `json:"id"`
	Advance bool   `json:"advance,omitempty"`
	Done    []int  `json:"done,omitempty"` // 1부터 센 위치
	Start   int    `json:"start,omitempty"`
	Finish  bool   `json:"finish,omitempty"`
}

// Wait is a permission request or a question to the user.
type Wait struct {
	Key    string `json:"key"`
	Prompt string `json:"prompt,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Agent  string `json:"agent,omitempty"`
}

// ToolDone closes the wait with the same key and marks activity.
type ToolDone struct {
	Key   string `json:"key"`
	Tool  string `json:"tool,omitempty"`
	Agent string `json:"agent,omitempty"`
}

// RunStart is SubagentStart.
type RunStart struct {
	Agent  string `json:"agent"`
	Type   string `json:"type,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// RunTool is the subagent tool's post-call response.
type RunTool struct {
	ToolUse    string `json:"toolUse,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Type       string `json:"type,omitempty"`
	Background bool   `json:"background,omitempty"`
	OK         bool   `json:"ok"`
	Model      string `json:"model,omitempty"`
}

// RunStop is SubagentStop.
type RunStop struct {
	Agent      string   `json:"agent"`
	Type       string   `json:"type,omitempty"`
	Transcript string   `json:"transcript,omitempty"`
	Verdict    *Verdict `json:"verdict,omitempty"`
}

// Verdict is a gate or reviewer decision read from the subagent's last answer.
// Only the decision is kept, not the answer text.
type Verdict struct {
	Role   string `json:"role"`
	Result string `json:"result"` // pass | needs_fix | needs_user
}

// Command is a test or build command result.
type Command struct {
	Text  string `json:"text"`
	OK    bool   `json:"ok"`
	Agent string `json:"agent,omitempty"`
}

// Commit is a commit the session made.
type Commit struct {
	Repo    string   `json:"repo"`
	SHA     string   `json:"sha"`
	Subject string   `json:"subject,omitempty"`
	Files   []string `json:"files,omitempty"`
	Agent   string   `json:"agent,omitempty"`
}

// Title is work_title's write.
type Title struct {
	Work  string `json:"work"`
	Title string `json:"title"`
}

// EdgeDropped records an edge refused because it would make a cycle.
type EdgeDropped struct {
	Work  string `json:"work"`
	Step  string `json:"step"`
	After string `json:"after"`
}
