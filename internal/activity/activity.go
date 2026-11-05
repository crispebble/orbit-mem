// Package activity finds what sessions did on this PC: the activity log the memory hooks
// keep in memory.db, and the sessions of the old work record (work.db), read only. The work
// record itself (steps, states, decision cards) moved to orbit-code; work.db is kept as is
// and never written here.
package activity

import (
	"database/sql"
	"errors"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/sqlitedb"
)

// Sources of a record.
const (
	SourceActivity = "activity" // memory.db 활동 기록(세션 훅)
	SourceWorkDB   = "work.db"  // 옛 작업 기록의 세션(읽기 전용)
)

// SummaryRunes is how much of a day summary activity_find and `orbit-mem activity` show; the full text stays in memory.db.
const SummaryRunes = 600

// Record is one session, from the activity log or the old work record.
type Record struct {
	Source       string   `json:"source"`
	Host         string   `json:"host"`
	Session      string   `json:"session"`
	Project      string   `json:"project,omitempty"`
	ProjectScope string   `json:"projectScope,omitempty"`
	Cwd          string   `json:"cwd,omitempty"`
	Launcher     string   `json:"launcher"`
	Parent       string   `json:"parent,omitempty"`
	StartedAt    int64    `json:"startedAt"`
	LastAt       int64    `json:"lastAt"`
	EndedAt      int64    `json:"endedAt,omitempty"`
	Prompts      int      `json:"prompts,omitempty"`
	FirstPrompt  string   `json:"firstPrompt,omitempty"`
	LastPrompt   string   `json:"lastPrompt,omitempty"`
	Works        []string `json:"works,omitempty"`   // 옛 작업 기록: 그 세션이 연 작업의 제목
	Summary      string   `json:"summary,omitempty"` // 하루 요약(옮겨 온 일지, S4-8)
}

// Query narrows the search. An empty field does not narrow.
type Query struct {
	Query        string
	Host         string
	ProjectScope string
	Since        int64 // Unix ms
	Limit        int   // 0이면 20, 최대 100
}

// Result is the records, newest first. LegacyError says why work.db could not be read.
type Result struct {
	Items       []Record `json:"items"`
	LegacyError string   `json:"legacyError,omitempty"`
}

func (q Query) limit() int {
	if q.Limit <= 0 {
		return 20
	}
	return min(q.Limit, 100)
}

// Find reads the activity log and the old work record together. A missing work.db is no
// error; an unreadable one is named in LegacyError and the activity log is still given.
// A session in both is one record: the activity log's, with the old work titles.
func Find(s *memory.Store, workDB string, q Query) (Result, error) {
	limit := q.limit()
	acts, err := s.Activities(memory.ActivityFilter{Query: q.Query, Host: q.Host, ProjectScope: q.ProjectScope, Since: q.Since, Limit: limit})
	if err != nil {
		return Result{}, err
	}
	res := Result{Items: []Record{}}
	index := map[string]int{}
	for _, a := range acts {
		index[a.Host+"\x00"+a.Session] = len(res.Items)
		res.Items = append(res.Items, Record{Source: SourceActivity, Host: a.Host, Session: a.Session, Project: a.ProjectLabel, ProjectScope: a.ProjectScope,
			Cwd: a.Cwd, Launcher: a.Launcher, Parent: a.Parent, StartedAt: a.StartedAt, LastAt: a.LastAt, EndedAt: a.EndedAt,
			Prompts: a.Prompts, FirstPrompt: a.FirstPrompt, LastPrompt: a.LastPrompt, Summary: memory.ClipRunes(a.Summary, SummaryRunes)})
	}
	old, err := Legacy(workDB, Query{Query: q.Query, Host: q.Host, ProjectScope: q.ProjectScope, Since: q.Since, Limit: limit})
	if err != nil {
		res.LegacyError = err.Error()
	}
	for _, r := range old {
		if i, ok := index[r.Host+"\x00"+r.Session]; ok {
			res.Items[i].Works = r.Works
			continue
		}
		res.Items = append(res.Items, r)
	}
	// 새 기록만 질의에 맞고 옛 제목으로만 맞는 세션이 빠지지 않게, 합친 뒤에 다시 고르지 않는다.
	sort.SliceStable(res.Items, func(i, j int) bool { return res.Items[i].LastAt > res.Items[j].LastAt })
	if len(res.Items) > limit {
		res.Items = res.Items[:limit]
	}
	return res, nil
}

// openLegacy opens work.db read only (mode=ro: no writes, no new file). A missing file is (nil, nil).
func openLegacy(path string) (*sql.DB, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := sql.Open("sqlite", sqlitedb.FileURI(path)+"?mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Legacy reads the sessions of the old work record with the titles of the works each opened.
func Legacy(path string, q Query) ([]Record, error) {
	db, err := openLegacy(path)
	if db == nil || err != nil {
		return nil, err
	}
	defer db.Close()
	titles := `COALESCE((SELECT group_concat(title, char(10)) FROM (SELECT title FROM works w WHERE w.host=s.host AND w.session=s.id AND w.title<>'' ORDER BY w.opened_at)), '')`
	sqlq := `SELECT s.host, s.id, s.cwd, s.project_scope, s.project_label, s.started_at, s.last_seen, s.ended_at, s.launcher, ` + titles + ` AS titles FROM sessions s WHERE 1=1`
	var args []any
	if q.Host != "" {
		sqlq += ` AND s.host=?`
		args = append(args, q.Host)
	}
	if q.ProjectScope != "" {
		sqlq += ` AND s.project_scope=?`
		args = append(args, q.ProjectScope)
	}
	if q.Since > 0 {
		sqlq += ` AND s.last_seen>=?`
		args = append(args, q.Since)
	}
	for _, w := range strings.Fields(q.Query) {
		like := "%" + memory.LikeEscape(w) + "%"
		sqlq += ` AND (s.project_label LIKE ? ESCAPE '\' OR s.cwd LIKE ? ESCAPE '\' OR s.id LIKE ? ESCAPE '\' OR ` + titles + ` LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like)
	}
	sqlq += ` ORDER BY s.last_seen DESC LIMIT ?`
	args = append(args, q.limit())
	rows, err := db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r := Record{Source: SourceWorkDB}
		var titles string
		if err := rows.Scan(&r.Host, &r.Session, &r.Cwd, &r.ProjectScope, &r.Project, &r.StartedAt, &r.LastAt, &r.EndedAt, &r.Launcher, &titles); err != nil {
			return nil, err
		}
		if r.Launcher == "" {
			r.Launcher = "human" // launcher 칸이 생기기 전의 세션은 사람이 연 것이다
		}
		if titles != "" {
			r.Works = strings.Split(titles, "\n")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LegacyCount is how many sessions the old work record holds (0 without work.db).
func LegacyCount(path string) (int, error) {
	db, err := openLegacy(path)
	if db == nil || err != nil {
		return 0, err
	}
	defer db.Close()
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	return n, err
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
