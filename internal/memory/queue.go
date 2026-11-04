package memory

import (
	"errors"
	"fmt"
	"strings"
)

// 대기열 정리(설계 · orbit-mem 2판 18.5): 사람 승인 대기열을 없애며, 쌓인 대기 제안을 새 규칙으로 다시
// 처리하고 모두 닫는다. 행은 지우지 않는다.

// StatusDropped is a pending proposal closed without applying it when the
// queue went away; its note says why.
const StatusDropped = "dropped"

// DrainSkipped is a pending proposal a preview left alone because applying it
// would write a file (the copy's database is not the real one, the files are).
const DrainSkipped = "skipped"

// DrainItem is what happened to one pending proposal.
type DrainItem struct {
	ProposalID string `json:"proposalId"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Status     string `json:"status"` // settled(자동 규칙으로 적용) | dropped(적용하지 않고 닫음) | pending(일시 오류, 다시 돌리면 다시 본다) | skipped(미리 보기)
	Note       string `json:"note"`
	AppliedAs  string `json:"appliedAs,omitempty"` // 적용한 자동 변경의 제안 id
	LedgerID   string `json:"ledgerId,omitempty"`
}

// DrainResult counts what DrainPending did.
type DrainResult struct {
	Items   []DrainItem `json:"items"`
	Applied int         `json:"applied"`
	Dropped int         `json:"dropped"`
	Left    int         `json:"left,omitempty"` // 일시 오류나 미리 보기로 대기에 남은 것
}

// DrainOptions tunes DrainPendingWith.
type DrainOptions struct {
	// Preview runs on a copy of the database: a proposal whose change writes a
	// file (a revert of a file change) is not run and stays pending.
	Preview bool
}

// DrainPending closes every pending proposal (DrainPendingWith without options).
func (s *Store) DrainPending() (DrainResult, error) { return s.DrainPendingWith(DrainOptions{}) }

// DrainPendingWith closes every pending proposal. Each one is closed first (so
// it does not stand in its own way), then handed in again as an automation's
// change (SubmitChange): applied, it is settled and points at the automatic
// change; not applied (no evidence, the user's words, a direction, done
// already, the memory changed since), it is dropped with the reason. A
// transient failure puts it back to pending so a later run sees it again.
func (s *Store) DrainPendingWith(o DrainOptions) (DrainResult, error) {
	res := DrainResult{Items: []DrainItem{}}
	pending, err := s.Proposals(StatusPending)
	if err != nil {
		return res, err
	}
	var errs []error
	for _, p := range pending {
		it := DrainItem{ProposalID: p.ID, Kind: p.Kind, Title: p.Title, Status: StatusDropped}
		if o.Preview && s.writesFile(p) {
			it.Status, it.Note = DrainSkipped, "미리 보기: 파일을 되돌리는 변경이라 사본에서도 돌리지 않음(실제로 돌리면 자동 변경만 되돌린다)"
			res.Left++
			res.Items = append(res.Items, it)
			continue
		}
		if why := s.changedSince(p); why != "" {
			it.Note = "대기열 정리: 적용하지 않고 닫음 — " + why
			if err := s.finish(&p, StatusDropped, ClipRunes(it.Note, 500)); err != nil {
				return res, err
			}
			res.Dropped++
			res.Items = append(res.Items, it)
			continue
		}
		if err := s.finish(&p, StatusDropped, "대기열 정리 중"); err != nil {
			return res, err
		}
		ids := p.MemoryIDs
		if p.Kind == ProposalRetag && len(p.Payload.FromTags) > 0 {
			// 태그 묶기는 뺄 태그를 가진 기억 모두가 대상이다(prepare가 채운 목록이 아니라 지금 기억으로 다시 고른다).
			// 그래야 사용자 말을 담은 기억 하나 때문에 묶기 전체가 닫히지 않고 그 기억만 건너뛴다.
			ids = nil
		}
		out, err := s.SubmitChange(ProposalInput{Kind: p.Kind, Title: p.Title, Body: originalBody(p), MemoryIDs: ids,
			Payload: p.Payload, RetroID: p.RetroID}, Who{Actor: ActorAutomation})
		switch {
		case err == nil && out.Applied:
			it.Status, it.AppliedAs, it.LedgerID = StatusSettled, out.Proposal.ID, out.LedgerID
			it.Note = "대기열 정리: 자동 규칙으로 적용(" + strings.Trim(out.Proposal.ID+" · "+out.LedgerID, " ·") + ")"
			if out.Reason != "" { // 사용자 말을 담은 기억은 빼고 적용했다
				it.Note += " — 뺀 것: " + out.Reason
			}
		case err == nil:
			it.Note = "대기열 정리: 적용하지 않고 닫음 — " + out.Reason
		case errors.Is(err, ErrAutoSkip), errors.Is(err, ErrDuplicateProposal), errors.Is(err, ErrInvalid),
			errors.Is(err, ErrFileChanged), errors.Is(err, ErrMemoryChanged):
			it.Note = "대기열 정리: 할 것이 남지 않아 닫음 — " + err.Error()
		default:
			// 일시 오류(잠김 같은 것): 대기로 되돌려 다음 실행이 다시 본다.
			it.Status, it.Note = StatusPending, "대기열 정리: 일시 오류로 남김 — "+err.Error()
			errs = append(errs, fmt.Errorf("%s: %w", p.ID, err))
		}
		if it.Status == StatusPending {
			if _, err := s.db.Exec(`UPDATE proposals SET status=?, decided_at=0, note='' WHERE id=?`, StatusPending, p.ID); err != nil {
				return res, err
			}
			res.Left++
			res.Items = append(res.Items, it)
			continue
		}
		if err := s.finish(&p, it.Status, ClipRunes(it.Note, 500)); err != nil {
			return res, err
		}
		if it.Status == StatusSettled {
			res.Applied++
		} else {
			res.Dropped++
		}
		res.Items = append(res.Items, it)
	}
	return res, errors.Join(errs...)
}

// writesFile says whether applying p writes a file: a file change, or the
// revert of a ledger line that changed a file.
func (s *Store) writesFile(p Proposal) bool {
	switch p.Kind {
	case ProposalApplyFile:
		return true
	case ProposalRevert:
		e, err := s.LedgerEntry(p.Payload.LedgerID)
		return err != nil || e.Before.File != nil || e.TargetKind != TargetMemory
	}
	return false
}

// changedSince is why an old edit or reclassify proposal must not be applied:
// a target memory changed after the proposal was made (applying it would
// overwrite what came later), or "".
func (s *Store) changedSince(p Proposal) string {
	if p.Kind != ProposalEdit && p.Kind != ProposalReclassify {
		return ""
	}
	for _, id := range p.MemoryIDs {
		if m, err := getTx(s.db, id); err == nil && m.UpdatedAt > p.CreatedAt {
			return "제안 뒤에 기억(" + id + ")이 바뀌어 옛 고치기로 덮지 않음"
		}
	}
	return ""
}

// originalBody is a proposal's body without what prepare added to it (a diff
// or the list of memories a retag changes), so handing it in again does not
// stack them.
func originalBody(p Proposal) string {
	body := p.Body
	for _, mark := range []string{"\n\n```diff\n", "\n\n파일: ", "\n\n넣을 태그: ", "\n\n바꿀 태그: ", "\n\n되돌릴 변경("} {
		if i := strings.Index(body, mark); i >= 0 {
			body = body[:i]
		}
	}
	for _, mark := range []string{"```diff\n", "파일: ", "넣을 태그: ", "바꿀 태그: ", "되돌릴 변경("} {
		if strings.HasPrefix(body, mark) {
			return ""
		}
	}
	return strings.TrimSpace(body)
}
