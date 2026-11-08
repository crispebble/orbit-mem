package memory

import (
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// Write verdicts a retrospective gives each candidate memory (설계 「쓰기 판정 기록」).
const (
	VerdictKeep   = "keep"   // 유지: 후보로 남기고 확정을 제안한다
	VerdictHold   = "hold"   // 보류: 후보로만 남긴다
	VerdictReject = "reject" // 거절: 기억을 만들지 않는다
)

var WriteVerdicts = []string{VerdictKeep, VerdictHold, VerdictReject}

// Judgment is one write decision about a candidate memory, with the user's
// answer to the proposal it led to.
type Judgment struct {
	ID           string `json:"id"`
	At           int64  `json:"at"`
	RetroID      string `json:"retroId,omitempty"`
	Session      string `json:"session,omitempty"`
	MemoryID     string `json:"memoryId,omitempty"`
	Kind         string `json:"kind,omitempty"` // 기억 종류, 또는 판정 없이 거절된 제안의 종류
	Title        string `json:"title"`
	Excerpt      string `json:"excerpt,omitempty"`
	Verdict      string `json:"verdict,omitempty"` // 회고 판정. 판정 없이 결정된 제안이면 비어 있다
	Reason       string `json:"reason,omitempty"`
	ProposalID   string `json:"proposalId,omitempty"`
	UserDecision string `json:"userDecision,omitempty"` // reject | edit
	UserNote     string `json:"userNote,omitempty"`
	DecidedAt    int64  `json:"decidedAt,omitempty"`
}

const judgmentCols = `id,at,retro_id,session,memory_id,kind,title,excerpt,verdict,reason,proposal_id,user_decision,user_note,decided_at`

func scanJudgment(row interface{ Scan(...any) error }) (Judgment, error) {
	var j Judgment
	err := row.Scan(&j.ID, &j.At, &j.RetroID, &j.Session, &j.MemoryID, &j.Kind, &j.Title, &j.Excerpt, &j.Verdict, &j.Reason,
		&j.ProposalID, &j.UserDecision, &j.UserNote, &j.DecidedAt)
	return j, err
}

// RecordJudgment keeps a retrospective's write verdict. A rejection needs a
// reason: it is what keeps the same wrong candidate from coming back.
// No orbit-mem path writes it since the retrospectives were removed (2026-11-08, ORBIT-13);
// orbit-mem stats still counts the old verdicts.
func (s *Store) RecordJudgment(j Judgment) (Judgment, error) {
	j.Reason = strings.TrimSpace(j.Reason)
	if !contains(WriteVerdicts, j.Verdict) || strings.TrimSpace(j.Title) == "" {
		return j, i18n.Errorf("memory.err.verdict_title", ErrInvalid, strings.Join(WriteVerdicts, "·"))
	}
	if j.Verdict == VerdictReject && j.Reason == "" {
		return j, i18n.Errorf("memory.err.reject_reason", ErrInvalid)
	}
	return j, s.insertJudgment(&j)
}

func (s *Store) insertJudgment(j *Judgment) error {
	j.ID = NewID("j-")
	if j.At == 0 {
		j.At = s.nowMs()
	}
	j.Title = ClipRunes(j.Title, 200)
	j.Excerpt = ClipRunes(MaskSecrets(j.Excerpt), 300)
	j.Reason = ClipRunes(MaskSecrets(j.Reason), 300)
	_, err := s.db.Exec(`INSERT INTO judgments(`+judgmentCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.At, j.RetroID, j.Session, j.MemoryID, j.Kind, j.Title, j.Excerpt, j.Verdict, j.Reason,
		j.ProposalID, j.UserDecision, j.UserNote, j.DecidedAt)
	return err
}

// JudgmentCounts counts the verdicts and user decisions recorded between two times (ms).
func (s *Store) JudgmentCounts(fromMs, toMs int64) (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT verdict, COUNT(*) FROM judgments WHERE verdict<>'' AND at>=? AND at<? GROUP BY verdict`, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			out[k] = n
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT user_decision, COUNT(*) FROM judgments WHERE user_decision<>'' AND decided_at>=? AND decided_at<? GROUP BY user_decision`, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			out["user_"+k] = n
		}
	}
	return out, rows.Err()
}

func (s *Store) judgments(where string, args ...any) ([]Judgment, error) {
	rows, err := s.db.Query(`SELECT `+judgmentCols+` FROM judgments `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Judgment
	for rows.Next() {
		j, err := scanJudgment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// attachDecision puts the user's rejection or edit of a proposal on the same
// record: a confirm proposal on its memory's latest judgment, any other
// proposal on a line of its own.
func (s *Store) attachDecision(p Proposal, decision, note string) error {
	now := s.nowMs()
	if p.Kind == ProposalConfirm {
		attached := false
		for _, id := range p.MemoryIDs {
			res, err := s.db.Exec(`UPDATE judgments SET proposal_id=?, user_decision=?, user_note=?, decided_at=?
				WHERE id=(SELECT id FROM judgments WHERE memory_id=? ORDER BY at DESC, rowid DESC LIMIT 1)`, p.ID, decision, note, now, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				attached = true
			}
		}
		if attached {
			return nil
		}
	}
	j := Judgment{Kind: p.Kind, Title: p.Title, Excerpt: p.Body, ProposalID: p.ID, RetroID: p.RetroID,
		UserDecision: decision, UserNote: note, DecidedAt: now, At: now}
	if len(p.MemoryIDs) > 0 {
		j.MemoryID = p.MemoryIDs[0]
	}
	return s.insertJudgment(&j)
}
