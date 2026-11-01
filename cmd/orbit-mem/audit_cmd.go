package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// cmdAudit is the audit list (A2): recent automatic confirmations and
// clean-ups, conflict marks, repeated corrections over the threshold. The
// person reverts or dismisses only what is wrong. `audit harness --json` is
// what the openorbit lab (a developer tool) reads; it only reads.
func cmdAudit(a *app, args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := a.flags("audit")
	days := fs.Int("days", 14, i18n.T("flag.audit.days"))
	note := fs.String("note", "", i18n.T("flag.edit.note"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	who := memory.Who{Actor: actorOf("", memory.ActorUser)}
	switch sub {
	case "":
		if len(pos) != 0 {
			return errUsage
		}
		au, err := s.Audit(memory.AuditFilter{Days: *days})
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(au)
		}
		a.printAudit(au, *days)
		return nil
	case "harness":
		hc, err := s.HarnessCandidates()
		if err != nil {
			return err
		}
		if hc == nil {
			hc = []memory.RepeatedCorrection{}
		}
		if *asJSON {
			return a.json(hc)
		}
		for _, r := range hc {
			a.printCorrection(r)
		}
		if len(hc) == 0 {
			fmt.Fprintln(a.stdout, i18n.T("cli.audit.none"))
		}
		return nil
	case "revert":
		if len(pos) != 1 {
			return errUsage
		}
		res, err := s.RevertChange(pos[0], who, *note)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(res)
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.audit.reverted", pos[0], res.LedgerID))
		return nil
	case "dismiss":
		if len(pos) != 1 {
			return errUsage
		}
		if strings.HasPrefix(pos[0], "correction:") {
			err = s.Acknowledge(pos[0], who)
		} else {
			err = s.ResolveFlag(pos[0], "dismissed", who)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.audit.dismissed", pos[0]))
		return nil
	}
	return errUsage
}

func (a *app) printAudit(au memory.Audit, days int) {
	fmt.Fprintln(a.stdout, i18n.T("cli.audit.changes", days))
	if len(au.Changes) == 0 {
		fmt.Fprintln(a.stdout, "  "+i18n.T("cli.audit.none"))
	}
	for _, c := range au.Changes {
		line := fmt.Sprintf("  %s %s %-9s %s · %s", time.UnixMilli(c.At).Format("2006-01-02"), c.LedgerID, c.Kind, oneLine(c.Title, 60), verdictName(c.Verdict))
		if c.Evidence != "" {
			line += " · " + i18n.T("cli.audit.evidence", c.Evidence)
		}
		if c.RevertedBy != "" {
			if c.AutoReverted {
				line += " · " + i18n.T("cli.audit.auto_reverted", c.RevertedBy)
			} else {
				line += " · " + i18n.T("cli.audit.reverted_by", c.RevertedBy)
			}
		}
		fmt.Fprintln(a.stdout, line)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.audit.conflicts"))
	if len(au.Conflicts) == 0 {
		fmt.Fprintln(a.stdout, "  "+i18n.T("cli.audit.none"))
	}
	for _, c := range au.Conflicts {
		fmt.Fprintf(a.stdout, "  %s %s (%s) ↔ %s (%s)", c.ID, oneLine(c.Title, 50), c.MemoryID, oneLine(c.OtherTitle, 50), c.OtherID)
		if c.Note != "" {
			fmt.Fprint(a.stdout, " — "+oneLine(c.Note, 80))
		}
		fmt.Fprintln(a.stdout)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.audit.corrections", au.Policy.CorrectionSessions))
	if len(au.Corrections) == 0 {
		fmt.Fprintln(a.stdout, "  "+i18n.T("cli.audit.none"))
	}
	for _, r := range au.Corrections {
		a.printCorrection(r)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.audit.pending", au.Pending))
	fmt.Fprintln(a.stdout, i18n.T("cli.audit.policy", au.Policy.SeenDays, au.Policy.FollowedVerified, au.Policy.CorrectionSessions))
}

func (a *app) printCorrection(r memory.RepeatedCorrection) {
	name := r.Target
	if r.Title != "" {
		name += " " + oneLine(r.Title, 50)
	}
	line := fmt.Sprintf("  %s  %s · %s", r.Key, name, i18n.T("cli.audit.sessions", r.Sessions, r.Corrections))
	if r.Harness {
		line += " · " + i18n.T("cli.audit.harness")
	}
	if len(r.Quotes) > 0 {
		line += " — \"" + oneLine(r.Quotes[0], 60) + "\""
	}
	fmt.Fprintln(a.stdout, line)
}
