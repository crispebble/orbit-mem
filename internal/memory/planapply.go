package memory

// 이전 계획 적용(S4, 설계 · orbit-mem 2판 11절): 1판 기억을 2판 자리로 옮긴다. 어느 제품·어떤 주제·무엇이 무엇을
// 대체하는지는 판단이라 실행 파일 밖(AI·회고)이 계획 파일로 만들고, 여기서는 그 계획을 결정적으로 적용한다.
// 항목마다 자동 변경(edit·supersede) 한 건으로 제안 표·원장에 남아 감사 목록에서 되돌린다.
//
// 지키는 것:
//   - 내용(제목·본문·태그·출처)은 바꾸지 않는다. 그래서 사용자 말 기억도 자리·주제는 옮긴다(지도 옮기기와 같다).
//   - 계획을 만든 뒤 바뀐 기억(판 번호가 다름)은 건너뛴다.
//   - 상시 핵심 기억은 자리를 옮기지 않는다(옮기면 핵심에서 조용히 빠진다). 사람이 먼저 내린다.
//   - 제품 지도에 없는 제품으로는 옮기지 않는다(어느 폴더에서도 보이지 않게 된다).
//   - 옮긴 자리에 같은 주제의 다른 현재값이 있으면 옮기지 않고 어긋남으로 남긴다: 대체는 계획이 밝힌 것만 한다.
//   - 대체는 출처 우선순위를 따른다: 대체하는 쪽이 더 새 기억이어야 하고, 사용자 말은 사용자 말만 대체한다.
//   - 같은 계획을 다시 돌려도 이미 된 것은 건너뛰고, 사람이 되돌린 변경은 다시 하지 않는다.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/place"
)

// Plan is a migration plan an AI wrote: what to move where, which topic to give, what supersedes what.
type Plan struct {
	Name  string     `json:"name,omitempty"`
	Items []PlanItem `json:"items"`
}

// PlanItem is one memory of a plan. Version is the version the plan was made from (0 skips the check).
type PlanItem struct {
	ID           string `json:"id"`
	Version      int    `json:"version,omitempty"`
	AppliesTo    string `json:"appliesTo,omitempty"`    // 새 적용 대상(user·product:<이름>·product:?·repo:<키>), 비우면 그대로
	Topic        string `json:"topic,omitempty"`        // 새 주제, 비우면 그대로
	SupersededBy string `json:"supersededBy,omitempty"` // 이 기억을 대체할 기억
	Basis        string `json:"basis"`                  // 근거(rule1:<낱말>·map·ai:<이유>)
}

// PlanResult is what happened to one item.
type PlanResult struct {
	ID       string `json:"id"`
	Action   string `json:"action"` // place(자리·주제) 또는 supersede
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Topic    string `json:"topic,omitempty"`
	By       string `json:"by,omitempty"`
	LedgerID string `json:"ledgerId,omitempty"`
	Skip     string `json:"skip,omitempty"`
	Basis    string `json:"basis,omitempty"`
}

// PlanReport is the result of a plan: applied (or, in a dry run, what would be), skipped and conflicts
// (what a person decides: a topic another memory holds, a supersession against source precedence).
type PlanReport struct {
	Applied   []PlanResult `json:"applied"`
	Skipped   []PlanResult `json:"skipped"`
	Conflicts []PlanResult `json:"conflicts"`
}

// ApplyPlan applies plan with the product map m. A dry run changes nothing and reports what it would do.
func (s *Store) ApplyPlan(plan Plan, m place.Map, dry bool) (PlanReport, error) {
	rep := PlanReport{Applied: []PlanResult{}, Skipped: []PlanResult{}, Conflicts: []PlanResult{}}
	// 계획 안에서 같은 자리·주제를 받는 기억: 대체되는 쪽이 밝혀졌으면 둘이 함께 있어도 된다.
	leaving := map[string]bool{}
	// 출처 우선순위가 막는 대체는 일어나지 않으므로 그 기억은 떠나지 않는다(같은 자리·주제의 현재값이 둘이 되지 않게).
	for _, it := range plan.Items {
		if it.SupersededBy == "" {
			continue
		}
		old, err1 := getTx(s.db, it.ID)
		nw, err2 := getTx(s.db, it.SupersededBy)
		if err1 != nil || err2 != nil {
			continue
		}
		if why, err := s.precedes(nw, old); err == nil && why == "" {
			leaving[it.ID] = true
		}
	}
	var errs []error
	var supersedes []PlanItem
	for _, it := range plan.Items {
		if it.SupersededBy != "" {
			supersedes = append(supersedes, it)
		}
		if it.AppliesTo == "" && it.Topic == "" {
			continue
		}
		r, err := s.planPlace(it, m, leaving, dry)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.ID, err))
			continue
		}
		rep.add(r)
	}
	for _, it := range supersedes {
		r, err := s.planSupersede(it, dry)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.ID, err))
			continue
		}
		rep.add(r)
	}
	return rep, errors.Join(errs...)
}

func (rep *PlanReport) add(r PlanResult) {
	switch {
	case r.Skip == "":
		rep.Applied = append(rep.Applied, r)
	case strings.HasPrefix(r.Skip, "topic taken"), strings.HasPrefix(r.Skip, "newer"), strings.HasPrefix(r.Skip, "user quote"):
		rep.Conflicts = append(rep.Conflicts, r)
	default:
		rep.Skipped = append(rep.Skipped, r)
	}
}

// planPlace moves one memory's applies-to and topic.
func (s *Store) planPlace(it PlanItem, m place.Map, leaving map[string]bool, dry bool) (PlanResult, error) {
	r := PlanResult{ID: it.ID, Action: "place", Basis: it.Basis}
	mem, err := getTx(s.db, it.ID)
	if err != nil {
		return r, err
	}
	r.From = place.AppliesTo(mem.Scope)
	if mem.State != StateCandidate && mem.State != StateConfirmed {
		r.Skip = "not live: " + mem.State
		return r, nil
	}
	scope, project := mem.Scope, mem.Project
	if it.AppliesTo != "" {
		sc, ok := place.ScopeOf(it.AppliesTo)
		if !ok {
			return r, fmt.Errorf("%w: appliesTo %q", ErrInvalid, it.AppliesTo)
		}
		if p := place.ProductOf(sc); p != "" {
			if _, ok := m[p]; !ok {
				r.Skip = "unknown product: " + p + " is not in the product map"
				return r, nil
			}
		}
		scope, project = sc, ""
		if strings.HasPrefix(sc, "project:") {
			project = strings.TrimPrefix(sc, "project:")
		}
		if sc == place.ScopeUnknown {
			project = mem.Project
		}
	}
	topic := mem.Topic
	if it.Topic != "" {
		topic = TopicKey(it.Topic)
	}
	r.To, r.Topic = place.AppliesTo(scope), topic
	if scope == mem.Scope && project == mem.Project && topic == mem.Topic {
		r.Skip = "already there"
		return r, nil
	}
	if it.Version != 0 && it.Version != mem.Version {
		r.Skip = fmt.Sprintf("changed: plan v%d, now v%d", it.Version, mem.Version)
		return r, nil
	}
	if mem.Pinned && scope != mem.Scope {
		r.Skip = "pinned: unpin it first (moving takes it out of the core without a word)"
		return r, nil
	}
	if topic != "" && !leaving[mem.ID] {
		if other := s.liveTopicHolder(scope, topic, mem.ID); other != "" && !leaving[other] {
			r.Skip = "topic taken by " + other
			return r, nil
		}
	}
	if dry {
		return r, nil
	}
	pl := Payload{Scope: scope, Project: project}
	if scope == mem.Scope && project == mem.Project {
		pl.Scope, pl.Project = "", ""
	}
	if topic != mem.Topic {
		pl.Topic = topic
	}
	res, err := s.AutoApply(AutoInput{Kind: ProposalEdit, Title: "이전 계획: " + mem.Title,
		Body:      fmt.Sprintf("%s → %s, 주제 %q. 근거: %s", r.From, r.To, topic, it.Basis),
		MemoryIDs: []string{mem.ID}, Payload: pl, Who: Who{Actor: ActorAI}, PlanMove: true})
	switch {
	case errors.Is(err, ErrAutoSkip), errors.Is(err, ErrDuplicateProposal):
		r.Skip = "reverted before or already proposed"
		return r, nil
	case err != nil:
		return r, err
	}
	r.LedgerID = res.LedgerID
	return r, nil
}

// planSupersede marks a memory stale, replaced by it.SupersededBy, when source precedence allows it.
func (s *Store) planSupersede(it PlanItem, dry bool) (PlanResult, error) {
	r := PlanResult{ID: it.ID, Action: "supersede", By: it.SupersededBy, Basis: it.Basis}
	old, err := getTx(s.db, it.ID)
	if err != nil {
		return r, err
	}
	nw, err := getTx(s.db, it.SupersededBy)
	if err != nil {
		return r, err
	}
	switch {
	case old.State == StateStale && old.SupersededBy == nw.ID:
		r.Skip = "already there"
		return r, nil
	case old.State != StateCandidate && old.State != StateConfirmed:
		r.Skip = "not live: " + old.State
		return r, nil
	case nw.State != StateCandidate && nw.State != StateConfirmed:
		r.Skip = "replacement not live: " + nw.State
		return r, nil
	case old.Pinned:
		r.Skip = "pinned: a person replaces a core memory"
		return r, nil
	}
	if why, err := s.precedes(nw, old); err != nil {
		return r, err
	} else if why != "" {
		r.Skip = why
		return r, nil
	}
	if dry {
		return r, nil
	}
	res, err := s.AutoApply(AutoInput{Kind: ProposalSupersede, Title: "이전 계획 대체: " + old.Title,
		Body:      fmt.Sprintf("%s가 대체한다. 근거: %s", nw.ID, it.Basis),
		MemoryIDs: []string{old.ID, nw.ID}, Who: Who{Actor: ActorAI}, PlanMove: true})
	switch {
	case errors.Is(err, ErrAutoSkip), errors.Is(err, ErrDuplicateProposal):
		r.Skip = "reverted before or already proposed"
		return r, nil
	case err != nil:
		return r, err
	}
	r.LedgerID = res.LedgerID
	return r, nil
}

// precedes says why nw may not supersede old ("" when it may), by source precedence (S2): only the user's
// words replace the user's words, and only newer words (the newest quote of each, not when the memory was
// saved: a retro can save old words late). What an AI learned yields to anything the plan names.
func (s *Store) precedes(nw, old Memory) (string, error) {
	qo, err := s.quoteAt(old.ID)
	if err != nil || qo == 0 {
		return "", err
	}
	qn, err := s.quoteAt(nw.ID)
	if err != nil {
		return "", err
	}
	if qn == 0 {
		return "user quote: " + nw.ID + " (not the user's words) does not replace the user's words in " + old.ID, nil
	}
	if qn <= qo {
		return "newer: the user's words in " + old.ID + " are newer than those in its replacement " + nw.ID, nil
	}
	return "", nil
}

// quoteAt is when the newest user's words of a memory were kept (Unix ms), or 0 when it has none.
func (s *Store) quoteAt(id string) (int64, error) {
	var at int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(at),0) FROM sources WHERE memory_id=? AND kind=?`, id, SourceQuote).Scan(&at)
	return at, err
}

// liveTopicHolder is another live memory holding topic in scope, or "".
func (s *Store) liveTopicHolder(scope, topic, except string) string {
	var id string
	_ = s.db.QueryRow(`SELECT id FROM memories WHERE scope=? AND topic=? AND id<>? AND state IN ('candidate','confirmed') ORDER BY created_at LIMIT 1`, scope, topic, except).Scan(&id)
	return id
}

// planOnly checks a plan change: an edit of one memory's applies-to and topic (no content), or a
// supersession the plan checked against source precedence.
func planOnly(in AutoInput) error {
	pl := in.Payload
	switch in.Kind {
	case ProposalEdit:
		if len(in.MemoryIDs) != 1 || pl.Title != "" || pl.Body != "" || pl.Tags != nil || (pl.Scope == "" && pl.Topic == "") {
			return fmt.Errorf("%w: a plan edit changes one memory's applies-to and topic only", ErrInvalid)
		}
	case ProposalSupersede:
	default:
		return fmt.Errorf("%w: a plan change is an edit or a supersession", ErrInvalid)
	}
	return nil
}

// planMoveOnly checks a PlanMove change before it skips the user's-words guard.
func (s *Store) planMoveOnly(in AutoInput) error {
	if err := planOnly(in); err != nil {
		return err
	}
	if in.Kind != ProposalSupersede {
		return nil
	}
	old, err := getTx(s.db, in.MemoryIDs[0])
	if err != nil {
		return err
	}
	nw, err := getTx(s.db, in.MemoryIDs[1])
	if err != nil {
		return err
	}
	why, err := s.precedes(nw, old)
	if err != nil {
		return err
	}
	if why != "" {
		return fmt.Errorf("%w: %s", ErrProtected, why)
	}
	return nil
}
