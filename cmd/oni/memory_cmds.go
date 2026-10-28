package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/inject"
	"github.com/onidot-labs/orbit/internal/mcpserver"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/project"
	"github.com/onidot-labs/orbit/internal/retro"
	"github.com/onidot-labs/orbit/internal/version"
	"github.com/onidot-labs/orbit/internal/work"
	"github.com/onidot-labs/orbit/internal/work/hook"
)

func paths() config.Paths { return config.Paths{Home: config.Home()} }

func loadConfig() (config.Config, error) { return config.Load(paths().Config()) }

func openStore() (*memory.Store, error) { return memory.Open(paths().DB()) }

func cmdVersion(a *app, args []string) error {
	fmt.Fprintln(a.stdout, "oni "+version.Version)
	return nil
}

func sessionProject() project.Project {
	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return project.Resolve(dir)
}

func cmdMCP(a *app, args []string) error {
	fs := a.flags("mcp")
	retroID := fs.String("retro", "", "회고 id(회고 실행 전용 도구만 연다)")
	reader := fs.Bool("reader", false, "읽는 쪽(Studio 등) 전용: 작업 도구 넷(work_list·work_get·work_sessions·work_changes)만 연다")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	p := sessionProject()
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	wo := &mcpserver.WorkOptions{DB: paths().WorkDB(), Spool: paths().WorkSpool(), Cwd: cwd, Project: p.Scope, Read: work.ReadOptions{StaleAfter: cfg.StaleAfter()}}
	if *reader {
		ctx, cancel := signalContext()
		defer cancel()
		return mcpserver.NewReader(*wo, version.Version).Run(ctx, &mcp.StdioTransport{})
	}
	if !cfg.WorkOn() {
		wo = nil
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := signalContext()
	defer cancel()
	var srv *mcp.Server
	if *retroID != "" {
		srv = mcpserver.NewRetro(s, paths(), *retroID, version.Version)
	} else {
		srv = mcpserver.New(s, mcpserver.Options{Project: p.Scope, ProjectLabel: p.Label, Version: version.Version, Work: wo})
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}

type hookInput struct {
	Cwd            string `json:"cwd"`
	Prompt         string `json:"prompt"`
	SessionID      string `json:"session_id"`
	Event          string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
}

// spawnDetached starts a background oni that outlives the hook, its output
// appended to log.
var spawnDetached = func(name string, args []string, log string) error {
	_ = os.MkdirAll(filepath.Dir(log), 0o700)
	f, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = f, f
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// sessionEnd queues the ended session for its session-end retrospective and
// starts the queue runner in the background; the host is not kept waiting.
func (a *app) sessionEnd(in hookInput, host string, cfg config.Config) {
	if os.Getenv("ORBIT_RETRO_ID") != "" || !cfg.SessionRetro {
		return
	}
	s, err := openStore()
	if err != nil {
		return
	}
	defer s.Close()
	queued, err := retro.Enqueue(s, paths(), retro.SessionEnd{Host: host, Session: in.SessionID, Transcript: in.TranscriptPath, Cwd: in.Cwd})
	if err != nil {
		fmt.Fprintln(a.stderr, "oni hook: 세션 끝 회고를 대기열에 넣지 못했습니다: "+err.Error())
		return
	}
	if !queued {
		return
	}
	if err := spawnDetached(selfPath(), []string{"retro", "sessions"}, filepath.Join(paths().Logs(), "session-retro.log")); err != nil {
		fmt.Fprintln(a.stderr, "oni hook: 세션 끝 회고를 띄우지 못했습니다(다음 하루 회고가 읽습니다): "+err.Error())
	}
}

// hookContextRunes is about the hosts' hook context limit (Claude Code
// 10,000자). The resume context uses what the memory context leaves.
const hookContextRunes = 10000

// cmdHook never fails the host: any problem, wrong arguments included, prints
// nothing to stdout and exits 0 (exit 2 would block a Claude Code prompt).
func cmdHook(a *app, args []string) error {
	at := time.Now().UnixMilli() // 입력을 읽기 전에 찍는다(비동기 훅의 도착 순서는 믿지 않는다)
	fs := a.flags("hook")
	fs.SetOutput(io.Discard)
	host := fs.String("host", "claude", "claude | codex")
	skipLists := fs.Bool("skip-lists", false, "목록 도구는 동기 훅에 맡기고 기록하지 않는다")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 || (pos[0] != "session-start" && pos[0] != "prompt" && pos[0] != "session-end" && pos[0] != "work") || (*host != "claude" && *host != "codex") {
		fmt.Fprintln(a.stderr, "oni hook: 인자를 알 수 없어 넘어갑니다 (oni hook session-start|prompt|session-end|work --host claude|codex)")
		return nil
	}
	start := time.Now()
	deadline := time.After(hookDeadline)
	until := start.Add(hookDeadline - hookExitMargin) // 이때까지 사건을 쓰거나 스풀에 남긴다
	done := make(chan string, 1)
	recorded := make(chan struct{})
	finished := make(chan struct{}) // 기억 사건까지 남겼다
	go func() {
		defer close(finished)
		defer func() {
			if recover() != nil {
				done <- ""
			}
		}()
		b, _ := io.ReadAll(io.LimitReader(a.stdin, 4<<20))
		cfg, err := loadConfig()
		if err != nil {
			close(recorded)
			fmt.Fprintln(a.stderr, "oni hook: 설정을 읽지 못해 넘어갑니다: "+err.Error())
			done <- ""
			return
		}
		if pos[0] == "work" {
			// 제목 요청 문맥은 기록 결과(연 작업)에 달려 있어 차례로 한다
			opened, listTool := recordWork(*host, b, at, until, cfg, *skipLists)
			close(recorded)
			var ctx []string
			if listTool {
				for _, id := range opened {
					ctx = append(ctx, work.TitleRequest(id))
				}
			}
			done <- inject.HookOutput("PostToolUse", strings.Join(ctx, "\n"))
			return
		}
		// 기억 문맥은 작업 기록(git 호출·쓰기 대기)을 기다리지 않는다
		go func() {
			defer close(recorded)
			defer func() { _ = recover() }()
			recordWork(*host, b, at, until, cfg, *skipLists)
		}()
		var in hookInput
		_ = json.Unmarshal(b, &in)
		memoryHook(a, done, pos[0], *host, in, cfg)
	}()
	select {
	case out := <-done:
		if out != "" {
			fmt.Fprintln(a.stdout, out)
		}
		// 작업 기록은 until 전에 쓰거나 스풀에 남기고, 기억 사건은 문맥을 낸 뒤에 쓴다.
		// 둘 다 기다리지 않고 끝나면 프로세스와 함께 사라진다.
		for _, ch := range []<-chan struct{}{recorded, finished} {
			select {
			case <-ch:
			case <-deadline:
				return nil
			}
		}
	case <-deadline:
		fmt.Fprintln(a.stderr, "oni hook: 시간이 걸려 넘어갑니다")
	}
	return nil
}

// hookDeadline is how long a hook may take before it gives up without output;
// hookExitMargin is left after the work record so the process exits in time.
const (
	hookDeadline   = 4 * time.Second
	hookExitMargin = 300 * time.Millisecond
)

// recordWork turns the hook input into work ledger events (설계 · Orbit 2차
// 「훅」). It returns the works this call opened and whether it was a list tool.
func recordWork(host string, raw []byte, at int64, until time.Time, cfg config.Config, skipLists bool) ([]string, bool) {
	if !cfg.WorkOn() || os.Getenv("ORBIT_RETRO_ID") != "" {
		return nil, false
	}
	env := hook.Env{Host: host, Getenv: os.Getenv, Git: hook.SystemGit{}, Now: at,
		TestCommands: append(append([]string{}, hook.DefaultTestCommands...), cfg.WorkTestCommands...),
		Project: func(cwd string) work.Project {
			p := project.Resolve(cwd)
			return work.Project{Scope: p.Scope, Label: p.Label}
		}}
	plan := hook.Translate(raw, env)
	if len(plan.Events) == 0 || (skipLists && plan.ListTool) {
		return nil, plan.ListTool
	}
	var opened []string
	for _, res := range work.RecordAll(paths().WorkDB(), paths().WorkSpool(), plan.Events, until) {
		opened = append(opened, res.Opened...)
	}
	return opened, plan.ListTool
}

// memoryHook is the memory context of session-start and prompt (설계 1차 「호스트에 붙는 방식」),
// with the open works of the project at session start (설계 · Orbit 2차 O9).
func memoryHook(a *app, done chan<- string, which, host string, in hookInput, cfg config.Config) {
	scope := ""
	if in.Cwd != "" {
		scope = project.Resolve(in.Cwd).Scope
	}
	if _, err := os.Stat(paths().DB()); err != nil {
		done <- ""
		return
	}
	if which == "session-end" {
		a.sessionEnd(in, host, cfg)
		done <- ""
		return
	}
	s, err := openStore()
	if err != nil {
		done <- ""
		return
	}
	defer s.Close()
	var inj inject.Injection
	kind := memory.EventInjectSessionStart
	if which == "session-start" {
		inj = inject.SessionBundle(s, cfg, scope, host, in.SessionID)
		text := inj.Text
		if resume := resumeContext(cfg, scope, hookContextRunes-len([]rune(text))-200); resume != "" {
			text = strings.TrimSpace(text + "\n" + resume)
		}
		done <- inject.HookOutput("SessionStart", text)
	} else {
		kind = memory.EventInjectPrompt
		inj = inject.PromptInjection(s, cfg, scope, in.Prompt)
		done <- inject.HookOutput("UserPromptSubmit", inj.Text)
	}
	// 문맥을 먼저 내보내고 지표용 사건을 남긴다(설계 「측정」). 넣지 않는 수준·빈 요청과
	// 회고 실행 안에서 도는 훅은 사용이 아니므로 세지 않는다.
	if os.Getenv("ORBIT_RETRO_ID") != "" || cfg.Inject == config.InjectOff ||
		(kind == memory.EventInjectPrompt && (cfg.Inject != config.InjectFull || strings.TrimSpace(in.Prompt) == "")) {
		return
	}
	data := map[string]any{"memoryIds": inj.MemoryIDs, "chars": inj.Chars}
	if kind == memory.EventInjectSessionStart {
		data["titleOnly"] = inj.TitleOnly
	}
	_ = s.RecordEvent(memory.Event{Kind: kind, Host: host, Session: in.SessionID, Scope: scope}, data)
}

// resumeContext is the open works of the project, only at the full injection level.
func resumeContext(cfg config.Config, scope string, budget int) string {
	if cfg.Inject != config.InjectFull || !cfg.WorkOn() || scope == "" {
		return ""
	}
	if _, err := os.Stat(paths().WorkDB()); err != nil {
		return ""
	}
	ws, err := work.Open(paths().WorkDB())
	if err != nil {
		return ""
	}
	defer ws.Close()
	return ws.ResumeContext(scope, budget, work.ReadOptions{StaleAfter: cfg.StaleAfter()})
}

func kindName(k string) string {
	return map[string]string{"rule": "규칙", "fact": "사실", "decision": "결정", "lesson": "교훈", "journal": "일지"}[k]
}

func stateName(s string) string {
	return map[string]string{"candidate": "후보", "confirmed": "확정", "stale": "낡음", "hidden": "숨김"}[s]
}

func cmdMemory(a *app, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "find":
		fs := a.flags("memory find")
		var kinds multiFlag
		fs.Var(&kinds, "kind", "종류(여러 번)")
		all := fs.Bool("all", false, "모든 프로젝트")
		stale := fs.Bool("stale", false, "낡은 기억도")
		limit := fs.Int("n", 20, "최대 개수")
		asJSON := fs.Bool("json", false, "JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		in := memory.FindInput{Query: strings.Join(pos, " "), Kinds: kinds, Limit: *limit, AllProjects: *all, Project: sessionProject().Scope}
		if *stale {
			in.States = []string{memory.StateCandidate, memory.StateConfirmed, memory.StateStale}
		}
		ms, err := s.Find(in)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(ms)
		}
		for _, m := range ms {
			fmt.Fprintf(a.stdout, "%s  %s·%s  %s\n", m.ID, kindName(m.Kind), stateName(m.State), m.Title)
		}
		if len(ms) == 0 {
			fmt.Fprintln(a.stdout, "찾은 기억이 없습니다.")
		}
		return nil
	case "show":
		fs := a.flags("memory show")
		history := fs.Bool("history", false, "버전 이력")
		asJSON := fs.Bool("json", false, "JSON")
		pos, err := parse(fs, args[1:])
		if err != nil || len(pos) != 1 {
			return errUsage
		}
		m, err := s.Get(pos[0])
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(m)
		}
		fmt.Fprintf(a.stdout, "%s · %s · %s · %s · v%d\n%s\n\n%s\n", m.ID, kindName(m.Kind), stateName(m.State), m.Scope, m.Version, m.Title, m.Body)
		if len(m.Tags) > 0 {
			fmt.Fprintln(a.stdout, "\n태그: "+strings.Join(m.Tags, ", "))
		}
		for _, src := range m.Sources {
			fmt.Fprintf(a.stdout, "출처 %s %s %s\n", src.Kind, src.Ref, oneLine(src.Excerpt, 80))
		}
		if *history {
			h, _ := s.History(m.ID)
			for _, v := range h {
				fmt.Fprintf(a.stdout, "v%d %s %s %s\n", v.Version, time.UnixMilli(v.At).Format("2006-01-02 15:04"), v.Action, v.Reason)
			}
		}
		return nil
	}
	return errUsage
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func printProposal(w io.Writer, s *memory.Store, p memory.Proposal, full bool) {
	fmt.Fprintf(w, "%s  [%s] %s\n", p.ID, p.Kind, p.Title)
	for _, id := range p.MemoryIDs {
		if m, err := s.Get(id); err == nil {
			fmt.Fprintf(w, "    대상 %s %s·%s %s\n", m.ID, kindName(m.Kind), stateName(m.State), m.Title)
		}
	}
	if full && p.Body != "" {
		fmt.Fprintln(w, "    "+strings.ReplaceAll(p.Body, "\n", "\n    "))
	}
}

func cmdProposals(a *app, args []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := a.flags("proposals " + sub)
	all := fs.Bool("all", false, "결정한 제안도")
	title := fs.String("title", "", "고쳐서 승인할 제목")
	body := fs.String("body", "", "고쳐서 승인할 본문")
	bodyFile := fs.String("body-file", "", "고쳐서 승인할 본문 파일")
	note := fs.String("note", "", "결정 이유")
	asJSON := fs.Bool("json", false, "JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		status := memory.StatusPending
		if *all {
			status = ""
		}
		ps, err := s.Proposals(status)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(ps)
		}
		for _, p := range ps {
			printProposal(a.stdout, s, p, false)
		}
		fmt.Fprintf(a.stdout, "대기 %d건. 자세히: oni proposals show <id> · 하나씩 결정: oni proposals review\n", s.PendingCount())
		return nil
	case "show":
		if len(pos) != 1 {
			return errUsage
		}
		p, err := s.GetProposal(pos[0])
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(p)
		}
		printProposal(a.stdout, s, p, true)
		return nil
	case "approve", "reject":
		if len(pos) != 1 {
			return errUsage
		}
		in := memory.DecideInput{ID: pos[0], Decision: sub, Note: *note}
		if *title != "" {
			in.Title = title
		}
		if *bodyFile != "" {
			b, err := os.ReadFile(*bodyFile)
			if err != nil {
				return err
			}
			text := string(b)
			in.Body = &text
		} else if *body != "" {
			in.Body = body
		}
		res, err := s.Decide(in)
		if err != nil {
			return err
		}
		return a.reportDecision(res)
	case "review":
		return a.review(s)
	}
	return errUsage
}

func (a *app) reportDecision(res memory.DecideResult) error {
	word := map[string]string{memory.StatusApproved: "승인", memory.StatusRejected: "거절"}[res.Proposal.Status]
	fmt.Fprintf(a.stdout, "%s %s했습니다.", res.Proposal.ID, word)
	if res.Created != "" {
		fmt.Fprintf(a.stdout, " 새 기억 %s.", res.Created)
	}
	if len(res.Changed) > 0 {
		fmt.Fprintf(a.stdout, " 바뀐 기억 %s.", strings.Join(res.Changed, ", "))
	}
	if res.File != "" {
		fmt.Fprintf(a.stdout, " 파일을 바꿨습니다(커밋은 직접): %s", res.File)
	}
	fmt.Fprintln(a.stdout)
	return nil
}

// review walks through pending proposals one by one on the terminal.
func (a *app) review(s *memory.Store) error {
	ps, err := s.Proposals(memory.StatusPending)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Fprintln(a.stdout, "대기 중인 제안이 없습니다.")
		return nil
	}
	in := bufio.NewReader(a.stdin)
	for i, p := range ps {
		fmt.Fprintf(a.stdout, "\n(%d/%d) ", i+1, len(ps))
		printProposal(a.stdout, s, p, true)
		fmt.Fprint(a.stdout, "승인 a · 거절 r · 넘김 s · 그만 q > ")
		line, err := in.ReadString('\n')
		answer := strings.TrimSpace(line)
		switch answer {
		case "a", "r":
			decision := map[string]string{"a": "approve", "r": "reject"}[answer]
			res, derr := s.Decide(memory.DecideInput{ID: p.ID, Decision: decision})
			if derr != nil {
				fmt.Fprintln(a.stdout, "결정하지 못했습니다: "+derr.Error())
				continue
			}
			a.reportDecision(res)
		case "q":
			return nil
		}
		if err != nil {
			return nil
		}
	}
	fmt.Fprintf(a.stdout, "\n남은 제안 %d건\n", s.PendingCount())
	return nil
}

func cmdBackup(a *app, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	path, err := s.Backup(paths().Backups(), cfg.BackupKeep)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "백업했습니다: "+path)
	wpath, err := work.DailyBackup(paths().WorkDB(), paths().Backups(), cfg.BackupKeep, time.Now())
	if err != nil {
		return err
	}
	if wpath != "" {
		fmt.Fprintln(a.stdout, "백업했습니다: "+wpath)
	}
	return nil
}

func cmdConfig(a *app, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "show" {
		fmt.Fprintln(a.stdout, "# "+paths().Config())
		return a.json(cfg)
	}
	if args[0] != "set" || len(args) != 3 {
		return errUsage
	}
	key, val := args[1], args[2]
	switch key {
	case "retroHost":
		cfg.RetroHost = val
	case "retroModel":
		cfg.RetroModel = val
	case "retroHour":
		_, err := fmt.Sscanf(val, "%d", &cfg.RetroHour)
		if err != nil {
			return err
		}
	case "inject":
		cfg.Inject = val
	case "recentDecisions":
		if _, err := fmt.Sscanf(val, "%d", &cfg.RecentDecisions); err != nil {
			return err
		}
	case "backupKeep":
		if _, err := fmt.Sscanf(val, "%d", &cfg.BackupKeep); err != nil {
			return err
		}
	case "applyTargets":
		cfg.ApplyTargets = nil
		for _, p := range strings.Split(val, ",") {
			if p = strings.TrimSpace(p); p != "" {
				abs, err := filepath.Abs(p)
				if err != nil {
					return err
				}
				cfg.ApplyTargets = append(cfg.ApplyTargets, abs)
			}
		}
	case "sessionRetro":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("sessionRetro는 true 또는 false입니다: %q", val)
		}
		cfg.SessionRetro = b
	case "sessionRetroModel":
		cfg.SessionRetroModel = val
	case "sessionRetroDailyMax", "recallChars", "ledgerWindowDays":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("%s는 숫자입니다: %q", key, val)
		}
		*map[string]*int{"sessionRetroDailyMax": &cfg.SessionRetroDailyMax, "recallChars": &cfg.RecallChars, "ledgerWindowDays": &cfg.LedgerWindowDays}[key] = n
	default:
		return fmt.Errorf("모르는 키입니다: %s (retroHost·retroModel·retroHour·inject·recentDecisions·backupKeep·applyTargets·sessionRetro·sessionRetroModel·sessionRetroDailyMax·recallChars·ledgerWindowDays)", key)
	}
	if err := config.Save(paths().Config(), cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s = %s 로 저장했습니다.", key, val)
	if key == "retroHour" {
		fmt.Fprint(a.stdout, " 예약 시각을 바꾸려면 oni install --no-hosts 로 예약을 다시 등록하세요.")
	}
	fmt.Fprintln(a.stdout)
	return nil
}

var _ = context.Background
