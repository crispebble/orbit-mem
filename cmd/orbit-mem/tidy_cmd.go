package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 하루 한 번 정리(기획 · orbit-mem 4.3 H1~H4): 세션 시작 훅이 그날 정리가 아직이면 orbit-mem tidy auto를
// 뒤에서 띄운다. 뒤 프로세스는 기억 DB meta에 그날을 먼저 표시한 쪽만 정리한다. 사람은 orbit-mem tidy로 지금 돌린다.

func cmdTidy(a *app, args []string) error {
	if len(args) > 0 && args[0] == "auto" {
		return tidyAuto(a, args[1:])
	}
	fs := a.flags("tidy")
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if pos, err := parse(fs, args); err != nil || len(pos) != 0 {
		return errUsage
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
	_ = s.SetMeta(memory.MetaTidyDay, memory.Day(s.Now())) // 오늘 뒤에서 다시 돌지 않게 한다
	r := s.Tidy(tidyOptions(cfg))
	if a.wantJSON(*asJSON) {
		return a.json(r)
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.tidy.done", r.Judged, r.Reverted, r.Confirmed, r.Closed, r.Reviewed, r.Expired, r.Moved))
	for _, e := range r.Errors {
		fmt.Fprintln(a.stdout, i18n.T("cli.tidy.error", e))
	}
	return nil
}

// tidyOptions maps the settings onto the tidy: long-unused candidates go stale only when
// recall is injected (inject full); with rules·off a candidate had no chance to be used.
func tidyOptions(cfg config.Config) memory.TidyOptions {
	return memory.TidyOptions{LedgerWindowDays: cfg.LedgerWindowDays, CandidateExpiryDays: cfg.CandidateExpiryDays,
		ExpireCandidates: cfg.Inject == config.InjectFull, Products: cfg.Products}
}

// tidyAuto is the background run the session-start hook starts. The process that marks
// today first tidies; the others stop. It never fails: problems go to logs/tidy.log, and a
// day whose tidy failed is tried again the next day.
func tidyAuto(a *app, args []string) error {
	fs := a.flags("tidy")
	if pos, err := parse(fs, args); err != nil || len(pos) != 0 {
		return errUsage
	}
	p := paths()
	cfg, err := loadConfig()
	if err != nil {
		tidyLog(p, "config: "+err.Error())
		return nil
	}
	if !cfg.AutoTidy {
		return nil
	}
	if _, err := os.Stat(p.DB()); err != nil { // 설치 전: 기억 DB를 만들지 않는다
		return nil
	}
	s, err := openStore()
	if err != nil {
		tidyLog(p, "open: "+err.Error())
		return nil
	}
	defer s.Close()
	won, err := s.ClaimDay(memory.MetaTidyDay, memory.Day(s.Now()))
	if err != nil {
		tidyLog(p, "claim: "+err.Error())
		return nil
	}
	if !won {
		return nil
	}
	r := s.Tidy(tidyOptions(cfg))
	tidyLog(p, fmt.Sprintf("ran judged %d reverted %d confirmed %d closed %d reviewed %d expired %d moved %d",
		r.Judged, r.Reverted, r.Confirmed, r.Closed, r.Reviewed, r.Expired, r.Moved))
	if len(r.Errors) > 0 {
		tidyLog(p, "error: "+strings.Join(r.Errors, "; "))
	}
	return nil
}

func tidyLog(p config.Paths, line string) {
	if err := os.MkdirAll(p.Logs(), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(p.Logs(), "tidy.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s tidy auto: %s\n", time.Now().Format(time.RFC3339), line)
}

// autoTidy is the session-start hook's part: when today's tidy has not run it starts
// `orbit-mem tidy auto` as its own process and does not wait. Checking costs one meta read;
// nothing here can fail the hook.
func autoTidy(cfg config.Config, s *memory.Store) {
	if !cfg.AutoTidy || s.Meta(memory.MetaTidyDay) == memory.Day(s.Now()) {
		return
	}
	_ = startBackground("tidy", "auto")
}
