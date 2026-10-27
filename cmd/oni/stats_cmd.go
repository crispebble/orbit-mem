package main

import (
	"fmt"
	"time"

	"github.com/onidot-labs/orbit/internal/metrics"
)

// cmdStats prints the metrics of recent days or of one week (설계 「측정」).
func cmdStats(a *app, args []string) error {
	fs := a.flags("stats")
	days := fs.Int("days", 7, "오늘까지 며칠")
	week := fs.String("week", "", "이 월요일부터 7일")
	asJSON := fs.Bool("json", false, "JSON")
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
			return fmt.Errorf("--week는 월요일 날짜(YYYY-MM-DD)입니다: %q", *week)
		}
		from, to = *week, start.AddDate(0, 0, 6).Format("2006-01-02")
	} else {
		if *days < 1 {
			return fmt.Errorf("--days는 1 이상입니다")
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
