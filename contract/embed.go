// Package contract carries the pairs of the host contract (설계 · 지연 회상·호스트 계약 10.2): files that fix,
// for each operation, one input and the output (and exit code) it must give. The Go tests of
// orbit-mem and the TypeScript adapter tests read these same files, so the two sides cannot drift.
//
// A pair is {name, op, setup, input, exit, output}:
//
//   - setup.db: "none" (no database file; the default), "empty" (a database of the current
//     schema with nothing saved) or "memories" (like empty, then setup.calls make the memories).
//   - setup.calls: contract calls run in order before the pair, each {op, input} (for example a
//     call to memory_save). They need no database of their own: memory_save makes it.
//   - setup.meta: stored settings of the database written after the calls (for example
//     {"schema.version": "99"} makes a newer database).
//   - setup.config: the settings file's content (for example {"hostTimeouts": {"context": "1ns"}}).
//   - input: the request JSON given to the operation on stdin. A JSON string is the raw stdin
//     text instead (how a pair gives input that is not JSON).
//   - "$calls[N].path": a string value in an input (or in a later setup call's input) that is
//     the value at path in the output of setup.calls[N] (the JSON it printed), for example
//     "$calls[0].result.memory.id". The path is keys after dots and numbers in brackets only;
//     a path that leads nowhere fails the pair. Only calls before the one that uses it count.
//   - exit: the exit code of `orbit-mem host <op>`.
//   - output: the JSON the operation prints, compared exactly, except that the strings "$string",
//     "$number", "$array" and "$any" stand for any value of that type (any value, for "$any").
package contract

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed host/v1/*.json
var hostV1 embed.FS

// Pair is one pair file.
type Pair struct {
	Name   string          `json:"name"`
	Op     string          `json:"op"`
	Setup  Setup           `json:"setup"`
	Input  json.RawMessage `json:"input"`
	Exit   int             `json:"exit"`
	Output any             `json:"output"`
}

// Setup is what a pair needs before it runs.
type Setup struct {
	DB     string            `json:"db,omitempty"`
	Calls  []Call            `json:"calls,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
	Config map[string]any    `json:"config,omitempty"`
}

// Call is one contract call made before a pair.
type Call struct {
	Op    string          `json:"op"`
	Input json.RawMessage `json:"input"`
}

// HostV1 reads every pair of the host contract v1, ordered by file name.
func HostV1() ([]Pair, error) {
	names, err := fs.Glob(hostV1, "host/v1/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var pairs []Pair
	for _, n := range names {
		b, err := hostV1.ReadFile(n)
		if err != nil {
			return nil, err
		}
		var p Pair
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		pairs = append(pairs, p)
	}
	return pairs, nil
}

var refPattern = regexp.MustCompile(`^\$calls((?:\[\d+\]|\.[A-Za-z0-9_-]+)+)$`)
var stepPattern = regexp.MustCompile(`\[(\d+)\]|\.([A-Za-z0-9_-]+)`)

// Resolve replaces every "$calls[N].path" string in a decoded input by the value it points to
// in the outputs of the calls made so far (see the package comment). Other strings stay.
func Resolve(v any, calls []any) (any, error) {
	switch x := v.(type) {
	case string:
		m := refPattern.FindStringSubmatch(x)
		if m == nil {
			return x, nil
		}
		var cur any = calls
		for _, st := range stepPattern.FindAllStringSubmatch(m[1], -1) {
			if st[1] != "" {
				i, _ := strconv.Atoi(st[1])
				l, ok := cur.([]any)
				if !ok || i >= len(l) {
					return nil, fmt.Errorf("%s: no element %s", x, st[1])
				}
				cur = l[i]
				continue
			}
			o, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: no key %s", x, st[2])
			}
			if cur, ok = o[st[2]]; !ok {
				return nil, fmt.Errorf("%s: no key %s", x, st[2])
			}
		}
		return cur, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			r, err := Resolve(e, calls)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := Resolve(e, calls)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}

// Match compares a decoded JSON output with the one a pair expects. The error names the path
// ("$.result.name") of the first difference.
func Match(want, got any) error { return match("$", want, got) }

func match(path string, want, got any) error {
	if w, ok := want.(string); ok && strings.HasPrefix(w, "$") && w != "$" {
		switch w {
		case "$any":
			return nil
		case "$string":
			if _, ok := got.(string); ok {
				return nil
			}
		case "$number":
			if _, ok := got.(float64); ok {
				return nil
			}
		case "$array":
			if _, ok := got.([]any); ok {
				return nil
			}
		default:
			return reflectMatch(path, want, got)
		}
		return fmt.Errorf("%s: want %s, got %v", path, w, got)
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object, got %v", path, got)
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return fmt.Errorf("%s.%s: missing", path, k)
			}
			if err := match(path+"."+k, wv, gv); err != nil {
				return err
			}
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				return fmt.Errorf("%s.%s: unexpected", path, k)
			}
		}
		return nil
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Errorf("%s: want %v, got %v", path, want, got)
		}
		for i := range w {
			if err := match(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return reflectMatch(path, want, got)
}

func reflectMatch(path string, want, got any) error {
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("%s: want %v, got %v", path, want, got)
	}
	return nil
}
