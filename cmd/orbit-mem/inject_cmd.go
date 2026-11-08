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

// cmdInject gives a host without hooks (one that calls orbit-mem itself) the memory context the
// hooks give Claude Code and Codex: the session bundle or the memories of one request (stdin),
// as JSON. --host names the host and is required. It records the same use events as the hooks.
func cmdInject(a *app, args []string) error {
	fs := a.flags("inject")
	cwd := fs.String("cwd", "", i18n.T("flag.inject.cwd"))
	session := fs.String("session", "", i18n.T("flag.inject.session"))
	host := fs.String("host", "", i18n.T("flag.inject.host"))
	rules := fs.String("rules", "", i18n.T("flag.inject.rules"))
	ids := fs.String("ids", "", i18n.T("flag.inject.ids"))
	source := fs.String("source", "", i18n.T("flag.inject.source"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *host == "" { // 호스트 이름이 빠지면 주입 진단(why)이 틀린 결과를 낸다
		return errUsage
	}
	if len(pos) == 1 && pos[0] == "delivered" {
		return a.injectDelivered(*host, *session, *ids)
	}
	if len(pos) != 1 || (pos[0] != "session-start" && pos[0] != "prompt") {
		return errUsage
	}
	var opts inject.Options
	switch *rules {
	case "", "full":
	case "brief": // 확정 규칙을 짧은 꼴로(규칙마다 한 줄, 전문은 memory_get과 요청 회상)
		opts.Brief = true
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
	scope, where := "", ""
	if *cwd != "" {
		p := project.Resolve(*cwd)
		scope, where = p.Scope, whereOf(p)
	}
	var inj inject.Injection
	kind, prompt := memory.EventInjectSessionStart, ""
	if pos[0] == "session-start" {
		if renewsBundle(*source) {
			inject.ResetSession(s, *host, *session)
		}
		inj = inject.SessionBundleWith(s, cfg, where, *host, *session, opts)
	} else {
		b, _ := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
		kind, prompt = memory.EventInjectPrompt, string(b)
		inj = inject.PromptInjectionFor(s, cfg, where, prompt, *host, *session, opts)
	}
	inject.RecordShown(s, *host, *session, inj)
	recordInjection(s, cfg, kind, *host, *session, scope, prompt, inj)
	out["text"] = inj.Text
	if inj.MemoryIDs != nil {
		out["memoryIds"] = inj.MemoryIDs
	}
	return a.json(out)
}

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
