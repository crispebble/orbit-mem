package memory

import (
	"database/sql"
	"strings"
)

// The activity log (설계 · OpenOrbit 4차 M5): one row per host session, kept by the
// session-start, prompt and session-end hooks. It says when, on which host, in which
// project and on what a session worked; it is not a work record (no steps or states).
const activitySchema = `
CREATE TABLE IF NOT EXISTS activity (
  host TEXT NOT NULL, session TEXT NOT NULL,
  project_scope TEXT NOT NULL DEFAULT '', project_label TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '',
  launcher TEXT NOT NULL DEFAULT 'human', parent TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL, last_at INTEGER NOT NULL, ended_at INTEGER NOT NULL DEFAULT 0,
  prompts INTEGER NOT NULL DEFAULT 0, first_prompt TEXT NOT NULL DEFAULT '', last_prompt TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(host, session));
CREATE INDEX IF NOT EXISTS activity_last ON activity(last_at);
`

// Activity hook kinds.
const (
	ActivityStart  = "start"
	ActivityPrompt = "prompt"
	ActivityEnd    = "end"
)

// ActivityPromptRunes is how much of a request the log keeps (first and last request).
const ActivityPromptRunes = 300

// Activity is one host session in the activity log. Times are Unix milliseconds.
type Activity struct {
	Host         string `json:"host"`
	Session      string `json:"session"`
	ProjectScope string `json:"projectScope,omitempty"`
	ProjectLabel string `json:"project,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Launcher     string `json:"launcher"`         // human | orbit-pi | studio | telegram | workflow
	Parent       string `json:"parent,omitempty"` // ORBIT_PARENT of the session
	StartedAt    int64  `json:"startedAt"`
	LastAt       int64  `json:"lastAt"`
	EndedAt      int64  `json:"endedAt,omitempty"`
	Prompts      int    `json:"prompts"`
	FirstPrompt  string `json:"firstPrompt,omitempty"`
	LastPrompt   string `json:"lastPrompt,omitempty"`
	// Summary is what the day or session was about, in full: a journal moved from the memories (S4-8),
	// later a retro's day summary. Hook rows leave it empty.
	Summary string `json:"summary,omitempty"`
}

// ActivityEvent is what one hook call adds to the log.
type ActivityEvent struct {
	Kind                            string // ActivityStart | ActivityPrompt | ActivityEnd
	Host, Session                   string
	Cwd, ProjectScope, ProjectLabel string
	Launcher, Parent                string
	Prompt                          string // ActivityPrompt only
	At                              int64
}

// activityText is a request as the log keeps it: secrets masked, whitespace collapsed, clipped.
func activityText(s string) string {
	return ClipRunes(strings.Join(strings.Fields(MaskSecrets(s)), " "), ActivityPromptRunes)
}

// RecordActivity adds one hook call to the log. The first call of a session makes its row
// (a missed session start is filled by the first prompt or the end); a later session start
// (resume, clear, compact) opens the row again. Empty project fields keep what was there.
func (s *Store) RecordActivity(e ActivityEvent) error {
	if e.Host == "" || e.Session == "" {
		return ErrInvalid
	}
	if e.Launcher == "" {
		e.Launcher = "human"
	}
	if e.At == 0 {
		e.At = s.nowMs()
	}
	prompts, first, last, ended := 0, "", "", int64(0)
	switch e.Kind {
	case ActivityStart:
	case ActivityPrompt:
		prompts = 1
		first = activityText(e.Prompt)
		last = first
	case ActivityEnd:
		ended = e.At
	default:
		return ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO activity(host,session,project_scope,project_label,cwd,launcher,parent,started_at,last_at,ended_at,prompts,first_prompt,last_prompt)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(host, session) DO UPDATE SET
		  project_scope=CASE WHEN excluded.project_scope<>'' THEN excluded.project_scope ELSE project_scope END,
		  project_label=CASE WHEN excluded.project_label<>'' THEN excluded.project_label ELSE project_label END,
		  cwd=CASE WHEN excluded.cwd<>'' THEN excluded.cwd ELSE cwd END,
		  launcher=excluded.launcher,
		  parent=CASE WHEN excluded.parent<>'' THEN excluded.parent ELSE parent END,
		  started_at=MIN(started_at, excluded.started_at),
		  last_at=MAX(last_at, excluded.last_at),
		  ended_at=CASE WHEN ? = 'start' THEN 0 WHEN excluded.ended_at<>0 THEN excluded.ended_at ELSE ended_at END,
		  prompts=prompts+excluded.prompts,
		  first_prompt=CASE WHEN first_prompt='' THEN excluded.first_prompt ELSE first_prompt END,
		  last_prompt=CASE WHEN excluded.prompts>0 THEN excluded.last_prompt ELSE last_prompt END`,
		e.Host, e.Session, e.ProjectScope, e.ProjectLabel, e.Cwd, e.Launcher, e.Parent, e.At, e.At, ended, prompts, first, last, e.Kind); err != nil {
		return err
	}
	return tx.Commit()
}

// ActivityFilter narrows the log. An empty field does not narrow.
type ActivityFilter struct {
	Query        string // 프로젝트·경로·세션·요청 글에 든 말
	Host         string
	ProjectScope string
	Since        int64 // 마지막 활동이 이때 이후
	Limit        int   // 0이면 20
}

// LikeEscape escapes a LIKE pattern for ESCAPE '\'.
func LikeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Activities reads the log, newest activity first.
func (s *Store) Activities(f ActivityFilter) ([]Activity, error) {
	q := `SELECT host,session,project_scope,project_label,cwd,launcher,parent,started_at,last_at,ended_at,prompts,first_prompt,last_prompt,summary FROM activity WHERE 1=1`
	var args []any
	if f.Host != "" {
		q += ` AND host=?`
		args = append(args, f.Host)
	}
	if f.ProjectScope != "" {
		q += ` AND project_scope=?`
		args = append(args, f.ProjectScope)
	}
	if f.Since > 0 {
		q += ` AND last_at>=?`
		args = append(args, f.Since)
	}
	for _, w := range strings.Fields(f.Query) {
		like := "%" + LikeEscape(w) + "%"
		q += ` AND (project_label LIKE ? ESCAPE '\' OR cwd LIKE ? ESCAPE '\' OR session LIKE ? ESCAPE '\' OR first_prompt LIKE ? ESCAPE '\' OR last_prompt LIKE ? ESCAPE '\' OR summary LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like, like, like)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	q += ` ORDER BY last_at DESC, started_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.Host, &a.Session, &a.ProjectScope, &a.ProjectLabel, &a.Cwd, &a.Launcher, &a.Parent, &a.StartedAt, &a.LastAt, &a.EndedAt, &a.Prompts, &a.FirstPrompt, &a.LastPrompt, &a.Summary); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActivityCount is how many sessions the log holds.
func (s *Store) ActivityCount() int {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM activity`).Scan(&n); err != nil && err != sql.ErrNoRows {
		return 0
	}
	return n
}
