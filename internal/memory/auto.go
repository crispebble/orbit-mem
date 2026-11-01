package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// 자동 변경(A2 「자동화가 고칠 수 있다」): 자동화의 확정·합치기·낡음·대체·연결·retag는 승인 없이
// 적용하고, 제안 표에 상태 auto로, 효과 원장에 한 줄로 남겨 되돌릴 수 있게 한다.

// StatusAuto is a change an automation applied without approval (kept for the audit list).
const StatusAuto = "auto"

// AutoKinds are the changes an automation applies without approval.
var AutoKinds = []string{ProposalConfirm, ProposalMerge, ProposalStale, ProposalSupersede, ProposalLink, ProposalRetag}

// EventAutoReverted is an automatic change the ledger judged harmful and the store undid.
const EventAutoReverted = "audit.auto_reverted"

// AutoInput is one automatic change.
type AutoInput struct {
	Kind      string
	Title     string
	Body      string
	MemoryIDs []string
	Payload   Payload
	RetroID   string
	Who       Who    // 비우면 자동화
	Evidence  string // 확정의 증거(EvidenceSeenDays…). 확정은 증거가 있어야 한다
}

// AutoApply applies one automatic change: it checks the rules (what the user
// said stays, confirmation needs evidence), keeps a proposal row with status
// auto, applies it and writes the ledger line. The result's LedgerID undoes it.
func (s *Store) AutoApply(in AutoInput) (DecideResult, error) {
	if in.Who.Actor == "" {
		in.Who.Actor = ActorAutomation
	}
	if !slices.Contains(AutoKinds, in.Kind) {
		return DecideResult{}, i18n.Errorf("memory.err.auto_kind", ErrInvalid, in.Kind, strings.Join(AutoKinds, "|"))
	}
	explicit := len(in.MemoryIDs) > 0
	pi := ProposalInput{Kind: in.Kind, Title: in.Title, Body: in.Body, MemoryIDs: in.MemoryIDs, Payload: in.Payload, RetroID: in.RetroID}
	if err := s.prepare(&pi); err != nil {
		return DecideResult{}, err
	}
	if in.Kind == ProposalRetag && !explicit {
		// 태그 묶기는 뺄 태그를 가진 기억 모두를 고른다. 사용자 인용 기억은 사용자 말 없이 건너뛴다.
		if in.Who.quote() == "" && !in.Who.human() {
			var keep []string
			for _, id := range pi.MemoryIDs {
				if p, err := s.Protected(id); err != nil {
					return DecideResult{}, err
				} else if !p {
					keep = append(keep, id)
				}
			}
			if len(keep) == 0 {
				return DecideResult{}, fmt.Errorf("%w: %s", ErrAutoSkip, in.Title)
			}
			pi.MemoryIDs = keep
		}
	}
	// 사람이 거절한 제안이나 되돌린 자동 변경은 자동화가 다시 하지 않는다.
	if err := s.notAgain(pi); err != nil {
		return DecideResult{}, err
	}
	targets := pi.MemoryIDs
	if in.Kind == ProposalSupersede {
		targets = pi.MemoryIDs[:1] // 대신하는 기억은 바뀌지 않는다
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DecideResult{}, err
	}
	err = guardTx(tx, in.Who, s.nowMs(), targets...)
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	if err != nil {
		return DecideResult{}, err
	}
	ev, err := s.autoReady(in, pi)
	if err != nil {
		return DecideResult{}, err
	}
	if ev != "" {
		in.Evidence = ev
	}
	pi.Payload.Actor, pi.Payload.Evidence = in.Who.actor(), in.Evidence
	p, err := s.insertProposal(pi, StatusAuto)
	if err != nil {
		return DecideResult{}, err
	}
	res, err := s.applyRecorded(&p, DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		return res, err
	}
	_ = s.RecordEvent(Event{Kind: EventProposalDecided}, map[string]any{"proposalId": p.ID, "proposalKind": p.Kind, "decision": "auto", "evidence": in.Evidence})
	return res, nil
}

// notAgain stops an automatic change the person already turned down: the
// same proposal was rejected, or the same automatic change was reverted.
func (s *Store) notAgain(pi ProposalInput) error {
	fp := fingerprint(pi)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM proposals WHERE fingerprint=? AND status='rejected'`, fp).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w (rejected)", ErrDuplicateProposal)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM proposals p JOIN ledger l ON l.proposal_id=p.id WHERE p.fingerprint=? AND p.status=? AND l.reverted_by<>''`,
		fp, StatusAuto).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %s", ErrAutoSkip, "reverted before")
	}
	return nil
}

// autoReady checks that an automatic change still has something to do, and
// for a confirmation returns its evidence.
func (s *Store) autoReady(in AutoInput, pi ProposalInput) (string, error) {
	evidence := ""
	switch in.Kind {
	case ProposalConfirm:
		for _, id := range pi.MemoryIDs {
			m, err := getTx(s.db, id)
			if err != nil {
				return "", err
			}
			if m.State != StateCandidate {
				return "", fmt.Errorf("%w: %s", ErrAutoSkip, id)
			}
			ev, err := s.EvidenceFor(id)
			if err != nil {
				return "", err
			}
			if ev == "" {
				return "", fmt.Errorf("%w: %s", ErrNoEvidence, id)
			}
			if why, err := s.confirmBlocked(id); err != nil {
				return "", err
			} else if why != "" {
				return "", fmt.Errorf("%w: %s (%s)", ErrAutoSkip, id, why)
			}
			if evidence == "" {
				evidence = ev
			}
		}
	case ProposalStale, ProposalSupersede:
		ids := pi.MemoryIDs
		if in.Kind == ProposalSupersede {
			ids = ids[:1]
		}
		for _, id := range ids {
			m, err := getTx(s.db, id)
			if err != nil {
				return "", err
			}
			if m.State != StateCandidate && m.State != StateConfirmed {
				return "", fmt.Errorf("%w: %s", ErrAutoSkip, id)
			}
		}
	}
	return evidence, nil
}

// applyRecorded applies a stored proposal and writes its ledger line. A change
// another way already made closes the proposal as settled.
func (s *Store) applyRecorded(p *Proposal, in DecideInput) (DecideResult, error) {
	res := DecideResult{}
	before, err := s.snapshotFor(*p)
	if err != nil {
		return res, err
	}
	if err := s.apply(*p, in, &res); errors.Is(err, errAlreadyLinked) || errors.Is(err, errAlreadyRetagged) || errors.Is(err, errMergeGone) {
		_ = s.finish(p, StatusSettled, err.Error())
		res.Proposal = *p
		return res, fmt.Errorf("%w: %v", ErrAutoSkip, err)
	} else if err != nil {
		_ = s.finish(p, StatusSettled, "적용 실패: "+err.Error())
		return res, err
	}
	if p.Kind == ProposalRetag {
		before.Memories = slices.DeleteFunc(before.Memories, func(m Memory) bool { return !slices.Contains(res.Changed, m.ID) })
	}
	if res.LedgerID, err = s.recordLedger(*p, before, res); err != nil {
		return res, i18n.Errorf("memory.err.ledger_write", err)
	}
	res.Proposal = *p
	return res, nil
}

// insertProposal stores a prepared proposal with a status (decided at once unless pending or lab).
func (s *Store) insertProposal(in ProposalInput, status string) (Proposal, error) {
	now := s.nowMs()
	p := Proposal{ID: NewID("p-"), Kind: in.Kind, Title: in.Title, Body: in.Body, MemoryIDs: in.MemoryIDs, Payload: in.Payload,
		Status: status, CreatedAt: now, RetroID: in.RetroID}
	if status != StatusPending && status != StatusLab {
		p.DecidedAt = now
	}
	ids, _ := json.Marshal(nonNil(p.MemoryIDs))
	payload, _ := json.Marshal(p.Payload)
	_, err := s.db.Exec(`INSERT INTO proposals(id,kind,title,body,memory_ids,payload,fingerprint,status,created_at,decided_at,retro_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Kind, p.Title, p.Body, string(ids), string(payload), fingerprint(in), p.Status, p.CreatedAt, p.DecidedAt, p.RetroID)
	return p, err
}

// ledgerFor finds a ledger line by its id or by the proposal it came from.
func (s *Store) ledgerFor(ref string) (LedgerEntry, error) {
	if e, err := s.LedgerEntry(ref); err == nil {
		return e, nil
	}
	var id string
	if err := s.db.QueryRow(`SELECT id FROM ledger WHERE proposal_id=? ORDER BY at DESC LIMIT 1`, ref).Scan(&id); err != nil {
		return LedgerEntry{}, i18n.Errorf("memory.err.not_in_ledger", ErrInvalid, ref)
	}
	return s.LedgerEntry(id)
}

// isAuto says whether a ledger line was an automatic change.
func (s *Store) isAuto(e LedgerEntry) bool {
	var status string
	_ = s.db.QueryRow(`SELECT status FROM proposals WHERE id=?`, e.ProposalID).Scan(&status)
	return status == StatusAuto
}

// RevertChange undoes one ledger line (by its id or its proposal's id): the
// memories go back to the version before it (an automatic confirmation goes
// back to candidate). The undo is itself a ledger line. An automation may only
// undo automatic changes.
func (s *Store) RevertChange(ref string, w Who, reason string) (DecideResult, error) {
	e, err := s.ledgerFor(ref)
	if err != nil {
		return DecideResult{}, err
	}
	if !w.human() && !s.isAuto(e) {
		return DecideResult{}, fmt.Errorf("%w: %s", ErrProtected, e.ID)
	}
	title := "되돌림: " + e.ProposalKind + " " + e.ID
	if p, err := s.GetProposal(e.ProposalID); err == nil {
		title = "되돌림: " + p.Title
	}
	pi := ProposalInput{Kind: ProposalRevert, Title: title, Body: strings.TrimSpace(reason), Payload: Payload{LedgerID: e.ID, Actor: w.actor()}}
	if err := s.prepare(&pi); err != nil {
		return DecideResult{}, err
	}
	var ids []string
	for _, m := range e.Before.Memories {
		ids = append(ids, m.ID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DecideResult{}, err
	}
	err = guardTx(tx, w, s.nowMs(), ids...)
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	if err != nil {
		return DecideResult{}, err
	}
	status := StatusApproved
	if !w.human() {
		status = StatusAuto
	}
	p, err := s.insertProposal(pi, status)
	if err != nil {
		return DecideResult{}, err
	}
	p.Note = strings.TrimSpace(reason)
	_, _ = s.db.Exec(`UPDATE proposals SET note=? WHERE id=?`, p.Note, p.ID)
	res, err := s.applyRecorded(&p, DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		return res, err
	}
	_ = s.RecordEvent(Event{Kind: EventProposalDecided}, map[string]any{"proposalId": p.ID, "proposalKind": p.Kind, "decision": "revert", "actor": w.actor()})
	return res, nil
}

// AutoRevert is one harmful automatic change the store undid (or could not).
type AutoRevert struct {
	LedgerID string `json:"ledgerId"`
	RevertID string `json:"revertId,omitempty"`
	Title    string `json:"title,omitempty"`
	Error    string `json:"error,omitempty"`
}

// RevertHarmfulAuto undoes every automatic change the ledger judged harmful
// and not undone yet, and leaves an event for each so the person hears of it.
// A change that cannot be undone (the memory changed again) is reported once.
func (s *Store) RevertHarmfulAuto() ([]AutoRevert, error) {
	rows, err := s.db.Query(`SELECT l.id, p.title FROM ledger l JOIN proposals p ON p.id=l.proposal_id
		WHERE l.verdict=? AND l.reverted_by='' AND p.status=? AND l.revert_of='' ORDER BY l.at`, VerdictHarmful, StatusAuto)
	if err != nil {
		return nil, err
	}
	type line struct{ id, title string }
	var todo []line
	for rows.Next() {
		var l line
		if rows.Scan(&l.id, &l.title) == nil {
			todo = append(todo, l)
		}
	}
	rows.Close()
	var out []AutoRevert
	for _, l := range todo {
		if s.autoRevertTried(l.id) {
			continue
		}
		r := AutoRevert{LedgerID: l.id, Title: l.title}
		res, err := s.RevertChange(l.id, Who{Actor: ActorAutomation}, "효과 원장 판정 해로움: 자동 되돌림")
		if err != nil {
			r.Error = err.Error()
		} else {
			r.RevertID = res.LedgerID
		}
		_ = s.RecordEvent(Event{Kind: EventAutoReverted}, map[string]any{"ledgerId": r.LedgerID, "revertId": r.RevertID, "title": ClipRunes(r.Title, 200), "error": r.Error})
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) autoRevertTried(ledgerID string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND data LIKE ?`, EventAutoReverted, `%"ledgerId":"`+ledgerID+`"%`).Scan(&n)
	return n > 0
}

// ChangeOutcome says what happened to a change an automation submitted.
type ChangeOutcome struct {
	Applied  bool     `json:"applied"`
	LedgerID string   `json:"ledgerId,omitempty"`
	Proposal Proposal `json:"proposal,omitzero"`
	Reason   string   `json:"reason,omitempty"` // 바로 적용하지 않고 대기 제안으로 둔 까닭
	Changed  []string `json:"changed,omitempty"`
}

// SubmitChange is how an automation hands in a change: the kinds an
// automation may apply are applied at once and kept in the ledger; a change to
// what the user said (without the user's words), a confirmation without
// evidence and every other kind wait for the person as a pending proposal.
func (s *Store) SubmitChange(in ProposalInput, w Who) (ChangeOutcome, error) {
	if w.Actor == "" {
		w.Actor = ActorAutomation
	}
	if slices.Contains(AutoKinds, in.Kind) && !in.Lab {
		res, err := s.AutoApply(AutoInput{Kind: in.Kind, Title: in.Title, Body: in.Body, MemoryIDs: in.MemoryIDs, Payload: in.Payload, RetroID: in.RetroID, Who: w})
		switch {
		case err == nil:
			return ChangeOutcome{Applied: true, LedgerID: res.LedgerID, Proposal: res.Proposal, Changed: res.Changed}, nil
		case errors.Is(err, ErrAutoSkip), errors.Is(err, ErrDuplicateProposal):
			return ChangeOutcome{Reason: err.Error()}, err
		case errors.Is(err, ErrProtected), errors.Is(err, ErrNoEvidence):
			p, perr := s.Propose(in)
			if perr != nil {
				return ChangeOutcome{Reason: err.Error()}, perr
			}
			return ChangeOutcome{Proposal: p, Reason: err.Error()}, nil
		default:
			return ChangeOutcome{}, err
		}
	}
	p, err := s.Propose(in)
	return ChangeOutcome{Proposal: p}, err
}
