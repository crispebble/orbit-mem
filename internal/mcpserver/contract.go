package mcpserver

// The contract machinery of the read tools for apps (memory_graph, orbit.memory): argument
// checks against the contract input schema, the contract head on every result and contract
// errors. It was shared with the work tools, which moved to OpenOrbit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type args map[string]json.RawMessage

func (a args) str(k string) string {
	var s string
	_ = json.Unmarshal(a[k], &s)
	return s
}

func (a args) strs(k string) []string {
	var s []string
	_ = json.Unmarshal(a[k], &s)
	return s
}

func (a args) int(k string) int {
	var n int
	_ = json.Unmarshal(a[k], &n)
	return n
}

func (a args) bool(k string) bool {
	var b bool
	_ = json.Unmarshal(a[k], &b)
	return b
}

func (a args) has(k string) bool { _, ok := a[k]; return ok }

// callError is a contract error (계약 README 「오류」).
type callError struct {
	Code       string    `json:"code"`
	Message    string    `json:"message"`
	Field      string    `json:"field,omitempty"`
	Supported  []int     `json:"supportedMajors,omitempty"`
	Candidates *[]string `json:"candidates,omitempty"` // AMBIGUOUS_WORK에는 빈 배열이라도 둔다
}

func (e *callError) Error() string { return e.Code + ": " + e.Message }

func invalid(field, msg string) *callError {
	return &callError{Code: "INVALID_ARGUMENT", Message: msg, Field: field}
}

// contractInfo is the version head of one read contract (orbit.work, orbit.memory).
type contractInfo struct {
	Name         string
	Major, Minor int
	Supported    []int
}

func (c contractInfo) head() map[string]any {
	return map[string]any{"name": c.Name, "major": c.Major, "minor": c.Minor, "supportedMajors": c.Supported}
}

// contractHandler is the handler of a contract tool: argument checks against the
// input schema, the contract head on every result, contract errors.
func contractHandler(c contractInfo, schema map[string]any, h func(context.Context, args) (any, error)) mcp.ToolHandler {
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var raw map[string]json.RawMessage
		if len(req.Params.Arguments) > 0 && string(req.Params.Arguments) != "null" {
			if err := json.Unmarshal(req.Params.Arguments, &raw); err != nil {
				return c.errorResult(invalid("", "인자는 JSON 객체여야 합니다")), nil
			}
		}
		a := args{}
		var ignored []string
		for k, v := range raw {
			ps, ok := props[k].(map[string]any)
			if !ok {
				ignored = append(ignored, k)
				continue
			}
			if string(v) == "null" {
				continue // null은 넘기지 않은 것으로 본다
			}
			if err := checkValue(k, ps, v); err != nil {
				return c.errorResult(err), nil
			}
			a[k] = v
		}
		if a.has("major") && !c.supports(a.int("major")) {
			return c.errorResult(&callError{Code: "UNSUPPORTED_MAJOR", Message: fmt.Sprintf("%s %d판은 지원하지 않습니다.", c.Name, a.int("major")), Supported: c.Supported}), nil
		}
		for _, r := range required {
			if k, _ := r.(string); !a.has(k) {
				return c.errorResult(invalid(k, k+"가 필요합니다.")), nil
			}
		}
		out, err := h(ctx, a)
		if err != nil {
			var ce *callError
			if errors.As(err, &ce) {
				return c.errorResult(ce), nil
			}
			return c.errorResult(&callError{Code: "INVALID_ARGUMENT", Message: err.Error()}), nil
		}
		doc := map[string]any{}
		b, _ := json.Marshal(out)
		_ = json.Unmarshal(b, &doc)
		doc["contract"] = c.head()
		if len(ignored) > 0 {
			sort.Strings(ignored)
			doc["ignoredArgs"] = ignored
		}
		body, _ := json.Marshal(doc)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}, StructuredContent: json.RawMessage(body)}, nil
	}
}

func (c contractInfo) supports(major int) bool {
	for _, m := range c.Supported {
		if m == major {
			return true
		}
	}
	return false
}

func (c contractInfo) errorResult(e *callError) *mcp.CallToolResult {
	body, _ := json.Marshal(map[string]any{"contract": c.head(), "error": e})
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: e.Error()}}, StructuredContent: json.RawMessage(body)}
}

// checkValue is the type part of the input schema: type, enum, minimum, items.
func checkValue(field string, ps map[string]any, v json.RawMessage) *callError {
	var x any
	if err := json.Unmarshal(v, &x); err != nil {
		return invalid(field, field+" 값을 읽을 수 없습니다.")
	}
	return checkAny(field, ps, x)
}

func checkAny(field string, ps map[string]any, x any) *callError {
	want, _ := ps["type"].(string)
	if want == "" && ps["enum"] != nil {
		want = "string"
	}
	ok := true
	switch want {
	case "string":
		_, ok = x.(string)
	case "boolean":
		_, ok = x.(bool)
	case "integer":
		f, isNum := x.(float64)
		ok = isNum && f == float64(int64(f))
		if ok {
			if min, has := ps["minimum"].(float64); has && f < min {
				return invalid(field, fmt.Sprintf("%s는 %v 이상이어야 합니다.", field, min))
			}
		}
	case "array":
		list, isList := x.([]any)
		ok = isList
		if items, has := ps["items"].(map[string]any); ok && has {
			for _, it := range list {
				if err := checkAny(field, items, it); err != nil {
					return err
				}
			}
		}
	}
	if !ok {
		return invalid(field, fmt.Sprintf("%s는 %s여야 합니다.", field, typeName[want]))
	}
	if enum, has := ps["enum"].([]any); has {
		for _, e := range enum {
			if e == x {
				return nil
			}
		}
		var names []string
		for _, e := range enum {
			names = append(names, fmt.Sprint(e))
		}
		return invalid(field, fmt.Sprintf("%s는 %s 중 하나여야 합니다.", field, strings.Join(names, "·")))
	}
	return nil
}

var typeName = map[string]string{"string": "문자열", "boolean": "불", "integer": "정수(밀리초·개수)", "array": "배열"}
