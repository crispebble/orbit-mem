// Package activity finds what sessions did on this PC: the activity log the memory hooks
// keep in memory.db. The old work record (work.db) is no longer read (2026-11-08, ORBIT-13);
// the file is left as it is.
package activity

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// SourceActivity is the source of a record: the memory.db activity log (session hooks).
const SourceActivity = "activity"

// SummaryRunes is how much of a day summary activity_find and `orbit-mem activity` show; the full text stays in memory.db.
const SummaryRunes = 600

// Record is one session of the activity log.
type Record struct {
	Source       string `json:"source"`
	Host         string `json:"host"`
	Session      string `json:"session"`
	Project      string `json:"project,omitempty"`
	ProjectScope string `json:"projectScope,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Launcher     string `json:"launcher"`
	Parent       string `json:"parent,omitempty"`
	StartedAt    int64  `json:"startedAt"`
	LastAt       int64  `json:"lastAt"`
	EndedAt      int64  `json:"endedAt,omitempty"`
	Prompts      int    `json:"prompts,omitempty"`
	FirstPrompt  string `json:"firstPrompt,omitempty"`
	LastPrompt   string `json:"lastPrompt,omitempty"`
	Summary      string `json:"summary,omitempty"` // 하루 요약(옮겨 온 일지, S4-8)
}

// Query narrows the search. An empty field does not narrow.
type Query struct {
	Query        string
	Host         string
	ProjectScope string
	Since        int64 // Unix ms
	Limit        int   // 0이면 20, 최대 100
}

// Result is the records, newest first.
type Result struct {
	Items []Record `json:"items"`
}

func (q Query) limit() int {
	if q.Limit <= 0 {
		return 20
	}
	return min(q.Limit, 100)
}

// Find reads the activity log.
func Find(s *memory.Store, q Query) (Result, error) {
	limit := q.limit()
	acts, err := s.Activities(memory.ActivityFilter{Query: q.Query, Host: q.Host, ProjectScope: q.ProjectScope, Since: q.Since, Limit: limit})
	if err != nil {
		return Result{}, err
	}
	res := Result{Items: []Record{}}
	for _, a := range acts { // 마지막 활동이 새로운 순
		res.Items = append(res.Items, Record{Source: SourceActivity, Host: a.Host, Session: a.Session, Project: a.ProjectLabel, ProjectScope: a.ProjectScope,
			Cwd: a.Cwd, Launcher: a.Launcher, Parent: a.Parent, StartedAt: a.StartedAt, LastAt: a.LastAt, EndedAt: a.EndedAt,
			Prompts: a.Prompts, FirstPrompt: a.FirstPrompt, LastPrompt: a.LastPrompt, Summary: memory.ClipRunes(a.Summary, SummaryRunes)})
	}
	return res, nil
}

var relative = regexp.MustCompile(`^(\d+)([dh])$`)

// ErrSince says that a since value has an unknown form.
var ErrSince = errors.New("since is YYYY-MM-DD, an RFC 3339 time, or Nd / Nh (days / hours ago)")

// ParseSince reads a since value as Unix ms: YYYY-MM-DD (local midnight), RFC 3339, or Nd / Nh
// before now. Empty is 0 (no limit).
func ParseSince(v string, now time.Time) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	if m := relative.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := 24 * time.Hour
		if m[2] == "h" {
			unit = time.Hour
		}
		return now.Add(-time.Duration(n) * unit).UnixMilli(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
		return t.UnixMilli(), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UnixMilli(), nil
	}
	return 0, ErrSince
}
