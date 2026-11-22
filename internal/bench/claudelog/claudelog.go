// Package claudelog reads Claude Code conversation logs
// (<config dir>/projects/*/*.jsonl) and extracts the session flow used as replay
// material for evaluation: session starts (each SessionStart hook run, resumes and
// compactions included), requests, tool calls with their input text and delegation
// texts. It is a host adapter: it only reads, never writes, and calls no model.
// Turning the result into case files is the job of the assembly point.
package claudelog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/host"
)

// Kind is the kind of a session step.
type Kind string

const (
	KindRequest  Kind = "request"
	KindTool     Kind = "tool"
	KindDelegate Kind = "delegate"
	// KindStart is a session start the host ran its SessionStart hooks for (a new session, a resume, a fork, the
	// start after a compaction or a clear). Source says which.
	KindStart Kind = "start"
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
	Input        string    `json:"input,omitempty"`  // tool calls: the input's text (host.ToolInputText)
	Source       string    `json:"source,omitempty"` // session starts: startup·resume·fork·compact·clear
	SubagentType string    `json:"subagentType,omitempty"`
	Description  string    `json:"description,omitempty"`
	Task         string    `json:"task,omitempty"` // delegation text
}

// Merge kinds: how the log files of one session id relate (Session.Merge).
const (
	// MergeCopy is a session whose log files are copies of one another: what each file holds of the session is
	// contained in the longest file (a copy of a log, a log under a work copy's folder, the copied start of a
	// log that a later session took over).
	MergeCopy = "copy"
	// MergeUnion is a session whose log files each hold records the others lack; the records of all of them
	// are put in time order.
	MergeUnion = "union"
)

// Session is the flow of one session id: the records of that id in one log file or, when several files hold
// records of it, in all of them (ReadMerged).
type Session struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	Cwd          string    `json:"cwd"`
	Started      time.Time `json:"started"`
	Steps        []Step    `json:"steps"`
	BrokenLines  int       `json:"brokenLines,omitempty"`
	HookContexts int       `json:"hookContexts,omitempty"` // system records carrying hookAdditionalContext
	// Files is how many log files the session was read from, set only when it is more than one; Merge says how
	// they relate (MergeCopy or MergeUnion).
	Files int    `json:"files,omitempty"`
	Merge string `json:"merge,omitempty"`
	// Parent is the id of the session whose records start the log file this session is in: the session that
	// was taken over (resumed or forked) into a new id. The records before this session's own belong to Parent.
	Parent string `json:"parent,omitempty"`
	// UnreadFiles counts the log files of the session that could not be read and were left out.
	UnreadFiles int `json:"unreadFiles,omitempty"`
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
	UUID              string          `json:"uuid"`
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
	// Attachment is what the host attached to the conversation, a hook's run among them: its event and its
	// name, "<event>:<matcher>" (SessionStart:resume).
	Attachment *struct {
		HookEvent string `json:"hookEvent"`
		HookName  string `json:"hookName"`
	} `json:"attachment"`
}

type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// delegation is the input of a delegation tool call.
type delegation struct {
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
	Description  string `json:"description"`
}

// sessionStartHook is the hook event a host runs at a session start; the matcher of the hook name is the source.
const sessionStartHook = "SessionStart"

// startFoldGap is how far apart two hook records of one session start can be. The hooks of one start run
// together and their records were seen within a fraction of a second; a person who resumes, leaves without a
// word and resumes again takes longer than this, so that second start is kept.
const startFoldGap = 10 * time.Second

// eachRecord calls fn for every line of a log file that parses as a record and returns how many lines did not.
// A file is read one line at a time, so a large log is never held whole.
func eachRecord(path string, fn func(record)) (broken int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var rec record
			if err := json.Unmarshal(line, &rec); err != nil {
				broken++
			} else {
				fn(rec)
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return broken, nil
			}
			return broken, rerr
		}
	}
}

// ReadSession reads one log file as one session. Broken lines are skipped and counted.
func ReadSession(path string) (Session, error) {
	s := Session{Project: filepath.Base(filepath.Dir(path)), ID: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	haveID := false
	broken, err := eachRecord(path, func(rec record) { s.add(rec, &haveID) })
	s.BrokenLines = broken
	return s, err
}

// scanSessionIDs lists the session ids a log file carries, in order of first appearance. A file that carries
// none is listed under its name without the extension (the name Claude Code gives a log is its session id).
func scanSessionIDs(path string) []string {
	var ids []string
	_, err := eachRecord(path, func(rec record) {
		if rec.SessionID != "" && !slices.Contains(ids, rec.SessionID) {
			ids = append(ids, rec.SessionID)
		}
	})
	if err != nil || len(ids) == 0 {
		return []string{strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	}
	return ids
}

// stamped is a record with the time its place in a merged session is decided by.
type stamped struct {
	rec  record
	at   time.Time
	file int
	pos  int
}

// ReadMerged reads the records of one session id out of the log files that hold them. A log file can hold
// more than one session id: a session taken over into a new id starts with a copy of the earlier records, which
// keep the earlier id, and goes on with records of the new id. The hook ran under each id on its own, so a
// session is the records of one id, never the whole file. A record without a session id belongs to the id before
// it in its file (the first of the file when it comes first).
//
// A record that carries a uuid counts once, from the first file (in path order) that has it; a copy that is
// contained in a longer log adds nothing. With more than one file the records are put in time order (equal
// times: file path, then place in the file; a record without a time follows the record before it in its file),
// which does not depend on the order of paths. With one file the order is the file's. Files that cannot be read
// are left out and counted in UnreadFiles; when none can be read it is an error, as it is for no paths. Records
// without a uuid are kept as they are (in the logs read so far every record that makes a step carries a uuid
// and the ones without are bookkeeping, which is an observation about the format, not a guarantee: such a
// record would be counted once per file). The project is that of the first path in sorted order.
func ReadMerged(paths []string, id string) (Session, error) {
	if len(paths) == 0 {
		return Session{}, errors.New("claudelog: no log files to read")
	}
	paths = slices.Clone(paths)
	sort.Strings(paths)
	s := Session{Project: filepath.Base(filepath.Dir(paths[0])), ID: id}
	seen := map[string]bool{}
	var all []stamped
	sets := make([]map[string]bool, len(paths))
	read := 0
	for i, p := range paths {
		// uuid는 다른 파일에서 이미 본 것만 건너뛴다. 같은 파일 안의 되풀이는 지금까지처럼 그대로 둔다.
		fresh := map[string]bool{}
		sets[i] = map[string]bool{}
		var prev time.Time
		cur, first := "", ""
		pos := 0
		var mine []stamped
		broken, err := eachRecord(p, func(rec record) {
			if rec.SessionID != "" {
				cur = rec.SessionID
			}
			if first == "" {
				first = cur
			}
			if cur != id {
				return
			}
			at, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
			if at.IsZero() {
				at = prev
			} else {
				prev = at
			}
			if u := rec.UUID; u != "" {
				sets[i][u] = true
				if seen[u] {
					return
				}
				fresh[u] = true
			}
			mine = append(mine, stamped{rec: rec, at: at, file: i, pos: pos})
			pos++
		})
		if err != nil {
			s.UnreadFiles++
			sets[i] = nil
			continue
		}
		read++
		s.BrokenLines += broken
		if s.Parent == "" && first != "" && first != id {
			s.Parent = first
		}
		for u := range fresh {
			seen[u] = true
		}
		all = append(all, mine...)
	}
	if read == 0 {
		return s, errors.New("claudelog: none of the log files could be read")
	}
	if read > 1 {
		s.Files, s.Merge = read, mergeKind(sets)
		sort.SliceStable(all, func(a, b int) bool {
			x, y := all[a], all[b]
			if !x.at.Equal(y.at) {
				return x.at.Before(y.at)
			}
			if x.file != y.file {
				return x.file < y.file
			}
			return x.pos < y.pos
		})
	}
	haveID := false
	for _, r := range all {
		s.add(r.rec, &haveID)
	}
	return s, nil
}

// mergeKind says whether the uuid sets of the files of a session are copies of one another (each contained in
// the largest) or hold records of their own. Files that could not be read (nil sets) are ignored.
func mergeKind(sets []map[string]bool) string {
	largest := -1
	for i, st := range sets {
		if st != nil && (largest < 0 || len(st) > len(sets[largest])) {
			largest = i
		}
	}
	for _, st := range sets {
		for u := range st {
			if !sets[largest][u] {
				return MergeUnion
			}
		}
	}
	return MergeCopy
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
	case "attachment":
		// 세션 시작 훅이 돈 기록(성공·실패 모두): 훅마다 한 줄이라, 같은 시작의 줄(같은 출처, 사이에 다른 단계 없음,
		// startFoldGap 안, 시각이 없으면 같은 시작으로 봄)은 하나로 센다. 출처가 없는 이름(SessionStart)은 훅이 넣은 글을
		// 붙인 기록이라 세지 않는다.
		a := rec.Attachment
		if a == nil || a.HookEvent != sessionStartHook {
			return
		}
		source, ok := strings.CutPrefix(a.HookName, sessionStartHook+":")
		if !ok || source == "" {
			return
		}
		if n := len(s.Steps); n > 0 && s.Steps[n-1].Kind == KindStart && s.Steps[n-1].Source == source {
			if prev := s.Steps[n-1].At; at.IsZero() || prev.IsZero() || at.Sub(prev).Abs() <= startFoldGap {
				return
			}
		}
		s.Steps = append(s.Steps, Step{Kind: KindStart, At: at, Source: source})
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
			st := Step{Kind: KindTool, At: at, Tool: b.Name, Input: host.ToolInputText(b.Name, b.Input)}
			if b.Name == "Agent" || b.Name == "Task" {
				var d delegation
				_ = json.Unmarshal(b.Input, &d) // 꼴이 다른 입력은 빈 위임 글로 둔다
				st = Step{Kind: KindDelegate, At: at, Tool: b.Name, Task: d.Prompt, SubagentType: d.SubagentType, Description: d.Description}
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

// NoLimit as a maximum of Options lifts that maximum.
const NoLimit = -1

// Options controls Select. Zero values take the defaults; NoLimit lifts a maximum.
type Options struct {
	MinSessions           int // default 10
	MinRequests           int // default 50
	MaxSessions           int // default 20
	MaxRequests           int // default 120
	MaxRequestsPerSession int // default 12
	// All takes every session the roots have: no minimum stops the pick, no maximum applies, and a session with
	// a session start but no person's request is kept too. The other fields are ignored.
	All bool
}

func (o Options) withDefaults() Options {
	if o.All {
		return Options{MinSessions: math.MaxInt, MinRequests: math.MaxInt, MaxSessions: math.MaxInt, MaxRequests: math.MaxInt,
			MaxRequestsPerSession: math.MaxInt, All: true}
	}
	def := func(v *int, d int) {
		switch {
		case *v == NoLimit:
			*v = math.MaxInt
		case *v <= 0:
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

// capRequests cuts a session at its limit-th person's request: the steps after it go, except the session
// starts. A replay still has to measure what a later resume or the start after a compaction put in.
func capRequests(s Session, limit int) Session {
	n := 0
	for i, st := range s.Steps {
		if st.Kind == KindRequest && st.Origin == OriginUser {
			if n++; n > limit {
				kept := s.Steps[:i:i]
				for _, later := range s.Steps[i:] {
					if later.Kind == KindStart {
						kept = append(kept, later)
					}
				}
				s.Steps = kept
				break
			}
		}
	}
	return s
}

// hasStart says whether a session has a session start step.
func hasStart(s Session) bool {
	return slices.ContainsFunc(s.Steps, func(st Step) bool { return st.Kind == KindStart })
}

// Select picks sessions from the roots (each a "projects" directory). A session is what one session id
// recorded, whichever log files hold it: the files of a session id are read together (ReadMerged), and a log
// file that a later session took over into a new id (it starts with a copy of the earlier session's records)
// gives the new id its own session. So no id comes out twice, and the case ids built from it do not repeat.
// It takes the newest session of each project in turn, so one project does not dominate, and stops once both
// minimums are met or a maximum is reached. Same input, same result. A session whose files cannot be read at
// all is left out.
func Select(roots []string, opts Options) ([]Session, error) {
	opts = opts.withDefaults()
	byProject := map[string][]Session{}
	var files []string
	for _, root := range roots {
		found, err := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	sort.Strings(files)
	files = slices.Compact(files)
	// 세션 id가 같은 파일을 묶는 것은 파일 이름이 아니라 기록 안의 id로 한다. 한 파일이 여러 id를 가질 수 있다.
	groups := map[string][]string{}
	idsOf := map[string][]string{}
	var order []string
	for _, p := range files {
		ids := scanSessionIDs(p)
		idsOf[p] = ids
		for _, id := range ids {
			if _, ok := groups[id]; !ok {
				order = append(order, id)
			}
			groups[id] = append(groups[id], p)
		}
	}
	seenID := map[string]bool{}
	for _, id := range order {
		var s Session
		var err error
		if paths := groups[id]; len(paths) == 1 && len(idsOf[paths[0]]) == 1 {
			s, err = ReadSession(paths[0])
		} else {
			s, err = ReadMerged(paths, id)
		}
		if err != nil {
			continue
		}
		if seenID[s.ID] {
			return nil, fmt.Errorf("claudelog: session id %q read twice", s.ID)
		}
		seenID[s.ID] = true
		if inTempDir(s.Cwd) {
			continue
		}
		s = capRequests(s, opts.MaxRequestsPerSession)
		if CountRequests(s) == 0 && !(opts.All && hasStart(s)) {
			continue
		}
		byProject[s.Project] = append(byProject[s.Project], s)
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
