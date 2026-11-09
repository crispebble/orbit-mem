// Package hostpath is the adapter of the bench ports for the recall path of today: it calls the
// host contract v1 (internal/host), so when the search index (1-3) and the injection layer (1-4)
// come into the host, this path measures them without a change here. The bench domain never knows
// this package; the assembly point puts it together.
//
// It calls no model and does nothing outside the DB copy it is given: the upkeep port is empty
// (no daily copy, no tidy) and the settings are not read from the home folder: they are the defaults
// with the overrides the caller gives (Options.Set), checked by the same Validate the settings file is.
package hostpath

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

const (
	// Name is the name of the recall path in a report.
	Name = "hostpath"
	// SimilarName tells where Similar comes from: the judgment a save meets (memory.Store.SaveCheck), the
	// same one memory_save runs, with the saveSimilarMin of the settings.
	SimilarName = "save-check"

	hostName = "bench" // host 칸: 기록에만 쓰인다
	// delegateTool and agentKind are the delegation tool and the subagent kind of every delegation case
	// (one kind: the cases of a session pair in order).
	delegateTool = "delegate"
	agentKind    = "bench-worker"
	opTimeout    = "30s" // 느린 시험 기계에서도 결과가 비지 않게
	similarOf    = bench.SimilarLimit
)

// clock is the time of the path: the set's now, moved by bench.Env.SetNow for cases that carry their times.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) ms() int64      { return c.t.UnixMilli() }

// service opens a host.Service on the DB copy: the settings given, the products of the set, long
// time limits, the clock c, and an empty upkeep.
func service(db string, set *bench.Set, cfg config.Config, c *clock) (*host.Service, error) {
	if len(set.Products) > 0 {
		var m place.Map
		if err := json.Unmarshal(set.Products, &m); err != nil {
			return nil, fmt.Errorf("hostpath: products: %w", err)
		}
		cfg.Products = m
	}
	cfg.HostTimeouts = map[string]string{}
	for _, op := range host.Ops {
		cfg.HostTimeouts[op] = opTimeout
	}
	return host.New(host.Setup{
		DB:         db,
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Upkeep:     host.UpkeepFunc(func(*memory.Store, config.Config) {}),
		Launcher:   hostName,
		Now:        c.now,
	}), nil
}

// Options are what the assembly point hands the path.
type Options struct {
	// Set overrides settings by their key in the settings file (saveSimilarMin, recallChars, ...), the
	// value as the file writes it (a number, true/false, or text). Empty keeps the defaults.
	Set map[string]string
}

// Factory is the bench.Factory of this path with the default settings.
func Factory(db string, set *bench.Set) (bench.Env, error) { return FactoryWith(Options{})(db, set) }

// FactoryWith is the bench.Factory of this path with the settings of o.
func FactoryWith(o Options) bench.Factory {
	return func(db string, set *bench.Set) (bench.Env, error) {
		cfg, err := applySet(config.Default(), o.Set)
		if err != nil {
			return bench.Env{}, err
		}
		c := &clock{t: set.At()}
		svc, err := service(db, set, cfg, c)
		if err != nil {
			return bench.Env{}, err
		}
		return bench.Env{
			Recaller: &recaller{svc: svc, at: c},
			Finder:   finder{svc: svc, at: c},
			Similar:  similar{db: db, at: c, min: cfg.SaveSimilarMin},
			SetNow:   func(t time.Time) { c.t = t },
			Close:    func() error { svc.Wait(); return nil },
		}, nil
	}
}

// NewFinder is the Finder of this path alone, so that another recall path can be paired with the
// same search.
func NewFinder(db string, set *bench.Set) bench.Finder {
	c := &clock{t: set.At()}
	svc, err := service(db, set, config.Default(), c)
	if err != nil {
		return failed{err}
	}
	return finder{svc: svc, at: c}
}

type failed struct{ err error }

func (f failed) Find(string, string, int) ([]string, error) { return nil, f.err }

type recaller struct {
	svc    *host.Service
	at     *clock
	agents int // subagents started so far: each gets its own id
}

func (*recaller) Name() string { return Name }

func (r *recaller) do(op string, req host.Request) (bench.Shown, error) {
	req.Host = hostName
	out, herr := r.svc.Do(op, req, host.Options{At: r.at.ms()})
	if herr != nil {
		return bench.Shown{}, fmt.Errorf("hostpath: %s: %w", op, herr)
	}
	rec, ok := out.(host.Recall)
	if !ok {
		return bench.Shown{}, fmt.Errorf("hostpath: %s: result is %T", op, out)
	}
	if rec.Skipped != "" || len(rec.MemoryIDs) == 0 {
		return bench.Shown{IDs: []string{}}, nil
	}
	return bench.Shown{IDs: rec.MemoryIDs, Chars: len([]rune(rec.Text)), Text: rec.Text}, nil
}

func (r *recaller) Start(session, cwd string) (bench.Shown, error) {
	return r.do(host.OpContext, host.Request{Session: session, Cwd: cwd, Source: host.SourceStartup, Audience: host.AudienceMain})
}

// Request always hands origin user: the origin of a case is the answer key, and the hook hands
// user too. When the contract learns to tell a person's request apart (1-4), change it here.
func (r *recaller) Request(session, cwd, prompt string) (bench.Shown, error) {
	return r.do(host.OpRecall, host.Request{Session: session, Cwd: cwd, Prompt: prompt, Origin: host.OriginUser})
}

func (r *recaller) Tool(session, cwd, tool string) (bench.Shown, error) {
	return r.do(host.OpToolContext, host.Request{Session: session, Cwd: cwd, Tool: tool})
}

// Subagent goes the way of a delegation in a host session (설계 · orbit-mem 4.1·11절): the delegation tool's
// tool-context keeps the task, then the subagent of that kind starts and the context pairs it with the task.
// The bundle's role share (from inject.RoleHead on) is the title lines of the user core: its ids and
// characters are the Lead; the rest is the recall for the task.
func (r *recaller) Subagent(session, cwd, task string) (bench.Shown, error) {
	if _, err := r.do(host.OpToolContext, host.Request{Session: session, Cwd: cwd, Tool: delegateTool, Task: task, AgentKind: agentKind}); err != nil {
		return bench.Shown{}, err
	}
	r.agents++
	out, err := r.do(host.OpContext, host.Request{Session: session, Cwd: cwd, Audience: host.AudienceSubagent,
		Agent: fmt.Sprintf("bench-agent-%d", r.agents), AgentKind: agentKind})
	if err != nil {
		return out, err
	}
	role := ""
	if i := strings.Index(out.Text, inject.RoleHead); i >= 0 {
		role = out.Text[i:]
	}
	out.Lead = []string{}
	for _, id := range out.IDs {
		if role != "" && strings.Contains(role, id) {
			out.Lead = append(out.Lead, id)
		}
	}
	out.LeadChars = len([]rune(role))
	return out, nil
}

type finder struct {
	svc *host.Service
	at  *clock
}

// Find is memory_find through the contract's call: the scope is the contract's default (here).
func (f finder) Find(cwd, query string, limit int) ([]string, error) {
	args, err := json.Marshal(mcpserver.FindIn{Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	out, herr := f.svc.Do(host.OpCall, host.Request{Host: hostName, Session: "bench-find", Cwd: cwd, Name: "memory_find", Args: args}, host.Options{At: f.at.ms()})
	if herr != nil {
		return nil, fmt.Errorf("hostpath: memory_find: %w", herr)
	}
	co, ok := out.(host.CallOut)
	if !ok {
		return nil, fmt.Errorf("hostpath: memory_find: result is %T", out)
	}
	fo, ok := co.Result.(mcpserver.FindOut)
	if !ok {
		return nil, fmt.Errorf("hostpath: memory_find: result is %T", co.Result)
	}
	ids := make([]string, 0, len(fo.Items))
	for _, it := range fo.Items {
		ids = append(ids, it.ID)
	}
	return ids, nil
}

// similar is the "similar memories" step of a save, the judgment memory_save runs before it writes
// (memory.Store.SaveCheck, read only, on a read of the DB copy), with the saveSimilarMin of the
// settings. A same-title or same-topic verdict names the memory the save would add its source to or
// supersede: it counts as the memory found, because with it the save leaves one memory of that content,
// not two (design L-16), and a wrong one would merge or replace a different memory in silence. Otherwise
// the memories of the same scope that are like the draft (top 3) count.
type similar struct {
	db  string
	at  *clock
	min float64
}

func (similar) Name() string { return SimilarName }

func (s similar) Similar(cwd string, d bench.Draft) ([]string, error) {
	st, _, err := memory.OpenRead(s.db)
	if err != nil {
		return nil, fmt.Errorf("hostpath: save check: %w", err)
	}
	defer st.Close()
	at := s.at.now()
	st.Now = func() time.Time { return at }
	res, err := st.SaveCheck(memory.SaveInput{Kind: d.Kind, Title: d.Title, Body: d.Body, Topic: d.Topic, Scope: d.Scope,
		Source: memory.Source{Kind: memory.SourceQuote}}, s.min)
	if err != nil {
		return nil, fmt.Errorf("hostpath: save check: %w", err)
	}
	ids := slices.Clone(res.Existing)
	for _, m := range res.Similar {
		ids = append(ids, m.ID)
	}
	if len(ids) > similarOf {
		ids = ids[:similarOf]
	}
	return ids, nil
}
