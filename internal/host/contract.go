// Package host is orbit-mem's side of the host contract v1 (설계 · 지연 회상·호스트 계약 10절, ORBIT-23 D3):
// the one entry every host adapter calls, one JSON request in and one JSON result out per operation. It
// is a port: the memory, recall and save domain below it never learn a host's name or the shape of its
// input, and the adapters above it translate their host into these types. The host field of a request
// is kept in the records only; nothing is decided by it.
//
// The package reads no environment. The assembly point gives a Service the DB path, the settings, the
// upkeep port, the activity launcher and the default parent; whatever the JSON contract does not carry
// (the brief rule form, the writer of a call, the activity time) comes as Go options.
package host

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// Contract is the contract version this package speaks.
const Contract = 1

// Operations (설계 10.1).
const (
	OpVersion     = "version"
	OpContext     = "context"
	OpRecall      = "recall"
	OpToolContext = "tool-context"
	OpCall        = "call"
	OpDelivered   = "delivered"
	OpReset       = "reset"
	OpSessionEnd  = "session-end"
)

// Ops are the operations in the order of the contract table.
var Ops = []string{OpVersion, OpContext, OpRecall, OpToolContext, OpCall, OpDelivered, OpReset, OpSessionEnd}

// Context sources, audiences and request origins.
const (
	SourceStartup = "startup"
	SourceResume  = "resume"
	SourceFork    = "fork"
	SourceCompact = "compact"
	SourceClear   = "clear"

	AudienceMain     = "main"
	AudienceSubagent = "subagent"

	OriginUser   = "user"
	OriginAgent  = "agent"
	OriginSystem = "system"
)

// Skipped reasons of an empty recall-shaped result: the request was not a person's, or the DB is not
// one this binary reads (so the model does not take an empty result for "no memories").
const (
	SkippedOrigin = "origin"
)

// Request is one contract request: the common fields and those of every operation (an operation
// reads its own). Trust is one word (memory.Trusts): it lowers the trust limit of the call, never raises it. Profile, ReadScope, Sandboxed, Task and RequestID are taken
// and not used yet. IdempotencyKey goes into the args of call memory_save that have none (callArgs).
type Request struct {
	Contract  int             `json:"contract,omitempty"` // 없으면 1
	Host      string          `json:"host,omitempty"`     // 기록에만 쓴다
	Session   string          `json:"session,omitempty"`
	Agent     string          `json:"agent,omitempty"` // 서브에이전트 id
	Cwd       string          `json:"cwd,omitempty"`   // 세션 폴더
	Parent    string          `json:"parent,omitempty"`
	Profile   string          `json:"profile,omitempty"`
	Trust     json.RawMessage `json:"trust,omitempty"` // 글 하나(owner·agent·untrusted·system), 이번 호출의 상한을 낮춘다
	ReadScope json.RawMessage `json:"readScope,omitempty"`
	Sandboxed *bool           `json:"sandboxed,omitempty"`

	Source         string          `json:"source,omitempty"`   // context
	Audience       string          `json:"audience,omitempty"` // context, 없으면 main
	Task           string          `json:"task,omitempty"`     // context
	Prompt         string          `json:"prompt,omitempty"`   // recall
	Origin         string          `json:"origin,omitempty"`   // recall, 없으면 user
	RequestID      string          `json:"requestId,omitempty"`
	Tool           string          `json:"tool,omitempty"` // tool-context
	Name           string          `json:"name,omitempty"` // call
	Args           json.RawMessage `json:"args,omitempty"` // call
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	MemoryIDs      []string        `json:"memoryIds,omitempty"` // delivered
}

// Decode reads one contract request. Input that is not one JSON object is a usage error.
func Decode(b []byte) (Request, *Error) {
	var r Request
	if len(strings.TrimSpace(string(b))) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, &Error{Code: CodeUsage, Message: err.Error(), Detail: &Detail{Reason: DetailInput}}
	}
	return r, nil
}

// trustLevel is the trust the request lowers its call to: "" when the field is absent or null, else the
// one word of memory.Trusts. Another shape or an unknown word is a usage error.
func (r Request) trustLevel() (string, *Error) {
	raw := strings.TrimSpace(string(r.Trust))
	if raw == "" || raw == "null" {
		return "", nil
	}
	var v string
	if err := json.Unmarshal(r.Trust, &v); err != nil || !slices.Contains(memory.Trusts, v) {
		return "", usageError(DetailBadValue, "trust", raw)
	}
	return v, nil
}

// key is the session the marks and events of a request are kept under: a subagent has its own
// context, so it is told once more what the main session was told.
func (r Request) key() string {
	if r.Agent == "" || r.Session == "" {
		return r.Session
	}
	return r.Session + "/" + r.Agent
}

// Budget is how much of its limit a recall-shaped result used, in characters (runes).
type Budget struct {
	Chars int `json:"chars"`
	Limit int `json:"limit"`
}

// Recall is the result of context, recall and tool-context.
type Recall struct {
	Text      string   `json:"text"`
	MemoryIDs []string `json:"memoryIds"` // 빈 결과도 []
	Budget    Budget   `json:"budget"`
	Skipped   string   `json:"skipped,omitempty"`
}

// DBSchema is the DB's schema version and its state against this binary.
type DBSchema struct {
	Schema int    `json:"schema"`
	State  string `json:"state"`
}

// VersionOut is the result of version.
type VersionOut struct {
	Binary   string   `json:"binary"`
	Contract int      `json:"contract"`
	Schema   int      `json:"schema"`
	DB       DBSchema `json:"db"`
}

// CallOut is the result of call: the tool's own result, as the MCP tool gives it.
type CallOut struct {
	Name   string `json:"name"`
	Result any    `json:"result"`
}

// OK is the result of delivered, reset and session-end.
type OK struct {
	OK bool `json:"ok"`
}

// Error codes.
const (
	CodeUsage       = "usage"
	CodeContract    = "contract"
	CodeTimeout     = "timeout"
	CodeNoDB        = "no-db"
	CodeSchemaNewer = "schema-newer"
	CodeSchemaOlder = "schema-older"
	CodeUnknownTool = "unknown-tool"
	CodeTool        = "tool"
	CodeInternal    = "internal"
)

// Error is an operation's error: a code, a message and, for a tool, its name. Its JSON is the
// contract's {"error", "code", "name"?, "mayHaveApplied"?}.
//
// Message is not for people: the CLI words an error from its code and Detail in the user's
// language (a tool's own error and an internal one carry their cause in Message).
type Error struct {
	Message string `json:"error"`
	Code    string `json:"code"`
	Name    string `json:"name,omitempty"`
	// MayHaveApplied is set on the timeout of a write tool (memory_save, memory_update): the
	// computing goes on past the limit, so the write may have been committed. The caller looks
	// before it writes again (memory_find, memory_get).
	MayHaveApplied bool `json:"mayHaveApplied,omitempty"`
	// Detail is what a usage or contract error is about, in parts (not in the contract JSON).
	Detail *Detail `json:"-"`
}

// Detail is the reason of a usage or contract error and its arguments, for the CLI to word.
type Detail struct {
	Reason string
	Args   []string
}

// Detail reasons and their arguments.
const (
	DetailInput     = "input"      // 입력이 JSON 객체 하나가 아니다. 인자 없음
	DetailUnknownOp = "unknown-op" // 모르는 op. [op]
	DetailContract  = "contract"   // 계약 판이 다르다. [요청의 판, 이 실행 파일의 판]
	DetailBadValue  = "bad-value"  // 칸의 값을 모른다. [칸, 값]
	DetailMissing   = "missing"    // 꼭 있어야 할 칸이 없다. [칸...]
)

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// Exit is the exit code of the error: 2 for a usage error, 1 for any other.
func (e *Error) Exit() int {
	if e.Code == CodeUsage {
		return 2
	}
	return 1
}

// fail is an error whose message is its code (the CLI gives the words).
func fail(code string) *Error { return &Error{Code: code, Message: code} }

// callArgs are the call's args with the common idempotencyKey merged into those of memory_save when the
// args do not have their own (the args win, 설계 6.1 · 계획 D9). Args that are not one JSON object stay as
// they are: the tool refuses them.
func (r Request) callArgs() json.RawMessage {
	if r.Name != "memory_save" || strings.TrimSpace(r.IdempotencyKey) == "" {
		return r.Args
	}
	args := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(r.Args))) > 0 {
		if err := json.Unmarshal(r.Args, &args); err != nil || args == nil {
			return r.Args
		}
	}
	if _, ok := args["idempotencyKey"]; ok {
		return r.Args
	}
	args["idempotencyKey"], _ = json.Marshal(r.IdempotencyKey)
	b, err := json.Marshal(args)
	if err != nil {
		return r.Args
	}
	return b
}
