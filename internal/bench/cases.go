package bench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// Case kinds and origins.
const (
	KindRequest   = "request"
	KindSubagent  = "subagent"
	KindDuplicate = "duplicate"

	OriginUser   = "user"
	OriginAgent  = "agent"
	OriginSystem = "system"
)

// ScopeHere in a memory means the project of the set's cwd.
const ScopeHere = "here"

// Seed fixes the split. A file may give it as a string or a number.
type Seed string

// UnmarshalJSON accepts a string or a number.
func (s *Seed) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = Seed(str)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return errors.New("seed must be a string or a number")
	}
	*s = Seed(n.String())
	return nil
}

// MemorySpec is one fake memory of a case file.
type MemorySpec struct {
	Key             string   `json:"key"`
	Kind            string   `json:"kind"`
	Scope           string   `json:"scope"`
	Quote           bool     `json:"quote,omitempty"`
	Title           string   `json:"title"`
	Body            string   `json:"body,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Retention       string   `json:"retention,omitempty"`
	TTLDays         int      `json:"ttlDays,omitempty"`
	ReviewAfterDays int      `json:"reviewAfterDays,omitempty"`
	Topic           string   `json:"topic,omitempty"`
	AgeDays         int      `json:"ageDays,omitempty"`
	Followed        int      `json:"followed,omitempty"` // how many times the retrospective judged it followed
	Pinned          bool     `json:"pinned,omitempty"`
	Tools           []string `json:"tools,omitempty"`
	State           string   `json:"state,omitempty"` // empty: confirmed for a quote, else candidate
}

// Case is one case. Expect, Allow and Forbid name memories by key (with memories in the
// file) or by id (with a DB given).
type Case struct {
	ID      string   `json:"id"`
	Session string   `json:"session,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
	Origin  string   `json:"origin,omitempty"` // the answer: who wrote the prompt
	Kind    string   `json:"kind,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Tool    string   `json:"tool,omitempty"`
	Task    string   `json:"task,omitempty"`
	Save    *Draft   `json:"save,omitempty"`
	Expect  []string `json:"expect,omitempty"`
	Allow   []string `json:"allow,omitempty"`
	Forbid  []string `json:"forbid,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// Set is a case file.
type Set struct {
	About    string          `json:"about,omitempty"`
	Now      string          `json:"now"`
	CWD      string          `json:"cwd,omitempty"`
	Seed     Seed            `json:"seed,omitempty"`
	Products json.RawMessage `json:"products,omitempty"` // opaque: the adapter puts it in its settings
	Memories []MemorySpec    `json:"memories,omitempty"`
	Cases    []Case          `json:"cases"`

	now time.Time
}

// At is the fixed now of the set.
func (s *Set) At() time.Time { return s.now }

// CaseKind is the kind with its default.
func (c Case) CaseKind() string {
	if c.Kind == "" {
		return KindRequest
	}
	return c.Kind
}

// CaseOrigin is the origin with its default.
func (c Case) CaseOrigin() string {
	if c.Origin == "" {
		return OriginUser
	}
	return c.Origin
}

// hasPrompt reports whether the case has a request text, the only case where the origin
// (who wrote that text) means anything. Tool-only, delegation and duplicate cases do not.
func (c Case) hasPrompt() bool {
	return c.CaseKind() == KindRequest && c.Prompt != ""
}

// SessionKey is what a case is grouped by: its session, or its id without one.
func (c Case) SessionKey() string {
	if c.Session != "" {
		return c.Session
	}
	return c.ID
}

// CWDOf is the working folder of a case.
func (s *Set) CWDOf(c Case) string {
	if c.CWD != "" {
		return c.CWD
	}
	return s.CWD
}

// Read reads and checks a case file.
func Read(path string) (*Set, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse reads and checks a case file: unknown keys, a missing now, a bad kind or origin,
// more than three expect, an expect on a case a person did not write, a repeated case id
// or memory key, a reference to a memory the file does not have, and an id both expected
// and forbidden are errors.
func Parse(b []byte) (*Set, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var s Set
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the case file")
	}
	var err error
	if s.now, err = time.Parse(time.RFC3339, s.Now); err != nil {
		return nil, fmt.Errorf("now must be RFC3339: %q", s.Now)
	}
	keys := map[string]bool{}
	for i, m := range s.Memories {
		if m.Key == "" || m.Title == "" || m.Kind == "" || m.Scope == "" {
			return nil, fmt.Errorf("memory %d: key, kind, scope and title are needed", i+1)
		}
		if keys[m.Key] {
			return nil, fmt.Errorf("memory key %q is repeated", m.Key)
		}
		keys[m.Key] = true
		if !slices.Contains(memory.Kinds, m.Kind) {
			return nil, fmt.Errorf("memory %q: kind %q", m.Key, m.Kind)
		}
		if m.State != "" && !slices.Contains(memory.States, m.State) {
			return nil, fmt.Errorf("memory %q: state %q", m.Key, m.State)
		}
		if m.Scope == ScopeHere && s.CWD == "" {
			return nil, fmt.Errorf("memory %q: scope here needs the cwd of the set", m.Key)
		}
		if m.AgeDays < 0 || m.Followed < 0 {
			return nil, fmt.Errorf("memory %q: negative days or count", m.Key)
		}
	}
	ids := map[string]bool{}
	for i, c := range s.Cases {
		if c.ID == "" {
			return nil, fmt.Errorf("case %d: no id", i+1)
		}
		if ids[c.ID] {
			return nil, fmt.Errorf("case id %q is repeated", c.ID)
		}
		ids[c.ID] = true
		if err := s.checkCase(c, keys); err != nil {
			return nil, fmt.Errorf("case %q: %w", c.ID, err)
		}
	}
	return &s, nil
}

func (s *Set) checkCase(c Case, keys map[string]bool) error {
	switch c.CaseOrigin() {
	case OriginUser, OriginAgent, OriginSystem:
	default:
		return fmt.Errorf("origin %q", c.Origin)
	}
	if len(c.Expect) > MaxExpect {
		return fmt.Errorf("%d expect, at most %d", len(c.Expect), MaxExpect)
	}
	if c.hasPrompt() && c.CaseOrigin() != OriginUser && len(c.Expect) > 0 {
		return errors.New("a prompt a person did not write cannot have an expect")
	}
	switch c.CaseKind() {
	case KindRequest:
		if c.Prompt == "" && c.Tool == "" {
			return errors.New("a request case needs a prompt or a tool")
		}
	case KindSubagent:
		if c.Task == "" {
			return errors.New("a subagent case needs a task")
		}
	case KindDuplicate:
		if c.Save == nil || c.Save.Title == "" || c.Save.Kind == "" {
			return errors.New("a duplicate case needs save with a kind and a title")
		}
	default:
		return fmt.Errorf("kind %q", c.Kind)
	}
	if s.CWDOf(c) == "" {
		return errors.New("no cwd (neither the case nor the set has one)")
	}
	for _, r := range c.Expect {
		if slices.Contains(c.Forbid, r) {
			return fmt.Errorf("%q is both expect and forbid", r)
		}
	}
	for _, r := range c.Allow {
		if slices.Contains(c.Forbid, r) {
			return fmt.Errorf("%q is both allow and forbid", r)
		}
	}
	if len(s.Memories) > 0 {
		for _, list := range [][]string{c.Expect, c.Allow, c.Forbid} {
			for _, r := range list {
				if !keys[r] {
					return fmt.Errorf("no memory with key %q", r)
				}
			}
		}
	}
	return nil
}

// memoryID is the fixed id of the n-th memory (1 based) of a file.
func memoryID(n int) string { return fmt.Sprintf("m-%012d", n) }
