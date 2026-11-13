package install

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// ClaudeMarket is one entry of claude plugin marketplace list --json.
type ClaudeMarket struct {
	Name   string `json:"name"`
	Source string `json:"source"` // github | directory | git | url …
	Repo   string `json:"repo"`
	URL    string `json:"url"`
	Path   string `json:"path"`
}

// ClaudePlugin is one entry of claude plugin list --json.
type ClaudePlugin struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	Scope   string `json:"scope"`
}

// Market is the marketplace part of the plugin id (name@market).
func (p ClaudePlugin) Market() string {
	_, m, _ := strings.Cut(p.ID, "@")
	return m
}

func (p ClaudePlugin) name() string {
	n, _, _ := strings.Cut(p.ID, "@")
	return n
}

// userScope says whether the plugin is installed for the user (orbit-mem installs and takes
// out only these; project and local scopes live in a project's own settings).
func (p ClaudePlugin) userScope() bool { return p.Scope == "" || p.Scope == "user" }

func (h Hosts) claudeJSON(v any, args ...string) error {
	out, err := h.Run(h.Claude, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), v)
}

func (h Hosts) claudeMarkets() ([]ClaudeMarket, error) {
	var list []ClaudeMarket
	err := h.claudeJSON(&list, "plugin", "marketplace", "list", "--json")
	return list, err
}

func (h Hosts) claudePlugins() ([]ClaudePlugin, error) {
	var list []ClaudePlugin
	err := h.claudeJSON(&list, "plugin", "list", "--json")
	return list, err
}

func marketNamed(list []ClaudeMarket, name string) *ClaudeMarket {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// marketFrom is the marketplace registered from source, in any spelling of it.
func marketFrom(list []ClaudeMarket, source string) *ClaudeMarket {
	k := marketKey(source)
	for i := range list {
		if k != "" && list[i].sourceKey() == k {
			return &list[i]
		}
	}
	return nil
}

func pluginWithID(list []ClaudePlugin, id string) *ClaudePlugin {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// othersOf are the plugins named name installed from a marketplace other than market.
func othersOf(list []ClaudePlugin, name, market string) []ClaudePlugin {
	var out []ClaudePlugin
	for _, p := range list {
		if p.name() == name && p.Market() != market {
			out = append(out, p)
		}
	}
	return out
}

// sharedMarket says whether a plugin other than name is installed from market (then the
// marketplace is not orbit-mem's alone and stays).
func sharedMarket(list []ClaudePlugin, market, name string) bool {
	for _, p := range list {
		if p.Market() == market && p.name() != name {
			return true
		}
	}
	return false
}

// marketLabel is a listed marketplace's source for people: github:<owner/repo>,
// git:<url>, directory:<path>, or its source type.
func marketLabel(m ClaudeMarket) string {
	switch {
	case m.Source == "github":
		return "github:" + m.Repo
	case m.Source == "directory":
		return "directory:" + m.Path
	case m.URL != "":
		return m.Source + ":" + m.URL
	}
	return m.Source
}

// ClaudeTarget is where orbit-mem's Claude Code plugin comes from.
type ClaudeTarget struct {
	Source    string // the marketplace source (MarketSource form)
	Plugin    string // the plugin's name, the same in every marketplace
	OwnMarket string // the name of orbit-mem's own marketplace (its GitHub repository and its local folder)
	// Own says Source is orbit-mem's own marketplace: its name is OwnMarket before it is added,
	// and a marketplace of that name from another source is replaced. Any other marketplace
	// (a catalog) is never removed: removing a marketplace takes every plugin installed from
	// it, and its data, out of Claude Code.
	Own bool
}

// ClaudeStep is one claude command. Note says why, for a step that takes something out.
type ClaudeStep struct {
	Args []string
	Note string
}

func (s ClaudeStep) String() string {
	line := "claude " + strings.Join(s.Args, " ")
	if s.Note != "" {
		line += " (" + s.Note + ")"
	}
	return line
}

// ClaudePlan is what RegisterClaude does, read from Claude Code's lists.
type ClaudePlan struct {
	Market string // the marketplace's name; "" when a catalog not yet registered is to be added first
	ID     string // the plugin to install: Plugin@Market
	Steps  []ClaudeStep
}

// planMarket is the marketplace step(s) for t: update the marketplace registered from t.Source
// (in any spelling), else add it (orbit-mem's own replaces its marketplace of the same name from
// another source). market is "" when only the add will tell the name.
func planMarket(t ClaudeTarget, markets []ClaudeMarket, merr error) (market string, steps []ClaudeStep, err error) {
	if cur := marketFrom(markets, t.Source); cur != nil {
		return cur.Name, []ClaudeStep{{Args: []string{"plugin", "marketplace", "update", cur.Name}}}, nil
	}
	add := ClaudeStep{Args: []string{"plugin", "marketplace", "add", t.Source}}
	if t.Own {
		if old := marketNamed(markets, t.OwnMarket); old != nil {
			steps = append(steps, ClaudeStep{Args: []string{"plugin", "marketplace", "remove", t.OwnMarket}, Note: i18n.T("install.note.replace_market", marketLabel(*old))})
		}
		return t.OwnMarket, append(steps, add), nil
	}
	if merr != nil { // 이름을 모르면 무엇을 깔지(그리고 무엇이 겹치는지) 알 수 없다
		return "", nil, i18n.Errorf("install.err.market_list", merr)
	}
	return "", []ClaudeStep{add}, nil
}

// planPlugin is the plugin steps once the marketplace is market: install Plugin@market (update it
// when installed), and take out every other user-scope Plugin so only one runs: first turn the
// new one on if it is off, then uninstall the others, then remove orbit-mem's own marketplace
// when it is not market and no other plugin comes from it. The new plugin is in place before
// anything is taken out, so a failed install leaves the old one running.
func planPlugin(t ClaudeTarget, market string, markets []ClaudeMarket, plugins []ClaudePlugin) []ClaudeStep {
	id := t.Plugin + "@" + market
	var steps []ClaudeStep
	cur := pluginWithID(plugins, id)
	if cur != nil {
		steps = append(steps, ClaudeStep{Args: []string{"plugin", "update", id}})
	} else {
		steps = append(steps, ClaudeStep{Args: []string{"plugin", "install", id, "--scope", "user"}})
	}
	var drop []ClaudeStep
	for _, o := range othersOf(plugins, t.Plugin, market) {
		if o.userScope() {
			drop = append(drop, ClaudeStep{Args: []string{"plugin", "uninstall", o.ID, "--scope", "user"}, Note: i18n.T("install.note.drop_plugin", id)})
		}
	}
	if len(drop) > 0 && cur != nil && !cur.Enabled {
		steps = append(steps, ClaudeStep{Args: []string{"plugin", "enable", id}, Note: i18n.T("install.note.enable", id)})
	}
	steps = append(steps, drop...)
	if market != t.OwnMarket && marketNamed(markets, t.OwnMarket) != nil && !sharedMarket(plugins, t.OwnMarket, t.Plugin) {
		steps = append(steps, ClaudeStep{Args: []string{"plugin", "marketplace", "remove", t.OwnMarket}, Note: i18n.T("install.note.drop_market", t.OwnMarket)})
	}
	return steps
}

// PlanClaude reads Claude Code's marketplace and plugin lists (it changes nothing) and says what
// RegisterClaude would run. A catalog not yet registered gets its name only from the add, so
// its plugin steps show a placeholder for it.
func (h Hosts) PlanClaude(t ClaudeTarget) (ClaudePlan, error) {
	markets, merr := h.claudeMarkets()
	plugins, _ := h.claudePlugins() // 목록을 읽지 못하면 깔린 것이 없다고 본다(옛 claude: add 뒤 install)
	market, steps, err := planMarket(t, markets, merr)
	if err != nil {
		return ClaudePlan{}, err
	}
	p := ClaudePlan{Market: market, Steps: steps}
	shown := market
	if market == "" {
		shown = i18n.T("install.placeholder.market")
	}
	if t.Own && market == t.OwnMarket && len(steps) > 1 { // 바꿔 넣는 자기 마켓의 플러그인은 remove와 함께 빠진다
		plugins = slices.DeleteFunc(slices.Clone(plugins), func(x ClaudePlugin) bool { return x.Market() == t.OwnMarket })
	}
	p.ID = t.Plugin + "@" + shown
	p.Steps = append(p.Steps, planPlugin(t, shown, markets, plugins)...)
	return p, nil
}

// RegisterClaude puts orbit-mem's plugin from t's marketplace into Claude Code for this user
// and returns what it ran (Market and ID as resolved). The marketplace registered from t.Source
// in any spelling is only updated (a catalog that setup put there keeps its other plugins); one
// not registered is added, and a catalog's name is read from the list after the add. The plugin
// steps are planned again from the lists after the marketplace step, so they match what Claude
// Code then has. When listing fails (an older claude), orbit-mem's own marketplace is added and
// its plugin installed.
func (h Hosts) RegisterClaude(t ClaudeTarget) (ClaudePlan, error) {
	markets, merr := h.claudeMarkets()
	market, steps, err := planMarket(t, markets, merr)
	if err != nil {
		return ClaudePlan{}, err
	}
	ran := ClaudePlan{Market: market}
	if err := h.runClaude(&ran, steps); err != nil {
		return ran, err
	}
	markets, _ = h.claudeMarkets()
	if market == "" {
		cur := marketFrom(markets, t.Source)
		if cur == nil {
			return ran, i18n.Errorf("install.err.market_not_listed", t.Source)
		}
		ran.Market = cur.Name
	}
	ran.ID = t.Plugin + "@" + ran.Market
	plugins, _ := h.claudePlugins()
	return ran, h.runClaude(&ran, planPlugin(t, ran.Market, markets, plugins))
}

// runClaude runs steps in order and records each one that ran; it stops at the first failure.
func (h Hosts) runClaude(ran *ClaudePlan, steps []ClaudeStep) error {
	for _, s := range steps {
		if _, err := h.Run(h.Claude, s.Args...); err != nil {
			return err
		}
		ran.Steps = append(ran.Steps, s)
	}
	return nil
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

// UnregisterClaude takes orbit-mem's plugin out of Claude Code for this user: every user-scope
// Plugin, from whichever marketplace, and orbit-mem's own marketplace when no other plugin comes
// from it. A catalog marketplace stays (removing it would take its other plugins too). When
// listing fails it tries what it can name: Plugin@OwnMarket and OwnMarket for orbit-mem's own
// marketplace, Plugin@<catalog> when the marketplace list names the catalog.
func (h Hosts) UnregisterClaude(t ClaudeTarget) ([]ClaudeStep, error) {
	plugins, perr := h.claudePlugins()
	markets, merr := h.claudeMarkets()
	var ids []string
	switch {
	case perr == nil:
		for _, p := range plugins {
			if p.name() == t.Plugin && p.userScope() {
				ids = append(ids, p.ID)
			}
		}
	case t.Own:
		ids = []string{t.Plugin + "@" + t.OwnMarket}
	default:
		if cur := marketFrom(markets, t.Source); cur != nil {
			ids = []string{t.Plugin + "@" + cur.Name}
		}
	}
	var steps []ClaudeStep
	for _, id := range ids {
		steps = append(steps, ClaudeStep{Args: []string{"plugin", "uninstall", id, "--scope", "user"}})
	}
	own := marketNamed(markets, t.OwnMarket) != nil || (merr != nil && t.Own)
	if own && !sharedMarket(plugins, t.OwnMarket, t.Plugin) {
		steps = append(steps, ClaudeStep{Args: []string{"plugin", "marketplace", "remove", t.OwnMarket}})
	}
	var errs []error
	if perr != nil && len(ids) == 0 && !t.Own {
		errs = append(errs, perr)
	}
	var done []ClaudeStep
	for _, s := range steps { // 하나가 실패해도 나머지는 해 본다
		if _, err := h.Run(h.Claude, s.Args...); err != nil {
			errs = append(errs, err)
			continue
		}
		done = append(done, s)
	}
	return done, errors.Join(errs...)
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

// ClaudeStatus is orbit-mem's plugin as Claude Code reports it.
type ClaudeStatus struct {
	Known       bool   // claude plugin list --json answered
	ID          string // the plugin the setting points to: Plugin@<marketplace>, Plugin when that marketplace is not registered
	Installed   bool
	Version     string
	Enabled     bool
	Marketplace string         // github:<owner/repo> | git:<url> | directory:<path> | <source type> | "" (not registered)
	Others      []ClaudePlugin // the same plugin installed from other marketplaces (it would run twice)
}

// ClaudeStatus reads the plugin of t's marketplace, and the same plugin from any other
// marketplace, from Claude Code.
func (h Hosts) ClaudeStatus(t ClaudeTarget) ClaudeStatus {
	st := ClaudeStatus{ID: t.Plugin}
	markets, _ := h.claudeMarkets()
	market := ""
	if cur := marketFrom(markets, t.Source); cur != nil {
		market, st.Marketplace = cur.Name, marketLabel(*cur)
	} else if t.Own {
		market = t.OwnMarket
	}
	if market != "" {
		st.ID = t.Plugin + "@" + market
	}
	plugins, err := h.claudePlugins()
	if err != nil {
		return st
	}
	st.Known = true
	if p := pluginWithID(plugins, st.ID); p != nil && market != "" {
		st.Installed, st.Version, st.Enabled = true, p.Version, p.Enabled
	}
	st.Others = othersOf(plugins, t.Plugin, market)
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
