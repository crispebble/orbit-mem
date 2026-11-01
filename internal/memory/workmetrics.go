package memory

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// AU-7(설계 「능숙해짐 측정」): OpenOrbit이 작업 기록(work.db)에서 계산한 작업별 지표를
// 사건 work.metrics로 넣는다. orbit-mem은 work.db를 읽지 않고 OpenOrbit을 부르지 않는다.
// 변경 효과 원장은 관찰할 기억이 붙은 작업의 지표를 변경 전·후로 견준다.

// EventWorkMetrics is one work's metrics as OpenOrbit computed them; the newest per work wins.
const EventWorkMetrics = "work.metrics"

// WorkMetricsHost is the host of work events: OpenOrbit's step recall and work retrospective use it too.
const WorkMetricsHost = "openorbit"

// WorkMetrics are the numbers of one work (계획 · AU-7 「지표 정의」).
type WorkMetrics struct {
	Asked       int     `json:"asked"`       // 멈추고 물은 결정 카드
	Assumed     int     `json:"assumed"`     // 가정하고 진행한 결정 지점
	Overturned  int     `json:"overturned"`  // 답이 가정과 달라 다시 연 결정
	Repeats     int     `json:"repeats"`     // 같은 실패 반복
	Replans     int     `json:"replans"`     // 계획 다시 쓰기
	FirstPassed int     `json:"firstPassed"` // 첫 시도에 검증을 통과한 단계
	FirstSteps  int     `json:"firstSteps"`  // 실행이 있었던 단계
	CostUSD     float64 `json:"costUsd"`
	Turns       int     `json:"turns"`
	Minutes     float64 `json:"minutes"`
}

// WorkSession is a session window of a work: corrections and injections in it belong to the work.
// To 0 is open (the session's next work has not opened).
type WorkSession struct {
	Host    string `json:"host,omitempty"`
	Session string `json:"session"`
	From    int64  `json:"from"`
	To      int64  `json:"to,omitempty"`
}

// WorkRecord is what OpenOrbit hands in for one work.
type WorkRecord struct {
	WorkID   string        `json:"workId"`
	EndedAt  int64         `json:"endedAt"`
	Status   string        `json:"status,omitempty"`
	Project  string        `json:"project,omitempty"`
	Kind     string        `json:"kind,omitempty"`
	Size     string        `json:"size,omitempty"`
	Sessions []WorkSession `json:"sessions,omitempty"`
	Metrics  WorkMetrics   `json:"metrics"`
}

// WorkRef names a work and its session windows (for the correction count).
type WorkRef struct {
	WorkID   string        `json:"workId"`
	Sessions []WorkSession `json:"sessions,omitempty"`
}

var workIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (r WorkRecord) validate() error {
	m := r.Metrics
	bad := !workIDPattern.MatchString(r.WorkID) || r.EndedAt <= 0 || len(r.Sessions) > 20 ||
		m.Asked < 0 || m.Assumed < 0 || m.Overturned < 0 || m.Repeats < 0 || m.Replans < 0 ||
		m.FirstPassed < 0 || m.FirstSteps < 0 || m.FirstPassed > m.FirstSteps || m.CostUSD < 0 || m.Turns < 0 || m.Minutes < 0
	for _, w := range r.Sessions {
		bad = bad || strings.TrimSpace(w.Session) == "" || len(w.Session) > 200 || (w.To != 0 && w.To < w.From)
	}
	if bad || len(r.Project) > 500 || len(r.Kind) > 40 || len(r.Size) > 40 || len(r.Status) > 40 {
		return i18n.Errorf("memory.err.work_record", ErrInvalid)
	}
	return nil
}

// RecordWorkMetrics keeps each work's metrics as an event (occurredAt = the work's end) unless
// the newest event of that work already says the same. It returns how many it recorded.
func (s *Store) RecordWorkMetrics(rs []WorkRecord) (int, error) {
	for _, r := range rs {
		if err := r.validate(); err != nil {
			return 0, err
		}
	}
	n := 0
	for _, r := range rs {
		b, err := json.Marshal(r)
		if err != nil {
			return n, err
		}
		var last string
		err = s.db.QueryRow(`SELECT data FROM events WHERE kind=? AND session=? ORDER BY id DESC LIMIT 1`, EventWorkMetrics, r.WorkID).Scan(&last)
		if err == nil && sameWorkRecord(last, r) {
			continue
		}
		data := map[string]any{}
		_ = json.Unmarshal(b, &data)
		data["occurredAt"] = r.EndedAt
		if err := s.RecordEvent(Event{Kind: EventWorkMetrics, Host: WorkMetricsHost, Session: r.WorkID, Scope: r.Project}, data); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func sameWorkRecord(stored string, r WorkRecord) bool {
	var old WorkRecord
	if json.Unmarshal([]byte(stored), &old) != nil {
		return false
	}
	a, _ := json.Marshal(old)
	b, _ := json.Marshal(r)
	return string(a) == string(b)
}

// WorkRecords are the newest records of the works that ended in [fromMs, toMs).
func (s *Store) WorkRecords(fromMs, toMs int64) ([]WorkRecord, error) {
	rows, err := s.db.Query(`SELECT session, data FROM events WHERE kind=? ORDER BY id`, EventWorkMetrics)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]WorkRecord{}
	var order []string
	for rows.Next() {
		var session, data string
		if err := rows.Scan(&session, &data); err != nil {
			return nil, err
		}
		var r WorkRecord
		if json.Unmarshal([]byte(data), &r) != nil || r.WorkID == "" {
			continue
		}
		if _, ok := latest[r.WorkID]; !ok {
			order = append(order, r.WorkID)
		}
		latest[r.WorkID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []WorkRecord
	for _, id := range order {
		if r := latest[id]; r.EndedAt >= fromMs && r.EndedAt < toMs {
			out = append(out, r)
		}
	}
	return out, nil
}

// inWindow says whether an event of host and session at time at falls in one of the work's windows,
// or belongs to the work's own session (host openorbit, session = work id).
func (r WorkRef) inWindow(host, session string, at int64) bool {
	if session == "" {
		return false
	}
	if session == r.WorkID {
		return true
	}
	for _, w := range r.Sessions {
		if w.Session != session || (w.Host != "" && host != "" && w.Host != host) {
			continue
		}
		if at >= w.From && (w.To == 0 || at < w.To) {
			return true
		}
	}
	return false
}

// eventTime is when an event happened: its occurredAt when it has one, else when it was recorded.
func eventTime(e Event) int64 {
	var d struct {
		OccurredAt int64 `json:"occurredAt"`
	}
	_ = json.Unmarshal(e.Data, &d)
	if d.OccurredAt > 0 {
		return d.OccurredAt
	}
	return e.At
}

// sessionEvents are the events of the given kinds whose session is one of the works' ids or windows.
func (s *Store) sessionEvents(refs []WorkRef, kinds ...string) ([]Event, error) {
	set := map[string]bool{}
	for _, r := range refs {
		if r.WorkID != "" {
			set[r.WorkID] = true
		}
		for _, w := range r.Sessions {
			if w.Session != "" {
				set[w.Session] = true
			}
		}
	}
	if len(set) == 0 || len(kinds) == 0 {
		return nil, nil
	}
	var args []any
	for _, k := range kinds {
		args = append(args, k)
	}
	q := `SELECT id,at,day,kind,host,session,scope,data FROM events WHERE kind IN (?` + strings.Repeat(",?", len(kinds)-1) + `) AND session IN (?` + strings.Repeat(",?", len(set)-1) + `) ORDER BY id`
	for id := range set {
		args = append(args, id)
	}
	rows, err := s.db.Query(q, args...)
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

// WorkCorrections counts the user's corrections of each work: those the work retrospective
// recorded (session = work id) and those in the work's session windows.
func (s *Store) WorkCorrections(refs []WorkRef) (map[string]int, error) {
	out := make(map[string]int, len(refs))
	evs, err := s.sessionEvents(refs, EventCorrection)
	if err != nil {
		return out, err
	}
	for _, r := range refs {
		out[r.WorkID] = 0
		for _, e := range evs {
			if r.inWindow(e.Host, e.Session, eventTime(e)) {
				out[r.WorkID]++
			}
		}
	}
	return out, nil
}

// Trouble is a work's problems for the ledger: questions that stopped it, overturned
// assumptions, the user's corrections, repeated failures and replans. Assuming and going on
// (A4) is the wanted behaviour, so it is not trouble.
func (m WorkMetrics) Trouble(corrections int) int {
	return m.Asked + m.Overturned + corrections + m.Repeats + m.Replans
}

// observeWorks adds the works that had one of ids put in (by step recall or into one of their
// sessions) to obs: before or after obs.Split by the work's end (a work given the memory before
// the change that ended after it counts after: it ran with the changed memory's latest state).
func (s *Store) observeWorks(obs *Observation, ids []string) error {
	recs, err := s.WorkRecords(obs.From, obs.To)
	if err != nil || len(recs) == 0 {
		return err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	refs := make([]WorkRef, len(recs))
	for i, r := range recs {
		refs[i] = WorkRef{WorkID: r.WorkID, Sessions: r.Sessions}
	}
	injects, err := s.sessionEvents(refs, EventInjectSessionStart, EventInjectPrompt)
	if err != nil {
		return err
	}
	corrections, err := s.WorkCorrections(refs)
	if err != nil {
		return err
	}
	for i, r := range recs {
		given := false
		for _, e := range injects {
			if given || !refs[i].inWindow(e.Host, e.Session, e.At) {
				continue
			}
			var d struct {
				MemoryIDs []string `json:"memoryIds"`
			}
			_ = json.Unmarshal(e.Data, &d)
			for _, id := range d.MemoryIDs {
				if want[id] {
					given = true
					break
				}
			}
		}
		if !given {
			continue
		}
		win := &obs.Before
		if r.EndedAt >= obs.Split {
			win = &obs.After
		}
		win.Works++
		win.Trouble += r.Metrics.Trouble(corrections[r.WorkID])
		win.FirstPassed += r.Metrics.FirstPassed
		win.FirstSteps += r.Metrics.FirstSteps
	}
	return nil
}
