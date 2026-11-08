package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// hostCreated is told of every host.Service this process makes. Tests use it to wait for the
// operation goroutines that outlive a timeout; it is nil in production.
var hostCreated func(*host.Service)

// hostService is the assembly point of the host contract port: it gives a host.Service the DB
// path, the settings, the upkeep (the daily copy and tidy, started behind a session's context),
// the launcher (a person's own session) and the parent session (ORBIT_PARENT). The port reads
// none of these from the environment itself.
func hostService() *host.Service {
	svc := host.New(host.Setup{
		DB:         paths().DB(),
		LoadConfig: loadConfig,
		Upkeep: host.UpkeepFunc(func(s *memory.Store, cfg config.Config) {
			autoBackup(cfg)
			autoTidy(cfg, s)
		}),
		Launcher: "human",
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
	in, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
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

// hostMessage is the words of an operation error. A tool's own error is told as it is; the rest
// are the catalog's.
func hostMessage(e *host.Error) string {
	switch e.Code {
	case host.CodeTool:
		return e.Message
	case host.CodeUsage:
		return i18n.T("cli.host.err.usage", e.Message)
	case host.CodeContract:
		return i18n.T("cli.host.err.contract", e.Message)
	case host.CodeTimeout:
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

// hostError prints an operation error as the contract's JSON and ends with its exit code.
func (a *app) hostError(e *host.Error) error {
	out := *e
	out.Message = hostMessage(e)
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
