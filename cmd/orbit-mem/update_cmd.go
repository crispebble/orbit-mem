package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/update"
	"github.com/onidot-labs/orbit-mem/internal/version"
)

// updateTarget is the orbit-mem file `update` replaces (tests point it elsewhere).
var updateTarget = selfPath

// updateReinstall runs `install` of the new binary so the host plugins match it.
var updateReinstall = func(bin string, out, errw io.Writer) error {
	cmd := exec.Command(bin, "install", "--bin-dir", filepath.Dir(bin))
	cmd.Stdout, cmd.Stderr = out, errw
	return cmd.Run()
}

// updateResult is what update --json prints, success or not.
type updateResult struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	Current string   `json:"current"`
	Latest  string   `json:"latest"`
	Updated bool     `json:"updated"`
	Binary  string   `json:"binary"`
	Steps   []string `json:"steps"`
}

func cmdUpdate(a *app, args []string) (err error) {
	if len(args) == 1 && args[0] == "--refresh-cache" { // hidden: the detached process of the session-start hook
		return refreshUpdateCache()
	}
	fs := a.flags("update")
	check := fs.Bool("check", false, i18n.T("flag.update.check"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.update.dry_run"))
	want := fs.String("version", "", i18n.T("flag.update.version"))
	force := fs.Bool("force", false, i18n.T("flag.update.force"))
	asJSON := fs.Bool("json", false, i18n.T("flag.update.json"))
	res := updateResult{Current: version.Version, Binary: updateTarget(), Steps: []string{}}
	if _, perr := parse(fs, args); perr != nil {
		if !wantsJSON(args) || errors.Is(perr, flag.ErrHelp) {
			return perr
		}
		res.Error = perr.Error()
		_ = a.json(res)
		return exitCode(2)
	}
	out := a.stdout
	if *asJSON { // stdout has the result JSON only
		out = a.stderr
	}
	ctx, stop := signalContext()
	defer stop()
	var r update.Result
	if !filepath.IsAbs(res.Binary) { // the running file's path is unknown: write nothing
		err = i18n.Errorf("cli.update.err.no_path", res.Binary)
	} else {
		r, err = update.Run(ctx, update.Options{
			Current: version.Version, Want: *want, Target: res.Binary, CheckOnly: *check, Force: *force, DryRun: *dry,
			Client:    &http.Client{Timeout: 2 * time.Minute},
			Reinstall: func(bin string) error { return updateReinstall(bin, out, a.stderr) },
		})
	}
	if r.Binary == "" {
		r.Binary = res.Binary
	}
	res.Latest, res.Updated, res.Binary, res.Steps = r.Latest, r.Updated, r.Binary, r.Steps
	if res.Steps == nil {
		res.Steps = []string{}
	}
	if *asJSON {
		res.OK = err == nil
		if err != nil {
			res.Error = err.Error()
			fmt.Fprintln(a.stderr, "orbit-mem update: "+err.Error())
		}
		if jerr := a.json(res); jerr != nil {
			return jerr
		}
		if err != nil {
			return exitCode(1)
		}
		return nil
	}
	if err != nil {
		return err
	}
	switch {
	case *check && update.Compare(res.Latest, res.Current) > 0:
		fmt.Fprintln(a.stdout, i18n.T("cli.update.available", res.Current, res.Latest))
	case *check:
		fmt.Fprintln(a.stdout, i18n.T("cli.update.up_to_date", res.Current))
	case *dry:
		for _, s := range res.Steps {
			fmt.Fprintln(a.stdout, "- "+s)
		}
	case res.Updated:
		fmt.Fprintln(a.stdout, i18n.T("cli.update.done", res.Current, res.Latest, res.Binary))
	default:
		fmt.Fprintln(a.stdout, i18n.T("cli.update.up_to_date", res.Current))
	}
	return nil
}

// refreshUpdateCache is `update --refresh-cache`, started detached by the session-start hook:
// it asks for the newest version (when the cache is stale and updateCheck is on) and writes
// the cache. It prints nothing and never fails.
func refreshUpdateCache() error {
	if cfg, err := loadConfig(); err != nil || !cfg.UpdateCheck {
		return nil
	}
	update.CheckDaily(context.Background(), paths().Home, time.Now(), &http.Client{}, "", refreshTimeout)
	return nil
}

// refreshTimeout bounds the detached refresh process.
const refreshTimeout = 15 * time.Second

// withUpdateNote adds one line at the end of the session-start context when the cached newest
// version is newer than this orbit-mem. The hook never waits for the network: it reads the
// cache only, and when that is missing or older than a day (and updateCheck is on) it starts
// `orbit-mem update --refresh-cache` as its own process. The context is for the AI, so the
// line is Korean.
func withUpdateNote(text string, cfg config.Config) string {
	if !cfg.UpdateCheck {
		return text
	}
	c, ok := update.ReadCache(paths().Home)
	if !ok || time.Since(c.CheckedAt) >= update.CacheTTL || c.CheckedAt.After(time.Now().Add(update.CacheTTL)) {
		_ = startBackground("update", "--refresh-cache")
	}
	if c.Latest == "" || update.Compare(c.Latest, version.Version) <= 0 {
		return text
	}
	line := fmt.Sprintf("orbit-mem 새 판(%s)이 나왔습니다(현재 %s): 사용자가 원하면 `orbit-mem update`로 올리세요.", c.Latest, version.Version) // i18n:keep AI가 읽는 세션 문맥
	if text = strings.TrimRight(text, "\n"); text == "" {
		return line
	}
	return text + "\n" + line
}
