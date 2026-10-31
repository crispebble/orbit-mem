// Package contract holds the orbit.work read contract: the tool input table,
// the input and response JSON Schemas and example responses for each major
// version. Readers (Studio, OpenClaw) copy the examples; orb's tests check
// real responses against the schemas.
package contract

import (
	"embed"

	"github.com/onidot-labs/openorbit/internal/contractkit"
)

// Name is the contract name in every response.
const Name = "orbit.work"

// Major and Minor are the version orb answers by default.
const (
	Major = 1
	Minor = 0
)

// SupportedMajors are the major versions orb answers.
var SupportedMajors = []int{1}

//go:embed v1
var files embed.FS

var kit = contractkit.New(files)

// Example is one example response file.
type Example = contractkit.Example

// Validator checks documents against one definition of a schema file.
type Validator = contractkit.Validator

// Examples reads the example responses of a major version, sorted by name.
func Examples(major int) ([]Example, error) { return kit.Examples(major) }

// InputSchema is a tool's input schema as a JSON object, for the MCP tool list.
func InputSchema(major int, tool string) (map[string]any, error) { return kit.InputSchema(major, tool) }

// ResponseValidator checks a tool's response ("work_list", …, "error").
func ResponseValidator(major int, def string) (*Validator, error) {
	return kit.ResponseValidator(major, def)
}

// InputValidator checks a tool's input.
func InputValidator(major int, tool string) (*Validator, error) {
	return kit.InputValidator(major, tool)
}
