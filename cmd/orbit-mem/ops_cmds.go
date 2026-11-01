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

func cmdPlugin(a *app, args []string) error {
	if len(args) == 0 || args[0] != "fragment" {
		return errUsage // 플러그인 빌드·등록은 OpenOrbit orb plugin build·orb install이 한다
	}
	// 기억 MCP·훅 조각: OpenOrbit의 플러그인 빌드(orb plugin build … --fragment)가 받는다
	fs := a.flags("plugin fragment")
	path := fs.String("path", "", i18n.T("flag.plugin.path"))
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return errUsage
	}
	if *path == "" {
		*path = selfPath()
	}
	b, err := memplugin.Fragment(*path)
	if err != nil {
		return err
	}
	_, err = a.stdout.Write(b)
	return err
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// cmdInstall puts orbit-mem in the bin folder, writes the settings (hosts, plugin source,
// apply targets), makes the memory DB and schedules the retrospectives. Building the OpenOrbit
// plugin and registering it in the hosts is OpenOrbit's orb install.
func cmdInstall(a *app, args []string) error {
	fs := a.flags("install")
	binDir := fs.String("bin-dir", install.DefaultBinDir(), i18n.T("flag.install.bin_dir"))
	retroHost := fs.String("retro-host", "", i18n.T("flag.install.retro_host"))
	retroModel := fs.String("retro-model", "", i18n.T("flag.install.retro_model"))
	pluginSource := fs.String("plugin-source", "", i18n.T("flag.install.plugin_source"))
	var targets multiFlag
	fs.Var(&targets, "apply-target", i18n.T("flag.install.apply_target"))
	noSchedule := fs.Bool("no-schedule", false, i18n.T("flag.install.no_schedule"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.install.dry_run"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p := paths()
	h := install.FindHosts()
	dst := filepath.Join(*binDir, install.BinaryName())
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
	if *dry {
		for _, s := range steps {
			fmt.Fprintln(a.stdout, "- "+s)
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.install.plugin_by_orb"))
		return nil
	}
	copied, err := install.CopyBinary(selfPath(), dst)
	if err != nil {
		return i18n.Errorf("cli.install.err.copy", err)
	}
	if copied {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.installed", dst))
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
	fmt.Fprintln(a.stdout, i18n.T("cli.install.plugin_by_orb"))
	fmt.Fprintln(a.stdout, i18n.T("cli.install.done"))
	return nil
}

// cmdUninstall takes the retrospective schedule out. The plugin is OpenOrbit's (orb uninstall).
func cmdUninstall(a *app, args []string) error {
	if _, err := parse(a.flags("uninstall"), args); err != nil {
		return err
	}
	if err := install.RemoveSchedule(install.RunCommand); err != nil {
		fmt.Fprintln(a.stderr, i18n.T("cli.uninstall.schedule_error", err))
		return i18n.Errorf("cli.uninstall.err.partial", paths().Home)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.done", paths().Home))
	fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.plugin_by_orb"))
	fmt.Fprintln(a.stdout, i18n.T("cli.uninstall.binary", selfPath()))
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
	Activity     activityStatus      `json:"activity"`
}

type activityStatus struct {
	Sessions       int `json:"sessions"`       // 활동 기록(memory.db)의 세션 수
	LegacySessions int `json:"legacySessions"` // 옛 작업 기록(work.db, 읽기 전용)의 세션 수
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
	rep.Activity.Sessions = s.ActivityCount()
	rep.Activity.LegacySessions, _ = activity.LegacyCount(p.WorkDB())
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
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.head", rep.Version, rep.Binary, runtime.GOOS, runtime.GOARCH, rep.Home))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.language", i18n.Current()))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.memories", rep.Memories["confirmed"], rep.Memories["candidate"], rep.Memories["stale"], rep.Memories["hidden"], rep.Pending))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.retro", cfg.RetroHost, cfg.RetroModel, cfg.RetroHour, cfg.Inject, yes(rep.Schedule)))
	sessionRetro := i18n.T("cli.doctor.session_retro_off")
	if cfg.SessionRetro {
		sessionRetro = i18n.T("cli.doctor.on_model", cfg.SessionModel())
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.session_retro", sessionRetro, rep.SessionQueue, cfg.RecallChars))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.activity", rep.Activity.Sessions, rep.Activity.LegacySessions))
	for _, name := range []string{"claude", "codex"} {
		if path, ok := rep.Hosts[name]; ok {
			fmt.Fprintln(a.stdout, i18n.T("cli.doctor.host", name, path, yes(rep.Plugin[name])))
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
	return nil
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
