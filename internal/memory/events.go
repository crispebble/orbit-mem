package memory

import (
	"encoding/json"
	"strings"
)

// Event kinds (설계 「측정」). A new metric adds a kind and its data; the table stays.
const (
	EventInjectSessionStart = "inject.session_start"
	EventInjectPrompt       = "inject.prompt"
	EventToolPrefix         = "tool."
	EventProposalDecided    = "proposal.decided"
	EventCorrection         = "correction"
)

const eventsSchema = `
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, day TEXT NOT NULL, kind TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT '',
  data TEXT NOT NULL DEFAULT '{}');
CREATE INDEX IF NOT EXISTS events_day ON events(day, kind);
CREATE INDEX IF NOT EXISTS events_session ON events(session) WHERE session <> '';
`

// Event is one thing that happened on this PC, kept for the metrics.
type Event struct {
	ID      int64           `json:"id"`
	At      int64           `json:"at"`
	Day     string          `json:"day"` // 비우면 기록 시각의 날짜. 회고가 남기는 사건은 대상 날짜
	Kind    string          `json:"kind"`
	Host    string          `json:"host,omitempty"`
	Session string          `json:"session,omitempty"`
	Scope   string          `json:"scope,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// RecordEvent stores an event with its kind-specific data.
func (s *Store) RecordEvent(e Event, data any) error {
	if e.At == 0 {
		e.At = s.nowMs()
	}
	if e.Day == "" {
		e.Day = Day(s.Now())
	}
	b := []byte("{}")
	if data != nil {
		var err error
		if b, err = json.Marshal(data); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`INSERT INTO events(at,day,kind,host,session,scope,data) VALUES(?,?,?,?,?,?,?)`,
		e.At, e.Day, e.Kind, e.Host, e.Session, e.Scope, string(b))
	return err
}

// EventFilter selects events by day range (inclusive) and kind prefix.
type EventFilter struct {
	FromDay, ToDay string
	KindPrefix     string
}

// Events returns matching events, oldest first.
func (s *Store) Events(f EventFilter) ([]Event, error) {
	q := `SELECT id,at,day,kind,host,session,scope,data FROM events WHERE day>=? AND day<=?`
	args := []any{f.FromDay, f.ToDay}
	if f.KindPrefix != "" {
		q += ` AND substr(kind,1,?)=?`
		args = append(args, len(f.KindPrefix), f.KindPrefix)
	}
	rows, err := s.db.Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.ID, &e.At, &e.Day, &e.Kind, &e.Host, &e.Session, &e.Scope, &data); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// InjectedIn says whether a hook put the memory into the session (injected),
// and whether the session's injections are on record at all (known).
func (s *Store) InjectedIn(session, memoryID string) (injected, known bool) {
	if session == "" {
		return false, false
	}
	rows, err := s.db.Query(`SELECT data FROM events WHERE session=? AND kind IN (?,?)`, session, EventInjectSessionStart, EventInjectPrompt)
	if err != nil {
		return false, false
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if rows.Scan(&data) != nil {
			continue
		}
		known = true
		var d struct {
			MemoryIDs []string `json:"memoryIds"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		for _, id := range d.MemoryIDs {
			if id == memoryID {
				injected = true
			}
		}
	}
	return injected, known
}

// ClipRunes cuts s to n characters with an ellipsis.
func ClipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
