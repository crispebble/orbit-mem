package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	}
	return errUsage
}

func benchRecall(a *app, args []string) error {
	fs := a.flags("bench recall")
	cases := fs.String("cases", "", i18n.T("flag.bench.cases"))
	db := fs.String("db", "", i18n.T("flag.bench.db"))
	recalls := fs.String("recall", "current,legacy", i18n.T("flag.bench.recall"))
	split := fs.String("split", "all", i18n.T("flag.bench.split"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	report := fs.String("report", "", i18n.T("flag.bench.report"))
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
	Expect  []string `json:"expect"`
	Note    string   `json:"note,omitempty"`
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
	projects := fs.String("projects", claudelog.DefaultRoot(), i18n.T("flag.bench.projects"))
	db := fs.String("db", paths().DB(), i18n.T("flag.bench.db_extract"))
	out := fs.String("out", filepath.Join(paths().Home, "bench", "replay"), i18n.T("flag.bench.out"))
	minSessions := fs.Int("min-sessions", 0, i18n.T("flag.bench.min_sessions"))
	minRequests := fs.Int("min-requests", 0, i18n.T("flag.bench.min_requests"))
	pos, err := parse(fs, args)
	if err != nil {
		return benchFlagErr(err)
	}
	if len(pos) != 0 {
		return errUsage
	}
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
	sessions, err := claudelog.Select([]string{*projects}, claudelog.Options{MinSessions: *minSessions, MinRequests: *minRequests})
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
	var reqs, subs, tools int
	for _, s := range sessions {
		n := 0
		for _, st := range s.Steps {
			n++
			c := benchCase{ID: fmt.Sprintf("%s-%03d", s.ID, n), Session: s.ID, Kind: bench.KindRequest, Expect: []string{}}
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
				c.Tool, c.Note = st.Tool, "tool call"
				tools++
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
	fmt.Fprintln(a.stdout, i18n.T("cli.bench.extract_done", outAbs, len(sessions), reqs, subs, tools))
	return nil
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
