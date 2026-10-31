package work

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// reducer applies one ledger event to the graph tables inside its transaction.
// Every rule here must give the same tables when the ledger is replayed, so it
// reads only the event and the tables, never the clock or the file system.
type reducer struct {
	tx        *sql.Tx
	e         Event
	replay    bool
	sub       int
	works     map[string]bool
	sessions  map[[2]string]bool
	decisions map[string]bool
	opened    []string
}

func newReducer(tx *sql.Tx, e Event, replay bool) *reducer {
	return &reducer{tx: tx, e: e, replay: replay, works: map[string]bool{}, sessions: map[[2]string]bool{}, decisions: map[string]bool{}}
}

func (r *reducer) exec(q string, args ...any) error {
	_, err := r.tx.Exec(q, args...)
	return err
}

func (r *reducer) payload(v any) error {
	if len(r.e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(r.e.Data, v)
}

func (r *reducer) apply() error {
	switch r.e.Kind {
	case KindDecisionOpen, KindDecisionAnswer, KindDecisionWithdraw:
		return r.decisionEvent()
	}
	if r.e.Host == "" || r.e.Session == "" {
		if r.e.Kind == KindTitle {
			return r.title()
		}
		return nil
	}
	if err := r.ensureSession(r.e.Host, r.e.Session); err != nil {
		return err
	}
	switch r.e.Kind {
	case KindSessionStart:
		return r.sessionStart()
	case KindTurnStart:
		return r.turnStart()
	case KindTurnEnd:
		return r.turnEnd()
	case KindSessionEnd:
		return r.sessionEnd()
	case KindTaskCreate:
		return r.taskCreate()
	case KindTaskUpdate:
		return r.taskUpdate()
	case KindTaskState:
		return r.taskState()
	case KindPlanSnapshot:
		return r.planSnapshot()
	case KindPlanOps:
		return r.planOps()
	case KindWaitOpen:
		return r.waitOpen()
	case KindToolDone:
		return r.toolDone()
	case KindRunStart:
		return r.runStart()
	case KindRunTool:
		return r.runTool()
	case KindRunStop:
		return r.runStop()
	case KindCommand, KindCommit:
		return r.evidence()
	case KindTitle:
		return r.title()
	}
	return nil // 모르는 종류는 원장에만 남긴다(새 orb가 쓴 사건을 옛 orb가 다시 적용할 때)
}

// flush stamps changed works and sessions with this event's position and
// records them for work_changes.
func (r *reducer) flush() error {
	ids := make([]string, 0, len(r.works))
	for id := range r.works {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := r.exec(`UPDATE works SET updated_seq=?, updated_at=MAX(updated_at,?) WHERE id=?`, r.e.Seq, r.e.At, id); err != nil {
			return err
		}
		if err := r.exec(`INSERT INTO changes(seq,work_id) VALUES(?,?)`, r.e.Seq, id); err != nil {
			return err
		}
	}
	keys := make([][2]string, 0, len(r.sessions))
	for k := range r.sessions {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		if err := r.exec(`UPDATE sessions SET updated_seq=? WHERE host=? AND id=?`, r.e.Seq, k[0], k[1]); err != nil {
			return err
		}
		if err := r.exec(`INSERT INTO changes(seq,host,session) VALUES(?,?,?)`, r.e.Seq, k[0], k[1]); err != nil {
			return err
		}
	}
	dids := make([]string, 0, len(r.decisions))
	for id := range r.decisions {
		dids = append(dids, id)
	}
	sort.Strings(dids)
	for _, id := range dids {
		if err := r.exec(`UPDATE decisions SET updated_seq=? WHERE id=?`, r.e.Seq, id); err != nil {
			return err
		}
		if err := r.exec(`INSERT INTO changes(seq,decision_id) VALUES(?,?)`, r.e.Seq, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *reducer) touchWork(id string) {
	if id != "" {
		r.works[id] = true
	}
}

func (r *reducer) touchSession(host, id string) { r.sessions[[2]string{host, id}] = true }

func (r *reducer) touchMine() { r.touchSession(r.e.Host, r.e.Session) }

// timeline adds a session event (계약 세션 사건) to the given session.
func (r *reducer) timeline(host, session, kind, workID, stepID, runID, summary, transcript, agentTranscript string) error {
	r.sub++
	return r.exec(`INSERT INTO timeline(seq,sub,host,session,at,kind,work_id,step_id,run_id,summary,transcript,agent_transcript) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.e.Seq, r.sub, host, session, r.e.At, kind, workID, stepID, runID, clip(summary, summaryMax), transcript, agentTranscript)
}

func (r *reducer) mine(kind, workID, stepID, runID, summary string) error {
	return r.timeline(r.e.Host, r.e.Session, kind, workID, stepID, runID, summary, "", "")
}

// derive records an event the reducer itself produced (a dropped edge).
// Replays skip it: the original row is already in the ledger.
func (r *reducer) derive(kind string, payload any) error {
	if r.replay {
		return nil
	}
	b, _ := json.Marshal(payload)
	return r.exec(`INSERT INTO ledger(kind,host,session,at,cwd,data,derived) VALUES(?,?,?,?,?,?,1)`, kind, r.e.Host, r.e.Session, r.e.At, r.e.Cwd, string(b))
}

// ---- 세션 ----

func (r *reducer) ensureSession(host, id string) error {
	if host != r.e.Host || id != r.e.Session {
		// 다른 세션(부모)은 자리만 만든다. 이 사건의 작업 폴더는 그 세션의 것이 아니다
		return r.exec(`INSERT OR IGNORE INTO sessions(host,id,started_at,last_seen) VALUES(?,?,?,?)`, host, id, r.e.At, r.e.At)
	}
	if err := r.exec(`INSERT OR IGNORE INTO sessions(host,id,cwd,started_at,last_seen) VALUES(?,?,?,?,?)`, host, id, r.e.Cwd, r.e.At, r.e.At); err != nil {
		return err
	}
	return r.exec(`UPDATE sessions SET last_seen=MAX(last_seen,?), cwd=CASE WHEN cwd='' THEN ? ELSE cwd END WHERE host=? AND id=?`, r.e.At, r.e.Cwd, host, id)
}

func (r *reducer) sessionStart() error {
	var p SessionStart
	if err := r.payload(&p); err != nil {
		return err
	}
	var endedAt int64
	_ = r.tx.QueryRow(`SELECT ended_at FROM sessions WHERE host=? AND id=?`, r.e.Host, r.e.Session).Scan(&endedAt)
	set := []string{"turn_open=0", "ended_at=0", "end_reason=''", "end_detail=''"}
	args := []any{}
	for col, v := range map[string]string{"surface": p.Surface, "cwd": r.e.Cwd, "repo": p.Repo, "branch": p.Branch, "project_scope": p.Project.Scope,
		"project_label": p.Project.Label, "model": p.Model, "transcript": p.Transcript, "list_id": p.ListID, "launcher": p.Launcher} {
		if v != "" {
			set = append(set, col+"=?")
			args = append(args, v)
		}
	}
	if err := r.exec(`UPDATE sessions SET `+strings.Join(set, ",")+` WHERE host=? AND id=?`, append(args, r.e.Host, r.e.Session)...); err != nil {
		return err
	}
	r.touchMine()
	summary := r.e.Host + " 세션 시작"
	if p.Source == "resume" || endedAt > 0 {
		summary = r.e.Host + " 세션 재개"
	}
	if err := r.timeline(r.e.Host, r.e.Session, "session.started", "", "", "", summary, p.Transcript, ""); err != nil {
		return err
	}
	if p.Parent != nil && p.Parent.Session != "" && (p.Parent.Session != r.e.Session || p.Parent.Host != r.e.Host) {
		return r.linkParent(p)
	}
	return nil
}

// linkParent ties a child session (ORBIT_PARENT) to its parent step as a run
// of kind session, and makes its works children of that step.
func (r *reducer) linkParent(p SessionStart) error {
	ph := p.Parent.Host
	if ph == "" {
		ph = r.e.Host
	}
	if err := r.ensureSession(ph, p.Parent.Session); err != nil {
		return err
	}
	workID, stepID := r.parentStep(ph, p.Parent.Session, p.Parent.Step)
	if err := r.exec(`UPDATE sessions SET parent_host=?, parent_session=?, parent_work=?, parent_step=? WHERE host=? AND id=?`,
		ph, p.Parent.Session, workID, stepID, r.e.Host, r.e.Session); err != nil {
		return err
	}
	id := hashID("r-", "session", r.e.Host, r.e.Session)
	link := "parent_env"
	var exists int
	_ = r.tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=?`, id).Scan(&exists)
	if exists == 0 {
		if err := r.exec(`INSERT INTO runs(id,kind,host,session,run_host,child_session,parent_session,step_id,link,background,cwd,repo,branch,state,started_at,last_seen,seq)
			VALUES(?,'session',?,?,?,?,?,?,?,1,?,?,?,'running',?,?,?)`,
			id, ph, p.Parent.Session, r.e.Host, r.e.Session, p.Parent.Session, stepID, link, r.e.Cwd, p.Repo, p.Branch, r.e.At, r.e.At, r.e.Seq); err != nil {
			return err
		}
	} else if err := r.exec(`UPDATE runs SET state='running', end_reason='', finished_at=0, last_seen=? WHERE id=?`, r.e.At, id); err != nil {
		return err
	}
	r.touchSession(ph, p.Parent.Session)
	r.touchWork(workID)
	return r.timeline(ph, p.Parent.Session, "run.started", workID, stepID, id, r.e.Host+" 하위 세션 시작", "", "")
}

// parentStep finds the parent's step by host number (Task id) or 1-based
// position in the parent's open snapshot list.
func (r *reducer) parentStep(host, session, ref string) (string, string) {
	if ref == "" {
		return "", ""
	}
	var listID string
	_ = r.tx.QueryRow(`SELECT list_id FROM sessions WHERE host=? AND id=?`, host, session).Scan(&listID)
	for _, l := range []string{listID, session} {
		if l == "" {
			continue
		}
		var stepID, workID string
		if r.tx.QueryRow(`SELECT n.step_id, s.work_id FROM natives n JOIN steps s ON s.id=n.step_id WHERE n.host=? AND n.list=? AND n.native=?`, host, l, ref).Scan(&stepID, &workID) == nil {
			return workID, stepID
		}
	}
	var pos int
	if _, err := fmt.Sscan(ref, &pos); err != nil || pos < 1 {
		return "", ""
	}
	var workID string
	if r.tx.QueryRow(`SELECT open_work FROM lists WHERE host=? AND id=? AND open_work<>'' AND kind NOT IN (?,?)`, host, session, ListClaudeTask, ListOrbitPiPlan).Scan(&workID) != nil {
		return "", ""
	}
	var stepID string
	if r.tx.QueryRow(`SELECT id FROM steps WHERE work_id=? AND listed=1 ORDER BY ord LIMIT 1 OFFSET ?`, workID, pos-1).Scan(&stepID) != nil {
		return workID, ""
	}
	return workID, stepID
}

func (r *reducer) turnStart() error {
	var p Turn
	if err := r.payload(&p); err != nil {
		return err
	}
	if p.Prompt != "" && r.turnEnded(p.Prompt) {
		return nil
	}
	if err := r.exec(`UPDATE sessions SET turn=?, turn_open=1 WHERE host=? AND id=?`, p.Prompt, r.e.Host, r.e.Session); err != nil {
		return err
	}
	r.touchMine()
	if err := r.mine("turn.started", "", "", "", ""); err != nil {
		return err
	}
	return r.resolveWaits(`1=1`) // 새 요청이 왔으면 앞의 질문·권한 요청은 답을 받은 것이다
}

func (r *reducer) turnEnded(prompt string) bool {
	var n int
	_ = r.tx.QueryRow(`SELECT COUNT(*) FROM ended_turns WHERE host=? AND session=? AND prompt=?`, r.e.Host, r.e.Session, prompt).Scan(&n)
	return n > 0
}

func (r *reducer) turnEnd() error {
	var p Turn
	if err := r.payload(&p); err != nil {
		return err
	}
	var turn string
	var open int
	_ = r.tx.QueryRow(`SELECT turn, turn_open FROM sessions WHERE host=? AND id=?`, r.e.Host, r.e.Session).Scan(&turn, &open)
	if p.Prompt != "" {
		if r.turnEnded(p.Prompt) {
			return nil // 한 턴에 Stop이 여러 번 와도 한 번만(기억 m-b6dd590a0eaf)
		}
		if err := r.exec(`INSERT OR IGNORE INTO ended_turns(host,session,prompt) VALUES(?,?,?)`, r.e.Host, r.e.Session, p.Prompt); err != nil {
			return err
		}
	}
	if p.Prompt == "" || p.Prompt == turn {
		if open == 0 && p.Prompt == "" {
			return nil
		}
		if err := r.exec(`UPDATE sessions SET turn_open=0 WHERE host=? AND id=?`, r.e.Host, r.e.Session); err != nil {
			return err
		}
		r.touchMine()
		if err := r.mine("turn.ended", "", "", "", ""); err != nil {
			return err
		}
	}
	if err := r.withdrawSessionDecisions("turn_ended", p.Prompt); err != nil {
		return err
	}
	if p.Prompt == "" {
		return r.resolveWaits(`1=1`)
	}
	return r.resolveWaits(`(prompt=? OR prompt='')`, p.Prompt)
}

func (r *reducer) sessionEnd() error {
	var p SessionEnd
	if err := r.payload(&p); err != nil {
		return err
	}
	var ended int64
	_ = r.tx.QueryRow(`SELECT ended_at FROM sessions WHERE host=? AND id=?`, r.e.Host, r.e.Session).Scan(&ended)
	if ended > 0 {
		return nil
	}
	if err := r.exec(`UPDATE sessions SET ended_at=?, end_reason='session_end', end_detail=?, turn_open=0 WHERE host=? AND id=?`,
		r.e.At, clip(p.Reason, 80), r.e.Host, r.e.Session); err != nil {
		return err
	}
	r.touchMine()
	summary := "세션 끝"
	if p.Reason != "" {
		summary += "(" + clip(p.Reason, 40) + ")"
	}
	if err := r.mine("session.ended", "", "", "", summary); err != nil {
		return err
	}
	if err := r.withdrawSessionDecisions("session_ended", ""); err != nil {
		return err
	}
	if err := r.resolveWaits(`1=1`); err != nil {
		return err
	}
	if err := r.exec(`DELETE FROM waits WHERE host=? AND session=?`, r.e.Host, r.e.Session); err != nil {
		return err
	}
	// 끝이 오지 않은 서브에이전트는 잃었다
	lost, err := r.queryRuns(`host=? AND session=? AND kind='subagent' AND state='running'`, r.e.Host, r.e.Session)
	if err != nil {
		return err
	}
	for _, run := range lost {
		if err := r.exec(`UPDATE runs SET state='lost', end_reason='session_ended', finished_at=? WHERE id=?`, r.e.At, run.id); err != nil {
			return err
		}
		if err := r.mine("run.ended", run.workID(r), run.step, run.id, roleName(run.role)+" 잃음: 세션이 끝났습니다"); err != nil {
			return err
		}
		if run.step != "" {
			r.touchWork(run.workID(r))
			if err := r.mine("block.opened", run.workID(r), run.step, run.id, "막힘: 맡긴 실행 잃음"); err != nil {
				return err
			}
		}
	}
	// 진행 중에 세션이 끝난 단계는 중단
	rows, err := r.tx.Query(`SELECT id, work_id FROM steps WHERE last_host=? AND last_session=? AND host_status='in_progress' ORDER BY id`, r.e.Host, r.e.Session)
	if err != nil {
		return err
	}
	var steps [][2]string
	for rows.Next() {
		var id, w string
		_ = rows.Scan(&id, &w)
		steps = append(steps, [2]string{id, w})
	}
	rows.Close()
	for _, s := range steps {
		if err := r.exec(`UPDATE steps SET interrupted_at=? WHERE id=?`, r.e.At, s[0]); err != nil {
			return err
		}
		r.touchWork(s[1])
		if err := r.mine("block.opened", s[1], s[0], "", "막힘: 중단"); err != nil {
			return err
		}
	}
	// 이 세션이 하위 세션이면 부모의 실행이 끝났다
	var ph, ps string
	_ = r.tx.QueryRow(`SELECT parent_host, parent_session FROM sessions WHERE host=? AND id=?`, r.e.Host, r.e.Session).Scan(&ph, &ps)
	if ps != "" {
		id := hashID("r-", "session", r.e.Host, r.e.Session)
		var step, state string
		if r.tx.QueryRow(`SELECT step_id, state FROM runs WHERE id=?`, id).Scan(&step, &state) == nil && state == "running" {
			if err := r.exec(`UPDATE runs SET state='finished', end_reason='completed', finished_at=?, last_seen=? WHERE id=?`, r.e.At, r.e.At, id); err != nil {
				return err
			}
			w := r.stepWork(step)
			r.touchWork(w)
			r.touchSession(ph, ps)
			if err := r.timeline(ph, ps, "run.ended", w, step, id, r.e.Host+" 하위 세션 끝", "", ""); err != nil {
				return err
			}
		}
	}
	return r.touchSessionWorks()
}

func (r *reducer) touchSessionWorks() error {
	rows, err := r.tx.Query(`SELECT id FROM works WHERE host=? AND session=? AND closed_at=0`, r.e.Host, r.e.Session)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		r.touchWork(id)
	}
	return rows.Err()
}

// ---- 기다림 ----

func (r *reducer) waitOpen() error {
	var p Wait
	if err := r.payload(&p); err != nil || p.Key == "" {
		return err
	}
	var ended int64
	_ = r.tx.QueryRow(`SELECT ended_at FROM sessions WHERE host=? AND id=?`, r.e.Host, r.e.Session).Scan(&ended)
	if ended > 0 || (p.Prompt != "" && r.turnEnded(p.Prompt)) {
		return nil // 이미 끝난 턴·세션의 늦은 요청
	}
	// 같은 도구·같은 입력의 해소 표시보다 뒤에 불린 요청은 새 요청이다(같은 명령을 다시 승인).
	// 앞에 불렸는데 늦게 온 요청은 이미 해소된 것이라 열지 않는다.
	res, err := r.tx.Exec(`INSERT INTO waits(host,session,key,prompt,tool,opened_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(host,session,key) DO UPDATE SET prompt=excluded.prompt, tool=excluded.tool, opened_at=excluded.opened_at, resolved=0, resolved_at=0
		WHERE waits.resolved=1 AND waits.resolved_at < excluded.opened_at`, r.e.Host, r.e.Session, p.Key, p.Prompt, p.Tool, r.e.At)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // 이미 열렸거나, 사후 사건이 먼저 왔다
	}
	r.touchMine()
	return r.blockEvents("block.opened", "사용자 응답 대기: "+p.Tool)
}

func (r *reducer) toolDone() error {
	var p ToolDone
	if err := r.payload(&p); err != nil {
		return err
	}
	if p.Agent != "" {
		if err := r.exec(`UPDATE runs SET last_seen=MAX(last_seen,?) WHERE id=?`, r.e.At, hashID("r-", r.e.Host, r.e.Session, p.Agent)); err != nil {
			return err
		}
	}
	if p.Key == "" {
		return nil
	}
	var resolved int
	err := r.tx.QueryRow(`SELECT resolved FROM waits WHERE host=? AND session=? AND key=?`, r.e.Host, r.e.Session, p.Key).Scan(&resolved)
	if errors.Is(err, sql.ErrNoRows) {
		// 늦게 올 기다림을 열지 않도록 해소 표시만 남긴다. 세션이 끝나면 지운다
		return r.exec(`INSERT INTO waits(host,session,key,tool,resolved,resolved_at) VALUES(?,?,?,?,1,?)`, r.e.Host, r.e.Session, p.Key, p.Tool, r.e.At)
	}
	if err != nil {
		return err
	}
	if resolved == 1 {
		// 다시 돈 같은 도구: 그 앞에 불린 늦은 요청도 열지 않도록 해소 시각을 미룬다
		return r.exec(`UPDATE waits SET resolved_at=MAX(resolved_at,?) WHERE host=? AND session=? AND key=?`, r.e.At, r.e.Host, r.e.Session, p.Key)
	}
	return r.resolveWaits(`key=?`, p.Key)
}

func (r *reducer) resolveWaits(cond string, args ...any) error {
	q := `UPDATE waits SET resolved=1, resolved_at=? WHERE host=? AND session=? AND resolved=0 AND ` + cond
	res, err := r.tx.Exec(q, append([]any{r.e.At, r.e.Host, r.e.Session}, args...)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	var still int
	_ = r.tx.QueryRow(`SELECT COUNT(*) FROM waits WHERE host=? AND session=? AND resolved=0`, r.e.Host, r.e.Session).Scan(&still)
	r.touchMine()
	if still > 0 {
		return nil
	}
	return r.blockEvents("block.cleared", "사용자 응답 대기 풀림")
}

// blockEvents adds a block event for every in-progress step of this session,
// or one session-level event when there is none.
func (r *reducer) blockEvents(kind, summary string) error {
	steps, err := r.activeSteps(r.e.Host, r.e.Session)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		return r.mine(kind, "", "", "", summary)
	}
	for _, s := range steps {
		r.touchWork(s.work)
		if err := r.mine(kind, s.work, s.id, "", summary); err != nil {
			return err
		}
	}
	return nil
}

type stepRef struct{ id, work, owner string }

func (r *reducer) activeSteps(host, session string) ([]stepRef, error) {
	return r.querySteps(`s.host_status='in_progress'`, host, session)
}

// activeStepsAt returns the steps that were in progress when this event's
// hook fired. An async hook can arrive after a later sync hook moved the list
// on, so evidence and runs follow the hook's time, not the arrival.
//
// Steps keep one start and one finish, not their history: a step reopened
// later counts as in progress since its first start, and only the session
// that touched a step last finds it. A finish and a hook in the same
// millisecond leave the finished step out.
func (r *reducer) activeStepsAt(host, session string) ([]stepRef, error) {
	return r.querySteps(`s.started_at>0 AND s.started_at<=? AND (s.host_status='in_progress' OR s.finished_at>?)`, host, session, r.e.At, r.e.At)
}

func (r *reducer) querySteps(cond, host, session string, args ...any) ([]stepRef, error) {
	rows, err := r.tx.Query(`SELECT s.id, s.work_id, s.owner FROM steps s WHERE s.last_host=? AND s.last_session=? AND `+cond+` ORDER BY s.started_at DESC, s.id`, append([]any{host, session}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stepRef
	for rows.Next() {
		var s stepRef
		if err := rows.Scan(&s.id, &s.work, &s.owner); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *reducer) stepWork(step string) string {
	if step == "" {
		return ""
	}
	var w string
	_ = r.tx.QueryRow(`SELECT work_id FROM steps WHERE id=?`, step).Scan(&w)
	return w
}

// ---- 실행 ----

type runRef struct {
	id, role, step, state, toolUse string
}

func (run runRef) workID(r *reducer) string { return r.stepWork(run.step) }

func (r *reducer) queryRuns(cond string, args ...any) ([]runRef, error) {
	rows, err := r.tx.Query(`SELECT id, role, step_id, state, tool_use FROM runs WHERE `+cond+` ORDER BY started_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runRef
	for rows.Next() {
		var x runRef
		if err := rows.Scan(&x.id, &x.role, &x.step, &x.state, &x.toolUse); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// hostStep finds the step the host named on the event (1.2 step). An unknown
// id gives no step: the event is not guessed onto another step.
func (r *reducer) hostStep(ref StepRef) string {
	if ref.Step == "" {
		return ""
	}
	var id string
	_ = r.tx.QueryRow(`SELECT step_id FROM natives WHERE host=? AND list=? AND native=?`, r.e.Host, r.sessionList(ref.List), ref.Step).Scan(&id)
	return id
}

// linkStep picks the step a new subagent run works on (설계 「서브에이전트를 단계에 잇는 순서」).
// A step the host named comes first (link step); a named step that is unknown links nothing.
func (r *reducer) linkStep(role string, ref StepRef) (string, string, error) {
	if ref.Step != "" {
		if id := r.hostStep(ref); id != "" {
			return id, "step", nil
		}
		return "", "none", nil
	}
	steps, err := r.activeStepsAt(r.e.Host, r.e.Session)
	if err != nil {
		return "", "none", err
	}
	if role != "" {
		for _, s := range steps {
			if s.owner != "" && sameRole(s.owner, role) {
				return s.id, "owner", nil
			}
		}
	}
	if len(steps) == 1 {
		return steps[0].id, "single_active", nil
	}
	return "", "none", nil
}

// sameRole matches a Task owner with a subagent type: "orbit:worker",
// "worker" and Codex's "orbit-worker" name the same role.
func sameRole(owner, role string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		if i := strings.LastIndex(s, ":"); i >= 0 {
			s = s[i+1:]
		}
		return strings.TrimPrefix(s, "orbit-")
	}
	return norm(owner) == norm(role)
}

func (r *reducer) ensureRun(agent, role string, ref StepRef) (runRef, bool, error) {
	id := hashID("r-", r.e.Host, r.e.Session, agent)
	var x runRef
	err := r.tx.QueryRow(`SELECT id, role, step_id, state, tool_use FROM runs WHERE id=?`, id).Scan(&x.id, &x.role, &x.step, &x.state, &x.toolUse)
	if err == nil {
		if x.role == "" && role != "" {
			x.role = role
			if err := r.exec(`UPDATE runs SET role=? WHERE id=?`, role, id); err != nil {
				return x, false, err
			}
		}
		// 호스트가 밝힌 단계는 짐작한 연결보다 앞선다(끝 사건이 먼저 와 단계 없이 만든 실행 포함)
		if s := r.hostStep(ref); s != "" && s != x.step {
			old := x.step
			x.step = s
			if err := r.exec(`UPDATE runs SET step_id=?, link='step' WHERE id=?`, s, id); err != nil {
				return x, false, err
			}
			r.touchWork(r.stepWork(old))
		}
		return x, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return x, false, err
	}
	step, link, err := r.linkStep(role, ref)
	if err != nil {
		return x, false, err
	}
	x = runRef{id: id, role: role, step: step, state: "running"}
	err = r.exec(`INSERT INTO runs(id,kind,host,session,run_host,agent,role,parent_session,step_id,link,cwd,state,started_at,last_seen,seq)
		VALUES(?,'subagent',?,?,?,?,?,?,?,?,?,'running',?,?,?)`, id, r.e.Host, r.e.Session, r.e.Host, agent, role, r.e.Session, step, link, r.e.Cwd, r.e.At, r.e.At, r.e.Seq)
	return x, true, err
}

func (r *reducer) runStart() error {
	var p RunStart
	if err := r.payload(&p); err != nil || p.Agent == "" {
		return err
	}
	run, created, err := r.ensureRun(p.Agent, p.Type, p.StepRef)
	if err != nil {
		return err
	}
	if !created {
		// 끝 사건이 먼저 왔다. 시작 정보만 채운다
		if err := r.exec(`UPDATE runs SET started_at=MIN(started_at,?), repo=?, branch=?, cwd=CASE WHEN cwd='' THEN ? ELSE cwd END WHERE id=?`, r.e.At, p.Repo, p.Branch, r.e.Cwd, run.id); err != nil {
			return err
		}
	} else if err := r.exec(`UPDATE runs SET repo=?, branch=? WHERE id=?`, p.Repo, p.Branch, run.id); err != nil {
		return err
	}
	r.touchMine()
	w := run.workID(r)
	r.touchWork(w)
	if !created {
		return nil
	}
	summary := roleName(p.Type) + " 시작"
	return r.mine("run.started", w, run.step, run.id, summary)
}

func (r *reducer) runTool() error {
	var p RunTool
	if err := r.payload(&p); err != nil {
		return err
	}
	var run runRef
	if p.Agent != "" {
		var err error
		if run, _, err = r.ensureRun(p.Agent, p.Type, StepRef{}); err != nil {
			return err
		}
	} else if !p.OK {
		runs, err := r.queryRuns(`host=? AND session=? AND kind='subagent' AND (tool_use=? OR (tool_use='' AND state='running' AND role=?))`, r.e.Host, r.e.Session, p.ToolUse, p.Type)
		if err != nil {
			return err
		}
		if len(runs) > 0 {
			run = runs[0]
		} else {
			var err error
			// 시작 훅 없이 실패한 실행: 도구 호출 하나를 실행 하나로 남긴다
			if run, _, err = r.ensureRun("tool:"+p.ToolUse, p.Type, StepRef{}); err != nil {
				return err
			}
		}
	} else {
		return nil
	}
	set, args := []string{}, []any{}
	if p.ToolUse != "" {
		set, args = append(set, "tool_use=?"), append(args, p.ToolUse)
	}
	if p.Background {
		set = append(set, "background=1")
	}
	if p.Model != "" {
		set, args = append(set, "model=?"), append(args, p.Model)
	}
	failed := !p.OK && run.state != "failed"
	if failed {
		set, args = append(set, "state='failed'", "end_reason='failed'", "finished_at=?"), append(args, r.e.At)
	}
	if len(set) > 0 {
		if err := r.exec(`UPDATE runs SET `+strings.Join(set, ",")+` WHERE id=?`, append(args, run.id)...); err != nil {
			return err
		}
	}
	w := run.workID(r)
	r.touchWork(w)
	r.touchMine()
	if !failed {
		return nil
	}
	if err := r.mine("run.ended", w, run.step, run.id, roleName(run.role)+" 실패"); err != nil {
		return err
	}
	if run.step != "" {
		return r.mine("block.opened", w, run.step, run.id, "막힘: 맡긴 실행 실패")
	}
	return nil
}

func (r *reducer) runStop() error {
	var p RunStop
	if err := r.payload(&p); err != nil || p.Agent == "" {
		return err
	}
	run, _, err := r.ensureRun(p.Agent, p.Type, p.StepRef)
	if err != nil {
		return err
	}
	if p.Transcript != "" {
		if err := r.exec(`UPDATE runs SET transcript=? WHERE id=?`, p.Transcript, run.id); err != nil {
			return err
		}
	}
	if p.Cost != nil {
		b, _ := json.Marshal(cleanCost(*p.Cost))
		if err := r.exec(`UPDATE runs SET cost=? WHERE id=?`, string(b), run.id); err != nil {
			return err
		}
	}
	w := run.workID(r)
	r.touchWork(w)
	r.touchMine()
	if run.state == "running" || run.state == "lost" {
		// 끝이 두 번 와도 합친다. 잃음으로 본 뒤 온 끝은 실제로 끝난 것이다
		if err := r.exec(`UPDATE runs SET state='finished', end_reason='completed', finished_at=?, last_seen=MAX(last_seen,?) WHERE id=?`, r.e.At, r.e.At, run.id); err != nil {
			return err
		}
		if err := r.timeline(r.e.Host, r.e.Session, "run.ended", w, run.step, run.id, roleName(run.role)+" 끝", "", p.Transcript); err != nil {
			return err
		}
	}
	if p.Verdict == nil {
		return nil
	}
	id := hashID("ev-", r.e.Seq, "verdict")
	if err := r.exec(`INSERT OR IGNORE INTO evidence(id,kind,host,session,step_id,run_id,at,seq,result,summary,transcript) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, "verdict", r.e.Host, r.e.Session, run.step, run.id, r.e.At, r.e.Seq, p.Verdict.Result, roleName(p.Verdict.Role)+" "+verdictName(p.Verdict.Result), p.Transcript); err != nil {
		return err
	}
	return r.mine("evidence.added", w, run.step, run.id, roleName(p.Verdict.Role)+" 판정: "+verdictName(p.Verdict.Result))
}

// ---- 증거 ----

func (r *reducer) evidence() error {
	var agent string
	var cmd Command
	var commit Commit
	var ref StepRef
	if r.e.Kind == KindCommand {
		if err := r.payload(&cmd); err != nil {
			return err
		}
		agent, ref = cmd.Agent, cmd.StepRef
	} else {
		if err := r.payload(&commit); err != nil || commit.SHA == "" {
			return err
		}
		agent, ref = commit.Agent, commit.StepRef
	}
	var runID, step string
	if agent != "" {
		runID = hashID("r-", r.e.Host, r.e.Session, agent)
		if r.tx.QueryRow(`SELECT step_id FROM runs WHERE id=?`, runID).Scan(&step) != nil {
			runID = ""
		}
	}
	if ref.Step != "" {
		step = r.hostStep(ref) // 호스트가 밝힌 단계가 앞선다. 모르는 단계면 세션 수준 증거
	} else if agent != "" {
		// 실행의 단계를 따른다
	} else if steps, err := r.activeStepsAt(r.e.Host, r.e.Session); err != nil {
		return err
	} else if len(steps) == 1 {
		step = steps[0].id
	}
	id := hashID("ev-", r.e.Seq)
	var err error
	var summary string
	if r.e.Kind == KindCommand {
		result := "ok"
		summary = "시험 명령 성공: " + cmd.Text
		if !cmd.OK {
			result, summary = "fail", "시험 명령 실패: "+cmd.Text
		}
		err = r.exec(`INSERT OR IGNORE INTO evidence(id,kind,host,session,step_id,run_id,at,seq,result,summary) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			id, "command", r.e.Host, r.e.Session, step, runID, r.e.At, r.e.Seq, result, clip(cmd.Text, commandMax))
	} else {
		files, _ := json.Marshal(nonNil(commit.Files))
		summary = "커밋: " + commit.Subject
		err = r.exec(`INSERT OR IGNORE INTO evidence(id,kind,host,session,step_id,run_id,at,seq,result,summary,repo,sha,files) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, "commit", r.e.Host, r.e.Session, step, runID, r.e.At, r.e.Seq, "ok", clip(commit.Subject, summaryMax), commit.Repo, commit.SHA, string(files))
	}
	if err != nil {
		return err
	}
	w := r.stepWork(step)
	r.touchWork(w)
	r.touchMine()
	return r.mine("evidence.added", w, step, runID, summary)
}

// ---- 제목 ----

func (r *reducer) title() error {
	var p Title
	if err := r.payload(&p); err != nil || p.Work == "" {
		return err
	}
	res, err := r.tx.Exec(`UPDATE works SET title=?, title_at=? WHERE id=?`, p.Title, r.e.At, p.Work)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.touchWork(p.Work)
	}
	return nil
}

// cleanCost keeps a run cost's text fields one line and short.
func cleanCost(c Cost) Cost {
	c.Engine, c.Model = Clean(c.Engine, 80), Clean(c.Model, 120)
	return c
}

func roleName(role string) string {
	if role == "" {
		return "서브에이전트"
	}
	return role
}

func verdictName(result string) string {
	return map[string]string{"pass": "통과", "needs_fix": "수정 필요", "needs_user": "사용자 확인 필요"}[result]
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
