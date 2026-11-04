package memory

// 처음 핵심 뽑기(S3 명세 2.7c): 확정 기억 가운데 한도 안에서 늘 지킬 것을 고르는 제안. 모델은 쓰지 않는다(A6).
// 사람이 틀린 것만 고치고, 적용은 승격(Pin)으로 한다. 이 함수는 아무것도 바꾸지 않는다.

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onidot-labs/orbit-mem/internal/place"
)

// CoreCandidate is one memory of a core suggestion with why it ranks where it does.
type CoreCandidate struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Runes       int    `json:"runes"`                 // 세션 시작에 드는 글자 수(CoreRunes)
	Score       int    `json:"score"`                 // 교정×3 + 어김×3 + 따름 + 사용자 말 2
	Corrections int    `json:"corrections,omitempty"` // 이 기억을 대상으로 한 사용자 교정 수
	Violated    int    `json:"violated,omitempty"`    // 사용 판정 「어김」 수
	Followed    int    `json:"followed,omitempty"`    // 사용 판정 「따름」 수
	Quote       bool   `json:"quote,omitempty"`       // 사용자 말을 담은 기억(승격에 userQuote가 든다)
}

// CoreSuggestion is the suggested core of one layer: what is pinned already, then the picks that fit.
type CoreSuggestion struct {
	Scope      string          `json:"scope"`
	AppliesTo  string          `json:"appliesTo"`
	Limit      int             `json:"limit"`
	Total      int             `json:"total"` // 이미 핵심 + 고른 것의 글자 수
	Pinned     []CoreCandidate `json:"pinned"`
	Picks      []CoreCandidate `json:"picks"`
	Considered int             `json:"considered"` // 고를 수 있던 후보 수(이미 핵심 빼고)
	Left       int             `json:"left"`       // 자리가 없어 못 고른 후보 수
}

// SuggestCore suggests the core of a layer (global or product:<name>): the confirmed rules (and, for a
// product, decisions) the user corrected or the AI broke most, then those it kept, a memory holding the
// user's words before others, shorter and newer first on a tie, as many as fit next to the core already
// there. Domain memories (a signal when a request matches), memories with tool triggers (they come right
// before the tool), expired, superseded or review-due ones and any longer than a third of the limit are
// left out. It changes nothing.
func (s *Store) SuggestCore(scope string) (CoreSuggestion, error) {
	limit := CoreLimit(scope)
	out := CoreSuggestion{Scope: scope, AppliesTo: place.AppliesTo(scope), Limit: limit, Pinned: []CoreCandidate{}, Picks: []CoreCandidate{}}
	if limit == 0 {
		return out, fmt.Errorf("%w: %s has no core (user·product:<name> only)", ErrInvalid, place.AppliesTo(scope))
	}
	corrections, violated, followed, err := s.coreSignals()
	if err != nil {
		return out, err
	}
	heads, _ := s.TopicHeads()
	now := s.Now()
	confirmed, err := s.List(StateConfirmed)
	if err != nil {
		return out, err
	}
	var cands []CoreCandidate
	for _, m := range confirmed {
		if m.Scope != scope {
			continue
		}
		c := CoreCandidate{ID: m.ID, Kind: m.Kind, Title: m.Title, Runes: CoreRunes(m), Corrections: corrections[m.ID], Violated: violated[m.ID], Followed: followed[m.ID]}
		c.Quote, _ = s.Protected(m.ID)
		c.Score = 3*c.Corrections + 3*c.Violated + c.Followed
		if c.Quote {
			c.Score += 2
		}
		if m.Pinned {
			out.Pinned = append(out.Pinned, c)
			out.Total += c.Runes
			continue
		}
		switch {
		case m.Kind != KindRule && !(m.Kind == KindDecision && scope != ScopeGlobal):
		case DomainOf(m) != "", len(m.Tools) > 0:
		case m.Freshness(now) != FreshOK, heads.Superseding(m) != "":
		case c.Runes > limit/3:
		default:
			cands = append(cands, c)
		}
	}
	updated := map[string]int64{}
	for _, m := range confirmed {
		updated[m.ID] = m.UpdatedAt
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		switch {
		case a.Score != b.Score:
			return a.Score > b.Score
		case a.Runes != b.Runes:
			return a.Runes < b.Runes
		case updated[a.ID] != updated[b.ID]:
			return updated[a.ID] > updated[b.ID]
		}
		return a.ID < b.ID
	})
	out.Considered = len(cands)
	for _, c := range cands {
		if out.Total+c.Runes > limit {
			out.Left++
			continue
		}
		out.Picks = append(out.Picks, c)
		out.Total += c.Runes
	}
	return out, nil
}

// coreSignals counts, per memory, the user's corrections that target it and the usage verdicts violated
// and followed.
func (s *Store) coreSignals() (corrections, violated, followed map[string]int, err error) {
	corrections, violated, followed = map[string]int{}, map[string]int{}, map[string]int{}
	rows, err := s.db.Query(`SELECT kind, data FROM events WHERE kind IN (?,?)`, EventCorrection, EventMemoryUsed)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, data string
		if rows.Scan(&kind, &data) != nil {
			continue
		}
		var d struct {
			MemoryID string `json:"memoryId"`
			Verdict  string `json:"verdict"`
		}
		if json.Unmarshal([]byte(data), &d) != nil || d.MemoryID == "" {
			continue
		}
		switch {
		case kind == EventCorrection:
			corrections[d.MemoryID]++
		case d.Verdict == UsageViolated:
			violated[d.MemoryID]++
		case d.Verdict == UsageFollowed:
			followed[d.MemoryID]++
		}
	}
	return corrections, violated, followed, rows.Err()
}
