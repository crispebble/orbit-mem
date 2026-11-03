package main

import (
	"io"
	"os"
	"strings"

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
	rules := fs.String("rules", "", i18n.T("flag.inject.rules"))
	ids := fs.String("ids", "", i18n.T("flag.inject.ids"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 1 && pos[0] == "delivered" {
		return a.injectDelivered(*host, *session, *ids)
	}
	if len(pos) != 1 || (pos[0] != "session-start" && pos[0] != "prompt") {
		return errUsage
	}
	// Orbit Code는 확정 규칙을 짧은 꼴로 받는다(규칙마다 한 줄, 전문은 memory_get과 요청 회상). 다른 호스트는 전문 꼴 그대로다.
	var opts inject.Options
	switch *rules {
	case "":
		opts.Brief = *host == hostOrbitCode
	case "brief":
		opts.Brief = true
	case "full":
	default:
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
		inj = inject.SessionBundleWith(s, cfg, scope, *host, *session, opts)
	} else {
		b, _ := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
		kind, prompt = memory.EventInjectPrompt, string(b)
		inj = inject.PromptInjectionWith(s, cfg, scope, prompt, opts)
	}
	recordInjection(s, cfg, kind, *host, *session, scope, prompt, inj)
	out["text"] = inj.Text
	if inj.MemoryIDs != nil {
		out["memoryIds"] = inj.MemoryIDs
	}
	return a.json(out)
}

// hostOrbitCode is the host name the Orbit Code extension gives orbit-mem.
const hostOrbitCode = "orbit-code"

// injectDelivered records that a host put recalled memories into a model
// request (inject.delivered): the 「delivered」 stage of orbit-mem why.
func (a *app) injectDelivered(host, session, ids string) error {
	var list []string
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			list = append(list, id)
		}
	}
	if session == "" || len(list) == 0 {
		return errUsage
	}
	if _, err := os.Stat(paths().DB()); err != nil {
		return nil
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RecordEvent(memory.Event{Kind: memory.EventInjectDelivered, Host: host, Session: session}, map[string]any{"memoryIds": list})
}
