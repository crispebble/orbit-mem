package host

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// Upkeep is the port of what runs after a main session's context (the daily copy and tidy the
// assembly point starts). It runs in the record stage, so it ends before the operation does.
type Upkeep interface {
	AfterSessionStart(s *memory.Store, cfg config.Config)
}

// UpkeepFunc is an Upkeep from a function.
type UpkeepFunc func(s *memory.Store, cfg config.Config)

func (f UpkeepFunc) AfterSessionStart(s *memory.Store, cfg config.Config) { f(s, cfg) }

// Setup is what the assembly point gives a Service: nothing in this package reads the environment.
type Setup struct {
	DB         string                        // 기억 DB 경로
	LoadConfig func() (config.Config, error) // op마다 부른다. 없으면 기본 설정
	Upkeep     Upkeep                        // 없어도 된다
	Launcher   string                        // 활동 기록의 launcher
	Parent     string                        // 요청에 parent가 없을 때의 활동 부모
	Now        func() time.Time              // 기억 저장소의 시계와 활동 시각의 기본값. 없으면 time.Now
}

// Options are the choices of one run that the JSON contract does not carry.
type Options struct {
	Brief bool   // 확정 규칙을 짧은 꼴로(inject.Options.Brief)
	Actor string // call로 쓰는 쪽. 없으면 AI
	At    int64  // 활동 시각(Unix ms). 0이면 Start 때
}

// EventFail is the diagnostic event of an operation that ended in an error or a timeout
// (data {op, code}). It is kept only in a DB this binary reads, and only when it can be written.
const EventFail = "host.fail"

// Service runs contract operations over one memory DB.
type Service struct {
	setup Setup
	wg    sync.WaitGroup
}

// New makes a Service.
func New(s Setup) *Service { return &Service{setup: s} }

// Wait waits until every operation this Service started has ended, its record stage included.
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) now() time.Time {
	if s.setup.Now != nil {
		return s.setup.Now()
	}
	return time.Now()
}

func (s *Service) config() (config.Config, error) {
	if s.setup.LoadConfig == nil {
		return config.Default(), nil
	}
	return s.setup.LoadConfig()
}

// Run is one operation under way. Its result comes as soon as the computing is done (Result);
// the records (marks, events, activity, upkeep) follow within the time left (Recorded).
type Run struct {
	deadline time.Time
	write    bool // 쓰기 도구의 call: 그 timeout은 반영됐을 수 있다
	state    atomic.Int32
	res      chan outcome
	once     sync.Once
	out      any
	err      *Error
	recorded chan struct{}
}

type outcome struct {
	out any
	err *Error
}

const (
	pending int32 = iota
	inTime
	late
)

// Result waits for the result until the time limit. A result computed after the limit is a timeout too.
func (r *Run) Result() (any, *Error) {
	r.once.Do(func() {
		timer := time.NewTimer(time.Until(r.deadline))
		defer timer.Stop()
		var o outcome
		select {
		case o = <-r.res:
		case <-timer.C:
			if r.state.CompareAndSwap(pending, late) {
				r.err = r.timeout()
				return
			}
			o = <-r.res // 계산이 한도 안에 끝났다고 이미 정했다: 곧 온다
		}
		r.out, r.err = o.out, o.err
	})
	return r.out, r.err
}

// Recorded is closed when the record stage has ended.
func (r *Run) Recorded() <-chan struct{} { return r.recorded }

// Deadline is when the operation's time limit runs out.
func (r *Run) Deadline() time.Time { return r.deadline }

// Finish waits for the result, then for the records until the time limit.
func (r *Run) Finish() {
	r.Result()
	timer := time.NewTimer(time.Until(r.deadline))
	defer timer.Stop()
	select {
	case <-r.recorded:
	case <-timer.C:
	}
}

// timeout is the run's timeout error. The computing of a write tool goes on past the limit and
// may still commit, so its timeout says the write may have been applied.
func (r *Run) timeout() *Error {
	e := fail(CodeTimeout)
	e.MayHaveApplied = r.write
	return e
}

// settle decides whether the computed result is in time and hands it to Result. It reports
// whether the result counts (false: a timeout).
func (r *Run) settle(out any, err *Error) bool {
	if time.Now().After(r.deadline) {
		r.state.CompareAndSwap(pending, late)
	} else {
		r.state.CompareAndSwap(pending, inTime)
	}
	if r.state.Load() == late {
		r.res <- outcome{err: r.timeout()}
		return false
	}
	if err != nil && err.Code == CodeTimeout { // 계산이 한도에 걸려 스스로 멈췄다
		err = r.timeout()
	}
	r.res <- outcome{out: out, err: err}
	return true
}

// done is a run that ended before it started (a usage or contract error).
func done(err *Error) *Run {
	r := &Run{deadline: time.Now(), res: make(chan outcome, 1), recorded: make(chan struct{})}
	r.state.Store(inTime)
	r.res <- outcome{err: err}
	close(r.recorded)
	return r
}

// Do runs an operation: its result, after the records have ended or the time limit has run out.
func (s *Service) Do(op string, req Request, o Options) (any, *Error) {
	r := s.Start(op, req, o)
	out, err := r.Result()
	r.Finish()
	return out, err
}

// Start starts an operation. The request is checked first (usage and contract errors come back at
// once); then the operation runs in its own goroutine within the time limit of its op (hostTimeouts).
//
// Settings that cannot be read stop the recall-shaped operations (an internal error: the defaults
// would turn on a recall the person turned off), while version and call go on with the defaults
// (a broken settings file does not block the memory tools, as the old tool command did not).
func (s *Service) Start(op string, req Request, o Options) *Run {
	if !slices.Contains(Ops, op) {
		return done(usageError(DetailUnknownOp, op))
	}
	if req.Contract == 0 {
		req.Contract = Contract
	}
	if req.Contract != Contract {
		return done(&Error{Code: CodeContract, Message: fmt.Sprintf("contract %d is not %d", req.Contract, Contract),
			Detail: &Detail{Reason: DetailContract, Args: []string{strconv.Itoa(req.Contract), strconv.Itoa(Contract)}}})
	}
	if err := check(op, &req); err != nil {
		return done(err)
	}
	cfg, cerr := s.config()
	if cerr != nil {
		if op != OpVersion && op != OpCall {
			return done(&Error{Code: CodeInternal, Message: cerr.Error()})
		}
		cfg = config.Default()
	}
	if o.At == 0 {
		o.At = s.now().UnixMilli()
	}
	r := &Run{deadline: time.Now().Add(cfg.HostTimeout(op)), write: op == OpCall && slices.Contains(writeTools, req.Name),
		res: make(chan outcome, 1), recorded: make(chan struct{})}
	s.wg.Add(1)
	go s.run(r, op, req, o, cfg)
	return r
}

func (s *Service) run(r *Run, op string, req Request, o Options, cfg config.Config) {
	defer s.wg.Done()
	defer close(r.recorded)
	c := &call{svc: s, req: req, opts: o, cfg: cfg, deadline: r.deadline}
	defer c.close()
	var out any
	var err *Error
	func() {
		defer func() {
			if p := recover(); p != nil {
				out, err = nil, &Error{Code: CodeInternal, Message: fmt.Sprint(p)}
			}
		}()
		out, err = c.compute(op)
	}()
	if !r.settle(out, err) {
		c.fail(op, CodeTimeout)
		return
	}
	if err != nil {
		c.fail(op, err.Code)
		return
	}
	c.record()
}

// usageError is a usage error about reason and its arguments. Its message is the same in parts,
// for a reader of the Go error (the CLI words it from Detail).
func usageError(reason string, args ...string) *Error {
	msg := reason
	if len(args) > 0 {
		msg += ": " + strings.Join(args, ", ")
	}
	return &Error{Code: CodeUsage, Message: msg, Detail: &Detail{Reason: reason, Args: args}}
}

// check reads the fields an operation needs; a missing or unknown value is a usage error.
func check(op string, req *Request) *Error {
	switch op {
	case OpContext:
		if !slices.Contains([]string{"", SourceStartup, SourceResume, SourceFork, SourceCompact, SourceClear}, req.Source) {
			return usageError(DetailBadValue, "source", req.Source)
		}
		if !slices.Contains([]string{"", AudienceMain, AudienceSubagent}, req.Audience) {
			return usageError(DetailBadValue, "audience", req.Audience)
		}
	case OpRecall:
		if !slices.Contains([]string{"", OriginUser, OriginAgent, OriginSystem}, req.Origin) {
			return usageError(DetailBadValue, "origin", req.Origin)
		}
	case OpCall:
		if req.Name == "" {
			return usageError(DetailMissing, "name")
		}
	case OpDelivered:
		var ids []string
		for _, id := range req.MemoryIDs {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		var missing []string
		if req.Session == "" {
			missing = append(missing, "session")
		}
		if len(ids) == 0 {
			missing = append(missing, "memoryIds")
		}
		if len(missing) > 0 {
			return usageError(DetailMissing, missing...)
		}
		req.MemoryIDs = ids
	}
	return nil
}
