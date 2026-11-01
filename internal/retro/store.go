package retro

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/sqlitedb"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
)

// The storage half of a retrospective run (OpenOrbit runs the host in between):
//
//	prepare  checks and builds the material, keeps it in retro/<id>/material.json
//	         and records the run as running (or empty without material);
//	host     reads the material with the retro MCP tools and submits once (Submit);
//	finish   removes the material, records the run's cost event and closes a run
//	         that did not submit as failed.
//
// Invariants: a retrospective's conversation excerpts never outlive its run
// (finish removes them, failed or not); every run that reached prepare's
// running state gets exactly one retro.run event (finish is idempotent); a run
// that submitted stays done whatever the host printed afterwards.

// submittingWait is how long finish waits for a submission still being written
// when the host ended (the submitting status).
var submittingWait = 2 * time.Second

// Prepared is the outcome of a prepare.
type Prepared struct {
	Status       string // running | empty | skipped | capped
	ID           string
	Kind         string
	Period       string
	Session      string // 세션 끝 회고의 세션(호스트:세션 id)
	SourceChecks int    // 하루 회고: 모델이 판정할 근거 파일 수
}

// MarshalJSON writes {"status","id","kind","period"} and, for a running one,
// "session" and "sourceChecks" too (OpenOrbit's orb retro reads these keys).
func (p Prepared) MarshalJSON() ([]byte, error) {
	type base struct {
		Status string `json:"status"`
		ID     string `json:"id,omitempty"`
		Kind   string `json:"kind"`
		Period string `json:"period"`
	}
	b := base{p.Status, p.ID, p.Kind, p.Period}
	if p.Status != StatusRunning {
		return json.Marshal(b)
	}
	return json.Marshal(struct {
		base
		Session      string `json:"session"`
		SourceChecks int    `json:"sourceChecks"`
	}{b, p.Session, p.SourceChecks})
}

// Prepare checks a daily or weekly retrospective and, unless it already
// finished, builds its material and begins it for host.
func (r *Keeper) Prepare(kind, period, host string) (Prepared, error) {
	res := Prepared{Kind: kind, Period: period}
	if kind != KindDaily && kind != KindWeekly {
		return res, i18n.Errorf("retro.err.kind")
	}
	start, err := time.ParseInLocation("2006-01-02", period, r.loc())
	if err != nil {
		return res, i18n.Errorf("retro.err.date_format", period)
	}
	today := dayOf(r.now())
	if kind == KindDaily && period >= today {
		return res, i18n.Errorf("retro.err.daily_until_yesterday", period)
	}
	if kind == KindWeekly && (start.Weekday() != time.Monday || dayOf(start.AddDate(0, 0, 7)) > today) {
		return res, i18n.Errorf("retro.err.weekly_monday", period)
	}
	if _, st, _ := r.existing(kind, period); st == StatusDone || st == StatusEmpty {
		res.Status = StatusSkipped
		return res, nil
	}
	var checks []SourceCheck
	if kind == KindDaily {
		checks = r.checkSources()
	}
	mat, err := r.material(kind, period)
	if err != nil {
		return res, err
	}
	mat.SourceChecks = checks
	return r.begin(mat, host)
}

// begin records a new run of mat for host. Empty material finishes at once
// (no host is needed); otherwise the material is kept for the host and the run
// is running.
func (r *Keeper) begin(mat Material, host string) (Prepared, error) {
	res := Prepared{Kind: mat.Kind, Period: mat.Period, Session: mat.Session}
	nowMs := r.now().UnixMilli()
	res.ID = memory.NewID("r-")
	mat.ID = res.ID
	if mat.empty() {
		res.Status = StatusEmpty
		_, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,finished_at,summary,session) VALUES(?,?,?,?,?,?,?,?,?)`,
			res.ID, mat.Kind, mat.Period, StatusEmpty, "", nowMs, nowMs, emptySummary(), mat.Session)
		return res, err
	}
	dir, err := r.keepMaterial(mat)
	if err != nil {
		return res, err
	}
	if _, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,session) VALUES(?,?,?,?,?,?,?)`,
		res.ID, mat.Kind, mat.Period, StatusRunning, host, nowMs, mat.Session); err != nil {
		os.RemoveAll(dir) // 줄 없이 재료만 남기지 않는다
		return res, err
	}
	res.Status, res.SourceChecks = StatusRunning, len(mat.SourceChecks)
	return res, nil
}

func emptySummary() string { return i18n.T("retro.no_material") }

// keepMaterial writes mat (its ID set) to retro/<id>/material.json for the host
// and returns that directory.
func (r *Keeper) keepMaterial(mat Material) (string, error) {
	dir := filepath.Join(r.Paths.RetroDir(), mat.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, _ := json.Marshal(mat)
	if err := os.WriteFile(filepath.Join(dir, "material.json"), b, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// HostResult is what OpenOrbit tells finish about the host run.
type HostResult struct {
	Model        string  `json:"model"`
	CostKnown    bool    `json:"costKnown"`
	CostUSD      float64 `json:"costUsd"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	ExecError    string  `json:"execError"` // 호스트 실행 오류("" 없음)
	Text         string  `json:"text"`      // 호스트가 낸 답(또는 출력)
	SignedOut    bool    `json:"signedOut"` // 호스트가 로그인되지 않아 모델에 닿지 못했다
}

// Finished is the outcome of a finish: done, or failed with the error kept on the run.
type Finished struct {
	ID        string
	Kind      string
	Period    string
	Status    string
	Error     string
	SignedOut bool
}

// MarshalJSON writes {"id","kind","period","status"} and, for a failed run,
// "error" and "signedOut" too.
func (f Finished) MarshalJSON() ([]byte, error) {
	type base struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Period string `json:"period"`
		Status string `json:"status"`
	}
	b := base{f.ID, f.Kind, f.Period, f.Status}
	if f.Status != StatusFailed {
		return json.Marshal(b)
	}
	return json.Marshal(struct {
		base
		Error     string `json:"error"`
		SignedOut bool   `json:"signedOut"`
	}{b, f.Error, f.SignedOut})
}

// Finish closes a run after its host ended: the material goes, the run's cost
// event is kept, and a run that did not submit is failed with the host's error
// and the tail of its answer. Finishing a run again returns how it ended
// without a second event.
func (r *Keeper) Finish(id string, h HostResult) (Finished, error) {
	st, err := getStatus(r.Store, id)
	if err != nil {
		return Finished{ID: id}, err
	}
	res := Finished{ID: id, Kind: st.Kind, Period: st.Period}
	if st.Status != StatusRunning && st.Status != "submitting" && st.Status != StatusDone && st.Status != StatusFailed {
		return res, i18n.Errorf("retro.err.not_running", id, st.Status)
	}
	if r.finished(id) { // 이미 마쳤다: 사건을 두 번 남기지 않는다
		res.Status, res.Error = st.Status, st.Error
		if st.Status == StatusFailed {
			res.SignedOut = signedOutError(st.Error)
		}
		return res, nil
	}
	status := st.Status
	if status == "submitting" { // 제출 처리 중에 호스트가 끝났다
		time.Sleep(submittingWait)
		_ = r.Store.DB().QueryRow(`SELECT status FROM retros WHERE id=?`, id).Scan(&status)
	}
	dir := filepath.Join(r.Paths.RetroDir(), id)
	checks := 0
	if mat, err := loadMaterial(r.Paths, id); err == nil {
		res.Kind, checks = mat.Kind, len(mat.SourceChecks)
	}
	os.RemoveAll(dir) // 재료(대화 발췌)는 남기지 않는다. 다시 돌 때 새로 만든다
	run := map[string]any{"retroId": id, "retroKind": res.Kind, "model": h.Model, "ok": status == StatusDone, "costKnown": h.CostKnown,
		"sourceChecks": checks}
	if h.CostKnown {
		run["costUsd"], run["inputTokens"], run["outputTokens"] = h.CostUSD, h.InputTokens, h.OutputTokens
	}
	// 사건을 남기지 못해도 마침은 이어 간다: 재료는 이미 지웠으니 실행 줄을 닫아야 한다.
	_ = r.Store.RecordEvent(memory.Event{Kind: memory.EventRetroRun, Host: st.Host, Session: st.Session}, run)
	if status == StatusDone {
		res.Status = StatusDone
		return res, nil
	}
	msg := i18n.T("retro.host_no_result")
	if h.ExecError != "" {
		msg += ": " + h.ExecError
	}
	if tail := strings.TrimSpace(h.Text); tail != "" {
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		msg += "\n" + tail
	}
	if h.SignedOut {
		msg += "\n" + i18n.T("retro.signed_out_note")
	}
	res.Status, res.Error, res.SignedOut = StatusFailed, msg, h.SignedOut
	if _, err := r.Store.DB().Exec(`UPDATE retros SET status=?, finished_at=?, error=? WHERE id=?`, StatusFailed, r.now().UnixMilli(), msg, id); err != nil {
		return res, err
	}
	return res, nil
}

// finished says whether finish already kept the run's event.
func (r *Keeper) finished(id string) bool {
	var n int
	_ = r.Store.DB().QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND data LIKE ?`, memory.EventRetroRun, `%"retroId":"`+id+`"%`).Scan(&n)
	return n > 0
}

// signedOutError says whether a failed run's error ends in the signed-out note
// of any language.
func signedOutError(msg string) bool {
	for _, l := range i18n.Langs {
		if strings.Contains(msg, i18n.TIn(l, "retro.signed_out_note")) {
			return true
		}
	}
	return false
}

// LegacyState is how far the old-memory judging got.
type LegacyState struct {
	Total  int `json:"total"`
	Judged int `json:"judged"`
	Left   int `json:"left"`
}

// State is what OpenOrbit's catch-up needs to decide what to run.
type State struct {
	Today            string      `json:"today"`
	FinishedDaily    []string    `json:"finishedDaily"`   // 끝난(done·empty) 하루 회고의 날, 오름차순
	LegacyRunsToday  int         `json:"legacyRunsToday"` // 오늘 옛 기억 판정 수(로그인 실패는 빼고)
	Queue            int         `json:"queue"`
	PendingProposals int         `json:"pendingProposals"`
	Legacy           LegacyState `json:"legacy"`
}

// State reads the retrospectives' state.
func (r *Keeper) State() (State, error) {
	st := State{Today: dayOf(r.now()), FinishedDaily: []string{}}
	rows, err := r.Store.DB().Query(`SELECT DISTINCT period FROM retros WHERE kind=? AND status IN (?,?) ORDER BY period`, KindDaily, StatusDone, StatusEmpty)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			st.FinishedDaily = append(st.FinishedDaily, p)
		}
	}
	rows.Close()
	if st.LegacyRunsToday, err = r.legacyRunsToday(); err != nil {
		return st, err
	}
	if st.Queue, err = QueueCount(r.Store); err != nil {
		return st, err
	}
	st.PendingProposals = r.Store.PendingCount()
	c, err := r.Store.CountLegacy()
	if err != nil {
		return st, err
	}
	st.Legacy = LegacyState{Total: c.Total, Judged: c.Judged, Left: c.Left}
	return st, nil
}

// QueueCount counts the sessions waiting for their session-end retrospective.
func QueueCount(s *memory.Store) (int, error) {
	var n int
	err := s.DB().QueryRow(`SELECT COUNT(*) FROM session_ends WHERE retro_id=''`).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// DaySession is a session of a day's material, as retro-store sessions lists it.
type DaySession struct {
	Host  string `json:"host"`
	ID    string `json:"id"`
	Cwd   string `json:"cwd"`
	Turns int    `json:"turns"`
}

// Sessions lists the sessions a daily retrospective of day would read and the
// number of Codex summaries of that day.
func (r *Keeper) Sessions(day string) ([]DaySession, int, error) {
	if _, err := time.ParseInLocation("2006-01-02", day, r.loc()); err != nil {
		return nil, 0, i18n.Errorf("retro.err.date_format", day)
	}
	sessions, err := transcript.Collect(r.Roots, day, r.loc(), []string{r.Paths.Home})
	if err != nil {
		return nil, 0, err
	}
	out := []DaySession{}
	for _, ss := range sessions {
		out = append(out, DaySession{Host: ss.Host, ID: ss.ID, Cwd: ss.Cwd, Turns: len(ss.Turns)})
	}
	return out, len(transcript.CodexSummaries(r.Roots, day)), nil
}

// Backup copies the memory database (a failure stops it) and, once a day, the
// old work database (a failure is only reported).
func (r *Keeper) Backup() (memoryPath, workPath string, errs []string, err error) {
	keep := max(r.Cfg.BackupKeep, 1)
	if memoryPath, err = r.Store.Backup(r.Paths.Backups(), keep); err != nil {
		return "", "", nil, err
	}
	errs = []string{}
	// 옛 작업 기록 DB(work.db)도 같은 날 함께 복사한다(읽기 전용으로 연다).
	workPath, werr := sqlitedb.BackupDaily(r.Paths.WorkDB(), r.Paths.Backups(), "work", keep, r.now())
	if werr != nil {
		errs = append(errs, i18n.Errorf("retro.err.backup", werr).Error())
	}
	return memoryPath, workPath, errs, nil
}

// Judge judges the changes whose observation window ended, then reverts the
// automatic changes judged harmful (A2). The weekly retrospective reads the verdicts.
func (r *Keeper) Judge() []string {
	errs := []string{}
	if _, err := r.Store.JudgeLedger(max(r.Cfg.LedgerWindowDays, 1)); err != nil {
		errs = append(errs, i18n.Errorf("retro.err.effect", err).Error())
	}
	if _, err := r.Store.RevertHarmfulAuto(); err != nil {
		errs = append(errs, err.Error())
	}
	return errs
}

// Settle confirms the candidates whose evidence came in, closes the settled
// proposals and prunes the old session state, after a catch-up.
func (r *Keeper) Settle() []string {
	errs := []string{}
	if _, err := r.Store.ConfirmCandidates(); err != nil {
		errs = append(errs, err.Error())
	}
	if _, err := r.Store.CloseSettled(); err != nil {
		errs = append(errs, err.Error())
	}
	if err := r.Store.PruneSessionState(SessionStateDays); err != nil {
		errs = append(errs, err.Error())
	}
	return errs
}
