// Package transcript reads Claude Code and Codex conversation logs and keeps
// only what a retrospective needs: the user's words and the AI's answers.
// Tool calls, tool output, thinking and injected context are dropped.
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Roots are the hosts' data directories (~/.claude and ~/.codex).
type Roots struct{ Claude, Codex string }

// DefaultRoots uses CLAUDE_CONFIG_DIR and CODEX_HOME when set.
func DefaultRoots() Roots {
	home, _ := os.UserHomeDir()
	r := Roots{Claude: filepath.Join(home, ".claude"), Codex: filepath.Join(home, ".codex")}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		r.Claude = v
	}
	if v := os.Getenv("CODEX_HOME"); v != "" {
		r.Codex = v
	}
	return r
}

// Turn is one message.
type Turn struct {
	Role string    `json:"role"` // user | ai
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Session is one conversation's turns on the requested day.
type Session struct {
	Host  string `json:"host"`
	ID    string `json:"id"`
	Cwd   string `json:"cwd,omitempty"`
	Turns []Turn `json:"turns"`
}

// Doc is a text file used as material.
type Doc struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

const (
	maxUserRunes = 2000
	maxAIRunes   = 1500
	maxLineBytes = 16 << 20
)

var reminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func dayBounds(day string, loc *time.Location) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return start, start, err
	}
	return start, start.AddDate(0, 0, 1), nil
}

func excluded(cwd string, exclude []string) bool {
	norm := func(p string) string {
		p = filepath.ToSlash(filepath.Clean(p))
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return strings.TrimSuffix(p, "/")
	}
	c := norm(cwd)
	for _, e := range exclude {
		if e == "" {
			continue
		}
		e = norm(e)
		if c == e || strings.HasPrefix(c, e+"/") {
			return true
		}
	}
	return false
}

func eachLine(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), maxLineBytes)
	for sc.Scan() {
		fn(sc.Bytes())
	}
	return nil
}

// Collect returns the sessions of both hosts that have turns on day (in loc),
// skipping sessions whose working directory is under an excluded directory.
func Collect(r Roots, day string, loc *time.Location, exclude []string) ([]Session, error) {
	start, end, err := dayBounds(day, loc)
	if err != nil {
		return nil, err
	}
	var out []Session
	if r.Claude != "" {
		out = append(out, collectClaude(r.Claude, start, end, exclude)...)
	}
	if r.Codex != "" {
		out = append(out, collectCodex(r.Codex, start, end, exclude)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Turns[0].At.Before(out[j].Turns[0].At) })
	return out, nil
}

// recentFiles lists files under root matching name filter modified at or after since.
func recentFiles(root string, since time.Time, keep func(rel string) bool) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if !keep(filepath.ToSlash(rel)) {
			return nil
		}
		if info, err := d.Info(); err == nil && !info.ModTime().Before(since) {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

type claudeLine struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	Cwd         string `json:"cwd"`
	SessionID   string `json:"sessionId"`
	IsMeta      bool   `json:"isMeta"`
	Entrypoint  string `json:"entrypoint"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"`
}

func collectClaude(root string, start, end time.Time, exclude []string) []Session {
	projects := filepath.Join(root, "projects")
	// projects/<dir>/<session>.jsonl only: subagent logs live deeper.
	files := recentFiles(projects, start, func(rel string) bool {
		return strings.Count(rel, "/") == 1 && strings.HasSuffix(rel, ".jsonl")
	})
	var out []Session
	for _, f := range files {
		if s, ok := readClaude(f, start, end, exclude); ok {
			out = append(out, s)
		}
	}
	return out
}

// within says whether at is inside [start, end); a zero bound is open.
func within(at, start, end time.Time) bool {
	return (start.IsZero() || !at.Before(start)) && (end.IsZero() || at.Before(end))
}

func readClaude(f string, start, end time.Time, exclude []string) (Session, bool) {
	s := Session{Host: "claude", ID: strings.TrimSuffix(filepath.Base(f), ".jsonl")}
	program := false
	if err := eachLine(f, func(line []byte) {
		var l claudeLine
		if json.Unmarshal(line, &l) != nil {
			return
		}
		if l.Entrypoint == "sdk-ts" { // 플러그인·프로그램이 띄운 세션
			program = true
		}
		if (l.Type != "user" && l.Type != "assistant") || l.IsMeta || l.IsSidechain {
			return
		}
		at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		if err != nil || !within(at, start, end) {
			return
		}
		if s.Cwd == "" && l.Cwd != "" {
			s.Cwd = l.Cwd
		}
		text := claudeText(l)
		if text == "" {
			return
		}
		role, n := "user", maxUserRunes
		if l.Type == "assistant" {
			role, n = "ai", maxAIRunes
		}
		s.Turns = append(s.Turns, Turn{Role: role, Text: clip(text, n), At: at})
	}); err != nil {
		return s, false
	}
	return s, len(s.Turns) > 0 && !program && !excluded(s.Cwd, exclude)
}

func claudeText(l claudeLine) string {
	var str string
	if json.Unmarshal(l.Message.Content, &str) == nil {
		return cleanUser(str)
	}
	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if l.Type == "assistant" {
				parts = append(parts, b.Text)
			} else if t := cleanUser(b.Text); t != "" {
				parts = append(parts, t)
			}
		case "tool_result":
			// 사용자가 질문 도구로 고른 답만 사용자 말로 남긴다.
			var s string
			if json.Unmarshal(b.Content, &s) == nil && strings.HasPrefix(s, "The user answered") {
				parts = append(parts, s)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func cleanUser(s string) string {
	s = strings.TrimSpace(reminderRE.ReplaceAllString(s, ""))
	for _, p := range []string{"<command-", "<local-command", "Caveat:", "[Request interrupted"} {
		if strings.HasPrefix(s, p) {
			return ""
		}
	}
	return s
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type           string          `json:"type"`
		ID             string          `json:"id"`
		Cwd            string          `json:"cwd"`
		ParentThreadID string          `json:"parent_thread_id"`
		Source         json.RawMessage `json:"source"`
		Role           string          `json:"role"`
		Phase          string          `json:"phase"`
		Content        []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

func collectCodex(root string, start, end time.Time, exclude []string) []Session {
	files := recentFiles(filepath.Join(root, "sessions"), start, func(rel string) bool { return strings.HasSuffix(rel, ".jsonl") })
	var out []Session
	for _, f := range files {
		if s, ok := readCodex(f, start, end, exclude); ok {
			out = append(out, s)
		}
	}
	return out
}

func readCodex(f string, start, end time.Time, exclude []string) (Session, bool) {
	s := Session{Host: "codex", ID: strings.TrimSuffix(filepath.Base(f), ".jsonl")}
	sub := false
	if err := eachLine(f, func(line []byte) {
		var l codexLine
		if json.Unmarshal(line, &l) != nil || sub {
			return
		}
		if l.Type == "session_meta" {
			// 하위 에이전트·자동 검토 스레드는 사용자 대화가 아니다.
			if l.Payload.ParentThreadID != "" || strings.Contains(string(l.Payload.Source), "subagent") {
				sub = true
				return
			}
			s.Cwd = l.Payload.Cwd
			if l.Payload.ID != "" {
				s.ID = l.Payload.ID
			}
			return
		}
		if l.Type != "response_item" || l.Payload.Type != "message" {
			return
		}
		at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		if err != nil || !within(at, start, end) {
			return
		}
		var parts []string
		for _, c := range l.Payload.Content {
			parts = append(parts, c.Text)
		}
		text := strings.TrimSpace(strings.Join(parts, "\n"))
		switch l.Payload.Role {
		case "user":
			if strings.HasPrefix(text, "# AGENTS.md instructions") || (strings.HasPrefix(text, "<") && !strings.HasPrefix(text, "<send_user_message_question_reply>")) {
				return
			}
			if text != "" {
				s.Turns = append(s.Turns, Turn{Role: "user", Text: clip(text, maxUserRunes), At: at})
			}
		case "assistant":
			if l.Payload.Phase == "commentary" || text == "" {
				return
			}
			s.Turns = append(s.Turns, Turn{Role: "ai", Text: clip(text, maxAIRunes), At: at})
		}
	}); err != nil {
		return s, false
	}
	return s, len(s.Turns) > 0 && !sub && !excluded(s.Cwd, exclude)
}

// SessionFile reads one conversation log of host in full: every day, the
// user's words and the AI's answers only. It is false for a log that is
// missing, empty, a subagent's or a program's, or under an excluded directory.
func SessionFile(host, path string, exclude []string) (Session, bool) {
	if host == "codex" {
		return readCodex(path, time.Time{}, time.Time{}, exclude)
	}
	return readClaude(path, time.Time{}, time.Time{}, exclude)
}

// FindSession finds the log of a session by its id: Claude keeps
// projects/<dir>/<id>.jsonl, Codex sessions/…/rollout-…-<id>.jsonl.
func FindSession(r Roots, host, id string) string {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return ""
	}
	var root string
	var match func(rel string) bool
	switch host {
	case "claude":
		root = filepath.Join(r.Claude, "projects")
		match = func(rel string) bool { return strings.Count(rel, "/") == 1 && filepath.Base(rel) == id+".jsonl" }
	case "codex":
		root = filepath.Join(r.Codex, "sessions")
		match = func(rel string) bool {
			return strings.HasSuffix(rel, "-"+id+".jsonl") || filepath.Base(rel) == id+".jsonl"
		}
	default:
		return ""
	}
	files := recentFiles(root, time.Time{}, match)
	if len(files) == 0 {
		return ""
	}
	return files[len(files)-1]
}

// CodexSummaries returns Codex's own conversation summaries dated day
// (file names start with the date).
func CodexSummaries(r Roots, day string) []Doc {
	if r.Codex == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(r.Codex, "memories", "rollout_summaries", day+"T*.md"))
	sort.Strings(matches)
	var out []Doc
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		out = append(out, Doc{Path: m, Text: clip(string(b), 4000)})
	}
	return out
}

// HostMemoryFiles lists the hosts' own memory files: Claude's automatic memory
// (projects/*/memory/*.md) and Codex's memories (memories/*.md).
func HostMemoryFiles(r Roots) []string {
	var files []string
	if r.Claude != "" {
		m, _ := filepath.Glob(filepath.Join(r.Claude, "projects", "*", "memory", "*.md"))
		files = append(files, m...)
	}
	if r.Codex != "" {
		m, _ := filepath.Glob(filepath.Join(r.Codex, "memories", "*.md"))
		files = append(files, m...)
	}
	sort.Strings(files)
	return files
}

// Size is the approximate byte size of sessions as material.
func Size(ss []Session) int {
	n := 0
	for _, s := range ss {
		n += len(s.Host) + len(s.ID) + len(s.Cwd) + 16
		for _, t := range s.Turns {
			n += len(t.Text) + 24
		}
	}
	return n
}

// Fit shrinks sessions to about budget bytes: first AI answers get shorter,
// then AI answers go, then user turns get shorter, then later sessions are
// dropped. It returns a note for the reader when anything was cut.
func Fit(ss []Session, budget int) ([]Session, string) {
	if Size(ss) <= budget {
		return ss, ""
	}
	steps := []struct {
		ai, user int
		note     string
	}{
		{300, maxUserRunes, "AI 답을 앞부분만 남김"},
		{0, maxUserRunes, "AI 답을 빼고 사용자 말만 남김"},
		{0, 400, "사용자 말을 앞부분만 남김"},
	}
	var out []Session
	for _, st := range steps {
		out = make([]Session, 0, len(ss))
		for _, s := range ss {
			c := Session{Host: s.Host, ID: s.ID, Cwd: s.Cwd}
			for _, t := range s.Turns {
				switch {
				case t.Role == "ai" && st.ai == 0:
					continue
				case t.Role == "ai":
					t.Text = clip(t.Text, st.ai)
				default:
					t.Text = clip(t.Text, st.user)
				}
				c.Turns = append(c.Turns, t)
			}
			if len(c.Turns) > 0 {
				out = append(out, c)
			}
		}
		if Size(out) <= budget {
			return out, "재료가 커서 " + st.note
		}
	}
	for Size(out) > budget && len(out) > 1 {
		out = out[:len(out)-1]
	}
	return out, fmt.Sprintf("재료가 커서 사용자 말을 줄이고 뒤 세션 %d개를 뺌", len(ss)-len(out))
}
