package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onidot-labs/openorbit/internal/i18n"
	"github.com/onidot-labs/openorbit/internal/inject"
	"github.com/onidot-labs/openorbit/internal/memory"
	"github.com/onidot-labs/openorbit/internal/orbitpi"
	"github.com/onidot-labs/openorbit/internal/project"
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

// cmdRuntime installs, updates and reports orbit-pi (설계 · OpenOrbit 4차 R4-0).
func cmdRuntime(a *app, args []string) error {
	fs := a.flags("runtime")
	repo := fs.String("repo", orbitpi.DefaultRepo, i18n.T("flag.runtime.repo"))
	ref := fs.String("ref", orbitpi.DefaultRef, i18n.T("flag.runtime.ref"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage
	}
	var st orbitpi.State
	switch pos[0] {
	case "install", "update":
		st, err = orbitpi.Install(paths().Home, orbitpi.Options{Repo: *repo, Ref: *ref, Log: a.stderr})
	case "status":
		st, err = orbitpi.Status(paths().Home)
	default:
		return errUsage
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return a.json(st)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.runtime.state", orbitpi.Dir(paths().Home), st.Commit, st.Ref, st.InstalledAt.Format(time.RFC3339)))
	if pos[0] == "status" && !st.Current {
		fmt.Fprintln(a.stdout, i18n.T("cli.runtime.not_current"))
	}
	return nil
}
