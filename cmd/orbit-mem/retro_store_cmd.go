package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/retro"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
)

// cmdRetroStore is the retrospectives' storage API for orbit-code's orb retro, which runs
// the retrospectives (orbit-mem never calls a model). Every op prints one JSON object on
// stdout; a failure exits 1 with one line on stderr. A failed retrospective is a result
// (finish prints it), not a failure of the command. No op creates the memory DB.
func cmdRetroStore(a *app, args []string) error {
	if len(args) == 0 {
		return storeUsage("")
	}
	op, rest := args[0], args[1:]
	if op == "queue" || op == "experiment" {
		if len(rest) == 0 {
			return storeUsage(op)
		}
		op, rest = op+" "+rest[0], rest[1:]
	}
	run, ok := storeOps[op]
	if !ok {
		return storeUsage(op)
	}
	fs := flag.NewFlagSet("retro-store "+op, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	get := run(fs)
	if pos, err := parse(fs, rest); err != nil || len(pos) != 0 {
		return storeUsage(op)
	}
	if _, err := os.Stat(paths().DB()); err != nil { // 설치 전: 기억 DB를 만들지 않는다
		return oneLine1(i18n.Err("cli.doctor.problem.db-missing"))
	}
	cfg, err := loadConfig()
	if err != nil {
		return oneLine1(err)
	}
	s, err := openStore()
	if err != nil {
		return oneLine1(err)
	}
	defer s.Close()
	k := &retro.Keeper{Store: s, Cfg: cfg, Paths: paths(), Roots: transcript.DefaultRoots(), Loc: time.Local}
	v, err := get(a, k, cfg)
	if err != nil {
		return oneLine1(err)
	}
	enc := json.NewEncoder(a.stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// storeOp registers its flags on fs and returns the op to run once they are parsed.
type storeOp func(fs *flag.FlagSet) func(a *app, k *retro.Keeper, cfg config.Config) (any, error)

var storeOps = map[string]storeOp{
	"prepare":    storePrepare,
	"finish":     storeFinish,
	"queue list": storeQueueList,
	"queue add":  storeQueueAdd,
	"queue mark": storeQueueMark,
	"state":      storeState,
	"list":       storeList,
	"sessions":   storeSessions,
	"backup":     storeBackup,
	"judge":      storeJudge,
	"settle":     storeSettle,
	// AU-7: orbit-code가 작업 지표를 넣고, 작업별 교정 수를 읽는다(모델을 부르지 않는다).
	"work-metrics": storeWorkMetrics,
	"work-signals": storeWorkSignals,
	// ONID-124: 하네스 실험실(orbit-code 개발 도구)이 교정·대기 제안을 읽고 실험 기록을 넣는다.
	"experiment corrections": storeExperimentCorrections,
	"experiment candidates":  storeExperimentCandidates,
	"experiment drop":        storeExperimentDrop,
	"experiment save":        storeExperimentSave,
	"experiment list":        storeExperimentList,
	"experiment get":         storeExperimentGet,
}

// storeError is a retro-store failure: exit 1 with one stderr line.
type storeError struct{ msg string }

func (e storeError) Error() string { return e.msg }

func oneLine1(err error) error {
	return storeError{strings.Join(strings.Fields(err.Error()), " ")}
}

func storeUsage(op string) error {
	return storeError{i18n.T("cli.retro_store.usage", op)}
}

func hostName(h string) bool { return h == "claude" || h == "codex" }

func storePrepare(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	kind := fs.String("kind", "", "")
	period := fs.String("period", "", "")
	host := fs.String("host", "", "")
	sessionHost := fs.String("session-host", "", "")
	session := fs.String("session", "", "")
	tr := fs.String("transcript", "", "")
	cwd := fs.String("cwd", "", "")
	endedAt := fs.Int64("ended-at", 0, "")
	dailyMax := fs.Int("daily-max", 0, "")
	return func(a *app, k *retro.Keeper, cfg config.Config) (any, error) {
		h := *host
		if h == "" {
			h = cfg.RetroHost
		}
		if !hostName(h) {
			return nil, storeUsage("prepare")
		}
		switch *kind {
		case retro.KindWork:
			in, err := readWork(a.stdin)
			if err != nil {
				return nil, err
			}
			return k.PrepareWork(h, in)
		case retro.KindTidy:
			return k.PrepareTidy(h)
		case retro.KindLegacy:
			return k.PrepareLegacy(h)
		case retro.KindSession:
			if !hostName(*sessionHost) || strings.TrimSpace(*session) == "" || *endedAt <= 0 {
				return nil, storeUsage("prepare")
			}
			return k.PrepareSession(h, retro.SessionEnd{Host: *sessionHost, Session: *session, Transcript: *tr, Cwd: *cwd, EndedAt: *endedAt}, *dailyMax)
		}
		return k.Prepare(*kind, *period, h)
	}
}

// maxWorkInput is how much of a work record prepare --kind work reads from stdin.
const maxWorkInput = 4 << 20

// readWork reads the one work record JSON object of prepare --kind work.
func readWork(r io.Reader) (retro.WorkIn, error) {
	var in retro.WorkIn
	dec := json.NewDecoder(io.LimitReader(r, maxWorkInput))
	if err := dec.Decode(&in); err != nil {
		return in, i18n.Errorf("cli.retro_store.err.work_stdin", err)
	}
	if dec.More() {
		return in, i18n.Errorf("cli.retro_store.err.work_stdin", i18n.T("cli.retro_store.err.one_object"))
	}
	if err := in.Validate(); err != nil {
		return in, storeError{i18n.T("cli.retro_store.usage", "prepare") + " — " + err.Error()}
	}
	return in, nil
}

func storeFinish(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	id := fs.String("id", "", "")
	return func(a *app, k *retro.Keeper, _ config.Config) (any, error) {
		if *id == "" {
			return nil, storeUsage("finish")
		}
		var h retro.HostResult
		dec := json.NewDecoder(io.LimitReader(a.stdin, 16<<20))
		if err := dec.Decode(&h); err != nil {
			return nil, i18n.Errorf("cli.retro_store.err.stdin", err)
		}
		return k.Finish(*id, h)
	}
}

func storeQueueList(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		items, err := retro.Pending(k.Store)
		if items == nil {
			items = []retro.SessionEnd{}
		}
		return map[string]any{"items": items}, err
	}
}

// queueEntry is a queue entry's flags.
func queueEntry(fs *flag.FlagSet) (*string, *string, *int64) {
	return fs.String("host", "", ""), fs.String("session", "", ""), fs.Int64("ended-at", 0, "")
}

func storeQueueAdd(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	host, session, endedAt := queueEntry(fs)
	tr := fs.String("transcript", "", "")
	cwd := fs.String("cwd", "", "")
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		if !hostName(*host) || strings.TrimSpace(*session) == "" || *endedAt < 0 {
			return nil, storeUsage("queue add")
		}
		queued, err := retro.Enqueue(k.Store, k.Paths, retro.SessionEnd{Host: *host, Session: *session, Transcript: *tr, Cwd: *cwd, EndedAt: *endedAt})
		return map[string]any{"queued": queued}, err
	}
}

func storeQueueMark(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	host, session, endedAt := queueEntry(fs)
	as := fs.String("as", "", "")
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		ok := *as == retro.StatusEmpty || *as == retro.StatusCapped || *as == retro.StatusSkipped || strings.HasPrefix(*as, "r-")
		if !hostName(*host) || *session == "" || *endedAt <= 0 || !ok {
			return nil, storeUsage("queue mark")
		}
		err := retro.MarkEnd(k.Store, retro.SessionEnd{Host: *host, Session: *session, EndedAt: *endedAt}, *as)
		return map[string]any{"ok": err == nil}, err
	}
}

func storeState(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) { return k.State() }
}

func storeList(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	n := fs.Int("n", 10, "")
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		list, err := retro.Recent(k.Store, max(*n, 1))
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []retro.RetroStatus{}
		}
		queue, err := retro.QueueCount(k.Store)
		return map[string]any{"retros": list, "pendingProposals": k.Store.PendingCount(), "queue": queue}, err
	}
}

func storeSessions(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	day := fs.String("day", "", "")
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		if *day == "" {
			return nil, storeUsage("sessions")
		}
		sessions, summaries, err := k.Sessions(*day)
		return map[string]any{"sessions": sessions, "codexSummaries": summaries}, err
	}
}

func storeBackup(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		mem, work, errs, err := k.Backup()
		return map[string]any{"memory": mem, "work": work, "errors": errs}, err
	}
}

func storeJudge(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		return map[string]any{"errors": k.Judge()}, nil
	}
}

func storeSettle(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		return map[string]any{"errors": k.Settle()}, nil
	}
}

// maxWorkMetricsInput is how much of the work-metrics and work-signals body is read.
const maxWorkMetricsInput = 8 << 20

// readWorks reads {"works":[…]} from stdin.
func readWorks[T any](r io.Reader) ([]T, error) {
	var in struct {
		Works []T `json:"works"`
	}
	dec := json.NewDecoder(io.LimitReader(r, maxWorkMetricsInput))
	if err := dec.Decode(&in); err != nil {
		return nil, i18n.Errorf("cli.retro_store.err.stdin", err)
	}
	return in.Works, nil
}

// storeWorkMetrics keeps the works' metrics orbit-code computed (사건 work.metrics).
func storeWorkMetrics(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(a *app, k *retro.Keeper, _ config.Config) (any, error) {
		works, err := readWorks[memory.WorkRecord](a.stdin)
		if err != nil {
			return nil, err
		}
		n, err := k.Store.RecordWorkMetrics(works)
		if err != nil {
			return nil, err
		}
		return map[string]any{"recorded": n}, nil
	}
}

// storeWorkSignals counts each work's user corrections (작업 id 세션과 작업 세션 창 안).
func storeWorkSignals(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(a *app, k *retro.Keeper, _ config.Config) (any, error) {
		refs, err := readWorks[memory.WorkRef](a.stdin)
		if err != nil {
			return nil, err
		}
		counts, err := k.Store.WorkCorrections(refs)
		if err != nil {
			return nil, err
		}
		out := make(map[string]any, len(counts))
		for id, n := range counts {
			out[id] = map[string]int{"corrections": n}
		}
		return map[string]any{"works": out}, nil
	}
}

// storeExperimentCorrections reads the user's words of correction events for a lab task.
func storeExperimentCorrections(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	ids := fs.String("ids", "", "")
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		var list []int64
		for _, f := range strings.Split(*ids, ",") {
			if f = strings.TrimSpace(f); f == "" {
				continue
			}
			id, err := strconv.ParseInt(f, 10, 64)
			if err != nil {
				return nil, storeUsage("experiment corrections")
			}
			list = append(list, id)
		}
		if len(list) == 0 {
			return nil, storeUsage("experiment corrections")
		}
		return k.Store.LabCorrections(list)
	}
}

// storeExperimentCandidates lists the file proposals waiting for the lab.
func storeExperimentCandidates(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		ps, err := k.Store.Proposals(memory.StatusLab)
		if ps == nil {
			ps = []memory.Proposal{}
		}
		return ps, err
	}
}

// storeExperimentDrop closes a proposal waiting for the lab without an experiment.
func storeExperimentDrop(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	id := fs.String("id", "", "")
	note := fs.String("note", "", "")
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		if *id == "" {
			return nil, storeUsage("experiment drop")
		}
		reason := "사용자가 실험 없이 닫음" // i18n:keep 제안에 남는 기록(회고가 읽는다)
		if n := strings.TrimSpace(*note); n != "" {
			reason += ": " + n
		}
		if err := k.Store.CloseLabProposal(*id, reason); err != nil {
			return nil, err
		}
		return map[string]any{"dropped": *id}, nil
	}
}

// storeExperimentSave records an experiment (memory.LabSave on stdin) and prints it as stored.
// File proposals are kept only for files inside this PC's plugin source (pluginSource).
func storeExperimentSave(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(a *app, k *retro.Keeper, cfg config.Config) (any, error) {
		var in memory.LabSave
		dec := json.NewDecoder(io.LimitReader(a.stdin, maxWorkMetricsInput))
		if err := dec.Decode(&in); err != nil {
			return nil, i18n.Errorf("cli.retro_store.err.stdin", err)
		}
		return k.Store.SaveLab(in, []string{cfg.PluginSource})
	}
}

// storeExperimentList lists the experiments, newest first.
func storeExperimentList(*flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		xs, err := k.Store.LabExperiments(0, 0)
		if xs == nil {
			xs = []memory.LabExperiment{}
		}
		return xs, err
	}
}

// storeExperimentGet reads one experiment and its runs.
func storeExperimentGet(fs *flag.FlagSet) func(*app, *retro.Keeper, config.Config) (any, error) {
	id := fs.String("id", "", "")
	return func(_ *app, k *retro.Keeper, _ config.Config) (any, error) {
		if *id == "" {
			return nil, storeUsage("experiment get")
		}
		x, err := k.Store.LabExperiment(*id)
		if err != nil {
			return nil, err
		}
		runs, err := k.Store.LabRuns(*id)
		if runs == nil {
			runs = []memory.LabRun{}
		}
		return map[string]any{"experiment": x, "runs": runs}, err
	}
}
