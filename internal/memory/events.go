package memory

import (
	"encoding/json"
	"strings"
)

// Event kinds (설계 「측정」). A new metric adds a kind and its data; the table stays.
const (
	EventInjectSessionStart = "inject.session_start"
	EventInjectPrompt       = "inject.prompt"
	EventInjectDelivered    = "inject.delivered" // 호스트가 회상을 모델 요청에 실었다고 알려 옴(Orbit Code)
	EventInjectTool         = "inject.tool"      // 도구 호출 직전에 넣음(PreToolUse)
	EventToolPrefix         = "tool."
	EventProposalDecided    = "proposal.decided"
	EventCorrection         = "correction"
	EventMemoryUsed         = "memory.used"   // 세션 끝 회고가 넣은 기억마다 낸 사용 판정
	EventRetroRun           = "retro.run"     // 회고 실행 하나의 비용·토큰
	EventSourceCheck        = "source.check"  // 근거 파일이 바뀐 기억의 판정(제안하지 않은 것 포함)
	EventLegacyJudged       = "legacy.judged" // 옮겨 온 옛 기억 하나의 판정(그대로 둠 포함, 설계 · Orbit 3차 O3-3)
)

// Usage verdicts of a memory put into a session.
const (
	UsageFollowed   = "followed"   // 따름
	UsageViolated   = "violated"   // 어김
	UsageIrrelevant = "irrelevant" // 상관없음
)

var UsageVerdicts = []string{UsageFollowed, UsageViolated, UsageIrrelevant}

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

// Usage counts a memory's usage verdicts.
type Usage struct {
	Followed   int `json:"followed"`
	Violated   int `json:"violated"`
	Irrelevant int `json:"irrelevant"`
}

// UsageCounts counts the usage verdicts of each memory over days from..to (inclusive).
func (s *Store) UsageCounts(from, to string) (map[string]Usage, error) {
	evs, err := s.Events(EventFilter{FromDay: from, ToDay: to, KindPrefix: EventMemoryUsed})
	if err != nil {
		return nil, err
	}
	out := map[string]Usage{}
	for _, e := range evs {
		var d struct {
			MemoryID string `json:"memoryId"`
			Verdict  string `json:"verdict"`
		}
		if json.Unmarshal(e.Data, &d) != nil || d.MemoryID == "" {
			continue
		}
		u := out[d.MemoryID]
		switch d.Verdict {
		case UsageFollowed:
			u.Followed++
		case UsageViolated:
			u.Violated++
		case UsageIrrelevant:
			u.Irrelevant++
		}
		out[d.MemoryID] = u
	}
	return out, nil
}

// LastUse is when each memory was last put into a session, read, found or
// judged, and when the oldest event on record happened (0 when none), so
// "not used for N days" is only claimed when the record is that old.
func (s *Store) LastUse() (map[string]int64, int64, error) {
	last := map[string]int64{}
	var earliest int64
	_ = s.db.QueryRow(`SELECT COALESCE(MIN(at),0) FROM events`).Scan(&earliest)
	rows, err := s.db.Query(`SELECT at, kind, data FROM events WHERE kind IN (?,?,?,?,?)`,
		EventInjectSessionStart, EventInjectPrompt, EventToolPrefix+"memory_get", EventToolPrefix+"memory_find", EventMemoryUsed)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		var kind, data string
		if rows.Scan(&at, &kind, &data) != nil {
			continue
		}
		var d struct {
			MemoryIDs  []string `json:"memoryIds"`
			IDs        []string `json:"ids"`
			MemoryID   string   `json:"memoryId"`
			OccurredAt int64    `json:"occurredAt"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		if d.OccurredAt > 0 {
			at = d.OccurredAt
		}
		ids := append(append(d.MemoryIDs, d.IDs...), d.MemoryID)
		for _, id := range ids {
			if id != "" && at > last[id] {
				last[id] = at
			}
		}
	}
	return last, earliest, rows.Err()
}
