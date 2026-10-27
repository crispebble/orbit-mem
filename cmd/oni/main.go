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
)

type app struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

type command struct {
	name, usage string
	run         func(a *app, args []string) error
}

var errUsage = errors.New("사용법이 맞지 않습니다")

func commands() []command {
	return []command{
		{"version", "oni version", cmdVersion},
		{"install", "oni install [--bin-dir D] [--retro-host claude|codex] [--retro-model M] [--plugin-source DIR] [--apply-target PATH]... [--no-schedule] [--no-hosts] [--dry-run]", cmdInstall},
		{"uninstall", "oni uninstall   # 플러그인 등록·예약을 지운다. 기억 DB는 남긴다", cmdUninstall},
		{"doctor", "oni doctor [--json]", cmdDoctor},
		{"mcp", "oni mcp [--retro ID]   # 호스트가 띄우는 MCP 서버(stdio)", cmdMCP},
		{"hook", "oni hook session-start|prompt --host claude|codex   # 호스트 훅(stdin JSON)", cmdHook},
		{"memory", "oni memory find [질의] [--kind K] [--all] [--json] | show <id> [--history]", cmdMemory},
		{"proposals", "oni proposals [list|review|show <id>|approve <id> [--title T] [--body B|--body-file F] [--note N]|reject <id> [--note N]] [--all]", cmdProposals},
		{"retro", "oni retro run [--day YYYY-MM-DD|--week YYYY-MM-DD] | status [--n N] | material --day YYYY-MM-DD", cmdRetro},
		{"migrate", "oni migrate --from <knowledge-auto-*.sqlite 사본> [--dry-run] [--json]", cmdMigrate},
		{"plugin", "oni plugin build <원본> [--out D] [--check] [--var k=v]... | install <빌드한 마켓 디렉터리>", cmdPlugin},
		{"config", "oni config [show | set <키> <값>]", cmdConfig},
		{"backup", "oni backup   # 기억 DB 사본을 지금 만든다", cmdBackup},
	}
}

func (a *app) usage() {
	fmt.Fprintln(a.stderr, "Orbit — 이 PC의 AI 기억·회고·플러그인 도구\n\n명령:")
	for _, c := range commands() {
		fmt.Fprintln(a.stderr, "  "+c.usage)
	}
}

func (a *app) main(args []string) int {
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
		if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.stderr, "사용법: "+c.usage)
			return 2
		}
		fmt.Fprintln(a.stderr, "oni "+c.name+": "+err.Error())
		return 1
	}
	fmt.Fprintf(a.stderr, "모르는 명령입니다: %s\n", args[0])
	a.usage()
	return 2
}

func (a *app) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
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
