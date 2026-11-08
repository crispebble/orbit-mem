package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/install"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/memplugin"
	"github.com/crispebble/orbit-mem/internal/migrate"
	"github.com/crispebble/orbit-mem/internal/transcript"
	"github.com/crispebble/orbit-mem/internal/update"
	"github.com/crispebble/orbit-mem/internal/version"
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

func cmdMigrate(a *app, args []string) error {
	fs := a.flags("migrate")
	from := fs.String("from", "", i18n.T("flag.migrate.from"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.migrate.dry_run"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *from == "" {
		if *dry {
			return errUsage
		}
		return migrateSchema(a, *asJSON)
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
	fmt.Fprintln(a.stdout, i18n.T("cli.migrate.sources", rep.Sources, rep.FilesSame, rep.FilesChanged, rep.FilesUnknown, rep.ToJudge, rep.Absorbed))
	return nil
}

// migrateSchema is orbit-mem migrate without --from: the schema migration doctor points to
// (copy first, then migrate; a DB a newer orbit-mem made is left alone and is an error).
func migrateSchema(a *app, asJSON bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	info, err := s.Schema()
	if err != nil {
		return err
	}
	if asJSON {
		return a.json(info)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.migrate.schema_done", info.DB, info.Binary))
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

// cmdInstall puts orbit-mem in the bin folder, writes the settings, makes the memory DB
// and registers orbit-mem's plugin in the hosts: Claude Code
// from the GitHub marketplace (or, with --local-plugin, the local folder), Codex always from
// the local folder it builds.
func cmdInstall(a *app, args []string) (err error) {
	fs := a.flags("install")
	binDir := fs.String("bin-dir", install.DefaultBinDir(), i18n.T("flag.install.bin_dir"))
	localPlugin := fs.Bool("local-plugin", false, i18n.T("flag.install.local_plugin"))
	noHosts := fs.Bool("no-hosts", false, i18n.T("flag.install.no_hosts"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.install.dry_run"))
	asJSON := fs.Bool("json", false, i18n.T("flag.install.json"))
	res := installResult{AskUser: []askUser{}, Warnings: []hostWarning{}, Steps: []string{}}
	if _, perr := parse(fs, args); perr != nil {
		if !wantsJSON(args) || errors.Is(perr, flag.ErrHelp) {
			return perr
		}
		res.Error = perr.Error()
		_ = a.json(res)
		return exitCode(2)
	}
	res.DryRun, res.Home = *dry, paths().Home
	asked := false
	// ask fills the questions for the user (설계 · orbit-mem 2판 17.4) and the files it could not
	// read. It never turns anything off: the user decides.
	ask := func() {
		if asked {
			return
		}
		asked = true
		cfg, cerr := loadConfig()
		if cerr != nil {
			res.Warnings = append(res.Warnings, hostWarning{Path: paths().Config(), Reason: cerr.Error()})
			cfg = config.Default()
		}
		ov, warns := overlapsToAsk(cfg, "")
		res.Warnings = append(res.Warnings, warns...)
		res.AskUser = askUsers(ov)
	}
	if *asJSON {
		// 사람이 읽는 진행 줄은 stderr로 보내고, stdout에는 성공이든 실패든 결과 JSON 하나만 낸다.
		out := a.stdout
		a.stdout = a.stderr
		defer func() {
			a.stdout = out
			ask()
			res.OK = err == nil
			if err != nil {
				res.Error = err.Error()
				fmt.Fprintln(a.stderr, "orbit-mem install: "+err.Error())
				err = exitCode(1)
			}
			if jerr := a.json(res); jerr != nil && err == nil {
				err = jerr
			}
		}()
	}
	var steps []string
	finish := func(dst string) error {
		res.Binary, res.Steps = dst, steps
		ask()
		if !*asJSON {
			a.printAskUser(res.AskUser)
			for _, w := range res.Warnings {
				fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.warning", w.Path, w.Reason))
			}
		}
		return nil
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
	steps = []string{
		i18n.T("cli.install.step_copy", selfPath(), dst),
		i18n.T("cli.install.step_home", p.Home),
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
	res.Binary, res.Steps = dst, steps // 실패해도 JSON에 어디까지 하려 했는지 남는다
	if *dry {
		for _, s := range steps {
			fmt.Fprintln(a.stdout, "- "+s)
		}
		return finish(dst)
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
	if err := config.Save(p.Config(), cfg); err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	s.Close()
	if *noHosts {
		fmt.Fprintln(a.stdout, i18n.T("cli.install.hosts_skipped", memplugin.GitHubRepo))
		fmt.Fprintln(a.stdout, i18n.T("cli.install.done"))
		return finish(dst)
	}
	if err := a.registerHosts(h, needLocal, dir, claudeSource); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.install.done"))
	return finish(dst)
}

// installResult is what install --json prints (설계 · orbit-mem 2판 17.4), success or not: the
// steps, the questions the agent puts to the user as they are, and the files it could not read.
type installResult struct {
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	DryRun   bool          `json:"dryRun"`
	Binary   string        `json:"binary"`
	Home     string        `json:"home"`
	Steps    []string      `json:"steps"`
	AskUser  []askUser     `json:"askUser"`
	Warnings []hostWarning `json:"warnings"`
}

// wantsJSON says whether the arguments ask for JSON, read before the flags parse
// (--json, or --json=<true value>).
func wantsJSON(args []string) bool {
	return slices.ContainsFunc(args, func(s string) bool {
		name, val, hasVal := strings.Cut(strings.TrimLeft(s, "-"), "=")
		if name != "json" || !strings.HasPrefix(s, "-") {
			return false
		}
		b, err := strconv.ParseBool(val)
		return !hasVal || (err == nil && b)
	})
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

// cmdUninstall takes orbit-mem's plugin and marketplace out of the hosts. The data and the
// local plugin folder stay.
func cmdUninstall(a *app, args []string) error {
	if _, err := parse(a.flags("uninstall"), args); err != nil {
		return err
	}
	failed := false
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

// doctorReport is orbit-mem doctor --json. orbit-code's orb doctor reads it: keep the keys.
type doctorReport struct {
	Version    string             `json:"version"`
	Binary     string             `json:"binary"`     // this orbit-mem
	PathBinary string             `json:"pathBinary"` // the orbit-mem hooks and MCP find on PATH ("" none)
	Home       string             `json:"home"`
	DB         bool               `json:"db"` // memory.db exists
	Config     config.Config      `json:"config"`
	Memories   map[string]int     `json:"memories"`
	Pending    int                `json:"pendingProposals"`
	Activity   activityStatus     `json:"activity"`
	Tidy       tidyStatus         `json:"tidy"`
	LastBackup string             `json:"lastBackup,omitempty"` // 마지막 기억 하루 사본(memory-YYYYMMDD.sqlite)
	Backups    backupStatus       `json:"backups"`
	Claude     claudeStatus       `json:"claude"`
	Codex      codexStatus        `json:"codex"`
	Update     updateStatus       `json:"update"`
	Schema     *memory.SchemaInfo `json:"schema,omitempty"` // 기억 DB의 판과 이 실행 파일의 판(DB가 없으면 빠진다)
	Problems   []problem          `json:"problems"`
}

// updateStatus is the version part of doctor: the newest version is the cached answer of the
// session-start hook's daily check (doctor itself never goes to the network).
type updateStatus struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"` // "" when no check has answered yet
	CheckedAt string `json:"checkedAt,omitempty"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"` // config updateCheck
}

// tidyStatus is the daily model-free tidy the session-start hook starts (orbit-mem tidy auto).
type tidyStatus struct {
	Auto    bool   `json:"auto"`              // config autoTidy
	LastDay string `json:"lastDay,omitempty"` // 마지막으로 정리한 날(YYYY-MM-DD)
}

type activityStatus struct {
	Sessions int `json:"sessions"` // 활동 기록(memory.db)의 세션 수
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
	rep := doctorReport{Version: version.Version, Binary: selfPath(), Home: p.Home, Config: cfg, Memories: map[string]int{}, Tidy: tidyStatus{Auto: cfg.AutoTidy}, Problems: []problem{}}
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
		s, info, err := memory.OpenReadOnly(p.DB()) // doctor는 DB를 이전하지 않고 판 문제를 보인다
		if err != nil {
			return err
		}
		defer s.Close()
		rep.Schema = &info
		switch info.State {
		case memory.SchemaNewer:
			add("binary-older-than-db", "orbit-mem update", info.DB, info.Binary)
		case memory.SchemaOlder:
			add("migration-needed", "orbit-mem migrate")
		}
		all, _ := s.List()
		for _, m := range all {
			rep.Memories[m.State]++
		}
		rep.Pending = s.PendingCount()
		if rep.Pending > 0 { // 사람 승인 대기열은 0.13.0에서 없앴다: 남은 옛 제안은 drain으로 닫는다
			add("old-proposals", "orbit-mem backup && orbit-mem proposals drain", rep.Pending)
		}
		rep.Tidy.LastDay = s.Meta(memory.MetaTidyDay)
		rep.Activity.Sessions = s.ActivityCount()
	} else {
		add("db-missing", "orbit-mem install")
	}
	rep.Update = updateStatus{Current: version.Version, Enabled: cfg.UpdateCheck}
	if c, ok := update.ReadCache(p.Home); ok {
		rep.Update.Latest, rep.Update.CheckedAt = c.Latest, c.CheckedAt.Format(time.RFC3339)
		rep.Update.Available = c.Latest != "" && update.Compare(c.Latest, version.Version) > 0
	}
	rep.Backups = readBackupStatus(p, rep.Config, time.Now())
	rep.LastBackup = rep.Backups.LastPath
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
	// 기억은 orbit-mem 하나만 쓴다(설계 · orbit-mem 2판 9절·17절). 사용자가 유지하기로 했으면(hostMemory keep) 문제로 세지 않는다.
	ov, warns := overlapsToAsk(cfg, "")
	for _, o := range ov {
		fix := o.Off
		if o.LockedBy != "" {
			fix = i18n.T("cli.doctor.fix.locked", o.LockedBy)
		}
		name := o.ID[strings.Index(o.ID, ":")+1:]
		switch {
		case o.ID == overlapClaude && o.LockedBy == "":
			add("claude-auto-memory", i18n.T("cli.doctor.fix.claude_auto_memory", o.Off, o.Settings), o.Settings)
		case o.ID == overlapClaude:
			add("claude-auto-memory", fix, o.LockedBy)
		case o.ID == overlapCodex:
			add("codex-memories", i18n.T("cli.doctor.fix.codex_memories", o.Off, o.Settings), o.Settings)
		case o.Host == "claude" && o.Kind == kindPlugin:
			add("claude-memory-plugin", fix, name)
		case o.Host == "claude":
			add("claude-memory-mcp", fix, name, o.Name)
		case o.Kind == kindPlugin:
			add("codex-memory-plugin", fix, name)
		default:
			add("codex-memory-mcp", fix, name, o.Name)
		}
	}
	for _, w := range warns {
		add("host-settings-unreadable", i18n.T("cli.doctor.fix.host_settings", w.Path), w.Path, w.Reason)
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
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.recall", cfg.RecallMode(), cfg.RecallChars, onOff(rep.Tidy.Auto), orNone(rep.Tidy.LastDay)))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.update", rep.Update.Current, orNone(rep.Update.Latest), yes(rep.Update.Available), onOff(rep.Update.Enabled)))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.activity", rep.Activity.Sessions))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.claude", orNone(rep.Claude.Path), memplugin.ID, rep.Claude.Plugin, rep.Claude.Version, orNone(rep.Claude.Marketplace)))
	fmt.Fprintln(a.stdout, i18n.T("cli.doctor.codex", orNone(rep.Codex.Path), memplugin.ID, rep.Codex.Plugin, yes(rep.Codex.Marketplace), yes(rep.Codex.HooksTrusted)))
	if b := rep.Backups; b.Last != "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.doctor.backups", timeText(b.LastAt), b.Last, b.Count, b.Keep, onOff(b.Auto)))
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
