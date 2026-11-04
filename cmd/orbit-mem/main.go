// Command orbit-mem is the shared memory of this PC for AI hosts: memories with
// their sources, the storage of the retrospectives (retro-store and the retro MCP
// tools), the activity log of host sessions, and its own Claude Code and Codex
// plugin (the memory MCP server, hooks and skills) that it builds and registers
// itself. It never runs a model or a headless host, and it never calls OpenOrbit's
// orb: the retrospectives run in OpenOrbit (orb retro) and the harness lab is an
// OpenOrbit dev tool (ONID-124); both reach orbit-mem through retro-store. It does
// not need OpenOrbit to keep and recall memories.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

type app struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

type command struct {
	name, usage string // usage is a catalog key
	run         func(a *app, args []string) error
}

var errUsage = i18n.Err("cli.err.usage")

// exitCode ends orbit-mem with a child's exit code without printing anything.
type exitCode int

func (c exitCode) Error() string { return "" }

func commands() []command {
	return []command{
		{"version", "usage.version", cmdVersion},
		{"install", "usage.install", cmdInstall},
		{"uninstall", "usage.uninstall", cmdUninstall},
		{"doctor", "usage.doctor", cmdDoctor},
		{"host-memory", "usage.host-memory", cmdHostMemory},
		{"search", "usage.search", cmdSearch},
		{"show", "usage.show", cmdShow},
		{"list", "usage.list", cmdList},
		{"edit", "usage.edit", cmdEdit},
		{"stale", "usage.stale", cmdStale},
		{"export", "usage.export", cmdExport},
		{"import", "usage.import", cmdImport},
		{"activity", "usage.activity", cmdActivity},
		{"proposals", "usage.proposals", cmdProposals},
		{"retro-store", "usage.retro-store", cmdRetroStore},
		{"mcp", "usage.mcp", cmdMCP},
		{"hook", "usage.hook", cmdHook},
		{"inject", "usage.inject", cmdInject},
		{"tool", "usage.tool", cmdTool},
		{"why", "usage.why", cmdWhy},
		{"place", "usage.place", cmdPlace},
		{"plugin", "usage.plugin", cmdPlugin},
		{"config", "usage.config", cmdConfig},
		{"backup", "usage.backup", cmdBackup},
		{"migrate", "usage.migrate", cmdMigrate},
		{"stats", "usage.stats", cmdStats},
		{"ledger", "usage.ledger", cmdLedger},
		{"audit", "usage.audit", cmdAudit},
	}
}

func (a *app) usage() {
	fmt.Fprintln(a.stderr, i18n.T("cli.usage_head"))
	for _, c := range commands() {
		fmt.Fprintln(a.stderr, "  "+i18n.T(c.usage))
	}
}

func (a *app) main(args []string) int {
	i18n.Set(i18n.Resolve(os.Getenv(i18n.EnvVar), config.Language(paths().Config())))
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		a.usage()
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}
		err := c.run(a, args[1:])
		if err == nil {
			return 0
		}
		var code exitCode
		if errors.As(err, &code) {
			return int(code)
		}
		if errors.Is(err, flag.ErrHelp) { // a.flags already printed the usage
			return 2
		}
		if errors.Is(err, errUsage) {
			fmt.Fprintln(a.stderr, i18n.T("cli.usage_line", i18n.T(c.usage)))
			return 2
		}
		fmt.Fprintln(a.stderr, "orbit-mem "+c.name+": "+err.Error())
		return 1
	}
	fmt.Fprintln(a.stderr, i18n.T("cli.unknown_command", args[0]))
	a.usage()
	return 2
}

func (a *app) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	cmd, _, _ := strings.Cut(name, " ")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), i18n.T("cli.usage_line", i18n.T("usage."+cmd)))
		fs.PrintDefaults()
	}
	return fs
}

// parse lets flags appear after positional arguments too.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (a *app) json(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	a := &app{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
	os.Exit(a.main(os.Args[1:]))
}
