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
	"errors"
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
		r := &recaller{svc: svc, at: c, db: db}
		return bench.Env{
			Recaller: r,
			Finder:   finder{svc: svc, at: c},
			Similar:  similar{db: db, at: c, mins: memory.CheckMins{Similar: cfg.SaveSimilarMin, Near: cfg.SaveNearMin}},
			SetNow:   func(t time.Time) { c.t = t },
			Close: func() error {
				svc.Wait()
				return r.close()
			},
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
	db     string
	read   *memory.Store // read only, opened at the first call that put an id in
}

func (*recaller) Name() string { return Name }

func (r *recaller) close() error {
	if r.read == nil {
		return nil
	}
	return r.read.Close()
}

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
	// 건너뛴 호출만 빈 결과다. id 없이 글만 있는 주입(찾는 곳 줄·알림 줄)도 모델 앞에 놓인 글이라 글자로 센다.
	if rec.Skipped != "" {
		return bench.Shown{IDs: []string{}}, nil
	}
	ids := rec.MemoryIDs
	if ids == nil {
		ids = []string{}
	}
	shown := bench.Shown{IDs: ids, Chars: len([]rune(rec.Text)), Text: rec.Text}
	if op == host.OpRecall { // 요청 훅: 앞의 「이 세션 중 바뀐 것」(B3) 몫을 회상(B1)과 나눈다
		shown.News, shown.NewsChars = inject.NewsShare(rec.Text, ids)
	}
	var err error
	if shown.Dead, shown.DeadWhy, err = r.dead(ids); err != nil {
		return bench.Shown{}, fmt.Errorf("hostpath: %s: %w", op, err)
	}
	return shown, nil
}

// dead are the ids that were not a current value at the path's time, with the reason of each (memory.Viewer, in
// the words of bench.DeadReason),
// read from the DB copy as it is after the call: a frozen session bundle (0.19.0 hands it back as it was) can hold
// a memory that went stale since. An id the copy does not have is dead too (bench.DeadGone): it was forgotten
// before the call, which is how the audit counts gone as well.
func (r *recaller) dead(ids []string) ([]string, map[string]string, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	if r.read == nil {
		s, _, err := memory.OpenReadOnly(r.db)
		if err != nil {
			return nil, nil, err
		}
		s.Now = r.at.now // 판정 시각은 경로의 시계다(Freshness가 그 시각으로 만료를 가른다)
		r.read = s
	}
	vw, _ := r.read.Viewer()
	var out []string
	var why map[string]string
	for _, id := range ids {
		m, err := r.read.Get(id)
		w := ""
		switch {
		case errors.Is(err, memory.ErrNotFound):
			w = bench.DeadGone
		case err != nil:
			return nil, nil, err
		default:
			w = deadWhy(vw.Of(m).Why)
		}
		if w == "" {
			continue
		}
		if why == nil {
			why = map[string]string{}
		}
		out, why[id] = append(out, id), w
	}
	return out, why, nil
}

// deadWhy maps the Viewer's verdict to the reason a baseline names (bench.DeadReason): stale, hidden, expired.
// An older value of a topic (memory.WhyOlder) is not dead here: the baseline never counted it, and counting it
// would make the numbers of before and after this judge incomparable.
func deadWhy(w memory.Why) string {
	switch w {
	case memory.WhyStale:
		return bench.DeadStale
	case memory.WhyRejected:
		return bench.DeadHidden
	case memory.WhyExpired:
		return bench.DeadExpired
	case memory.WhyGone:
		return bench.DeadGone
	}
	return ""
}

// Reset forgets what the session was told (its bundle and marks), as the hook does before the start that follows
// a compaction or a clear (hook/claudecode: reset, then context).
func (r *recaller) Reset(session string) error {
	out, herr := r.svc.Do(host.OpReset, host.Request{Host: hostName, Session: session}, host.Options{At: r.at.ms()})
	if herr != nil {
		return fmt.Errorf("hostpath: %s: %w", host.OpReset, herr)
	}
	if _, ok := out.(host.OK); !ok {
		return fmt.Errorf("hostpath: %s: result is %T", host.OpReset, out)
	}
	return nil
}

func (r *recaller) Start(session, cwd string) (bench.Shown, error) {
	return r.do(host.OpContext, host.Request{Session: session, Cwd: cwd, Source: host.SourceStartup, Audience: host.AudienceMain})
}

// Request always hands origin user: the origin of a case is the answer key, and the hook hands
// user too. When the contract learns to tell a person's request apart (1-4), change it here.
func (r *recaller) Request(session, cwd, prompt string) (bench.Shown, error) {
	return r.do(host.OpRecall, host.Request{Session: session, Cwd: cwd, Prompt: prompt, Origin: host.OriginUser})
}

// Tool is the recall before a tool call. input (the case's host.ToolInputText) goes in the contract's toolInput as
// the hooks put it; a case without one is recalled by the tool's name, as from a host that gives no input.
func (r *recaller) Tool(session, cwd, tool, input string) (bench.Shown, error) {
	return r.do(host.OpToolContext, host.Request{Session: session, Cwd: cwd, Tool: tool, ToolInput: input})
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
// the memories of the same scope that are like the draft (top 3) are the block. A save that goes on has the
// close memories memory_save shows beside it (saveNearMin, top 3) as its near ids.
type similar struct {
	db   string
	at   *clock
	mins memory.CheckMins
}

func (similar) Name() string { return SimilarName }

func (s similar) Similar(cwd string, d bench.Draft) (bench.SaveMeets, error) {
	st, _, err := memory.OpenRead(s.db)
	if err != nil {
		return bench.SaveMeets{}, fmt.Errorf("hostpath: save check: %w", err)
	}
	defer st.Close()
	at := s.at.now()
	st.Now = func() time.Time { return at }
	res, err := st.SaveCheck(memory.SaveInput{Kind: d.Kind, Title: d.Title, Body: d.Body, Topic: d.Topic, Scope: d.Scope,
		Source: memory.Source{Kind: memory.SourceQuote}}, s.mins)
	if err != nil {
		return bench.SaveMeets{}, fmt.Errorf("hostpath: save check: %w", err)
	}
	block := slices.Clone(res.Existing)
	for _, m := range res.Similar {
		block = append(block, m.ID)
	}
	var near []string
	for _, m := range res.Near {
		near = append(near, m.ID)
	}
	return bench.SaveMeets{Block: top(block), Near: top(near)}, nil
}

// top is the first similarOf of ids.
func top(ids []string) []string {
	if len(ids) > similarOf {
		return ids[:similarOf]
	}
	return ids
}
