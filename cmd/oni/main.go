// Command oni is Orbit: per-PC memory for AI hosts, background
// retrospectives that propose what was learned, and a plugin builder that
// writes Claude Code and Codex formats from one source.
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

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/i18n"
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

func commands() []command {
	return []command{
		{"version", "usage.version", cmdVersion},
		{"install", "usage.install", cmdInstall},
		{"uninstall", "usage.uninstall", cmdUninstall},
		{"doctor", "usage.doctor", cmdDoctor},
		{"mcp", "usage.mcp", cmdMCP},
		{"hook", "usage.hook", cmdHook},
		{"memory", "usage.memory", cmdMemory},
		{"proposals", "usage.proposals", cmdProposals},
		{"retro", "usage.retro", cmdRetro},
		{"migrate", "usage.migrate", cmdMigrate},
		{"plugin", "usage.plugin", cmdPlugin},
		{"config", "usage.config", cmdConfig},
		{"backup", "usage.backup", cmdBackup},
		{"stats", "usage.stats", cmdStats},
		{"lab", "usage.lab", cmdLab},
		{"ledger", "usage.ledger", cmdLedger},
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
		if errors.Is(err, flag.ErrHelp) { // a.flags already printed the usage
			return 2
		}
		if errors.Is(err, errUsage) {
			fmt.Fprintln(a.stderr, i18n.T("cli.usage_line", i18n.T(c.usage)))
			return 2
		}
		fmt.Fprintln(a.stderr, "oni "+c.name+": "+err.Error())
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
