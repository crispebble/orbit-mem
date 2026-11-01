package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/lab"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

func newLab(s *memory.Store) (*lab.Lab, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.PluginSource == "" {
		return nil, i18n.Errorf("cli.lab.err.no_source")
	}
	return &lab.Lab{Store: s, Cfg: cfg, Paths: paths(), Mem: selfPath(), Source: cfg.PluginSource}, nil
}

func readArg(text, file string) (string, error) {
	if file == "" {
		return text, nil
	}
	b, err := os.ReadFile(file)
	return string(b), err
}

// cmdLab is the harness lab (명세 · Orbit 3차 O3-5).
func cmdLab(a *app, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	sub := args[0]
	if sub == "task" {
		if len(args) < 2 {
			return errUsage
		}
		sub = "task " + args[1]
		args = args[1:]
	}
	fs := a.flags("lab " + sub)
	title := fs.String("title", "", i18n.T("flag.lab.title"))
	instruction := fs.String("instruction", "", i18n.T("flag.lab.instruction"))
	instructionFile := fs.String("instruction-file", "", i18n.T("flag.lab.instruction_file"))
	criteria := fs.String("criteria", "", i18n.T("flag.lab.criteria"))
	criteriaFile := fs.String("criteria-file", "", i18n.T("flag.lab.criteria_file"))
	start := fs.String("start", "", i18n.T("flag.lab.start"))
	corrections := fs.String("from-corrections", "", i18n.T("flag.lab.from_corrections"))
	task := fs.String("task", "", i18n.T("flag.lab.task"))
	change := fs.String("change", "", i18n.T("flag.lab.change"))
	proposal := fs.String("proposal", "", i18n.T("flag.lab.proposal"))
	note := fs.String("note", "", i18n.T("flag.lab.note"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	rest, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	l, err := newLab(s)
	if err != nil {
		return err
	}
	switch sub {
	case "task new":
		in := lab.TaskInput{Title: *title, Start: *start}
		if in.Instruction, err = readArg(*instruction, *instructionFile); err != nil {
			return err
		}
		if in.Criteria, err = readArg(*criteria, *criteriaFile); err != nil {
			return err
		}
		for _, f := range strings.Split(*corrections, ",") {
			if f = strings.TrimSpace(f); f != "" {
				id, err := strconv.ParseInt(f, 10, 64)
				if err != nil {
					return i18n.Errorf("cli.lab.err.correction_id", f)
				}
				in.Corrections = append(in.Corrections, id)
			}
		}
		t, err := l.NewTask(in)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(t)
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.lab.task_created", t.ID, t.Title))
	case "task list":
		ts, err := l.Tasks()
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(ts)
		}
		for _, t := range ts {
			fmt.Fprintln(a.stdout, i18n.T("cli.lab.task_line", t.ID, t.Title, len(t.Sources)))
		}
	case "candidates":
		ps, err := s.Proposals(memory.StatusLab)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(ps)
		}
		for _, p := range ps {
			fmt.Fprintf(a.stdout, "%s %s · %s\n", p.ID, p.Title, p.Payload.Path)
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.lab.candidates_tail", len(ps)))
	case "drop":
		if len(rest) != 1 {
			return errUsage
		}
		reason := "사용자가 실험 없이 닫음" // i18n:keep 제안에 남는 기록(회고가 읽는다)
		if strings.TrimSpace(*note) != "" {
			reason += ": " + strings.TrimSpace(*note)
		}
		if err := s.CloseLabProposal(rest[0], reason); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.lab.dropped", rest[0]))
	case "run":
		ctx, cancel := signalContext()
		defer cancel()
		fmt.Fprintln(a.stdout, i18n.T("cli.lab.run_start", l.Cfg.LabRuns, l.Cfg.LabModel, l.Cfg.LabJudgeModel, l.Cfg.LabRunUSD, l.Cfg.LabExperimentUSD))
		x, err := l.Run(ctx, lab.RunInput{TaskID: *task, ChangeDir: *change, ProposalID: *proposal})
		if x.ID != "" {
			if *asJSON {
				_ = a.json(x)
			} else {
				fmt.Fprintln(a.stdout, i18n.T("cli.lab.run_done", x.ID, x.Status, x.Verdict, x.CostUSD, x.LedgerID, strings.Join(x.ProposalIDs, ", ")))
			}
		}
		return err
	case "show":
		if len(rest) != 1 {
			return errUsage
		}
		if *asJSON {
			x, err := s.LabExperiment(rest[0])
			if err != nil {
				return err
			}
			return a.json(x)
		}
		out, err := l.Show(rest[0])
		if err != nil {
			return err
		}
		fmt.Fprint(a.stdout, out)
	case "list":
		xs, err := s.LabExperiments(0, 0)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(xs)
		}
		for _, x := range xs {
			fmt.Fprintln(a.stdout, i18n.T("cli.lab.list_line", x.ID, x.TaskID, x.Status, x.Verdict, x.CostUSD, len(x.Picks)))
		}
	default:
		return errUsage
	}
	return nil
}
