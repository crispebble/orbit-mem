// Package retro runs the retrospectives: once a day for the day before and
// once a week, in the background, through a headless host (claude -p or codex
// exec). Orbit gathers the material; the host reads it through the retro MCP
// tools and submits a journal, candidate memories, flags and proposals once.
package retro

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/transcript"
)

// Kinds and statuses of a retrospective.
const (
	KindDaily  = "daily"
	KindWeekly = "weekly"

	StatusRunning = "running"
	StatusDone    = "done"
	StatusEmpty   = "empty" // 재료가 없어 호스트를 부르지 않음
	StatusFailed  = "failed"
	StatusSkipped = "skipped" // 이미 끝남(결과에만 쓰임)
)

// ErrBusy means another retrospective is running on this PC.
var ErrBusy = errors.New("다른 회고가 이미 돌고 있습니다")

const (
	maxCatchUpDays = 7
	runTimeout     = 30 * time.Minute
	lockStale      = 3 * time.Hour
)

// Cmd is a host process to run.
type Cmd struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

// Runner runs retrospectives for this PC.
type Runner struct {
	Store *memory.Store
	Cfg   config.Config
	Paths config.Paths
	Roots transcript.Roots
	Loc   *time.Location
	Now   func() time.Time
	Oni   string // MCP 서버로 띄울 oni 절대경로
	Exec  func(context.Context, Cmd) (string, error)
}

// Result is the outcome of one retrospective.
type Result struct {
	ID     string `json:"id,omitempty"`
	Kind   string `json:"kind"`
	Period string `json:"period"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().In(r.loc())
	}
	return time.Now().In(r.loc())
}

func (r *Runner) loc() *time.Location {
	if r.Loc != nil {
		return r.Loc
	}
	return time.Local
}

// lock takes the PC-wide retrospective lock.
func (r *Runner) lock() (func(), error) {
	if err := os.MkdirAll(r.Paths.RetroDir(), 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(r.Paths.RetroDir(), "lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d %d\n", os.Getpid(), time.Now().Unix())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		b, _ := os.ReadFile(path)
		var pid, at int64
		fmt.Sscanf(string(b), "%d %d", &pid, &at)
		if time.Since(time.Unix(at, 0)) < lockStale {
			return nil, ErrBusy
		}
		os.Remove(path) // 오래된 잠금(죽은 실행)
	}
	return nil, ErrBusy
}

func (r *Runner) existing(kind, period string) (id, status string, started int64) {
	_ = r.Store.DB().QueryRow(`SELECT id, status, started_at FROM retros WHERE kind=? AND period=? ORDER BY started_at DESC LIMIT 1`, kind, period).Scan(&id, &status, &started)
	return
}

// Run runs one retrospective unless it already finished.
func (r *Runner) Run(ctx context.Context, kind, period string) (Result, error) {
	res := Result{Kind: kind, Period: period}
	if kind != KindDaily && kind != KindWeekly {
		return res, fmt.Errorf("회고 종류는 daily 또는 weekly입니다")
	}
	if _, st, _ := r.existing(kind, period); st == StatusDone || st == StatusEmpty {
		res.Status = StatusSkipped
		return res, nil
	}
	if kind == KindDaily {
		r.proposeChangedFiles()
	}
	mat, err := r.material(kind, period)
	if err != nil {
		return res, err
	}
	nowMs := r.now().UnixMilli()
	res.ID = memory.NewID("r-")
	mat.ID = res.ID
	if mat.empty() {
		res.Status = StatusEmpty
		_, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,finished_at,summary) VALUES(?,?,?,?,?,?,?,?)`,
			res.ID, kind, period, StatusEmpty, "", nowMs, nowMs, "재료 없음")
		return res, err
	}
	dir := filepath.Join(r.Paths.RetroDir(), res.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	b, _ := json.Marshal(mat)
	if err := os.WriteFile(filepath.Join(dir, "material.json"), b, 0o600); err != nil {
		return res, err
	}
	if _, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at) VALUES(?,?,?,?,?,?)`,
		res.ID, kind, period, StatusRunning, r.Cfg.RetroHost, nowMs); err != nil {
		return res, err
	}
	runDir := filepath.Join(r.Paths.RetroDir(), "run")
	_ = os.MkdirAll(runDir, 0o700)
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	exe := r.Exec
	if exe == nil {
		exe = execHost
	}
	out, execErr := exe(ctx, r.hostCommand(res.ID, runDir, prompt(mat)))
	var status string
	_ = r.Store.DB().QueryRow(`SELECT status FROM retros WHERE id=?`, res.ID).Scan(&status)
	if status == StatusDone {
		res.Status = StatusDone
		os.RemoveAll(dir)
		return res, nil
	}
	msg := "호스트가 결과를 제출하지 않았습니다"
	if execErr != nil {
		msg += ": " + execErr.Error()
	}
	if tail := strings.TrimSpace(out); tail != "" {
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		msg += "\n" + tail
	}
	res.Status, res.Error = StatusFailed, msg
	_, _ = r.Store.DB().Exec(`UPDATE retros SET status=?, finished_at=?, error=? WHERE id=?`, StatusFailed, r.now().UnixMilli(), msg, res.ID)
	return res, errors.New(msg)
}

// CatchUp backs up the memory database, then runs every missing daily
// retrospective from the last finished day (at most a week back, only
// yesterday the first time) and last week's weekly one.
func (r *Runner) CatchUp(ctx context.Context) ([]Result, error) {
	unlock, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := r.Store.Backup(r.Paths.Backups(), max(r.Cfg.BackupKeep, 1)); err != nil {
		return nil, err
	}
	today := r.now()
	yesterday := dayOf(today.AddDate(0, 0, -1))
	start := yesterday
	var last string
	_ = r.Store.DB().QueryRow(`SELECT MAX(period) FROM retros WHERE kind='daily' AND status IN ('done','empty')`).Scan(&last)
	if last != "" {
		if t, err := time.ParseInLocation("2006-01-02", last, r.loc()); err == nil {
			start = dayOf(t.AddDate(0, 0, 1))
		}
	}
	floor := dayOf(today.AddDate(0, 0, -maxCatchUpDays))
	if start < floor {
		start = floor
	}
	var results []Result
	var errs []error
	for d := start; d <= yesterday; d = nextDay(d, r.loc()) {
		res, err := r.Run(ctx, KindDaily, d)
		results = append(results, res)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d, err))
		}
	}
	week := lastWeekStart(today)
	res, err := r.Run(ctx, KindWeekly, week)
	if res.Status != StatusSkipped {
		results = append(results, res)
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("주간 %s: %w", week, err))
	}
	if _, err := r.Store.CloseSettled(); err != nil {
		errs = append(errs, err)
	}
	return results, errors.Join(errs...)
}

func dayOf(t time.Time) string { return t.Format("2006-01-02") }

func nextDay(d string, loc *time.Location) string {
	t, _ := time.ParseInLocation("2006-01-02", d, loc)
	return dayOf(t.AddDate(0, 0, 1))
}

// lastWeekStart is the Monday of the last completed week (Monday to Sunday).
func lastWeekStart(now time.Time) string {
	offset := (int(now.Weekday()) + 6) % 7 // 월요일=0
	thisMonday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -offset)
	return dayOf(thisMonday.AddDate(0, 0, -7))
}

// proposeChangedFiles proposes cleaning up memories whose source file changed.
func (r *Runner) proposeChangedFiles() {
	rows, err := r.Store.DB().Query(`SELECT s.memory_id, s.ref, s.hash, m.title FROM sources s JOIN memories m ON m.id=s.memory_id
		WHERE s.kind='file' AND s.hash<>'' AND m.state IN ('candidate','confirmed')`)
	if err != nil {
		return
	}
	type hit struct{ id, ref, title string }
	var hits []hit
	for rows.Next() {
		var h hit
		var hash string
		if rows.Scan(&h.id, &h.ref, &hash, &h.title) == nil && filepath.IsAbs(h.ref) && memory.FileHash(h.ref) != hash {
			hits = append(hits, h)
		}
	}
	rows.Close()
	for _, h := range hits {
		_, _ = r.Store.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: "출처 파일이 바뀜: " + h.title,
			Body: "근거 파일 " + h.ref + " 이 기억을 만든 뒤 바뀌었습니다. 내용이 아직 맞는지 보고 낡음으로 정리할지 정해 주세요.", MemoryIDs: []string{h.id}})
	}
}

func (r *Runner) hostCommand(id, dir, text string) Cmd {
	env := []string{"ORBIT_HOME=" + r.Paths.Home, "ORBIT_RETRO_ID=" + id}
	if r.Cfg.RetroHost == "codex" {
		name := r.Cfg.CodexPath
		if name == "" {
			name = "codex"
		}
		q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
		args := []string{"exec", "--ephemeral", "--skip-git-repo-check", "-C", dir, "-s", "read-only",
			"-c", `approval_policy="never"`,
			"-c", "mcp_servers.orbit-retro.command=" + q(r.Oni),
			"-c", `mcp_servers.orbit-retro.args=["mcp","--retro",` + q(id) + `]`,
			"-c", "mcp_servers.orbit-retro.env={ORBIT_HOME=" + q(r.Paths.Home) + "}"}
		if r.Cfg.RetroModel != "" {
			args = append(args, "-m", r.Cfg.RetroModel)
		}
		return Cmd{Name: name, Args: append(args, text), Dir: dir, Env: env}
	}
	name := r.Cfg.ClaudePath
	if name == "" {
		name = "claude"
	}
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"orbit-retro": map[string]any{
		"type": "stdio", "command": r.Oni, "args": []string{"mcp", "--retro", id}, "env": map[string]string{"ORBIT_HOME": r.Paths.Home}}}})
	tools := []string{"retro_material", "retro_submit", "retro_read_file", "memory_find", "memory_get"}
	for i, t := range tools {
		tools[i] = "mcp__orbit-retro__" + t
	}
	args := []string{"-p", text, "--mcp-config", string(mcpConfig), "--strict-mcp-config", "--allowedTools", strings.Join(tools, ","),
		"--no-session-persistence", "--output-format", "text"}
	if r.Cfg.RetroModel != "" {
		args = append(args, "--model", r.Cfg.RetroModel)
	}
	return Cmd{Name: name, Args: args, Dir: dir, Env: env}
}

// execHost runs a host process with stdin closed and returns the tail of its output.
func execHost(ctx context.Context, c Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// RetroStatus is a stored retrospective.
type RetroStatus struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Period     string   `json:"period"`
	Status     string   `json:"status"`
	Host       string   `json:"host,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Flags      []string `json:"flags,omitempty"`
	JournalID  string   `json:"journalId,omitempty"`
	Error      string   `json:"error,omitempty"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt,omitempty"`
}

const statusCols = `id,kind,period,status,host,summary,flags,journal_id,error,started_at,finished_at`

func scanStatus(row interface{ Scan(...any) error }) (RetroStatus, error) {
	var st RetroStatus
	var flags string
	err := row.Scan(&st.ID, &st.Kind, &st.Period, &st.Status, &st.Host, &st.Summary, &flags, &st.JournalID, &st.Error, &st.StartedAt, &st.FinishedAt)
	_ = json.Unmarshal([]byte(flags), &st.Flags)
	return st, err
}

// Status reads the latest retrospective of kind and period.
func Status(s *memory.Store, kind, period string) (RetroStatus, error) {
	return scanStatus(s.DB().QueryRow(`SELECT `+statusCols+` FROM retros WHERE kind=? AND period=? ORDER BY started_at DESC LIMIT 1`, kind, period))
}

// Recent lists the latest n retrospectives, newest first.
func Recent(s *memory.Store, n int) ([]RetroStatus, error) {
	rows, err := s.DB().Query(`SELECT `+statusCols+` FROM retros ORDER BY started_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetroStatus
	for rows.Next() {
		st, err := scanStatus(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func getStatus(s *memory.Store, id string) (RetroStatus, error) {
	st, err := scanStatus(s.DB().QueryRow(`SELECT `+statusCols+` FROM retros WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return st, fmt.Errorf("회고가 없습니다: %s", id)
	}
	return st, err
}

func itoa(n int) string { return strconv.Itoa(n) }
