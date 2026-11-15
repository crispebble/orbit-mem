package memory

// 지운 기억의 흔적을 효과 원장·제안·자동 되돌림 사건에서 비운다(명세 5.3 「글만 비움」). 줄과 id는 남긴다: 감사 목록과
// 자동 변경 되돌리기가 ledger JOIN proposals로 읽고, 여러 기억에 걸친 변경은 남은 기억의 감사 항목이어야 하기 때문이다.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// forgetDropNote is the note of a pending proposal Forget closed.
const forgetDropNote = "지운 기억이 든 제안이라 닫았다"

// revertableErr says why a ledger line cannot be undone: the memories Forget removed from it
// (LedgerBefore.Forgotten) leave nothing to put back, and a half undo would bring a memory back to a
// state that points at the forgotten one. nil when the line lost nothing.
func revertableErr(b LedgerBefore) error {
	if len(b.Forgotten) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrLedgerForgotten, strings.Join(b.Forgotten, ", "))
}

// auditLine is a ledger line scrubAuditTx has to blank, with the ids of ids it is about.
type auditLine struct {
	id, proposalID, before string
	revertOf, revertedBy   string
	hit                    []string // 지운 id 가운데 이 줄이 가리키는 것
	direct                 bool     // targets·observe·before 글에 id가 있는 줄(되돌림으로 이어진 줄은 false)
}

// scrubAuditTx blanks what the ledger and the proposals keep of the memories ids, inside tx. It runs before
// the rows of the memories are deleted.
//
// A ledger line is a target when its targets, observe or before text holds an id (a plain text search, so a
// line that only carries the id inside a snapshot is found too), and so is every line linked to it by
// revert_of or reverted_by. A target gets change and basis replaced by forgetLedgerChange; its before loses
// the snapshots of the forgotten memories and the snapshots that point at one (a superseded_by), and
// LedgerBefore.Forgotten names the ids it lost, so revertableErr refuses to undo it. A line that held the
// id only in observe loses text but not the way back.
//
// A proposal is a target when its memory_ids hold an id or a target line belongs to it: its title becomes
// forgetProposalTitle, body and payload are emptied and memory_ids stay. A pending one is closed
// (StatusDropped). The title of an audit.auto_reverted event of a target line goes too; its ledgerId stays.
func scrubAuditTx(tx *sql.Tx, ids []string) error {
	lines, err := auditLinesTx(tx, ids)
	if err != nil {
		return err
	}
	proposals := map[string][]string{} // 제안 id → 그 제안이 가리키는 지운 id
	for _, l := range lines {
		if l.proposalID != "" {
			proposals[l.proposalID] = unionIDs(proposals[l.proposalID], l.hit)
		}
		if err := scrubLedgerLineTx(tx, l, ids); err != nil {
			return err
		}
	}
	if err := proposalsHoldingTx(tx, ids, proposals); err != nil {
		return err
	}
	pids := make([]string, 0, len(proposals))
	for pid := range proposals {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	for _, pid := range pids {
		title := fmt.Sprintf(forgetProposalTitle, strings.Join(proposals[pid], ", "))
		if _, err := tx.Exec(`UPDATE proposals SET title=?, body='', payload='{}' WHERE id=?`, title, pid); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE proposals SET status=?, note=? WHERE id=? AND status=?`, StatusDropped, forgetDropNote, pid, StatusPending); err != nil {
			return err
		}
	}
	for _, l := range lines {
		if err := scrubAutoRevertEventsTx(tx, l); err != nil {
			return err
		}
	}
	return nil
}

// auditLinesTx finds the target ledger lines of ids: the ones that hold an id and the ones linked to them.
func auditLinesTx(tx *sql.Tx, ids []string) ([]auditLine, error) {
	rows, err := tx.Query(`SELECT id, proposal_id, targets, observe, before, revert_of, reverted_by FROM ledger`)
	if err != nil {
		return nil, err
	}
	var all []auditLine
	for rows.Next() {
		var l auditLine
		var targets, observe string
		if err := rows.Scan(&l.id, &l.proposalID, &targets, &observe, &l.before, &l.revertOf, &l.revertedBy); err != nil {
			rows.Close()
			return nil, err
		}
		for _, id := range ids {
			if strings.Contains(targets, id) || strings.Contains(observe, id) || strings.Contains(l.before, id) {
				l.hit = append(l.hit, id)
			}
		}
		l.direct = len(l.hit) > 0
		all = append(all, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := map[string]*auditLine{}
	for i := range all {
		byID[all[i].id] = &all[i]
	}
	inherit := map[string][]string{} // 이어진 줄이 물려받는 id. 직접 걸린 줄의 hit은 그대로 둔다
	for _, l := range all {
		if !l.direct {
			continue
		}
		for _, link := range []string{l.revertOf, l.revertedBy} {
			if p := byID[link]; link != "" && p != nil && !p.direct {
				inherit[link] = unionIDs(inherit[link], l.hit)
			}
		}
	}
	var out []auditLine
	for _, l := range all {
		if !l.direct {
			l.hit = inherit[l.id]
		}
		if len(l.hit) > 0 {
			out = append(out, l)
		}
	}
	return out, nil
}

// scrubLedgerLineTx blanks one target line.
func scrubLedgerLineTx(tx *sql.Tx, l auditLine, ids []string) error {
	text := fmt.Sprintf(forgetLedgerChange, strings.Join(l.hit, ", "))
	if !l.direct {
		_, err := tx.Exec(`UPDATE ledger SET change=?, basis=? WHERE id=?`, text, text, l.id)
		return err
	}
	var b LedgerBefore
	if err := json.Unmarshal([]byte(l.before), &b); err != nil {
		return fmt.Errorf("ledger %s: before: %w", l.id, err)
	}
	if !scrubBefore(&b, ids) {
		_, err := tx.Exec(`UPDATE ledger SET change=?, basis=? WHERE id=?`, text, text, l.id)
		return err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE ledger SET change=?, basis=?, before=? WHERE id=?`, text, text, string(raw), l.id)
	return err
}

// scrubBefore drops from b the snapshots that are one of ids or point at one (a superseded_by, a link) and
// adds the ids it lost to b.Forgotten. It says whether b changed. Created and the ids in targets stay.
func scrubBefore(b *LedgerBefore, ids []string) bool {
	changed := false
	var lost []string
	kept := b.Memories[:0:0]
	for _, m := range b.Memories {
		raw, _ := json.Marshal(m)
		var refs []string
		for _, id := range ids {
			if m.ID == id || strings.Contains(string(raw), id) {
				refs = append(refs, id)
			}
		}
		if len(refs) == 0 {
			kept = append(kept, m)
			continue
		}
		changed = true
		lost = append(lost, refs...)
	}
	b.Memories = kept
	for _, id := range ids {
		if slices.Contains(b.Created, id) || strings.Contains(b.Activity, id) || strings.Contains(b.ActivityGone, id) {
			lost = append(lost, id)
			changed = true
		}
		if _, ok := b.After[id]; ok {
			delete(b.After, id)
			changed = true
		}
	}
	if len(lost) > 0 {
		forgotten := slices.Concat(b.Forgotten, lost)
		slices.Sort(forgotten)
		b.Forgotten = slices.Compact(forgotten)
	}
	return changed
}

// proposalsHoldingTx adds to proposals the proposals whose memory_ids hold one of ids, with those ids.
func proposalsHoldingTx(tx *sql.Tx, ids []string, proposals map[string][]string) error {
	for _, id := range ids {
		rows, err := tx.Query(`SELECT p.id FROM proposals p WHERE EXISTS (SELECT 1 FROM json_each(p.memory_ids) WHERE value=?)`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var pid string
			if err := rows.Scan(&pid); err != nil {
				rows.Close()
				return err
			}
			proposals[pid] = unionIDs(proposals[pid], []string{id})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// scrubAutoRevertEventsTx replaces the title in the audit.auto_reverted events of ledger line l (the line
// that was undone or the undo itself). The ledger id in the event stays.
func scrubAutoRevertEventsTx(tx *sql.Tx, l auditLine) error {
	title := fmt.Sprintf(forgetProposalTitle, strings.Join(l.hit, ", "))
	_, err := tx.Exec(`UPDATE events SET data=json_set(data,'$.title',?) WHERE kind=? AND json_valid(data)
		AND COALESCE(json_extract(data,'$.title'),'')<>'' AND (json_extract(data,'$.ledgerId')=? OR json_extract(data,'$.revertId')=?)`,
		title, EventAutoReverted, l.id, l.id)
	return err
}

// unionIDs is a with the ids of b it lacks, in order.
func unionIDs(a, b []string) []string {
	for _, id := range b {
		if !slices.Contains(a, id) {
			a = append(a, id)
		}
	}
	return a
}
