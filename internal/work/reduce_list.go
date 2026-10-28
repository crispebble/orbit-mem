package work

import (
	"database/sql"
	"errors"
	"strings"
)

// Host step statuses kept in steps.host_status.
const (
	hostPending   = "pending"
	hostActive    = "in_progress"
	hostDone      = "done"
	hostCancelled = "cancelled"
)

// hostStatus maps a host list status to ours. Unknown values give "".
func hostStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending":
		return hostPending
	case "in_progress", "in-progress", "active":
		return hostActive
	case "completed", "done":
		return hostDone
	case "deleted", "cancelled", "canceled":
		return hostCancelled
	}
	return ""
}

var statusName = map[string]string{hostPending: "대기", hostActive: "진행", hostDone: "끝남", hostCancelled: "빠짐"}

type stepRow struct {
	id, work, title, status string
	ord                     int
	closedWork              bool
}

// listWork returns the list's open work, opening a new one when there is none.
func (r *reducer) listWork(kind, listID string, open bool) (string, error) {
	var w string
	err := r.tx.QueryRow(`SELECT open_work FROM lists WHERE host=? AND kind=? AND id=?`, r.e.Host, kind, listID).Scan(&w)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if w != "" || !open {
		return w, nil
	}
	return r.openWork(kind, listID)
}

func (r *reducer) openWork(kind, listID string) (string, error) {
	id := hashID("w-", r.e.Host, kind, listID, r.e.Seq)
	var surface, cwd, scope, label, pw, ps string
	_ = r.tx.QueryRow(`SELECT surface, cwd, project_scope, project_label, parent_work, parent_step FROM sessions WHERE host=? AND id=?`, r.e.Host, r.e.Session).
		Scan(&surface, &cwd, &scope, &label, &pw, &ps)
	if cwd == "" {
		cwd = r.e.Cwd
	}
	if surface == "" {
		surface = "unknown"
	}
	if err := r.exec(`INSERT INTO works(id,host,session,surface,list_kind,list_id,cwd,project_scope,project_label,parent_work,parent_step,opened_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, r.e.Host, r.e.Session, surface, kind, listID, cwd, scope, label, pw, ps, r.e.At, r.e.At); err != nil {
		return "", err
	}
	if err := r.exec(`INSERT INTO lists(host,kind,id,open_work,last_work) VALUES(?,?,?,?,?)
		ON CONFLICT(host,kind,id) DO UPDATE SET open_work=excluded.open_work, last_work=excluded.last_work`, r.e.Host, kind, listID, id, id); err != nil {
		return "", err
	}
	r.opened = append(r.opened, id)
	r.touchWork(id)
	r.touchMine()
	r.touchWork(pw)
	return id, nil
}

func (r *reducer) nextOrd(work string) int {
	var n sql.NullInt64
	_ = r.tx.QueryRow(`SELECT MAX(ord) FROM steps WHERE work_id=?`, work).Scan(&n)
	if !n.Valid {
		return 0
	}
	return int(n.Int64) + 1
}

func (r *reducer) insertStep(work, native, title string, ord int) (string, error) {
	id := hashID("st-", work, native, title, ord, r.e.Seq)
	err := r.exec(`INSERT INTO steps(id,work_id,native,title,ord,host_status,last_host,last_session,created_seq) VALUES(?,?,?,?,?,'pending',?,?,?)`,
		id, work, native, clip(title, stepTitleMax), ord, r.e.Host, r.e.Session, r.e.Seq)
	r.touchWork(work)
	return id, err
}

// nativeStep finds or creates the step of a Claude Code task id.
func (r *reducer) nativeStep(list, native, title string) (string, string, bool, error) {
	var id, work string
	err := r.tx.QueryRow(`SELECT n.step_id, s.work_id FROM natives n JOIN steps s ON s.id=n.step_id WHERE n.host=? AND n.list=? AND n.native=?`, r.e.Host, list, native).Scan(&id, &work)
	if err == nil {
		return id, work, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", false, err
	}
	if work, err = r.listWork(ListClaudeTask, list, true); err != nil {
		return "", "", false, err
	}
	if title == "" {
		title = "#" + native // 만들기를 보지 못한 항목. 다음 TaskList·TaskGet이 제목을 채운다
	}
	if id, err = r.insertStep(work, native, title, r.nextOrd(work)); err != nil {
		return "", "", false, err
	}
	if err := r.exec(`INSERT INTO natives(host,list,native,step_id) VALUES(?,?,?,?)`, r.e.Host, list, native, id); err != nil {
		return "", "", false, err
	}
	return id, work, true, nil
}

// setStatus moves a step's host status and keeps its start and finish times.
func (r *reducer) setStatus(step, work, status string) (bool, error) {
	var cur, title string
	var started, finished int64
	if err := r.tx.QueryRow(`SELECT host_status, title, started_at, finished_at FROM steps WHERE id=?`, step).Scan(&cur, &title, &started, &finished); err != nil {
		return false, err
	}
	if status == "" || status == cur {
		return false, nil
	}
	switch status {
	case hostActive:
		if started == 0 {
			started = r.e.At
		}
		finished = 0
	case hostDone, hostCancelled:
		finished = r.e.At
	case hostPending:
		finished = 0
	}
	if err := r.exec(`UPDATE steps SET host_status=?, started_at=?, finished_at=? WHERE id=?`, status, started, finished, step); err != nil {
		return false, err
	}
	r.touchWork(work)
	return true, r.mine("step.changed", work, step, "", title+": "+statusName[status])
}

// touchStep marks a step as last updated by this session. An update clears
// "interrupted" (설계 「막힘」: 같은 작업 목록을 이어 쓰는 세션이 그 단계를 갱신).
func (r *reducer) touchStep(step, work string) error {
	var interrupted int64
	_ = r.tx.QueryRow(`SELECT interrupted_at FROM steps WHERE id=?`, step).Scan(&interrupted)
	if err := r.exec(`UPDATE steps SET last_host=?, last_session=?, interrupted_at=0 WHERE id=?`, r.e.Host, r.e.Session, step); err != nil {
		return err
	}
	r.touchWork(work)
	r.touchMine()
	if interrupted > 0 {
		return r.mine("block.cleared", work, step, "", "중단 풀림")
	}
	return nil
}

func (r *reducer) setDeclared(step, work, text string) error {
	var cur string
	_ = r.tx.QueryRow(`SELECT declared FROM steps WHERE id=?`, step).Scan(&cur)
	text = clip(text, summaryMax)
	if text == cur {
		return nil
	}
	at := r.e.At
	if text == "" {
		at = 0
	}
	if err := r.exec(`UPDATE steps SET declared=?, declared_at=? WHERE id=?`, text, at, step); err != nil {
		return err
	}
	if text == "" {
		return r.mine("block.cleared", work, step, "", "AI가 밝힌 막힘 풀림")
	}
	return r.mine("block.opened", work, step, "", "AI가 밝힌 막힘: "+text)
}

// addEdge says step comes after `after`. An edge that would make a cycle is
// dropped and the drop is recorded (설계 「단계 상태」).
func (r *reducer) addEdge(step, after, source string) error {
	if step == "" || after == "" {
		return nil
	}
	var n int
	_ = r.tx.QueryRow(`SELECT COUNT(*) FROM edges WHERE step_id=? AND after_id=?`, step, after).Scan(&n)
	if n > 0 {
		return nil
	}
	cycle := step == after
	if !cycle {
		var err error
		if cycle, err = r.reaches(after, step); err != nil {
			return err
		}
	}
	work := r.stepWork(step)
	if cycle {
		return r.derive(KindEdgeDropped, EdgeDropped{Work: work, Step: step, After: after})
	}
	r.touchWork(work)
	return r.exec(`INSERT INTO edges(step_id,after_id,source) VALUES(?,?,?)`, step, after, source)
}

// reaches says whether `from` comes after `target` through the edges.
func (r *reducer) reaches(from, target string) (bool, error) {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		rows, err := r.tx.Query(`SELECT after_id FROM edges WHERE step_id=?`, cur)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var a string
			_ = rows.Scan(&a)
			if a == target {
				rows.Close()
				return true, nil
			}
			if !seen[a] {
				seen[a] = true
				queue = append(queue, a)
			}
		}
		rows.Close()
	}
	return false, nil
}

// settle closes a work whose steps all ended, and reopens a closed one that got
// a live step again.
func (r *reducer) settle(work string) error {
	var total, live int
	var closed int64
	var kind, list string
	if err := r.tx.QueryRow(`SELECT closed_at, list_kind, list_id FROM works WHERE id=?`, work).Scan(&closed, &kind, &list); err != nil {
		return err
	}
	_ = r.tx.QueryRow(`SELECT COUNT(*), COALESCE(SUM(host_status NOT IN ('done','cancelled')),0) FROM steps WHERE work_id=?`, work).Scan(&total, &live)
	switch {
	case closed == 0 && total > 0 && live == 0:
		if err := r.exec(`UPDATE works SET closed_at=? WHERE id=?`, r.e.At, work); err != nil {
			return err
		}
		r.touchWork(work)
		r.touchMine()
		return r.exec(`UPDATE lists SET open_work='' WHERE host=? AND kind=? AND id=? AND open_work=?`, r.e.Host, kind, list, work)
	case closed > 0 && live > 0:
		if err := r.exec(`UPDATE works SET closed_at=0 WHERE id=?`, work); err != nil {
			return err
		}
		r.touchWork(work)
		r.touchMine()
		return r.exec(`UPDATE lists SET open_work=? WHERE host=? AND kind=? AND id=? AND open_work=''`, work, r.e.Host, kind, list)
	}
	return nil
}

// ---- Claude Code Task ----

func (r *reducer) taskCreate() error {
	var p TaskCreate
	if err := r.payload(&p); err != nil || p.ID == "" {
		return err
	}
	step, work, created, err := r.nativeStep(p.List, p.ID, p.Subject)
	if err != nil {
		return err
	}
	if !created {
		// 갱신이 먼저 왔다. 만들기는 처음 상태 통지라 진행·완료를 되돌리지 않는다(기억 m-6a9286af2233)
		if err := r.exec(`UPDATE steps SET title=CASE WHEN title LIKE '#%' THEN ? ELSE title END WHERE id=?`, clip(p.Subject, stepTitleMax), step); err != nil {
			return err
		}
	} else if err := r.mine("step.changed", work, step, "", p.Subject+": 대기"); err != nil {
		return err
	}
	set, args := []string{}, []any{}
	if p.ActiveForm != "" {
		set, args = append(set, "active_title=?"), append(args, clip(p.ActiveForm, stepTitleMax))
	}
	if p.Owner != "" {
		set, args = append(set, "owner=?"), append(args, p.Owner)
	}
	if len(set) > 0 {
		if err := r.exec(`UPDATE steps SET `+strings.Join(set, ",")+` WHERE id=?`, append(args, step)...); err != nil {
			return err
		}
	}
	if p.Blocked != "" {
		if err := r.setDeclared(step, work, p.Blocked); err != nil {
			return err
		}
	}
	if err := r.touchStep(step, work); err != nil {
		return err
	}
	return r.settle(work)
}

func (r *reducer) taskUpdate() error {
	var p TaskUpdate
	if err := r.payload(&p); err != nil || p.ID == "" {
		return err
	}
	title := ""
	if p.Subject != nil {
		title = *p.Subject
	}
	step, work, created, err := r.nativeStep(p.List, p.ID, title)
	if err != nil {
		return err
	}
	if created && (p.Status == nil || hostStatus(*p.Status) == hostPending) {
		if err := r.mine("step.changed", work, step, "", title+": 대기"); err != nil {
			return err
		}
	}
	set, args := []string{}, []any{}
	if p.Subject != nil && *p.Subject != "" {
		set, args = append(set, "title=?"), append(args, clip(*p.Subject, stepTitleMax))
	}
	if p.ActiveForm != nil {
		set, args = append(set, "active_title=?"), append(args, clip(*p.ActiveForm, stepTitleMax))
	}
	if p.Owner != nil {
		set, args = append(set, "owner=?"), append(args, *p.Owner)
	}
	if len(set) > 0 {
		if err := r.exec(`UPDATE steps SET `+strings.Join(set, ",")+` WHERE id=?`, append(args, step)...); err != nil {
			return err
		}
	}
	if p.Status != nil {
		if _, err := r.setStatus(step, work, hostStatus(*p.Status)); err != nil {
			return err
		}
	}
	if p.Blocked != nil {
		if err := r.setDeclared(step, work, *p.Blocked); err != nil {
			return err
		}
	}
	for _, pred := range p.AddBlockedBy {
		ps, _, _, err := r.nativeStep(p.List, pred, "")
		if err != nil {
			return err
		}
		if err := r.addEdge(step, ps, "native"); err != nil {
			return err
		}
	}
	for _, succ := range p.AddBlocks {
		ss, _, _, err := r.nativeStep(p.List, succ, "")
		if err != nil {
			return err
		}
		if err := r.addEdge(ss, step, "native"); err != nil {
			return err
		}
	}
	if err := r.touchStep(step, work); err != nil {
		return err
	}
	return r.settle(work)
}

// taskState applies TaskList/TaskGet: the host's current state of its items.
func (r *reducer) taskState() error {
	var p TaskState
	if err := r.payload(&p); err != nil {
		return err
	}
	works := map[string]bool{}
	seen := map[string]bool{}
	type preds struct {
		step string
		want map[string]bool
	}
	var reconcile []preds
	for _, t := range p.Tasks {
		if t.ID == "" {
			continue
		}
		seen[t.ID] = true
		step, work, _, err := r.nativeStep(p.List, t.ID, t.Subject)
		if err != nil {
			return err
		}
		works[work] = true
		if t.Subject != "" {
			if err := r.exec(`UPDATE steps SET title=? WHERE id=?`, clip(t.Subject, stepTitleMax), step); err != nil {
				return err
			}
		}
		if t.Owner != nil {
			if err := r.exec(`UPDATE steps SET owner=? WHERE id=?`, *t.Owner, step); err != nil {
				return err
			}
		}
		if _, err := r.setStatus(step, work, hostStatus(t.Status)); err != nil {
			return err
		}
		if t.BlockedBy != nil {
			want := map[string]bool{}
			for _, pred := range t.BlockedBy {
				ps, _, _, err := r.nativeStep(p.List, pred, "")
				if err != nil {
					return err
				}
				want[ps] = true
				if err := r.addEdge(step, ps, "native"); err != nil {
					return err
				}
			}
			reconcile = append(reconcile, preds{step, want})
		}
	}
	// TaskGet의 blockedBy는 선행 전체이고, TaskList의 것은 끝나지 않은 선행만이다(탐침).
	// 그래서 TaskList에서는 끝나거나 빠진 선행으로 가는 간선을 남긴다. 같은 목록에서 상태가
	// 바뀐 선행도 있으므로 모든 항목의 상태를 고친 뒤에 맞춘다.
	for _, x := range reconcile {
		if err := r.dropNativeEdgesExcept(x.step, x.want, p.Full); err != nil {
			return err
		}
	}
	if p.Full {
		// 목록 전체에서 빠진 열린 작업의 항목은 지워진 것이다
		open, err := r.listWork(ListClaudeTask, p.List, false)
		if err != nil {
			return err
		}
		if open != "" {
			works[open] = true
			rows, err := r.tx.Query(`SELECT s.id, s.native FROM steps s WHERE s.work_id=? AND s.native<>'' AND s.host_status NOT IN ('done','cancelled')`, open)
			if err != nil {
				return err
			}
			var gone []string
			for rows.Next() {
				var id, native string
				_ = rows.Scan(&id, &native)
				if !seen[native] {
					gone = append(gone, id)
				}
			}
			rows.Close()
			for _, id := range gone {
				if _, err := r.setStatus(id, open, hostCancelled); err != nil {
					return err
				}
			}
		}
	}
	for w := range works {
		if err := r.settle(w); err != nil {
			return err
		}
	}
	r.touchMine()
	return nil
}

// dropNativeEdgesExcept drops the native edges of step to predecessors not in keep.
// With openOnly the list names only open predecessors, so edges to finished ones stay.
func (r *reducer) dropNativeEdgesExcept(step string, keep map[string]bool, openOnly bool) error {
	rows, err := r.tx.Query(`SELECT e.after_id, COALESCE(s.host_status,'') FROM edges e LEFT JOIN steps s ON s.id=e.after_id
		WHERE e.step_id=? AND e.source='native'`, step)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var a, st string
		_ = rows.Scan(&a, &st)
		if openOnly && (st == hostDone || st == hostCancelled) {
			continue
		}
		if !keep[a] {
			drop = append(drop, a)
		}
	}
	rows.Close()
	for _, a := range drop {
		if err := r.exec(`DELETE FROM edges WHERE step_id=? AND after_id=?`, step, a); err != nil {
			return err
		}
		r.touchWork(r.stepWork(step))
	}
	return nil
}

// ---- 스냅숏 목록 ----

func (r *reducer) workSteps(work string) ([]stepRow, error) {
	rows, err := r.tx.Query(`SELECT id, work_id, title, host_status, ord FROM steps WHERE work_id=? ORDER BY ord, id`, work)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stepRow
	for rows.Next() {
		var s stepRow
		if err := rows.Scan(&s.id, &s.work, &s.title, &s.status, &s.ord); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *reducer) planSnapshot() error {
	var p PlanSnapshot
	if err := r.payload(&p); err != nil || p.List == "" {
		return err
	}
	work, err := r.listWork(p.List, p.ID, false)
	if err != nil {
		return err
	}
	if work == "" {
		if len(p.Items) == 0 {
			return nil
		}
		// 닫힌 작업과 똑같은 목록을 다시 보낸 것이면 새 작업이 아니다
		var last string
		_ = r.tx.QueryRow(`SELECT last_work FROM lists WHERE host=? AND kind=? AND id=?`, r.e.Host, p.List, p.ID).Scan(&last)
		if last != "" {
			same, err := r.sameList(last, p.Items)
			if err != nil || same {
				return err
			}
		}
		if work, err = r.openWork(p.List, p.ID); err != nil {
			return err
		}
	}
	steps, err := r.workSteps(work)
	if err != nil {
		return err
	}
	// 글이 정확히 같은 항목끼리만 잇는다. 같은 글이 여럿이면 순서대로 짝짓는다. 순서 번호로 잇지 않는다
	live, gone := map[string][]stepRow{}, map[string][]stepRow{}
	for _, s := range steps {
		if s.status == hostCancelled {
			gone[s.title] = append(gone[s.title], s)
		} else {
			live[s.title] = append(live[s.title], s)
		}
	}
	used := map[string]bool{}
	var order []string
	for i, it := range p.Items {
		text := clip(it.Text, stepTitleMax)
		var id string
		if q := live[text]; len(q) > 0 {
			id, live[text] = q[0].id, q[1:]
		} else if q := gone[text]; len(q) > 0 {
			id, gone[text] = q[0].id, q[1:]
		} else if id, err = r.insertStep(work, "", text, i); err != nil {
			return err
		} else if err := r.mine("step.changed", work, id, "", text+": 대기"); err != nil {
			return err
		}
		used[id] = true
		order = append(order, id)
		if err := r.exec(`UPDATE steps SET ord=?, listed=1, active_title=? WHERE id=?`, i, clip(it.ActiveText, stepTitleMax), id); err != nil {
			return err
		}
		if _, err := r.setStatus(id, work, hostStatus(it.Status)); err != nil {
			return err
		}
		if err := r.touchStep(id, work); err != nil {
			return err
		}
	}
	// 새 목록에 없는 단계는 빠짐. 이미 끝난 단계는 끝남으로 둔다(끝났다는 사실은 지우지 않는다)
	next := len(p.Items)
	for _, s := range steps {
		if used[s.id] {
			continue
		}
		if err := r.exec(`UPDATE steps SET ord=?, listed=0 WHERE id=?`, next, s.id); err != nil {
			return err
		}
		next++
		if s.status != hostDone {
			if _, err := r.setStatus(s.id, work, hostCancelled); err != nil {
				return err
			}
		}
	}
	if err := r.orderEdges(work, order); err != nil {
		return err
	}
	return r.settle(work)
}

func (r *reducer) sameList(work string, items []PlanItem) (bool, error) {
	steps, err := r.workSteps(work)
	if err != nil {
		return false, err
	}
	var listed []stepRow
	for _, s := range steps {
		if s.status != hostCancelled {
			listed = append(listed, s)
		}
	}
	if len(listed) != len(items) {
		return false, nil
	}
	for i, it := range items {
		if listed[i].title != clip(it.Text, stepTitleMax) || listed[i].status != hostStatus(it.Status) {
			return false, nil
		}
	}
	return true, nil
}

// orderEdges makes each listed step come after the one before it (source order).
func (r *reducer) orderEdges(work string, order []string) error {
	type edge struct{ step, after string }
	want := map[edge]bool{}
	for i := 1; i < len(order); i++ {
		want[edge{order[i], order[i-1]}] = true
	}
	rows, err := r.tx.Query(`SELECT e.step_id, e.after_id FROM edges e JOIN steps s ON s.id=e.step_id WHERE s.work_id=? AND e.source='order'`, work)
	if err != nil {
		return err
	}
	have := map[edge]bool{}
	for rows.Next() {
		var e edge
		_ = rows.Scan(&e.step, &e.after)
		have[e] = true
	}
	rows.Close()
	for e := range have {
		if !want[e] {
			if err := r.exec(`DELETE FROM edges WHERE step_id=? AND after_id=?`, e.step, e.after); err != nil {
				return err
			}
			r.touchWork(work)
		}
	}
	for i := 1; i < len(order); i++ {
		if e := (edge{order[i], order[i-1]}); !have[e] {
			if err := r.addEdge(e.step, e.after, "order"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *reducer) planOps() error {
	var p PlanOps
	if err := r.payload(&p); err != nil || p.List == "" {
		return err
	}
	work, err := r.listWork(p.List, p.ID, false)
	if err != nil || work == "" {
		return err
	}
	all, err := r.workSteps(work)
	if err != nil {
		return err
	}
	var listed []stepRow
	rows, err := r.tx.Query(`SELECT id FROM steps WHERE work_id=? AND listed=1 ORDER BY ord`, work)
	if err != nil {
		return err
	}
	isListed := map[string]bool{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		isListed[id] = true
	}
	rows.Close()
	for _, s := range all {
		if isListed[s.id] {
			listed = append(listed, s)
		}
	}
	set := func(i int, status string) error {
		if i < 0 || i >= len(listed) {
			return nil
		}
		if _, err := r.setStatus(listed[i].id, work, status); err != nil {
			return err
		}
		listed[i].status = status
		return r.touchStep(listed[i].id, work)
	}
	for _, n := range p.Done {
		if err := set(n-1, hostDone); err != nil {
			return err
		}
	}
	if p.Advance {
		cur := -1
		for i, s := range listed {
			if s.status == hostActive {
				cur = i
				break
			}
		}
		if cur >= 0 {
			if err := set(cur, hostDone); err != nil {
				return err
			}
		}
		for i := cur + 1; i < len(listed); i++ {
			if listed[i].status == hostPending {
				if err := set(i, hostActive); err != nil {
					return err
				}
				break
			}
		}
	}
	if p.Start > 0 {
		if err := set(p.Start-1, hostActive); err != nil {
			return err
		}
	}
	return r.settle(work)
}
