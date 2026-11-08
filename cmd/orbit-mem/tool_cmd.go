package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/project"
)

// cmdTool runs one memory tool for a host without an MCP client (--host names it, required):
// the MCP input as JSON on stdin, the MCP output as JSON on stdout. A tool
// error is {"error": "..."} with exit 1. The writer is the host's AI unless
// --actor says otherwise, and the call is recorded as that host's tool use.
func cmdTool(a *app, args []string) error {
	fs := a.flags("tool")
	cwd := fs.String("cwd", "", i18n.T("flag.inject.cwd"))
	host := fs.String("host", "", i18n.T("flag.inject.host"))
	session := fs.String("session", "", i18n.T("flag.inject.session"))
	actor := fs.String("actor", "", i18n.T("flag.mcp.actor"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || !slices.Contains(mcpserver.HostTools, pos[0]) || *host == "" {
		return errUsage
	}
	in, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths().DB()); err != nil && pos[0] != "memory_save" {
		return a.toolError(errors.New(i18n.T("cli.tool.no_db")))
	}
	s, err := openStore()
	if err != nil {
		return a.toolError(err)
	}
	defer s.Close()
	p := sessionProject()
	if *cwd != "" {
		p = project.Resolve(*cwd)
	}
	t := mcpserver.NewTools(s, mcpserver.Options{Project: p.Scope, ProjectLabel: p.Label, Dir: p.Dir, Git: p.Git, Temp: p.Temp, Actor: actorOf(*actor, memory.ActorAI), Products: productsOf()})
	out, err := t.Call(pos[0], in, *host, *session)
	if err != nil {
		return a.toolError(err)
	}
	return a.json(out)
}

func (a *app) toolError(err error) error {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	a.stdout.Write(append(b, '\n'))
	return exitCode(1)
}
