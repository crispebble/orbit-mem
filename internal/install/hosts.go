package install

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeMarket is one entry of claude plugin marketplace list --json.
type ClaudeMarket struct {
	Name   string `json:"name"`
	Source string `json:"source"` // github | directory | git | url …
	Repo   string `json:"repo"`
	Path   string `json:"path"`
}

// ClaudePlugin is one entry of claude plugin list --json.
type ClaudePlugin struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	Scope   string `json:"scope"`
}

func (h Hosts) claudeJSON(v any, args ...string) error {
	out, err := h.Run(h.Claude, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), v)
}

func (h Hosts) claudeMarket(market string) (*ClaudeMarket, error) {
	var list []ClaudeMarket
	if err := h.claudeJSON(&list, "plugin", "marketplace", "list", "--json"); err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == market {
			return &list[i], nil
		}
	}
	return nil, nil
}

func (h Hosts) claudePlugin(id string) (*ClaudePlugin, error) {
	var list []ClaudePlugin
	if err := h.claudeJSON(&list, "plugin", "list", "--json"); err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, nil
}

// sameSource says whether a Claude marketplace entry is source: a folder (absolute path) or
// a GitHub owner/repo.
func sameSource(m ClaudeMarket, source string) bool {
	if filepath.IsAbs(source) {
		return m.Source == "directory" && filepath.Clean(m.Path) == filepath.Clean(source)
	}
	return m.Source == "github" && strings.EqualFold(m.Repo, source)
}

// RegisterClaude puts plugin name from marketplace market at source (a GitHub owner/repo or
// a local marketplace folder) into Claude Code, for this user. A marketplace of that name
// from the same source is updated; from another source it is replaced. An installed plugin
// is updated, else installed. When listing fails (an older claude), it adds and installs.
func (h Hosts) RegisterClaude(source, name, market string) error {
	cur, _ := h.claudeMarket(market)
	switch {
	case cur == nil:
	case sameSource(*cur, source):
		if _, err := h.Run(h.Claude, "plugin", "marketplace", "update", market); err != nil {
			return err
		}
	default:
		if _, err := h.Run(h.Claude, "plugin", "marketplace", "remove", market); err != nil {
			return err
		}
		cur = nil
	}
	if cur == nil {
		if _, err := h.Run(h.Claude, "plugin", "marketplace", "add", source); err != nil {
			return err
		}
	}
	id := name + "@" + market
	if p, _ := h.claudePlugin(id); p != nil {
		_, err := h.Run(h.Claude, "plugin", "update", id)
		return err
	}
	_, err := h.Run(h.Claude, "plugin", "install", id, "--scope", "user")
	return err
}

// RegisterCodex adds the local marketplace folder dir and installs plugin name from it in
// Codex (the same as orbit-code's harness registerPlugin; both accept a repeat, and plugin
// add copies the current folder into Codex's cache again).
func (h Hosts) RegisterCodex(dir, name, market string) error {
	if _, err := h.Run(h.Codex, "plugin", "marketplace", "add", dir); err != nil {
		return err
	}
	_, err := h.Run(h.Codex, "plugin", "add", name+"@"+market)
	return err
}

// UnregisterClaude takes plugin name and its marketplace out of Claude Code, only what is
// there. When listing fails it tries both.
func (h Hosts) UnregisterClaude(name, market string) error {
	id := name + "@" + market
	var errs []error
	p, perr := h.claudePlugin(id)
	if p != nil || perr != nil {
		if _, err := h.Run(h.Claude, "plugin", "uninstall", id, "--scope", "user"); err != nil {
			errs = append(errs, err)
		}
	}
	m, merr := h.claudeMarket(market)
	if m != nil || merr != nil {
		if _, err := h.Run(h.Claude, "plugin", "marketplace", "remove", market); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// UnregisterCodex takes plugin name and its marketplace out of Codex, only what st says is
// there.
func (h Hosts) UnregisterCodex(st CodexState, name, market string) error {
	var errs []error
	if st.Plugin {
		if _, err := h.Run(h.Codex, "plugin", "remove", name+"@"+market); err != nil {
			errs = append(errs, err)
		}
	}
	if st.Marketplace {
		if _, err := h.Run(h.Codex, "plugin", "marketplace", "remove", market); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ClaudeStatus is the plugin as Claude Code reports it.
type ClaudeStatus struct {
	Known       bool // claude plugin list --json answered
	Installed   bool
	Version     string
	Enabled     bool
	Marketplace string // github:<owner/repo> | directory:<path> | <source> | ""
}

// ClaudeStatus reads the plugin and its marketplace from Claude Code.
func (h Hosts) ClaudeStatus(name, market string) ClaudeStatus {
	var st ClaudeStatus
	p, err := h.claudePlugin(name + "@" + market)
	if err != nil {
		return st
	}
	st.Known = true
	if p != nil {
		st.Installed, st.Version, st.Enabled = true, p.Version, p.Enabled
	}
	if m, _ := h.claudeMarket(market); m != nil {
		switch m.Source {
		case "github":
			st.Marketplace = "github:" + m.Repo
		case "directory":
			st.Marketplace = "directory:" + m.Path
		default:
			st.Marketplace = m.Source
		}
	}
	return st
}

// CodexState is the plugin as ~/.codex/config.toml records it.
type CodexState struct {
	Marketplace       bool
	MarketplaceSource string
	Plugin            bool
	Enabled           bool
	HooksTrusted      bool // a [hooks.state."<id>:hooks/codex.json:…"] entry: the person trusted the hooks in /hooks
}

// CodexConfigPath is Codex's config.toml: $CODEX_HOME/config.toml, else ~/.codex/config.toml.
func CodexConfigPath() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "config.toml")
}

// ReadCodexState reads the plugin's entries from Codex's config.toml by its table headers
// (no TOML library: only these headers and their source and enabled keys are needed). A
// missing file is an empty state.
func ReadCodexState(path, name, market string) CodexState {
	var st CodexState
	f, err := os.Open(path)
	if err != nil {
		return st
	}
	defer f.Close()
	id := name + "@" + market
	unquote := func(s string) string { return strings.Trim(strings.TrimSpace(s), `"'`) }
	section := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && !strings.HasPrefix(line, "[[") {
			header := line[1 : len(line)-1]
			section = ""
			switch {
			case header == "marketplaces."+market || header == `marketplaces."`+market+`"`:
				st.Marketplace, section = true, "market"
			case header == `plugins."`+id+`"`:
				st.Plugin, st.Enabled, section = true, true, "plugin" // Codex turns a plugin on unless enabled = false
			case strings.HasPrefix(header, `hooks.state."`+id+`:hooks/codex.json:`):
				st.HooksTrusted = true
			}
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k := strings.TrimSpace(key); {
		case section == "market" && k == "source":
			st.MarketplaceSource = unquote(val)
		case section == "plugin" && k == "enabled":
			st.Enabled = strings.TrimSpace(val) == "true"
		}
	}
	return st
}
