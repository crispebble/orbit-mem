// Package legacy is the recall of 87ab0fb frozen in place (설계 보완 T-23): a copy of the
// recall path of internal/inject and of the strong match of internal/memory/search.go, with
// the budgets and the policy of that day as constants. It is the 「지금 판보다」 baseline the
// evaluation set measures against, so it does not move when the live recall changes. What it
// still takes from memory, place and project is fixed by a test (symbols_test.go).
//
// It is an adapter of the bench Recaller port. The finder and the similar search of the
// evaluation are shared with the live path and paired by the caller, so this package gives a
// Recaller only.
package legacy

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/project"
)

// Name is the name of the frozen recall in a report.
const Name = "legacy-87ab0fb"

// Host is the host name the frozen recall keeps its session bundles and marks under.
const Host = "bench"

// New opens the frozen recall on a DB copy (bench.Prepared.Copy): the session bundles and the
// shown marks are written to that copy. Now is the fixed now of the set. Of the settings only
// the product map of the set is read; the rest are the defaults of 87ab0fb with inject=full.
func New(dbPath string, set *bench.Set) (bench.Recaller, func() error, error) {
	var products place.Map
	if len(set.Products) > 0 {
		if err := json.Unmarshal(set.Products, &products); err != nil {
			return nil, nil, fmt.Errorf("products: %w", err)
		}
		if err := products.Validate(); err != nil {
			return nil, nil, err
		}
	}
	s, err := memory.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	at := set.At()
	s.Now = func() time.Time { return at }
	return &recaller{s: s, products: products}, s.Close, nil
}

// Env is New in the shape of a bench.Factory, without a finder or a similar search.
func Env(dbPath string, set *bench.Set) (bench.Env, error) {
	r, closeFn, err := New(dbPath, set)
	if err != nil {
		return bench.Env{}, err
	}
	return bench.Env{Recaller: r, Close: closeFn}, nil
}

type recaller struct {
	s        *memory.Store
	products place.Map
}

func (r *recaller) Name() string { return Name }

// where resolves the session folder the way the host did (internal/host/ops.go:149-158 at 87ab0fb).
func (r *recaller) where(cwd string) string {
	var p project.Project
	if cwd != "" {
		p = project.Resolve(cwd)
	}
	place.Known(p)
	if p.Dir != "" {
		return p.Dir
	}
	return p.Scope
}

// shown keeps the marks of an injection, as the host did after each recall
// (internal/host/ops.go:189-194 at 87ab0fb), and gives what was shown. The use events and the
// activity the host also kept are not read by the recall, so they are not written.
func (r *recaller) shown(session string, in injection) bench.Shown {
	recordShown(r.s, Host, session, in)
	return bench.Shown{IDs: in.memoryIDs, Chars: in.chars, Text: in.text}
}

// Start is the session start bundle (internal/host/ops.go:251-263 at 87ab0fb).
func (r *recaller) Start(session, cwd string) (bench.Shown, error) {
	where := r.where(cwd)
	return r.shown(session, sessionBundle(r.s, r.products, where, Host, session)), nil
}

// Request is the work recall of a request with the session's news (internal/host/ops.go:266-279 at
// 87ab0fb). The prompt is taken as a person's: the hook of that day sent every prompt as user.
func (r *recaller) Request(session, cwd, prompt string) (bench.Shown, error) {
	where := r.where(cwd)
	return r.shown(session, promptInjectionFor(r.s, r.products, where, prompt, Host, session)), nil
}

// Tool is the recall right before a tool call (internal/host/ops.go:281-290 at 87ab0fb).
func (r *recaller) Tool(session, cwd, tool string) (bench.Shown, error) {
	where := r.where(cwd)
	return r.shown(session, toolInjection(r.s, r.products, where, Host, session, tool)), nil
}

// Subagent is what a sub agent got when it started (internal/host/ops.go:240-249 at 87ab0fb): the
// user core only. The task is not read: 87ab0fb matched nothing against it.
func (r *recaller) Subagent(session, cwd, task string) (bench.Shown, error) {
	r.where(cwd)
	return r.shown(session, subagentStart(r.s)), nil
}
