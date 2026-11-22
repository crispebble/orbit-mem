// Package bench is the evaluation set for recall: it reads a case file, prepares a
// memory DB, splits the cases, runs a recall path over them and measures what it
// showed. It never calls a model. The recall path is a port: the adapters that
// implement Recaller, Finder and Similar live in sub packages and are put together
// by the caller.
package bench

import "time"

// Shown is what one recall step put in front of the model: the memory ids in the
// order shown, the characters of the text, and the text itself.
//
// A delegation bundle has two shares: the always-on title lines (the user's core, the guidance of the
// role) and the recall for the task. The path says which ids are the first with Lead (a subset of IDs)
// and how many of the characters they are with LeadChars; the rest is the recall share. A path with no
// such split leaves both empty and all of it counts as recall.
//
// A request hook has two shares too: the session news (B3, what changed in the session since its bundle: a
// replaced memory, one confirmed elsewhere) and the work recall (B1). The path says which ids are the news with
// News (a subset of IDs) and how many characters they take with NewsChars. The news is not a recall of the
// request: the measure counts its ids as shown but not as put in (설계 5.1 B3, 1-6 b6r 3차).
//
// Dead names the ids of IDs that were not a current value at the time of the call (DeadReason: stale, rejected,
// expired, or no longer in the DB): a dead injection (명세 · 기억 정확성 7절, F-6). A path that cannot tell leaves
// it empty; the measure counts only ids that are in IDs too. DeadWhy gives the reason of each dead id (a
// DeadReason value or DeadGone), so a later change of the judgement can be told apart from a change of the recall.
type Shown struct {
	IDs       []string
	Chars     int
	Text      string
	Lead      []string
	LeadChars int
	News      []string
	NewsChars int
	Dead      []string
	DeadWhy   map[string]string
}

// Recaller is the recall path under test. The origin of a case is the answer key and is
// never handed over: the path decides from the text whether a person asked.
type Recaller interface {
	Name() string
	// Start is the session start bundle (layer A).
	Start(session, cwd string) (Shown, error)
	// Request is the recall for a request (B1, B3).
	Request(session, cwd, prompt string) (Shown, error)
	// Tool is the recall just before a tool call (B2). input is the text of the call's input as
	// host.ToolInputText gives it ("" when the case has none); a path that does not read it ignores it.
	Tool(session, cwd, tool, input string) (Shown, error)
	// Subagent is the recall for a task handed to a sub agent.
	Subagent(session, cwd, task string) (Shown, error)
}

// Resetter is a recall path that can forget what it told a session (its bundle and marks), as a host does before
// the session start that follows a compaction or a clear. A start case with such a source (Case.Resets) needs it.
type Resetter interface {
	Reset(session string) error
}

// SourcedStarter is a recall path that takes how a session started, as a host hands it to its session start: the
// Source of a start case (StartSources), or "" for the start the run makes before the first case of a session whose
// log recorded no start. The run calls it in place of Recaller.Start when the path has it.
type SourcedStarter interface {
	StartFrom(session, cwd, source string) (Shown, error)
}

// Finder is the search a missed recall falls back to: the ids of the top limit hits.
// Each recall path brings its own: the frozen one keeps the search of its day (internal/bench/legacy).
type Finder interface {
	Find(cwd, query string, limit int) ([]string, error)
}

// Draft is a memory about to be saved, for the duplicate cases.
type Draft struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Topic string `json:"topic,omitempty"`
	Scope string `json:"scope,omitempty"`
}

// SaveMeets is what saving a draft meets: Block, the memories the save stops at for the caller to pick from
// (or merges into or replaces), and Near, the close memories shown beside a save that goes on. Each is
// ranked, best first; only the first SimilarLimit of each count.
type SaveMeets struct {
	Block []string
	Near  []string
}

// Similar gives what saving a draft meets.
type Similar interface {
	Similar(cwd string, d Draft) (SaveMeets, error)
}

// Env is one recall path opened on one DB copy.
type Env struct {
	Recaller Recaller
	Finder   Finder
	Similar  Similar
	// SetNow moves the clock of the path (recall, search, similar) to t. A set whose cases carry their times
	// (Set.Timed) needs it: the run calls it before a case whose time differs from the last one.
	SetNow func(t time.Time)
	Close  func() error
}

// Factory opens a recall path on a fresh copy of the prepared DB; every recall path
// gets its own copy so shown marks and activity never mix. The set carries the top
// level values the adapter needs (now, products).
type Factory func(dbPath string, set *Set) (Env, error)

// Limits of the measure.
const (
	FindLimit    = 8 // ids a Finder is asked for
	SimilarLimit = 3 // ids of Similar that count
	RequestTop   = 3 // ids of a request recall that count as shown
	MaxExpect    = 3
)
