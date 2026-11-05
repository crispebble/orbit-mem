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
	"strings"
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
	r.From, r.To = m.Scope, JournalHost+" "+JournalSession(id)
	if m.Pinned {
		r.Skip = "pinned: unpin it first"
		return r, nil
	}
	if dry {
		return r, nil
	}
	session := JournalSession(id)
	scope := m.Scope
	if scope == ScopeGlobal {
		scope = ""
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO activity(host,session,project_scope,project_label,cwd,launcher,parent,started_at,last_at,ended_at,prompts,first_prompt,last_prompt,summary)
		VALUES(?,?,?,?,'',?,'',?,?,?,0,?,'',?)`, JournalHost, session, scope, m.Project, JournalHost, m.CreatedAt, m.CreatedAt, m.CreatedAt, ClipRunes(m.Title, ActivityPromptRunes), m.Body)
	if err != nil {
		return r, err
	}
	inserted, _ := res.RowsAffected()
	dr, err := s.AutoApply(AutoInput{Kind: ProposalStale, Title: JournalReason + ": " + m.Title,
		Body:      "일지는 기억이 아니라 하루 기록이라 활동 기록 " + session + "으로 옮겼다(S4-8).",
		MemoryIDs: []string{id}, Payload: Payload{Activity: JournalHost + "\x00" + session}, Who: Who{Actor: ActorAI}, JournalMove: true})
	if err != nil {
		if inserted == 1 { // 옮기지 못했으면 넣은 줄도 뺀다: 같은 일지가 두 곳에 남지 않는다
			_, _ = s.db.Exec(`DELETE FROM activity WHERE host=? AND session=?`, JournalHost, session)
		}
		if errors.Is(err, ErrAutoSkip) || errors.Is(err, ErrDuplicateProposal) {
			r.Skip = "reverted before or already proposed"
			return r, nil
		}
		return r, err
	}
	r.LedgerID = dr.LedgerID
	return r, nil
}

// journalMoveOnly checks a JournalMove change before it skips the user's-words guard: it marks one
// journal stale, and the activity row it names holds that journal's title and body.
func (s *Store) journalMoveOnly(in AutoInput) error {
	if in.Kind != ProposalStale || len(in.MemoryIDs) != 1 {
		return fmt.Errorf("%w: a journal move marks one journal stale", ErrInvalid)
	}
	m, err := getTx(s.db, in.MemoryIDs[0])
	if err != nil {
		return err
	}
	host, session, _ := strings.Cut(in.Payload.Activity, "\x00")
	if m.Kind != KindJournal || host != JournalHost || session != JournalSession(m.ID) {
		return fmt.Errorf("%w: a journal move needs the journal's activity row", ErrInvalid)
	}
	var body string
	if err := s.db.QueryRow(`SELECT summary FROM activity WHERE host=? AND session=?`, host, session).Scan(&body); err != nil || body != m.Body {
		return fmt.Errorf("%w: activity row %s does not hold the journal", ErrInvalid, session)
	}
	return nil
}
