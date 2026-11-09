package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	hooks "github.com/crispebble/orbit-mem/internal/hook" // 시험의 hook 도우미와 이름이 겹치지 않게
	"github.com/crispebble/orbit-mem/internal/hook/claudecode"
	"github.com/crispebble/orbit-mem/internal/hook/codex"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/project"
	"github.com/crispebble/orbit-mem/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// 통로마다 출처 등급의 상한(결정 D6): 조립 지점이 값으로 넣는다. 도메인은 호스트 이름을 모르고, 상한은
// actor(ORBIT_ACTOR·--actor)에서 끌어내지 않는다.
const (
	mcpTrustLimit   = memory.TrustAgent // MCP 서버: 호스트 AI
	hostTrustLimit  = memory.TrustAgent // host call·tool 별칭: 호스트 AI
	humanTrustLimit = memory.TrustOwner // 사람이 터미널에서 부르는 thin CLI
)

// actorOf is who writes through a command: the flag, else ORBIT_ACTOR, else def.
func actorOf(flag, def string) string {
	return mcpserver.ResolveActor(flag, os.Getenv("ORBIT_ACTOR"), def)
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

func sessionProject() project.Project {
	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return project.Resolve(dir)
}

func cmdMCP(a *app, args []string) error {
	fs := a.flags("mcp")
	actor := fs.String("actor", "", i18n.T("flag.mcp.actor"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	// MCP 응답 글은 AI가 읽는다(ONID-61 대상 아님). 공유 오류(기억·제안)도 언어 설정과 무관하게 한국어로 낸다.
	i18n.Set(i18n.KO)
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
	srv := mcpserver.New(s, mcpserver.Options{Project: p.Scope, ProjectLabel: p.Label, Dir: p.Dir, Git: p.Git, Temp: p.Temp, Version: version.Version,
		Actor: actorOf(*actor, memory.ActorAI), TrustLimit: mcpTrustLimit, LoadProducts: func() (place.Map, error) {
			cfg, err := loadConfig()
			return cfg.Products, err
		}, LoadSaveSimilarMin: saveSimilarMinOf})
	return srv.Run(ctx, &mcp.StdioTransport{})
}

// hookAdapter is the adapter of a hook's --host: how that host's hook input becomes contract
// requests and how its context is printed (ORBIT-23 D5). nil for a host with no hooks.
func hookAdapter(name string) hooks.Adapter {
	switch name {
	case claudecode.Host:
		return claudecode.New(os.Getenv)
	case codex.Host:
		return codex.New()
	}
	return nil
}

// cmdHook never fails the host: any problem, wrong arguments, a timeout, a locked or unreadable
// DB included, prints nothing to stdout and exits 0 (exit 2 would block a Claude Code prompt).
// It is the assembly point of the hooks: it picks the host's adapter and runs the hook's contract
// operations on the host port (session-start → context, compact·clear → reset first, prompt →
// recall, pre-tool → tool-context, subagent-start → context for a subagent, session-end →
// session-end). Each operation keeps its own time limit; the context is printed as soon as it is
// computed and its records follow within that limit.
func cmdHook(a *app, args []string) error {
	at := time.Now().UnixMilli() // 입력을 읽기 전에 찍는다
	fs := a.flags("hook")
	fs.SetOutput(io.Discard)
	hostName := fs.String("host", "claude", "claude | codex")
	pluginVersion := fs.String("plugin-version", "", "") // 플러그인 훅이 붙이는 플러그인 판: 시작 훅만 본다
	pos, err := parse(fs, args)
	var ev hooks.Event
	ok := err == nil && len(pos) == 1
	if ok {
		ev, ok = hooks.ParseEvent(pos[0])
	}
	adapter := hookAdapter(*hostName)
	if !ok || adapter == nil {
		fmt.Fprintln(a.stderr, "orbit-mem hook: "+i18n.T("cli.hook.bad_args"))
		return nil
	}
	defer func() { _ = recover() }() // 어떤 panic도 호스트를 막지 않는다
	in, err := hooks.ReadInput(a.stdin, 4<<20, hookInputWait)
	if errors.Is(err, hooks.ErrInputWait) {
		fmt.Fprintln(a.stderr, "orbit-mem hook: "+i18n.T("cli.hook.timeout"))
		return nil
	}
	if err != nil {
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(a.stderr, "orbit-mem hook: "+i18n.T("cli.hook.config_failed", err))
		return nil
	}
	timedOut := false
	noted := make(chan struct{}) // 안내 줄을 다 붙였다: 유지보수가 그 뒤에 돈다(옛 훅의 차례)
	var noteOnce sync.Once
	doneNoting := func() { noteOnce.Do(func() { close(noted) }) }
	defer doneNoting()
	hooks.Runner{
		Service: hostServiceWith(func() (config.Config, error) { return cfg, nil }, noted, launcherHuman),
		Options: host.Options{At: at},
		// 시작 문맥 뒤의 판·업데이트·호스트 기억 안내 줄(호스트 기억은 훅 입력의 날 cwd로 본다).
		Note: func(ev hooks.Event, plan hooks.Plan, text string) string {
			defer doneNoting()
			if ev != hooks.SessionStart {
				return text
			}
			text = withVersionNote(text, *pluginVersion)
			text = withUpdateNote(text, cfg)
			return withHostMemoryNote(text, cfg, plan.Cwd)
		},
		Failed: func(op string, e *host.Error) {
			if e.Code == host.CodeTimeout && !timedOut {
				timedOut = true
				fmt.Fprintln(a.stderr, "orbit-mem hook: "+i18n.T("cli.hook.timeout"))
			}
		},
	}.Run(adapter, ev, in, a.stdout)
	return nil
}

// hookDeadline is how long a hook waits for its input to end before it gives up without output.
const hookDeadline = 4 * time.Second

// hookInputWait is how long a hook waits for its input to end (tests shorten it).
var hookInputWait = hookDeadline

// parentEnv names the session that started this one (the activity log's parent).
const parentEnv = "ORBIT_PARENT"

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
	case "autoTidy":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return i18n.Errorf("cli.config.err.bool", key, val)
		}
		cfg.AutoTidy = b
	case "hostMemory":
		cfg.HostMemory = val
	case "recallChars", "ledgerWindowDays", "confirmSeenDays", "confirmFollowedVerified", "correctionRepeatSessions", "candidateExpiryDays":
		n, err := strconv.Atoi(val)
		if err != nil {
			return i18n.Errorf("cli.config.err.number", key, val)
		}
		*map[string]*int{"recallChars": &cfg.RecallChars, "ledgerWindowDays": &cfg.LedgerWindowDays,
			"confirmSeenDays": &cfg.ConfirmSeenDays, "confirmFollowedVerified": &cfg.ConfirmFollowedVerified, "correctionRepeatSessions": &cfg.CorrectionRepeatSessions,
			"candidateExpiryDays": &cfg.CandidateExpiryDays}[key] = n
	default:
		return i18n.Errorf("cli.config.err.unknown_key", key, "language·inject·recentDecisions·backupKeep·autoBackup·autoTidy·recallChars·ledgerWindowDays·confirmSeenDays·confirmFollowedVerified·correctionRepeatSessions·candidateExpiryDays·hostMemory·updateCheck")
	}
	if err := config.Save(paths().Config(), cfg); err != nil {
		return err
	}
	if key == "language" {
		i18n.Set(i18n.Resolve(os.Getenv(i18n.EnvVar), cfg.Language))
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.config.saved", key, val))
	return nil
}
