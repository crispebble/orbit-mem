package work

import "time"

// The orbit.work 1.0 document shapes (internal/work/contract/v1). Times are
// RFC 3339 UTC strings with milliseconds; an empty string means "not yet"
// and is left out.

// Origin is where a work came from.
type Origin struct {
	Host      string  `json:"host"`
	Surface   string  `json:"surface"`
	Launcher  string  `json:"launcher,omitempty"` // 세션을 띄운 쪽(1.1). 모르면 없다
	SessionID string  `json:"sessionId"`
	List      ListRef `json:"list"`
}

// ListRef is the host list a work was read from.
type ListRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// ParentRef is the step a child work works for.
type Parent struct {
	WorkID string `json:"workId"`
	StepID string `json:"stepId,omitempty"`
}

// Counts are steps per status.
type Counts struct {
	Pending    int `json:"pending"`
	Ready      int `json:"ready"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Done       int `json:"done"`
	Cancelled  int `json:"cancelled"`
}

func (c *Counts) add(status string) {
	switch status {
	case "pending":
		c.Pending++
	case "ready":
		c.Ready++
	case "in_progress":
		c.InProgress++
	case "blocked":
		c.Blocked++
	case "done":
		c.Done++
	case "cancelled":
		c.Cancelled++
	}
}

// WorkHead holds the fields a summary and a full work share.
type WorkHead struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	TitleSource    string   `json:"titleSource"`
	TitleUpdatedAt string   `json:"titleUpdatedAt,omitempty"`
	State          string   `json:"state"`
	Cwd            string   `json:"cwd,omitempty"`
	Project        *Project `json:"project,omitempty"`
	Origin         Origin   `json:"origin"`
	Parent         *Parent  `json:"parent,omitempty"`
	OpenedAt       string   `json:"openedAt"`
	UpdatedAt      string   `json:"updatedAt"`
	ClosedAt       string   `json:"closedAt,omitempty"`
	Counts         Counts   `json:"counts"`
}

// VerificationCounts are steps per verification state (작업 요약, 제안).
type VerificationCounts struct {
	None      int `json:"none"`
	Pass      int `json:"pass"`
	Fail      int `json:"fail"`
	NeedsUser int `json:"needs_user"`
	Outdated  int `json:"outdated"`
}

// WorkSummary is a work in a list.
type WorkSummary struct {
	WorkHead
	StepCount    int                `json:"stepCount"`
	Verification VerificationCounts `json:"verification"`
}

// Work is the whole graph of one work.
type Work struct {
	WorkHead
	Steps    []Step     `json:"steps"`
	Runs     []Run      `json:"runs"`
	Evidence []Evidence `json:"evidence"`
	Children []Work     `json:"children,omitempty"`
}

// Native is the host's own id of a step.
type Native struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// After is a predecessor edge.
type After struct {
	StepID string `json:"stepId"`
	Source string `json:"source"`
}

// Block is why an in-progress step stopped.
type Block struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	Since  string `json:"since"`
}

// Assignee is who works on a step.
type Assignee struct {
	Kind  string `json:"kind"`
	Role  string `json:"role,omitempty"`
	Host  string `json:"host,omitempty"`
	Model string `json:"model,omitempty"`
}

// Verification is a step's verification result.
type Verification struct {
	State      string `json:"state"`
	Outdated   bool   `json:"outdated"`
	EvidenceID string `json:"evidenceId,omitempty"`
}

// Step is one step of a work.
type Step struct {
	ID           string       `json:"id"`
	Native       *Native      `json:"native,omitempty"`
	Title        string       `json:"title"`
	ActiveTitle  string       `json:"activeTitle,omitempty"`
	Detail       string       `json:"detail,omitempty"` // 1.2: 호스트가 준 단계 설명(비밀값 가림, DetailMax 글자)
	Order        int          `json:"order"`
	Wave         int          `json:"wave"`
	After        []After      `json:"after"`
	Status       string       `json:"status"`
	Block        *Block       `json:"block,omitempty"`
	Assignee     *Assignee    `json:"assignee,omitempty"`
	StartedAt    string       `json:"startedAt,omitempty"`
	FinishedAt   string       `json:"finishedAt,omitempty"`
	Verification Verification `json:"verification"`
	EvidenceIDs  []string     `json:"evidenceIds"`
	ChildWorkIDs []string     `json:"childWorkIds"`
}

// Run is one subagent or child session run.
type Run struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Host            string `json:"host"`
	Role            string `json:"role,omitempty"`
	AgentID         string `json:"agentId,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	StepID          string `json:"stepId,omitempty"`
	Link            string `json:"link"`
	Background      bool   `json:"background,omitempty"`
	Cwd             string `json:"cwd,omitempty"`
	Repo            string `json:"repo,omitempty"`
	Branch          string `json:"branch,omitempty"`
	State           string `json:"state"`
	EndReason       string `json:"endReason,omitempty"`
	StartedAt       string `json:"startedAt"`
	LastSeenAt      string `json:"lastSeenAt,omitempty"`
	FinishedAt      string `json:"finishedAt,omitempty"`
	Cost            *Cost  `json:"cost,omitempty"` // 1.2: 실행이 쓴 비용(orbit-pi run.stop)
}

// EvidenceRef points at what an evidence is about.
type EvidenceRef struct {
	Repo       string `json:"repo,omitempty"`
	SHA        string `json:"sha,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// Evidence is one observation attached to a step or a session.
type Evidence struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`
	StepID  string       `json:"stepId,omitempty"`
	RunID   string       `json:"runId,omitempty"`
	At      string       `json:"at"`
	Result  string       `json:"result"`
	Summary string       `json:"summary,omitempty"`
	Ref     *EvidenceRef `json:"ref,omitempty"`
	Files   []string     `json:"files,omitempty"`
}

// Session is one host session.
type Session struct {
	SessionID       string   `json:"sessionId"`
	Host            string   `json:"host"`
	Surface         string   `json:"surface"`
	Launcher        string   `json:"launcher,omitempty"`
	Cwd             string   `json:"cwd,omitempty"`
	Repo            string   `json:"repo,omitempty"`
	Branch          string   `json:"branch,omitempty"`
	Project         *Project `json:"project,omitempty"`
	ParentSessionID string   `json:"parentSessionId,omitempty"`
	Model           string   `json:"model,omitempty"`
	State           string   `json:"state"`
	EndReason       string   `json:"endReason,omitempty"`
	EndDetail       string   `json:"endDetail,omitempty"`
	StartedAt       string   `json:"startedAt"`
	LastSeenAt      string   `json:"lastSeenAt"`
	EndedAt         string   `json:"endedAt,omitempty"`
	OpenWorkIDs     []string `json:"openWorkIds"`
	ActiveStepIDs   []string `json:"activeStepIds"`
}

// EventRef points at long records by local path.
type EventRef struct {
	Transcript      string `json:"transcript,omitempty"`
	AgentTranscript string `json:"agentTranscript,omitempty"`
}

// SessionEvent is one entry of a session's event page.
type SessionEvent struct {
	Seq     string    `json:"seq"`
	At      string    `json:"at"`
	Kind    string    `json:"kind"`
	WorkID  string    `json:"workId,omitempty"`
	StepID  string    `json:"stepId,omitempty"`
	RunID   string    `json:"runId,omitempty"`
	Summary string    `json:"summary,omitempty"`
	Ref     *EventRef `json:"ref,omitempty"`
}

// Stamp formats a millisecond time for the contract. Zero gives "".
func Stamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}
