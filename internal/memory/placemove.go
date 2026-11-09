package memory

// 갇힌 기억 옮기기(S3-R5, 명세 2.10): 제품 지도가 생기거나 바뀌면 product:? 기억을 다시 판정해 제품으로 옮긴다.
// 자동 변경(edit, 범위만)으로 제안 표·원장에 남아 감사 목록에서 되돌린다.

import (
	"errors"
	"fmt"
	"os"

	"github.com/crispebble/orbit-mem/internal/place"
)

// Moved is one memory the product map moved.
type Moved struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Folder   string `json:"folder"`
	From     string `json:"from"`
	To       string `json:"to"`
	LedgerID string `json:"ledgerId"`
}

// MoveUnknown is MoveUnknownIn with this user's home.
func (s *Store) MoveUnknown(m place.Map) ([]Moved, error) {
	home, _ := os.UserHomeDir()
	return s.MoveUnknownIn(m, home)
}

// MoveUnknownIn moves the live product:? memories whose folder the map now gives one product, or, in a
// folder of several products, whose title and body name one of them (S3-R2b), to that product. A move the
// person undid is not done again. Nothing to move, or no map, is no error.
func (s *Store) MoveUnknownIn(m place.Map, home string) ([]Moved, error) {
	if len(m) == 0 {
		return nil, nil
	}
	live, err := s.List(StateCandidate, StateConfirmed)
	if err != nil {
		return nil, err
	}
	var out []Moved
	var errs []error
	for _, mem := range live {
		if mem.Scope != place.ScopeUnknown || mem.Project == "" {
			continue
		}
		pl := place.ResolveIn("project:"+mem.Project, m, home)
		to := pl.Product()
		if to == "" && pl.Multi() {
			// 낱말 일치는 추측이다: 사용자 말 기억은 지도가 한 제품으로 정한 폴더일 때만 옮긴다(검토 지적).
			if p, err := s.Protected(mem.ID); err != nil || p {
				continue
			}
			to = pl.Narrow(mem.Title + "\n" + mem.Body)
		}
		if to == "" {
			continue
		}
		scope := "product:" + to
		// 옮기면 같은 제품·주제의 현재값이 둘이 된다: 그대로 두고 사람·회고가 대체로 정리한다.
		if mem.Topic != "" && s.liveTopicIn(scope, mem.Topic, mem.ID) {
			continue
		}
		res, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "제품 지도로 옮김: " + mem.Title,
			Body:      fmt.Sprintf("product:?(%s) → %s. 제품 지도(config products)가 이 폴더를 이 제품으로 정한다.", mem.Project, scope),
			MemoryIDs: []string{mem.ID}, Payload: Payload{Scope: scope}, MapMove: true})
		switch {
		case errors.Is(err, ErrAutoSkip), errors.Is(err, ErrDuplicateProposal):
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", mem.ID, err))
			continue
		}
		out = append(out, Moved{ID: mem.ID, Title: mem.Title, Folder: mem.Project, From: place.ScopeUnknown, To: scope, LedgerID: res.LedgerID})
	}
	return out, errors.Join(errs...)
}

// liveTopicIn says whether another live memory holds the topic in the scope.
func (s *Store) liveTopicIn(scope, topic, except string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE scope=? AND topic=? AND id<>? AND state IN ('candidate','confirmed')`, scope, topic, except).Scan(&n)
	return n > 0
}
