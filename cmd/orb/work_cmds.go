package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onidot-labs/openorbit/internal/config"
	"github.com/onidot-labs/openorbit/internal/i18n"
	"github.com/onidot-labs/openorbit/internal/mcpserver"
	"github.com/onidot-labs/openorbit/internal/project"
	"github.com/onidot-labs/openorbit/internal/work"
	"github.com/onidot-labs/openorbit/internal/work/hook"
)

// cmdWork is orbit-pi's door to orbit.work (설계 · OpenOrbit 4차 R4-5): emit writes the
// orchestrator's plan, progress, evidence and cost; get and list read them back as
// the same contract JSON the MCP work_get and work_list give.
func cmdWork(a *app, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "emit":
		if len(args) != 1 {
			return errUsage
		}
		return a.workEmit()
	case "get":
		fs := a.flags("work")
		children := fs.Bool("children", false, i18n.T("flag.work.children"))
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return errUsage
		}
		return a.workCall("work_get", map[string]any{"workId": pos[0], "includeChildren": *children})
	case "list":
		fs := a.flags("work")
		var cwds multiFlag
		fs.Var(&cwds, "cwd", i18n.T("flag.work.cwd"))
		state := fs.String("state", "any", "open | done | any")
		launcher := fs.String("launcher", "", i18n.T("flag.work.launcher"))
		limit := fs.Int("limit", 0, i18n.T("flag.work.limit"))
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 0 {
			return errUsage
		}
		in := map[string]any{"state": *state}
		if len(cwds) > 0 {
			abs := make([]string, len(cwds))
			for i, c := range cwds {
				if abs[i], err = filepath.Abs(c); err != nil {
					return err
				}
			}
			in["cwdPrefix"] = abs
		}
		if *launcher != "" {
			in["launcher"] = *launcher
		}
		if *limit != 0 {
			in["limit"] = *limit
		}
		return a.workCall("work_list", in)
	}
	return errUsage
}

// workCall prints a read tool's structured result. A contract error is printed
// the same way and ends with exit code 1.
func (a *app) workCall(tool string, in map[string]any) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	o := mcpserver.WorkOptions{DB: paths().WorkDB(), Spool: paths().WorkSpool(), Read: work.ReadOptions{StaleAfter: cfg.StaleAfter()}}
	body, isErr, err := mcpserver.CallWork(o, tool, in)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", "  "); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, out.String())
	if isErr {
		var e struct {
			Error struct {
				Code, Message string
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return i18n.Errorf("cli.work.call_failed", e.Error.Code, e.Error.Message)
	}
	return nil
}

// emitLine is one stdin line of orb work emit.
type emitLine struct {
	Kind    string          `json:"kind"`
	Session string          `json:"session"`
	At      int64           `json:"at"`
	Key     string          `json:"key"`
	Cwd     string          `json:"cwd"`
	Data    json.RawMessage `json:"data"`
}

// emitSessionStart is session.start's data; project, repo and branch come from cwd when empty.
type emitSessionStart struct {
	ListID   string        `json:"listId"`
	Source   string        `json:"source"`
	Launcher string        `json:"launcher"`
	Model    string        `json:"model"`
	Project  *work.Project `json:"project"`
	Repo     string        `json:"repo"`
	Branch   string        `json:"branch"`
}

type emitTaskCreate struct {
	List, ID, Subject, Description, Owner string
}

type emitTaskUpdate struct {
	List         string   `json:"list"`
	ID           string   `json:"id"`
	Status       *string  `json:"status"`
	Subject      *string  `json:"subject"`
	Owner        *string  `json:"owner"`
	Blocked      *string  `json:"blocked"`
	AddBlockedBy []string `json:"addBlockedBy"`
}

type emitRun struct {
	Agent   string        `json:"agent"`
	Type    string        `json:"type"`
	Step    string        `json:"step"`
	List    string        `json:"list"`
	Verdict *work.Verdict `json:"verdict"`
	Cost    *work.Cost    `json:"cost"`
}

type emitCommand struct {
	Text  string `json:"text"`
	OK    *bool  `json:"ok"`
	Agent string `json:"agent"`
	Step  string `json:"step"`
	List  string `json:"list"`
}

type emitCommit struct {
	Repo    string   `json:"repo"`
	SHA     string   `json:"sha"`
	Subject string   `json:"subject"`
	Files   []string `json:"files"`
	Agent   string   `json:"agent"`
	Step    string   `json:"step"`
	List    string   `json:"list"`
}

// emitEnv is what turning lines into events may consult besides the lines.
type emitEnv struct {
	now     func() int64
	cwd     string
	git     hook.Git
	project func(cwd string) work.Project
}

// workEmit checks every stdin line first and writes nothing when one is wrong.
func (a *app) workEmit() error {
	cwd, _ := os.Getwd()
	env := emitEnv{now: func() int64 { return time.Now().UnixMilli() }, cwd: cwd, git: hook.SystemGit{},
		project: func(dir string) work.Project {
			p := project.Resolve(dir)
			return work.Project{Scope: p.Scope, Label: p.Label}
		}}
	evs, err := parseEmit(a.stdin, env)
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.WorkOn() {
		fmt.Fprintln(a.stderr, "orb work: "+i18n.T("cli.work.disabled"))
	}
	return a.json(recordEmit(cfg, evs))
}

type emitResult struct {
	Events   int      `json:"events"`
	WorkIDs  []string `json:"workIds"`
	Disabled bool     `json:"disabled,omitempty"` // 작업 기록이 꺼져 있어 아무것도 쓰지 않았다
}

func recordEmit(cfg config.Config, evs []work.Event) emitResult {
	out := emitResult{WorkIDs: []string{}}
	if !cfg.WorkOn() {
		out.Disabled = true
		return out
	}
	if len(evs) == 0 {
		return out
	}
	seen := map[string]bool{}
	for _, res := range work.RecordAll(paths().WorkDB(), paths().WorkSpool(), evs, time.Now().Add(10*time.Second)) {
		if res.Seq == 0 {
			continue // 같은 멱등 키가 이미 있었거나 스풀에 남았다
		}
		out.Events++
		for _, id := range append(append([]string{}, res.Opened...), res.Touched...) {
			if !seen[id] {
				seen[id] = true
				out.WorkIDs = append(out.WorkIDs, id)
			}
		}
	}
	return out
}

// parseEmit turns stdin lines into orbit-pi events. Unknown kinds and wrong
// types fail the whole input; unknown data fields are ignored (orbit.work 판 규칙).
func parseEmit(r io.Reader, env emitEnv) ([]work.Event, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var evs []work.Event
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		e, err := emitEvent(line, env)
		if err != nil {
			return nil, i18n.Errorf("cli.work.bad_line", n, err)
		}
		evs = append(evs, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return evs, nil
}

func need(fields ...[2]string) error {
	for _, f := range fields {
		if f[1] == "" {
			return i18n.Errorf("cli.work.missing", f[0])
		}
	}
	return nil
}

func oneOf(field, v string, allowed ...string) error {
	for _, x := range allowed {
		if v == x {
			return nil
		}
	}
	return i18n.Errorf("cli.work.bad_value", field, v)
}

func emitEvent(line []byte, env emitEnv) (work.Event, error) {
	var l emitLine
	if err := json.Unmarshal(line, &l); err != nil {
		return work.Event{}, err
	}
	if err := need([2]string{"kind", l.Kind}, [2]string{"session", l.Session}); err != nil {
		return work.Event{}, err
	}
	if l.At < 0 {
		return work.Event{}, i18n.Errorf("cli.work.bad_value", "at", fmt.Sprint(l.At))
	}
	if l.At == 0 {
		l.At = env.now()
	}
	if l.Cwd == "" {
		l.Cwd = env.cwd
	}
	data := l.Data
	if len(data) == 0 || string(data) == "null" {
		data = []byte("{}")
	}
	payload, err := emitPayload(l.Kind, data, l.Cwd, env)
	if err != nil {
		return work.Event{}, err
	}
	e := work.NewEvent(l.Kind, work.HostOrbitPi, l.Session, l.At, payload)
	e.Key, e.Cwd = l.Key, l.Cwd
	return e, nil
}

func emitPayload(kind string, data []byte, cwd string, env emitEnv) (any, error) {
	decode := func(v any) error {
		if err := json.Unmarshal(data, v); err != nil {
			return i18n.Errorf("cli.work.bad_data", kind, err)
		}
		return nil
	}
	switch kind {
	case work.KindSessionStart:
		var d emitSessionStart
		if err := decode(&d); err != nil {
			return nil, err
		}
		if err := need([2]string{"listId", d.ListID}); err != nil {
			return nil, err
		}
		if err := oneOf("source", d.Source, "startup", "resume"); err != nil {
			return nil, err
		}
		if d.Launcher == "" {
			d.Launcher = work.HostOrbitPi
		}
		p := work.SessionStart{Source: d.Source, ListID: d.ListID, Launcher: work.Clean(d.Launcher, 40), Model: work.Clean(d.Model, 80), Repo: d.Repo, Branch: d.Branch}
		if d.Project != nil {
			p.Project = *d.Project
		}
		if cwd != "" {
			if p.Project.Scope == "" && env.project != nil {
				p.Project = env.project(cwd)
			}
			if p.Repo == "" && env.git != nil {
				p.Repo = env.git.Toplevel(cwd)
			}
			if p.Branch == "" && env.git != nil {
				p.Branch = env.git.Branch(cwd)
			}
		}
		return p, nil
	case work.KindTaskCreate:
		var d emitTaskCreate
		if err := decode(&d); err != nil {
			return nil, err
		}
		if err := need([2]string{"list", d.List}, [2]string{"id", d.ID}, [2]string{"subject", d.Subject}); err != nil {
			return nil, err
		}
		return work.TaskCreate{List: d.List, ID: d.ID, Subject: work.Clean(d.Subject, 200), Description: d.Description, Owner: work.Clean(d.Owner, 80)}, nil
	case work.KindTaskUpdate:
		var d emitTaskUpdate
		if err := decode(&d); err != nil {
			return nil, err
		}
		if err := need([2]string{"list", d.List}, [2]string{"id", d.ID}); err != nil {
			return nil, err
		}
		if d.Status != nil {
			if err := oneOf("status", *d.Status, "pending", "in_progress", "completed", "deleted"); err != nil {
				return nil, err
			}
		}
		clean := func(s *string, n int) *string {
			if s == nil {
				return nil
			}
			v := work.Clean(*s, n)
			return &v
		}
		return work.TaskUpdate{List: d.List, ID: d.ID, Status: d.Status, Subject: clean(d.Subject, 200), Owner: clean(d.Owner, 80), Blocked: clean(d.Blocked, 200), AddBlockedBy: d.AddBlockedBy}, nil
	case work.KindRunStart, work.KindRunStop:
		var d emitRun
		if err := decode(&d); err != nil {
			return nil, err
		}
		if err := need([2]string{"agent", d.Agent}, [2]string{"type", d.Type}, [2]string{"step", d.Step}); err != nil {
			return nil, err
		}
		ref := work.StepRef{Step: d.Step, List: d.List}
		if kind == work.KindRunStart {
			return work.RunStart{Agent: d.Agent, Type: d.Type, StepRef: ref}, nil
		}
		if d.Verdict != nil {
			if err := oneOf("verdict.result", d.Verdict.Result, "pass", "needs_fix", "needs_user"); err != nil {
				return nil, err
			}
			if d.Verdict.Role == "" {
				d.Verdict.Role = d.Type
			}
		}
		if d.Cost != nil {
			if err := need([2]string{"cost.engine", d.Cost.Engine}, [2]string{"cost.model", d.Cost.Model}); err != nil {
				return nil, err
			}
			c := d.Cost
			if c.InputTokens < 0 || c.OutputTokens < 0 || c.CacheReadTokens < 0 || c.CacheWriteTokens < 0 || (c.USD != nil && *c.USD < 0) || (c.Turns != nil && *c.Turns < 0) {
				return nil, i18n.Errorf("cli.work.bad_value", "cost", "-")
			}
		}
		return work.RunStop{Agent: d.Agent, Type: d.Type, Verdict: d.Verdict, Cost: d.Cost, StepRef: ref}, nil
	case work.KindCommand:
		var d emitCommand
		if err := decode(&d); err != nil {
			return nil, err
		}
		if err := need([2]string{"text", d.Text}, [2]string{"step", d.Step}); err != nil {
			return nil, err
		}
		if d.OK == nil {
			return nil, i18n.Errorf("cli.work.missing", "ok")
		}
		return work.Command{Text: work.Clean(d.Text, 200), OK: *d.OK, Agent: d.Agent, StepRef: work.StepRef{Step: d.Step, List: d.List}}, nil
	case work.KindCommit:
		var d emitCommit
		if err := decode(&d); err != nil {
			return nil, err
		}
		if err := need([2]string{"repo", d.Repo}, [2]string{"sha", d.SHA}, [2]string{"step", d.Step}); err != nil {
			return nil, err
		}
		return work.Commit{Repo: d.Repo, SHA: d.SHA, Subject: work.Clean(d.Subject, 200), Files: d.Files, Agent: d.Agent, StepRef: work.StepRef{Step: d.Step, List: d.List}}, nil
	case work.KindTitle:
		var d work.Title
		if err := decode(&d); err != nil {
			return nil, err
		}
		d.Title = work.Clean(d.Title, work.TitleMax)
		if err := need([2]string{"work", d.Work}, [2]string{"title", d.Title}); err != nil {
			return nil, err
		}
		return d, nil
	case work.KindSessionEnd:
		var d work.SessionEnd
		if err := decode(&d); err != nil {
			return nil, err
		}
		d.Reason = work.Clean(d.Reason, 80)
		return d, nil
	}
	return nil, i18n.Errorf("cli.work.unknown_kind", kind)
}
