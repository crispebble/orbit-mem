package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/inject"
	"github.com/onidot-labs/orbit-mem/internal/mcpserver"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
	"github.com/onidot-labs/orbit-mem/internal/project"
	"github.com/onidot-labs/orbit-mem/internal/version"
)

func paths() config.Paths { return config.Paths{Home: config.Home()} }

func loadConfig() (config.Config, error) { return config.Load(paths().Config()) }

// openStore opens this PC's memory database with the configured confirmation
// counts (A2). A configuration that does not load leaves the defaults.
func openStore() (*memory.Store, error) {
	s, err := memory.Open(paths().DB())
	if err != nil {
		return nil, err
	}
	if cfg, err := loadConfig(); err == nil {
		s.Policy = policyOf(cfg)
	}
	return s, nil
}

func policyOf(cfg config.Config) memory.Policy {
	return memory.Policy{SeenDays: cfg.ConfirmSeenDays, FollowedVerified: cfg.ConfirmFollowedVerified, CorrectionSessions: cfg.CorrectionRepeatSessions}
}

// actorOf is who writes through a command: inside a retrospective run always
// automation, else the flag, else ORBIT_ACTOR, else def.
func actorOf(flag, def string) string {
	return mcpserver.ResolveActor(flag, os.Getenv("ORBIT_ACTOR"), os.Getenv("ORBIT_RETRO_ID"), def)
}

func cmdVersion(a *app, args []string) error {
	fmt.Fprintln(a.stdout, "orbit-mem "+version.Version)
	return nil
}

// whereOf is what the recall reads a session's place from: its folder (the parents it inherits, S3-R2),
// or its scope key when the folder is not known.
func whereOf(p project.Project) string {
	place.Known(p)
	if p.Dir != "" {
		return p.Dir
	}
	return p.Scope
}

// sessionDir is the folder a hook reads the session's place from: the folder the session started in
// (CLAUDE_PROJECT_DIR, which Claude Code gives hooks and the MCP server alike), else the hook input's cwd
// (Codex). So recall and memory_find look from the same folder even after the session moves (S3-R1).
func sessionDir(cwd string) string {
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		return dir
	}
	return cwd
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
	retroID := fs.String("retro", "", i18n.T("flag.mcp.retro"))
	reader := fs.Bool("reader", false, i18n.T("flag.mcp.reader"))
	actor := fs.String("actor", "", i18n.T("flag.mcp.actor"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	// MCP 응답 글은 AI가 읽는다(ONID-61 대상 아님). 공유 오류(기억·제안)도 언어 설정과 무관하게 한국어로 낸다.
	i18n.Set(i18n.KO)
	if *reader { // 기억 그래프만(Orbit Studio 같은 앱)
		ctx, cancel := signalContext()
		defer cancel()
		return mcpserver.NewReader(paths().DB(), version.Version).Run(ctx, &mcp.StdioTransport{})
	}
	if _, err := loadConfig(); err != nil {
		return err
	}
	p := sessionProject()
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
		srv = mcpserver.New(s, mcpserver.Options{Project: p.Scope, ProjectLabel: p.Label, Dir: p.Dir, Git: p.Git, Temp: p.Temp, Version: version.Version, WorkDB: paths().WorkDB(),
			Actor: actorOf(*actor, memory.ActorAI), LoadProducts: func() (place.Map, error) {
				cfg, err := loadConfig()
				return cfg.Products, err
			}})
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}

type hookInput struct {
	Cwd            string `json:"cwd"`
	Prompt         string `json:"prompt"`
	SessionID      string `json:"session_id"`
	Event          string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
	Source         string `json:"source"`    // SessionStart: startup·resume·clear·compact·fork
	ToolName       string `json:"tool_name"` // PreToolUse
	// AgentID is the subagent a hook fires in (SubagentStart, and tool hooks inside a subagent).
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
}

// sessionKey is the session the injection marks are kept under: a subagent has its own context, so it is
// told what the main session was told once more (설계 · orbit-mem 2판 6절 「중복 제거」, S3 명세 2.9).
func (in hookInput) sessionKey() string {
	if in.AgentID == "" || in.SessionID == "" {
		return in.SessionID
	}
	return in.SessionID + "/" + in.AgentID
}

// cmdHook never fails the host: any problem, wrong arguments included, prints
// nothing to stdout and exits 0 (exit 2 would block a Claude Code prompt).
// session-start, prompt and pre-tool give the memory context; session-start, prompt
// and session-end keep the activity log. session-end only ends the activity row: the
// retrospectives (and their session-end queue) are orbit-code's.
func cmdHook(a *app, args []string) error {
	at := time.Now().UnixMilli() // 입력을 읽기 전에 찍는다
	fs := a.flags("hook")
	fs.SetOutput(io.Discard)
	host := fs.String("host", "claude", "claude | codex")
	fs.Bool("skip-lists", false, "") // 옛 작업 기록 훅의 인자: 받고 버린다
	runtime := fs.String("runtime", "", i18n.T("flag.hook.runtime"))
	launcher := fs.String("launcher", "", i18n.T("flag.hook.launcher"))
	pluginVersion := fs.String("plugin-version", "", "") // 플러그인 훅이 붙이는 플러그인 판: 시작 훅만 본다
	// AU-7: 지시에 단계 회상이 이미 든 실행(orbit-pi claude 엔진)의 요청 훅. 활동만 남기고
	// 기억을 찾지도, 「넣음」 사건(inject.prompt)을 남기지도 않는다.
	recordOnly := fs.Bool("record-only", false, i18n.T("flag.hook.record_only"))
	pos, err := parse(fs, args)
	if err == nil && len(pos) == 1 && pos[0] == "work" {
		// 옛 플러그인·옛 런타임의 작업 기록 훅. 작업 기록은 orbit-code로 옮겼다: 입력을 버리고 끝낸다.
		_, _ = io.Copy(io.Discard, io.LimitReader(a.stdin, 4<<20))
		return nil
	}
	if err != nil || len(pos) != 1 || !slices.Contains([]string{"session-start", "prompt", "pre-tool", "subagent-start", "session-end"}, pos[0]) || (*host != "claude" && *host != "codex") ||
		(*runtime != "" && *runtime != runtimeOrbitPi) || (*recordOnly && pos[0] != "prompt") {
		fmt.Fprintln(a.stderr, "orbit-mem hook: "+i18n.T("cli.hook.bad_args"))
		return nil
	}
	// 설계 · Studio 4차 「Claude 엔진 중복 규칙」: orbit-pi가 띄운 세션에서는 기억 주입·활동 기록을
	// orbit-pi가 맡는다. 플러그인 훅(--runtime 없음)은 아무것도 하지 않고,
	// orbit-pi가 같은 입력으로 --runtime orbit-pi를 붙여 부른 훅만 기록한다.
	if *runtime == "" && os.Getenv(runtimeEnv) == runtimeOrbitPi {
		_, _ = io.Copy(io.Discard, io.LimitReader(a.stdin, 4<<20))
		return nil
	}
	who := launcherOf(*runtime, *launcher)
	deadline := time.After(hookDeadline)
	done := make(chan string, 1)
	finished := make(chan struct{}) // 사건·활동 기록까지 남겼다
	go func() {
		defer close(finished)
		defer func() {
			if recover() != nil {
				select {
				case done <- "":
				default:
				}
			}
		}()
		b, _ := io.ReadAll(io.LimitReader(a.stdin, 4<<20))
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintln(a.stderr, "orbit-mem hook: "+i18n.T("cli.hook.config_failed", err))
			done <- ""
			return
		}
		var in hookInput
		_ = json.Unmarshal(b, &in)
		if _, err := os.Stat(paths().DB()); err != nil { // 설치 전: 기억 DB를 만들지 않는다
			done <- ""
			return
		}
		s, err := openStore()
		if err != nil {
			done <- ""
			return
		}
		defer s.Close()
		var p project.Project
		if dir := sessionDir(in.Cwd); dir != "" {
			p = project.Resolve(dir)
		}
		if *recordOnly {
			done <- ""
		} else {
			memoryHook(a, s, done, pos[0], *host, in, p, cfg, *pluginVersion, who)
		}
		if pos[0] != "pre-tool" && pos[0] != "subagent-start" { // 도구 호출·서브에이전트 시작은 활동 기록이 아니다
			recordActivity(s, pos[0], *host, in, p, who, at)
		}
		if pos[0] == "session-start" {
			autoBackup(cfg)
		}
	}()
	select {
	case out := <-done:
		if out != "" {
			fmt.Fprintln(a.stdout, out)
		}
		// 사건·활동 기록은 문맥을 낸 뒤에 쓴다. 시간 안에 끝나지 않으면 프로세스와 함께 사라진다.
		select {
		case <-finished:
		case <-deadline:
		}
	case <-deadline:
		fmt.Fprintln(a.stderr, "orbit-mem hook: "+i18n.T("cli.hook.timeout"))
	}
	return nil
}

// hookDeadline is how long a hook may take before it gives up without output.
const hookDeadline = 4 * time.Second

// Runtime marks (설계 · Studio 4차 「Claude 엔진 중복 규칙」). orbit-pi puts
// ORBIT_RUNTIME=orbit-pi into the claude it starts and calls the hooks itself
// with --runtime orbit-pi and --launcher (ORBIT_LAUNCHER) for the activity log.
const (
	runtimeEnv     = "ORBIT_RUNTIME"
	runtimeOrbitPi = "orbit-pi"
	launcherEnv    = "ORBIT_LAUNCHER"
	parentEnv      = "ORBIT_PARENT" // 이 세션을 띄운 세션(활동 기록의 parent)
)

// hookCaller is who called a hook: a person's own session or orbit-pi.
type hookCaller struct {
	launcher string
	runtime  bool
}

// askable says whether a person is in the session to answer a question: their own session,
// or one orbit-pi started for Studio or Telegram (not a workflow, not an unknown launcher).
func (c hookCaller) askable() bool {
	return c.launcher == "human" || c.launcher == "studio" || c.launcher == "telegram"
}

// launcherOf is the activity log's launcher: human for a person's own session; for
// orbit-pi the launcher it names (studio·telegram·workflow…), else orbit-pi.
func launcherOf(runtime, launcher string) hookCaller {
	if runtime == "" {
		return hookCaller{launcher: "human"}
	}
	if launcher == "" {
		launcher = os.Getenv(launcherEnv)
	}
	switch launcher {
	case "studio", "telegram", "workflow":
	default:
		launcher = runtimeOrbitPi // 모르는 값은 짐작하지 않는다
	}
	return hookCaller{launcher: launcher, runtime: true}
}

// recordActivity keeps the session in the activity log (설계 · orbit-code 4차 M5). Sessions
// inside a retrospective run are not activity.
func recordActivity(s *memory.Store, which, host string, in hookInput, p project.Project, who hookCaller, at int64) {
	if os.Getenv("ORBIT_RETRO_ID") != "" || in.SessionID == "" {
		return
	}
	kind := map[string]string{"session-start": memory.ActivityStart, "prompt": memory.ActivityPrompt, "session-end": memory.ActivityEnd}[which]
	_ = s.RecordActivity(memory.ActivityEvent{Kind: kind, Host: host, Session: in.SessionID, Cwd: in.Cwd, ProjectScope: p.Scope, ProjectLabel: p.Label,
		Launcher: who.launcher, Parent: os.Getenv(parentEnv), Prompt: in.Prompt, At: at})
}

// memoryHook is the memory context of session-start and prompt (설계 1차 「호스트에 붙는 방식」).
func memoryHook(a *app, s *memory.Store, done chan<- string, which, host string, in hookInput, p project.Project, cfg config.Config, pluginVersion string, who hookCaller) {
	scope, where := p.Scope, whereOf(p)
	if which == "session-end" {
		done <- ""
		return
	}
	var inj inject.Injection
	kind := memory.EventInjectSessionStart
	switch which {
	case "session-start":
		if renewsBundle(in.Source) {
			inject.ResetSession(s, host, in.SessionID)
		}
		inj = inject.SessionBundle(s, cfg, where, host, in.SessionID)
		text := withVersionNote(inj.Text, pluginVersion)
		text = withUpdateNote(text, cfg)
		if who.askable() {
			text = withHostMemoryNote(text, cfg, in.Cwd)
		}
		done <- inject.HookOutput("SessionStart", text)
	case "pre-tool":
		kind = memory.EventInjectTool
		inj = inject.ToolInjection(s, cfg, where, host, in.sessionKey(), in.ToolName)
		done <- inject.HookOutput("PreToolUse", inj.Text)
	case "subagent-start":
		kind = memory.EventInjectSubagent
		inj = inject.SubagentStart(s, cfg)
		done <- inject.HookOutput("SubagentStart", inj.Text)
	default:
		kind = memory.EventInjectPrompt
		inj = inject.PromptInjectionFor(s, cfg, where, in.Prompt, host, in.SessionID, inject.Options{})
		done <- inject.HookOutput("UserPromptSubmit", inj.Text)
	}
	// 문맥을 먼저 내보내고 세션 표시와 지표용 사건을 남긴다(설계 「측정」).
	inject.RecordShown(s, host, in.sessionKey(), inj)
	recordInjection(s, cfg, kind, host, in.sessionKey(), scope, in.Prompt, inj)
}

// renewsBundle says whether a session start makes the session's bundle anew: after a
// compaction or /clear the earlier context is gone, so the cache it kept is too
// (설계 · orbit-mem 2판 AC-4). A resume keeps the bundle.
func renewsBundle(source string) bool { return source == "compact" || source == "clear" }

// withVersionNote adds one line at the end of the session-start context when the plugin
// that called the hook is another version than this orbit-mem (the plugin from GitHub and
// the installed binary can drift apart). The context is for the AI, so it is Korean like
// the rest of it.
func withVersionNote(text, pluginVersion string) string {
	if pluginVersion == "" || pluginVersion == version.Version {
		return text
	}
	line := fmt.Sprintf("orbit-mem 판(%s)과 플러그인 판(%s)이 다릅니다: `orbit-mem install`로 맞추세요.", version.Version, pluginVersion) // i18n:keep AI가 읽는 세션 문맥
	if text = strings.TrimRight(text, "\n"); text == "" {
		return line
	}
	return text + "\n" + line
}

// recordInjection counts one injection as a use event. Levels that inject nothing,
// empty requests and hooks inside a retrospective run are not use, so they are not counted.
func recordInjection(s *memory.Store, cfg config.Config, kind, host, session, scope, prompt string, inj inject.Injection) {
	if os.Getenv("ORBIT_RETRO_ID") != "" || cfg.Inject == config.InjectOff ||
		(kind == memory.EventInjectPrompt && (cfg.Inject != config.InjectFull || strings.TrimSpace(prompt) == "")) ||
		((kind == memory.EventInjectTool || kind == memory.EventInjectSubagent) && len(inj.MemoryIDs) == 0) { // 도구 호출·서브에이전트마다 부르므로 넣은 것이 있을 때만 센다
		return
	}
	data := map[string]any{"memoryIds": inj.MemoryIDs, "chars": inj.Chars}
	if kind == memory.EventInjectSessionStart {
		data["omitted"] = inj.Omitted
	}
	_ = s.RecordEvent(memory.Event{Kind: kind, Host: host, Session: session, Scope: scope}, data)
}

// kindName and stateName are the names a person reads; an unknown code is shown as is.
func kindName(k string) string {
	if !contains(memory.Kinds, k) {
		return k
	}
	return i18n.T("kind." + k)
}

func stateName(s string) string {
	if !contains([]string{memory.StateCandidate, memory.StateConfirmed, memory.StateStale, memory.StateHidden}, s) {
		return s
	}
	return i18n.T("state." + s)
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
			fmt.Fprintln(w, "    "+i18n.T("cli.proposals.target", m.ID, kindName(m.Kind), stateName(m.State), m.Title))
		}
	}
	if p.Payload.Note != "" {
		fmt.Fprintln(w, "    "+i18n.T("cli.proposals.link_note", p.Payload.Note))
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
	all := fs.Bool("all", false, i18n.T("flag.proposals.all"))
	title := fs.String("title", "", i18n.T("flag.proposals.title"))
	body := fs.String("body", "", i18n.T("flag.proposals.body"))
	bodyFile := fs.String("body-file", "", i18n.T("flag.proposals.body_file"))
	note := fs.String("note", "", i18n.T("flag.proposals.note"))
	dryRun := fs.Bool("dry-run", false, i18n.T("flag.proposals.dry_run"))
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
		fmt.Fprintln(a.stdout, i18n.T("cli.proposals.list_tail", s.PendingCount()))
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
	case "drain":
		if len(pos) != 0 {
			return errUsage
		}
		return a.drain(s, *dryRun, *asJSON)
	}
	return errUsage
}

// drain closes the pending proposals left from before the queue went away
// (설계 · orbit-mem 2판 18.5). With dryRun it works on a copy of the
// database and reports what would happen.
func (a *app) drain(s *memory.Store, dryRun, asJSON bool) error {
	target := s
	if dryRun {
		dir, err := os.MkdirTemp("", "orbit-mem-drain-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		path, err := s.Snapshot(dir, "drain")
		if err != nil {
			return err
		}
		if target, err = memory.Open(path); err != nil {
			return err
		}
		defer target.Close()
		target.Policy = s.Policy
	}
	res, err := target.DrainPending()
	if asJSON {
		if jerr := a.json(map[string]any{"dryRun": dryRun, "result": res}); jerr != nil {
			return jerr
		}
		return err
	}
	for _, it := range res.Items {
		fmt.Fprintf(a.stdout, "%s %s [%s] %s\n    %s\n", it.ProposalID, it.Kind, it.Status, it.Title, it.Note)
	}
	if dryRun {
		fmt.Fprintln(a.stdout, i18n.T("cli.proposals.drain_dry", len(res.Items), res.Applied, res.Dropped))
	} else {
		fmt.Fprintln(a.stdout, i18n.T("cli.proposals.drained", len(res.Items), res.Applied, res.Dropped))
	}
	if res.Left > 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.proposals.drain_left", res.Left))
	}
	return err
}

func (a *app) reportDecision(res memory.DecideResult) error {
	switch res.Proposal.Status {
	case memory.StatusApproved:
		fmt.Fprint(a.stdout, i18n.T("decision.approved", res.Proposal.ID))
	case memory.StatusRejected:
		fmt.Fprint(a.stdout, i18n.T("decision.rejected", res.Proposal.ID))
	case memory.StatusSettled:
		note := res.Proposal.Note
		for _, k := range memory.SettledNoteKeys {
			if note == i18n.TIn(i18n.KO, k) {
				note = i18n.T(k)
			}
		}
		fmt.Fprint(a.stdout, i18n.T("decision.settled", res.Proposal.ID, note))
	default:
		fmt.Fprint(a.stdout, res.Proposal.ID+" "+res.Proposal.Status+".")
	}
	if res.Link != "" {
		if res.Proposal.Kind == memory.ProposalRevert {
			fmt.Fprint(a.stdout, " "+i18n.T("decision.unlinked", res.Link))
		} else {
			fmt.Fprint(a.stdout, " "+i18n.T("decision.linked", res.Link))
		}
	}
	if res.Created != "" {
		fmt.Fprint(a.stdout, " "+i18n.T("decision.created", res.Created))
	}
	if len(res.Changed) > 0 {
		fmt.Fprint(a.stdout, " "+i18n.T("decision.changed", strings.Join(res.Changed, ", ")))
	}
	if res.File != "" {
		fmt.Fprint(a.stdout, " "+i18n.T("decision.file", res.File))
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
		fmt.Fprintln(a.stdout, i18n.T("cli.proposals.none_waiting"))
		return nil
	}
	in := bufio.NewReader(a.stdin)
	for i, p := range ps {
		fmt.Fprintf(a.stdout, "\n(%d/%d) ", i+1, len(ps))
		printProposal(a.stdout, s, p, true)
		fmt.Fprint(a.stdout, i18n.T("cli.proposals.review_prompt")+" > ")
		line, err := in.ReadString('\n')
		answer := strings.TrimSpace(line)
		switch answer {
		case "a", "r":
			decision := map[string]string{"a": "approve", "r": "reject"}[answer]
			res, derr := s.Decide(memory.DecideInput{ID: p.ID, Decision: decision})
			if derr != nil {
				fmt.Fprintln(a.stdout, i18n.T("cli.proposals.decide_failed", derr))
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
	fmt.Fprintln(a.stdout, "\n"+i18n.T("cli.proposals.left", s.PendingCount()))
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
	case "language":
		l, ok := i18n.Parse(val)
		if !ok {
			return i18n.Errorf("config.err.language", val)
		}
		cfg.Language, val = string(l), string(l)
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
	case "updateCheck":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return i18n.Errorf("cli.config.err.bool", key, val)
		}
		cfg.UpdateCheck = b
	case "autoBackup":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return i18n.Errorf("cli.config.err.bool", key, val)
		}
		cfg.AutoBackup = b
	case "sessionRetro":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return i18n.Errorf("cli.config.err.bool", key, val)
		}
		cfg.SessionRetro = b
	case "sessionRetroModel":
		cfg.SessionRetroModel = val
	case "hostMemory":
		cfg.HostMemory = val
	case "retiredNames":
		cfg.RetiredNames = nil
		for _, n := range strings.Split(val, ",") {
			if n = strings.TrimSpace(n); n != "" {
				cfg.RetiredNames = append(cfg.RetiredNames, n)
			}
		}
	case "sessionRetroDailyMax", "recallChars", "ledgerWindowDays", "graphTidyMemories", "graphTidyUntagged", "graphTidyProposals", "legacyMemories", "legacyBatches",
		"confirmSeenDays", "confirmFollowedVerified", "correctionRepeatSessions", "candidateExpiryDays":
		n, err := strconv.Atoi(val)
		if err != nil {
			return i18n.Errorf("cli.config.err.number", key, val)
		}
		*map[string]*int{"sessionRetroDailyMax": &cfg.SessionRetroDailyMax, "recallChars": &cfg.RecallChars, "ledgerWindowDays": &cfg.LedgerWindowDays,
			"graphTidyMemories": &cfg.GraphTidyMemories, "graphTidyUntagged": &cfg.GraphTidyUntagged, "graphTidyProposals": &cfg.GraphTidyProposals,
			"legacyMemories": &cfg.LegacyMemories, "legacyBatches": &cfg.LegacyBatches,
			"confirmSeenDays": &cfg.ConfirmSeenDays, "confirmFollowedVerified": &cfg.ConfirmFollowedVerified, "correctionRepeatSessions": &cfg.CorrectionRepeatSessions,
			"candidateExpiryDays": &cfg.CandidateExpiryDays}[key] = n
	default:
		return i18n.Errorf("cli.config.err.unknown_key", key, "language·retroHost·retroModel·retroHour·inject·recentDecisions·backupKeep·autoBackup·applyTargets·sessionRetro·sessionRetroModel·sessionRetroDailyMax·recallChars·ledgerWindowDays·graphTidyMemories·graphTidyUntagged·graphTidyProposals·legacyMemories·legacyBatches·retiredNames·confirmSeenDays·confirmFollowedVerified·correctionRepeatSessions·candidateExpiryDays·hostMemory·updateCheck")
	}
	if err := config.Save(paths().Config(), cfg); err != nil {
		return err
	}
	if key == "language" {
		i18n.Set(i18n.Resolve(os.Getenv(i18n.EnvVar), cfg.Language))
	}
	fmt.Fprint(a.stdout, i18n.T("cli.config.saved", key, val))
	if key == "retroHour" {
		fmt.Fprint(a.stdout, " "+i18n.T("cli.config.retro_hour_note"))
	}
	fmt.Fprintln(a.stdout)
	return nil
}
