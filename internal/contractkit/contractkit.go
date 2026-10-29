// Package contractkit reads a versioned read contract laid out as
// v<major>/{inputs.schema.json,response.schema.json,examples/*.json}:
// example responses, tool input schemas for the MCP tool list and schema
// validators. orbit.work and orbit.memory share it.
package contractkit

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Kit reads one contract's files.
type Kit struct{ files fs.FS }

// New wraps the contract files (usually an embed.FS holding v1, v2, …).
func New(files fs.FS) Kit { return Kit{files: files} }

// Example is one example response file.
type Example struct {
	Name    string          `json:"-"`
	Tool    string          `json:"tool"`
	Args    map[string]any  `json:"args"`
	IsError bool            `json:"isError,omitempty"`
	Result  json.RawMessage `json:"result"`
}

// Examples reads the example responses of a major version, sorted by name.
func (k Kit) Examples(major int) ([]Example, error) {
	dir := fmt.Sprintf("v%d/examples", major)
	entries, err := fs.ReadDir(k.files, dir)
	if err != nil {
		return nil, err
	}
	var out []Example
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := fs.ReadFile(k.files, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var ex Example
		if err := json.Unmarshal(b, &ex); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		ex.Name = strings.TrimSuffix(e.Name(), ".json")
		out = append(out, ex)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InputSchema is a tool's input schema as a JSON object, for the MCP tool list.
func (k Kit) InputSchema(major int, tool string) (map[string]any, error) {
	root, err := k.schemaMap(major, "inputs.schema.json")
	if err != nil {
		return nil, err
	}
	defs, _ := root["$defs"].(map[string]any)
	if _, ok := defs[tool]; !ok {
		return nil, fmt.Errorf("입력 Schema에 %s가 없습니다", tool)
	}
	// $ref를 풀어 둔 사본을 준다. 호스트가 $defs를 따라가지 않아도 칸 설명이 보이게 한다.
	return inline(defs[tool], defs).(map[string]any), nil
}

func inline(v any, defs map[string]any) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok && strings.HasPrefix(ref, "#/$defs/") {
			return inline(defs[strings.TrimPrefix(ref, "#/$defs/")], defs)
		}
		out := map[string]any{}
		for k, x := range t {
			out[k] = inline(x, defs)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = inline(x, defs)
		}
		return out
	}
	return v
}

func (k Kit) schemaMap(major int, name string) (map[string]any, error) {
	b, err := fs.ReadFile(k.files, fmt.Sprintf("v%d/%s", major, name))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

// Validator checks documents against one definition of a schema file.
type Validator struct{ r *jsonschema.Resolved }

// ResponseValidator checks a tool's response (a tool name, or "error").
func (k Kit) ResponseValidator(major int, def string) (*Validator, error) {
	return k.validator(major, "response.schema.json", def)
}

// InputValidator checks a tool's input.
func (k Kit) InputValidator(major int, tool string) (*Validator, error) {
	return k.validator(major, "inputs.schema.json", tool)
}

func (k Kit) validator(major int, file, def string) (*Validator, error) {
	root, err := k.schemaMap(major, file)
	if err != nil {
		return nil, err
	}
	defs, _ := root["$defs"].(map[string]any)
	if _, ok := defs[def]; !ok {
		return nil, fmt.Errorf("%s에 %s가 없습니다", file, def)
	}
	delete(root, "oneOf")
	root["$ref"] = "#/$defs/" + def
	b, _ := json.Marshal(root)
	var s jsonschema.Schema
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	r, err := s.Resolve(nil)
	if err != nil {
		return nil, err
	}
	return &Validator{r: r}, nil
}

// Validate checks a JSON document.
func (v *Validator) Validate(doc []byte) error {
	var inst any
	if err := json.Unmarshal(doc, &inst); err != nil {
		return err
	}
	return v.r.Validate(inst)
}
