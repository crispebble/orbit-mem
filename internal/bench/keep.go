package bench

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Kinds are the case kinds a set may hold, in the order a filter names them.
var Kinds = []string{KindRequest, KindSubagent, KindDuplicate, KindStart}

// standInSuffix ends the id of the start case Keep puts where the run would start a session it left no case for.
const standInSuffix = "+start"

// Keep narrows the set to the cases of the sessions (SessionKey) and of the kinds given, in file order, and reports
// how many cases are left. An empty list keeps every session or every kind. A replay case file of every session is
// large; a part of it measures some sessions, or one path, at a fraction of the time.
//
// With the start kind kept, every session start the run would make stays at the same time and in the same order:
// when the first case of a session that calls the session start (any case but a duplicate) is dropped and is not a
// start case itself, a start case with no source takes its place, with its session, folder and time and the id
// "<id>+start" (the run starts a session before its first case, Run). Without the start kind, the run still starts
// each session before its first kept case, at that case's time.
//
// A kind it does not know, a session no case has, an id the stand-in start would repeat, or a filter that keeps no
// case is an error, and the set is left as it was.
func (s *Set) Keep(sessions, kinds []string) (int, error) {
	for _, k := range kinds {
		if !slices.Contains(Kinds, k) {
			return 0, fmt.Errorf("kind %q (%s)", k, strings.Join(Kinds, ", "))
		}
	}
	keepKind := func(k string) bool { return len(kinds) == 0 || slices.Contains(kinds, k) }
	keepSession := func(key string) bool { return len(sessions) == 0 || slices.Contains(sessions, key) }
	ids := map[string]bool{}
	has := map[string]bool{}
	for _, c := range s.Cases {
		ids[c.ID] = true
		has[c.SessionKey()] = true
	}
	for _, key := range sessions {
		if !has[key] {
			return 0, fmt.Errorf("no case of session %q", key)
		}
	}
	started := map[string]bool{}
	var out []Case
	for _, c := range s.Cases {
		key := c.SessionKey()
		if !keepSession(key) {
			continue
		}
		k := c.CaseKind()
		first := k != KindDuplicate && !started[key]
		if first {
			started[key] = true
		}
		if keepKind(k) {
			out = append(out, c)
			continue
		}
		if first && k != KindStart && keepKind(KindStart) {
			stand := Case{ID: c.ID + standInSuffix, Session: c.Session, CWD: c.CWD, Kind: KindStart, At: c.At}
			if ids[stand.ID] {
				return 0, fmt.Errorf("case %q: the stand-in start would repeat the id %q", c.ID, stand.ID)
			}
			out = append(out, stand)
		}
	}
	if len(out) == 0 {
		return 0, errors.New("no case is left")
	}
	s.Cases = out
	return len(out), nil
}

// ProductsOf reads the product map of a settings file (its "products", as the file writes it) for a set that has
// none: a replay set from logs carries no products, and without them no product's memories are in the recall of a
// product folder. The map is handed on as it is; the recall path checks it. A file that cannot be read, is not a
// JSON object or has no products is an error.
func ProductsOf(path string) (json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p, ok := top["products"]
	if !ok || string(p) == "null" {
		return nil, fmt.Errorf("%s: no products", path)
	}
	return p, nil
}
