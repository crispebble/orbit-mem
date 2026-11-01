package retro

import (
	"context"
	"errors"
	"fmt"
)

// The retrospective runner is OpenOrbit's (orb retro). These tests drive the
// storage the way it does: prepare, a fake host that reads the material and
// submits through the same functions the retro MCP tools call, then finish.

// Result is one retrospective as the tests see it.
type Result struct {
	ID     string
	Kind   string
	Period string
	Status string
	Error  string
}

type runner struct {
	*Keeper
	e *env
}

// host runs the fake host on a prepared run and finishes it. A failed run is an error.
func (r *runner) host(p Prepared) (Result, error) {
	res := Result{ID: p.ID, Kind: p.Kind, Period: p.Period, Status: p.Status}
	if p.Status != StatusRunning {
		return res, nil
	}
	r.e.calls = append(r.e.calls, p.ID)
	var h HostResult
	switch {
	case r.e.host != nil:
		h = r.e.host(p.ID)
	case r.e.ai != nil:
		if err := r.e.ai(p.ID); err != nil {
			h.ExecError = err.Error()
		}
	}
	f, err := r.Finish(p.ID, h)
	if err != nil {
		return res, err
	}
	res.Status, res.Error = f.Status, f.Error
	if f.Status == StatusFailed {
		return res, errors.New(f.Error)
	}
	return res, nil
}

func (r *runner) Run(_ context.Context, kind, period string) (Result, error) {
	p, err := r.Prepare(kind, period, r.Cfg.RetroHost)
	if err != nil {
		return Result{Kind: kind, Period: period}, err
	}
	return r.host(p)
}

func (r *runner) RunTidy(context.Context) (Result, error) {
	p, err := r.PrepareTidy(r.Cfg.RetroHost)
	if err != nil {
		return Result{Kind: KindTidy}, err
	}
	return r.host(p)
}

func (r *runner) RunLegacy(context.Context) (Result, error) {
	p, err := r.PrepareLegacy(r.Cfg.RetroHost)
	if err != nil {
		return Result{Kind: KindLegacy}, err
	}
	return r.host(p)
}

// DrainSessions runs the session-end queue once, oldest first, and closes
// each entry with its run (OpenOrbit leaves a signed-out run queued; these
// tests never sign out).
func (r *runner) DrainSessions(context.Context) ([]Result, error) {
	pending, err := Pending(r.Store)
	if err != nil {
		return nil, err
	}
	var results []Result
	var errs []error
	for _, e := range pending {
		p, err := r.PrepareSession(r.Cfg.RetroHost, e, r.Cfg.SessionRetroDailyMax)
		if err != nil {
			return results, err
		}
		res, err := r.host(p)
		results = append(results, res)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s:%s: %w", e.Host, e.Session, err))
		}
		if res.ID != "" {
			if err := MarkEnd(r.Store, e, res.ID); err != nil {
				return results, err
			}
		}
	}
	return results, errors.Join(errs...)
}
