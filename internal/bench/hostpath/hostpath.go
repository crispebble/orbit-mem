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
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/host"
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

	hostName  = "bench" // host 칸: 기록에만 쓰인다
	opTimeout = "30s"   // 느린 시험 기계에서도 결과가 비지 않게
	similarOf = bench.SimilarLimit
)

// service opens a host.Service on the DB copy: the settings given, the products of the set, long
// time limits, the clock fixed at the set's now, and an empty upkeep.
func service(db string, set *bench.Set, cfg config.Config) (*host.Service, error) {
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
	at := set.At()
	return host.New(host.Setup{
		DB:         db,
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Upkeep:     host.UpkeepFunc(func(*memory.Store, config.Config) {}),
		Launcher:   hostName,
		Now:        func() time.Time { return at },
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
		svc, err := service(db, set, cfg)
		if err != nil {
			return bench.Env{}, err
		}
		at := set.At()
		return bench.Env{
			Recaller: &recaller{svc: svc, at: at.UnixMilli(), started: map[string][]string{}},
			Finder:   finder{svc: svc, at: at.UnixMilli()},
			Similar:  similar{db: db, at: at, min: cfg.SaveSimilarMin},
			Close:    func() error { svc.Wait(); return nil },
		}, nil
	}
}

// NewFinder is the Finder of this path alone, so that another recall path can be paired with the
// same search.
func NewFinder(db string, set *bench.Set) bench.Finder {
	svc, err := service(db, set, config.Default())
	if err != nil {
		return failed{err}
	}
	return finder{svc: svc, at: set.At().UnixMilli()}
}

type failed struct{ err error }

func (f failed) Find(string, string, int) ([]string, error) { return nil, f.err }

type recaller struct {
	svc     *host.Service
	at      int64
	started map[string][]string // session → the ids layer A showed
}

func (*recaller) Name() string { return Name }

func (r *recaller) do(op string, req host.Request) (bench.Shown, error) {
	req.Host = hostName
	out, herr := r.svc.Do(op, req, host.Options{At: r.at})
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
	out, err := r.do(host.OpContext, host.Request{Session: session, Cwd: cwd, Source: host.SourceStartup, Audience: host.AudienceMain})
	if err == nil {
		r.started[session] = out.IDs
	}
	return out, err
}

// Request always hands origin user: the origin of a case is the answer key, and the hook hands
// user too. When the contract learns to tell a person's request apart (1-4), change it here.
func (r *recaller) Request(session, cwd, prompt string) (bench.Shown, error) {
	return r.do(host.OpRecall, host.Request{Session: session, Cwd: cwd, Prompt: prompt, Origin: host.OriginUser})
}

func (r *recaller) Tool(session, cwd, tool string) (bench.Shown, error) {
	return r.do(host.OpToolContext, host.Request{Session: session, Cwd: cwd, Tool: tool})
}

// Subagent splits the bundle into its two shares. The title lines are the always-on core, which layer A
// of the session shows too, so the ids layer A showed are the Lead. Today the host gives the core alone,
// so the recall share is empty and the characters are all Lead; a host that adds a recall for the task
// puts its ids (and, for LeadChars, the split of its text) here. Until the text can be split the
// characters of a bundle with a recall share count as recall (LeadChars stays 0).
func (r *recaller) Subagent(session, cwd, task string) (bench.Shown, error) {
	out, err := r.do(host.OpContext, host.Request{Session: session, Cwd: cwd, Audience: host.AudienceSubagent, Task: task})
	if err != nil {
		return out, err
	}
	out.Lead = []string{}
	for _, id := range out.IDs {
		if slices.Contains(r.started[session], id) {
			out.Lead = append(out.Lead, id)
		}
	}
	if len(out.Lead) == len(out.IDs) {
		out.LeadChars = out.Chars
	}
	return out, nil
}

type finder struct {
	svc *host.Service
	at  int64
}

// Find is memory_find through the contract's call: the scope is the contract's default (here).
func (f finder) Find(cwd, query string, limit int) ([]string, error) {
	args, err := json.Marshal(mcpserver.FindIn{Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	out, herr := f.svc.Do(host.OpCall, host.Request{Host: hostName, Session: "bench-find", Cwd: cwd, Name: "memory_find", Args: args}, host.Options{At: f.at})
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
	at  time.Time
	min float64
}

func (similar) Name() string { return SimilarName }

func (s similar) Similar(cwd string, d bench.Draft) ([]string, error) {
	st, _, err := memory.OpenRead(s.db)
	if err != nil {
		return nil, fmt.Errorf("hostpath: save check: %w", err)
	}
	defer st.Close()
	at := s.at
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
