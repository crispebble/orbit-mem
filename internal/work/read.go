package work

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// ReadOptions are settings that change derived values at read time.
type ReadOptions struct {
	StaleAfter time.Duration // 0이면 응답 없음(stale)을 계산하지 않는다(설계 O5)
}

// ErrNotFound is a missing work or session.
var ErrNotFound = errors.New("없습니다")

type workRow struct {
	id, host, session, surface, listKind, listID, cwd, scope, label, parentWork, parentStep, title string
	titleAt, openedAt, updatedAt, closedAt, updatedSeq                                             int64
}

const workCols = `id,host,session,surface,list_kind,list_id,cwd,project_scope,project_label,parent_work,parent_step,title,title_at,opened_at,updated_at,closed_at,updated_seq`

func scanWork(sc interface{ Scan(...any) error }) (workRow, error) {
	var w workRow
	err := sc.Scan(&w.id, &w.host, &w.session, &w.surface, &w.listKind, &w.listID, &w.cwd, &w.scope, &w.label, &w.parentWork, &w.parentStep,
		&w.title, &w.titleAt, &w.openedAt, &w.updatedAt, &w.closedAt, &w.updatedSeq)
	return w, err
}

type stepData struct {
	id, work, native, title, active, status, owner, declared, lastHost, lastSession string
	ord                                                                             int
	declaredAt, interruptedAt, startedAt, finishedAt                                int64
}

type runData struct {
	id, kind, host, session, runHost, agent, role, child, parent, step, link, cwd, repo, branch, model, state, endReason string
	background                                                                                                           bool
	startedAt, lastSeen, finishedAt, seq                                                                                 int64
}

type evData struct {
	id, kind, host, session, step, run, result, summary, repo, sha, transcript, files string
	at, seq                                                                           int64
}

type sessData struct {
	host, id, surface, cwd, repo, branch, scope, label, model, parentHost, parentSession, endReason, endDetail string
	startedAt, lastSeen, endedAt, updatedSeq                                                                   int64
	turnOpen                                                                                                   bool
	openWaits                                                                                                  int
	waitSince                                                                                                  int64
}

// reader loads rows and derives the contract documents.
type reader struct {
	q        querier
	opt      ReadOptions
	now      int64
	sessions map[[2]string]*sessData
}

type querier interface {
	Query(q string, args ...any) (*sql.Rows, error)
	QueryRow(q string, args ...any) *sql.Row
}

func (s *Store) reader(opt ReadOptions) *reader {
	return &reader{q: s.db, opt: opt, now: s.nowMs(), sessions: map[[2]string]*sessData{}}
}

func (rd *reader) session(host, id string) *sessData {
	k := [2]string{host, id}
	if sd, ok := rd.sessions[k]; ok {
		return sd
	}
	var sd sessData
	var turnOpen int
	err := rd.q.QueryRow(`SELECT host,id,surface,cwd,repo,branch,project_scope,project_label,model,parent_host,parent_session,end_reason,end_detail,
		started_at,last_seen,ended_at,updated_seq,turn_open FROM sessions WHERE host=? AND id=?`, host, id).Scan(
		&sd.host, &sd.id, &sd.surface, &sd.cwd, &sd.repo, &sd.branch, &sd.scope, &sd.label, &sd.model, &sd.parentHost, &sd.parentSession,
		&sd.endReason, &sd.endDetail, &sd.startedAt, &sd.lastSeen, &sd.endedAt, &sd.updatedSeq, &turnOpen)
	if err != nil {
		rd.sessions[k] = nil
		return nil
	}
	sd.turnOpen = turnOpen == 1
	var since sql.NullInt64
	_ = rd.q.QueryRow(`SELECT COUNT(*), MIN(opened_at) FROM waits WHERE host=? AND session=? AND resolved=0`, host, id).Scan(&sd.openWaits, &since)
	sd.waitSince = since.Int64
	rd.sessions[k] = &sd
	return &sd
}

// state is a session's derived state (계약 세션 상태).
func (rd *reader) state(sd *sessData) string {
	switch {
	case sd == nil:
		return "idle"
	case sd.endedAt > 0:
		return "ended"
	case sd.openWaits > 0:
		return "waiting_user"
	case sd.turnOpen && rd.stale(sd):
		return "stale"
	case sd.turnOpen:
		return "running"
	}
	return "idle"
}

func (rd *reader) stale(sd *sessData) bool {
	return rd.opt.StaleAfter > 0 && rd.now-sd.lastSeen > rd.opt.StaleAfter.Milliseconds()
}

func (rd *reader) works(cond string, args ...any) ([]workRow, error) {
	rows, err := rd.q.Query(`SELECT `+workCols+` FROM works WHERE `+cond, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workRow
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (rd *reader) steps(work string) ([]stepData, error) {
	rows, err := rd.q.Query(`SELECT id,work_id,native,title,active_title,host_status,owner,declared,last_host,last_session,ord,declared_at,interrupted_at,started_at,finished_at
		FROM steps WHERE work_id=? ORDER BY ord, id`, work)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stepData
	for rows.Next() {
		var s stepData
		if err := rows.Scan(&s.id, &s.work, &s.native, &s.title, &s.active, &s.status, &s.owner, &s.declared, &s.lastHost, &s.lastSession,
			&s.ord, &s.declaredAt, &s.interruptedAt, &s.startedAt, &s.finishedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (rd *reader) edges(work string) (map[string][]After, error) {
	rows, err := rd.q.Query(`SELECT e.step_id, e.after_id, e.source FROM edges e JOIN steps s ON s.id=e.step_id WHERE s.work_id=? ORDER BY e.step_id, e.after_id`, work)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]After{}
	for rows.Next() {
		var step string
		var a After
		if err := rows.Scan(&step, &a.StepID, &a.Source); err != nil {
			return nil, err
		}
		out[step] = append(out[step], a)
	}
	return out, rows.Err()
}

const runCols = `id,kind,host,session,run_host,agent,role,child_session,parent_session,step_id,link,cwd,repo,branch,model,state,end_reason,background,started_at,last_seen,finished_at,seq`

func (rd *reader) runs(cond string, args ...any) ([]runData, error) {
	rows, err := rd.q.Query(`SELECT `+runCols+` FROM runs WHERE `+cond+` ORDER BY started_at, seq, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runData
	for rows.Next() {
		var x runData
		var bg int
		if err := rows.Scan(&x.id, &x.kind, &x.host, &x.session, &x.runHost, &x.agent, &x.role, &x.child, &x.parent, &x.step, &x.link, &x.cwd, &x.repo, &x.branch,
			&x.model, &x.state, &x.endReason, &bg, &x.startedAt, &x.lastSeen, &x.finishedAt, &x.seq); err != nil {
			return nil, err
		}
		x.background = bg == 1
		out = append(out, x)
	}
	return out, rows.Err()
}

func (rd *reader) evidence(cond string, args ...any) ([]evData, error) {
	rows, err := rd.q.Query(`SELECT id,kind,host,session,step_id,run_id,result,summary,repo,sha,transcript,files,at,seq FROM evidence WHERE `+cond+` ORDER BY at, seq, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evData
	for rows.Next() {
		var e evData
		if err := rows.Scan(&e.id, &e.kind, &e.host, &e.session, &e.step, &e.run, &e.result, &e.summary, &e.repo, &e.sha, &e.transcript, &e.files, &e.at, &e.seq); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (rd *reader) children(stepIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(stepIDs) == 0 {
		return out, nil
	}
	ph := strings.Repeat(",?", len(stepIDs))[1:]
	args := make([]any, len(stepIDs))
	for i, s := range stepIDs {
		args[i] = s
	}
	rows, err := rd.q.Query(`SELECT parent_step, id FROM works WHERE parent_step IN (`+ph+`) ORDER BY opened_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var step, id string
		_ = rows.Scan(&step, &id)
		out[step] = append(out[step], id)
	}
	return out, rows.Err()
}

func runDoc(x runData) Run {
	r := Run{ID: x.id, Kind: x.kind, Host: x.host, Role: x.role, StepID: x.step, Link: x.link, Background: x.background, Cwd: x.cwd, Repo: x.repo, Branch: x.branch,
		State: x.state, StartedAt: Stamp(x.startedAt), LastSeenAt: Stamp(x.lastSeen), FinishedAt: Stamp(x.finishedAt)}
	if x.runHost != "" {
		r.Host = x.runHost
	}
	if x.kind == "session" {
		r.SessionID, r.ParentSessionID = x.child, x.parent
	} else {
		r.AgentID, r.SessionID, r.ParentSessionID = x.agent, x.session, x.session
		if strings.HasPrefix(x.agent, "tool:") {
			r.AgentID = ""
		}
	}
	if x.state != "running" {
		r.EndReason = x.endReason
		if r.EndReason == "" {
			r.EndReason = "unknown"
		}
	}
	if r.StartedAt == "" {
		r.StartedAt = Stamp(x.finishedAt)
	}
	return r
}

func evDoc(e evData) Evidence {
	d := Evidence{ID: e.id, Kind: e.kind, StepID: e.step, RunID: e.run, At: Stamp(e.at), Result: e.result, Summary: e.summary}
	if e.repo != "" || e.sha != "" || e.transcript != "" {
		d.Ref = &EvidenceRef{Repo: e.repo, SHA: e.sha, Transcript: e.transcript}
	}
	if e.kind == "commit" {
		_ = json.Unmarshal([]byte(e.files), &d.Files)
		if len(d.Files) == 0 {
			d.Files = nil
		}
	}
	return d
}

// graph is one work with everything needed to derive its documents.
type graph struct {
	w     workRow
	steps []stepData
	edges map[string][]After
	runs  []runData
	ev    []evData
	kids  map[string][]string
}

func (rd *reader) load(w workRow) (*graph, error) {
	g := &graph{w: w}
	var err error
	if g.steps, err = rd.steps(w.id); err != nil {
		return nil, err
	}
	if g.edges, err = rd.edges(w.id); err != nil {
		return nil, err
	}
	ids := make([]string, len(g.steps))
	for i, s := range g.steps {
		ids[i] = s.id
	}
	if g.kids, err = rd.children(ids); err != nil {
		return nil, err
	}
	if g.runs, err = rd.runs(`step_id IN (SELECT id FROM steps WHERE work_id=?)`, w.id); err != nil {
		return nil, err
	}
	if g.ev, err = rd.evidence(`step_id IN (SELECT id FROM steps WHERE work_id=?)`, w.id); err != nil {
		return nil, err
	}
	return g, nil
}

// derive computes steps' status, wave, block, verification and the work's head.
func (rd *reader) derive(g *graph) (WorkHead, []Step, VerificationCounts) {
	byID := map[string]*stepData{}
	for i := range g.steps {
		byID[g.steps[i].id] = &g.steps[i]
	}
	waves := map[string]int{}
	var wave func(id string, depth int) int
	wave = func(id string, depth int) int {
		if v, ok := waves[id]; ok {
			return v
		}
		best := 0
		if depth <= len(g.steps) { // 간선은 순환 없이 들어오지만 깨진 DB에서도 멈추지 않게
			for _, a := range g.edges[id] {
				if byID[a.StepID] != nil {
					if n := wave(a.StepID, depth+1) + 1; n > best {
						best = n
					}
				}
			}
		}
		waves[id] = best
		return best
	}
	latestRun := map[string]runData{}
	for _, x := range g.runs {
		latestRun[x.step] = x // 시작 순으로 읽었으니 마지막이 가장 최근
	}
	evByStep := map[string][]evData{}
	for _, e := range g.ev {
		evByStep[e.step] = append(evByStep[e.step], e)
	}
	head := rd.head(g)
	var vc VerificationCounts
	steps := make([]Step, 0, len(g.steps))
	for _, s := range g.steps {
		st := Step{ID: s.id, Title: s.title, ActiveTitle: s.active, Order: s.ord, Wave: wave(s.id, 0), After: nonNilAfter(g.edges[s.id]),
			StartedAt: Stamp(s.startedAt), FinishedAt: Stamp(s.finishedAt), EvidenceIDs: []string{}, ChildWorkIDs: nonNil(g.kids[s.id])}
		if s.native != "" {
			st.Native = &Native{Kind: g.w.listKind, ID: s.native}
		}
		switch s.status {
		case hostDone:
			st.Status = "done"
		case hostCancelled:
			st.Status = "cancelled"
		case hostActive:
			st.Status = "in_progress"
			if b := rd.block(s, latestRun[s.id], g.steps); b != nil {
				st.Status, st.Block = "blocked", b
			}
		default:
			st.Status = "ready"
			for _, a := range g.edges[s.id] {
				if p := byID[a.StepID]; p != nil && p.status != hostDone && p.status != hostCancelled {
					st.Status = "pending"
				}
			}
		}
		st.Assignee = rd.assignee(g, s, latestRun[s.id])
		st.Verification = verify(evByStep[s.id])
		for _, e := range evByStep[s.id] {
			st.EvidenceIDs = append(st.EvidenceIDs, e.id)
		}
		head.Counts.add(st.Status)
		switch st.Verification.State {
		case "pass":
			vc.Pass++
		case "fail":
			vc.Fail++
		case "needs_user":
			vc.NeedsUser++
		default:
			vc.None++
		}
		if st.Verification.Outdated {
			vc.Outdated++
		}
		steps = append(steps, st)
	}
	if head.TitleSource == "step" && len(steps) > 0 {
		head.Title = steps[0].Title
	}
	return head, steps, vc
}

func (rd *reader) head(g *graph) WorkHead {
	w := g.w
	h := WorkHead{ID: w.id, Title: w.title, TitleSource: "ai", TitleUpdatedAt: Stamp(w.titleAt), State: "open", Cwd: w.cwd,
		Origin:   Origin{Host: w.host, Surface: w.surface, SessionID: w.session, List: ListRef{Kind: w.listKind}},
		OpenedAt: Stamp(w.openedAt), UpdatedAt: Stamp(w.updatedAt), ClosedAt: Stamp(w.closedAt)}
	if w.listKind == ListClaudeTask {
		h.Origin.List.ID = w.listID
	}
	if w.titleAt == 0 {
		h.TitleSource, h.TitleUpdatedAt = "step", ""
	}
	if w.closedAt > 0 {
		h.State = "done"
	}
	if w.scope != "" {
		h.Project = &Project{Scope: w.scope, Label: w.label}
	}
	if w.parentWork != "" {
		h.Parent = &Parent{WorkID: w.parentWork, StepID: w.parentStep}
	}
	return h
}

// block picks the open block reason of an in-progress step. When several are
// open the first of this order shows (계약 README 「파생 규칙」).
func (rd *reader) block(s stepData, run runData, all []stepData) *Block {
	sd := rd.session(s.lastHost, s.lastSession)
	if sd != nil && sd.endedAt == 0 && sd.openWaits > 0 {
		return &Block{Reason: "waiting_user", Detail: "사용자 응답을 기다립니다", Since: Stamp(sd.waitSince)}
	}
	switch run.state {
	case "failed":
		return &Block{Reason: "run_failed", Detail: roleName(run.role) + " 실행이 실패로 끝났습니다", Since: Stamp(run.finishedAt)}
	case "lost":
		return &Block{Reason: "run_lost", Detail: roleName(run.role) + " 실행의 끝을 받지 못했습니다", Since: Stamp(run.finishedAt)}
	}
	if s.declared != "" {
		return &Block{Reason: "declared", Detail: s.declared, Since: Stamp(s.declaredAt)}
	}
	if s.interruptedAt > 0 {
		return &Block{Reason: "interrupted", Detail: "진행 중에 세션이 끝났습니다", Since: Stamp(s.interruptedAt)}
	}
	if sd != nil && sd.endedAt == 0 && sd.turnOpen && rd.stale(sd) {
		return &Block{Reason: "stale", Detail: "세션에 사건이 오래 없습니다", Since: Stamp(sd.lastSeen + rd.opt.StaleAfter.Milliseconds())}
	}
	return nil
}

func (rd *reader) assignee(g *graph, s stepData, run runData) *Assignee {
	host := s.lastHost
	if host == "" {
		host = g.w.host
	}
	switch {
	case run.id != "" && run.kind == "session":
		return &Assignee{Kind: "session", Host: run.runHost}
	case run.id != "":
		return &Assignee{Kind: "subagent", Role: run.role, Host: run.host, Model: run.model}
	case s.owner != "" && s.owner != "main":
		return &Assignee{Kind: "subagent", Role: s.owner, Host: host}
	case s.startedAt > 0 || s.owner == "main":
		a := &Assignee{Kind: "main", Host: host}
		if sd := rd.session(s.lastHost, s.lastSession); sd != nil {
			a.Model = sd.model
		}
		return a
	}
	return nil
}

// verify is the verification rule (설계 「증거와 검증 결과」).
func verify(evs []evData) Verification {
	var chosen *evData
	for _, kind := range []string{"verdict", "command"} {
		for i := range evs {
			if evs[i].kind == kind {
				chosen = &evs[i] // at·seq 순이라 마지막이 가장 최근
			}
		}
		if chosen != nil {
			break
		}
	}
	if chosen == nil {
		return Verification{State: "none"}
	}
	v := Verification{EvidenceID: chosen.id}
	switch chosen.result {
	case "pass", "ok":
		v.State = "pass"
	case "needs_user":
		v.State = "needs_user"
	default:
		v.State = "fail"
	}
	for _, e := range evs {
		if e.kind == "commit" && (e.at > chosen.at || e.at == chosen.at && e.seq > chosen.seq) {
			v.Outdated = true
		}
	}
	return v
}

func nonNilAfter(a []After) []After {
	if a == nil {
		return []After{}
	}
	sort.Slice(a, func(i, j int) bool { return a[i].StepID < a[j].StepID })
	return a
}

func (rd *reader) workDoc(g *graph) Work {
	head, steps, _ := rd.derive(g)
	w := Work{WorkHead: head, Steps: steps, Runs: []Run{}, Evidence: []Evidence{}}
	for _, x := range g.runs {
		w.Runs = append(w.Runs, runDoc(x))
	}
	for _, e := range g.ev {
		w.Evidence = append(w.Evidence, evDoc(e))
	}
	return w
}

func (rd *reader) summaryDoc(g *graph) WorkSummary {
	head, steps, vc := rd.derive(g)
	return WorkSummary{WorkHead: head, StepCount: len(steps), Verification: vc}
}

func (rd *reader) sessionDoc(sd *sessData) Session {
	s := Session{SessionID: sd.id, Host: sd.host, Surface: sd.surface, Cwd: sd.cwd, Repo: sd.repo, Branch: sd.branch, ParentSessionID: sd.parentSession,
		Model: sd.model, State: rd.state(sd), StartedAt: Stamp(sd.startedAt), LastSeenAt: Stamp(sd.lastSeen), EndedAt: Stamp(sd.endedAt),
		OpenWorkIDs: []string{}, ActiveStepIDs: []string{}}
	if s.Surface == "" {
		s.Surface = "unknown"
	}
	if sd.scope != "" {
		s.Project = &Project{Scope: sd.scope, Label: sd.label}
	}
	if sd.endedAt > 0 {
		s.EndReason, s.EndDetail = sd.endReason, sd.endDetail
		if s.EndReason == "" {
			s.EndReason = "unknown"
		}
	}
	if rows, err := rd.q.Query(`SELECT id FROM works WHERE host=? AND session=? AND closed_at=0 ORDER BY opened_at, id`, sd.host, sd.id); err == nil {
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			s.OpenWorkIDs = append(s.OpenWorkIDs, id)
		}
		rows.Close()
	}
	if rows, err := rd.q.Query(`SELECT id FROM steps WHERE last_host=? AND last_session=? AND host_status='in_progress' ORDER BY started_at, id`, sd.host, sd.id); err == nil {
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			s.ActiveStepIDs = append(s.ActiveStepIDs, id)
		}
		rows.Close()
	}
	return s
}
