// Package retro keeps the retrospectives' storage: it builds the material a
// retrospective reads, takes its single submission (journal, candidate memories,
// flags and proposals) and keeps the records and the session-end queue. It never
// runs a model or a headless host: orbit-code runs the retrospectives (orb retro)
// and calls this storage through orbit-mem retro-store and the retro MCP tools
// (orbit-mem mcp --retro).
package retro

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
)

// Kinds and statuses of a retrospective.
const (
	KindDaily  = "daily"
	KindWeekly = "weekly"

	StatusRunning = "running"
	StatusDone    = "done"
	StatusEmpty   = "empty" // 재료가 없어 호스트를 부르지 않음
	StatusFailed  = "failed"
	StatusSkipped = "skipped" // 이미 끝남(결과에만 쓰임)
)

// SessionStateDays is how many days the pinned session bundles and the handled
// session-end queue are kept: Claude Code's default transcript keeping
// (cleanupPeriodDays 30).
const SessionStateDays = 30

// Keeper is the retrospectives' storage of this PC.
type Keeper struct {
	Store *memory.Store
	Cfg   config.Config
	Paths config.Paths
	Roots transcript.Roots
	Loc   *time.Location
	Now   func() time.Time
}

func (r *Keeper) now() time.Time {
	if r.Now != nil {
		return r.Now().In(r.loc())
	}
	return time.Now().In(r.loc())
}

func (r *Keeper) loc() *time.Location {
	if r.Loc != nil {
		return r.Loc
	}
	return time.Local
}

func (r *Keeper) existing(kind, period string) (id, status string, started int64) {
	_ = r.Store.DB().QueryRow(`SELECT id, status, started_at FROM retros WHERE kind=? AND period=? ORDER BY started_at DESC LIMIT 1`, kind, period).Scan(&id, &status, &started)
	return
}

// signedOutNotes are the endings (in every language) of the error of a run
// the host was not signed in for; State finds such runs by them.
func signedOutNotes() []any {
	var out []any
	for _, l := range i18n.Langs {
		out = append(out, "%"+i18n.TIn(l, "retro.signed_out_note")+"%")
	}
	return out
}

func dayOf(t time.Time) string { return t.Format("2006-01-02") }

// RetroStatus is a stored retrospective.
type RetroStatus struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Period     string   `json:"period"`
	Status     string   `json:"status"`
	Host       string   `json:"host,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Flags      []string `json:"flags,omitempty"`
	JournalID  string   `json:"journalId,omitempty"`
	Error      string   `json:"error,omitempty"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt,omitempty"`
	Session    string   `json:"session,omitempty"` // 세션 끝 회고의 세션(호스트:세션 id)
}

const statusCols = `id,kind,period,status,host,summary,flags,journal_id,error,started_at,finished_at,session`

func scanStatus(row interface{ Scan(...any) error }) (RetroStatus, error) {
	var st RetroStatus
	var flags string
	err := row.Scan(&st.ID, &st.Kind, &st.Period, &st.Status, &st.Host, &st.Summary, &flags, &st.JournalID, &st.Error, &st.StartedAt, &st.FinishedAt, &st.Session)
	st.Flags = parseFlags(flags)
	return st, err
}

func parseFlags(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// memoryIDs reads the memoryIds of an injection event's data.
func memoryIDs(data string) []string {
	var d struct {
		MemoryIDs []string `json:"memoryIds"`
	}
	_ = json.Unmarshal([]byte(data), &d)
	return d.MemoryIDs
}

// Status reads the latest retrospective of kind and period.
func Status(s *memory.Store, kind, period string) (RetroStatus, error) {
	return scanStatus(s.DB().QueryRow(`SELECT `+statusCols+` FROM retros WHERE kind=? AND period=? ORDER BY started_at DESC LIMIT 1`, kind, period))
}

// Recent lists the latest n retrospectives, newest first.
func Recent(s *memory.Store, n int) ([]RetroStatus, error) {
	rows, err := s.DB().Query(`SELECT `+statusCols+` FROM retros ORDER BY started_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetroStatus
	for rows.Next() {
		st, err := scanStatus(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func getStatus(s *memory.Store, id string) (RetroStatus, error) {
	st, err := scanStatus(s.DB().QueryRow(`SELECT `+statusCols+` FROM retros WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return st, i18n.Errorf("retro.err.no_retro", id)
	}
	return st, err
}

func itoa(n int) string { return strconv.Itoa(n) }
