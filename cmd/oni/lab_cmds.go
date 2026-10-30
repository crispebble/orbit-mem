package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onidot-labs/orbit/internal/lab"
	"github.com/onidot-labs/orbit/internal/memory"
)

func newLab(s *memory.Store) (*lab.Lab, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.PluginSource == "" {
		return nil, errors.New("실험실은 플러그인 원본을 쓰는 PC에서 돕니다(oni install --plugin-source <원본>)")
	}
	return &lab.Lab{Store: s, Cfg: cfg, Paths: paths(), Oni: selfPath(), Source: cfg.PluginSource}, nil
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
	title := fs.String("title", "", "과제 제목")
	instruction := fs.String("instruction", "", "작업 지시")
	instructionFile := fs.String("instruction-file", "", "작업 지시 파일")
	criteria := fs.String("criteria", "", "판정 기준")
	criteriaFile := fs.String("criteria-file", "", "판정 기준 파일")
	start := fs.String("start", "", "시작 상태 폴더")
	corrections := fs.String("from-corrections", "", "교정 사건 id(쉼표로 구분)")
	task := fs.String("task", "", "과제 id")
	change := fs.String("change", "", "바꿀 파일을 원본과 같은 상대 경로로 둔 폴더")
	proposal := fs.String("proposal", "", "실험실 대기 중인 파일 반영 제안 id")
	note := fs.String("note", "", "닫는 이유")
	asJSON := fs.Bool("json", false, "JSON")
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
					return fmt.Errorf("교정 사건 id는 숫자입니다: %q", f)
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
		fmt.Fprintf(a.stdout, "과제를 만들었습니다: %s 「%s」\n", t.ID, t.Title)
	case "task list":
		ts, err := l.Tasks()
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(ts)
		}
		for _, t := range ts {
			fmt.Fprintf(a.stdout, "%s 「%s」 출처 교정 %d건\n", t.ID, t.Title, len(t.Sources))
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
		fmt.Fprintf(a.stdout, "실험실 대기 %d건. 실험: oni lab run --task <과제> --proposal <id>\n", len(ps))
	case "drop":
		if len(rest) != 1 {
			return errUsage
		}
		reason := "사용자가 실험 없이 닫음"
		if strings.TrimSpace(*note) != "" {
			reason += ": " + strings.TrimSpace(*note)
		}
		if err := s.CloseLabProposal(rest[0], reason); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "실험실 대기 제안 %s를 닫았습니다.\n", rest[0])
	case "run":
		ctx, cancel := signalContext()
		defer cancel()
		fmt.Fprintf(a.stdout, "실험을 시작합니다: 후보마다 %d회, 실행 %s · 고르기 %s, 상한 실행 $%.2f · 실험 $%.2f\n",
			l.Cfg.LabRuns, l.Cfg.LabModel, l.Cfg.LabJudgeModel, l.Cfg.LabRunUSD, l.Cfg.LabExperimentUSD)
		x, err := l.Run(ctx, lab.RunInput{TaskID: *task, ChangeDir: *change, ProposalID: *proposal})
		if x.ID != "" {
			if *asJSON {
				_ = a.json(x)
			} else {
				fmt.Fprintf(a.stdout, "실험 %s: %s %s · 비용 $%.4f · 원장 %s · 제안 %s\n", x.ID, x.Status, x.Verdict, x.CostUSD, x.LedgerID, strings.Join(x.ProposalIDs, ", "))
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
			fmt.Fprintf(a.stdout, "%s 과제 %s · %s %s · 비용 $%.4f · 고른 회차 %d\n", x.ID, x.TaskID, x.Status, x.Verdict, x.CostUSD, len(x.Picks))
		}
	default:
		return errUsage
	}
	return nil
}
