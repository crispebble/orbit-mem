package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/onidot-labs/openorbit/internal/config"
	"github.com/onidot-labs/openorbit/internal/i18n"
	"github.com/onidot-labs/openorbit/internal/install"
	"github.com/onidot-labs/openorbit/internal/memory"
	"github.com/onidot-labs/openorbit/internal/migrate"
	"github.com/onidot-labs/openorbit/internal/orbitplugin"
	"github.com/onidot-labs/openorbit/internal/pluginbuild"
	"github.com/onidot-labs/openorbit/internal/retro"
	"github.com/onidot-labs/openorbit/internal/transcript"
	"github.com/onidot-labs/openorbit/internal/version"
	"github.com/onidot-labs/openorbit/internal/work"
)

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "orb"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

func newRunner(s *memory.Store, cfg config.Config) *retro.Runner {
	return &retro.Runner{Store: s, Cfg: cfg, Paths: paths(), Roots: transcript.DefaultRoots(), Loc: time.Local, Orb: selfPath()}
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

func cmdPlugin(a *app, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "build":
		fs := a.flags("plugin build")
		out := fs.String("out", "", i18n.T("flag.plugin.out"))
		check := fs.Bool("check", false, i18n.T("flag.plugin.check"))
		market := fs.String("marketplace", "", i18n.T("flag.plugin.marketplace"))
		var vars multiFlag
		fs.Var(&vars, "var", i18n.T("flag.plugin.var"))
		pos, err := parse(fs, args[1:])
		if err != nil || len(pos) != 1 {
			return errUsage
		}
		src, _ := filepath.Abs(pos[0])
		if *out == "" {
			*out = filepath.Dir(src)
		}
		vm := map[string]string{}
		for _, v := range vars {
			k, val, ok := strings.Cut(v, "=")
			if !ok {
				return errUsage
			}
			vm[k] = val
		}
		res, err := pluginbuild.Build(pluginbuild.Options{Source: src, Out: *out, Marketplace: *market, Vars: vm, Check: *check})
		if err != nil {
			return err
		}
		if *check {
			for _, d := range res.Drift {
				fmt.Fprintln(a.stdout, d)
			}
			if len(res.Drift) > 0 {
				return i18n.Errorf("cli.plugin.err.drift", len(res.Drift))
			}
			fmt.Fprintln(a.stdout, i18n.T("cli.plugin.same"))
			return nil
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.plugin.built", *out, len(res.Written), len(res.Removed)))
		return nil
	case "install":
		pos, err := parse(a.flags("plugin install"), args[1:])
		if err != nil || len(pos) != 1 {
			return errUsage
		}
		dir, _ := filepath.Abs(pos[0])
		return a.installMarketplace(install.FindHosts(), dir)
	}
	return errUsage
}

type marketplaceFile struct {
	Name    string `json:"name"`
	Plugins []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"plugins"`
}

// installMarketplace registers every plugin of a built marketplace directory in
// both hosts and copies their Codex roles.
func (a *app) installMarketplace(h install.Hosts, dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return i18n.Errorf("cli.plugin.err.not_marketplace", dir)
	}
	var m marketplaceFile
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	for _, p := range m.Plugins {
		if err := h.RegisterPlugin(dir, p.Name, m.Name); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.plugin.registered", p.Name, m.Name, hostList(h)))
		if h.Codex != "" {
			res, err := install.CopyCodexAgents(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p.Source, "./"))), codexHome())
			if err != nil {
				return err
			}
			if len(res.Copied)+len(res.Kept)+len(res.Removed) > 0 {
				fmt.Fprintln(a.stdout, i18n.T("cli.plugin.codex_roles", len(res.Copied), len(res.Kept), len(res.Removed)))
			}
			for _, k := range res.Kept {
				fmt.Fprintln(a.stdout, "  "+i18n.T("cli.plugin.codex_kept", k))
			}
		}
	}
	return nil
}

func codexHome() string {
	return transcript.DefaultRoots().Codex
}

func hostList(h install.Hosts) string {
	var hs []string
	if h.Claude != "" {
		hs = append(hs, "Claude Code")
	}
	if h.Codex != "" {
		hs = append(hs, "Codex")
	}
	if len(hs) == 0 {
		return i18n.T("cli.install.no_hosts")
	}
	return strings.Join(hs, "·")
}

func cmdInstall(a *app, args []string) error {
	fs := a.flags("install")
	binDir := fs.String("bin-dir", install.DefaultBinDir(), i18n.T("flag.install.bin_dir"))
	retroHost := fs.String("retro-host", "", i18n.T("flag.install.retro_host"))
	retroModel := fs.String("retro-model", "", i18n.T("flag.install.retro_model"))
	pluginSource := fs.String("plugin-source", "", i18n.T("flag.install.plugin_source"))
	var targets multiFlag
	fs.Var(&targets, "apply-target", i18n.T("flag.install.apply_target"))
	noSchedule := fs.Bool("no-schedule", false, i18n.T("flag.install.no_schedule"))
	noHosts := fs.Bool("no-hosts", false, i18n.T("flag.install.no_hosts"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.install.dry_run"))
	enablePlan := fs.Bool("enable-codex-plan", false, i18n.T("flag.install.enable_codex_plan"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p := paths()
	h := install.FindHosts()
	dst := filepath.Join(*binDir, install.BinaryName())
	steps := []string{
		i18n.T("cli.install.step_copy", selfPath(), dst),
		i18n.T("cli.install.step_alias", install.AliasPath(dst)), // 4차 동안의 별칭(ONID-107)
		i18n.T("cli.install.step_home", p.Home),
		i18n.T("cli.install.step_build", p.Marketplace()),
	}
	if *pluginSource != "" {
		steps = append(steps, i18n.T("cli.install.step_source", *pluginSource))
	}
	if !*noHosts {
		steps = append(steps, i18n.T("cli.install.step_hosts", hostList(h)))
	}
	if !*noSchedule {
		steps = append(steps, i18n.T("cli.install.step_schedule", config.Default().RetroHour))
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
	// 4차 동안의 별칭(ONID-107): 두지 못해도 orb는 깔렸으니 알리고 이어 간다.
	if alias, changed, err := install.InstallAlias(dst); err != nil {
		fmt.Fprintln(a.stderr, i18n.T("cli.install.alias_failed", err))
	} else if changed {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.alias", alias))
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
	src := cfg.PluginSource
	if src == "" {
		tmp, err := os.MkdirTemp("", "orbit-plugin-src")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := orbitplugin.Write(tmp); err != nil {
			return err
		}
		src = tmp
	}
	if _, err := pluginbuild.Build(pluginbuild.Options{Source: src, Out: p.Marketplace(), Vars: orbitplugin.Vars(dst)}); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.install.plugin_built", p.Marketplace(), map[bool]string{true: i18n.T("cli.install.builtin"), false: src}[cfg.PluginSource == ""]))
	if !*noHosts {
		if err := a.installMarketplace(h, p.Marketplace()); err != nil {
			return err
		}
	}
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
		where, err := install.ApplySchedule(install.Schedule{Orb: dst, Hour: cfg.RetroHour, Home: home, PathDirs: dirs, Logs: p.Logs()}, install.RunCommand)
		if err != nil {
			return i18n.Errorf("cli.install.err.schedule", err)
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.install.scheduled", cfg.RetroHour, where))
		fmt.Fprintln(a.stdout, i18n.T("cli.install.scheduled_note"))
	}
	if h.Codex != "" && !*noHosts {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.codex_hooks"))
		switch {
		case install.CodexPlanEnabled(codexHome()):
		case *enablePlan:
			if err := install.EnableCodexPlan(codexHome()); err != nil {
				return i18n.Errorf("cli.install.err.codex_plan", err)
			}
			fmt.Fprintln(a.stdout, i18n.T("cli.install.codex_plan_on", filepath.Join(codexHome(), "config.toml")))
		default:
			fmt.Fprintln(a.stdout, i18n.T("cli.install.codex_plan_off"))
		}
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.install.done"))
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

func cmdUninstall(a *app, args []string) error {
	if _, err := parse(a.flags("uninstall"), args); err != nil {
		return err
	}
	h := install.FindHosts()
	failed := false
	if err := h.UnregisterPlugin("orbit", "orbit"); err != nil {
		failed = true
		fmt.Fprintln(a.stderr, i18n.T("cli.uninstall.plugin_error", err))
	}
	if err := install.RemoveSchedule(install.RunCommand); err != nil {
		failed = true
		fmt.Fprintln(a.stderr, i18n.T("cli.uninstall.schedule_error", err))
	}
	if failed {
		return i18n.Errorf("cli.uninstall.err.partial", paths().Home)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.done", paths().Home))
	fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.binary", selfPath()))
	if alias := install.AliasPath(selfPath()); lexists(alias) { // 4차 동안의 별칭(ONID-107)
		fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.alias", alias))
	}
	return nil
}

type doctorReport struct {
	Version      string              `json:"version"`
	Binary       string              `json:"binary"`
	Home         string              `json:"home"`
	Config       config.Config       `json:"config"`
	Memories     map[string]int      `json:"memories"`
	Pending      int                 `json:"pendingProposals"`
	SessionQueue int                 `json:"sessionQueue"`
	Hosts        map[string]string   `json:"hosts"`
	Plugin       map[string]bool     `json:"pluginRegistered"`
	Schedule     bool                `json:"schedule"`
	LastRetros   []retro.RetroStatus `json:"lastRetros"`
	LastBackup   string              `json:"lastBackup,omitempty"`
	CodexPlan    bool                `json:"codexPlanTool"`
	Work         workStatus          `json:"work"`
}

type workStatus struct {
	On      bool `json:"on"`
	Open    int  `json:"openWorks"`
	Total   int  `json:"works"`
	Spooled int  `json:"spooled"` // 아직 DB에 넣지 못한 훅 사건
}

func workReport(cfg config.Config) workStatus {
	st := workStatus{On: cfg.WorkOn()}
	if files, err := filepath.Glob(filepath.Join(paths().WorkSpool(), "*.json")); err == nil {
		st.Spooled = len(files)
	}
	if _, err := os.Stat(paths().WorkDB()); err != nil {
		return st
	}
	if s, err := work.Open(paths().WorkDB()); err == nil {
		st.Open, st.Total = s.Count()
		s.Close()
	}
	return st
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
	rep := doctorReport{Version: version.Version, Binary: selfPath(), Home: p.Home, Config: cfg, Memories: map[string]int{}, Hosts: map[string]string{}, Plugin: map[string]bool{}}
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
	h := install.FindHosts()
	for name, path := range map[string]string{"claude": h.Claude, "codex": h.Codex} {
		if path == "" {
			continue
		}
		rep.Hosts[name] = path
		out, _ := exec.Command(path, "plugin", "list").CombinedOutput()
		rep.Plugin[name] = strings.Contains(string(out), "orbit@orbit")
	}
	rep.Schedule = install.ScheduleInstalled(install.RunCommand)
	rep.CodexPlan = h.Codex != "" && install.CodexPlanEnabled(codexHome())
	rep.Work = workReport(cfg)
	if entries, err := os.ReadDir(p.Backups()); err == nil && len(entries) > 0 {
		rep.LastBackup = filepath.Join(p.Backups(), entries[len(entries)-1].Name())
	}
	if *asJSON {
		return a.json(rep)
	}
	yes := func(b bool) string {
		if b {
			return i18n.T("cli.doctor.yes")
		}
		return i18n.T("cli.doctor.no")
	}
	onOff := func(b bool) string {
		if b {
			return i18n.T("cli.doctor.on")
		}
		return i18n.T("cli.doctor.off")
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.head", rep.Version, rep.Binary, runtime.GOOS, runtime.GOARCH, rep.Home))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.language", i18n.Current()))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.memories", rep.Memories["confirmed"], rep.Memories["candidate"], rep.Memories["stale"], rep.Memories["hidden"], rep.Pending))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.retro", cfg.RetroHost, cfg.RetroModel, cfg.RetroHour, cfg.Inject, yes(rep.Schedule)))
	sessionRetro := i18n.T("cli.doctor.session_retro_off")
	if cfg.SessionRetro {
		sessionRetro = i18n.T("cli.doctor.on_model", cfg.SessionModel())
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.session_retro", sessionRetro, rep.SessionQueue, cfg.RecallChars))
	for _, name := range []string{"claude", "codex"} {
		if path, ok := rep.Hosts[name]; ok {
			fmt.Fprintln(a.stdout, i18n.T("cli.doctor.host", name, path, yes(rep.Plugin[name])))
			if name == "codex" {
				if rep.CodexPlan {
					fmt.Fprintln(a.stdout, i18n.T("cli.doctor.codex_plan_on"))
				} else {
					fmt.Fprintln(a.stdout, i18n.T("cli.doctor.codex_plan_off"))
				}
			}
		} else {
			fmt.Fprintln(a.stdout, i18n.T("cli.doctor.host_missing", name))
		}
	}
	for _, r := range rep.LastRetros {
		fmt.Fprintln(a.stdout, i18n.T("cli.doctor.last_retro", r.Period, r.Kind, r.Status, oneLine(r.Summary+" "+r.Error, 80)))
	}
	if rep.LastBackup != "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.doctor.last_backup", rep.LastBackup))
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.work", onOff(rep.Work.On), rep.Work.Open, rep.Work.Total, rep.Work.Spooled))
	return nil
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
