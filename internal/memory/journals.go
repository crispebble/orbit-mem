package memory

// 일지 → 활동 기록(S4-8, 설계 · orbit-mem 2판 11절 5): 일지(journal)는 기억이 아니라 하루에 한 일이다. 살아 있는
// 일지마다 제목·본문을 그대로 활동 기록 한 줄로 옮기고 원래 기억은 이유와 함께 낡음으로 표시한다. 일지마다 자동
// 변경 한 건(낡음)으로 제안 표·원장에 남고, audit revert로 되돌리면 기억이 돌아오고 옮긴 활동 줄은 빠진다.
//
// 지키는 것:
//   - 제목·본문·출처는 바꾸지 않는다. 활동 줄은 같은 글을 가진다(그래서 사용자 말이 담긴 일지도 옮긴다).
//   - 활동 줄 열쇠는 host 「journal」·session 「journal:<기억 id>」라 같은 일지를 두 번 넣지 않는다.
//   - 다시 돌리면 이미 낡은 일지는 건너뛰고, 사람이 되돌린 옮기기는 다시 하지 않는다.
//   - 상시 핵심 일지는 옮기지 않는다(사람이 먼저 내린다).

import (
	"errors"
	"fmt"
)

// JournalHost is the host and launcher of an activity row that holds a journal moved from the memories.
const JournalHost = "journal"

// JournalReason starts the stale reason of a moved journal.
const JournalReason = "활동 기록으로 옮김"

// JournalSession is the activity session key of a journal memory.
func JournalSession(id string) string { return "journal:" + id }

// JournalsToActivity moves every live journal to the activity log (dry: report only).
func (s *Store) JournalsToActivity(dry bool) (PlanReport, error) {
	rep := PlanReport{Applied: []PlanResult{}, Skipped: []PlanResult{}, Conflicts: []PlanResult{}}
	rows, err := s.db.Query(`SELECT id FROM memories WHERE kind=? AND state IN ('candidate','confirmed') ORDER BY created_at, id`, KindJournal)
	if err != nil {
		return rep, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return rep, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var errs []error
	for _, id := range ids {
		r, err := s.journalToActivity(id, dry)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		rep.add(r)
	}
	return rep, errors.Join(errs...)
}

func (s *Store) journalToActivity(id string, dry bool) (PlanResult, error) {
	r := PlanResult{ID: id, Action: "journal", Basis: "S4-8 일지는 활동 기록"}
	m, err := getTx(s.db, id)
	if err != nil {
		return r, err
	}
	session := JournalSession(id)
	r.From, r.To = m.Scope, JournalHost+" "+session
	if m.Pinned {
		r.Skip = "pinned: unpin it first"
		return r, nil
	}
	pi := ProposalInput{Kind: ProposalStale, Title: JournalReason + ": " + m.Title,
		Body:      "일지는 기억이 아니라 하루 기록이라 활동 기록 " + session + "으로 옮겼다(S4-8).",
		MemoryIDs: []string{id}, Payload: Payload{Activity: JournalHost + "\x00" + session, Actor: ActorAI}}
	if err := s.notAgain(pi); errors.Is(err, ErrAutoSkip) || errors.Is(err, ErrDuplicateProposal) {
		r.Skip = "reverted before or already proposed"
		return r, nil
	} else if err != nil {
		return r, err
	}
	if dry {
		return r, nil
	}
	// 활동 줄·낡음 표시·제안·원장을 한 트랜잭션에 쓴다: 하나라도 실패하면 아무것도 남지 않는다.
	tx, err := s.db.Begin()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	cur, err := getTx(tx, id) // 그사이 바뀌었으면(낡음 등) 건드리지 않는다
	if err != nil {
		return r, err
	}
	if cur.Kind != KindJournal || (cur.State != StateCandidate && cur.State != StateConfirmed) || cur.Pinned {
		r.Skip = "not live: " + cur.State
		return r, nil
	}
	if err := insertJournalActivity(tx, cur); err != nil {
		return r, err
	}
	now := s.nowMs()
	next := cur
	next.State, next.StaleReason = StateStale, JournalReason+": "+session
	next.Version++
	next.UpdatedAt = now
	if err := writeTx(tx, next, StateStale, next.StaleReason, now); err != nil {
		return r, err
	}
	p, err := s.insertProposalTx(tx, pi, StatusAuto)
	if err != nil {
		return r, err
	}
	if r.LedgerID, err = s.recordLedgerTx(tx, p, LedgerBefore{Memories: []Memory{cur}}, DecideResult{}); err != nil {
		return r, err
	}
	if err := tx.Commit(); err != nil {
		return r, err
	}
	s.closeFlagsOf(id, StateStale) // 낡은 기억에 열린 감사 표시를 남기지 않는다(재검토 N4)
	return r, nil
}

// insertJournalActivity puts journal m into the activity log (title and body as they are); an existing row stays.
func insertJournalActivity(q dbtx, m Memory) error {
	scope := m.Scope
	if scope == ScopeGlobal {
		scope = ""
	}
	_, err := q.Exec(`INSERT OR IGNORE INTO activity(host,session,project_scope,project_label,cwd,launcher,parent,started_at,last_at,ended_at,prompts,first_prompt,last_prompt,summary)
		VALUES(?,?,?,?,'',?,'',?,?,?,0,?,'',?)`, JournalHost, JournalSession(m.ID), scope, m.Project, JournalHost, m.CreatedAt, m.CreatedAt, m.CreatedAt, ClipRunes(m.Title, ActivityPromptRunes), m.Body)
	return err
}
