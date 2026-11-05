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
	// Split cuts a long memory into one memory per claim (S4-9, split.go). The parts go to AppliesTo
	// (or stay where the memory is) and the original is superseded.
	Split []PlanPart `json:"split,omitempty"`
}

// PlanPart is one claim of a split: a title, its own topic and its body (≤ MaxClaimRunes).
type PlanPart struct {
	Title string `json:"title"`
	Topic string `json:"topic,omitempty"`
	Body  string `json:"body"`
}

// PlanResult is what happened to one item.
type PlanResult struct {
	ID       string   `json:"id"`
	Action   string   `json:"action"` // place(자리·주제) 또는 supersede
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Topic    string   `json:"topic,omitempty"`
	By       string   `json:"by,omitempty"`
	LedgerID string   `json:"ledgerId,omitempty"`
	Parts    []string `json:"parts,omitempty"` // split: 새로 만든 기억
	Skip     string   `json:"skip,omitempty"`
	Basis    string   `json:"basis,omitempty"`
}

// PlanReport is the result of a plan: applied (or, in a dry run, what would be), skipped and conflicts
// (what a person decides: a topic another memory holds, a supersession against source precedence).
type PlanReport struct {
	Applied   []PlanResult `json:"applied"`
	Skipped   []PlanResult `json:"skipped"`
	Conflicts []PlanResult `json:"conflicts"`
}

// planRun is what one run of a plan has decided so far, so a dry run sees what the real run would:
// the versions at plan start, the slots (applies-to, topic) taken in this run and where moved memories went.
type planRun struct {
	start   map[string]int    // 계획을 시작할 때 판 번호
	slots   map[string]string // 이번 실행에서 받은 자리(scope\x00topic) → 받은 기억
	moved   map[string]string // 이번 실행에서 옮긴 기억 → 새 자리
	leaving map[string]bool   // 대체가 실제로 일어날 기억(같은 자리를 함께 써도 된다)
}

func slotKey(scope, topic string) string { return scope + "\x00" + topic }

// ApplyPlan applies plan with the product map m. A dry run changes nothing and reports what the real run would.
func (s *Store) ApplyPlan(plan Plan, m place.Map, dry bool) (PlanReport, error) {
	rep := PlanReport{Applied: []PlanResult{}, Skipped: []PlanResult{}, Conflicts: []PlanResult{}}
	run := &planRun{start: map[string]int{}, slots: map[string]string{}, moved: map[string]string{}, leaving: map[string]bool{}}
	for _, it := range plan.Items {
		for _, id := range []string{it.ID, it.SupersededBy} {
			if mm, err := getTx(s.db, id); id != "" && err == nil {
				run.start[id] = mm.Version
			}
		}
	}
	// 대체가 실제로 일어날지는 대체 적용과 같은 판단(supersedeVerdict)으로 정한다: 막힐 대체의 옛 기억은 떠나지 않는다.
	for _, it := range plan.Items {
		if it.SupersededBy == "" {
			continue
		}
		if why, _, _, err := s.supersedeVerdict(it, run); err == nil && why == "" {
			run.leaving[it.ID] = true
		}
	}
	var errs []error
	var supersedes []PlanItem
	for _, it := range plan.Items {
		if it.SupersededBy != "" {
			supersedes = append(supersedes, it)
		}
		var r PlanResult
		var err error
		switch {
		case len(it.Split) > 0: // 나누기는 자리·주제도 함께 정한다: 원래 기억은 옮기지 않고 대체된다
			r, err = s.planSplit(it, m, run, dry)
		case it.AppliesTo != "" || it.Topic != "":
			r, err = s.planPlace(it, m, run, dry)
		default:
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.ID, err))
			continue
		}
		rep.add(r)
	}
	for _, it := range supersedes {
		r, err := s.planSupersede(it, run, dry)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.ID, err))
			continue
		}
		rep.add(r)
	}
	return rep, errors.Join(errs...)
}

// slotTaken is the memory that holds (scope, topic) for id in this run, or "": a live holder in the store
// that this run has not moved away or will not supersede, or a memory this run moved there.
func (s *Store) slotTaken(run *planRun, scope, topic, id string) (string, error) {
	key := slotKey(scope, topic)
	if other, ok := run.slots[key]; ok && other != id && !run.leaving[other] {
		return other, nil
	}
	rows, err := s.db.Query(`SELECT id FROM memories WHERE scope=? AND topic=? AND id<>? AND state IN ('candidate','confirmed') ORDER BY created_at, id`, scope, topic, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var other string
		if err := rows.Scan(&other); err != nil {
			return "", err
		}
		if run.leaving[other] {
			continue
		}
		if to, ok := run.moved[other]; ok && to != key {
			continue // 이번 실행에서 다른 자리로 옮겼다(미리 보기에서는 아직 여기 있다)
		}
		return other, nil
	}
	return "", rows.Err()
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
func (s *Store) planPlace(it PlanItem, m place.Map, run *planRun, dry bool) (PlanResult, error) {
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
	if topic != "" && !run.leaving[mem.ID] {
		other, err := s.slotTaken(run, scope, topic, mem.ID)
		if err != nil {
			return r, err
		}
		if other != "" {
			r.Skip = "topic taken by " + other
			return r, nil
		}
	}
	if topic != "" {
		run.slots[slotKey(scope, topic)] = mem.ID
	}
	run.moved[mem.ID] = slotKey(scope, topic)
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

// supersedeVerdict says why it.SupersededBy may not supersede it.ID now ("" when it may). ApplyPlan uses
// it both to know which memories leave their slot and to apply the supersession, so the two agree.
func (s *Store) supersedeVerdict(it PlanItem, run *planRun) (string, Memory, Memory, error) {
	old, err := getTx(s.db, it.ID)
	if err != nil {
		return "", old, Memory{}, err
	}
	nw, err := getTx(s.db, it.SupersededBy)
	if err != nil {
		return "", old, nw, err
	}
	start, ok := run.start[old.ID]
	if !ok {
		start = old.Version
	}
	switch {
	case old.State == StateStale && old.SupersededBy == nw.ID:
		return "already there", old, nw, nil
	case old.State != StateCandidate && old.State != StateConfirmed:
		return "not live: " + old.State, old, nw, nil
	case nw.State != StateCandidate && nw.State != StateConfirmed:
		return "replacement not live: " + nw.State, old, nw, nil
	case old.Pinned:
		return "pinned: a person replaces a core memory", old, nw, nil
	case it.Version != 0 && it.Version != start:
		// 같은 항목의 옮기기가 판을 올려도 걸리지 않게 계획을 시작할 때 판과 견준다.
		return fmt.Sprintf("changed: plan v%d, now v%d", it.Version, start), old, nw, nil
	}
	if why, err := s.precedes(nw, old); err != nil || why != "" {
		return why, old, nw, err
	}
	if err := s.notAgain(ProposalInput{Kind: ProposalSupersede, MemoryIDs: []string{old.ID, nw.ID}}); errors.Is(err, ErrAutoSkip) || errors.Is(err, ErrDuplicateProposal) {
		return "reverted before or already proposed", old, nw, nil
	} else if err != nil {
		return "", old, nw, err
	}
	return "", old, nw, nil
}

// planSupersede marks a memory stale, replaced by it.SupersededBy, when supersedeVerdict allows it.
func (s *Store) planSupersede(it PlanItem, run *planRun, dry bool) (PlanResult, error) {
	r := PlanResult{ID: it.ID, Action: "supersede", By: it.SupersededBy, Basis: it.Basis}
	why, old, nw, err := s.supersedeVerdict(it, run)
	if err != nil {
		return r, err
	}
	if why != "" {
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
	// 사용자 말인지는 출처 수로 정한다(시각이 0인 옛 출처도 사용자 말이다). 시각은 둘 다 사용자 말일 때 견주기만 한다.
	if po, err := protectedTx(s.db, old.ID); err != nil || !po {
		return "", err
	}
	if pn, err := protectedTx(s.db, nw.ID); err != nil {
		return "", err
	} else if !pn {
		return "user quote: " + nw.ID + " (not the user's words) does not replace the user's words in " + old.ID, nil
	}
	qo, err := s.quoteAt(old.ID)
	if err != nil {
		return "", err
	}
	qn, err := s.quoteAt(nw.ID)
	if err != nil {
		return "", err
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
