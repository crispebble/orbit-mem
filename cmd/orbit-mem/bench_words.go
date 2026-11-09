package main

import (
	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/i18n"
)

// benchWording is the bench report text from the message catalog (bench.Wording).
type benchWording struct{}

func (benchWording) Part(part string) string {
	switch part {
	case bench.PartTrain:
		return i18n.T("bench.part.train")
	case bench.PartTest:
		return i18n.T("bench.part.test")
	}
	return i18n.T("bench.part.all")
}

func (benchWording) Header(about, now, seed string) string {
	return i18n.T("bench.report.header", about, now, seed)
}

func (benchWording) Units(cases, units, train, test int) string {
	return i18n.T("bench.report.units", cases, units, train, test)
}

func (benchWording) TestSize(b bench.Bundles, p bench.Points) string {
	return i18n.T("bench.report.test_size", b.Answered, p.Answered, b.Empty, p.Empty, b.Subagent, p.Subagent, b.Duplicate, p.Duplicate)
}

func (benchWording) Requests(n int) string { return i18n.T("bench.report.requests", n) }

func (benchWording) Hit(rate string, byStart int) string {
	return i18n.T("bench.report.hit", rate, byStart)
}

func (benchWording) Wrong(rate string, injected, correct, neutral, wrong, forbid, forbidCases int) string {
	return i18n.T("bench.report.wrong", rate, injected, correct, neutral, wrong, forbid, forbidCases)
}

func (benchWording) Recover(rate, everyMissed string) string {
	return i18n.T("bench.report.recover", rate, everyMissed)
}

func (benchWording) Empty(rate string) string { return i18n.T("bench.report.empty", rate) }

func (benchWording) CharsRequest(dist string) string {
	return i18n.T("bench.report.chars_request", dist)
}

func (benchWording) CharsTool(dist string) string { return i18n.T("bench.report.chars_tool", dist) }

func (benchWording) CharsSession(dist string) string {
	return i18n.T("bench.report.chars_session", dist)
}

func (benchWording) CharsStart(dist string) string { return i18n.T("bench.report.chars_start", dist) }

func (benchWording) Subagent(cases int, hit string, byLead int, wrong string, forbid int, empty, chars, leadChars string) string {
	return i18n.T("bench.report.subagent", cases, hit, byLead, wrong, forbid, empty, chars, leadChars)
}

func (benchWording) Duplicate(cases int, hit, offered string, forbid int) string {
	return i18n.T("bench.report.duplicate", cases, hit, offered, forbid)
}

func (benchWording) ZeroDen() string { return i18n.T("bench.report.zero_den") }

func (benchWording) Dist(mean, median float64, max, n int) string {
	return i18n.T("bench.report.dist", mean, median, max, n)
}
