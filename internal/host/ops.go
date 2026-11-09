package host

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/project"
	"github.com/crispebble/orbit-mem/internal/version"
)

// call is one operation's state: the store it opened, the session's place and the records it
// leaves for after the result.
type call struct {
	svc      *Service
	req      Request
	opts     Options
	cfg      config.Config
	deadline time.Time
	store    *memory.Store // 이 실행 파일이 읽는 판(ok)으로 열었을 때만
	p        project.Project
	where    string
	request  string // 사람이 쓴 요청 글(recall 판정 뒤): 사용 사건의 「빈 요청」 판정이 본다
	after    []func()
}

func (c *call) late() bool { return !time.Now().Before(c.deadline) }

func (c *call) close() {
	if c.store != nil {
		c.store.Close()
	}
}

func (c *call) compute(op string) (any, *Error) {
	switch op {
	case OpVersion:
		return c.version()
	case OpContext:
		return c.context()
	case OpRecall:
		return c.recall()
	case OpToolContext:
		return c.toolContext()
	case OpCall:
		return c.tool()
	case OpDelivered:
		return c.delivered()
	case OpReset:
		return c.reset()
	case OpSessionEnd:
		return c.sessionEnd()
	}
	return nil, fail(CodeUsage)
}

// record runs the records the computing left, each while time is left. Their errors are dropped
// (설계 10.2: a mark that cannot be written is skipped, at the cost of one more injection).
func (c *call) record() {
	for _, f := range c.after {
		if c.late() {
			return
		}
		func() {
			defer func() { _ = recover() }()
			f()
		}()
	}
}

// fail keeps the diagnostic event of an operation that ended in an error or a timeout.
func (c *call) fail(op, code string) {
	if c.store == nil {
		return
	}
	defer func() { _ = recover() }()
	_ = c.store.RecordEvent(memory.Event{Kind: EventFail, Host: c.req.Host, Session: c.req.key(), Scope: c.p.Scope}, map[string]any{"op": op, "code": code})
}

func (c *call) adopt(s *memory.Store) {
	s.Policy = memory.Policy{SeenDays: c.cfg.ConfirmSeenDays, FollowedVerified: c.cfg.ConfirmFollowedVerified, CorrectionSessions: c.cfg.CorrectionRepeatSessions}
	if c.svc.setup.Now != nil {
		s.Now = c.svc.setup.Now
	}
	c.store = s
}

// stateCode is the error code of a DB this binary does not read ("" for one it reads).
func stateCode(st memory.SchemaState) string {
	switch st {
	case memory.SchemaOK:
		return ""
	case memory.SchemaNewer:
		return CodeSchemaNewer
	case memory.SchemaOlder:
		return CodeSchemaOlder
	default: // empty, missing
		return CodeNoDB
	}
}

// openRead opens the DB for a read operation: no migration, no copy, no new file.
func (c *call) openRead() *Error {
	s, info, err := memory.OpenRead(c.svc.setup.DB)
	if err != nil {
		if info.State == memory.SchemaMissing {
			return fail(CodeNoDB)
		}
		return &Error{Code: CodeInternal, Message: err.Error()}
	}
	if code := stateCode(info.State); code != "" {
		s.Close()
		return fail(code)
	}
	c.adopt(s)
	return c.inTime()
}

// openWrite opens the DB for a write tool: it migrates an older DB and makes a missing one,
// and never touches a newer one.
func (c *call) openWrite() *Error {
	s, err := memory.Open(c.svc.setup.DB)
	if errors.Is(err, memory.ErrSchemaNewer) {
		return &Error{Code: CodeSchemaNewer, Message: err.Error()}
	}
	if err != nil {
		return &Error{Code: CodeInternal, Message: err.Error()}
	}
	c.adopt(s)
	return c.inTime()
}

// inTime stops an operation whose time ran out while it opened the DB.
func (c *call) inTime() *Error {
	if c.late() {
		return fail(CodeTimeout)
	}
	return nil
}

// locate resolves the session folder to the place the recall reads.
func (c *call) locate() {
	if c.req.Cwd != "" {
		c.p = project.Resolve(c.req.Cwd)
	}
	place.Known(c.p)
	c.where = c.p.Dir
	if c.where == "" {
		c.where = c.p.Scope
	}
}

func recallOf(inj inject.Injection, limit int) Recall {
	ids := inj.MemoryIDs
	if ids == nil {
		ids = []string{}
	}
	return Recall{Text: inj.Text, MemoryIDs: ids, Budget: Budget{Chars: inj.Chars, Limit: limit}}
}

func skipped(why string, limit int) Recall {
	return Recall{MemoryIDs: []string{}, Budget: Budget{Limit: limit}, Skipped: why}
}

// openRecall opens the DB for a recall-shaped operation. A DB this binary does not read gives an
// empty result with the reason (it is not an error: the host goes on without memories).
func (c *call) openRecall(limit int) (any, *Error, bool) {
	err := c.openRead()
	if err == nil {
		return nil, nil, false
	}
	switch err.Code {
	case CodeNoDB, CodeSchemaNewer, CodeSchemaOlder:
		return skipped(err.Code, limit), nil, true
	}
	return nil, err, true
}

// shown leaves the marks and the use event of an injection (marks and events under the session key).
func (c *call) shown(kind string, inj inject.Injection) {
	c.after = append(c.after, func() {
		inject.RecordShown(c.store, c.req.Host, c.req.key(), inj)
		c.injected(kind, inj)
	})
}

// injected counts one injection as a use event. Levels that inject nothing and empty requests are
// not use; a tool call or a subagent counts only when something was put in.
func (c *call) injected(kind string, inj inject.Injection) {
	mode := c.cfg.RecallMode()
	if mode == config.RecallOff ||
		(kind == memory.EventInjectPrompt && (mode != config.RecallAuto || strings.TrimSpace(c.request) == "")) ||
		((kind == memory.EventInjectTool || kind == memory.EventInjectSubagent) && len(inj.MemoryIDs) == 0) {
		return
	}
	data := map[string]any{"memoryIds": inj.MemoryIDs, "chars": inj.Chars}
	if kind == memory.EventInjectSessionStart {
		data["omitted"] = inj.Omitted
	}
	_ = c.store.RecordEvent(memory.Event{Kind: kind, Host: c.req.Host, Session: c.req.key(), Scope: c.p.Scope}, data)
}

// activity keeps the session in the activity log (under the session, not the subagent).
func (c *call) activity(kind, prompt string) {
	c.after = append(c.after, func() {
		if c.req.Session == "" {
			return
		}
		_ = c.store.RecordActivity(c.activityEvent(kind, prompt))
	})
}

// endActivity keeps the end of the session in the activity log. A session whose row the start
// made gets only its end time (no project resolving: git stays out of the end's time limit);
// a session whose start was missed resolves its project here and gets its row.
func (c *call) endActivity() {
	c.after = append(c.after, func() {
		if c.req.Session == "" {
			return
		}
		e := c.activityEvent(memory.ActivityEnd, "")
		if found, err := c.store.EndActivity(e); err != nil || found {
			return
		}
		c.locate()
		e.ProjectScope, e.ProjectLabel = c.p.Scope, c.p.Label
		_ = c.store.RecordActivity(e)
	})
}

// activityEvent is this call as an activity log event, with the session's project as located
// and the request words the call keeps (empty: none).
func (c *call) activityEvent(kind, prompt string) memory.ActivityEvent {
	parent := c.req.Parent
	if parent == "" {
		parent = c.svc.setup.Parent
	}
	return memory.ActivityEvent{Kind: kind, Host: c.req.Host, Session: c.req.Session, Cwd: c.req.Cwd,
		ProjectScope: c.p.Scope, ProjectLabel: c.p.Label, Launcher: c.svc.setup.Launcher, Parent: parent, Prompt: prompt, At: c.opts.At}
}

func (c *call) version() (any, *Error) {
	out := VersionOut{Binary: version.Version, Contract: Contract, Schema: memory.SchemaVersion}
	s, info, err := memory.OpenReadOnly(c.svc.setup.DB)
	if err != nil && info.State != memory.SchemaMissing {
		return nil, &Error{Code: CodeInternal, Message: err.Error()}
	}
	if s != nil {
		s.Close()
	}
	out.DB = DBSchema{Schema: info.DB, State: string(info.State)}
	return out, nil
}

func (c *call) context() (any, *Error) {
	if c.req.Audience == AudienceSubagent {
		limit := inject.MaxSubagentRunes
		if out, err, done := c.openRecall(limit); done {
			return out, err
		}
		c.locate()
		inj := inject.SubagentStart(c.store, c.cfg, c.where, c.subagentTask())
		c.shown(memory.EventInjectSubagent, inj)
		return recallOf(inj, limit), nil
	}
	limit := inject.MaxSessionStartRunes
	if out, err, done := c.openRecall(limit); done {
		return out, err
	}
	c.locate()
	inj := inject.SessionBundleWith(c.store, c.cfg, c.where, c.req.Host, c.req.Session, c.opts.SessionNotes)
	c.shown(memory.EventInjectSessionStart, inj)
	c.activity(memory.ActivityStart, "")
	if u := c.svc.setup.Upkeep; u != nil {
		cfg := c.cfg
		c.after = append(c.after, func() { u.AfterSessionStart(c.store, cfg) })
	}
	return recallOf(inj, limit), nil
}

func (c *call) recall() (any, *Error) {
	limit := inject.MaxHookRunes
	if c.req.Origin != "" && c.req.Origin != OriginUser {
		return skipped(SkippedOrigin, limit), nil
	}
	// 훅 입력에는 출처 칸이 없어 호스트는 prompt 칸을 사람 글 후보로 넘긴다. 사람 요청인지는 여기서 꼴로 판정한다(DB를 열기 전).
	words, person := inject.PersonWords(c.req.Prompt)
	if !person {
		return skipped(SkippedNotPerson, limit), nil
	}
	text, trivial := inject.RequestText(words)
	if !trivial { // 요청 회상이 돌지 않으면 요청 회상 사용도 아니다
		c.request = text
	}
	if out, err, done := c.openRecall(limit); done {
		return out, err
	}
	c.locate()
	// 사소한 요청은 요청 회상만 건너뛰고 세션 중 바뀐 것은 싣는다.
	inj := inject.PromptInjectionFor(c.store, c.cfg, c.where, text, c.req.Host, c.req.Session, inject.Options{NewsOnly: trivial})
	c.shown(memory.EventInjectPrompt, inj)
	c.activity(memory.ActivityPrompt, words)
	return recallOf(inj, limit), nil
}

func (c *call) toolContext() (any, *Error) {
	limit := inject.MaxToolRunes
	if out, err, done := c.openRecall(limit); done {
		return out, err
	}
	c.delegate()
	c.locate()
	inj := inject.ToolInjection(c.store, c.cfg, c.where, c.req.Host, c.req.key(), c.req.Tool)
	c.shown(memory.EventInjectTool, inj)
	return recallOf(inj, limit), nil
}

// writeTools are the call tools that write: they open the DB for writing (migrating it).
var writeTools = []string{"memory_save", "memory_update"}

func (c *call) tool() (any, *Error) {
	name := c.req.Name
	if !slices.Contains(mcpserver.HostTools, name) {
		return nil, &Error{Code: CodeUnknownTool, Message: CodeUnknownTool + ": " + name, Name: name}
	}
	var err *Error
	switch {
	case !slices.Contains(writeTools, name):
		err = c.openRead()
	case name == "memory_save": // 첫 저장은 DB를 만든다
		err = c.openWrite()
	default:
		if _, serr := os.Stat(c.svc.setup.DB); errors.Is(serr, fs.ErrNotExist) {
			err = fail(CodeNoDB)
		} else {
			err = c.openWrite()
		}
	}
	if err != nil {
		if err.Code == CodeSchemaNewer || err.Code == CodeSchemaOlder || err.Code == CodeNoDB {
			err.Name = name
		}
		return nil, err
	}
	c.locate()
	actor := c.opts.Actor
	if actor == "" {
		actor = memory.ActorAI
	}
	t := mcpserver.NewTools(c.store, mcpserver.Options{Project: c.p.Scope, ProjectLabel: c.p.Label, Dir: c.p.Dir, Git: c.p.Git, Temp: c.p.Temp,
		Actor: actor, TrustLimit: c.opts.TrustLimit, Products: c.cfg.Products, SaveSimilarMin: c.cfg.SaveSimilarMin})
	if lvl, _ := c.req.trustLevel(); lvl != "" { // check가 이미 걸러 냈다
		var lerr error
		if t, lerr = t.Lower(lvl); lerr != nil {
			return nil, &Error{Code: CodeInternal, Message: lerr.Error()}
		}
	}
	out, terr := t.Call(name, c.req.callArgs(), c.req.Host, c.req.key())
	if terr != nil {
		return nil, &Error{Code: CodeTool, Message: terr.Error(), Name: name}
	}
	return CallOut{Name: name, Result: out}, nil
}

func (c *call) delivered() (any, *Error) {
	if err := c.openRead(); err != nil {
		return nil, err
	}
	if err := c.store.RecordEvent(memory.Event{Kind: memory.EventInjectDelivered, Host: c.req.Host, Session: c.req.key()}, map[string]any{"memoryIds": c.req.MemoryIDs}); err != nil {
		return nil, &Error{Code: CodeInternal, Message: err.Error()}
	}
	return OK{OK: true}, nil
}

// maxTaskRunes is how much of a delegation's text is kept: its start, enough for the recall's query
// (at most 40 meaningful words) and the reasons.
const maxTaskRunes = 2000

// delegate keeps the task of a tool-context request (a call that hands work to a subagent) for the
// subagent of its kind that starts next in the session (설계 · orbit-mem 4.1). It is written before the
// result, so a subagent that starts right after finds it. A task that cannot be kept is dropped: that
// subagent gets the core title lines alone.
func (c *call) delegate() {
	task := strings.TrimSpace(c.req.Task)
	if task == "" {
		return
	}
	if r := []rune(task); len(r) > maxTaskRunes {
		task = string(r[:maxTaskRunes])
	}
	_ = c.store.SaveDelegation(c.req.Host, c.req.Session, c.req.AgentKind, task)
}

// subagentTask is the task of a starting subagent: the request's own, else the delegation of the session
// and kind that came first and no other subagent took (it takes it), else none.
func (c *call) subagentTask() string {
	if task := strings.TrimSpace(c.req.Task); task != "" {
		return task
	}
	task, _, _ := c.store.TakeDelegation(c.req.Host, c.req.Session, c.req.AgentKind, c.req.Agent)
	return task
}

func (c *call) reset() (any, *Error) {
	if err := c.openRead(); err != nil {
		return nil, err
	}
	// 표시를 지우지 못한 reset을 성공으로 숨기지 않는다: 숨기면 압축 뒤 회상에서 기억이 「이미 보인 것」으로 빠진다.
	if err := c.store.ResetSession(c.req.Host, c.req.Session); err != nil {
		return nil, &Error{Code: CodeInternal, Message: err.Error()}
	}
	return OK{OK: true}, nil
}

func (c *call) sessionEnd() (any, *Error) {
	if err := c.openRead(); err != nil {
		return nil, err
	}
	// 이 세션이 맡기고 아무도 가져가지 않은 위임 글도, 가져간 글도 세션과 함께 끝난다.
	if err := c.store.DropDelegations(c.req.Host, c.req.Session); err != nil {
		return nil, &Error{Code: CodeInternal, Message: err.Error()}
	}
	c.endActivity()
	return OK{OK: true}, nil
}
