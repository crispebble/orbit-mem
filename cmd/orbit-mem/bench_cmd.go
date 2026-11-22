package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/claudelog"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
	benchpaths "github.com/crispebble/orbit-mem/internal/bench/paths"
	"github.com/crispebble/orbit-mem/internal/i18n"
)

// cmdBench is the assembly point of the evaluation set: it puts the bench domain together with
// its adapters (the recall paths, the Claude Code log). It does no backup, tidy, activity
// record or update check, and it never writes to a DB it is given.
func cmdBench(a *app, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "recall":
		return benchRecall(a, args[1:])
	case "extract":
		return benchExtract(a, args[1:])
	case "audit-injections":
		return benchAuditInjections(a, args[1:])
	}
	return errUsage
}

// benchAuditInjections counts the recorded injections of memories that were not a current value at the time
// (bench.AuditInjections). It reads a copy of the DB given and never opens or writes the DB itself; there is no
// default DB, so it never reads the home's memory DB unless that path is given.
func benchAuditInjections(a *app, args []string) error {
	fs := a.flags("bench audit-injections")
	db := fs.String("db", "", i18n.T("flag.bench.db_audit"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return benchFlagErr(err)
	}
	if len(pos) != 0 || *db == "" {
		return errUsage
	}
	res, err := bench.AuditInjections(*db)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(res)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.bench.audit", res.Events, res.Injections, res.Dead, res.DeadEvents, res.Pairs, res.Memories, res.Unknown))
	for _, k := range sortedCounts(res.ByKind) {
		fmt.Fprintln(a.stdout, i18n.T("cli.bench.audit_kind", k, res.ByKind[k]))
	}
	for _, k := range sortedCounts(res.ByWhy) {
		fmt.Fprintln(a.stdout, i18n.T("cli.bench.audit_why", k, res.ByWhy[k]))
	}
	return nil
}

// sortedCounts are the keys of m in order.
func sortedCounts(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func benchRecall(a *app, args []string) error {
	fs := a.flags("bench recall")
	cases := fs.String("cases", "", i18n.T("flag.bench.cases"))
	db := fs.String("db", "", i18n.T("flag.bench.db"))
	recalls := fs.String("recall", "current,legacy", i18n.T("flag.bench.recall"))
	split := fs.String("split", "all", i18n.T("flag.bench.split"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	report := fs.String("report", "", i18n.T("flag.bench.report"))
	steps := fs.String("steps", "", i18n.T("flag.bench.steps"))
	var sets setFlags
	fs.Var(&sets, "set", i18n.T("flag.bench.set"))
	pos, err := parse(fs, args)
	if err != nil {
		return benchFlagErr(err)
	}
	if len(pos) != 0 || *cases == "" {
		return errUsage
	}
	if !slices.Contains([]string{bench.PartAll, bench.PartTrain, bench.PartTest}, *split) {
		return i18n.Errorf("cli.bench.split_bad", *split)
	}
	override, err := sets.parse()
	if err != nil {
		return err
	}
	if err := hostpath.CheckSet(override); err != nil {
		return i18n.Errorf("cli.bench.set_bad", strings.Join(sets, " "), err)
	}
	factories, err := benchpaths.FactoriesWith(strings.Split(*recalls, ","), benchpaths.Options{Set: override})
	if u := (benchpaths.UnknownError{}); errors.As(err, &u) {
		return i18n.Errorf("cli.bench.recall_bad", u.Name)
	} else if err != nil {
		return err
	}
	set, err := bench.Read(*cases)
	if err != nil {
		return err
	}
	if *steps != "" {
		sessions, err := readSteps(*steps)
		if err != nil {
			return err
		}
		kept, dropped := caseTimes(set, sessions)
		if kept == 0 {
			return i18n.Err("cli.bench.steps_none")
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.bench.steps", kept, dropped))
	}
	switch {
	case len(set.Memories) > 0 && *db != "":
		return i18n.Err("cli.bench.db_both")
	case len(set.Memories) == 0 && *db == "":
		return i18n.Err("cli.bench.db_needed")
	}
	p, err := bench.Prepare(set, *db)
	if err != nil {
		return err
	}
	defer p.Close()
	rep, err := bench.Run(p, factories...)
	if err != nil {
		return err
	}
	data, err := rep.JSON()
	if err != nil {
		return err
	}
	if *report != "" {
		if err := writeFile0600(*report, data); err != nil {
			return err
		}
	}
	if *asJSON {
		_, err = a.stdout.Write(data)
		return err
	}
	fmt.Fprint(a.stdout, rep.Text(*split, benchWording{}))
	fmt.Fprintln(a.stdout, i18n.T("cli.bench.similar", hostpath.SimilarName))
	if len(sets) > 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.bench.set", strings.Join(sets, " ")))
	}
	if *report != "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.bench.report_written", *report))
	}
	return nil
}

// setFlags are the --set key=value flags, in the order given.
type setFlags []string

func (s *setFlags) String() string { return strings.Join(*s, ",") }

func (s *setFlags) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// parse splits the flags into key and value; a key given twice keeps the last value.
func (s setFlags) parse() (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range s {
		k, v, ok := strings.Cut(kv, "=")
		if k = strings.TrimSpace(k); !ok || k == "" {
			return nil, i18n.Errorf("cli.bench.set_form", kv)
		}
		out[k] = v
	}
	return out, nil
}

// benchFlagErr turns a flag error (already printed with the usage) into a usage error.
func benchFlagErr(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return errUsage
}

func writeFile0600(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// benchCase is a case of the replay file the extraction writes (the shape of bench.Case).
type benchCase struct {
	ID      string   `json:"id"`
	Session string   `json:"session"`
	CWD     string   `json:"cwd,omitempty"`
	Origin  string   `json:"origin,omitempty"`
	Kind    string   `json:"kind"`
	Prompt  string   `json:"prompt,omitempty"`
	Task    string   `json:"task,omitempty"`
	Tool    string   `json:"tool,omitempty"`
	Input   string   `json:"input,omitempty"`  // a tool call's input text (bench.Case.Input)
	Source  string   `json:"source,omitempty"` // a session start's source (bench.Case.Source)
	Expect  []string `json:"expect"`
	Note    string   `json:"note,omitempty"`
	At      string   `json:"at"` // when the step happened: the run puts the DB at that time (bench.Case.At)
}

type benchCaseFile struct {
	About string      `json:"about"`
	Now   string      `json:"now"`
	CWD   string      `json:"cwd"`
	Seed  string      `json:"seed"`
	Cases []benchCase `json:"cases"`
}

func benchExtract(a *app, args []string) error {
	fs := a.flags("bench extract")
	var projects multiFlag
	fs.Var(&projects, "projects", i18n.T("flag.bench.projects"))
	db := fs.String("db", paths().DB(), i18n.T("flag.bench.db_extract"))
	out := fs.String("out", filepath.Join(paths().Home, "bench", "replay"), i18n.T("flag.bench.out"))
	minSessions := fs.Int("min-sessions", 0, i18n.T("flag.bench.min_sessions"))
	minRequests := fs.Int("min-requests", 0, i18n.T("flag.bench.min_requests"))
	maxSessions := fs.Int("max-sessions", 20, i18n.T("flag.bench.max_sessions"))
	maxRequests := fs.Int("max-requests", 120, i18n.T("flag.bench.max_requests"))
	maxPerSession := fs.Int("max-requests-per-session", 12, i18n.T("flag.bench.max_requests_per_session"))
	all := fs.Bool("all", false, i18n.T("flag.bench.all"))
	pos, err := parse(fs, args)
	if err != nil {
		return benchFlagErr(err)
	}
	if len(pos) != 0 || *maxSessions < 0 || *maxRequests < 0 || *maxPerSession < 0 {
		return errUsage
	}
	// 깃발의 0은 상한 없음이다(claudelog.NoLimit). Options의 0은 기본값이라 그대로 넘기지 않는다.
	limit := func(v int) int {
		if v == 0 {
			return claudelog.NoLimit
		}
		return v
	}
	pick := claudelog.Options{MinSessions: *minSessions, MinRequests: *minRequests, MaxSessions: limit(*maxSessions),
		MaxRequests: limit(*maxRequests), MaxRequestsPerSession: limit(*maxPerSession), All: *all}
	outAbs, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if tree := insideGitTree(outAbs); tree != "" {
		return i18n.Errorf("cli.bench.out_in_repo", tree)
	}
	if _, err := os.Stat(*db); err != nil {
		return err
	}
	roots := []string(projects)
	if len(roots) == 0 {
		roots = []string{claudelog.DefaultRoot()}
	}
	sessions, rep, err := claudelog.SelectReport(roots, pick)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return i18n.Err("cli.bench.no_sessions")
	}
	now := time.Now().UTC()
	file := benchCaseFile{
		About: "replay cases extracted from Claude Code logs (origin is a draft, expect is empty)",
		Now:   now.Format(time.RFC3339),
		CWD:   sessions[0].Cwd,
		Seed:  "replay-" + now.Format("20060102"),
	}
	var starts, reqs, subs, tools, merged, forked, unread, unkeyed int
	for _, s := range sessions {
		if s.Files > 1 {
			merged++
		}
		if s.Parent != "" {
			forked++
		}
		unread += s.UnreadFiles
		unkeyed += s.UnkeyedSteps
		n := 0
		for _, st := range s.Steps {
			n++
			c := benchCase{ID: fmt.Sprintf("%s-%03d", s.ID, n), Session: s.ID, Kind: bench.KindRequest, Expect: []string{},
				At: st.At.UTC().Format(time.RFC3339Nano)}
			if s.Cwd != "" {
				c.CWD = s.Cwd
			} else if file.CWD == "" {
				continue
			}
			switch st.Kind {
			case claudelog.KindRequest:
				c.Prompt, c.Origin, c.Note = st.Text, string(st.Origin), originNote(st.Evidence)
				reqs++
			case claudelog.KindTool:
				c.Tool, c.Input, c.Note = st.Tool, st.Input, "tool call"
				tools++
			case claudelog.KindStart:
				// 기록이 모르는 출처는 빈 출처로 둔다: 재생은 그 시작을 다시 부르되 표시를 비우지 않는다.
				c.Kind, c.Note = bench.KindStart, "session start "+st.Source
				if slices.Contains(bench.StartSources, st.Source) {
					c.Source = st.Source
				}
				starts++
			case claudelog.KindDelegate:
				c.Kind, c.Task, c.Note = bench.KindSubagent, st.Task, "delegation "+st.SubagentType
				subs++
			default:
				continue
			}
			file.Cases = append(file.Cases, c)
		}
	}
	if err := os.MkdirAll(outAbs, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(outAbs, 0o700); err != nil {
		return err
	}
	if err := replaceCopy(*db, filepath.Join(outAbs, "memory-copy.db")); err != nil {
		return err
	}
	for name, v := range map[string]any{"sessions.json": sessions, "cases.json": file} {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFile0600(filepath.Join(outAbs, name), append(b, '\n')); err != nil {
			return err
		}
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.bench.extract_done", outAbs, len(sessions), starts, reqs, subs, tools))
	if merged+forked+unread+unkeyed+rep.LostFiles > 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.bench.extract_merged", merged, forked, unread, rep.LostFiles, unkeyed))
	}
	return nil
}

// readSteps reads the sessions.json bench extract wrote next to its cases.
func readSteps(path string) ([]claudelog.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sessions []claudelog.Session
	if err := json.Unmarshal(b, &sessions); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return sessions, nil
}

// caseTimes gives the cases of a replay file made before bench extract wrote the time of each case their
// time from the sessions of the same extraction. The id of such a case is "<session>-<n>", n the step number
// (1 based) of that session, and the step must be the same: a request with the same text, a tool call of the
// same tool, a delegation with the same task. A case whose step does not say so has no known time and is
// left out (dropped), so the set runs only on cases whose time is certain. A case that has its time keeps it.
func caseTimes(set *bench.Set, sessions []claudelog.Session) (kept, dropped int) {
	byID := make(map[string]claudelog.Session, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}
	out := set.Cases[:0]
	for _, c := range set.Cases {
		if c.At == "" {
			if st, ok := stepOf(byID, c); ok {
				c.At = st.At.UTC().Format(time.RFC3339Nano)
			}
		}
		if _, ok := c.Time(); !ok {
			dropped++
			continue
		}
		out = append(out, c)
	}
	set.Cases = out
	return len(out), dropped
}

func stepOf(byID map[string]claudelog.Session, c bench.Case) (claudelog.Step, bool) {
	sid, num, ok := strings.Cut(c.ID[min(len(c.ID), len(c.Session)):], "-")
	n, err := strconv.Atoi(num)
	s, found := byID[c.Session]
	if !ok || sid != "" || err != nil || !found || !strings.HasPrefix(c.ID, c.Session) || n < 1 || n > len(s.Steps) {
		return claudelog.Step{}, false
	}
	st := s.Steps[n-1]
	switch {
	case c.CaseKind() == bench.KindStart:
		ok = st.Kind == claudelog.KindStart && st.Source == c.Source
	case c.CaseKind() == bench.KindSubagent:
		ok = st.Kind == claudelog.KindDelegate && st.Task == c.Task
	case c.Prompt != "":
		ok = st.Kind == claudelog.KindRequest && st.Text == c.Prompt
	default:
		ok = st.Kind == claudelog.KindTool && st.Tool == c.Tool && c.Tool != ""
	}
	return st, ok && !st.At.IsZero()
}

func originNote(ev claudelog.Evidence) string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("turnOrigin", ev.TurnOrigin)
	add("origin.kind", ev.OriginKind)
	add("promptSource", ev.PromptSource)
	if ev.IsMeta {
		parts = append(parts, "isMeta")
	}
	if len(ev.Markers) > 0 {
		parts = append(parts, "markers="+strings.Join(ev.Markers, "+"))
	}
	if len(parts) == 0 {
		return "no signal: draft user"
	}
	return strings.Join(parts, " ")
}

// replaceCopy writes a copy of the DB at src to dst over the copy an earlier run left (the source
// is never opened, see bench.CopyDB).
func replaceCopy(src, dst string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dst + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return bench.CopyDB(src, dst)
}

// insideGitTree reports the git working tree root that holds path ("" if none): it walks up
// from the nearest existing folder looking for .git, with no external command.
func insideGitTree(path string) string {
	d := path
	for {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
	if real, err := filepath.EvalSymlinks(d); err == nil {
		d = real
	}
	for {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
