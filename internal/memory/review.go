package memory

// 재검토 후보 목록(명세 L-3): 마지막 확인 뒤 90일이 지난 기억을 세션 시작의 개수 줄과 감사 목록이 같은 규칙으로 센다.

import (
	"cmp"
	"slices"
	"strings"
)

// ReviewDueMemories lists the memories due for review (L-1) that show as a current value: live (candidate or
// confirmed), not a journal, not volatile, past their review date, and not an older value of a topic. sees narrows
// them to the memories a place sees (place.Place.Sees); nil keeps all. Oldest review date first, then id.
func (s *Store) ReviewDueMemories(sees func(scope, project string) bool) ([]Memory, error) {
	live, err := s.List(StateCandidate, StateConfirmed)
	if err != nil {
		return nil, err
	}
	vw, err := s.Viewer()
	if err != nil {
		return nil, err
	}
	var out []Memory
	for _, m := range live {
		if m.Kind == KindJournal || (sees != nil && !sees(m.Scope, m.Project)) {
			continue
		}
		if v := vw.Of(m); v.Current() && v.ReviewDue {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b Memory) int {
		if c := cmp.Compare(reviewDate(a), reviewDate(b)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// reviewDate is when m falls (or fell) due for review: its review date, or 90 days after its last change when it has none.
func reviewDate(m Memory) int64 {
	if m.ReviewAt > 0 {
		return m.ReviewAt
	}
	return m.UpdatedAt + DefaultReviewDays*dayMs
}
