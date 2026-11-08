package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	hooks "github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// hostCreated is told of every host.Service this process makes. Tests use it to wait for the
// operation goroutines that outlive a timeout; it is nil in production.
var hostCreated func(*host.Service)

// hostService is the assembly point of the host contract port: it gives a host.Service the DB
// path, the settings, the upkeep (the daily copy and tidy, started behind a session's context),
// the launcher and the parent session (ORBIT_PARENT). The port reads none of these from the
// environment itself.
//
// The `host` command and the old names (inject, tool) are a host that came through the contract:
// who started its session is not known, so its launcher is "host". Only a hook is a person's own
// session ("human", see hookService).
func hostService() *host.Service { return hostServiceWith(loadConfig, launcherHost) }

// The launchers the assembly points give (the activity log's launcher column).
const (
	launcherHost  = "host"  // 계약을 거친 호스트(host, inject, tool): 누가 띄웠는지는 모른다
	launcherHuman = "human" // 훅: 사람이 연 세션
)

// hostServiceWith is hostService with the settings from load (a hook gives the ones it read once).
// A hook starts its update check before it runs the operations, so the check still comes before
// the copy and the tidy, as the hooks always did.
func hostServiceWith(load func() (config.Config, error), launcher string) *host.Service {
	svc := host.New(host.Setup{
		DB:         paths().DB(),
		LoadConfig: load,
		Upkeep: host.UpkeepFunc(func(s *memory.Store, cfg config.Config) {
			autoBackup(cfg)
			autoTidy(cfg, s)
		}),
		Launcher: launcher,
		Parent:   os.Getenv(parentEnv),
	})
	if hostCreated != nil {
		hostCreated(svc)
	}
	return svc
}

// cmdHost runs one contract operation: one JSON request on stdin, one JSON result on stdout.
// Exit 0 is a result, 1 an operation error ({"error", "code"}), 2 a usage error.
func cmdHost(a *app, args []string) error {
	at := time.Now().UnixMilli() // 활동 시각: 입력을 읽기 전에 찍는다(훅과 같다)
	fs := a.flags("host")
	actor := fs.String("actor", "", i18n.T("flag.mcp.actor"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage
	}
	in, err := readRequest(a.stdin)
	if errors.Is(err, hooks.ErrInputWait) {
		return a.hostErrorText(&host.Error{Code: host.CodeTimeout}, inputWaitMessage())
	}
	if err != nil {
		return err
	}
	req, herr := host.Decode(in)
	if herr != nil {
		return a.hostError(herr)
	}
	svc := hostService()
	run := svc.Start(pos[0], req, host.Options{Actor: actorOf(*actor, memory.ActorAI), At: at})
	out, herr := run.Result()
	if herr != nil {
		err = a.hostError(herr)
	} else if err = a.json(out); err != nil {
		return err
	}
	run.Finish() // 결과를 낸 뒤 기록이 끝나기까지(한도 안에서) 기다린다
	return err
}

// readRequest reads a request from stdin: up to 1 MiB, and for no longer than a hook waits for its
// input (hooks.ReadInput, the same function). hooks.ErrInputWait is a request that never ended.
func readRequest(rd io.Reader) ([]byte, error) { return hooks.ReadInput(rd, 1<<20, hookInputWait) }

// hostMessage is the words of an operation error. A tool's own error is told as it is; the rest
// are the catalog's.
func hostMessage(e *host.Error) string {
	switch e.Code {
	case host.CodeTool:
		return e.Message
	case host.CodeUsage:
		return usageMessage(e.Detail)
	case host.CodeContract:
		if d := e.Detail; d != nil && d.Reason == host.DetailContract && len(d.Args) == 2 {
			return i18n.T("cli.host.err.contract", d.Args[0], d.Args[1])
		}
		return i18n.T("cli.host.err.contract_any")
	case host.CodeTimeout:
		if e.MayHaveApplied {
			return i18n.T("cli.host.err.timeout") + ". " + i18n.T("cli.host.err.may_have_applied")
		}
		return i18n.T("cli.host.err.timeout")
	case host.CodeNoDB:
		return i18n.T("cli.host.err.no_db")
	case host.CodeSchemaNewer:
		return i18n.T("cli.host.err.schema_newer")
	case host.CodeSchemaOlder:
		return i18n.T("cli.host.err.schema_older")
	case host.CodeUnknownTool:
		return i18n.T("cli.host.err.unknown_tool", e.Name)
	}
	return i18n.T("cli.host.err.internal", e.Message)
}

// usageMessage words a usage error from its reason and arguments (the port's own Message is not
// used: it is English).
func usageMessage(d *host.Detail) string {
	if d != nil {
		switch {
		case d.Reason == host.DetailInput:
			return i18n.T("cli.host.err.usage.input")
		case d.Reason == host.DetailUnknownOp && len(d.Args) == 1:
			return i18n.T("cli.host.err.usage.unknown_op", d.Args[0])
		case d.Reason == host.DetailBadValue && len(d.Args) == 2:
			return i18n.T("cli.host.err.usage.bad_value", d.Args[0], d.Args[1])
		case d.Reason == host.DetailMissing && len(d.Args) > 0:
			return i18n.T("cli.host.err.usage.missing", strings.Join(d.Args, ", "))
		}
	}
	return i18n.T("cli.host.err.usage")
}

// hostError prints an operation error as the contract's JSON and ends with its exit code.
func (a *app) hostError(e *host.Error) error { return a.hostErrorText(e, hostMessage(e)) }

// inputWaitMessage is the words of a request (stdin) that did not end in time: not an operation's
// time limit, so it is a timeout with no operation behind it.
func inputWaitMessage() string { return i18n.T("cli.host.err.input_wait") }

func (a *app) hostErrorText(e *host.Error, text string) error {
	out := *e
	out.Message = text
	if err := a.json(out); err != nil {
		return err
	}
	return exitCode(e.Exit())
}

// hostFailure is an operation error as a Go error, for the old names that print their own way.
func hostFailure(e *host.Error) error { return errors.New(strings.TrimSpace(hostMessage(e))) }

// hostDo runs one operation to its end for the older commands that print in their own way: the
// result, after the records have ended or the time limit has run out.
func hostDo(op string, req host.Request, o host.Options) (any, *host.Error) {
	return hostService().Do(op, req, o)
}
