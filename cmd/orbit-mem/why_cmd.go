package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/inject"
	"github.com/onidot-labs/orbit-mem/internal/project"
)

// cmdWhy is the recall diagnosis: why one memory did not reach a request
// (기획 · Orbit Code K15). It reads only.
func cmdWhy(a *app, args []string) error {
	fs := a.flags("why")
	cwd := fs.String("cwd", "", i18n.T("flag.inject.cwd"))
	host := fs.String("host", "", i18n.T("flag.inject.host"))
	session := fs.String("session", "", i18n.T("flag.inject.session"))
	rules := fs.String("rules", "", i18n.T("flag.inject.rules"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
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
	if _, err := os.Stat(paths().DB()); err != nil {
		return fmt.Errorf("%s", i18n.T("cli.tool.no_db"))
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
	p := sessionProject()
	if *cwd != "" {
		p = project.Resolve(*cwd)
	}
	d := inject.Diagnose(s, cfg, inject.DiagnoseInput{ID: pos[0], Scope: whereOf(p), Prompt: strings.Join(pos[1:], " "), Host: *host, Session: *session, Options: opts})
	if *asJSON || !isTerminal(a.stdout) {
		return a.json(d)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.why.head", d.MemoryID, d.Title))
	for _, st := range d.Stages {
		if st.Code == "stored" && len(st.Args) == 2 {
			st.Args = []string{stateName(st.Args[0]), kindName(st.Args[1])}
		}
		fmt.Fprintf(a.stdout, "  %s %-6s %s\n", whyMark(st.Status), whyStage(st.Name), whyText(st.Code, st.Args))
	}
	for _, n := range d.Notes {
		fmt.Fprintln(a.stdout, "  · "+whyText(n.Code, n.Args))
	}
	if d.StoppedAt != "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.why.stopped", whyStage(d.StoppedAt)))
	}
	return nil
}

func whyMark(status string) string {
	return map[string]string{inject.Yes: "✓", inject.No: "✗", inject.Unknown: "?", inject.Skipped: "-"}[status]
}

func whyStage(name string) string {
	switch name {
	case inject.StageStored:
		return i18n.T("cli.why.stage.stored")
	case inject.StageEligible:
		return i18n.T("cli.why.stage.eligible")
	case inject.StageSelected:
		return i18n.T("cli.why.stage.selected")
	case inject.StageInjected:
		return i18n.T("cli.why.stage.injected")
	case inject.StageDelivered:
		return i18n.T("cli.why.stage.delivered")
	case inject.StageUsed:
		return i18n.T("cli.why.stage.used")
	}
	return name
}

// whyKeys maps each diagnosis code to its message; the arguments fill it in order.
var whyKeys = map[string]string{
	"not_found": "cli.why.not_found", "hidden": "cli.why.hidden", "stale": "cli.why.stale", "stored": "cli.why.stored",
	"eligible": "cli.why.eligible", "session_rule": "cli.why.session_rule", "out_of_scope": "cli.why.out_of_scope", "journal": "cli.why.journal",
	"expired": "cli.why.expired", "superseded": "cli.why.superseded", "in_bundle": "cli.why.in_bundle", "not_in_bundle": "cli.why.not_in_bundle",
	"no_prompt": "cli.why.no_prompt", "weak_match": "cli.why.weak_match", "matched": "cli.why.matched", "observed": "cli.why.observed",
	"would_inject": "cli.why.would_inject", "would_inject_rules": "cli.why.would_inject_rules", "over_budget": "cli.why.over_budget",
	"skipped": "cli.why.skipped", "no_session": "cli.why.no_session", "delivered": "cli.why.delivered", "hook_returned": "cli.why.hook_returned",
	"not_delivered": "cli.why.not_delivered", "no_record": "cli.why.no_record", "verdict_followed": "cli.why.verdict_followed",
	"verdict_violated": "cli.why.verdict_violated", "verdict_irrelevant": "cli.why.verdict_irrelevant", "opened": "cli.why.opened",
	"no_evidence": "cli.why.no_evidence", "review_due": "cli.why.review_due", "source_changed": "cli.why.source_changed",
}

func whyText(code string, args []string) string {
	key, ok := whyKeys[code]
	if !ok {
		return code + " " + strings.Join(args, " ")
	}
	vals := make([]any, len(args))
	for i, v := range args {
		vals[i] = v
	}
	return i18n.T(key, vals...)
}
