package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/retro"
)

const mark = "LABMARK 묻기 전에 무엇을 정하는지 한 줄로 풀어 쓴다"

type env struct {
	l       *Lab
	s       *memory.Store
	src     string
	calls   []retro.Cmd
	cost    float64
	failRun func(changed bool, round int) bool
	cancel  context.CancelFunc // 첫 고르기 뒤에 부르면 실험을 취소한다
	better  string             // 고르는 가짜 모델이 고르는 답에 든 말
}

func hostJSON(result string, cost float64, isError bool) string {
	b, _ := json.Marshal(map[string]any{"type": "result", "is_error": isError, "result": result, "total_cost_usd": cost,
		"usage": map[string]int{"input_tokens": 100, "output_tokens": 20}})
	return string(b) + "\n"
}

func argOf(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func setup(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	s, err := memory.Open(filepath.Join(home, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return now }
	e := &env{s: s, src: filepath.Join(t.TempDir(), "source"), cost: 0.1, better: "무엇을 정할지"}
	if err := copyTree("../orbitplugin/source", e.src); err != nil {
		t.Fatal(err)
	}
	rounds := map[string]int{}
	e.l = &Lab{Store: s, Cfg: config.Default(), Paths: config.Paths{Home: home}, Oni: "/opt/oni", Source: e.src, Now: func() time.Time { return now },
		Shuffle: func(round int) bool { return round%2 == 1 }, // 홀수 회차는 고침이 A
		Exec: func(ctx context.Context, c retro.Cmd) (string, error) {
			e.calls = append(e.calls, c)
			dir := argOf(c.Args, "--plugin-dir")
			if dir == "" { // 고르는 모델
				a, b := part(c.Stdin, "## 결과물 A", "## 결과물 B"), part(c.Stdin, "## 결과물 B", "## 답")
				pick := "same"
				if strings.Contains(a, e.better) && !strings.Contains(b, e.better) {
					pick = "A"
				} else if strings.Contains(b, e.better) && !strings.Contains(a, e.better) {
					pick = "B"
				}
				if e.cancel != nil {
					e.cancel()
				}
				return hostJSON(`{"pick":"`+pick+`","reason":"기준에 더 맞음"}`, e.cost, false), nil
			}
			skill, _ := os.ReadFile(filepath.Join(dir, "skills", "plain-writing", "SKILL.md"))
			changed := strings.Contains(string(skill), "LABMARK")
			answer := "작업 기록 정리를 언제 할까요?"
			if changed {
				answer = "무엇을 정할지: 작업 기록 정리(원장 크기 줄이기)를 언제 할지 정해 주세요."
			}
			// 실행 모델은 흔히 만든 파일의 절대경로를 답에 쓴다.
			answer += "\n" + filepath.Join(c.Dir, "보고.md") + "를 만들었습니다."
			rounds[dir]++
			if e.failRun != nil && e.failRun(changed, rounds[dir]) {
				return hostJSON("상한을 넘었습니다", e.cost, true), nil
			}
			if err := os.WriteFile(filepath.Join(c.Dir, "보고.md"), []byte(answer+"\n"), 0o644); err != nil {
				return "", err
			}
			return hostJSON(answer, e.cost, false), nil
		}}
	return e
}

// part is the text of s between from and to.
func part(s, from, to string) string {
	_, rest, _ := strings.Cut(s, from)
	before, _, _ := strings.Cut(rest, to)
	return before
}

func (e *env) task(t *testing.T) Task {
	t.Helper()
	start := t.TempDir()
	os.WriteFile(filepath.Join(start, "상황.md"), []byte("ONID-14 작업 기록 크기·정리는 11-14 예정. O3-4.\n"), 0o644)
	os.MkdirAll(filepath.Join(start, ".git"), 0o755)
	os.WriteFile(filepath.Join(start, ".git", "HEAD"), []byte("ref"), 0o644)
	if err := e.s.RecordEvent(memory.Event{Kind: memory.EventCorrection, Day: "2026-10-30"}, map[string]any{"quote": "뭐알려달라는거야? 이해 못하겠어"}); err != nil {
		t.Fatal(err)
	}
	var ev int64
	e.s.DB().QueryRow(`SELECT MAX(id) FROM events`).Scan(&ev)
	task, err := e.l.NewTask(TaskInput{Title: "묻기 전에 풀어 쓰기", Instruction: "상황.md를 읽고 사용자에게 정할 것 하나를 물어라. 보고.md에 써라.",
		Criteria: "내부 번호·약어를 풀어 쓰고 무엇을 정하는지 한 줄로 말한다.", Start: start, Corrections: []int64{ev}})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (e *env) changeDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	b, _ := os.ReadFile(filepath.Join(e.src, "skills", "plain-writing", "SKILL.md"))
	p := filepath.Join(d, "skills", "plain-writing", "SKILL.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, append(b, []byte("\n"+mark+"\n")...), 0o644)
	return d
}

// 수용 기준 1: 과제는 그 PC 실험 폴더에만 생기고(.git 제외), 교정 사건의 인용이 판정 기준과 출처에 들어간다.
func TestTask_교정에서_과제를_만든다(t *testing.T) {
	e := setup(t)
	task := e.task(t)
	dir := filepath.Join(e.l.Paths.Home, "lab", "tasks", task.ID)
	if _, err := os.Stat(filepath.Join(dir, "start", "상황.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "start", ".git")); !os.IsNotExist(err) {
		t.Errorf(".git은 복사하지 않습니다")
	}
	if !strings.Contains(task.Criteria, "뭐알려달라는거야? 이해 못하겠어") || len(task.Sources) != 1 {
		t.Fatalf("과제 %+v", task)
	}
	got, err := e.l.Task(task.ID)
	if err != nil || got.Instruction != task.Instruction {
		t.Fatalf("다시 읽은 과제 %+v %v", got, err)
	}
	if list, _ := e.l.Tasks(); len(list) != 1 {
		t.Fatalf("과제 목록 %+v", list)
	}
}

// 수용 기준 2~5: 두 후보를 회차마다 번갈아 각 3번, 실험 전용 ORBIT_HOME·사용자 설정 없이·후보 플러그인만 실어 돌린다.
// 고르는 모델에는 A·B만 보인다. 고침이 이기면 도움됨, 원장 실험 줄, 바뀐 파일마다 파일 반영 제안(대기)이 생긴다.
func TestRun_두_후보를_돌려_익명으로_고르고_원장에_남긴다(t *testing.T) {
	e := setup(t)
	task := e.task(t)
	x, err := e.l.Run(context.Background(), RunInput{TaskID: task.ID, ChangeDir: e.changeDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	var runs, judges []retro.Cmd
	for _, c := range e.calls {
		if argOf(c.Args, "--plugin-dir") != "" {
			runs = append(runs, c)
		} else {
			judges = append(judges, c)
		}
	}
	if len(runs) != 6 || len(judges) != 3 {
		t.Fatalf("실행 %d, 고르기 %d", len(runs), len(judges))
	}
	labHome := ""
	for i, c := range runs {
		for _, want := range []string{"-p", "--restricted", "--setting-sources", "project", "--model", "sonnet", "--max-budget-usd", "--output-format"} {
			if !contains(c.Args, want) {
				t.Errorf("실행 %d 인자에 %s가 없습니다: %v", i, want, c.Args)
			}
		}
		if argOf(c.Args, "--allowedTools") != runTools || strings.Contains(runTools, "Bash") || c.Stdin != task.Instruction {
			t.Errorf("실행 %d 도구·입력 %v %q", i, c.Args, c.Stdin)
		}
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, "ORBIT_HOME="); ok {
				labHome = v
			}
		}
		if !strings.HasPrefix(labHome, filepath.Join(e.l.Paths.Home, "lab")) {
			t.Errorf("실험 전용 ORBIT_HOME이어야 합니다: %q", labHome)
		}
		if _, err := os.Stat(filepath.Join(c.Dir, ".git")); !os.IsNotExist(err) {
			t.Errorf("작업 폴더에 .git이 없어야 합니다")
		}
		if !contains(c.Args, "--strict-mcp-config") || argOf(c.Args, "--add-dir") != argOf(c.Args, "--plugin-dir") {
			t.Errorf("실행 %d: 사용자 MCP를 싣지 않고 후보 플러그인 폴더(분야 묶음)만 더 읽게 한다: %v", i, c.Args)
		}
		for _, leak := range []string{Current, Changed} {
			if strings.Contains(c.Dir, leak) || strings.Contains(argOf(c.Args, "--plugin-dir"), leak) {
				t.Errorf("실행 경로에 후보 이름이 보입니다: %s %s", c.Dir, argOf(c.Args, "--plugin-dir"))
			}
		}
	}
	if cfg, err := config.Load(filepath.Join(labHome, "config.json")); err != nil || cfg.SessionRetro || cfg.WorkOn() {
		t.Errorf("실험 전용 설정은 세션 끝 회고·작업 기록을 끈다: %+v %v", cfg, err)
	}
	// 회차마다 지금·고침을 번갈아 돈다.
	cur, chg := argOf(runs[0].Args, "--plugin-dir"), argOf(runs[1].Args, "--plugin-dir")
	if cur == chg || argOf(runs[2].Args, "--plugin-dir") != chg || argOf(runs[3].Args, "--plugin-dir") != cur {
		t.Errorf("순서 %v", []string{cur, chg, argOf(runs[2].Args, "--plugin-dir"), argOf(runs[3].Args, "--plugin-dir")})
	}
	for _, j := range judges {
		if argOf(j.Args, "--model") != "opus" || !contains(j.Args, "--tools") || argOf(j.Args, "--tools") != "" || !contains(j.Args, "--restricted") {
			t.Errorf("고르는 모델 %v", j.Args)
		}
		for _, leak := range []string{"current", "changed", cur, chg, "지금 하네스", "고친 하네스", filepath.Join(e.l.Paths.Home, "lab")} {
			if strings.Contains(j.Stdin, leak) {
				t.Errorf("고르는 모델에 %q가 보입니다", leak)
			}
		}
		if !strings.Contains(j.Stdin, "뭐알려달라는거야") || !strings.Contains(j.Stdin, "보고.md") {
			t.Errorf("판정 기준과 변경이 재료에 있어야 합니다:\n%s", j.Stdin)
		}
	}
	if x.Status != memory.LabDone || x.Verdict != memory.VerdictHelped || len(x.Picks) != 3 || x.Picks[0].A != "changed" || x.Picks[0].Pick != "A" || x.Picks[1].Pick != "B" {
		t.Fatalf("실험 %+v", x)
	}
	if x.CostUSD < 0.89 || x.CostUSD > 0.91 || x.Hashes["current"] == x.Hashes["changed"] {
		t.Errorf("비용 %v 해시 %v", x.CostUSD, x.Hashes)
	}
	l, err := e.s.LedgerEntry(x.LedgerID)
	if err != nil || l.ProposalKind != memory.LedgerKindLab || l.Verdict != memory.VerdictHelped || !strings.Contains(l.Change, "LABMARK") {
		t.Fatalf("원장 %+v %v", l, err)
	}
	pending, _ := e.s.Proposals(memory.StatusPending)
	if len(pending) != 1 || pending[0].Kind != memory.ProposalApplyFile || pending[0].Payload.Path != filepath.Join(e.src, "skills", "plain-writing", "SKILL.md") ||
		!strings.Contains(pending[0].Body, x.ID) {
		t.Fatalf("제안 %+v", pending)
	}
	if b, _ := os.ReadFile(filepath.Join(e.src, "skills", "plain-writing", "SKILL.md")); strings.Contains(string(b), "LABMARK") {
		t.Fatal("실험실은 원본을 바꾸지 않습니다")
	}
	runsDB, _ := e.s.LabRuns(x.ID)
	if len(runsDB) != 9 {
		t.Errorf("실행 기록 %d", len(runsDB))
	}
	out, err := e.l.Show(x.ID)
	if err != nil || !strings.Contains(out, "도움됨") || !strings.Contains(out, "무엇을 정할지") || !strings.Contains(out, "기준에 더 맞음") {
		t.Errorf("보기:\n%s %v", out, err)
	}
}

// 수용 기준 6: 실험실 대기 제안으로 실험하면, 도움됨이면 대기로 올라가고 해로움이면 닫힌다.
func TestRun_실험실_대기_제안(t *testing.T) {
	for _, tc := range []struct {
		name, better, status, verdict string
	}{{"도움됨", "무엇을 정할지", memory.StatusPending, memory.VerdictHelped}, {"해로움", "언제 할까요", memory.StatusRejected, memory.VerdictHarmful}} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			e.better = tc.better
			task := e.task(t)
			path := filepath.Join(e.src, "skills", "plain-writing", "SKILL.md")
			b, _ := os.ReadFile(path)
			p, err := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalApplyFile, Title: "묻기 규칙", Lab: true,
				Payload: memory.Payload{Path: path, Content: string(b) + "\n" + mark + "\n"}})
			if err != nil {
				t.Fatal(err)
			}
			x, err := e.l.Run(context.Background(), RunInput{TaskID: task.ID, ProposalID: p.ID})
			if err != nil || x.Verdict != tc.verdict {
				t.Fatalf("실험 %+v %v", x, err)
			}
			got, _ := e.s.GetProposal(p.ID)
			if got.Status != tc.status || !strings.Contains(got.Body+got.Note, x.ID) {
				t.Fatalf("제안 %+v", got)
			}
		})
	}
}

// 수용 기준 4: 실패한 회차는 고르기에서 빠지고, 누적 비용이 상한에 닿으면 멈추고 실패로 닫는다(원장·제안 없음).
func TestRun_실패한_회차와_비용_상한(t *testing.T) {
	e := setup(t)
	task := e.task(t)
	e.failRun = func(changed bool, round int) bool { return round == 2 && changed }
	x, err := e.l.Run(context.Background(), RunInput{TaskID: task.ID, ChangeDir: e.changeDir(t)})
	if err != nil || len(x.Picks) != 2 || x.Verdict != memory.VerdictHelped {
		t.Fatalf("실패 회차 제외 %+v %v", x, err)
	}

	e = setup(t)
	task = e.task(t)
	e.l.Cfg.LabExperimentUSD = 0.25
	e.l.Cfg.LabRunUSD = 0.2
	x, err = e.l.Run(context.Background(), RunInput{TaskID: task.ID, ChangeDir: e.changeDir(t)})
	if err == nil || x.Status != memory.LabFailed || !strings.Contains(x.Note, "상한") || x.LedgerID != "" {
		t.Fatalf("상한 %+v %v", x, err)
	}
	if n := len(e.calls); n != 3 {
		t.Errorf("상한에 닿은 뒤에는 돌지 않습니다: %d번", n)
	}
	if pending, _ := e.s.Proposals(memory.StatusPending); len(pending) != 0 {
		t.Errorf("실패한 실험은 제안을 만들지 않습니다")
	}
}

// 바꿀 파일은 원본 안이어야 한다(제안 경로가 원본 밖이면 받지 않는다).
func TestRun_원본_밖_파일은_받지_않는다(t *testing.T) {
	e := setup(t)
	task := e.task(t)
	other := filepath.Join(t.TempDir(), "AGENTS.md")
	os.WriteFile(other, []byte("x"), 0o644)
	p, _ := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalApplyFile, Title: "밖", Lab: true, Payload: memory.Payload{Path: other, Content: "y"}})
	if _, err := e.l.Run(context.Background(), RunInput{TaskID: task.ID, ProposalID: p.ID}); err == nil {
		t.Fatal("원본 밖 파일은 실험하지 않습니다")
	}
	if len(e.calls) != 0 {
		t.Fatal(fmt.Sprint("호스트를 부르지 않습니다: ", len(e.calls)))
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// 검토 지적 2: 실험 중에 취소하면 고른 회차가 있어도 판정·원장·제안 없이 실패로 닫는다.
func TestRun_취소하면_판정하지_않는다(t *testing.T) {
	e := setup(t)
	task := e.task(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	x, err := e.l.Run(ctx, RunInput{TaskID: task.ID, ChangeDir: e.changeDir(t)})
	if err == nil || x.Status != memory.LabFailed || x.LedgerID != "" || !strings.Contains(x.Note, "중단") {
		t.Fatalf("취소 %+v %v", x, err)
	}
	if pending, _ := e.s.Proposals(memory.StatusPending); len(pending) != 0 {
		t.Error("취소한 실험은 제안을 만들지 않습니다")
	}
}

// 검토 지적 3·9: 제안을 만든 뒤 원본 파일이 바뀌었으면 돌리지 않는다. 바꿀 폴더의 숨김 파일은 변경으로 세지 않는다.
func TestRun_바뀐_원본과_숨김_파일(t *testing.T) {
	e := setup(t)
	task := e.task(t)
	path := filepath.Join(e.src, "skills", "plain-writing", "SKILL.md")
	b, _ := os.ReadFile(path)
	p, _ := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalApplyFile, Title: "묻기", Lab: true, Payload: memory.Payload{Path: path, Content: string(b) + mark}})
	os.WriteFile(path, append(b, []byte("\n다른 사람이 고침\n")...), 0o644)
	if _, err := e.l.Run(context.Background(), RunInput{TaskID: task.ID, ProposalID: p.ID}); err == nil || !strings.Contains(err.Error(), "바뀌었") || len(e.calls) != 0 {
		t.Fatalf("바뀐 원본은 실험하지 않습니다: %v calls=%d", err, len(e.calls))
	}
	dir := e.changeDir(t)
	os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644)
	x, err := e.l.Run(context.Background(), RunInput{TaskID: task.ID, ChangeDir: dir})
	if err != nil || len(x.Files) != 1 {
		t.Fatalf("숨김 파일 %+v %v", x.Files, err)
	}
}
