package main

import (
	"io"
	"os"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/inject"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/project"
)

// cmdInject gives orbit-pi the memory context the hooks give a host: the session bundle or the
// memories of one request (stdin), as JSON. It records the same use events as the hooks.
func cmdInject(a *app, args []string) error {
	fs := a.flags("inject")
	cwd := fs.String("cwd", "", i18n.T("flag.inject.cwd"))
	session := fs.String("session", "", i18n.T("flag.inject.session"))
	host := fs.String("host", runtimeOrbitPi, i18n.T("flag.inject.host"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || (pos[0] != "session-start" && pos[0] != "prompt") {
		return errUsage
	}
	out := map[string]any{"text": "", "memoryIds": []string{}}
	if _, err := os.Stat(paths().DB()); err != nil {
		return a.json(out)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	scope := ""
	if *cwd != "" {
		scope = project.Resolve(*cwd).Scope
	}
	var inj inject.Injection
	kind, prompt := memory.EventInjectSessionStart, ""
	if pos[0] == "session-start" {
		inj = inject.SessionBundle(s, cfg, scope, *host, *session)
	} else {
		b, _ := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
		kind, prompt = memory.EventInjectPrompt, string(b)
		inj = inject.PromptInjection(s, cfg, scope, prompt)
	}
	recordInjection(s, cfg, kind, *host, *session, scope, prompt, inj)
	out["text"] = inj.Text
	if inj.MemoryIDs != nil {
		out["memoryIds"] = inj.MemoryIDs
	}
	return a.json(out)
}
