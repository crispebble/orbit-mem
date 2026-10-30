package memory

import (
	"fmt"
	"strings"
)

// BatchFailure is one proposal a batch decision could not decide, and why.
type BatchFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// BatchResult is the outcome of deciding several proposals at once.
type BatchResult struct {
	Decided []DecideResult `json:"decided"`
	Failed  []BatchFailure `json:"failed"`
}

// DecideMany approves or rejects several proposals with the same answer
// (설계 · Orbit 3차 「묶어 보기와 일괄 결정」). Proposals do not depend on each
// other, so one that fails does not stop the rest; it comes back with its
// reason. Editing before approval stays one at a time.
func (s *Store) DecideMany(ids []string, decision, note string) BatchResult {
	res := BatchResult{Decided: []DecideResult{}, Failed: []BatchFailure{}}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if decision != "approve" && decision != "reject" {
			res.Failed = append(res.Failed, BatchFailure{ID: id, Error: fmt.Sprintf("%v: decision은 approve 또는 reject", ErrInvalid)})
			continue
		}
		r, err := s.Decide(DecideInput{ID: id, Decision: decision, Note: note})
		if err != nil {
			res.Failed = append(res.Failed, BatchFailure{ID: id, Error: err.Error()})
			continue
		}
		res.Decided = append(res.Decided, r)
	}
	return res
}
