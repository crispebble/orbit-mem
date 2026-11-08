package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	hooks "github.com/crispebble/orbit-mem/internal/hook"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/i18n"
)

// cmdInject gives a host without hooks (one that calls orbit-mem itself) the memory context the
// hooks give Claude Code and Codex: the session bundle or the memories of one request (stdin),
// as JSON. --host names the host and is required. It is the older name of the host contract's
// context, recall and delivered operations, with the output it always had.
func cmdInject(a *app, args []string) error {
	at := time.Now().UnixMilli()
	fs := a.flags("inject")
	cwd := fs.String("cwd", "", i18n.T("flag.inject.cwd"))
	session := fs.String("session", "", i18n.T("flag.inject.session"))
	hostName := fs.String("host", "", i18n.T("flag.inject.host"))
	rules := fs.String("rules", "", i18n.T("flag.inject.rules"))
	ids := fs.String("ids", "", i18n.T("flag.inject.ids"))
	source := fs.String("source", "", i18n.T("flag.inject.source"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *hostName == "" { // 호스트 이름이 빠지면 주입 진단(why)이 틀린 결과를 낸다
		return errUsage
	}
	if len(pos) == 1 && pos[0] == "delivered" {
		return a.injectDelivered(*hostName, *session, *ids, at)
	}
	if len(pos) != 1 || (pos[0] != "session-start" && pos[0] != "prompt") {
		return errUsage
	}
	var opts host.Options
	opts.At = at
	switch *rules {
	case "", "full":
	case "brief": // 확정 규칙을 짧은 꼴로(규칙마다 한 줄, 전문은 memory_get과 요청 회상)
		opts.Brief = true
	default:
		return errUsage
	}
	req := host.Request{Host: *hostName, Session: *session, Cwd: *cwd}
	var res any
	var herr *host.Error
	if pos[0] == "session-start" {
		if renewsBundle(*source) { // 압축·/clear 뒤에는 앞선 문맥이 없으니 표시를 먼저 비운다
			hostDo(host.OpReset, req, opts)
		}
		if slices.Contains([]string{host.SourceStartup, host.SourceResume, host.SourceFork, host.SourceCompact, host.SourceClear}, *source) {
			req.Source = *source
		}
		res, herr = hostDo(host.OpContext, req, opts)
	} else {
		b, err := readRequest(a.stdin)
		if errors.Is(err, hooks.ErrInputWait) {
			return errors.New(inputWaitMessage())
		}
		req.Prompt = string(b)
		res, herr = hostDo(host.OpRecall, req, opts)
	}
	if herr != nil {
		return hostFailure(herr)
	}
	out := map[string]any{"text": "", "memoryIds": []string{}}
	// 읽지 못하는 DB(없음·새 판·옛 판)는 `skipped` 칸 대신 지금까지의 빈 문맥이다. 판이 맞지 않는 것은 「기억 없음」과
	// 구분되도록 stderr에 한 줄 남긴다(DB 없음은 기억이 없는 것이니 조용하다).
	if r, ok := res.(host.Recall); ok {
		switch r.Skipped {
		case "":
			out["text"] = r.Text
			if r.MemoryIDs != nil {
				out["memoryIds"] = r.MemoryIDs
			}
		case host.CodeSchemaNewer:
			fmt.Fprintln(a.stderr, "orbit-mem inject: "+i18n.T("cli.inject.schema_newer"))
		case host.CodeSchemaOlder:
			fmt.Fprintln(a.stderr, "orbit-mem inject: "+i18n.T("cli.inject.schema_older"))
		}
	}
	return a.json(out)
}

// injectDelivered records that a host put recalled memories into a model
// request (inject.delivered): the 「delivered」 stage of orbit-mem why.
func (a *app) injectDelivered(hostName, session, ids string, at int64) error {
	var list []string
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			list = append(list, id)
		}
	}
	if session == "" || len(list) == 0 {
		return errUsage
	}
	_, herr := hostDo(host.OpDelivered, host.Request{Host: hostName, Session: session, MemoryIDs: list}, host.Options{At: at})
	switch {
	case herr == nil, herr.Code == host.CodeNoDB, herr.Code == host.CodeSchemaNewer, herr.Code == host.CodeSchemaOlder:
		return nil // DB가 아직 없거나 읽지 못하는 판이면 남길 것이 없다(조용히 0)
	case herr.Code == host.CodeUsage:
		return errUsage
	}
	return hostFailure(herr)
}
