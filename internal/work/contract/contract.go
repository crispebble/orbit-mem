// Package contract holds the orbit.work read contract: the tool input table,
// the input and response JSON Schemas and example responses for each major
// version. Readers (Studio, OpenClaw) copy the examples; oni's tests check
// real responses against the schemas.
package contract

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Name is the contract name in every response.
const Name = "orbit.work"

// Major and Minor are the version oni answers by default.
const (
	Major = 1
	Minor = 0
)

// SupportedMajors are the major versions oni answers.
var SupportedMajors = []int{1}

//go:embed v1
var files embed.FS

// Example is one example response file.
type Example struct {
	Name    string          `json:"-"`
	Tool    string          `json:"tool"`
	Args    map[string]any  `json:"args"`
	IsError bool            `json:"isError,omitempty"`
	Result  json.RawMessage `json:"result"`
}

// Examples reads the example responses of a major version, sorted by name.
func Examples(major int) ([]Example, error) {
	dir := fmt.Sprintf("v%d/examples", major)
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, err
	}
	var out []Example
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := files.ReadFile(path.Join(dir, e.Name()))
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
func InputSchema(major int, tool string) (map[string]any, error) {
	root, err := schemaMap(major, "inputs.schema.json")
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

func schemaMap(major int, name string) (map[string]any, error) {
	b, err := files.ReadFile(fmt.Sprintf("v%d/%s", major, name))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

// Validator checks documents against one definition of a schema file.
type Validator struct{ r *jsonschema.Resolved }

// ResponseValidator checks a tool's response ("work_list", …, "error").
func ResponseValidator(major int, def string) (*Validator, error) {
	return validator(major, "response.schema.json", def)
}

// InputValidator checks a tool's input.
func InputValidator(major int, tool string) (*Validator, error) {
	return validator(major, "inputs.schema.json", tool)
}

func validator(major int, file, def string) (*Validator, error) {
	root, err := schemaMap(major, file)
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
