package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
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
