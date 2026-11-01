// Package retro runs the retrospectives: once a day for the day before and
// once a week, in the background, through a headless host (claude -p or codex
// exec). OpenOrbit gathers the material; the host reads it through the retro MCP
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

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/sqlitedb"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
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
var ErrBusy = i18n.Err("retro.err.busy")

// ErrHostSignedOut means the headless host found no sign-in: it never
// reached the model. macOS logout is one cause (the keychain goes away while
// the session-end hooks still run), so the run is retried later.
var ErrHostSignedOut = i18n.Err("retro.err.host_signed_out")

const (
	maxCatchUpDays = 7
	runTimeout     = 30 * time.Minute
	lockStale      = 6 * time.Hour // 최악 실행(일 7회+주간 1회, 각 30분)보다 길게
	// 세션 고정 묶음·처리한 대기열을 남기는 날 수: Claude Code 대화 기록 기본 보관(cleanupPeriodDays 30일)
	sessionStateDays = 30
)

// Cmd is a host process to run.
type Cmd struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin string
}

// Runner runs retrospectives for this PC.
type Runner struct {
	Store *memory.Store
	Cfg   config.Config
	Paths config.Paths
	Roots transcript.Roots
	Loc   *time.Location
	Now   func() time.Time
	Mem   string // MCP 서버로 띄울 orbit-mem 절대경로
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

// lock takes the PC-wide retrospective lock. A lock left by a process that
// is gone, or older than lockStale, is taken over. Unlock removes only our own lock.
func (r *Runner) lock() (func(), error) {
	if err := os.MkdirAll(r.Paths.RetroDir(), 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(r.Paths.RetroDir(), "lock")
	mine := fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().UnixNano())
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.WriteString(mine)
			f.Close()
			return func() {
				if b, err := os.ReadFile(path); err == nil && string(b) == mine {
					os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		b, _ := os.ReadFile(path)
		var pid, at int64
		fmt.Sscanf(string(b), "%d %d", &pid, &at)
		if processAlive(int(pid)) && time.Since(time.Unix(0, at)) < lockStale {
			return nil, ErrBusy
		}
		os.Remove(path) // 죽은 실행이 남긴 잠금
	}
	return nil, ErrBusy
}

func (r *Runner) existing(kind, period string) (id, status string, started int64) {
	_ = r.Store.DB().QueryRow(`SELECT id, status, started_at FROM retros WHERE kind=? AND period=? ORDER BY started_at DESC LIMIT 1`, kind, period).Scan(&id, &status, &started)
	return
}

// RunOne runs one retrospective under the PC-wide lock.
func (r *Runner) RunOne(ctx context.Context, kind, period string) (Result, error) {
	unlock, err := r.lock()
	if err != nil {
		return Result{Kind: kind, Period: period}, err
	}
	defer unlock()
	return r.Run(ctx, kind, period)
}

// Run runs one retrospective unless it already finished. Callers hold the lock.
func (r *Runner) Run(ctx context.Context, kind, period string) (Result, error) {
	res := Result{Kind: kind, Period: period}
	if kind != KindDaily && kind != KindWeekly {
		return res, i18n.Errorf("retro.err.kind")
	}
	start, err := time.ParseInLocation("2006-01-02", period, r.loc())
	if err != nil {
		return res, i18n.Errorf("retro.err.date_format", period)
	}
	today := dayOf(r.now())
	if kind == KindDaily && period >= today {
		return res, i18n.Errorf("retro.err.daily_until_yesterday", period)
	}
	if kind == KindWeekly && (start.Weekday() != time.Monday || dayOf(start.AddDate(0, 0, 7)) > today) {
		return res, i18n.Errorf("retro.err.weekly_monday", period)
	}
	if _, st, _ := r.existing(kind, period); st == StatusDone || st == StatusEmpty {
		res.Status = StatusSkipped
		return res, nil
	}
	var checks []SourceCheck
	if kind == KindDaily {
		checks = r.checkSources()
	}
	mat, err := r.material(kind, period)
	if err != nil {
		return res, err
	}
	mat.SourceChecks = checks
	return r.launch(ctx, mat, r.Cfg.RetroModel)
}

// launch runs the host on material and waits for its single submission.
// Empty material finishes without calling the host.
func (r *Runner) launch(ctx context.Context, mat Material, model string) (Result, error) {
	res := Result{Kind: mat.Kind, Period: mat.Period}
	nowMs := r.now().UnixMilli()
	res.ID = memory.NewID("r-")
	mat.ID = res.ID
	if mat.empty() {
		res.Status = StatusEmpty
		_, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,finished_at,summary,session) VALUES(?,?,?,?,?,?,?,?,?)`,
			res.ID, mat.Kind, mat.Period, StatusEmpty, "", nowMs, nowMs, i18n.T("retro.no_material"), mat.Session)
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
	if _, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,session) VALUES(?,?,?,?,?,?,?)`,
		res.ID, mat.Kind, mat.Period, StatusRunning, r.Cfg.RetroHost, nowMs, mat.Session); err != nil {
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
	out, execErr := exe(ctx, r.hostCommand(res.ID, runDir, prompt(mat), model))
	var status string
	_ = r.Store.DB().QueryRow(`SELECT status FROM retros WHERE id=?`, res.ID).Scan(&status)
	if status == "submitting" { // 제출 처리 중에 호스트가 끝났다
		time.Sleep(2 * time.Second)
		_ = r.Store.DB().QueryRow(`SELECT status FROM retros WHERE id=?`, res.ID).Scan(&status)
	}
	os.RemoveAll(dir) // 재료(대화 발췌)는 남기지 않는다. 다시 돌 때 새로 만든다
	host := parseHostOutput(out)
	run := map[string]any{"retroId": res.ID, "retroKind": mat.Kind, "model": model, "ok": status == StatusDone, "costKnown": host.Known,
		"sourceChecks": len(mat.SourceChecks)}
	if host.Known {
		run["costUsd"], run["inputTokens"], run["outputTokens"] = host.CostUSD, host.InputTokens, host.OutputTokens
	}
	_ = r.Store.RecordEvent(memory.Event{Kind: memory.EventRetroRun, Host: r.Cfg.RetroHost, Session: mat.Session}, run)
	if status == StatusDone {
		res.Status = StatusDone
		return res, nil
	}
	msg := i18n.T("retro.host_no_result")
	if execErr != nil {
		msg += ": " + execErr.Error()
	}
	if tail := strings.TrimSpace(host.Text); tail != "" {
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		msg += "\n" + tail
	}
	signedOut := hostSignedOut(out)
	if signedOut {
		msg += "\n" + i18n.T("retro.signed_out_note")
	}
	res.Status, res.Error = StatusFailed, msg
	_, _ = r.Store.DB().Exec(`UPDATE retros SET status=?, finished_at=?, error=? WHERE id=?`, StatusFailed, r.now().UnixMilli(), msg, res.ID)
	if signedOut {
		return res, signedOutError(msg)
	}
	return res, errors.New(msg)
}

// hostSignedOut says whether the host failed for want of a sign-in before
// it reached the model. Claude prints its JSON result with no cost or tokens
// (keychain unreadable, no /login); codex exec ends on a 401 line (no
// auth.json). A run that reached the model is never one, whatever its answer
// says.
func hostSignedOut(out string) bool {
	if h := parseHostOutput(out); h.Known {
		return h.CostUSD == 0 && h.InputTokens == 0 && h.OutputTokens == 0 &&
			(strings.Contains(h.Text, "Not logged in") || strings.Contains(h.Text, "Please run /login"))
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ERROR: unexpected status 401 Unauthorized: Missing bearer or basic authentication") {
			return true
		}
	}
	return false
}

// signedOutNotes are the endings (in every language) of the error of a run
// the host was not signed in for; legacyDaily finds such runs by them.
func signedOutNotes() []any {
	var out []any
	for _, l := range i18n.Langs {
		out = append(out, "%"+i18n.TIn(l, "retro.signed_out_note")+"%")
	}
	return out
}

// signedOutError is a failed run that matches ErrHostSignedOut.
type signedOutError string

func (e signedOutError) Error() string        { return string(e) }
func (e signedOutError) Is(target error) bool { return target == ErrHostSignedOut }

// HostOutput is what a headless host printed: Claude's JSON result carries
// the cost and tokens; anything else is kept as text.
type HostOutput struct {
	Known        bool
	CostUSD      float64
	InputTokens  int
	OutputTokens int
	Text         string
	IsError      bool // 호스트가 오류로 끝났다(상한 초과·로그인 없음 등)
}

// parseHostOutput reads the last line of out that is a JSON object with a
// cost (claude -p --output-format json prints one); without one the whole
// output is the text.
func parseHostOutput(out string) HostOutput {
	h := HostOutput{Text: out}
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var v struct {
			IsError bool     `json:"is_error"`
			Result  string   `json:"result"`
			Cost    *float64 `json:"total_cost_usd"`
			Usage   struct {
				Input       int `json:"input_tokens"`
				CacheRead   int `json:"cache_read_input_tokens"`
				CacheCreate int `json:"cache_creation_input_tokens"`
				Output      int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &v) != nil || v.Cost == nil {
			continue
		}
		return HostOutput{Known: true, CostUSD: *v.Cost, InputTokens: v.Usage.Input + v.Usage.CacheRead + v.Usage.CacheCreate,
			OutputTokens: v.Usage.Output, Text: v.Result, IsError: v.IsError}
	}
	return h
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
	var results []Result
	var errs []error
	// 옛 작업 기록 DB(work.db)도 같은 날 함께 복사한다(읽기 전용으로 연다). 실패해도 회고는 돈다
	if _, err := sqlitedb.BackupDaily(r.Paths.WorkDB(), r.Paths.Backups(), "work", max(r.Cfg.BackupKeep, 1), r.now()); err != nil {
		errs = append(errs, i18n.Errorf("retro.err.backup", err))
	}
	// 세션 끝 회고를 먼저 끝내야 하루 회고가 그 결과를 모으고 남은 발화만 읽는다.
	sres, err := r.drain(ctx)
	results = append(results, sres...)
	if err != nil {
		errs = append(errs, err)
	}
	today := r.now()
	yesterday := dayOf(today.AddDate(0, 0, -1))
	floor := dayOf(today.AddDate(0, 0, -maxCatchUpDays))
	finished := map[string]bool{}
	var first string
	rows, err := r.Store.DB().Query(`SELECT period FROM retros WHERE kind='daily' AND status IN ('done','empty') ORDER BY period`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			finished[p] = true
			if first == "" {
				first = p
			}
		}
	}
	rows.Close()
	start := yesterday // 처음이면 어제만
	if first != "" {
		start = first
		if start < floor {
			start = floor
		}
	}
	for d := start; d <= yesterday; d = nextDay(d, r.loc()) {
		if finished[d] {
			continue
		}
		res, err := r.Run(ctx, KindDaily, d)
		results = append(results, res)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d, err))
		}
	}
	// 주간 회고가 판정을 근거로 쓰도록 그 전에 관찰 기간이 끝난 변경을 판정한다.
	if _, err := r.Store.JudgeLedger(max(r.Cfg.LedgerWindowDays, 1)); err != nil {
		errs = append(errs, i18n.Errorf("retro.err.effect", err))
	}
	// 해로움으로 판정된 자동 변경은 저장 계층이 되돌리고 알린다(A2).
	if _, err := r.Store.RevertHarmfulAuto(); err != nil {
		errs = append(errs, err)
	}
	week := lastWeekStart(today)
	res, err := r.Run(ctx, KindWeekly, week)
	if res.Status != StatusSkipped {
		results = append(results, res)
	}
	if err != nil {
		errs = append(errs, i18n.Errorf("retro.err.weekly", week, err))
	}
	// 옛 기억 판정은 하루 회고·주간 회고 뒤에 하루 묶음 수까지 돈다(설계 · Orbit 3차 O3-3).
	lres, err := r.legacyDaily(ctx)
	results = append(results, lres...)
	if err != nil {
		errs = append(errs, err)
	}
	// 그 사이 쌓인 교정·사용 판정으로 증거가 생긴 후보를 확정한다(A2).
	if _, err := r.Store.ConfirmCandidates(); err != nil {
		errs = append(errs, err)
	}
	if _, err := r.Store.CloseSettled(); err != nil {
		errs = append(errs, err)
	}
	if err := r.Store.PruneSessionState(sessionStateDays); err != nil {
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

// hostCommand builds the headless host run. The prompt goes through stdin
// (multi-line arguments break through .cmd shims on Windows) and Claude's MCP
// configuration through a file.
func (r *Runner) hostCommand(id, dir, text, model string) Cmd {
	env := []string{"ORBIT_HOME=" + r.Paths.Home, "ORBIT_RETRO_ID=" + id}
	if r.Cfg.RetroHost == "codex" {
		name := r.Cfg.CodexPath
		if name == "" {
			name = "codex"
		}
		q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
		args := []string{"exec", "--ephemeral", "--skip-git-repo-check", "-C", dir, "-s", "read-only",
			"-c", `approval_policy="never"`,
			"-c", "mcp_servers.orbit-retro.command=" + q(r.Mem),
			"-c", `mcp_servers.orbit-retro.args=["mcp","--retro",` + q(id) + `]`,
			"-c", "mcp_servers.orbit-retro.env={ORBIT_HOME=" + q(r.Paths.Home) + "}",
			"-c", `mcp_servers.orbit-retro.default_tools_approval_mode="approve"`}
		if model != "" {
			args = append(args, "-m", model)
		}
		return Cmd{Name: name, Args: append(args, "-"), Dir: dir, Env: env, Stdin: text}
	}
	name := r.Cfg.ClaudePath
	if name == "" {
		name = "claude"
	}
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"orbit-retro": map[string]any{
		"type": "stdio", "command": r.Mem, "args": []string{"mcp", "--retro", id}, "env": map[string]string{"ORBIT_HOME": r.Paths.Home}}}})
	configPath := filepath.Join(r.Paths.RetroDir(), id, "mcp.json")
	_ = os.MkdirAll(filepath.Dir(configPath), 0o700)
	_ = os.WriteFile(configPath, mcpConfig, 0o600)
	tools := []string{"retro_material", "retro_submit", "retro_read_file", "memory_find", "memory_get"}
	for i, t := range tools {
		tools[i] = "mcp__orbit-retro__" + t
	}
	args := []string{"-p", "--mcp-config", configPath, "--strict-mcp-config", "--allowedTools", strings.Join(tools, ","),
		"--no-session-persistence", "--setting-sources", "project", "--output-format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return Cmd{Name: name, Args: args, Dir: dir, Env: env, Stdin: text}
}

// ExecHost runs a host process with stdin closed and returns all of its output
// (stdout and stderr). The harness lab runs its candidates and judge with it.
func ExecHost(ctx context.Context, c Cmd) (string, error) { return execHost(ctx, c) }

// ParseHostOutput reads cost, tokens and the answer from a host's JSON output.
func ParseHostOutput(out string) HostOutput { return parseHostOutput(out) }

// execHost runs a host process with stdin closed and returns the tail of its output.
func execHost(ctx context.Context, c Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = strings.NewReader(c.Stdin)
	cmd.WaitDelay = 15 * time.Second // 자식이 출력 파이프를 쥐고 있어도 끝낸다
	setProcessGroup(cmd)
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
	Session    string   `json:"session,omitempty"` // 세션 끝 회고의 세션(호스트:세션 id)
}

const statusCols = `id,kind,period,status,host,summary,flags,journal_id,error,started_at,finished_at,session`

func scanStatus(row interface{ Scan(...any) error }) (RetroStatus, error) {
	var st RetroStatus
	var flags string
	err := row.Scan(&st.ID, &st.Kind, &st.Period, &st.Status, &st.Host, &st.Summary, &flags, &st.JournalID, &st.Error, &st.StartedAt, &st.FinishedAt, &st.Session)
	st.Flags = parseFlags(flags)
	return st, err
}

func parseFlags(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// memoryIDs reads the memoryIds of an injection event's data.
func memoryIDs(data string) []string {
	var d struct {
		MemoryIDs []string `json:"memoryIds"`
	}
	_ = json.Unmarshal([]byte(data), &d)
	return d.MemoryIDs
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
		return st, i18n.Errorf("retro.err.no_retro", id)
	}
	return st, err
}

func itoa(n int) string { return strconv.Itoa(n) }
