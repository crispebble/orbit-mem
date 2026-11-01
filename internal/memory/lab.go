package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// The harness lab's records (명세 · Orbit 3차 O3-5 「기록」). The answers and
// changes each run made stay in the lab folder; the database keeps what the
// metrics and the ledger need.
const labSchema = `
CREATE TABLE IF NOT EXISTS lab_experiments (
  id TEXT PRIMARY KEY, task_id TEXT NOT NULL, status TEXT NOT NULL, verdict TEXT NOT NULL DEFAULT '',
  runs INTEGER NOT NULL, files TEXT NOT NULL DEFAULT '[]', hashes TEXT NOT NULL DEFAULT '{}', picks TEXT NOT NULL DEFAULT '[]',
  cost_usd REAL NOT NULL DEFAULT 0, ledger_id TEXT NOT NULL DEFAULT '', proposal_ids TEXT NOT NULL DEFAULT '[]',
  note TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS lab_runs (
  experiment TEXT NOT NULL, candidate TEXT NOT NULL, round INTEGER NOT NULL, cost_usd REAL NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, ms INTEGER NOT NULL DEFAULT 0,
  ok INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', PRIMARY KEY(experiment, candidate, round));
`

// Experiment statuses.
const (
	LabRunning = "running"
	LabDone    = "done"
	LabFailed  = "failed"
)

// LedgerKindLab marks a ledger line written by the harness lab, not by a proposal.
const LedgerKindLab = "lab"

// LabPick is the judge's choice in one round, with the blind label mapped back.
type LabPick struct {
	Round  int    `json:"round"`
	A      string `json:"a"`      // A로 보인 후보(current|changed)
	Pick   string `json:"pick"`   // A|B|same
	Winner string `json:"winner"` // current|changed|same
	Reason string `json:"reason,omitempty"`
}

// LabExperiment is one comparison of the current harness and a changed one on a task.
type LabExperiment struct {
	ID          string            `json:"id"`
	TaskID      string            `json:"taskId"`
	Status      string            `json:"status"`
	Verdict     string            `json:"verdict,omitempty"`
	Runs        int               `json:"runs"`
	Files       []string          `json:"files"`  // 바꾼 원본 파일(원본 기준 상대 경로)
	Hashes      map[string]string `json:"hashes"` // 후보별 빌드한 플러그인 해시
	Picks       []LabPick         `json:"picks"`
	CostUSD     float64           `json:"costUsd"`
	LedgerID    string            `json:"ledgerId,omitempty"`
	ProposalIDs []string          `json:"proposalIds,omitempty"`
	Note        string            `json:"note,omitempty"`
	CreatedAt   int64             `json:"createdAt"`
	FinishedAt  int64             `json:"finishedAt,omitempty"`
}

// LabRun is one run of one candidate.
type LabRun struct {
	Experiment   string  `json:"experiment"`
	Candidate    string  `json:"candidate"` // current|changed|judge
	Round        int     `json:"round"`
	CostUSD      float64 `json:"costUsd"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	Ms           int64   `json:"ms"`
	OK           bool    `json:"ok"`
	Error        string  `json:"error,omitempty"`
}

// SaveLabExperiment writes an experiment, replacing what was there.
func (s *Store) SaveLabExperiment(e LabExperiment) error {
	files, _ := json.Marshal(nonNil(e.Files))
	if e.Hashes == nil {
		e.Hashes = map[string]string{}
	}
	hashes, _ := json.Marshal(e.Hashes)
	if e.Picks == nil {
		e.Picks = []LabPick{}
	}
	picks, _ := json.Marshal(e.Picks)
	pids, _ := json.Marshal(nonNil(e.ProposalIDs))
	_, err := s.db.Exec(`INSERT INTO lab_experiments(id,task_id,status,verdict,runs,files,hashes,picks,cost_usd,ledger_id,proposal_ids,note,created_at,finished_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status, verdict=excluded.verdict, files=excluded.files,
		hashes=excluded.hashes, picks=excluded.picks, cost_usd=excluded.cost_usd, ledger_id=excluded.ledger_id, proposal_ids=excluded.proposal_ids,
		note=excluded.note, finished_at=excluded.finished_at`,
		e.ID, e.TaskID, e.Status, e.Verdict, e.Runs, string(files), string(hashes), string(picks), e.CostUSD, e.LedgerID, string(pids), e.Note, e.CreatedAt, e.FinishedAt)
	return err
}

const labCols = `id,task_id,status,verdict,runs,files,hashes,picks,cost_usd,ledger_id,proposal_ids,note,created_at,finished_at`

func scanLab(row interface{ Scan(...any) error }) (LabExperiment, error) {
	var e LabExperiment
	var files, hashes, picks, pids string
	if err := row.Scan(&e.ID, &e.TaskID, &e.Status, &e.Verdict, &e.Runs, &files, &hashes, &picks, &e.CostUSD, &e.LedgerID, &pids, &e.Note, &e.CreatedAt, &e.FinishedAt); err != nil {
		return e, err
	}
	_ = json.Unmarshal([]byte(files), &e.Files)
	_ = json.Unmarshal([]byte(hashes), &e.Hashes)
	_ = json.Unmarshal([]byte(picks), &e.Picks)
	_ = json.Unmarshal([]byte(pids), &e.ProposalIDs)
	return e, nil
}

// LabExperiment reads one experiment.
func (s *Store) LabExperiment(id string) (LabExperiment, error) {
	e, err := scanLab(s.db.QueryRow(`SELECT `+labCols+` FROM lab_experiments WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, i18n.Errorf("memory.err.no_experiment", id)
	}
	return e, err
}

// LabExperiments lists experiments started in [fromMs, toMs), newest first. toMs 0 means no end.
func (s *Store) LabExperiments(fromMs, toMs int64) ([]LabExperiment, error) {
	if toMs == 0 {
		toMs = 1 << 62
	}
	rows, err := s.db.Query(`SELECT `+labCols+` FROM lab_experiments WHERE created_at>=? AND created_at<? ORDER BY created_at DESC`, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LabExperiment
	for rows.Next() {
		e, err := scanLab(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddLabRun records one run.
func (s *Store) AddLabRun(r LabRun) error {
	ok := 0
	if r.OK {
		ok = 1
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO lab_runs(experiment,candidate,round,cost_usd,input_tokens,output_tokens,ms,ok,error) VALUES(?,?,?,?,?,?,?,?,?)`,
		r.Experiment, r.Candidate, r.Round, r.CostUSD, r.InputTokens, r.OutputTokens, r.Ms, ok, ClipRunes(r.Error, 500))
	return err
}

// LabRuns lists an experiment's runs in order.
func (s *Store) LabRuns(experiment string) ([]LabRun, error) {
	rows, err := s.db.Query(`SELECT experiment,candidate,round,cost_usd,input_tokens,output_tokens,ms,ok,error FROM lab_runs WHERE experiment=? ORDER BY round, candidate`, experiment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LabRun
	for rows.Next() {
		var r LabRun
		var ok int
		if err := rows.Scan(&r.Experiment, &r.Candidate, &r.Round, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.Ms, &ok, &r.Error); err != nil {
			return nil, err
		}
		r.OK = ok == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// LabLedgerInput is the ledger line of a judged experiment.
type LabLedgerInput struct {
	ExperimentID string
	Targets      []string // 바꾼 원본 파일 절대경로
	Change       string   // diff
	Basis        string   // 과제와 회차별 고름 요약
	Verdict      string   // helped|harmful|no_effect
}

// RecordLabLedger writes the experiment's verdict as a ledger line (설계 「기록」:
// 결과를 변경 효과 원장에 실험 판정으로 남긴다). It is judged already, so the
// observation-window judgment leaves it alone, and it has nothing to undo: the
// lab changes no file.
func (s *Store) RecordLabLedger(in LabLedgerInput) (string, error) {
	if in.Verdict != VerdictHelped && in.Verdict != VerdictHarmful && in.Verdict != VerdictNoEffect {
		return "", i18n.Errorf("memory.err.experiment_verdict", ErrInvalid)
	}
	now := s.nowMs()
	id := NewID("l-")
	targets, _ := json.Marshal(nonNil(in.Targets))
	basis := strings.TrimSpace(in.Basis + "\n실험 " + in.ExperimentID)
	_, err := s.db.Exec(`INSERT INTO ledger(`+ledgerCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, now, Day(time.UnixMilli(now)), in.ExperimentID, LedgerKindLab, TargetHarness, string(targets), "[]", in.Change, basis,
		"{}", in.Verdict, 1, "{}", now, "", "", "")
	return id, err
}

// ReleaseLabProposal moves a proposal waiting for the lab to pending and adds
// the lab's summary to its body: the user decides it now.
func (s *Store) ReleaseLabProposal(id, summary string) error {
	res, err := s.db.Exec(`UPDATE proposals SET status=?, body=TRIM(body || char(10) || char(10) || ?) WHERE id=? AND status=?`, StatusPending, summary, id, StatusLab)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return i18n.Errorf("memory.err.not_lab_pending", ErrInvalid, id)
	}
	return nil
}

// CloseLabProposal rejects a proposal the lab did not find helpful, with the reason.
func (s *Store) CloseLabProposal(id, note string) error {
	res, err := s.db.Exec(`UPDATE proposals SET status=?, decided_at=?, note=? WHERE id=? AND status=?`, StatusRejected, s.nowMs(), note, id, StatusLab)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return i18n.Errorf("memory.err.not_lab_pending", ErrInvalid, id)
	}
	return nil
}

// LabCorrection is a correction event's id and the user's words: the harness lab
// (OpenOrbit's dev tool, ONID-124) adds them to a task's criteria.
type LabCorrection struct {
	ID    int64  `json:"id"`
	Quote string `json:"quote"`
}

// LabCorrections reads the correction events with these ids, in the given order.
func (s *Store) LabCorrections(ids []int64) ([]LabCorrection, error) {
	out := []LabCorrection{}
	for _, id := range ids {
		var data string
		if err := s.db.QueryRow(`SELECT data FROM events WHERE id=? AND kind=?`, id, EventCorrection).Scan(&data); err != nil {
			return nil, i18n.Errorf("memory.err.no_correction", id)
		}
		var d struct{ Quote string }
		_ = json.Unmarshal([]byte(data), &d)
		out = append(out, LabCorrection{ID: id, Quote: strings.TrimSpace(d.Quote)})
	}
	return out, nil
}

// LabLedger is the ledger line of a judged experiment as the lab sends it; the verdict is the experiment's.
type LabLedger struct {
	Targets []string `json:"targets"`
	Change  string   `json:"change"`
	Basis   string   `json:"basis"`
}

// LabProposalNote releases or closes one proposal waiting for the lab.
type LabProposalNote struct {
	ProposalID string `json:"proposalId"`
	Summary    string `json:"summary,omitempty"`
	Note       string `json:"note,omitempty"`
}

// LabFileProposal is a file change that helped, proposed to the user.
type LabFileProposal struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// LabSave is what the harness lab records at once: the experiment, its runs, and for a
// judged experiment its ledger line and what happens to the proposals.
type LabSave struct {
	Experiment LabExperiment     `json:"experiment"`
	Runs       []LabRun          `json:"runs"`
	Ledger     *LabLedger        `json:"ledger"`
	Release    *LabProposalNote  `json:"release"`
	Close      *LabProposalNote  `json:"close"`
	Propose    []LabFileProposal `json:"propose"`
}

// labID is an id the lab made: a prefix and letters, digits or '-' only (it names a folder).
func labID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// inRoots says whether path (absolute, after following symlinks) is inside one of roots.
func inRoots(roots []string, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		if d, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			return filepath.Join(d, filepath.Base(p))
		}
		return filepath.Clean(p)
	}
	real := resolve(path)
	for _, r := range roots {
		if r == "" {
			continue
		}
		r = resolve(r)
		if strings.HasPrefix(real, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// checkLab refuses a record before anything is written: a bad experiment, a ledger line
// that does not match the status, a proposal action that does not match the verdict, a
// proposal that is not waiting for the lab, or a file outside the plugin source.
func (s *Store) checkLab(in LabSave, sources []string) error {
	x := in.Experiment
	bad := func() error { return i18n.Errorf("memory.err.lab_record", ErrInvalid) }
	if !labID(x.ID) || !labID(x.TaskID) || x.Runs < 1 || x.CreatedAt <= 0 ||
		(x.Status != LabRunning && x.Status != LabDone && x.Status != LabFailed) {
		return bad()
	}
	if old, err := s.LabExperiment(x.ID); err == nil && (old.Status != LabRunning || old.TaskID != x.TaskID) {
		return i18n.Errorf("memory.err.lab_finished", ErrInvalid, x.ID, old.Status)
	}
	actions := 0
	if in.Release != nil {
		actions++
	}
	if in.Close != nil {
		actions++
	}
	if len(in.Propose) > 0 {
		actions++
	}
	if (in.Ledger != nil) != (x.Status == LabDone) || (in.Ledger == nil && actions > 0) || actions > 1 {
		return bad()
	}
	if in.Ledger == nil {
		return nil
	}
	if x.Verdict != VerdictHelped && x.Verdict != VerdictHarmful && x.Verdict != VerdictNoEffect {
		return i18n.Errorf("memory.err.experiment_verdict", ErrInvalid)
	}
	helped := x.Verdict == VerdictHelped
	if (in.Release != nil || len(in.Propose) > 0) && !helped || in.Close != nil && helped {
		return i18n.Errorf("memory.err.lab_action", ErrInvalid, x.Verdict)
	}
	for _, n := range []*LabProposalNote{in.Release, in.Close} {
		if n == nil {
			continue
		}
		p, err := s.GetProposal(n.ProposalID)
		if err != nil || p.Status != StatusLab || p.Kind != ProposalApplyFile {
			return i18n.Errorf("memory.err.not_lab_pending", ErrInvalid, n.ProposalID)
		}
	}
	for _, f := range in.Propose {
		if !inRoots(sources, f.Path) {
			return i18n.Errorf("memory.err.lab_outside_source", ErrInvalid, f.Path)
		}
		if _, dup := s.SameProposal(ProposalInput{Kind: ProposalApplyFile, Title: f.Title, Payload: Payload{Path: f.Path, Content: f.Content}}); dup {
			return fmt.Errorf("%s: %w", f.Path, ErrDuplicateProposal)
		}
	}
	return nil
}

// SaveLab records what the lab sends (ONID-124: the lab is OpenOrbit's dev tool and
// reaches the records only through this). Everything is checked first (checkLab); an
// experiment already done or failed is not written again, so a repeated save makes no
// second ledger line. Then the runs are kept, a done experiment's ledger line is written,
// the waiting proposal is released or closed or the helped files are proposed (only
// inside sources, the plugin source), and the experiment is written last with the ledger
// and proposal ids. It returns the experiment as stored.
func (s *Store) SaveLab(in LabSave, sources []string) (LabExperiment, error) {
	x := in.Experiment
	if err := s.checkLab(in, sources); err != nil {
		return x, err
	}
	for _, r := range in.Runs {
		r.Experiment = x.ID
		if err := s.AddLabRun(r); err != nil {
			return x, err
		}
	}
	var errs []error
	if in.Ledger != nil {
		id, err := s.RecordLabLedger(LabLedgerInput{ExperimentID: x.ID, Targets: in.Ledger.Targets, Change: in.Ledger.Change,
			Basis: in.Ledger.Basis, Verdict: x.Verdict})
		if err != nil {
			return x, err
		}
		x.LedgerID = id
		switch {
		case in.Release != nil:
			errs = append(errs, s.ReleaseLabProposal(in.Release.ProposalID, in.Release.Summary))
			x.ProposalIDs = []string{in.Release.ProposalID}
		case in.Close != nil:
			errs = append(errs, s.CloseLabProposal(in.Close.ProposalID, in.Close.Note))
			x.ProposalIDs = []string{in.Close.ProposalID}
		default:
			for _, f := range in.Propose {
				p, err := s.Propose(ProposalInput{Kind: ProposalApplyFile, Title: f.Title, Body: f.Body, Payload: Payload{Path: f.Path, Content: f.Content}})
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", f.Path, err))
					continue
				}
				x.ProposalIDs = append(x.ProposalIDs, p.ID)
			}
		}
	}
	errs = append(errs, s.SaveLabExperiment(x))
	return x, errors.Join(errs...)
}
