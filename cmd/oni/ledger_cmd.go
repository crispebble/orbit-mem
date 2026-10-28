package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/onidot-labs/orbit/internal/memory"
)

var ledgerVerdictNames = map[string]string{memory.VerdictPending: "관찰 중", memory.VerdictHelped: "도움됨", memory.VerdictHarmful: "해로움",
	memory.VerdictNoEffect: "효과 없음", memory.VerdictMixed: "섞임"}

var ledgerTargetNames = map[string]string{memory.TargetMemory: "기억", memory.TargetHarness: "하네스", memory.TargetInstruction: "지침"}

// cmdLedger prints the change effect ledger (설계 「변경 효과 원장」), newest first.
func cmdLedger(a *app, args []string) error {
	fs := a.flags("ledger")
	all := fs.Bool("all", false, "변경 내용까지")
	n := fs.Int("n", 30, "개수")
	asJSON := fs.Bool("json", false, "JSON")
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
		fmt.Fprintln(a.stdout, "원장이 비어 있습니다. 제안을 승인하면 한 줄씩 남습니다.")
		return nil
	}
	for _, e := range rows {
		verdict := ledgerVerdictNames[e.Verdict]
		if e.Verdict != memory.VerdictPending && !e.Evidence {
			verdict += "(증거 없음)"
		}
		title, _, _ := strings.Cut(e.Basis, "\n")
		fmt.Fprintf(a.stdout, "%s %s %-6s %s %s · %s %s\n", time.UnixMilli(e.At).Format("2006-01-02"), e.ID, verdict, e.ProposalKind,
			oneLine(title, 60), ledgerTargetNames[e.TargetKind], oneLine(strings.Join(e.Targets, ", "), 80))
		if e.Verdict != memory.VerdictPending {
			o := e.Observation
			fmt.Fprintf(a.stdout, "    관찰: 전 나쁜 신호 %d·상관없음 %d·세션 %d → 후 %d·%d·%d\n", o.Before.Bad, o.Before.Irrelevant, o.Before.Sessions,
				o.After.Bad, o.After.Irrelevant, o.After.Sessions)
		}
		for _, link := range []struct{ label, id string }{{"교훈", e.LessonID}, {"되돌림", e.RevertedBy}, {"되돌린 원장", e.RevertOf}} {
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
