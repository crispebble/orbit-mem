package main

// The thin commands (설계 S6): each calls the same function as the MCP tool an AI host uses
// (mcpserver.Tools), and prints a table or text on a terminal, JSON for a pipe or with --json.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// tools opens the store and gives the AI host tools for this directory's project.
func tools() (mcpserver.Tools, *memory.Store, error) {
	s, err := openStore()
	if err != nil {
		return mcpserver.Tools{}, nil, err
	}
	p := sessionProject()
	return mcpserver.NewTools(s, mcpserver.Options{Project: p.Scope, ProjectLabel: p.Label, Dir: p.Dir, Git: p.Git, Temp: p.Temp, Actor: actorOf("", memory.ActorUser), TrustLimit: humanTrustLimit, Products: productsOf()}), s, nil
}

func (a *app) printSummaries(items []mcpserver.FindItem) {
	for _, m := range items {
		fmt.Fprintf(a.stdout, "%s  %s·%s  %s\n", m.ID, kindName(m.Kind), stateName(m.State), m.Title)
	}
	if len(items) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.memory.none_found"))
	}
}

func cmdSearch(a *app, args []string) error {
	fs := a.flags("search")
	var kinds multiFlag
	fs.Var(&kinds, "kind", i18n.T("flag.memory.kind"))
	all := fs.Bool("all", false, i18n.T("flag.memory.all"))
	stale := fs.Bool("stale", false, i18n.T("flag.memory.stale"))
	limit := fs.Int("n", 20, i18n.T("flag.max"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	in := mcpserver.FindIn{Query: strings.Join(pos, " "), Kinds: kinds, Limit: *limit}
	if *all {
		in.Scope = "all"
	}
	if *stale {
		in.States = []string{memory.StateCandidate, memory.StateConfirmed, memory.StateStale}
	}
	return a.find(in, *asJSON)
}

func cmdList(a *app, args []string) error {
	fs := a.flags("list")
	var kinds, states, tags multiFlag
	fs.Var(&kinds, "kind", i18n.T("flag.memory.kind"))
	fs.Var(&states, "state", i18n.T("flag.memory.state"))
	fs.Var(&tags, "tag", i18n.T("flag.memory.tag"))
	all := fs.Bool("all", false, i18n.T("flag.memory.all"))
	limit := fs.Int("n", 20, i18n.T("flag.max"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return errUsage
	}
	in := mcpserver.FindIn{Kinds: kinds, States: states, Tags: tags, Limit: *limit} // 질의가 없으면 최근 것부터
	if *all {
		in.Scope = "all"
	}
	return a.find(in, *asJSON)
}

func (a *app) find(in mcpserver.FindIn, asJSON bool) error {
	t, s, err := tools()
	if err != nil {
		return err
	}
	defer s.Close()
	out, err := t.Find(in)
	if err != nil {
		return err
	}
	if a.wantJSON(asJSON) {
		return a.json(out)
	}
	a.printSummaries(out.Items)
	return nil
}

func cmdShow(a *app, args []string) error {
	fs := a.flags("show")
	history := fs.Bool("history", false, i18n.T("flag.memory.history"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage
	}
	t, s, err := tools()
	if err != nil {
		return err
	}
	defer s.Close()
	out, err := t.Get(mcpserver.GetIn{IDs: pos, History: *history, Full: true})
	if err != nil {
		return err
	}
	if a.wantJSON(*asJSON) {
		return a.json(out)
	}
	m := out.Items[0]
	fmt.Fprintf(a.stdout, "%s · %s · %s · %s · v%d\n%s\n\n%s\n", m.ID, kindName(m.Kind), stateName(m.State), m.AppliesTo, m.Version, m.Title, m.Body)
	if len(m.Tags) > 0 {
		fmt.Fprintln(a.stdout, "\n"+i18n.T("cli.memory.tags", strings.Join(m.Tags, ", ")))
	}
	for _, src := range m.Sources {
		fmt.Fprintln(a.stdout, i18n.T("cli.memory.source", src.Kind, src.Ref, oneLine(src.Excerpt, 80)))
	}
	for _, l := range m.Links {
		fmt.Fprintln(a.stdout, i18n.T("cli.memory.link", l))
	}
	for _, v := range out.History[m.ID] {
		fmt.Fprintf(a.stdout, "v%d %s %s %s\n", v.Version, time.UnixMilli(v.At).Format("2006-01-02 15:04"), v.Action, v.Reason)
	}
	return nil
}

func (a *app) printUpdated(out mcpserver.MemoryOut, asJSON bool) error {
	if a.wantJSON(asJSON) {
		return a.json(out)
	}
	m := out.Memory
	fmt.Fprintln(a.stdout, i18n.T("cli.memory.updated", m.ID, stateName(m.State), m.Version, m.Title))
	return nil
}

func cmdEdit(a *app, args []string) error {
	fs := a.flags("edit")
	title := fs.String("title", "", i18n.T("flag.edit.title"))
	body := fs.String("body", "", i18n.T("flag.edit.body"))
	bodyFile := fs.String("body-file", "", i18n.T("flag.edit.body_file"))
	var tags, links, unlinks multiFlag
	fs.Var(&tags, "tag", i18n.T("flag.edit.tag"))
	fs.Var(&links, "link", i18n.T("flag.edit.link"))
	fs.Var(&unlinks, "unlink", i18n.T("flag.edit.unlink"))
	note := fs.String("note", "", i18n.T("flag.edit.note"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if len(pos) != 1 || (set["body"] && set["body-file"]) {
		return errUsage
	}
	in := mcpserver.UpdateIn{ID: pos[0], Reason: *note}
	if set["title"] {
		in.Title = title
	}
	switch {
	case set["body"]:
		in.Body = body
	case set["body-file"]:
		b, err := os.ReadFile(*bodyFile)
		if err != nil {
			return err
		}
		text := string(b)
		in.Body = &text
	}
	if set["tag"] {
		t := []string(tags)
		in.Tags = &t
	}
	for _, id := range links {
		in.Link = append(in.Link, mcpserver.LinkIn{ID: id, Note: *note})
	}
	for _, id := range unlinks {
		in.Unlink = append(in.Unlink, mcpserver.UnlinkIn{ID: id, Reason: *note})
	}
	if in.Title == nil && in.Body == nil && in.Tags == nil && len(in.Link) == 0 && len(in.Unlink) == 0 {
		return errUsage
	}
	t, s, err := tools()
	if err != nil {
		return err
	}
	defer s.Close()
	out, err := t.Update(in)
	if err != nil {
		return err
	}
	return a.printUpdated(out, *asJSON)
}

func cmdStale(a *app, args []string) error {
	fs := a.flags("stale")
	note := fs.String("note", "", i18n.T("flag.edit.note"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage
	}
	t, s, err := tools()
	if err != nil {
		return err
	}
	defer s.Close()
	out, err := t.Update(mcpserver.UpdateIn{ID: pos[0], Action: "stale", Reason: *note})
	if err != nil {
		return err
	}
	return a.printUpdated(out, *asJSON)
}

func cmdExport(a *app, args []string) error {
	fs := a.flags("export")
	format := fs.String("format", "jsonl", "jsonl | md")
	out := fs.String("out", "", i18n.T("flag.export.out"))
	all := fs.Bool("all", false, i18n.T("flag.export.all"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 || (*format != "jsonl" && *format != "md") {
		return errUsage
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	var states []string
	if *all {
		states = memory.States
	}
	ms, err := s.Export(states...)
	if err != nil {
		return err
	}
	var where string
	switch *format {
	case "jsonl":
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		for _, m := range ms {
			if err := enc.Encode(m); err != nil {
				return err
			}
		}
		if *out == "" { // stdout이 기본이다(파이프로 넘긴다)
			_, err := io.WriteString(a.stdout, b.String())
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(*out, []byte(b.String()), 0o600); err != nil {
			return err
		}
		where = *out
	case "md":
		where = *out
		if where == "" {
			where = "orbit-mem-export-" + time.Now().Format("20060102")
		}
		if err := exportMarkdown(where, ms); err != nil {
			return err
		}
	}
	if a.wantJSON(*asJSON) {
		return a.json(map[string]any{"format": *format, "out": where, "count": len(ms)})
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.export.done", len(ms), where))
	return nil
}

// exportMarkdown writes one <id>.md per memory and an index.md into dir.
func exportMarkdown(dir string, ms []memory.Memory) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var index strings.Builder
	index.WriteString("# orbit-mem\n\n")
	for _, m := range ms {
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", m.Title)
		fmt.Fprintf(&b, "- id: %s\n- kind: %s\n- state: %s\n- scope: %s\n", m.ID, m.Kind, m.State, m.Scope)
		if m.Project != "" {
			fmt.Fprintf(&b, "- project: %s\n", m.Project)
		}
		fmt.Fprintf(&b, "- version: %d\n- created: %s\n- updated: %s\n", m.Version, time.UnixMilli(m.CreatedAt).Format(time.RFC3339), time.UnixMilli(m.UpdatedAt).Format(time.RFC3339))
		if len(m.Tags) > 0 {
			fmt.Fprintf(&b, "- tags: %s\n", strings.Join(m.Tags, ", "))
		}
		if m.SupersededBy != "" {
			fmt.Fprintf(&b, "- superseded by: [%s](%s.md)\n", m.SupersededBy, m.SupersededBy)
		}
		if m.Body != "" {
			b.WriteString("\n" + strings.TrimRight(m.Body, "\n") + "\n")
		}
		if len(m.Sources) > 0 {
			b.WriteString("\n## Sources\n\n")
			for _, src := range m.Sources {
				fmt.Fprintf(&b, "- %s %s %s\n", src.Kind, src.Ref, oneLine(src.Excerpt, 200))
			}
		}
		if len(m.Links) > 0 {
			b.WriteString("\n## Links\n\n")
			for _, l := range m.Links {
				fmt.Fprintf(&b, "- [%s](%s.md) %s\n", l.MemoryID, l.MemoryID, l.Note)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(m.ID)+".md"), []byte(b.String()), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(&index, "- [%s](%s.md) · %s · %s\n", m.Title, m.ID, m.Kind, m.State)
	}
	return os.WriteFile(filepath.Join(dir, "index.md"), []byte(index.String()), 0o600)
}

type importSkip struct {
	ID     string `json:"id,omitempty"`
	Line   int    `json:"line"`
	Reason string `json:"reason"` // exists | duplicate | invalid
}

type importReport struct {
	DryRun   bool         `json:"dryRun"`
	Imported []string     `json:"imported"`
	Skipped  []importSkip `json:"skipped"`
	Linked   int          `json:"linked"`
	Snapshot string       `json:"snapshot,omitempty"`
}

// cmdImport adds the memories of an exported jsonl: a new id is added as it was (history
// "import"), an id that is there, a same-titled memory or a broken line is skipped and named.
// Links between memories that are both there afterwards are added too.
func cmdImport(a *app, args []string) error {
	fs := a.flags("import")
	dry := fs.Bool("dry-run", false, i18n.T("flag.import.dry_run"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage
	}
	f, err := os.Open(pos[0])
	if err != nil {
		return err
	}
	defer f.Close()
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	rep := importReport{DryRun: *dry, Imported: []string{}, Skipped: []importSkip{}}
	if !*dry {
		if rep.Snapshot, err = s.Snapshot(paths().Backups(), "before-import"); err != nil {
			return err
		}
	}
	reason := "import: " + filepath.Base(pos[0])
	var added []memory.Memory
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var m memory.Memory
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			rep.Skipped = append(rep.Skipped, importSkip{Line: line, Reason: memory.ImportInvalid})
			continue
		}
		res, err := s.Import(m, reason, *dry)
		if err != nil {
			return err
		}
		if res != memory.ImportDone {
			rep.Skipped = append(rep.Skipped, importSkip{ID: m.ID, Line: line, Reason: res})
			continue
		}
		rep.Imported = append(rep.Imported, m.ID)
		added = append(added, m)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for _, m := range added {
		if *dry {
			break
		}
		for _, l := range m.Links {
			if !s.Exists(l.MemoryID) {
				continue
			}
			if _, made, err := s.Link(memory.LinkInput{From: m.ID, To: l.MemoryID, Kind: l.Kind, Note: l.Note, Origin: l.Origin, OriginRef: reason}); err == nil && made {
				rep.Linked++
			}
		}
	}
	if a.wantJSON(*asJSON) {
		return a.json(rep)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.import.done", len(rep.Imported), len(rep.Skipped), rep.Linked))
	for _, sk := range rep.Skipped {
		fmt.Fprintln(a.stdout, i18n.T("cli.import.skipped", sk.Line, sk.ID, sk.Reason))
	}
	if *dry {
		fmt.Fprintln(a.stdout, i18n.T("cli.import.dry_run"))
	}
	return nil
}

func cmdActivity(a *app, args []string) error {
	fs := a.flags("activity")
	host := fs.String("host", "", i18n.T("flag.activity.host"))
	project := fs.String("project", "", i18n.T("flag.activity.project"))
	since := fs.String("since", "", i18n.T("flag.activity.since"))
	limit := fs.Int("n", 20, i18n.T("flag.max"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	t, s, err := tools()
	if err != nil {
		return err
	}
	defer s.Close()
	out, err := t.Activity(mcpserver.ActivityIn{Query: strings.Join(pos, " "), Host: *host, Project: *project, Since: *since, Limit: *limit})
	if err != nil {
		return err
	}
	if a.wantJSON(*asJSON) {
		return a.json(out)
	}
	for _, r := range out.Items {
		fmt.Fprintf(a.stdout, "%s  %-6s %-8s %s  %s  %s\n", time.UnixMilli(r.LastAt).Format("2006-01-02 15:04"), r.Host, r.Launcher, r.Session,
			oneLine(r.Project, 40), oneLine(r.FirstPrompt, 80))
	}
	if len(out.Items) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.activity.none"))
	}
	return nil
}
