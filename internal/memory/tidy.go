package memory

import "github.com/crispebble/orbit-mem/internal/place"

// 하루 한 번 정리(기획 · orbit-mem 4.3 H1~H4): 모델 없이 도는 정리 단계다. 예전에는 회고 마무리
// (Keeper.Judge·Settle)가 돌렸고, 이제는 세션 시작 훅이 하루 한 번 뒤 프로세스(orbit-mem tidy auto)로 돌린다.

// MetaTidyDay is the meta key holding the last day (YYYY-MM-DD) the daily tidy ran.
const MetaTidyDay = "tidy.day"

// SessionStateDays is how many days the pinned session bundles are kept. Claude Code keeps
// conversation logs 30 days by default (cleanupPeriodDays), so an older session is not resumed.
const SessionStateDays = 30

// TidyOptions are the settings the daily tidy reads.
type TidyOptions struct {
	LedgerWindowDays    int       // 변경 효과를 보는 기간(일), 1 미만이면 1
	CandidateExpiryDays int       // 이 날 수 동안 쓰지 않은 후보를 낡음으로, 0이면 끔
	ExpireCandidates    bool      // 후보 기한 낡음을 돌린다: 회상을 넣는 설정(inject full)에서만 참
	Products            place.Map // product:? 기억을 옮길 제품 지도
}

// TidyResult is what one tidy did. A failed step does not stop the others.
type TidyResult struct {
	Judged    int      `json:"judged"`    // 관찰 기간이 끝나 판정한 변경
	Reverted  int      `json:"reverted"`  // 해롭다고 판정돼 되돌린 자동 변경
	Confirmed int      `json:"confirmed"` // 증거가 생겨 확정한 후보
	Closed    int      `json:"closed"`    // 닫은 끝난 제안
	Reviewed  int      `json:"reviewed"`  // 기한이 지나 낡음으로 바꾼 휘발 기억
	Expired   int      `json:"expired"`   // 오래 쓰지 않아 낡음으로 바꾼 후보
	Moved     int      `json:"moved"`     // 제품 지도로 옮긴 product:? 기억
	Errors    []string `json:"errors"`
}

// Tidy runs the model-free tidy: it judges the changes whose observation window ended and
// reverts the automatic changes judged harmful, then confirms the candidates whose evidence
// came in, closes the settled proposals, marks expired volatile memories (and, when asked,
// long-unused candidates) stale, moves product:? memories onto the product map and drops
// old session bundles and idempotency keys. Every change it makes is an automatic change in the ledger.
func (s *Store) Tidy(o TidyOptions) TidyResult {
	r := TidyResult{Errors: []string{}}
	fail := func(err error) {
		if err != nil {
			r.Errors = append(r.Errors, err.Error())
		}
	}
	n, err := s.JudgeLedger(max(o.LedgerWindowDays, 1))
	r.Judged = n
	fail(err)
	reverts, err := s.RevertHarmfulAuto()
	fail(err)
	for _, rv := range reverts {
		if rv.Error != "" {
			r.Errors = append(r.Errors, rv.LedgerID+": "+rv.Error)
			continue
		}
		r.Reverted++
	}
	r.Confirmed, err = s.ConfirmCandidates()
	fail(err)
	r.Closed, err = s.CloseSettled()
	fail(err)
	rev, err := s.ReviewDue()
	r.Reviewed = rev.Expired
	fail(err)
	// 회상을 넣지 않는 설정(inject rules·off)에서는 후보가 쓰일 기회가 없으니 하지 않는다(설계 · orbit-mem 2판 18.4).
	if o.ExpireCandidates {
		r.Expired, err = s.ExpireCandidates(o.CandidateExpiryDays)
		fail(err)
	}
	moved, err := s.MoveUnknown(o.Products)
	r.Moved = len(moved)
	fail(err)
	fail(s.PruneSessionState(SessionStateDays))
	_, err = s.PruneSaveRequests(SaveRequestDays)
	fail(err)
	return r
}

// ClaimDay marks day under key unless it is already marked, in one statement, and says
// whether this call marked it. Of several processes claiming the same day only the first
// wins, so a daily job runs once even when two sessions start together.
func (s *Store) ClaimDay(key, day string) (bool, error) {
	res, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value WHERE meta.value<>excluded.value`, key, day)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
