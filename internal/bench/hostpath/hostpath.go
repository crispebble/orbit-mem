// Package hostpath is the adapter of the bench ports for the recall path of today: it calls the
// host contract v1 (internal/host), so when the search index (1-3) and the injection layer (1-4)
// come into the host, this path measures them without a change here. The bench domain never knows
// this package; the assembly point puts it together.
//
// It calls no model and does nothing outside the DB copy it is given: the upkeep port is empty
// (no daily copy, no tidy) and the settings are not read from the home folder.
package hostpath

import (
	"encoding/json"
	"fmt"
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
	// SimilarName tells that Similar is a stand-in: a memory_find over the draft.
	SimilarName = "find-proxy"

	hostName  = "bench" // host 칸: 기록에만 쓰인다
	opTimeout = "30s"   // 느린 시험 기계에서도 결과가 비지 않게
	similarOf = bench.SimilarLimit
)

// service opens a host.Service on the DB copy: default settings, the products of the set, long
// time limits, the clock fixed at the set's now, and an empty upkeep.
func service(db string, set *bench.Set) (*host.Service, error) {
	cfg := config.Default()
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

// Factory is the bench.Factory of this path.
func Factory(db string, set *bench.Set) (bench.Env, error) {
	svc, err := service(db, set)
	if err != nil {
		return bench.Env{}, err
	}
	return bench.Env{
		Recaller: recaller{svc: svc, at: set.At().UnixMilli()},
		Finder:   finder{svc: svc, at: set.At().UnixMilli()},
		Similar:  similar{finder{svc: svc, at: set.At().UnixMilli()}},
		Close:    func() error { svc.Wait(); return nil },
	}, nil
}

// NewFinder is the Finder of this path alone, so that another recall path can be paired with the
// same search.
func NewFinder(db string, set *bench.Set) bench.Finder {
	svc, err := service(db, set)
	if err != nil {
		return failed{err}
	}
	return finder{svc: svc, at: set.At().UnixMilli()}
}

type failed struct{ err error }

func (f failed) Find(string, string, int) ([]string, error) { return nil, f.err }

type recaller struct {
	svc *host.Service
	at  int64
}

func (recaller) Name() string { return Name }

func (r recaller) do(op string, req host.Request) (bench.Shown, error) {
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

func (r recaller) Start(session, cwd string) (bench.Shown, error) {
	return r.do(host.OpContext, host.Request{Session: session, Cwd: cwd, Source: host.SourceStartup, Audience: host.AudienceMain})
}

// Request always hands origin user: the origin of a case is the answer key, and the hook hands
// user too. When the contract learns to tell a person's request apart (1-4), change it here.
func (r recaller) Request(session, cwd, prompt string) (bench.Shown, error) {
	return r.do(host.OpRecall, host.Request{Session: session, Cwd: cwd, Prompt: prompt, Origin: host.OriginUser})
}

func (r recaller) Tool(session, cwd, tool string) (bench.Shown, error) {
	return r.do(host.OpToolContext, host.Request{Session: session, Cwd: cwd, Tool: tool})
}

func (r recaller) Subagent(session, cwd, task string) (bench.Shown, error) {
	return r.do(host.OpContext, host.Request{Session: session, Cwd: cwd, Audience: host.AudienceSubagent, Task: task})
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

// similar stands in for the "similar memories" step of a save: memory_find over the title and the
// body of the draft, top 3. 1-3의 저장 때 비슷한 기억 고르기가 생기면 1-6에서 다시 잇는다.
type similar struct{ f finder }

func (similar) Name() string { return SimilarName }

func (s similar) Similar(cwd string, d bench.Draft) ([]string, error) {
	q := d.Title
	if d.Body != "" {
		q += "\n" + d.Body
	}
	return s.f.Find(cwd, q, similarOf)
}
