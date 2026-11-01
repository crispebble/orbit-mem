package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The lab is the one place in orbit-mem that still runs a headless host (claude -p):
// its candidates and its judge. Everything else in orbit-mem only stores and reads;
// the retrospectives run in OpenOrbit. ONID-124 takes the lab out, and this file with it.

// Cmd is a host process to run.
type Cmd struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin string
}

// ExecHost runs a host process with its stdin from c.Stdin and returns all of its
// output (stdout and stderr). The host runs in its own process group so a cancel or
// timeout stops its children (MCP servers, node) too.
func ExecHost(ctx context.Context, c Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = strings.NewReader(c.Stdin)
	cmd.WaitDelay = 15 * time.Second // 자식이 출력 파이프를 쥐고 있어도 끝낸다
	setProcessGroup(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// HostOutput is what a headless host printed: Claude's JSON result carries
// the cost and tokens; anything else is kept as text.
type HostOutput struct {
	Known        bool
	CostUSD      float64
	InputTokens  int
	OutputTokens int
	Text         string
	IsError      bool // 호스트가 오류로 끝났다(상한 초과·로그인 없음 등)
}

// ParseHostOutput reads the last line of out that is a JSON object with a
// cost (claude -p --output-format json prints one); without one the whole
// output is the text.
func ParseHostOutput(out string) HostOutput {
	h := HostOutput{Text: out}
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var v struct {
			IsError bool     `json:"is_error"`
			Result  string   `json:"result"`
			Cost    *float64 `json:"total_cost_usd"`
			Usage   struct {
				Input       int `json:"input_tokens"`
				CacheRead   int `json:"cache_read_input_tokens"`
				CacheCreate int `json:"cache_creation_input_tokens"`
				Output      int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &v) != nil || v.Cost == nil {
			continue
		}
		return HostOutput{Known: true, CostUSD: *v.Cost, InputTokens: v.Usage.Input + v.Usage.CacheRead + v.Usage.CacheCreate,
			OutputTokens: v.Usage.Output, Text: v.Result, IsError: v.IsError}
	}
	return h
}
