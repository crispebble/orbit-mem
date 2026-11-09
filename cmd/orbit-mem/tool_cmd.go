package main

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	hooks "github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// cmdTool runs one memory tool for a host without an MCP client (--host names it, required):
// the MCP input as JSON on stdin, the MCP output as JSON on stdout. A tool
// error is {"error": "..."} with exit 1. The writer is the host's AI unless
// --actor says otherwise, and the call is recorded as that host's tool use.
// It is the older name of the host contract's call operation, with the output it always had
// (the tool's own result, not wrapped in {name, result}).
func cmdTool(a *app, args []string) error {
	at := time.Now().UnixMilli()
	fs := a.flags("tool")
	cwd := fs.String("cwd", "", i18n.T("flag.inject.cwd"))
	hostName := fs.String("host", "", i18n.T("flag.inject.host"))
	session := fs.String("session", "", i18n.T("flag.inject.session"))
	actor := fs.String("actor", "", i18n.T("flag.mcp.actor"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || !slices.Contains(mcpserver.HostTools, pos[0]) || *hostName == "" {
		return errUsage
	}
	in, err := readRequest(a.stdin)
	if errors.Is(err, hooks.ErrInputWait) {
		return a.toolError(errors.New(inputWaitMessage()))
	}
	if err != nil {
		return err
	}
	dir := *cwd
	if dir == "" { // 세션 폴더: CLAUDE_PROJECT_DIR, 없으면 작업 폴더(sessionProject와 같은 규칙)
		dir = sessionProject().Dir
	}
	res, herr := hostDo(host.OpCall, host.Request{Host: *hostName, Session: *session, Cwd: dir, Name: pos[0], Args: json.RawMessage(in)},
		host.Options{Actor: actorOf(*actor, memory.ActorAI), TrustLimit: hostTrustLimit, At: at})
	if herr != nil {
		return a.toolError(errors.New(toolMessage(herr)))
	}
	return a.json(res.(host.CallOut).Result)
}

// toolMessage is the words of a tool error: the DB-less one is the older, tool-specific text.
func toolMessage(e *host.Error) string {
	if e.Code == host.CodeNoDB {
		return i18n.T("cli.tool.no_db")
	}
	return hostFailure(e).Error()
}

func (a *app) toolError(err error) error {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	a.stdout.Write(append(b, '\n'))
	return exitCode(1)
}
