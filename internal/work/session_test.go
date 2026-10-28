package work

import (
	"strings"
	"testing"
)

func (w *tw) sessionDoc(host, id string) Session {
	w.t.Helper()
	d, err := w.s.SessionEvents(id, "", 0, ReadOptions{})
	if err != nil {
		w.t.Fatal(err)
	}
	return d.Session
}

func activeTask(w *tw, ids ...string) {
	for _, id := range ids {
		w.claude(KindTaskCreate, TaskCreate{List: "S1", ID: id, Subject: "단계 " + id})
	}
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: ids[0], Status: str("in_progress")})
}

// 끝 기준: 권한 요청 뒤 진행 단계가 blocked(waiting_user), 같은 도구의 사후 사건 뒤 in_progress.
// 도착 순서를 섞어도 같은 결과(순열 시험).
func TestWaitingUserPermutations(t *testing.T) {
	type step struct {
		kind    string
		payload any
	}
	open := step{KindWaitOpen, Wait{Key: "k1", Prompt: "P1", Tool: "Bash"}}
	done := step{KindToolDone, ToolDone{Key: "k1", Tool: "Bash"}}
	stop := step{KindTurnEnd, Turn{Prompt: "P1"}}
	other := step{KindToolDone, ToolDone{Key: "k2", Tool: "Read"}}
	cases := []struct {
		name    string
		events  []step
		blocked bool
	}{
		{"요청만", []step{open}, true},
		{"요청·다른 도구", []step{open, other}, true},
		{"다른 도구·요청", []step{other, open}, true},
		{"요청·사후", []step{open, done}, false},
		{"사후·요청(늦게 온 요청)", []step{done, open}, false},
		{"요청·턴 끝", []step{open, stop}, false},
		{"턴 끝·요청", []step{stop, open}, false},
		{"사후·턴 끝·요청", []step{done, stop, open}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newTW(t)
			w.claude(KindTurnStart, Turn{Prompt: "P1"})
			activeTask(w, "1")
			for _, e := range c.events {
				w.claude(e.kind, e.payload)
			}
			s := w.only().Steps[0]
			if got := s.Status == "blocked" && s.Block.Reason == "waiting_user"; got != c.blocked {
				t.Errorf("막힘 %v, 원하는 값 %v (%+v)", got, c.blocked, s)
			}
			state := w.sessionDoc("claude", "S1").State
			if c.blocked && state != "waiting_user" || !c.blocked && state == "waiting_user" {
				t.Errorf("세션 상태 %s", state)
			}
		})
	}
}

func TestSessionStates(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{Source: "startup", Surface: "cli"})
	if st := w.sessionDoc("claude", "S1").State; st != "idle" {
		t.Errorf("시작 뒤 %s", st)
	}
	w.claude(KindTurnStart, Turn{Prompt: "P1"})
	if st := w.sessionDoc("claude", "S1").State; st != "running" {
		t.Errorf("요청 뒤 %s", st)
	}
	w.claude(KindTurnEnd, Turn{Prompt: "P1"})
	w.claude(KindTurnEnd, Turn{Prompt: "P1"}) // Stop 여러 번
	if st := w.sessionDoc("claude", "S1").State; st != "idle" {
		t.Errorf("턴 끝 뒤 %s", st)
	}
	d, _ := w.s.SessionEvents("S1", "", 0, ReadOptions{})
	n := 0
	for _, e := range d.Events {
		if e.Kind == "turn.ended" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("turn.ended %d건", n)
	}
}

// 끝 기준: 진행 중 단계 하나에 백그라운드 서브에이전트를 맡기면 single_active로 이어지고 끝나면 finished.
// 끝 사건 없이 세션이 끝나면 실행 lost, 단계 막힘 run_lost.
func TestSubagentRuns(t *testing.T) {
	w := newTW(t)
	activeTask(w, "1")
	w.claude(KindRunStart, RunStart{Agent: "a1", Type: "orbit:worker"})
	w.claude(KindRunTool, RunTool{ToolUse: "tu1", Agent: "a1", Type: "orbit:worker", Background: true, OK: true})
	doc := w.only()
	if len(doc.Runs) != 1 || doc.Runs[0].Link != "single_active" || doc.Runs[0].StepID != doc.Steps[0].ID || !doc.Runs[0].Background || doc.Runs[0].State != "running" {
		t.Fatalf("실행: %+v", doc.Runs)
	}
	if a := doc.Steps[0].Assignee; a == nil || a.Kind != "subagent" || a.Role != "orbit:worker" {
		t.Errorf("담당: %+v", a)
	}
	w.claude(KindRunStop, RunStop{Agent: "a1", Type: "orbit:worker", Transcript: "/t/agent-a1.jsonl"})
	w.claude(KindRunStop, RunStop{Agent: "a1", Type: "orbit:worker"}) // 끝이 두 번 와도 합친다
	doc = w.only()
	if doc.Runs[0].State != "finished" || doc.Runs[0].EndReason != "completed" || doc.Steps[0].Status != "in_progress" {
		t.Errorf("끝난 뒤: %+v / %s", doc.Runs[0], doc.Steps[0].Status)
	}
	// 단계의 시작·끝은 호스트 목록 갱신만 바꾼다
	if doc.Steps[0].FinishedAt != "" {
		t.Error("서브에이전트 끝이 단계를 끝냈습니다")
	}

	w2 := newTW(t)
	activeTask(w2, "1")
	w2.claude(KindRunStart, RunStart{Agent: "a2", Type: "orbit:worker"})
	w2.claude(KindSessionEnd, SessionEnd{Reason: "other"})
	doc = w2.only()
	if doc.Runs[0].State != "lost" || doc.Runs[0].EndReason != "session_ended" {
		t.Errorf("잃음: %+v", doc.Runs[0])
	}
	if s := doc.Steps[0]; s.Status != "blocked" || s.Block.Reason != "run_lost" {
		t.Errorf("막힘: %+v", s)
	}
}

func TestOwnerLinkAndFailure(t *testing.T) {
	w := newTW(t)
	activeTask(w, "1", "2")
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "2", Status: str("in_progress"), Owner: str("orbit:gate")})
	w.claude(KindRunStart, RunStart{Agent: "g1", Type: "orbit:gate"})
	w.claude(KindRunStart, RunStart{Agent: "x1", Type: "Explore"}) // 진행 단계가 둘이고 주인이 없으면 잇지 않는다
	w.claude(KindRunTool, RunTool{ToolUse: "tu9", Type: "orbit:gate", OK: false})
	doc := w.only()
	var gate Run
	for _, r := range doc.Runs {
		if r.Role == "orbit:gate" {
			gate = r
		}
	}
	if gate.Link != "owner" || gate.StepID != doc.Steps[1].ID || gate.State != "failed" {
		t.Errorf("gate 실행: %+v", gate)
	}
	if doc.Steps[1].Status != "blocked" || doc.Steps[1].Block.Reason != "run_failed" {
		t.Errorf("실패 막힘: %+v", doc.Steps[1])
	}
	d, _ := w.s.SessionEvents("S1", "", 0, ReadOptions{})
	found := false
	for _, r := range d.Runs {
		if r.Role == "Explore" && r.StepID == "" && r.Link == "none" {
			found = true
		}
	}
	if !found {
		t.Error("단계에 잇지 못한 실행이 세션 응답에 없습니다")
	}
	// 같은 단계에 새 실행이 시작되면 실패 막힘이 풀린다
	w.claude(KindRunStart, RunStart{Agent: "g2", Type: "orbit:gate"})
	if s := w.only().Steps[1]; s.Status != "in_progress" {
		t.Errorf("새 실행 뒤: %+v", s)
	}
}

// 끝 기준: 서브에이전트 안 시험 명령 성공·실패와 커밋 → 그 단계에 command 둘(ok·fail)·commit 하나.
// gate가 "수정 필요"를 내면 fail, 그 뒤 커밋이 생기면 outdated.
func TestEvidenceAndVerification(t *testing.T) {
	w := newTW(t)
	activeTask(w, "1")
	w.claude(KindRunStart, RunStart{Agent: "a1", Type: "orbit:worker"})
	w.claude(KindCommand, Command{Text: "go test ./...", OK: true, Agent: "a1"})
	w.claude(KindCommand, Command{Text: "go test ./...", OK: false, Agent: "a1"})
	w.claude(KindCommit, Commit{Repo: "/repo", SHA: "3b9e0c1f7a2d4e6b8c0a1f3e5d7b9c2a4e6f8a0b", Subject: "feat: X", Files: []string{"a.go"}, Agent: "a1"})
	doc := w.only()
	kinds := map[string][]string{}
	for _, e := range doc.Evidence {
		if e.StepID != doc.Steps[0].ID {
			t.Errorf("증거가 다른 단계에: %+v", e)
		}
		kinds[e.Kind] = append(kinds[e.Kind], e.Result)
	}
	eq(t, "command", kinds["command"], []string{"ok", "fail"})
	eq(t, "commit", kinds["commit"], []string{"ok"})
	if doc.Evidence[2].Ref == nil || doc.Evidence[2].Ref.SHA != "3b9e0c1f7a2d4e6b8c0a1f3e5d7b9c2a4e6f8a0b" || doc.Evidence[2].Files[0] != "a.go" {
		t.Errorf("커밋 증거: %+v", doc.Evidence[2])
	}
	// 판정이 없으면 가장 최근 시험 명령(실패). 그 뒤 커밋이 있으니 지난 검증
	if v := doc.Steps[0].Verification; v.State != "fail" || !v.Outdated {
		t.Errorf("검증: %+v", v)
	}
	w.claude(KindRunStart, RunStart{Agent: "g1", Type: "orbit:gate"})
	w.claude(KindRunStop, RunStop{Agent: "g1", Type: "orbit:gate", Verdict: &Verdict{Role: "orbit:gate", Result: "needs_fix"}})
	if v := w.only().Steps[0].Verification; v.State != "fail" || v.Outdated {
		t.Errorf("판정 뒤: %+v", v)
	}
	w.claude(KindCommit, Commit{Repo: "/repo", SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Subject: "fix: Y", Agent: "a1"})
	if v := w.only().Steps[0].Verification; v.State != "fail" || !v.Outdated {
		t.Errorf("판정 뒤 커밋: %+v", v)
	}
	w.claude(KindRunStart, RunStart{Agent: "g2", Type: "orbit:gate"})
	w.claude(KindRunStop, RunStop{Agent: "g2", Type: "orbit:gate", Verdict: &Verdict{Role: "orbit:gate", Result: "pass"}})
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "1", Status: str("completed")})
	doc = w.only()
	if v := doc.Steps[0].Verification; v.State != "pass" || v.Outdated || doc.Steps[0].Status != "done" {
		t.Errorf("통과: %+v %s", v, doc.Steps[0].Status)
	}
}

func TestSessionLevelEvidence(t *testing.T) {
	w := newTW(t)
	activeTask(w, "1", "2")
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "2", Status: str("in_progress")})
	w.claude(KindCommand, Command{Text: "npm test", OK: true}) // 진행 단계가 둘: 세션 수준
	doc := w.only()
	if len(doc.Evidence) != 0 {
		t.Errorf("단계에 붙었습니다: %+v", doc.Evidence)
	}
	d, _ := w.s.SessionEvents("S1", "", 0, ReadOptions{})
	if len(d.Evidence) != 1 || d.Evidence[0].StepID != "" {
		t.Errorf("세션 수준 증거: %+v", d.Evidence)
	}
}

// 끝 기준: 진행 중 단계가 있는 세션에 SessionEnd가 오면 단계 blocked(interrupted), 세션 ended(session_end).
// 같은 작업 목록을 이어 쓰는 재개 세션이 그 단계를 갱신하면 같은 작업·같은 단계로 이어지고 풀린다.
func TestInterruptedAndResume(t *testing.T) {
	for _, mode := range []string{"같은 세션 id", "같은 목록 id의 새 세션"} {
		t.Run(mode, func(t *testing.T) {
			w := newTW(t)
			w.start("claude", "S1", SessionStart{Source: "startup", ListID: "L1"})
			w.ev(KindTaskCreate, "claude", "S1", TaskCreate{List: "L1", ID: "1", Subject: "A"})
			w.ev(KindTaskUpdate, "claude", "S1", TaskUpdate{List: "L1", ID: "1", Status: str("in_progress")})
			w.ev(KindSessionEnd, "claude", "S1", SessionEnd{Reason: "prompt_input_exit"})
			doc := w.only()
			if s := doc.Steps[0]; s.Status != "blocked" || s.Block.Reason != "interrupted" {
				t.Fatalf("중단: %+v", s)
			}
			sess := w.sessionDoc("claude", "S1")
			if sess.State != "ended" || sess.EndReason != "session_end" || sess.EndDetail != "prompt_input_exit" || len(sess.OpenWorkIDs) != 1 {
				t.Errorf("세션: %+v", sess)
			}
			next := "S1"
			if mode != "같은 세션 id" {
				next = "S2"
			}
			w.start("claude", next, SessionStart{Source: "resume", ListID: "L1"})
			res := w.ev(KindTaskUpdate, "claude", next, TaskUpdate{List: "L1", ID: "1", Status: str("completed")})
			if len(res.Opened) != 0 {
				t.Error("재개가 새 작업을 열었습니다")
			}
			after := w.only()
			if after.ID != doc.ID || after.Steps[0].ID != doc.Steps[0].ID || after.Steps[0].Status != "done" {
				t.Errorf("재개 뒤: %+v", after.Steps[0])
			}
			if w.sessionDoc("claude", next).State == "ended" {
				t.Error("재개한 세션이 끝난 상태입니다")
			}
		})
	}
}

func TestInterruptedClearsOnUpdate(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{ListID: "L1"})
	w.ev(KindTaskCreate, "claude", "S1", TaskCreate{List: "L1", ID: "1", Subject: "A"})
	w.ev(KindTaskUpdate, "claude", "S1", TaskUpdate{List: "L1", ID: "1", Status: str("in_progress")})
	w.ev(KindSessionEnd, "claude", "S1", SessionEnd{})
	w.start("claude", "S2", SessionStart{ListID: "L1"})
	w.ev(KindTaskUpdate, "claude", "S2", TaskUpdate{List: "L1", ID: "1", ActiveForm: str("다시 하는 중")})
	if s := w.only().Steps[0]; s.Status != "in_progress" || s.Block != nil {
		t.Errorf("갱신 뒤: %+v", s)
	}
}

// 하위 세션(ORBIT_PARENT): 부모 단계에 kind=session 실행과 자식 작업이 붙는다.
func TestChildSession(t *testing.T) {
	w := newTW(t)
	activeTask(w, "1")
	w.start("codex", "C9", SessionStart{Surface: "headless", Parent: &ParentRef{Host: "claude", Session: "S1", Step: "1"}})
	w.ev(KindPlanSnapshot, "codex", "C9", plan("하위 일", "in_progress"))
	page, _ := w.s.ListWorks(Filter{}, ReadOptions{})
	if len(page.Items) != 2 {
		t.Fatalf("작업 %d", len(page.Items))
	}
	var parent Work
	for _, it := range page.Items {
		if it.Parent == nil {
			parent, _, _ = w.s.GetWork(it.ID, true, ReadOptions{})
		}
	}
	st := parent.Steps[0]
	if len(st.ChildWorkIDs) != 1 || len(parent.Children) != 1 || parent.Children[0].Parent.StepID != st.ID {
		t.Fatalf("자식 작업: %+v / %d", st.ChildWorkIDs, len(parent.Children))
	}
	var run Run
	for _, r := range parent.Runs {
		if r.Kind == "session" {
			run = r
		}
	}
	if run.Link != "parent_env" || run.SessionID != "C9" || run.ParentSessionID != "S1" || run.Host != "codex" || run.State != "running" {
		t.Errorf("하위 세션 실행: %+v", run)
	}
	if a := st.Assignee; a == nil || a.Kind != "session" || a.Host != "codex" {
		t.Errorf("담당: %+v", a)
	}
	w.ev(KindSessionEnd, "codex", "C9", SessionEnd{})
	parent, _, _ = w.s.GetWork(parent.ID, false, ReadOptions{})
	for _, r := range parent.Runs {
		if r.Kind == "session" && (r.State != "finished" || r.EndReason != "completed") {
			t.Errorf("하위 세션 끝: %+v", r)
		}
	}
	if sd := w.sessionDoc("codex", "C9"); sd.ParentSessionID != "S1" {
		t.Errorf("부모 세션: %+v", sd)
	}
}

func TestEventsHoldNoBodies(t *testing.T) {
	w := newTW(t)
	activeTask(w, "1")
	w.claude(KindRunStart, RunStart{Agent: "a1", Type: "orbit:gate"})
	w.claude(KindRunStop, RunStop{Agent: "a1", Type: "orbit:gate", Transcript: "/t/agent-a1.jsonl", Verdict: &Verdict{Role: "orbit:gate", Result: "pass", Line: "판정 줄 원문 Q7"}})
	d, _ := w.s.SessionEvents("S1", "", 0, ReadOptions{})
	for _, e := range d.Events {
		if strings.Contains(e.Summary, "원문 Q7") {
			t.Errorf("판정 줄 전문이 사건에 들어갔습니다: %+v", e)
		}
		if e.Kind == "run.ended" && (e.Ref == nil || e.Ref.AgentTranscript != "/t/agent-a1.jsonl") {
			t.Errorf("서브에이전트 끝 사건에 대화 기록 위치가 없습니다: %+v", e)
		}
	}
}
