package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/backup"
	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 백업(ONID-136): 세션 시작 훅이 하루 한 번 뒤에서 사본을 만들고, 사람은 orbit-mem backup 하나로
// 만들기·목록·상태·복구를 한다. 스키마 이전(memory.Open)·복구·들여오기·옮기기 전 사본은 규칙과 관계없이 만든다.

// manualWait is how long a person's backup or restore waits for one already running.
const manualWait = time.Minute

func cmdBackup(a *app, args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "":
		return backupNow(a, args)
	case "list":
		return backupList(a, args)
	case "status":
		return backupStatusCmd(a, args)
	case "restore":
		return backupRestore(a, args)
	case "auto":
		return backupAuto(a, args)
	}
	return errUsage
}

// backupNow copies the memory DB now, whatever the time of the last copy.
func backupNow(a *app, args []string) error {
	fs := a.flags("backup")
	if pos, err := parse(fs, args); err != nil || len(pos) != 0 {
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
	r, err := s.DailyBackup(memory.BackupOptions{Dir: paths().Backups(), Keep: cfg.BackupKeep, Force: true, Wait: manualWait})
	if err != nil {
		return err
	}
	if r.Skipped == memory.SkipBusy {
		return i18n.Errorf("cli.backup.busy")
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.backup.done", r.Memory))
	return nil
}

// backupList prints the copies in the backups folder, newest first.
func backupList(a *app, args []string) error {
	fs := a.flags("backup")
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if pos, err := parse(fs, args); err != nil || len(pos) != 0 {
		return errUsage
	}
	dir := paths().Backups()
	list, err := backup.List(dir)
	if err != nil {
		return err
	}
	if a.wantJSON(*asJSON) {
		return a.json(map[string]any{"dir": dir, "copies": list})
	}
	fmt.Fprintln(a.stdout, "# "+dir)
	if len(list) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.backup.none"))
	}
	for _, c := range list {
		fmt.Fprintf(a.stdout, "%s  %-8s  %s  %s\n", c.At.Local().Format("2006-01-02 15:04"), kindText(c.Kind), sizeText(c.Size), c.Name)
	}
	return nil
}

// backupStatus is the state of the copies: the doctor and backup status show it.
type backupStatus struct {
	Dir        string `json:"dir"`
	Auto       bool   `json:"auto"`
	Keep       int    `json:"keep"`
	Count      int    `json:"count"`              // 기억 하루 사본 수
	Last       string `json:"last,omitempty"`     // 마지막 기억 하루 사본 이름
	LastPath   string `json:"lastPath,omitempty"` // 그 경로
	LastAt     string `json:"lastAt,omitempty"`   // 그 시각(RFC 3339)
	Due        bool   `json:"due"`                // 다음 세션 시작이 사본을 만든다(자동이 켜져 있으면)
	NextAfter  string `json:"nextAfter,omitempty"`
	WorkCopies int    `json:"workCopies"`
	Snapshots  int    `json:"snapshots"` // 스키마 이전·복구·들여오기·옮기기 전 사본
}

func readBackupStatus(p config.Paths, cfg config.Config, now time.Time) backupStatus {
	st := backupStatus{Dir: p.Backups(), Auto: cfg.AutoBackup, Keep: cfg.BackupKeep, Due: backup.Due(p.Backups(), now)}
	list, _ := backup.List(p.Backups())
	for _, c := range list {
		switch c.Kind {
		case backup.KindMemory:
			st.Count++
		case backup.KindWork:
			st.WorkCopies++
		case backup.KindSnapshot:
			st.Snapshots++
		}
	}
	if last, ok := backup.Last(p.Backups(), backup.PrefixMemory); ok {
		st.Last, st.LastPath, st.LastAt = last.Name, last.Path, last.At.Format(time.RFC3339)
		st.NextAfter = last.At.Add(backup.Interval).Format(time.RFC3339)
	}
	return st
}

func backupStatusCmd(a *app, args []string) error {
	fs := a.flags("backup")
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if pos, err := parse(fs, args); err != nil || len(pos) != 0 {
		return errUsage
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	st := readBackupStatus(paths(), cfg, time.Now())
	if a.wantJSON(*asJSON) {
		return a.json(st)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.backup.status.dir", st.Dir))
	fmt.Fprintln(a.stdout, i18n.T("cli.backup.status.auto", onOff(st.Auto), st.Keep))
	if st.Last == "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.backup.status.no_last"))
	} else {
		fmt.Fprintln(a.stdout, i18n.T("cli.backup.status.last", timeText(st.LastAt), st.Last, st.Count))
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.backup.status.others", st.WorkCopies, st.Snapshots))
	switch {
	case !st.Auto:
	case st.Due:
		fmt.Fprintln(a.stdout, i18n.T("cli.backup.status.due"))
	default:
		fmt.Fprintln(a.stdout, i18n.T("cli.backup.status.next", timeText(st.NextAfter)))
	}
	return nil
}

func kindText(kind string) string {
	switch kind {
	case backup.KindMemory:
		return i18n.T("cli.backup.kind.memory")
	case backup.KindWork:
		return i18n.T("cli.backup.kind.work")
	}
	return i18n.T("cli.backup.kind.snapshot")
}

func onOff(b bool) string {
	if b {
		return i18n.T("cli.backup.on")
	}
	return i18n.T("cli.backup.off")
}

func timeText(rfc string) string {
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return rfc
	}
	return t.Local().Format("2006-01-02 15:04")
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%5.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%5.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%6dB", n)
}

// backupRestore replaces the memory DB with a copy. A person runs it: it first prints
// what it changes, and without --yes it changes nothing (on a terminal it asks).
func backupRestore(a *app, args []string) error {
	fs := a.flags("backup")
	yes := fs.Bool("yes", false, i18n.T("flag.backup.yes"))
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return errUsage
	}
	src := pos[0]
	if !strings.ContainsRune(src, filepath.Separator) && !strings.ContainsRune(src, '/') {
		src = filepath.Join(paths().Backups(), src) // 이름만 주면 백업 폴더에서 찾는다
	}
	if abs, err := filepath.Abs(src); err == nil {
		src = abs
	}
	info, err := memory.ReadCopyInfo(src)
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	now, _ := s.Count()
	fmt.Fprintln(a.stdout, i18n.T("cli.backup.restore.plan", paths().DB(), now, src, info.Memories))
	fmt.Fprintln(a.stdout, i18n.T("cli.backup.restore.keep", paths().Backups()))
	if !*yes {
		if !isTerminal(a.stdin) {
			fmt.Fprintln(a.stderr, i18n.T("cli.backup.restore.need_yes"))
			return exitCode(1)
		}
		fmt.Fprint(a.stdout, i18n.T("cli.backup.restore.ask"))
		line, _ := bufio.NewReader(a.stdin).ReadString('\n')
		if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
			fmt.Fprintln(a.stdout, i18n.T("cli.backup.restore.cancelled"))
			return nil
		}
	}
	r, err := s.Restore(src, paths().Backups())
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.backup.restore.done", src, r.Info.Memories, r.Before))
	// 복구는 이전하지 않는다: 같은 프로세스에서 다시 열어 판을 올리고(판 4 사본이면 판 5로) 색인을 다 채운다.
	s.Close()
	w, err := openStore()
	if err != nil {
		return err
	}
	defer w.Close()
	if n, err := w.FillIndex(0); err != nil {
		return err
	} else if n > 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.migrate.index_filled", n))
	}
	return nil
}

// backupAuto is the background run the session-start hook starts: the daily copy when it
// is due, under the lock (another run makes it: skip). It never fails; problems go to
// logs/backup.log.
func backupAuto(a *app, args []string) error {
	fs := a.flags("backup")
	if pos, err := parse(fs, args); err != nil || len(pos) != 0 {
		return errUsage
	}
	p := paths()
	cfg, err := loadConfig()
	if err != nil {
		backupLog(p, "config: "+err.Error())
		return nil
	}
	if !cfg.AutoBackup {
		return nil
	}
	if _, err := os.Stat(p.DB()); err != nil { // 설치 전: 기억 DB를 만들지 않는다
		return nil
	}
	if !backup.Due(p.Backups(), time.Now()) {
		return nil
	}
	s, err := openStore()
	if err != nil {
		backupLog(p, "open: "+err.Error())
		return nil
	}
	defer s.Close()
	r, err := s.DailyBackup(memory.BackupOptions{Dir: p.Backups(), Keep: cfg.BackupKeep})
	if err != nil {
		backupLog(p, "error: "+err.Error())
		return nil
	}
	if r.Made {
		line := "made " + r.Memory
		if len(r.Removed) > 0 {
			line += " removed " + strings.Join(r.Removed, ",")
		}
		backupLog(p, line)
	}
	return nil
}

func backupLog(p config.Paths, line string) {
	if err := os.MkdirAll(p.Logs(), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(p.Logs(), "backup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s backup auto: %s\n", time.Now().Format(time.RFC3339), line)
}

// autoBackup is the session-start hook's part: when the daily copy is due it starts
// `orbit-mem backup auto` as its own process and does not wait for it. Checking costs
// one folder read; nothing here can fail the hook.
func autoBackup(cfg config.Config) {
	if !cfg.AutoBackup {
		return
	}
	if !backup.Due(paths().Backups(), time.Now()) {
		return
	}
	_ = startBackground("backup", "auto")
}

// startBackground starts this orbit-mem with args, detached from the hook (own
// session, no inherited stdio), and does not wait. Tests replace it.
var startBackground = func(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
