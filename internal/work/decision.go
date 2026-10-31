package work

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/onidot-labs/openorbit/internal/memory"
)

// Decision cards (orbit.decision 1판, 설계 · Studio 4차 「결정 카드 계약」). A card
// is what an agent asks the user: a question tool, a permission, an ops
// approval or a plugin decision. Cards live in work.db as ledger events, so a
// rebuild gives the same cards and decision_changes shares the work cursor.

// Event kinds of decision cards.
const (
	KindDecisionOpen     = "decision.open"
	KindDecisionAnswer   = "decision.answer"
	KindDecisionWithdraw = "decision.withdraw"
)

// Card states. expired is derived when reading: an open card past expiresAt.
const (
	DecisionOpenState        = "open"
	DecisionAnswered         = "answered"
	DecisionWithdrawn        = "withdrawn"
	DecisionExpired          = "expired"
	DecisionQuestionSource   = "question"
	DecisionPermissionSource = "permission"
	DecisionOpsSource        = "ops"
	DecisionPluginSource     = "plugin"
)

// Owners pass the answer back to the live session (계약 owner).
const (
	OwnerOrbitPi = "orbit-pi" // orbit-pi가 띄운 세션·워크플로·오케스트레이션 단계
	OwnerHuman   = "human"    // 사람이 직접 연 세션. 답을 넘길 수 없고 그 앱에서 답한다
	OwnerStudio  = "studio"   // Orbit Studio 플러그인이 연 카드
)

var (
	DecisionStates  = []string{DecisionOpenState, DecisionAnswered, DecisionWithdrawn, DecisionExpired}
	DecisionSources = []string{DecisionQuestionSource, DecisionPermissionSource, DecisionOpsSource, DecisionPluginSource}
	DecisionOwners  = []string{OwnerOrbitPi, OwnerHuman, OwnerStudio}
	// AnswerBy is where an answer came from.
	AnswerBy = []string{"studio", "telegram", "terminal", "host", "orbit-pi"}
)

// Text limits of a card.
const (
	decisionTextMax      = 2000
	decisionDetailMax    = 1000
	decisionContextMax   = 4000
	decisionLabelMax     = 200
	DecisionLimitMax     = 200
	decisionLimitDef     = 50
	DecisionQuestionsMax = 10
	DecisionOptionsMax   = 20
)

var (
	// ErrInvalidAnswer is an answer that does not fit the card's questions.
	ErrInvalidAnswer = errors.New("invalid answer")
	// ErrCannotRelay is an answer to a card whose session nobody can resume
	// (owner human): answer in the app that asked.
	ErrCannotRelay = errors.New("cannot relay")
)

// DecisionConflict is a write to a card that is no longer open. Current is the
// card as it is, so the late side can show who answered first.
type DecisionConflict struct{ Current Decision }

func (c *DecisionConflict) Error() string {
	return "decision " + c.Current.ID + " is " + c.Current.State
}

// DecisionOption is one choice.
type DecisionOption struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

// DecisionQuestion is one question of a card.
type DecisionQuestion struct {
	Text        string           `json:"text"`
	Header      string           `json:"header,omitempty"`
	MultiSelect bool             `json:"multiSelect"`
	Options     []DecisionOption `json:"options"`
	Recommended string           `json:"recommended,omitempty"`
	Reason      string           `json:"reason,omitempty"`
}

// DecisionLink is a source of the card: work·session·step, an Atlas page or
// issue, a PR, a memory or a URL.
type DecisionLink struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label,omitempty"`
}

// DecisionWork ties a card to a session (and the step in progress) and to the
// orbit.work wait key, so the map's 「답 기다림」 and the hook's close meet.
type DecisionWork struct {
	Host      string `json:"host,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	WorkID    string `json:"workId,omitempty"`
	StepID    string `json:"stepId,omitempty"`
	WaitKey   string `json:"waitKey,omitempty"`
}

// DecisionBinding is what a card is about (예: 운영 감시 PR 머리 커밋). When it
// changes, the opener withdraws the card.
type DecisionBinding struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// DecisionAnswerItem answers one question: chosen options or written text.
type DecisionAnswerItem struct {
	OptionIDs []string `json:"optionIds,omitempty"`
	Text      string   `json:"text,omitempty"`
}

// DecisionAnswer is the answer of an answered card.
type DecisionAnswer struct {
	Answers []DecisionAnswerItem `json:"answers"`
	By      string               `json:"by"`
	At      string               `json:"at"`
}

// Decision is a card as orbit.decision gives it.
type Decision struct {
	ID          string             `json:"id"`
	State       string             `json:"state"`
	Source      string             `json:"source"`
	Owner       string             `json:"owner"`
	Questions   []DecisionQuestion `json:"questions"`
	Context     string             `json:"context,omitempty"`
	Links       []DecisionLink     `json:"links"`
	Work        *DecisionWork      `json:"work,omitempty"`
	Binding     *DecisionBinding   `json:"binding,omitempty"`
	CreatedAt   string             `json:"createdAt"`
	UpdatedAt   string             `json:"updatedAt"`
	ExpiresAt   string             `json:"expiresAt,omitempty"`
	ClosedAt    string             `json:"closedAt,omitempty"`
	CloseReason string             `json:"closeReason,omitempty"`
	Answer      *DecisionAnswer    `json:"answer,omitempty"`
}

// DecisionInput is decision_open's card and the payload of decision.open.
// Prompt is the host turn the question was asked in (훅만): a late open of an
// ended turn is kept withdrawn.
type DecisionInput struct {
	ID        string             `json:"id"`
	Source    string             `json:"source"`
	Owner     string             `json:"owner"`
	Questions []DecisionQuestion `json:"questions"`
	Context   string             `json:"context,omitempty"`
	Links     []DecisionLink     `json:"links,omitempty"`
	Work      *DecisionWork      `json:"work,omitempty"`
	Binding   *DecisionBinding   `json:"binding,omitempty"`
	ExpiresAt int64              `json:"expiresAt,omitempty"` // 밀리초
	Prompt    string             `json:"prompt,omitempty"`
}

// DecisionAnswerEvent is the payload of decision.answer.
type DecisionAnswerEvent struct {
	ID      string               `json:"id"`
	Answers []DecisionAnswerItem `json:"answers"`
	By      string               `json:"by"`
}

// DecisionWithdrawEvent is the payload of decision.withdraw.
type DecisionWithdrawEvent struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

// decisionBody is the stored part a card does not filter on.
type decisionBody struct {
	Questions []DecisionQuestion `json:"questions"`
	Context   string             `json:"context,omitempty"`
	Links     []DecisionLink     `json:"links,omitempty"`
}

// NewDecisionID is a fresh card id.
func NewDecisionID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "d-" + hex.EncodeToString(b)
}

// CleanDecision applies the text rules to a card: secrets masked, lengths
// clipped, option ids filled (1부터 센 위치) when the opener gave none.
func CleanDecision(in DecisionInput) DecisionInput {
	text := func(s string, n int) string { return clip(memory.MaskSecrets(strings.TrimSpace(s)), n) }
	qs := make([]DecisionQuestion, 0, len(in.Questions))
	for _, q := range in.Questions {
		c := DecisionQuestion{Text: text(q.Text, decisionTextMax), Header: Clean(q.Header, decisionLabelMax), MultiSelect: q.MultiSelect,
			Recommended: q.Recommended, Reason: text(q.Reason, decisionDetailMax), Options: []DecisionOption{}}
		for i, o := range q.Options {
			id := strings.TrimSpace(o.ID)
			if id == "" {
				id = itoa(i + 1)
			}
			c.Options = append(c.Options, DecisionOption{ID: id, Label: Clean(o.Label, decisionLabelMax), Detail: text(o.Detail, decisionDetailMax)})
		}
		qs = append(qs, c)
	}
	in.Questions = qs
	in.Context = text(in.Context, decisionContextMax)
	for i := range in.Links {
		in.Links[i].Label = Clean(in.Links[i].Label, decisionLabelMax)
	}
	return in
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// ---- 원장 적용 ----

// decisionEvent applies a decision event. It runs before the session rules:
// a card without a session (운영 감시) has no host or session.
func (r *reducer) decisionEvent() error {
	switch r.e.Kind {
	case KindDecisionOpen:
		return r.decisionOpen()
	case KindDecisionAnswer:
		var p DecisionAnswerEvent
		if err := r.payload(&p); err != nil || p.ID == "" {
			return err
		}
		b, _ := json.Marshal(DecisionAnswer{Answers: p.Answers, By: p.By})
		return r.closeDecision(p.ID, DecisionAnswered, string(b), p.By, "")
	case KindDecisionWithdraw:
		var p DecisionWithdrawEvent
		if err := r.payload(&p); err != nil || p.ID == "" {
			return err
		}
		return r.closeDecision(p.ID, DecisionWithdrawn, "", "", clip(p.Reason, 80))
	}
	return nil
}

func (r *reducer) decisionOpen() error {
	var p DecisionInput
	if err := r.payload(&p); err != nil || p.ID == "" {
		return err
	}
	body, _ := json.Marshal(decisionBody{Questions: p.Questions, Context: p.Context, Links: p.Links})
	w := DecisionWork{}
	if p.Work != nil {
		w = *p.Work
	}
	state, reason := DecisionOpenState, ""
	if w.Host != "" && w.SessionID != "" {
		if w.WorkID == "" && w.StepID == "" {
			// 질문이 나온 때 진행 중이던 단계에 묶는다(비동기 훅이 늦게 와도 그 시각 기준)
			if steps, err := r.activeStepsAt(w.Host, w.SessionID); err == nil && len(steps) > 0 {
				w.StepID, w.WorkID = steps[0].id, steps[0].work
			}
		}
		var ended int64
		_ = r.tx.QueryRow(`SELECT ended_at FROM sessions WHERE host=? AND id=?`, w.Host, w.SessionID).Scan(&ended)
		if ended > 0 {
			state, reason = DecisionWithdrawn, "session_ended"
		} else if p.Prompt != "" && r.turnEndedOf(w.Host, w.SessionID, p.Prompt) {
			state, reason = DecisionWithdrawn, "turn_ended"
		}
	}
	closed := int64(0)
	if state != DecisionOpenState {
		closed = r.e.At
	}
	var bk, br string
	if p.Binding != nil {
		bk, br = p.Binding.Kind, p.Binding.Ref
	}
	// 사후(답·거절)가 먼저 와서 자리만 있는 카드는 내용만 채우고 상태는 그대로 둔다
	res, err := r.tx.Exec(`INSERT INTO decisions(id,state,source,owner,host,session,work_id,step_id,wait_key,binding_kind,binding_ref,body,
		created_at,updated_at,closed_at,expires_at,close_reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET source=excluded.source, owner=excluded.owner, host=excluded.host, session=excluded.session,
		work_id=excluded.work_id, step_id=excluded.step_id, wait_key=excluded.wait_key, binding_kind=excluded.binding_kind,
		binding_ref=excluded.binding_ref, body=excluded.body, created_at=MIN(decisions.created_at, excluded.created_at), expires_at=excluded.expires_at
		WHERE decisions.body='{}'`,
		p.ID, state, p.Source, p.Owner, w.Host, w.SessionID, w.WorkID, w.StepID, w.WaitKey, bk, br, string(body),
		r.e.At, r.e.At, closed, p.ExpiresAt, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.touchDecision(p.ID)
	}
	return nil
}

func (r *reducer) turnEndedOf(host, session, prompt string) bool {
	var n int
	_ = r.tx.QueryRow(`SELECT COUNT(*) FROM ended_turns WHERE host=? AND session=? AND prompt=?`, host, session, prompt).Scan(&n)
	return n > 0
}

// closeDecision answers or withdraws an open card. A card not yet opened (the
// async post-call came first) gets a placeholder in that state, which the
// late open fills in.
func (r *reducer) closeDecision(id, state, answer, by, reason string) error {
	res, err := r.tx.Exec(`UPDATE decisions SET state=?, answer=?, answered_by=?, close_reason=?, closed_at=?, updated_at=? WHERE id=? AND state='open'`,
		state, answer, by, reason, r.e.At, r.e.At, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.touchDecision(id)
		return nil
	}
	res, err = r.tx.Exec(`INSERT OR IGNORE INTO decisions(id,state,host,session,answer,answered_by,close_reason,created_at,updated_at,closed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, id, state, r.e.Host, r.e.Session, answer, by, reason, r.e.At, r.e.At, r.e.At)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.touchDecision(id)
	}
	return nil
}

// withdrawSessionDecisions withdraws the open cards of this event's session
// (턴 끝·세션 끝, 설계 「정리」).
func (r *reducer) withdrawSessionDecisions(reason string) error {
	rows, err := r.tx.Query(`SELECT id FROM decisions WHERE host=? AND session=? AND state='open' ORDER BY id`, r.e.Host, r.e.Session)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := r.closeDecision(id, DecisionWithdrawn, "", "", reason); err != nil {
			return err
		}
	}
	return nil
}

func (r *reducer) touchDecision(id string) { r.decisions[id] = true }

// ---- 쓰기 ----

// OpenDecision records a new card (decision_open). An empty id gets a new one.
func (s *Store) OpenDecision(in DecisionInput) (Decision, int64, error) {
	in = CleanDecision(in)
	if in.ID == "" {
		in.ID = NewDecisionID()
	}
	in.Prompt = ""
	e := NewEvent(KindDecisionOpen, "", "", s.nowMs(), in)
	e.Key = "decision.open:" + in.ID
	res, err := s.Append(e)
	if err != nil {
		return Decision{}, 0, err
	}
	d, seq, err := s.GetDecision(in.ID)
	if err == nil && res.Skipped {
		return d, seq, &DecisionConflict{Current: d} // 같은 id가 이미 있다
	}
	return d, seq, err
}

// AnswerDecision answers an open card (decision_answer): compare and swap,
// the first answer wins and a late one gets DecisionConflict.
func (s *Store) AnswerDecision(id string, answers []DecisionAnswerItem, by string) (Decision, int64, error) {
	e := NewEvent(KindDecisionAnswer, "", "", s.nowMs(), DecisionAnswerEvent{ID: id, Answers: answers, By: by})
	return s.writeDecision(id, e, func(d Decision) error {
		if d.Owner == OwnerHuman {
			return ErrCannotRelay
		}
		return checkAnswer(d.Questions, answers)
	})
}

// WithdrawDecision withdraws an open card (decision_withdraw).
func (s *Store) WithdrawDecision(id, reason string) (Decision, int64, error) {
	e := NewEvent(KindDecisionWithdraw, "", "", s.nowMs(), DecisionWithdrawEvent{ID: id, Reason: Clean(reason, 80)})
	return s.writeDecision(id, e, nil)
}

// writeDecision checks the card in the same transaction it appends the event,
// so two writers cannot both find it open.
func (s *Store) writeDecision(id string, e Event, check func(Decision) error) (Decision, int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Decision{}, 0, err
	}
	defer tx.Rollback()
	d, err := s.decisionIn(tx, id)
	if err != nil {
		return Decision{}, 0, err
	}
	if d.State != DecisionOpenState {
		return d, 0, &DecisionConflict{Current: d}
	}
	if check != nil {
		if err := check(d); err != nil {
			return d, 0, err
		}
	}
	// 같은 카드의 이벤트는 그 카드의 세션에 둔다(세션 사건 쪽과 정리 규칙이 같은 세션을 본다)
	if d.Work != nil {
		e.Host, e.Session = d.Work.Host, d.Work.SessionID
	}
	if _, err := s.appendTx(tx, e); err != nil {
		return Decision{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return Decision{}, 0, err
	}
	return s.GetDecision(id)
}

func checkAnswer(qs []DecisionQuestion, answers []DecisionAnswerItem) error {
	if len(answers) != len(qs) {
		return ErrInvalidAnswer
	}
	for i, a := range answers {
		q := qs[i]
		if len(a.OptionIDs) == 0 && strings.TrimSpace(a.Text) == "" {
			return ErrInvalidAnswer
		}
		if !q.MultiSelect && len(a.OptionIDs) > 1 {
			return ErrInvalidAnswer
		}
		for _, id := range a.OptionIDs {
			found := false
			for _, o := range q.Options {
				if o.ID == id {
					found = true
				}
			}
			if !found {
				return ErrInvalidAnswer
			}
		}
	}
	return nil
}

// ---- 읽기 ----

const decisionCols = `id,state,source,owner,host,session,work_id,step_id,wait_key,binding_kind,binding_ref,body,answer,answered_by,close_reason,created_at,updated_at,closed_at,expires_at`

func (s *Store) decisionIn(q querier, id string) (Decision, error) {
	d, err := s.scanDecision(q.QueryRow(`SELECT `+decisionCols+` FROM decisions WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Decision{}, ErrNotFound
	}
	return d, err
}

func (s *Store) scanDecision(sc interface{ Scan(...any) error }) (Decision, error) {
	var d Decision
	var host, session, workID, stepID, waitKey, bk, br, body, answer, by string
	var created, updated, closed, expires int64
	if err := sc.Scan(&d.ID, &d.State, &d.Source, &d.Owner, &host, &session, &workID, &stepID, &waitKey, &bk, &br, &body, &answer, &by,
		&d.CloseReason, &created, &updated, &closed, &expires); err != nil {
		return Decision{}, err
	}
	var b decisionBody
	_ = json.Unmarshal([]byte(body), &b)
	d.Questions, d.Context, d.Links = b.Questions, b.Context, b.Links
	if d.Questions == nil {
		d.Questions = []DecisionQuestion{}
	}
	if d.Links == nil {
		d.Links = []DecisionLink{}
	}
	if host != "" || session != "" || workID != "" || stepID != "" || waitKey != "" {
		d.Work = &DecisionWork{Host: host, SessionID: session, WorkID: workID, StepID: stepID, WaitKey: waitKey}
	}
	if bk != "" || br != "" {
		d.Binding = &DecisionBinding{Kind: bk, Ref: br}
	}
	d.CreatedAt, d.UpdatedAt, d.ClosedAt, d.ExpiresAt = Stamp(created), Stamp(updated), Stamp(closed), Stamp(expires)
	if d.State == DecisionAnswered && answer != "" {
		var a DecisionAnswer
		if json.Unmarshal([]byte(answer), &a) == nil {
			if a.Answers == nil {
				a.Answers = []DecisionAnswerItem{}
			}
			a.By, a.At = by, Stamp(closed)
			d.Answer = &a
		}
	}
	if d.State == DecisionOpenState && expires > 0 && s.nowMs() >= expires {
		d.State = DecisionExpired
	}
	return d, nil
}

// GetDecision reads one card and the ledger position it was read at.
func (s *Store) GetDecision(id string) (Decision, int64, error) {
	seq, err := s.LastSeq()
	if err != nil {
		return Decision{}, 0, err
	}
	d, err := s.decisionIn(s.db, id)
	return d, seq, err
}

// DecisionFilter is decision_list's arguments. Empty States is open.
type DecisionFilter struct {
	States    []string
	Sources   []string
	SessionID string
	WorkID    string
	Since     int64 // 밀리초. 이 뒤에 바뀐 카드만
	Limit     int
}

// DecisionPage is a list of cards, the newest first.
type DecisionPage struct {
	Seq       int64
	Items     []Decision
	Total     int
	Truncated bool
}

// ListDecisions lists cards by state (expired is derived), source, session,
// work and change time.
func (s *Store) ListDecisions(f DecisionFilter) (DecisionPage, error) {
	seq, err := s.LastSeq()
	if err != nil {
		return DecisionPage{}, err
	}
	states := map[string]bool{}
	for _, st := range f.States {
		states[st] = true
	}
	if len(states) == 0 {
		states[DecisionOpenState] = true
	}
	cond, args := []string{"body<>'{}'"}, []any{}
	// expired는 저장 상태 open에서 나온다
	var stored []string
	for st := range states {
		if st == DecisionExpired {
			st = DecisionOpenState
		}
		stored = append(stored, st)
	}
	cond = append(cond, "state IN ("+placeholders(len(stored))+")")
	for _, st := range stored {
		args = append(args, st)
	}
	if len(f.Sources) > 0 {
		cond = append(cond, "source IN ("+placeholders(len(f.Sources))+")")
		for _, x := range f.Sources {
			args = append(args, x)
		}
	}
	if f.SessionID != "" {
		cond, args = append(cond, "session=?"), append(args, f.SessionID)
	}
	if f.WorkID != "" {
		cond, args = append(cond, "work_id=?"), append(args, f.WorkID)
	}
	if f.Since > 0 {
		cond, args = append(cond, "updated_at>?"), append(args, f.Since)
	}
	rows, err := s.db.Query(`SELECT `+decisionCols+` FROM decisions WHERE `+strings.Join(cond, " AND ")+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return DecisionPage{}, err
	}
	defer rows.Close()
	limit := f.Limit
	if limit <= 0 {
		limit = decisionLimitDef
	}
	if limit > DecisionLimitMax {
		limit = DecisionLimitMax
	}
	page := DecisionPage{Seq: seq, Items: []Decision{}}
	for rows.Next() {
		d, err := s.scanDecision(rows)
		if err != nil {
			return DecisionPage{}, err
		}
		if !states[d.State] {
			continue
		}
		page.Total++
		if len(page.Items) < limit {
			page.Items = append(page.Items, d)
		} else {
			page.Truncated = true
		}
	}
	return page, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// DecisionChangeSet is decision_changes' result: cards changed after the cursor.
type DecisionChangeSet struct {
	Seq   int64
	Reset bool
	More  bool
	IDs   []string
}

// DecisionChanges gives the cards changed after the cursor (the same cursor as
// work_changes), waiting up to wait when nothing changed.
func (s *Store) DecisionChanges(ctx context.Context, after string, wait time.Duration) (DecisionChangeSet, error) {
	var out DecisionChangeSet
	reset, last, err := s.waitChanges(ctx, after, wait, func(from int64) (bool, error) {
		// 끝 위치를 먼저 읽는다: 그 뒤에 들어온 카드 변경은 다음 부름이 받는다
		last, err := s.LastSeq()
		if err != nil {
			return false, err
		}
		rows, err := s.db.Query(`SELECT seq, decision_id FROM changes WHERE seq>? AND seq<=? AND decision_id<>'' ORDER BY seq, decision_id LIMIT ?`, from, last, ChangesMax*4)
		if err != nil {
			return false, err
		}
		defer rows.Close()
		out = DecisionChangeSet{Seq: last, IDs: []string{}}
		seen := map[string]bool{}
		var cur, complete int64 = -1, from
		n := 0
		for rows.Next() {
			var seq int64
			var id string
			if err := rows.Scan(&seq, &id); err != nil {
				return false, err
			}
			n++
			if seq != cur {
				if cur >= 0 {
					complete = cur
				}
				// 사건 하나의 바뀐 것은 나누지 않는다
				if len(seen) >= ChangesMax {
					out.Seq, out.More = complete, true
					return true, nil
				}
				cur = seq
			}
			if !seen[id] {
				seen[id] = true
				out.IDs = append(out.IDs, id)
			}
		}
		if err := rows.Err(); err != nil {
			return false, err
		}
		if n == ChangesMax*4 {
			out.Seq, out.More = cur, true
		}
		return len(out.IDs) > 0, nil
	})
	if err != nil {
		return DecisionChangeSet{}, err
	}
	if reset {
		return DecisionChangeSet{Seq: last, Reset: true}, nil
	}
	return out, nil
}
