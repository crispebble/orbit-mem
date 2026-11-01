package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/activity"
	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/install"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/memplugin"
	"github.com/onidot-labs/orbit-mem/internal/migrate"
	"github.com/onidot-labs/orbit-mem/internal/retro"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
	"github.com/onidot-labs/orbit-mem/internal/version"
)

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "orbit-mem"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

func newRunner(s *memory.Store, cfg config.Config) *retro.Runner {
	return &retro.Runner{Store: s, Cfg: cfg, Paths: paths(), Roots: transcript.DefaultRoots(), Loc: time.Local, Mem: selfPath()}
}

func cmdRetro(a *app, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	fs := a.flags("retro " + args[0])
	day := fs.String("day", "", i18n.T("flag.retro.day"))
	week := fs.String("week", "", i18n.T("flag.retro.week"))
	n := fs.Int("n", 10, i18n.T("flag.count"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args[1:]); err != nil {
		return err
	}
	r := newRunner(s, cfg)
	ctx, cancel := signalContext()
	defer cancel()
	switch args[0] {
	case "sessions":
		pending, err := retro.Pending(s)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.retro.sessions_waiting", time.Now().Format("2006-01-02 15:04:05"), len(pending)))
		if len(pending) == 0 {
			return nil
		}
		results, err := r.DrainSessions(ctx)
		a.printResults(results)
		return err
	case "run":
		var results []retro.Result
		switch {
		case *day != "":
			res, err := r.RunOne(ctx, retro.KindDaily, *day)
			results = append(results, res)
			if err != nil {
				a.printResults(results)
				return err
			}
		case *week != "":
			res, err := r.RunOne(ctx, retro.KindWeekly, *week)
			results = append(results, res)
			if err != nil {
				a.printResults(results)
				return err
			}
		default:
			fmt.Fprintln(a.stdout, i18n.T("cli.retro.catch_up", time.Now().Format("2006-01-02 15:04"), cfg.RetroHost))
			results, err = r.CatchUp(ctx)
			a.printResults(results)
			return err
		}
		a.printResults(results)
		return nil
	case "tidy":
		res, err := r.RunTidyOne(ctx)
		a.printResults([]retro.Result{res})
		return err
	case "legacy":
		res, err := r.RunLegacyOne(ctx)
		a.printResults([]retro.Result{res})
		if c, cerr := s.CountLegacy(); cerr == nil && c.Total > 0 {
			fmt.Fprintln(a.stdout, i18n.T("cli.retro.legacy_count", c.Total, c.Judged, c.Left))
		}
		return err
	case "status":
		list, err := retro.Recent(s, *n)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(list)
		}
		for _, st := range list {
			fmt.Fprintf(a.stdout, "%s %s %-7s %s %s%s\n", st.Period, st.Kind, st.Status, st.ID, sessionTag(st.Session), oneLine(st.Summary+" "+st.Error, 100))
			for _, f := range st.Flags {
				fmt.Fprintln(a.stdout, "    "+i18n.T("cli.retro.flag", f))
			}
		}
		pending, _ := retro.Pending(s)
		fmt.Fprintln(a.stdout, i18n.T("cli.retro.status_tail", s.PendingCount(), len(pending)))
		return nil
	case "material":
		if *day == "" {
			return errUsage
		}
		sessions, err := transcript.Collect(transcript.DefaultRoots(), *day, time.Local, []string{paths().Home})
		if err != nil {
			return err
		}
		turns := 0
		for _, ss := range sessions {
			turns += len(ss.Turns)
			fmt.Fprintln(a.stdout, i18n.T("cli.retro.material_session", ss.Host, ss.ID, ss.Cwd, len(ss.Turns)))
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.retro.material_total", len(sessions), turns, len(transcript.CodexSummaries(transcript.DefaultRoots(), *day))))
		return nil
	}
	return errUsage
}

func sessionTag(session string) string {
	if session == "" {
		return ""
	}
	return "[" + session + "] "
}

func (a *app) printResults(results []retro.Result) {
	for _, r := range results {
		line := fmt.Sprintf("%s %s: %s", r.Kind, r.Period, r.Status)
		if r.Error != "" {
			line += " — " + oneLine(r.Error, 300)
		}
		fmt.Fprintln(a.stdout, line)
	}
}

func cmdMigrate(a *app, args []string) error {
	fs := a.flags("migrate")
	from := fs.String("from", "", i18n.T("flag.migrate.from"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.migrate.dry_run"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *from == "" {
		return errUsage
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	if !*dry {
		p, err := s.Snapshot(paths().Backups(), "before-migrate")
		if err != nil {
			return i18n.Errorf("cli.migrate.err.snapshot", err)
		}
		fmt.Fprintln(a.stderr, i18n.T("cli.migrate.snapshot", p))
	}
	roots := transcript.DefaultRoots()
	var dirs []string
	for _, f := range transcript.HostMemoryFiles(roots) {
		dirs = append(dirs, filepath.Dir(f))
	}
	rep, err := migrate.Run(s, migrate.Options{From: *from, DryRun: *dry, HostMemoryDirs: dirs})
	if err != nil {
		return err
	}
	if *asJSON {
		return a.json(rep)
	}
	if *dry {
		fmt.Fprintln(a.stdout, i18n.T("cli.migrate.done_dry", rep.Records, rep.Imported, rep.Skipped))
	} else {
		fmt.Fprintln(a.stdout, i18n.T("cli.migrate.done", rep.Records, rep.Imported, rep.Skipped))
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.migrate.by_state_kind", counts(rep.ByState), counts(rep.ByKind)))
	fmt.Fprintln(a.stdout, i18n.T("cli.migrate.sources", rep.Sources, rep.FilesSame, rep.FilesChanged, rep.FilesUnknown, rep.Proposals, rep.Absorbed))
	return nil
}

func counts(m map[string]int) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, " · ")
}

// localPluginDir is the local marketplace folder orbit-mem install builds for Codex (and for
// Claude Code with --local-plugin).
func localPluginDir() string { return filepath.Join(paths().Home, "plugins", memplugin.Name) }

// cmdPlugin builds orbit-mem's own plugin from the embedded source: --out (default the local
// marketplace folder) gets both host formats and both local marketplaces; --repo gets the
// GitHub marketplace this repository is (the Claude format only).
func cmdPlugin(a *app, args []string) error {
	if len(args) == 0 || args[0] != "build" {
		return errUsage
	}
	fs := a.flags("plugin build")
	out := fs.String("out", "", i18n.T("flag.plugin.out"))
	repo := fs.String("repo", "", i18n.T("flag.plugin.repo"))
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 0 || (*out != "" && *repo != "") {
		return errUsage
	}
	build, dir := memplugin.Local, *out
	if *repo != "" {
		build, dir = memplugin.Repo, *repo
	}
	if dir == "" {
		dir = localPluginDir()
	}
	files, err := build(version.Version)
	if err != nil {
		return err
	}
	if err := memplugin.Write(dir, files); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.plugin.built", memplugin.ID, version.Version, dir))
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// cmdInstall puts orbit-mem in the bin folder, writes the settings, makes the memory DB,
// schedules the retrospectives and registers orbit-mem's plugin in the hosts: Claude Code
// from the GitHub marketplace (or, with --local-plugin, the local folder), Codex always from
// the local folder it builds.
func cmdInstall(a *app, args []string) error {
	fs := a.flags("install")
	binDir := fs.String("bin-dir", install.DefaultBinDir(), i18n.T("flag.install.bin_dir"))
	retroHost := fs.String("retro-host", "", i18n.T("flag.install.retro_host"))
	retroModel := fs.String("retro-model", "", i18n.T("flag.install.retro_model"))
	pluginSource := fs.String("plugin-source", "", i18n.T("flag.install.plugin_source"))
	var targets multiFlag
	fs.Var(&targets, "apply-target", i18n.T("flag.install.apply_target"))
	localPlugin := fs.Bool("local-plugin", false, i18n.T("flag.install.local_plugin"))
	noHosts := fs.Bool("no-hosts", false, i18n.T("flag.install.no_hosts"))
	noSchedule := fs.Bool("no-schedule", false, i18n.T("flag.install.no_schedule"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.install.dry_run"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p := paths()
	h := install.FindHosts()
	dst := filepath.Join(*binDir, install.BinaryName())
	dir := localPluginDir()
	claudeSource := memplugin.GitHubRepo
	if *localPlugin {
		claudeSource = dir
	}
	needLocal := !*noHosts && (h.Codex != "" || (*localPlugin && h.Claude != ""))
	steps := []string{
		i18n.T("cli.install.step_copy", selfPath(), dst),
		i18n.T("cli.install.step_home", p.Home),
	}
	if *pluginSource != "" {
		steps = append(steps, i18n.T("cli.install.step_source", *pluginSource))
	}
	if !*noSchedule {
		steps = append(steps, i18n.T("cli.install.step_schedule", config.Default().RetroHour))
	}
	if !*noHosts {
		if needLocal {
			steps = append(steps, i18n.T("cli.install.step_build", memplugin.ID, dir))
		}
		if h.Claude != "" {
			steps = append(steps, i18n.T("cli.install.step_claude", claudeSource, memplugin.Market, memplugin.ID))
		}
		if h.Codex != "" {
			steps = append(steps, i18n.T("cli.install.step_codex", dir, memplugin.ID))
		}
		if h.Claude == "" && h.Codex == "" {
			steps = append(steps, i18n.T("cli.install.step_no_hosts"))
		}
	}
	if *dry {
		for _, s := range steps {
			fmt.Fprintln(a.stdout, "- "+s)
		}
		return nil
	}
	copied, err := install.CopyBinary(selfPath(), dst)
	if err != nil {
		return i18n.Errorf("cli.install.err.copy", err)
	}
	if copied {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.installed", dst))
	}
	if found, err := exec.LookPath("orbit-mem"); err != nil {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.not_on_path", *binDir))
	} else if !samePath(found, dst) {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.other_on_path", found, dst))
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *retroHost != "" {
		cfg.RetroHost = *retroHost
	} else if h.Claude == "" && h.Codex != "" {
		cfg.RetroHost = "codex"
	}
	if *retroModel != "" {
		cfg.RetroModel = *retroModel
	}
	cfg.ClaudePath, cfg.CodexPath = h.Claude, h.Codex
	if *pluginSource != "" {
		abs, _ := filepath.Abs(*pluginSource)
		if _, err := os.Stat(filepath.Join(abs, "plugin.json")); err != nil {
			return i18n.Errorf("cli.install.err.not_source", abs)
		}
		cfg.PluginSource = abs
		if !contains(cfg.ApplyTargets, abs) {
			cfg.ApplyTargets = append(cfg.ApplyTargets, abs)
		}
	}
	for _, t := range targets {
		abs, _ := filepath.Abs(t)
		if !contains(cfg.ApplyTargets, abs) {
			cfg.ApplyTargets = append(cfg.ApplyTargets, abs)
		}
	}
	if err := config.Save(p.Config(), cfg); err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	s.Close()
	if !*noSchedule {
		var dirs []string
		for _, hp := range []string{h.Claude, h.Codex} {
			if hp != "" && !contains(dirs, filepath.Dir(hp)) {
				dirs = append(dirs, filepath.Dir(hp))
			}
		}
		home := ""
		if os.Getenv("ORBIT_HOME") != "" {
			home = p.Home
		}
		where, err := install.ApplySchedule(install.Schedule{Bin: dst, Hour: cfg.RetroHour, Home: home, PathDirs: dirs, Logs: p.Logs()}, install.RunCommand)
		if err != nil {
			return i18n.Errorf("cli.install.err.schedule", err)
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.install.scheduled", cfg.RetroHour, where))
		fmt.Fprintln(a.stdout, i18n.T("cli.install.scheduled_note"))
	}
	if *noHosts {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.hosts_skipped", memplugin.GitHubRepo))
		fmt.Fprintln(a.stdout, i18n.T("cli.install.done"))
		return nil
	}
	if err := a.registerHosts(h, needLocal, dir, claudeSource); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.install.done"))
	return nil
}

// registerHosts builds the local plugin folder when a host needs it and registers the plugin
// in every host present. A host that fails does not stop the other.
func (a *app) registerHosts(h install.Hosts, needLocal bool, dir, claudeSource string) error {
	if h.Claude == "" && h.Codex == "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.no_hosts", memplugin.GitHubRepo))
		return nil
	}
	if needLocal {
		files, err := memplugin.Local(version.Version)
		if err == nil {
			err = memplugin.Write(dir, files)
		}
		if err != nil {
			return i18n.Errorf("cli.install.err.build", dir, err)
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.plugin.built", memplugin.ID, version.Version, dir))
	}
	failed := false
	if h.Claude != "" {
		if err := h.RegisterClaude(claudeSource, memplugin.Name, memplugin.Market); err != nil {
			fmt.Fprintln(a.stderr, i18n.T("cli.install.host_failed", "Claude Code", err))
			failed = true
		} else {
			fmt.Fprintln(a.stdout, i18n.T("cli.install.registered", "Claude Code", memplugin.ID, claudeSource))
		}
	}
	if h.Codex != "" {
		if err := h.RegisterCodex(dir, memplugin.Name, memplugin.Market); err != nil {
			fmt.Fprintln(a.stderr, i18n.T("cli.install.host_failed", "Codex", err))
			failed = true
		} else {
			fmt.Fprintln(a.stdout, i18n.T("cli.install.registered", "Codex", memplugin.ID, dir))
			fmt.Fprintln(a.stdout, i18n.T("cli.install.codex_hooks"))
		}
	}
	if failed {
		return i18n.Err("cli.install.err.hosts")
	}
	return nil
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// cmdUninstall takes the retrospective schedule out and orbit-mem's plugin and marketplace
// out of the hosts. The data and the local plugin folder stay.
func cmdUninstall(a *app, args []string) error {
	if _, err := parse(a.flags("uninstall"), args); err != nil {
		return err
	}
	failed := false
	if err := install.RemoveSchedule(install.RunCommand); err != nil {
		fmt.Fprintln(a.stderr, i18n.T("cli.uninstall.schedule_error", err))
		failed = true
	}
	h := install.FindHosts()
	if h.Claude != "" {
		if err := h.UnregisterClaude(memplugin.Name, memplugin.Market); err != nil {
			fmt.Fprintln(a.stderr, i18n.T("cli.uninstall.host_error", "Claude Code", err))
			failed = true
		}
	}
	if h.Codex != "" {
		st := install.ReadCodexState(install.CodexConfigPath(), memplugin.Name, memplugin.Market)
		if err := h.UnregisterCodex(st, memplugin.Name, memplugin.Market); err != nil {
			fmt.Fprintln(a.stderr, i18n.T("cli.uninstall.host_error", "Codex", err))
			failed = true
		}
	}
	if failed {
		return i18n.Errorf("cli.uninstall.err.partial", paths().Home)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.done", memplugin.ID, paths().Home))
	fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.binary", selfPath()))
	return nil
}

// doctorReport is orbit-mem doctor --json. OpenOrbit's orb doctor reads it: keep the keys.
type doctorReport struct {
	Version      string              `json:"version"`
	Binary       string              `json:"binary"`     // this orbit-mem
	PathBinary   string              `json:"pathBinary"` // the orbit-mem hooks and MCP find on PATH ("" none)
	Home         string              `json:"home"`
	DB           bool                `json:"db"` // memory.db exists
	Config       config.Config       `json:"config"`
	Memories     map[string]int      `json:"memories"`
	Pending      int                 `json:"pendingProposals"`
	SessionQueue int                 `json:"sessionQueue"`
	Activity     activityStatus      `json:"activity"`
	Schedule     bool                `json:"schedule"`
	LastRetros   []retro.RetroStatus `json:"lastRetros"`
	LastBackup   string              `json:"lastBackup,omitempty"`
	Claude       claudeStatus        `json:"claude"`
	Codex        codexStatus         `json:"codex"`
	Problems     []problem           `json:"problems"`
}

type activityStatus struct {
	Sessions       int `json:"sessions"`       // 활동 기록(memory.db)의 세션 수
	LegacySessions int `json:"legacySessions"` // 옛 작업 기록(work.db, 읽기 전용)의 세션 수
}

// Plugin states in claudeStatus.Plugin and codexStatus.Plugin.
const (
	pluginInstalled = "installed"
	pluginMissing   = "missing"
	pluginUnknown   = "unknown" // claude plugin list --json did not answer
	pluginNoHost    = "no-host" // claude is not on PATH
)

type claudeStatus struct {
	Path        string `json:"path"`   // claude on PATH ("" none)
	Plugin      string `json:"plugin"` // installed | missing | unknown | no-host
	Version     string `json:"version,omitempty"`
	Enabled     bool   `json:"enabled"`
	Marketplace string `json:"marketplace,omitempty"` // github:<owner/repo> | directory:<path>
}

type codexStatus struct {
	Path         string `json:"path"`   // codex on PATH ("" none)
	Config       string `json:"config"` // the config.toml read
	Marketplace  bool   `json:"marketplace"`
	Plugin       string `json:"plugin"` // installed | missing
	Enabled      bool   `json:"enabled"`
	HooksTrusted bool   `json:"hooksTrusted"`
}

// problem is one thing to fix, with one line that fixes it.
type problem struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

func cmdDoctor(a *app, args []string) error {
	fs := a.flags("doctor")
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p := paths()
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	rep := doctorReport{Version: version.Version, Binary: selfPath(), Home: p.Home, Config: cfg, Memories: map[string]int{}, Problems: []problem{}}
	add := func(id, fix string, args ...any) {
		rep.Problems = append(rep.Problems, problem{ID: id, Message: i18n.T("cli.doctor.problem."+id, args...), Fix: fix})
	}
	if found, err := exec.LookPath("orbit-mem"); err == nil {
		rep.PathBinary = found
	} else {
		add("not-on-path", fmt.Sprintf(`orbit-mem install && export PATH="%s:$PATH"`, install.DefaultBinDir()))
	}
	if _, err := os.Stat(p.DB()); err == nil { // 설치 전이면 DB를 만들지 않는다
		rep.DB = true
		s, err := openStore()
		if err != nil {
			return err
		}
		defer s.Close()
		all, _ := s.List()
		for _, m := range all {
			rep.Memories[m.State]++
		}
		rep.Pending = s.PendingCount()
		if waiting, err := retro.Pending(s); err == nil {
			rep.SessionQueue = len(waiting)
		}
		rep.LastRetros, _ = retro.Recent(s, 3)
		rep.Activity.Sessions = s.ActivityCount()
	} else {
		add("db-missing", "orbit-mem install")
	}
	rep.Activity.LegacySessions, _ = activity.LegacyCount(p.WorkDB())
	rep.Schedule = install.ScheduleInstalled(install.RunCommand)
	if !rep.Schedule {
		add("schedule-missing", "orbit-mem install")
	}
	if entries, err := os.ReadDir(p.Backups()); err == nil && len(entries) > 0 {
		rep.LastBackup = filepath.Join(p.Backups(), entries[len(entries)-1].Name())
	}
	h := install.FindHosts()
	rep.Claude = claudeStatus{Path: h.Claude, Plugin: pluginNoHost}
	if h.Claude != "" {
		st := h.ClaudeStatus(memplugin.Name, memplugin.Market)
		rep.Claude.Marketplace, rep.Claude.Version, rep.Claude.Enabled = st.Marketplace, st.Version, st.Enabled
		switch {
		case !st.Known:
			rep.Claude.Plugin = pluginUnknown
		case !st.Installed:
			rep.Claude.Plugin = pluginMissing
			add("claude-plugin-missing", "orbit-mem install")
		default:
			rep.Claude.Plugin = pluginInstalled
			if !st.Enabled {
				add("claude-plugin-disabled", "claude plugin enable "+memplugin.ID)
			}
			if st.Version != "" && st.Version != version.Version {
				add("claude-plugin-version", "orbit-mem install", st.Version, version.Version)
			}
		}
	}
	cs := install.ReadCodexState(install.CodexConfigPath(), memplugin.Name, memplugin.Market)
	rep.Codex = codexStatus{Path: h.Codex, Config: install.CodexConfigPath(), Marketplace: cs.Marketplace, Plugin: pluginMissing, Enabled: cs.Enabled, HooksTrusted: cs.HooksTrusted}
	if cs.Plugin {
		rep.Codex.Plugin = pluginInstalled
	}
	if h.Codex != "" {
		switch {
		case !cs.Plugin || !cs.Marketplace:
			add("codex-plugin-missing", "orbit-mem install")
		case !cs.Enabled:
			add("codex-plugin-disabled", "orbit-mem install")
		case !cs.HooksTrusted:
			add("codex-hooks-untrusted", i18n.T("cli.doctor.fix.codex_hooks"))
		}
	}
	if a.wantJSON(*asJSON) {
		return a.json(rep)
	}
	yes := func(b bool) string {
		if b {
			return i18n.T("cli.doctor.yes")
		}
		return i18n.T("cli.doctor.no")
	}
	orNone := func(s string) string {
		if s == "" {
			return i18n.T("cli.doctor.no")
		}
		return s
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.head", rep.Version, rep.Binary, runtime.GOOS, runtime.GOARCH, rep.Home))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.path_binary", orNone(rep.PathBinary)))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.language", i18n.Current()))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.memories", rep.Memories["confirmed"], rep.Memories["candidate"], rep.Memories["stale"], rep.Memories["hidden"], rep.Pending))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.retro", cfg.RetroHost, cfg.RetroModel, cfg.RetroHour, cfg.Inject, yes(rep.Schedule)))
	sessionRetro := i18n.T("cli.doctor.session_retro_off")
	if cfg.SessionRetro {
		sessionRetro = i18n.T("cli.doctor.on_model", cfg.SessionModel())
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.session_retro", sessionRetro, rep.SessionQueue, cfg.RecallChars))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.activity", rep.Activity.Sessions, rep.Activity.LegacySessions))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.claude", orNone(rep.Claude.Path), memplugin.ID, rep.Claude.Plugin, rep.Claude.Version, orNone(rep.Claude.Marketplace)))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.codex", orNone(rep.Codex.Path), memplugin.ID, rep.Codex.Plugin, yes(rep.Codex.Marketplace), yes(rep.Codex.HooksTrusted)))
	for _, r := range rep.LastRetros {
		fmt.Fprintln(a.stdout, i18n.T("cli.doctor.last_retro", r.Period, r.Kind, r.Status, oneLine(r.Summary+" "+r.Error, 80)))
	}
	if rep.LastBackup != "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.doctor.last_backup", rep.LastBackup))
	}
	if len(rep.Problems) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.doctor.no_problems"))
	}
	for _, pr := range rep.Problems {
		fmt.Fprintln(a.stdout, i18n.T("cli.doctor.problem", pr.Message, pr.Fix))
	}
	return nil
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
