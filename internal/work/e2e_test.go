package work

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 호스트 실행 끝 기준(설계 「끝 기준」). scripts/e2e-work.sh가 새 헤드리스 세션을 돌리고
// ORBIT_E2E_DIR에 기록 DB(home/work.db)와 세션 출력(<이름>.jsonl)을 남긴 뒤 이 시험을 부른다.
// 호스트 계정과 사용량이 들기 때문에 평소 go test ./...에서는 건너뛴다.

func e2eDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("ORBIT_E2E_DIR")
	if d == "" {
		t.Skip("ORBIT_E2E_DIR가 없어 호스트 실행 시험을 건너뜁니다(scripts/e2e-work.sh로 돌립니다)")
	}
	return d
}

func e2eStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(filepath.Join(dir, "home", "work.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// toolCalls reads tool names from a stream-json (claude -p) or --json (codex exec) output.
func toolCalls(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skip("세션 출력이 없어 건너뜁니다: " + path)
	}
	defer f.Close()
	var names []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var l struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
			Item struct {
				Type   string `json:"type"`
				Server string `json:"server"`
				Tool   string `json:"tool"`
			} `json:"item"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		for _, c := range l.Message.Content {
			if c.Type == "tool_use" {
				names = append(names, c.Name)
			}
		}
		if l.Type == "item.started" && l.Item.Type == "mcp_tool_call" {
			names = append(names, "mcp__"+l.Item.Server+"__"+l.Item.Tool)
		}
	}
	return names
}

func orbitCalls(names []string) []string {
	var out []string
	for _, n := range names {
		if strings.Contains(n, "orbit") && strings.HasPrefix(n, "mcp__") {
			out = append(out, n[strings.LastIndex(n, "__")+2:])
		}
	}
	return out
}

// beforeTitle rebuilds a copy of the ledger up to the first title.set, so the
// test sees what work_get gave before the AI named the work.
func beforeTitle(t *testing.T, s *Store, id string) Work {
	t.Helper()
	cp := filepath.Join(t.TempDir(), "before.db")
	if err := s.Backup(cp); err != nil {
		t.Fatal(err)
	}
	c, err := Open(cp)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var seq int64
	if err := c.db.QueryRow(`SELECT MIN(seq) FROM ledger WHERE kind=?`, KindTitle).Scan(&seq); err != nil || seq == 0 {
		t.Fatalf("work_title 사건이 없습니다: %v", err)
	}
	if _, err := c.db.Exec(`DELETE FROM ledger WHERE seq>=?`, seq); err != nil {
		t.Fatal(err)
	}
	if err := c.Rebuild(); err != nil {
		t.Fatal(err)
	}
	w, _, err := c.GetWork(id, false, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func worksOf(t *testing.T, s *Store, host string) []Work {
	t.Helper()
	p, err := s.ListWorks(Filter{Limit: 50}, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out []Work
	for _, it := range p.Items {
		if it.Origin.Host == host {
			w, _, err := s.GetWork(it.ID, false, ReadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, w)
		}
	}
	return out
}

// 끝 기준: Claude Code 헤드리스 세션에서 TaskCreate 셋(셋째가 첫째·둘째를 선행)을 차례로 진행·완료하면
// 단계 셋, native 간선 둘, 층 0·0·1, 모두 done, 작업 done. Orbit 도구 호출은 work_title뿐.
// 제목 요청 문맥을 한 번 받고, work_title 전에는 첫 단계 제목·step, 뒤에는 그 제목·ai.
func TestE2EClaudeTasks(t *testing.T) {
	dir := e2eDir(t)
	calls := orbitCalls(toolCalls(t, filepath.Join(dir, "claude-a.jsonl")))
	if strings.Join(calls, ",") != "work_title" {
		t.Errorf("Orbit 도구 호출: %v (work_title 한 번이어야 합니다)", calls)
	}
	s := e2eStore(t, filepath.Join(dir, "a"))
	ws := worksOf(t, s, "claude")
	if len(ws) != 1 {
		t.Fatalf("Claude 작업 %d개", len(ws))
	}
	w := ws[0]
	if len(w.Steps) != 3 {
		t.Fatalf("단계 %d개", len(w.Steps))
	}
	waves := []int{w.Steps[0].Wave, w.Steps[1].Wave, w.Steps[2].Wave}
	if waves[0] != 0 || waves[1] != 0 || waves[2] != 1 {
		t.Errorf("층 %v", waves)
	}
	native := 0
	for _, st := range w.Steps {
		for _, a := range st.After {
			if a.Source == "native" {
				native++
			}
		}
		if st.Status != "done" {
			t.Errorf("%s: %s", st.Title, st.Status)
		}
	}
	if native != 2 || w.State != "done" || w.Origin.List.Kind != ListClaudeTask {
		t.Errorf("native 간선 %d, 작업 %s, 목록 %s", native, w.State, w.Origin.List.Kind)
	}
	if w.TitleSource != "ai" || w.Title == w.Steps[0].Title {
		t.Errorf("제목 뒤: %q %s", w.Title, w.TitleSource)
	}
	b := beforeTitle(t, s, w.ID)
	if b.TitleSource != "step" || b.Title != w.Steps[0].Title {
		t.Errorf("제목 전: %q %s", b.Title, b.TitleSource)
	}
}

// 끝 기준: 진행 중 단계 하나에 백그라운드 서브에이전트를 맡기면 실행이 single_active로 이어지고 끝난 뒤 finished.
// 서브에이전트 안 시험 명령 성공·실패와 git commit → 그 단계에 command 둘(ok·fail)·commit 하나(sha 일치).
// gate가 "수정 필요"를 내면 검증 fail, 그 뒤 커밋이 생기면 outdated.
func TestE2EClaudeSubagentEvidence(t *testing.T) {
	dir := e2eDir(t)
	if calls := orbitCalls(toolCalls(t, filepath.Join(dir, "claude-b.jsonl"))); len(calls) > 1 || len(calls) == 1 && calls[0] != "work_title" {
		t.Errorf("Orbit 도구 호출: %v", calls)
	}
	s := e2eStore(t, filepath.Join(dir, "b"))
	ws := worksOf(t, s, "claude")
	if len(ws) != 1 || len(ws[0].Steps) != 1 {
		t.Fatalf("작업·단계: %d", len(ws))
	}
	w, st := ws[0], ws[0].Steps[0]
	var worker, gate *Run
	for i, r := range w.Runs {
		switch r.Role {
		case "orbit:worker":
			worker = &w.Runs[i]
		case "orbit:gate":
			gate = &w.Runs[i]
		}
	}
	if worker == nil || worker.Link != "single_active" || worker.StepID != st.ID || !worker.Background || worker.State != "finished" {
		t.Errorf("worker 실행: %+v", worker)
	}
	if gate == nil || gate.State != "finished" {
		t.Errorf("gate 실행: %+v", gate)
	}
	var cmds []string
	var commits []Evidence
	var verdict *Evidence
	for i, e := range w.Evidence {
		switch e.Kind {
		case "command":
			if worker != nil && e.RunID == worker.ID {
				cmds = append(cmds, e.Result)
			}
		case "commit":
			commits = append(commits, e)
		case "verdict":
			verdict = &w.Evidence[i]
		}
	}
	if strings.Join(cmds, ",") != "ok,fail" {
		t.Errorf("worker 시험 명령 증거: %v", cmds)
	}
	if len(commits) != 2 {
		t.Fatalf("커밋 증거 %d건", len(commits))
	}
	log := gitLog(t, filepath.Join(dir, "b", "repo"))
	if commits[0].Ref.SHA != log["test: Remove the broken test"] || commits[0].RunID != worker.ID {
		t.Errorf("서브에이전트 커밋: %+v / %v", commits[0], log)
	}
	if commits[1].Ref.SHA != log["docs: Describe the package"] || commits[1].RunID != "" {
		t.Errorf("메인 커밋: %+v", commits[1])
	}
	if verdict == nil || verdict.Result != "needs_fix" {
		t.Errorf("gate 판정: %+v", verdict)
	}
	if v := st.Verification; v.State != "fail" || !v.Outdated {
		t.Errorf("검증 결과: %+v", v)
	}
	if st.Status != "done" || w.State != "done" {
		t.Errorf("단계 %s, 작업 %s", st.Status, w.State)
	}
}

func gitLog(t *testing.T, repo string) map[string]string {
	t.Helper()
	out, err := execGit(repo, "log", "--format=%H %s")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if sha, subj, ok := strings.Cut(line, " "); ok {
			m[subj] = sha
		}
	}
	return m
}

// 끝 기준: Codex 헤드리스 세션에서 update_plan 세 항목을 차례로 진행·완료하면 단계 셋, order 간선 둘,
// 층 0·1·2, 모두 done, 작업 done. Orbit 도구 호출은 work_title뿐이고, 그 전에는 첫 단계 제목·step, 뒤에는 ai.
func TestE2ECodexPlan(t *testing.T) {
	dir := e2eDir(t)
	calls := orbitCalls(toolCalls(t, filepath.Join(dir, "codex-c.jsonl")))
	if strings.Join(calls, ",") != "work_title" {
		t.Errorf("Orbit 도구 호출: %v (work_title 한 번이어야 합니다)", calls)
	}
	s := e2eStore(t, filepath.Join(dir, "c"))
	ws := worksOf(t, s, "codex")
	if len(ws) != 1 {
		t.Fatalf("Codex 작업 %d개", len(ws))
	}
	w := ws[0]
	if len(w.Steps) != 3 {
		t.Fatalf("단계 %d개", len(w.Steps))
	}
	order := 0
	for i, st := range w.Steps {
		if st.Wave != i {
			t.Errorf("%s: 층 %d", st.Title, st.Wave)
		}
		for _, a := range st.After {
			if a.Source == "order" {
				order++
			}
		}
		if st.Status != "done" {
			t.Errorf("%s: %s", st.Title, st.Status)
		}
	}
	if order != 2 || w.State != "done" || w.Origin.List.Kind != ListCodexPlan {
		t.Errorf("order 간선 %d, 작업 %s, 목록 %s", order, w.State, w.Origin.List.Kind)
	}
	if w.TitleSource != "ai" || w.Title == w.Steps[0].Title {
		t.Errorf("제목 뒤: %q %s", w.Title, w.TitleSource)
	}
	b := beforeTitle(t, s, w.ID)
	if b.TitleSource != "step" || b.Title != w.Steps[0].Title {
		t.Errorf("제목 전: %q %s", b.Title, b.TitleSource)
	}
}
