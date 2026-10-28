package work

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

type tw struct {
	t  *testing.T
	s  *Store
	at int64
}

func newTW(t *testing.T) *tw {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "work.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	base := time.Date(2026, 10, 28, 5, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }
	return &tw{t: t, s: s, at: base.UnixMilli()}
}

func (w *tw) ev(kind, host, session string, payload any) Result {
	w.t.Helper()
	w.at += 1000
	e := NewEvent(kind, host, session, w.at, payload)
	e.Cwd = "/repo"
	res, err := w.s.Append(e)
	if err != nil {
		w.t.Fatalf("%s: %v", kind, err)
	}
	return res
}

// evAt appends an event fired at a given time (it may arrive out of order).
func (w *tw) evAt(at int64, kind, host, session string, payload any) Result {
	w.t.Helper()
	e := NewEvent(kind, host, session, at, payload)
	e.Cwd = "/repo"
	res, err := w.s.Append(e)
	if err != nil {
		w.t.Fatalf("%s: %v", kind, err)
	}
	return res
}

func (w *tw) claude(kind string, payload any) Result { return w.ev(kind, "claude", "S1", payload) }

func (w *tw) start(host, session string, p SessionStart) {
	w.ev(KindSessionStart, host, session, p)
}

func str(s string) *string { return &s }

func (w *tw) only() Work {
	w.t.Helper()
	page, err := w.s.ListWorks(Filter{}, ReadOptions{})
	if err != nil {
		w.t.Fatal(err)
	}
	if len(page.Items) != 1 {
		w.t.Fatalf("작업 수 %d, 하나여야 합니다", len(page.Items))
	}
	doc, _, err := w.s.GetWork(page.Items[0].ID, false, ReadOptions{})
	if err != nil {
		w.t.Fatal(err)
	}
	return doc
}

func statuses(w Work) []string {
	var out []string
	for _, s := range w.Steps {
		out = append(out, s.Status)
	}
	return out
}

func eq[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()
	b1, _ := json.Marshal(got)
	b2, _ := json.Marshal(want)
	if string(b1) != string(b2) {
		t.Errorf("%s: %s, 원하는 값 %s", what, b1, b2)
	}
}

// 끝 기준: TaskCreate 셋(셋째가 첫째·둘째를 선행으로)을 차례로 진행·완료하면
// 단계 셋, native 간선 둘, 층 0·0·1, 모두 done, 작업 done.
func TestClaudeTasksMakeGraph(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{Source: "startup", Surface: "headless", Project: Project{Scope: "project:x", Label: "x"}})
	for i, s := range []string{"A 준비", "B 준비", "C 합치기"} {
		res := w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: string(rune('1' + i)), Subject: s})
		if i == 0 && len(res.Opened) != 1 {
			t.Fatalf("첫 TaskCreate가 작업을 열어야 합니다: %v", res.Opened)
		}
		if i > 0 && len(res.Opened) != 0 {
			t.Fatalf("같은 작업에 붙어야 합니다")
		}
	}
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "3", AddBlockedBy: []string{"1", "2"}})
	doc := w.only()
	eq(t, "처음 상태", statuses(doc), []string{"ready", "ready", "pending"})
	if doc.TitleSource != "step" || doc.Title != "A 준비" {
		t.Errorf("제목 전: %q %q", doc.Title, doc.TitleSource)
	}
	for _, id := range []string{"1", "2", "3"} {
		w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: id, Status: str("in_progress")})
		w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: id, Status: str("completed")})
	}
	doc = w.only()
	eq(t, "상태", statuses(doc), []string{"done", "done", "done"})
	eq(t, "층", []int{doc.Steps[0].Wave, doc.Steps[1].Wave, doc.Steps[2].Wave}, []int{0, 0, 1})
	if len(doc.Steps[2].After) != 2 || doc.Steps[2].After[0].Source != "native" || doc.Steps[2].After[1].Source != "native" {
		t.Errorf("선행 간선: %+v", doc.Steps[2].After)
	}
	if doc.State != "done" || doc.ClosedAt == "" {
		t.Errorf("작업 상태 %s %q", doc.State, doc.ClosedAt)
	}
	if doc.Steps[0].Native == nil || doc.Steps[0].Native.ID != "1" || doc.Origin.List.Kind != ListClaudeTask {
		t.Errorf("native: %+v %+v", doc.Steps[0].Native, doc.Origin)
	}
	// 닫힌 뒤 새 항목은 새 작업
	if res := w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "4", Subject: "다음 일"}); len(res.Opened) != 1 {
		t.Errorf("닫힌 뒤 새 항목이 새 작업을 열지 않았습니다")
	}
}

// 기억 m-6a9286af2233: 늦게 온 만들기는 진행·완료를 되돌리지 않는다.
func TestLateCreateDoesNotRegress(t *testing.T) {
	w := newTW(t)
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "1", Status: str("in_progress"), Subject: str("A")})
	w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "1", Subject: "A"})
	doc := w.only()
	eq(t, "상태", statuses(doc), []string{"in_progress"})
	if len(doc.Steps) != 1 {
		t.Fatalf("단계 %d", len(doc.Steps))
	}
}

// 끝 기준: 순환이 생기는 TaskUpdate는 그 간선만 버리고 나머지 간선과 층은 그대로, 버린 간선은 사건으로 남는다.
func TestCycleEdgeDropped(t *testing.T) {
	w := newTW(t)
	for _, id := range []string{"1", "2", "3"} {
		w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: id, Subject: "단계 " + id})
	}
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "2", AddBlockedBy: []string{"1"}})
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "3", AddBlockedBy: []string{"2"}})
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "1", AddBlockedBy: []string{"3"}}) // 순환
	doc := w.only()
	eq(t, "층", []int{doc.Steps[0].Wave, doc.Steps[1].Wave, doc.Steps[2].Wave}, []int{0, 1, 2})
	if len(doc.Steps[0].After) != 0 {
		t.Errorf("순환 간선이 들어갔습니다: %+v", doc.Steps[0].After)
	}
	var n int
	_ = w.s.db.QueryRow(`SELECT COUNT(*) FROM ledger WHERE kind=? AND derived=1`, KindEdgeDropped).Scan(&n)
	if n != 1 {
		t.Errorf("버린 간선 사건 %d건", n)
	}
	// 다시 만들어도 같다(버린 사건을 두 번 남기지 않는다)
	before := w.only()
	if err := w.s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	after := w.only()
	b1, _ := json.Marshal(before)
	b2, _ := json.Marshal(after)
	if string(b1) != string(b2) {
		t.Errorf("다시 만든 그래프가 다릅니다\n%s\n%s", b1, b2)
	}
	_ = w.s.db.QueryRow(`SELECT COUNT(*) FROM ledger WHERE kind=?`, KindEdgeDropped).Scan(&n)
	if n != 1 {
		t.Errorf("다시 만든 뒤 버린 간선 사건 %d건", n)
	}
}

func plan(items ...string) PlanSnapshot {
	p := PlanSnapshot{List: ListCodexPlan, ID: "C1"}
	for i := 0; i+1 < len(items); i += 2 {
		p.Items = append(p.Items, PlanItem{Text: items[i], Status: items[i+1]})
	}
	return p
}

// 끝 기준: update_plan 세 항목을 차례로 완료하면 단계 셋, order 간선 둘, 상태 변화.
func TestCodexPlanSnapshots(t *testing.T) {
	w := newTW(t)
	cx := func(p PlanSnapshot) Result { return w.ev(KindPlanSnapshot, "codex", "C1", p) }
	if res := cx(plan("A", "in_progress", "B", "pending", "C", "pending")); len(res.Opened) != 1 {
		t.Fatal("첫 스냅숏이 작업을 열어야 합니다")
	}
	doc := w.only()
	eq(t, "처음", statuses(doc), []string{"in_progress", "pending", "pending"})
	cx(plan("A", "completed", "B", "in_progress", "C", "pending"))
	cx(plan("A", "completed", "B", "completed", "C", "in_progress"))
	cx(plan("A", "completed", "B", "completed", "C", "completed"))
	doc = w.only()
	eq(t, "끝", statuses(doc), []string{"done", "done", "done"})
	if len(doc.Steps[1].After) != 1 || doc.Steps[1].After[0].Source != "order" || doc.Steps[1].After[0].StepID != doc.Steps[0].ID ||
		len(doc.Steps[2].After) != 1 || doc.Steps[2].After[0].StepID != doc.Steps[1].ID {
		t.Errorf("순서 간선: %+v %+v", doc.Steps[1].After, doc.Steps[2].After)
	}
	if doc.State != "done" {
		t.Errorf("작업 %s", doc.State)
	}
	if doc.Steps[0].Native != nil || doc.Origin.List.ID != "" {
		t.Errorf("스냅숏 목록에는 호스트 번호가 없습니다")
	}
	// 같은 목록을 다시 보내도 새 작업이 아니다
	if res := cx(plan("A", "completed", "B", "completed", "C", "completed")); len(res.Opened) != 0 {
		t.Errorf("닫힌 작업과 같은 목록이 새 작업을 열었습니다")
	}
}

// 끝 기준: 글 하나를 바꾸면 옛 단계는 cancelled, 새 글은 새 단계. 글이 같으면 순서가 바뀌어도 같은 단계.
func TestSnapshotMatchesByText(t *testing.T) {
	w := newTW(t)
	cx := func(p PlanSnapshot) { w.ev(KindPlanSnapshot, "codex", "C1", p) }
	cx(plan("A", "pending", "B", "pending", "C", "pending"))
	first := w.only()
	ids := map[string]string{}
	for _, s := range first.Steps {
		ids[s.Title] = s.ID
	}
	cx(plan("C", "in_progress", "A", "pending", "B2", "pending"))
	doc := w.only()
	got := map[string]Step{}
	for _, s := range doc.Steps {
		got[s.Title] = s
	}
	if got["C"].ID != ids["C"] || got["A"].ID != ids["A"] {
		t.Errorf("같은 글이 다른 단계가 됐습니다")
	}
	if got["B"].Status != "cancelled" {
		t.Errorf("바뀐 글의 옛 단계: %s", got["B"].Status)
	}
	if got["B2"].ID == "" || got["B2"].ID == ids["B"] {
		t.Errorf("새 글이 새 단계가 아닙니다")
	}
	if got["C"].Order != 0 || got["A"].Order != 1 || got["A"].After[0].StepID != got["C"].ID {
		t.Errorf("순서가 새 목록을 따르지 않습니다: C=%d A=%d %+v", got["C"].Order, got["A"].Order, got["A"].After)
	}
	if len(got["B"].After) != 0 {
		t.Errorf("빠진 단계에 순서 간선이 남았습니다")
	}
	// 같은 글이 두 번이면 둘 다 잇는다
	cx(plan("C", "in_progress", "A", "pending", "B2", "pending", "A", "pending"))
	doc = w.only()
	n := 0
	for _, s := range doc.Steps {
		if s.Title == "A" && s.Status != "cancelled" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("같은 글 두 항목: %d", n)
	}
}

func TestAppPlanOps(t *testing.T) {
	w := newTW(t)
	ap := func(kind string, p any) { w.ev(kind, "claude", "D1", p) }
	ap(KindPlanSnapshot, PlanSnapshot{List: ListAppPlan, ID: "D1", Items: []PlanItem{{"A", "in_progress", ""}, {"B", "pending", ""}, {"C", "pending", ""}}})
	ap(KindPlanOps, PlanOps{List: ListAppPlan, ID: "D1", Advance: true})
	eq(t, "다음", statuses(w.only()), []string{"done", "in_progress", "pending"})
	ap(KindPlanOps, PlanOps{List: ListAppPlan, ID: "D1", Done: []int{2}, Start: 3})
	eq(t, "완료·시작", statuses(w.only()), []string{"done", "done", "in_progress"})
	ap(KindPlanOps, PlanOps{List: ListAppPlan, ID: "D1", Advance: true})
	doc := w.only()
	eq(t, "끝", statuses(doc), []string{"done", "done", "done"})
	if doc.State != "done" {
		t.Errorf("작업 %s", doc.State)
	}
}

// 앱의 wrap_up은 목록을 닫는다: 진행 중인 단계를 끝내고 다음 단계를 시작하지 않는다.
func TestAppPlanFinish(t *testing.T) {
	w := newTW(t)
	ap := func(kind string, p any) { w.ev(kind, "claude", "D1", p) }
	ap(KindPlanSnapshot, PlanSnapshot{List: ListAppPlan, ID: "D1", Items: []PlanItem{{"A", "done", ""}, {"B", "in_progress", ""}}})
	ap(KindPlanOps, PlanOps{List: ListAppPlan, ID: "D1", Finish: true})
	doc := w.only()
	eq(t, "끝", statuses(doc), []string{"done", "done"})
	if doc.State != "done" {
		t.Errorf("작업 %s", doc.State)
	}

	w = newTW(t)
	ap(KindPlanSnapshot, PlanSnapshot{List: ListAppPlan, ID: "D1", Items: []PlanItem{{"A", "in_progress", ""}, {"B", "pending", ""}}})
	ap(KindPlanOps, PlanOps{List: ListAppPlan, ID: "D1", Finish: true})
	doc = w.only()
	eq(t, "남은 단계", statuses(doc), []string{"done", "ready"}) // 시작하지 않았다
	if doc.State == "done" {
		t.Error("남은 단계가 있는데 작업이 끝났습니다")
	}
}

func TestDeclaredBlock(t *testing.T) {
	w := newTW(t)
	w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "1", Subject: "A"})
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "1", Status: str("in_progress"), Blocked: str("Codex 훅 신뢰 필요")})
	doc := w.only()
	if doc.Steps[0].Status != "blocked" || doc.Steps[0].Block.Reason != "declared" || doc.Steps[0].Block.Detail != "Codex 훅 신뢰 필요" {
		t.Errorf("밝힌 막힘: %+v", doc.Steps[0])
	}
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "1", Blocked: str("")})
	if s := w.only().Steps[0]; s.Status != "in_progress" || s.Block != nil {
		t.Errorf("풀리지 않았습니다: %+v", s)
	}
}

// TaskList는 현재 상태: 상태·제목·선행을 맞추고, 목록에서 빠진 열린 항목은 지워진 것이다.
func TestTaskListReconciles(t *testing.T) {
	w := newTW(t)
	w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "1", Subject: "A"})
	w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "2", Subject: "B"})
	w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "3", Subject: "C"})
	w.claude(KindTaskState, TaskState{List: "S1", Full: true, Tasks: []TaskItem{
		{ID: "1", Subject: "A", Status: "completed", BlockedBy: []string{}},
		{ID: "3", Subject: "C 고침", Status: "in_progress", BlockedBy: []string{"1"}},
	}})
	doc := w.only()
	eq(t, "상태", statuses(doc), []string{"done", "cancelled", "in_progress"})
	if doc.Steps[2].Title != "C 고침" || len(doc.Steps[2].After) != 1 {
		t.Errorf("맞춤: %+v", doc.Steps[2])
	}
}

func TestTitle(t *testing.T) {
	w := newTW(t)
	w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: "1", Subject: "첫 단계"})
	id := w.only().ID
	if _, err := w.s.SetTitle(id, "작업 제목"); err != nil {
		t.Fatal(err)
	}
	doc := w.only()
	if doc.Title != "작업 제목" || doc.TitleSource != "ai" || doc.TitleUpdatedAt == "" {
		t.Errorf("제목: %q %q %q", doc.Title, doc.TitleSource, doc.TitleUpdatedAt)
	}
	if _, err := w.s.SetTitle("w-없음", "x"); err != ErrNotFound {
		t.Errorf("없는 작업: %v", err)
	}
	// 제목은 원장 사건이라 다시 만들어도 남는다
	if err := w.s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if w.only().Title != "작업 제목" {
		t.Error("다시 만든 뒤 제목이 사라졌습니다")
	}
}

func TestOneLineMasks(t *testing.T) {
	got := Clean("고칠 것\n  password=hunter2 과 sk-abcdefghijklmnopqrstuv", TitleMax)
	if got != "고칠 것 password=[가림] 과 [가림]" {
		t.Errorf("%q", got)
	}
	long := Clean(string(make([]rune, 0))+repeat("가", 300), TitleMax)
	if n := len([]rune(long)); n != TitleMax {
		t.Errorf("길이 %d", n)
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// TaskList의 blockedBy는 끝나지 않은 선행만 준다(탐침 PostToolUse.TaskList.1). 끝난 선행으로 가는 간선은 지우지 않는다.
func TestTaskListKeepsDoneEdges(t *testing.T) {
	w := newTW(t)
	for _, id := range []string{"1", "2", "3", "4"} {
		w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: id, Subject: "단계 " + id})
	}
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "3", AddBlockedBy: []string{"1", "2", "4"}})
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "1", Status: str("completed")})
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "2", Status: str("completed")})
	w.claude(KindTaskState, TaskState{List: "S1", Full: true, Tasks: []TaskItem{
		{ID: "1", Subject: "단계 1", Status: "completed", BlockedBy: []string{}},
		{ID: "2", Subject: "단계 2", Status: "completed", BlockedBy: []string{}},
		{ID: "3", Subject: "단계 3", Status: "pending", BlockedBy: []string{}},
		{ID: "4", Subject: "단계 4", Status: "pending", BlockedBy: []string{}},
	}})
	s := w.only().Steps[2]
	// 끝난 1·2로 가는 간선은 남고, 열린 4로 가는 간선은 목록에서 빠졌으니 지운 것이다
	if len(s.After) != 2 || s.Wave != 1 || s.Status != "ready" {
		t.Errorf("단계 3: after %d, wave %d, %s", len(s.After), s.Wave, s.Status)
	}
	// TaskGet은 전체 선행을 준다
	w.claude(KindTaskState, TaskState{List: "S1", Tasks: []TaskItem{{ID: "3", Subject: "단계 3", Status: "pending", BlockedBy: []string{"1"}}}})
	if s := w.only().Steps[2]; len(s.After) != 1 {
		t.Errorf("TaskGet 뒤 after %d", len(s.After))
	}
}
