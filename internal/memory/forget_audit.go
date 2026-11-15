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
	text                   string // targets·observe·before를 이은 글: id를 찾는 범위
	revertOf, revertedBy   string
	hit                    []string // 지운 id 가운데 이 줄이 가리키는 것
	direct                 bool     // targets·observe·before 글에 id가 있는 줄(되돌림으로 이어진 줄은 false)
}

// scrubAuditTx blanks what the ledger and the proposals keep of the memories ids, inside tx. It runs before
// the rows of the memories are deleted; now (ms) dates the pending proposals it closes.
//
// A ledger line is a target when its targets, observe or before text holds an id (a plain text search, so a
// line that only carries the id inside a snapshot is found too), and so is every line linked to it by
// revert_of or reverted_by. A target gets change and basis replaced by forgetLedgerChange; its before loses
// the snapshots of the forgotten memories and the snapshots that point at one (a superseded_by), and
// LedgerBefore.Forgotten names the ids it lost, so revertableErr refuses to undo it. A line that held the
// id only in observe loses text but not the way back.
//
// A proposal is a target when its memory_ids hold an id or a target line belongs to it: its title becomes
// forgetProposalTitle, body and payload are emptied and memory_ids stay. A revert proposal of a target line
// is one too (payload.ledgerId): one for a file change has no memory_ids and no ledger line when it is
// pending or its apply failed. A pending one is closed
// (StatusDropped). The title of an audit.auto_reverted event of a target line goes too; its ledgerId stays.
func scrubAuditTx(tx *sql.Tx, ids []string, now int64) error {
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
	for _, l := range lines {
		if err := revertProposalsOfTx(tx, l, proposals); err != nil {
			return err
		}
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
		if _, err := tx.Exec(`UPDATE proposals SET status=?, note=?, decided_at=? WHERE id=? AND status=?`, StatusDropped, forgetDropNote, now, pid, StatusPending); err != nil {
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
	list, _ := json.Marshal(ids)
	// 글 전체 검색이다: 스냅숏 안의 supersededBy처럼 targets에 없는 id도 찾는다.
	direct, err := queryAuditLines(tx, `SELECT id, proposal_id, targets, observe, before, revert_of, reverted_by FROM ledger
		WHERE EXISTS (SELECT 1 FROM json_each(?) j WHERE instr(targets, j.value)>0 OR instr(observe, j.value)>0 OR instr(before, j.value)>0)`, string(list))
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for i := range direct {
		l := &direct[i]
		have[l.id] = true
		for _, id := range ids {
			if strings.Contains(l.text, id) {
				l.hit = append(l.hit, id)
			}
		}
		l.direct = true
	}
	// 이어진 줄은 id가 글에 없을 수 있다(관찰 대상으로만 든 줄의 되돌림은 observe가 비어 있다): 이은 줄의 id를 물려받는다.
	inherit := map[string][]string{}
	for _, l := range direct {
		for _, link := range []string{l.revertOf, l.revertedBy} {
			if link != "" && !have[link] {
				inherit[link] = unionIDs(inherit[link], l.hit)
			}
		}
	}
	out := direct
	linked := make([]string, 0, len(inherit))
	for id := range inherit {
		linked = append(linked, id)
	}
	sort.Strings(linked)
	for _, id := range linked {
		rows, err := queryAuditLines(tx, `SELECT id, proposal_id, targets, observe, before, revert_of, reverted_by FROM ledger WHERE id=?`, id)
		if err != nil {
			return nil, err
		}
		for _, l := range rows {
			l.hit = inherit[l.id]
			out = append(out, l)
		}
	}
	return out, nil
}

// queryAuditLines reads ledger lines (id, proposal_id, targets, observe, before, revert_of, reverted_by).
func queryAuditLines(tx *sql.Tx, query string, args ...any) ([]auditLine, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditLine
	for rows.Next() {
		var l auditLine
		var targets, observe string
		if err := rows.Scan(&l.id, &l.proposalID, &targets, &observe, &l.before, &l.revertOf, &l.revertedBy); err != nil {
			return nil, err
		}
		l.text = targets + "\n" + observe + "\n" + l.before
		out = append(out, l)
	}
	return out, rows.Err()
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
		if slices.Contains(b.Created, id) {
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

// revertProposalsOfTx adds to proposals the revert proposals that point at ledger line l.
func revertProposalsOfTx(tx *sql.Tx, l auditLine, proposals map[string][]string) error {
	rows, err := tx.Query(`SELECT id FROM proposals WHERE kind=? AND json_valid(payload) AND json_extract(payload,'$.ledgerId')=?`, ProposalRevert, l.id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return err
		}
		proposals[pid] = unionIDs(proposals[pid], l.hit)
	}
	return rows.Err()
}
