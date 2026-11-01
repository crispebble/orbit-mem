package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/retro"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
)

// cmdRetroStore is the retrospectives' storage API for OpenOrbit's orb retro, which runs
// the retrospectives (orbit-mem never calls a model). Every op prints one JSON object on
// stdout; a failure exits 1 with one line on stderr. A failed retrospective is a result
// (finish prints it), not a failure of the command. No op creates the memory DB.
func cmdRetroStore(a *app, args []string) error {
	if len(args) == 0 {
		return storeUsage("")
	}
	op, rest := args[0], args[1:]
	if op == "queue" {
		if len(rest) == 0 {
			return storeUsage(op)
		}
		op, rest = "queue "+rest[0], rest[1:]
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
