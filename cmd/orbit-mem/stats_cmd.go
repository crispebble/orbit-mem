package main

import (
	"fmt"
	"time"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/metrics"
)

// cmdStats prints the metrics of recent days or of one week (설계 「측정」).
func cmdStats(a *app, args []string) error {
	fs := a.flags("stats")
	days := fs.Int("days", 7, i18n.T("flag.stats.days"))
	week := fs.String("week", "", i18n.T("flag.stats.week"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	var from, to string
	if *week != "" {
		start, err := time.ParseInLocation("2006-01-02", *week, time.Local)
		if err != nil || start.Weekday() != time.Monday {
			return i18n.Errorf("cli.stats.err.week", *week)
		}
		from, to = *week, start.AddDate(0, 0, 6).Format("2006-01-02")
	} else {
		if *days < 1 {
			return i18n.Errorf("cli.stats.err.days")
		}
		now := s.Now()
		from, to = now.AddDate(0, 0, -(*days-1)).Format("2006-01-02"), now.Format("2006-01-02")
	}
	rep, err := metrics.Compute(s, from, to)
	if err != nil {
		return err
	}
	if *asJSON {
		return a.json(rep)
	}
	fmt.Fprint(a.stdout, metrics.Render(rep))
	return nil
}
