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

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/install"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/migrate"
	"github.com/onidot-labs/orbit/internal/orbitplugin"
	"github.com/onidot-labs/orbit/internal/pluginbuild"
	"github.com/onidot-labs/orbit/internal/retro"
	"github.com/onidot-labs/orbit/internal/transcript"
	"github.com/onidot-labs/orbit/internal/version"
	"github.com/onidot-labs/orbit/internal/work"
)

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "oni"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

func newRunner(s *memory.Store, cfg config.Config) *retro.Runner {
	return &retro.Runner{Store: s, Cfg: cfg, Paths: paths(), Roots: transcript.DefaultRoots(), Loc: time.Local, Oni: selfPath()}
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
	day := fs.String("day", "", "하루 회고 날짜")
	week := fs.String("week", "", "주간 회고 주의 월요일")
	n := fs.Int("n", 10, "개수")
	asJSON := fs.Bool("json", false, "JSON")
	if _, err := parse(fs, args[1:]); err != nil {
		return err
	}
	r := newRunner(s, cfg)
	ctx, cancel := signalContext()
	defer cancel()
	switch args[0] {
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
			fmt.Fprintf(a.stdout, "%s 회고 따라잡기 시작 (%s)\n", time.Now().Format("2006-01-02 15:04"), cfg.RetroHost)
			results, err = r.CatchUp(ctx)
			a.printResults(results)
			return err
		}
		a.printResults(results)
		return nil
	case "status":
		list, err := retro.Recent(s, *n)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(list)
		}
		for _, st := range list {
			fmt.Fprintf(a.stdout, "%s %s %-7s %s %s\n", st.Period, st.Kind, st.Status, st.ID, oneLine(st.Summary+" "+st.Error, 100))
			for _, f := range st.Flags {
				fmt.Fprintln(a.stdout, "    짚을 점: "+f)
			}
		}
		fmt.Fprintf(a.stdout, "대기 제안 %d건\n", s.PendingCount())
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
			fmt.Fprintf(a.stdout, "%s %s %s 말 %d개\n", ss.Host, ss.ID, ss.Cwd, len(ss.Turns))
		}
		fmt.Fprintf(a.stdout, "세션 %d개, 말 %d개, Codex 요약 %d개\n", len(sessions), turns, len(transcript.CodexSummaries(transcript.DefaultRoots(), *day)))
		return nil
	}
	return errUsage
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
	from := fs.String("from", "", "옛 Knowledge SQLite 사본")
	dry := fs.Bool("dry-run", false, "쓰지 않고 센다")
	asJSON := fs.Bool("json", false, "JSON")
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
			return fmt.Errorf("옮기기 전 사본을 만들지 못해 멈췄습니다: %w", err)
		}
		fmt.Fprintln(a.stderr, "옮기기 전 사본: "+p)
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
	word := "옮겼습니다"
	if *dry {
		word = "옮길 수 있습니다(미리보기)"
	}
	fmt.Fprintf(a.stdout, "옛 기록 %d건 중 %d건을 %s. 이미 옮긴 것 %d건.\n", rep.Records, rep.Imported, word, rep.Skipped)
	fmt.Fprintf(a.stdout, "상태: %s\n종류: %s\n", counts(rep.ByState), counts(rep.ByKind))
	fmt.Fprintf(a.stdout, "출처 %d건. 파일 근거: 그대로 %d · 바뀜 %d · 판단 못함 %d. 정리 제안 %d건. 흡수 표시 %d건.\n",
		rep.Sources, rep.FilesSame, rep.FilesChanged, rep.FilesUnknown, rep.Proposals, rep.Absorbed)
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
		out := fs.String("out", "", "출력 디렉터리(기본: 원본의 상위)")
		check := fs.Bool("check", false, "쓰지 않고 어긋남만 본다")
		market := fs.String("marketplace", "", "마켓 이름(기본: 플러그인 이름)")
		var vars multiFlag
		fs.Var(&vars, "var", "치환 변수 k=v(여러 번)")
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
				return fmt.Errorf("생성물이 원본과 어긋납니다(%d건). oni plugin build로 다시 만드세요", len(res.Drift))
			}
			fmt.Fprintln(a.stdout, "생성물이 원본과 같습니다.")
			return nil
		}
		fmt.Fprintf(a.stdout, "만들었습니다: %s (바뀐 파일 %d, 지운 파일 %d)\n", *out, len(res.Written), len(res.Removed))
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
		return fmt.Errorf("빌드한 마켓 디렉터리가 아닙니다(.claude-plugin/marketplace.json 없음): %s", dir)
	}
	var m marketplaceFile
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	for _, p := range m.Plugins {
		if err := h.RegisterPlugin(dir, p.Name, m.Name); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "플러그인 %s@%s 를 등록했습니다(%s).\n", p.Name, m.Name, hostList(h))
		if h.Codex != "" {
			res, err := install.CopyCodexAgents(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p.Source, "./"))), codexHome())
			if err != nil {
				return err
			}
			if len(res.Copied)+len(res.Kept)+len(res.Removed) > 0 {
				fmt.Fprintf(a.stdout, "Codex 역할: 복사 %d · 그대로 둠 %d · 지움 %d\n", len(res.Copied), len(res.Kept), len(res.Removed))
			}
			for _, k := range res.Kept {
				fmt.Fprintln(a.stdout, "  사용자 파일이라 덮어쓰지 않음: "+k)
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
		return "호스트 없음"
	}
	return strings.Join(hs, "·")
}

func cmdInstall(a *app, args []string) error {
	fs := a.flags("install")
	binDir := fs.String("bin-dir", install.DefaultBinDir(), "oni를 둘 디렉터리")
	retroHost := fs.String("retro-host", "", "회고를 돌릴 호스트(claude|codex)")
	retroModel := fs.String("retro-model", "", "회고 모델(비우면 호스트 기본)")
	pluginSource := fs.String("plugin-source", "", "Orbit 플러그인 원본 디렉터리(비우면 바이너리 내장 원본, 한 번 주면 기억함)")
	var targets multiFlag
	fs.Var(&targets, "apply-target", "회고가 반영 제안을 낼 수 있는 파일·디렉터리(여러 번)")
	noSchedule := fs.Bool("no-schedule", false, "회고 예약을 등록하지 않음")
	noHosts := fs.Bool("no-hosts", false, "호스트 플러그인 등록을 건너뜀")
	dry := fs.Bool("dry-run", false, "무엇을 할지만 보여 줌")
	enablePlan := fs.Bool("enable-codex-plan", false, "Codex 계획 도구(update_plan)를 ~/.codex/config.toml에 켠다(고치기 전 사본을 남김)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p := paths()
	h := install.FindHosts()
	dst := filepath.Join(*binDir, install.BinaryName())
	steps := []string{
		"oni 복사: " + selfPath() + " → " + dst,
		"설정·기억 DB: " + p.Home,
		"Orbit 플러그인 빌드: " + p.Marketplace(),
	}
	if *pluginSource != "" {
		steps = append(steps, "플러그인 원본: "+*pluginSource)
	}
	if !*noHosts {
		steps = append(steps, "호스트 등록(orbit@orbit, Codex 역할 복사): "+hostList(h))
	}
	if !*noSchedule {
		steps = append(steps, fmt.Sprintf("회고 예약: 매일 %02d시와 로그인 때", config.Default().RetroHour))
	}
	if *dry {
		for _, s := range steps {
			fmt.Fprintln(a.stdout, "- "+s)
		}
		return nil
	}
	copied, err := install.CopyBinary(selfPath(), dst)
	if err != nil {
		return fmt.Errorf("oni를 복사하지 못했습니다: %w", err)
	}
	if copied {
		fmt.Fprintln(a.stdout, "oni를 설치했습니다: "+dst)
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
			return fmt.Errorf("플러그인 원본이 아닙니다(plugin.json 없음): %s", abs)
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
	fmt.Fprintf(a.stdout, "Orbit 플러그인을 만들었습니다: %s (원본 %s)\n", p.Marketplace(), map[bool]string{true: "내장", false: src}[cfg.PluginSource == ""])
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
		where, err := install.ApplySchedule(install.Schedule{Oni: dst, Hour: cfg.RetroHour, Home: home, PathDirs: dirs, Logs: p.Logs()}, install.RunCommand)
		if err != nil {
			return fmt.Errorf("회고 예약을 등록하지 못했습니다: %w", err)
		}
		fmt.Fprintf(a.stdout, "회고 예약을 등록했습니다(매일 %02d시, 로그인 때 밀린 회고): %s\n", cfg.RetroHour, where)
		fmt.Fprintln(a.stdout, "등록하면 밀린 회고를 곧바로 한 번 돕니다(처음이면 어제 하루, 호스트 사용량을 씁니다). 결과: oni retro status")
	}
	if h.Codex != "" && !*noHosts {
		fmt.Fprintln(a.stdout, "Codex는 플러그인 훅을 신뢰해야 돕니다. Codex에서 /hooks 로 Orbit 훅(기억 주입과 작업 기록)을 검토·신뢰해 주세요. 훅 파일이 바뀌면 다시 신뢰해야 합니다.")
		switch {
		case install.CodexPlanEnabled(codexHome()):
		case *enablePlan:
			if err := install.EnableCodexPlan(codexHome()); err != nil {
				return fmt.Errorf("Codex 계획 도구를 켜지 못했습니다: %w", err)
			}
			fmt.Fprintln(a.stdout, "Codex 계획 도구(update_plan)를 켰습니다: "+filepath.Join(codexHome(), "config.toml"))
		default:
			fmt.Fprintln(a.stdout, "Codex 계획 도구(update_plan)가 꺼져 있어 Codex 작업은 작업 그래프에 남지 않습니다. 켜려면: oni install --enable-codex-plan (또는 config.toml에 [tools.update_plan] enabled = true)")
		}
	}
	fmt.Fprintln(a.stdout, "새 세션부터 기억이 붙습니다. 상태 확인: oni doctor")
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
		fmt.Fprintln(a.stderr, "플러그인 등록 해제 중 오류: "+err.Error())
	}
	if err := install.RemoveSchedule(install.RunCommand); err != nil {
		failed = true
		fmt.Fprintln(a.stderr, "예약 해제 중 오류: "+err.Error())
	}
	if failed {
		return fmt.Errorf("일부를 지우지 못했습니다. 위 오류를 확인해 주세요. 기억 DB는 그대로입니다: %s", paths().Home)
	}
	fmt.Fprintf(a.stdout, "Orbit 플러그인과 회고 예약을 지웠습니다. 기억 DB와 백업은 남겼습니다: %s\n", paths().Home)
	fmt.Fprintf(a.stdout, "oni 실행 파일은 직접 지워 주세요: %s\n", selfPath())
	return nil
}

type doctorReport struct {
	Version    string              `json:"version"`
	Binary     string              `json:"binary"`
	Home       string              `json:"home"`
	Config     config.Config       `json:"config"`
	Memories   map[string]int      `json:"memories"`
	Pending    int                 `json:"pendingProposals"`
	Hosts      map[string]string   `json:"hosts"`
	Plugin     map[string]bool     `json:"pluginRegistered"`
	Schedule   bool                `json:"schedule"`
	LastRetros []retro.RetroStatus `json:"lastRetros"`
	LastBackup string              `json:"lastBackup,omitempty"`
	CodexPlan  bool                `json:"codexPlanTool"`
	Work       workStatus          `json:"work"`
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
	asJSON := fs.Bool("json", false, "JSON")
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
			return "있음"
		}
		return "없음"
	}
	fmt.Fprintf(a.stdout, "oni %s (%s, %s/%s)\n데이터: %s\n", rep.Version, rep.Binary, runtime.GOOS, runtime.GOARCH, rep.Home)
	fmt.Fprintf(a.stdout, "기억: 확정 %d · 후보 %d · 낡음 %d · 숨김 %d · 대기 제안 %d\n", rep.Memories["confirmed"], rep.Memories["candidate"], rep.Memories["stale"], rep.Memories["hidden"], rep.Pending)
	fmt.Fprintf(a.stdout, "회고: %s %s, 매일 %02d시, 주입 %s · 예약 %s\n", cfg.RetroHost, cfg.RetroModel, cfg.RetroHour, cfg.Inject, yes(rep.Schedule))
	for _, name := range []string{"claude", "codex"} {
		if path, ok := rep.Hosts[name]; ok {
			fmt.Fprintf(a.stdout, "%s: %s · Orbit 플러그인 %s\n", name, path, yes(rep.Plugin[name]))
			if name == "codex" {
				if rep.CodexPlan {
					fmt.Fprintln(a.stdout, "codex 계획 도구(update_plan): 켜짐 · 작업 훅은 /hooks에서 신뢰해야 돕니다")
				} else {
					fmt.Fprintln(a.stdout, "codex 계획 도구(update_plan): 꺼짐 · 켜려면 oni install --enable-codex-plan")
				}
			}
		} else {
			fmt.Fprintf(a.stdout, "%s: 없음\n", name)
		}
	}
	for _, r := range rep.LastRetros {
		fmt.Fprintf(a.stdout, "최근 회고: %s %s %s %s\n", r.Period, r.Kind, r.Status, oneLine(r.Summary+" "+r.Error, 80))
	}
	if rep.LastBackup != "" {
		fmt.Fprintln(a.stdout, "최근 백업: "+rep.LastBackup)
	}
	onOff := map[bool]string{true: "켜짐", false: "꺼짐"}[rep.Work.On]
	fmt.Fprintf(a.stdout, "작업 기록: %s · 열린 작업 %d · 전체 %d · 스풀 %d\n", onOff, rep.Work.Open, rep.Work.Total, rep.Work.Spooled)
	return nil
}
