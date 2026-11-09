// Package claudelog reads Claude Code conversation logs
// (<config dir>/projects/*/*.jsonl) and extracts the session flow used as replay
// material for evaluation: session start, requests, tool calls and delegation
// texts. It is a host adapter: it only reads, never writes, and calls no model.
// Turning the result into case files is the job of the assembly point.
package claudelog

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind is the kind of a session step.
type Kind string

const (
	KindRequest  Kind = "request"
	KindTool     Kind = "tool"
	KindDelegate Kind = "delegate"
)

// Origin is the draft judgement of who wrote a request.
type Origin string

const (
	OriginUser   Origin = "user"
	OriginAgent  Origin = "agent"
	OriginSystem Origin = "system"
)

// Evidence keeps the raw signals the origin draft was based on.
type Evidence struct {
	TurnOrigin   string   `json:"turnOrigin,omitempty"`
	OriginKind   string   `json:"originKind,omitempty"`
	PromptSource string   `json:"promptSource,omitempty"`
	IsMeta       bool     `json:"isMeta,omitempty"`
	Markers      []string `json:"markers,omitempty"`
}

// Step is one item of the session flow.
type Step struct {
	Kind         Kind      `json:"kind"`
	At           time.Time `json:"at"`
	Text         string    `json:"text,omitempty"`   // request text, markers kept
	Origin       Origin    `json:"origin,omitempty"` // requests only
	Evidence     Evidence  `json:"evidence,omitempty"`
	Tool         string    `json:"tool,omitempty"`
	SubagentType string    `json:"subagentType,omitempty"`
	Description  string    `json:"description,omitempty"`
	Task         string    `json:"task,omitempty"` // delegation text
}

// Session is the flow of one log file.
type Session struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	Cwd          string    `json:"cwd"`
	Started      time.Time `json:"started"`
	Steps        []Step    `json:"steps"`
	BrokenLines  int       `json:"brokenLines,omitempty"`
	HookContexts int       `json:"hookContexts,omitempty"` // system records carrying hookAdditionalContext
}

// DefaultRoot is <CLAUDE_CONFIG_DIR or ~/.claude>/projects.
func DefaultRoot() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "projects")
}

type record struct {
	Type              string          `json:"type"`
	SessionID         string          `json:"sessionId"`
	Cwd               string          `json:"cwd"`
	Timestamp         string          `json:"timestamp"`
	IsMeta            bool            `json:"isMeta"`
	IsSidechain       bool            `json:"isSidechain"`
	IsCompactSummary  bool            `json:"isCompactSummary"`
	TurnOrigin        string          `json:"turnOrigin"`
	PromptSource      string          `json:"promptSource"`
	HookAdditionalCtx json.RawMessage `json:"hookAdditionalContext"`
	Origin            *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Input struct {
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
	} `json:"input"`
}

// ReadSession reads one log file as one session. Broken lines are skipped and counted.
func ReadSession(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()
	s := Session{Project: filepath.Base(filepath.Dir(path)), ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	haveID := false
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var rec record
			if err := json.Unmarshal(line, &rec); err != nil {
				s.BrokenLines++
			} else {
				s.add(rec, &haveID)
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return s, rerr
		}
	}
	return s, nil
}

func (s *Session) add(rec record, haveID *bool) {
	at, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if rec.SessionID != "" && !*haveID {
		s.ID, *haveID = rec.SessionID, true
	}
	if rec.Cwd != "" && s.Cwd == "" {
		s.Cwd = rec.Cwd
	}
	if !at.IsZero() && (s.Started.IsZero() || at.Before(s.Started)) {
		s.Started = at
	}
	if rec.IsSidechain {
		return
	}
	switch rec.Type {
	case "system":
		if h := strings.TrimSpace(string(rec.HookAdditionalCtx)); h != "" && h != "null" && h != `""` {
			s.HookContexts++
		}
	case "user":
		if rec.IsCompactSummary {
			return
		}
		if st, ok := requestStep(rec, at); ok {
			s.Steps = append(s.Steps, st)
		}
	case "assistant":
		var blocks []block
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			return
		}
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			st := Step{Kind: KindTool, At: at, Tool: b.Name}
			if b.Name == "Agent" || b.Name == "Task" {
				st.Kind = KindDelegate
				st.Task, st.SubagentType, st.Description = b.Input.Prompt, b.Input.SubagentType, b.Input.Description
			}
			s.Steps = append(s.Steps, st)
		}
	}
}

// userText returns the text of a user record; ok is false when it has none
// (tool_result or image only).
func userText(raw json.RawMessage) (string, bool) {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str, strings.TrimSpace(str) != ""
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

func tagValue(text, tag string) (string, bool) {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(text, open)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:j]), true
}

func requestStep(rec record, at time.Time) (Step, bool) {
	text, ok := userText(rec.Message.Content)
	if !ok {
		return Step{}, false
	}
	if strings.HasPrefix(strings.TrimSpace(text), "This session is being continued from a previous conversation") {
		return Step{}, false // compaction summary without its flag
	}
	ev := Evidence{TurnOrigin: rec.TurnOrigin, PromptSource: rec.PromptSource, IsMeta: rec.IsMeta}
	if rec.Origin != nil {
		ev.OriginKind = rec.Origin.Kind
	}
	if name, ok := tagValue(text, "command-name"); ok {
		ev.Markers = append(ev.Markers, "command-name")
		args, _ := tagValue(text, "command-args")
		text = strings.TrimSpace(name + " " + args)
		if !strings.HasPrefix(text, "/") {
			text = "/" + text
		}
	}
	for _, m := range []string{"task-notification", "local-command-stdout", "local-command-stderr", "local-command-caveat"} {
		if strings.Contains(text, "<"+m) {
			ev.Markers = append(ev.Markers, m)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(text), "[Request interrupted") {
		ev.Markers = append(ev.Markers, "interrupted")
	}
	return Step{Kind: KindRequest, At: at, Text: text, Origin: draftOrigin(ev), Evidence: ev}, true
}

// draftOrigin applies the origin draft rules in order.
func draftOrigin(ev Evidence) Origin {
	has := func(m string) bool {
		for _, v := range ev.Markers {
			if v == m {
				return true
			}
		}
		return false
	}
	switch {
	case has("local-command-stdout"), has("local-command-stderr"), has("local-command-caveat"), has("interrupted"):
		return OriginSystem
	case has("task-notification"):
		return OriginAgent
	case ev.IsMeta:
		return OriginSystem
	case ev.OriginKind == "task-notification", ev.OriginKind == "peer":
		return OriginAgent
	case ev.OriginKind == "human":
		return OriginUser
	case ev.PromptSource == "system":
		return OriginSystem
	case ev.TurnOrigin == "peer", ev.TurnOrigin == "task_notification", ev.TurnOrigin == "sdk", ev.PromptSource == "sdk":
		return OriginAgent // unverified: desktop-typed requests may carry sdk
	}
	return OriginUser
}

// CountRequests counts the requests judged as user-written.
func CountRequests(s Session) int {
	n := 0
	for _, st := range s.Steps {
		if st.Kind == KindRequest && st.Origin == OriginUser {
			n++
		}
	}
	return n
}

// Options controls Select. Zero values take the defaults.
type Options struct {
	MinSessions           int // default 10
	MinRequests           int // default 50
	MaxSessions           int // default 20
	MaxRequests           int // default 120
	MaxRequestsPerSession int // default 12
}

func (o Options) withDefaults() Options {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&o.MinSessions, 10)
	def(&o.MinRequests, 50)
	def(&o.MaxSessions, 20)
	def(&o.MaxRequests, 120)
	def(&o.MaxRequestsPerSession, 12)
	return o
}

var tmpPrefixes = []string{"/private/tmp", "/tmp", "/private/var/folders", "/var/folders"}

func inTempDir(cwd string) bool {
	for _, p := range tmpPrefixes {
		if cwd == p || strings.HasPrefix(cwd, p+"/") {
			return true
		}
	}
	return false
}

func capRequests(s Session, limit int) Session {
	n := 0
	for i, st := range s.Steps {
		if st.Kind == KindRequest && st.Origin == OriginUser {
			if n++; n > limit {
				s.Steps = s.Steps[:i:i]
				break
			}
		}
	}
	return s
}

// Select picks sessions from the roots (each a "projects" directory). It takes the
// newest session of each project in turn, so one project does not dominate, and
// stops once both minimums are met or a maximum is reached. Same input, same result.
func Select(roots []string, opts Options) ([]Session, error) {
	opts = opts.withDefaults()
	byProject := map[string][]Session{}
	for _, root := range roots {
		files, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		for _, p := range files {
			s, err := ReadSession(p)
			if err != nil {
				continue
			}
			if inTempDir(s.Cwd) {
				continue
			}
			s = capRequests(s, opts.MaxRequestsPerSession)
			if CountRequests(s) == 0 {
				continue
			}
			byProject[s.Project] = append(byProject[s.Project], s)
		}
	}
	names := make([]string, 0, len(byProject))
	for name, list := range byProject {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].Started.Equal(list[j].Started) {
				return list[i].Started.After(list[j].Started)
			}
			return list[i].ID < list[j].ID
		})
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := byProject[names[i]][0], byProject[names[j]][0]
		if !a.Started.Equal(b.Started) {
			return a.Started.After(b.Started)
		}
		return names[i] < names[j]
	})
	var out []Session
	reqs := 0
	for round := 0; ; round++ {
		any := false
		for _, name := range names {
			list := byProject[name]
			if round >= len(list) {
				continue
			}
			any = true
			if (len(out) >= opts.MinSessions && reqs >= opts.MinRequests) ||
				len(out) >= opts.MaxSessions || reqs >= opts.MaxRequests {
				return out, nil
			}
			out = append(out, list[round])
			reqs += CountRequests(list[round])
		}
		if !any {
			return out, nil
		}
	}
}
