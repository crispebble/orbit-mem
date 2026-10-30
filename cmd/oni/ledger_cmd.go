package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/onidot-labs/orbit/internal/i18n"
	"github.com/onidot-labs/orbit/internal/memory"
)

// verdictName and targetName are the ledger names a person reads; an unknown code is shown as is.
func verdictName(v string) string {
	if !contains([]string{memory.VerdictPending, memory.VerdictHelped, memory.VerdictHarmful, memory.VerdictNoEffect, memory.VerdictMixed}, v) {
		return v
	}
	return i18n.T("verdict." + v)
}

func targetName(t string) string {
	if !contains([]string{memory.TargetMemory, memory.TargetHarness, memory.TargetInstruction}, t) {
		return t
	}
	return i18n.T("target." + t)
}

// cmdLedger prints the change effect ledger (설계 「변경 효과 원장」), newest first.
func cmdLedger(a *app, args []string) error {
	fs := a.flags("ledger")
	all := fs.Bool("all", false, i18n.T("flag.ledger.all"))
	n := fs.Int("n", 30, i18n.T("flag.count"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	rows, err := s.Ledger(memory.LedgerFilter{Limit: *n})
	if err != nil {
		return err
	}
	if *asJSON {
		if rows == nil {
			rows = []memory.LedgerEntry{}
		}
		return a.json(rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.ledger.empty"))
		return nil
	}
	for _, e := range rows {
		verdict := verdictName(e.Verdict)
		if e.Verdict != memory.VerdictPending && !e.Evidence {
			verdict = i18n.T("cli.ledger.no_evidence", verdict)
		}
		title, _, _ := strings.Cut(e.Basis, "\n")
		fmt.Fprintf(a.stdout, "%s %s %-6s %s %s · %s %s\n", time.UnixMilli(e.At).Format("2006-01-02"), e.ID, verdict, e.ProposalKind,
			oneLine(title, 60), targetName(e.TargetKind), oneLine(strings.Join(e.Targets, ", "), 80))
		if e.Verdict != memory.VerdictPending {
			o := e.Observation
			fmt.Fprintln(a.stdout, "    "+i18n.T("cli.ledger.observation", o.Before.Bad, o.Before.Irrelevant, o.Before.Sessions,
				o.After.Bad, o.After.Irrelevant, o.After.Sessions))
		}
		for _, link := range []struct{ label, id string }{{i18n.T("cli.ledger.lesson"), e.LessonID}, {i18n.T("cli.ledger.reverted_by"), e.RevertedBy}, {i18n.T("cli.ledger.revert_of"), e.RevertOf}} {
			if link.id != "" {
				fmt.Fprintf(a.stdout, "    %s: %s\n", link.label, link.id)
			}
		}
		if *all {
			fmt.Fprintln(a.stdout, "    "+strings.ReplaceAll(strings.TrimSpace(e.Change), "\n", "\n    "))
		}
	}
	return nil
}
