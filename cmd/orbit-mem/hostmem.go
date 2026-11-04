package main

// 호스트 자체 기억 점검(설계 · orbit-mem 2판 9절·17절): 기억은 orbit-mem 하나만 쓴다. Claude Code auto memory,
// Codex memories, Claude Code에 켜진 알려진 기억 플러그인이 겹친다. 판정은 hostMemoryOverlaps 하나이고
// host-memory·doctor·install·세션 시작 훅이 모두 쓴다. 끌지는 사용자가 정한다: install과 훅은 묻게만 하고,
// 끄는 것은 사용자가 동의한 뒤 host-memory off가 한다. 어떤 명령도 기억 파일을 지우지 않는다.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/install"
)

// claudeConfigDir is Claude Code's settings folder: $CLAUDE_CONFIG_DIR, else ~/.claude.
func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func claudeSettingsPath() string { return filepath.Join(claudeConfigDir(), "settings.json") }

// claudeSettings is what the overlap check reads from Claude Code's user settings.
type claudeSettings struct {
	AutoMemoryEnabled *bool           `json:"autoMemoryEnabled"`
	EnabledPlugins    map[string]bool `json:"enabledPlugins"`
}

func readClaudeSettings() claudeSettings {
	var v claudeSettings
	if b, err := os.ReadFile(claudeSettingsPath()); err == nil {
		_ = json.Unmarshal(b, &v)
	}
	return v
}

// claudeAutoMemory says whether Claude Code's own auto memory is on and in which
// settings file: it is on unless the user settings turn it off (autoMemoryEnabled
// false) or CLAUDE_CODE_DISABLE_AUTO_MEMORY is set. Without a settings folder Claude
// Code is not set up here, so there is nothing to say.
func claudeAutoMemory() (string, bool) {
	if _, err := os.Stat(claudeConfigDir()); err != nil {
		return "", false
	}
	switch strings.ToLower(os.Getenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY")) {
	case "1", "true", "yes":
		return "", false
	}
	v := readClaudeSettings().AutoMemoryEnabled
	return claudeSettingsPath(), v == nil || *v
}

// codexMemoryKeys are the config.toml values that turn Codex's memories on ([table, key]).
var codexMemoryKeys = [][2]string{{"features", "memories"}, {"memories", "generate_memories"}, {"memories", "use_memories"}}

// codexMemories says whether Codex's memories are turned on in its config.toml
// (features.memories, memories.generate_memories or memories.use_memories true).
// Codex keeps the feature off unless it is turned on.
func codexMemories(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, k := range codexMemoryKeys {
		if v, _ := tomlGet(string(b), k[0], k[1]); v == "true" {
			return true
		}
	}
	return false
}

// memoryPlugins are the Claude Code plugins known to keep memories of their own, by
// plugin name (the part before @, so any marketplace counts), with the folder under
// HOME where they keep them. orbit-mem itself is never one of them.
var memoryPlugins = map[string]string{
	"claude-mem": ".claude-mem", // thedotmack/claude-mem
}

func isMemoryPlugin(id string) bool {
	name, _, _ := strings.Cut(id, "@")
	_, ok := memoryPlugins[name]
	return ok && name != "orbit-mem"
}

// Overlap ids (17.2).
const (
	overlapClaude       = "claude-auto-memory"
	overlapCodex        = "codex-memories"
	overlapPluginPrefix = "claude-plugin:"
)

// hostOverlap is one memory system besides orbit-mem on this PC.
type hostOverlap struct {
	ID       string        `json:"id"`
	Host     string        `json:"host"` // claude | codex
	On       bool          `json:"on"`
	Settings string        `json:"settings"` // the file that turns it on or off
	Off      string        `json:"off"`      // the command that turns it off
	Stores   []memoryStore `json:"stores,omitempty"`
}

// memoryStore is where an overlap keeps its memories (host-memory status only: it walks folders).
type memoryStore struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

func offCommand(id string) string { return "orbit-mem host-memory off --only " + id }

const keepCommand = "orbit-mem config set hostMemory keep"

// hostMemoryOverlaps lists every memory system it knows of whose host is set up here,
// on or off, in a fixed order: Claude Code's own, Claude plugins by id, then Codex.
func hostMemoryOverlaps() []hostOverlap {
	var out []hostOverlap
	if _, err := os.Stat(claudeConfigDir()); err == nil {
		_, on := claudeAutoMemory()
		out = append(out, hostOverlap{ID: overlapClaude, Host: "claude", On: on, Settings: claudeSettingsPath(), Off: offCommand(overlapClaude)})
		plugins := readClaudeSettings().EnabledPlugins
		ids := make([]string, 0, len(plugins))
		for id := range plugins {
			if isMemoryPlugin(id) {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			oid := overlapPluginPrefix + id
			out = append(out, hostOverlap{ID: oid, Host: "claude", On: plugins[id], Settings: claudeSettingsPath(), Off: offCommand(oid)})
		}
	}
	path := install.CodexConfigPath()
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		out = append(out, hostOverlap{ID: overlapCodex, Host: "codex", On: codexMemories(path), Settings: path, Off: offCommand(overlapCodex)})
	}
	return out
}

// activeOverlaps are the overlaps that are on.
func activeOverlaps() []hostOverlap {
	var on []hostOverlap
	for _, o := range hostMemoryOverlaps() {
		if o.On {
			on = append(on, o)
		}
	}
	return on
}

// overlapsToAsk are the overlaps to put to the user: none once they chose to keep them.
func overlapsToAsk(cfg config.Config) []hostOverlap {
	if cfg.HostMemory == config.HostMemoryKeep {
		return nil
	}
	return activeOverlaps()
}

// storesOf finds where an overlap keeps its memories.
func storesOf(o hostOverlap) []memoryStore {
	var globs []string
	home, _ := os.UserHomeDir()
	switch {
	case o.ID == overlapClaude:
		globs = []string{filepath.Join(claudeConfigDir(), "projects", "*", "memory")}
	case o.ID == overlapCodex:
		globs = []string{filepath.Join(filepath.Dir(o.Settings), "memories*")}
	case strings.HasPrefix(o.ID, overlapPluginPrefix):
		name, _, _ := strings.Cut(strings.TrimPrefix(o.ID, overlapPluginPrefix), "@")
		globs = []string{filepath.Join(home, memoryPlugins[name])}
	}
	var out []memoryStore
	for _, g := range globs {
		st := memoryStore{Path: g}
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			_ = filepath.WalkDir(m, func(_ string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if info, err := d.Info(); err == nil {
					st.Files++
					st.Bytes += info.Size()
				}
				return nil
			})
		}
		if len(matches) > 0 {
			out = append(out, st)
		}
	}
	return out
}

// askUser is one question install puts to the user through their agent (17.4).
type askUser struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Reason   string `json:"reason"`
	Apply    string `json:"apply"`
	Keep     string `json:"keep"`
}

func overlapQuestion(o hostOverlap) string {
	switch o.ID {
	case overlapClaude:
		return i18n.T("cli.hostmem.ask.claude")
	case overlapCodex:
		return i18n.T("cli.hostmem.ask.codex")
	}
	return i18n.T("cli.hostmem.ask.plugin", strings.TrimPrefix(o.ID, overlapPluginPrefix))
}

func askUsers(ov []hostOverlap) []askUser {
	out := []askUser{}
	for _, o := range ov {
		out = append(out, askUser{ID: o.ID, Question: overlapQuestion(o), Reason: i18n.T("cli.hostmem.ask.reason"), Apply: o.Off, Keep: keepCommand})
	}
	return out
}

// printAskUser is the 「check with the user」 block at the end of install.
func (a *app) printAskUser(asks []askUser) {
	if len(asks) == 0 {
		return
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.ask.head"))
	for _, q := range asks {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.ask.line", q.Question, q.Apply))
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.ask.tail", asks[0].Reason, keepCommand))
}

// withHostMemoryNote adds one line at the end of the session-start context while a
// memory system overlaps orbit-mem and the user has not chosen to keep it (17.4). The
// AI reads it and asks the user.
func withHostMemoryNote(text string, cfg config.Config) string {
	if cfg.Inject == config.InjectOff || os.Getenv("ORBIT_RETRO_ID") != "" {
		return text
	}
	ov := overlapsToAsk(cfg)
	if len(ov) == 0 {
		return text
	}
	ids := make([]string, len(ov))
	for i, o := range ov {
		ids[i] = o.ID
	}
	line := i18n.T("cli.hostmem.note", strings.Join(ids, ", "), keepCommand)
	if text = strings.TrimRight(text, "\n"); text == "" {
		return line
	}
	return text + "\n" + line
}

// hostChange is one value host-memory off changed, kept so on can put back only that.
type hostChange struct {
	ID     string   `json:"id"`
	File   string   `json:"file"`
	Format string   `json:"format"` // json | toml
	Key    []string `json:"key"`    // JSON path, or [table, key] in TOML
	Before *string  `json:"before"` // the raw value before; null when the key was absent
	At     string   `json:"at"`
}

type hostRecord struct {
	Changes []hostChange `json:"changes"`
}

func hostRecordPath() string { return filepath.Join(paths().Home, "host-memory.json") }

func loadHostRecord() (hostRecord, error) {
	var r hostRecord
	b, err := os.ReadFile(hostRecordPath())
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

func saveHostRecord(r hostRecord) error {
	if len(r.Changes) == 0 {
		if err := os.Remove(hostRecordPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.MkdirAll(filepath.Dir(hostRecordPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(hostRecordPath(), append(b, '\n'), 0o600)
}

// edit is one value to set (val) or remove (val nil) in a settings file.
type edit struct {
	key []string
	val *string
}

// offEdits are the edits that turn an overlap off.
func offEdits(o hostOverlap) (format string, edits []edit) {
	no := "false"
	switch {
	case o.ID == overlapClaude:
		return "json", []edit{{[]string{"autoMemoryEnabled"}, &no}, {[]string{"autoDreamEnabled"}, &no}}
	case strings.HasPrefix(o.ID, overlapPluginPrefix):
		return "json", []edit{{[]string{"enabledPlugins", strings.TrimPrefix(o.ID, overlapPluginPrefix)}, &no}}
	default:
		for _, k := range codexMemoryKeys {
			edits = append(edits, edit{[]string{k[0], k[1]}, &no})
		}
		return "toml", edits
	}
}

func getValue(format string, text []byte, key []string) (string, bool) {
	if format == "toml" {
		return tomlGet(string(text), key[0], key[1])
	}
	return jsonGet(text, key)
}

func setValue(format string, text []byte, key []string, val *string) ([]byte, error) {
	if format == "toml" {
		if tomlInline(string(text), key[0]) {
			return nil, i18n.Errorf("cli.hostmem.err.inline_table", key[0], key[0])
		}
		return []byte(tomlSet(string(text), key[0], key[1], val)), nil
	}
	return jsonSet(text, key, val)
}

// backupFile copies path to <path>.bak-orbit-mem-<date>, or -2, -3… when that day's copy
// exists, and returns the copy's path. A missing file has nothing to back up.
func backupFile(path string, now time.Time) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	base := path + ".bak-orbit-mem-" + now.Format("2006-01-02")
	dst := base
	for n := 2; lexists(dst); n++ {
		dst = fmt.Sprintf("%s-%d", base, n)
	}
	return dst, os.WriteFile(dst, b, 0o600)
}

type hostResult struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Backup string `json:"backup,omitempty"`
}

type hostOutcome struct {
	Changed   []hostResult `json:"changed"`
	Unchanged []string     `json:"unchanged"`         // already off (off) or nothing recorded (on)
	Kept      []hostResult `json:"kept,omitempty"`    // on: a person changed the value since; left as it is
	Setting   string       `json:"setting,omitempty"` // hostMemory
}

func hostMemorySetting(cfg config.Config) string {
	if cfg.HostMemory == "" {
		return config.HostMemoryAsk
	}
	return cfg.HostMemory
}

func cmdHostMemory(a *app, args []string) error {
	fs := a.flags("host-memory")
	host := fs.String("host", "all", "claude | codex | all")
	only := fs.String("only", "", i18n.T("flag.hostmem.only"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		pos = []string{"status"}
	}
	if len(pos) != 1 || !slices.Contains([]string{"status", "off", "on"}, pos[0]) || !slices.Contains([]string{"claude", "codex", "all"}, *host) {
		return errUsage
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	all := hostMemoryOverlaps()
	known := map[string]bool{}
	for _, o := range all {
		known[o.ID] = true
	}
	var ids []string
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if !known[id] && !(pos[0] == "on" && recordHas(id)) {
			return i18n.Errorf("cli.hostmem.err.unknown", id)
		}
		ids = append(ids, id)
	}
	pick := func(id, h string) bool {
		return (*host == "all" || *host == h) && (len(ids) == 0 || slices.Contains(ids, id))
	}
	switch pos[0] {
	case "status":
		var sel []hostOverlap
		for _, o := range all {
			if pick(o.ID, o.Host) {
				o.Stores = storesOf(o)
				sel = append(sel, o)
			}
		}
		return a.printHostStatus(sel, hostMemorySetting(cfg), a.wantJSON(*asJSON))
	case "off":
		out, err := hostOff(all, pick)
		if err != nil {
			return err
		}
		return a.printHostOutcome(out, a.wantJSON(*asJSON), outcomeText{
			changed:   func(r hostResult) string { return i18n.T("cli.hostmem.off.changed", r.ID, r.File) },
			backup:    func(r hostResult) string { return i18n.T("cli.hostmem.off.changed_backup", r.ID, r.File, r.Backup) },
			unchanged: func(id string) string { return i18n.T("cli.hostmem.off.unchanged", id) },
			nothing:   i18n.T("cli.hostmem.off.nothing"),
		})
	default:
		out, err := hostOn(pick)
		if err != nil {
			return err
		}
		return a.printHostOutcome(out, a.wantJSON(*asJSON), outcomeText{
			changed: func(r hostResult) string { return i18n.T("cli.hostmem.on.changed", r.ID, r.File) },
			nothing: i18n.T("cli.hostmem.on.nothing"),
		})
	}
}

func recordHas(id string) bool {
	r, _ := loadHostRecord()
	return slices.ContainsFunc(r.Changes, func(c hostChange) bool { return c.ID == id })
}

// hostOff turns off the picked overlaps that are on. For each it works out the new file
// first (a file it cannot edit safely is left untouched), then backs the file up once,
// writes it and records what it changed, so a later failure never loses what on needs.
func hostOff(all []hostOverlap, pick func(id, host string) bool) (hostOutcome, error) {
	out := hostOutcome{Changed: []hostResult{}, Unchanged: []string{}}
	rec, err := loadHostRecord()
	if err != nil {
		return out, err
	}
	now := time.Now()
	backups := map[string]string{}
	for _, o := range all {
		if !pick(o.ID, o.Host) {
			continue
		}
		if !o.On {
			out.Unchanged = append(out.Unchanged, o.ID)
			continue
		}
		format, edits := offEdits(o)
		text, err := os.ReadFile(o.Settings)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return out, err
		}
		var changes []hostChange
		for _, e := range edits {
			var before *string
			if v, ok := getValue(format, text, e.key); ok {
				before = &v
			}
			if before != nil && *before == *e.val {
				continue
			}
			if text, err = setValue(format, text, e.key, e.val); err != nil {
				return out, i18n.Errorf("cli.hostmem.err.edit", o.Settings, err)
			}
			changes = append(changes, hostChange{ID: o.ID, File: o.Settings, Format: format, Key: e.key, Before: before, At: now.Format(time.RFC3339)})
		}
		if len(changes) == 0 {
			out.Unchanged = append(out.Unchanged, o.ID)
			continue
		}
		if _, done := backups[o.Settings]; !done {
			if backups[o.Settings], err = backupFile(o.Settings, now); err != nil {
				return out, err
			}
		}
		if err := writeSettings(o.Settings, text); err != nil {
			return out, err
		}
		for _, c := range changes {
			rec.Changes = slices.DeleteFunc(rec.Changes, func(r hostChange) bool { return r.File == c.File && slices.Equal(r.Key, c.Key) })
			rec.Changes = append(rec.Changes, c)
		}
		if err := saveHostRecord(rec); err != nil {
			return out, err
		}
		out.Changed = append(out.Changed, hostResult{ID: o.ID, File: o.Settings, Backup: backups[o.Settings]})
	}
	return out, nil
}

// hostOn puts back what hostOff changed for the picked overlaps, file by file: a value still
// at what orbit-mem wrote goes back to what it was (a key that was absent is removed); a
// value a person changed since is left. Either way those records are settled.
func hostOn(pick func(id, host string) bool) (hostOutcome, error) {
	out := hostOutcome{Changed: []hostResult{}, Unchanged: []string{}}
	rec, err := loadHostRecord()
	if err != nil {
		return out, err
	}
	hostOf := func(c hostChange) string {
		if c.Format == "toml" {
			return "codex"
		}
		return "claude"
	}
	var files []string
	for _, c := range rec.Changes {
		if pick(c.ID, hostOf(c)) && !slices.Contains(files, c.File) {
			files = append(files, c.File)
		}
	}
	for _, f := range files {
		mine := func(c hostChange) bool { return c.File == f && pick(c.ID, hostOf(c)) }
		text, err := os.ReadFile(f)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return out, err
		}
		changed := false
		for _, c := range rec.Changes {
			if !mine(c) {
				continue
			}
			if v, ok := getValue(c.Format, text, c.Key); err != nil || !ok || v != "false" {
				out.Kept = append(out.Kept, hostResult{ID: c.ID, File: c.File})
				continue
			}
			if text, err = setValue(c.Format, text, c.Key, c.Before); err != nil {
				return out, i18n.Errorf("cli.hostmem.err.edit", c.File, err)
			}
			changed = true
			if !slices.ContainsFunc(out.Changed, func(r hostResult) bool { return r.ID == c.ID && r.File == f }) {
				out.Changed = append(out.Changed, hostResult{ID: c.ID, File: f})
			}
		}
		if changed {
			if err := writeSettings(f, text); err != nil {
				return out, err
			}
		}
		rec.Changes = slices.DeleteFunc(rec.Changes, mine)
		if err := saveHostRecord(rec); err != nil {
			return out, err
		}
	}
	return out, nil
}

// writeSettings replaces a settings file through a temporary file in the same folder, so a
// crash never leaves it cut short. A symbolic link is followed: the file it points to is
// replaced and the link stays. The permissions are kept.
func writeSettings(path string, b []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".orbit-mem-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (a *app) printHostStatus(ov []hostOverlap, setting string, asJSON bool) error {
	if asJSON {
		if ov == nil {
			ov = []hostOverlap{}
		}
		return a.json(map[string]any{"setting": setting, "overlaps": ov})
	}
	if len(ov) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.none"))
	}
	for _, o := range ov {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.line", o.ID, onOff(o.On), o.Settings))
		if o.On {
			fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.off", o.Off))
		}
		for _, s := range o.Stores {
			fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.store", s.Path, s.Files, strings.TrimSpace(sizeText(s.Bytes))))
		}
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.setting", setting))
	return nil
}

// outcomeText is how off and on say what they did.
type outcomeText struct {
	changed, backup func(hostResult) string
	unchanged       func(string) string
	nothing         string
}

func (a *app) printHostOutcome(out hostOutcome, asJSON bool, say outcomeText) error {
	if asJSON {
		return a.json(out)
	}
	for _, c := range out.Changed {
		if c.Backup != "" && say.backup != nil {
			fmt.Fprintln(a.stdout, say.backup(c))
		} else {
			fmt.Fprintln(a.stdout, say.changed(c))
		}
	}
	for _, id := range out.Unchanged {
		fmt.Fprintln(a.stdout, say.unchanged(id))
	}
	for _, k := range out.Kept {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.on.kept", k.ID, k.File))
	}
	if len(out.Changed) == 0 && len(out.Unchanged) == 0 && len(out.Kept) == 0 {
		fmt.Fprintln(a.stdout, say.nothing)
	}
	if len(out.Changed) > 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.after"))
	}
	return nil
}

// TOML (no library: only bare top-level tables and key = value lines are needed). Lines
// keep their ending: a file written with CRLF stays CRLF.

// tomlKeyValue reads a key = value line: its key as written, the value, and what follows the value.
func tomlKeyValue(line string) (key, val, rest string, ok bool) {
	line = strings.TrimSuffix(line, "\r")
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", "", false
	}
	k = strings.TrimSpace(k)
	if k == "" || strings.HasPrefix(k, "#") || strings.HasPrefix(k, "[") {
		return "", "", "", false
	}
	v = strings.TrimLeft(v, " \t")
	val, rest = v, ""
	if i := strings.Index(v, "#"); i >= 0 && !strings.ContainsAny(v[:i], `"'`) {
		val, rest = v[:i], v[i:]
	}
	trimmed := strings.TrimRight(val, " \t")
	if rest != "" {
		rest = val[len(trimmed):] + rest
	}
	return k, trimmed, rest, true
}

func tomlHeader(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if i := strings.Index(t, "#"); i >= 0 && strings.Contains(t[:i], "]") {
		t = strings.TrimSpace(t[:i])
	}
	if strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "[[") && strings.HasSuffix(t, "]") {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

// tomlRootDotted is the last root line with a table.key key, or -1.
func tomlRootDotted(lines []string, table string) int {
	last := -1
	for i, l := range lines {
		if _, ok := tomlHeader(l); ok {
			break
		}
		if k, _, _, ok := tomlKeyValue(l); ok && strings.HasPrefix(k, table+".") {
			last = i
		}
	}
	return last
}

// tomlFind is the line index of table.key (under [table], or as table.key before any
// table), and the index of the [table] header (-1 when none).
func tomlFind(lines []string, table, key string) (at, header int) {
	at, header = -1, -1
	section := ""
	for i, l := range lines {
		if h, ok := tomlHeader(l); ok {
			section = h
			if h == table && header < 0 {
				header = i
			}
			continue
		}
		k, _, _, ok := tomlKeyValue(l)
		if !ok {
			continue
		}
		if (section == table && k == key) || (section == "" && k == table+"."+key) {
			return i, header
		}
	}
	return -1, header
}

// tomlInline says whether table is written as an inline table at the root (table = { … }),
// which these line edits cannot change without breaking the file.
func tomlInline(text, table string) bool {
	for _, l := range strings.Split(text, "\n") {
		if _, ok := tomlHeader(l); ok {
			return false
		}
		if k, v, _, ok := tomlKeyValue(l); ok && k == table && strings.HasPrefix(v, "{") {
			return true
		}
	}
	return false
}

func tomlGet(text, table, key string) (string, bool) {
	lines := strings.Split(text, "\n")
	at, _ := tomlFind(lines, table, key)
	if at < 0 {
		return "", false
	}
	_, v, _, _ := tomlKeyValue(lines[at])
	return v, true
}

// tomlSet sets table.key to val (a raw TOML value) or removes it (val nil), leaving every
// other line as it was. A missing key goes after the last line of its table; a missing
// table goes at the end.
func tomlSet(text, table, key string, val *string) string {
	cr := ""
	if strings.Contains(text, "\r\n") {
		cr = "\r"
	}
	lines := strings.Split(text, "\n")
	at, header := tomlFind(lines, table, key)
	switch {
	case at >= 0 && val == nil:
		lines = slices.Delete(lines, at, at+1)
	case at >= 0:
		l := lines[at]
		eq := strings.Index(l, "=")
		_, _, rest, _ := tomlKeyValue(l)
		end := ""
		if strings.HasSuffix(l, "\r") {
			end = "\r"
		}
		lines[at] = strings.TrimRight(l[:eq], " \t") + " = " + *val + rest + end
	case val == nil:
	case header < 0 && tomlRootDotted(lines, table) >= 0:
		// 표가 점 키로만 정의돼 있으면 [표]를 새로 열 수 없다(TOML이 금지): 점 키로 더한다.
		lines = slices.Insert(lines, tomlRootDotted(lines, table)+1, table+"."+key+" = "+*val+cr)
	case header >= 0:
		last := header
		for i := header + 1; i < len(lines); i++ {
			if _, ok := tomlHeader(lines[i]); ok {
				break
			}
			if strings.TrimSpace(lines[i]) != "" {
				last = i
			}
		}
		lines = slices.Insert(lines, last+1, key+" = "+*val+cr)
	default:
		body := strings.TrimRight(text, "\r\n")
		block := "[" + table + "]" + cr + "\n" + key + " = " + *val + cr + "\n"
		if body == "" {
			return block
		}
		return body + cr + "\n" + cr + "\n" + block
	}
	return strings.Join(lines, "\n")
}

// JSON with the members kept in their order (Claude Code's settings.json).

type jsonMember struct {
	Key string
	Val json.RawMessage
}

func parseObject(b []byte) ([]jsonMember, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, i18n.Err("cli.hostmem.err.not_object")
	}
	var ms []jsonMember
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		ms = append(ms, jsonMember{key, raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return ms, nil
}

func compactObject(ms []jsonMember) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range ms {
		if i > 0 {
			b.WriteByte(',')
		}
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(m.Key)
		b.Truncate(b.Len() - 1) // Encode adds a newline
		b.WriteByte(':')
		_ = json.Compact(&b, m.Val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func setMember(ms []jsonMember, path []string, val *string) ([]jsonMember, error) {
	i := slices.IndexFunc(ms, func(m jsonMember) bool { return m.Key == path[0] })
	if len(path) == 1 {
		switch {
		case val == nil && i >= 0:
			return slices.Delete(ms, i, i+1), nil
		case val == nil:
			return ms, nil
		case i >= 0:
			ms[i].Val = json.RawMessage(*val)
			return ms, nil
		}
		return append(ms, jsonMember{path[0], json.RawMessage(*val)}), nil
	}
	var inner []jsonMember
	if i >= 0 {
		var err error
		if inner, err = parseObject(ms[i].Val); err != nil {
			return nil, err
		}
	} else if val == nil {
		return ms, nil
	}
	inner, err := setMember(inner, path[1:], val)
	if err != nil {
		return nil, err
	}
	if i >= 0 {
		ms[i].Val = compactObject(inner)
		return ms, nil
	}
	return append(ms, jsonMember{path[0], compactObject(inner)}), nil
}

// jsonIndentOf is the indent of the first indented line, else two spaces.
func jsonIndentOf(b []byte) string {
	for _, l := range strings.Split(string(b), "\n")[1:] {
		if t := strings.TrimLeft(l, " \t"); t != "" && len(t) < len(l) {
			return l[:len(l)-len(t)]
		}
	}
	return "  "
}

func jsonGet(b []byte, path []string) (string, bool) {
	raw := json.RawMessage(b)
	for _, k := range path {
		ms, err := parseObject(raw)
		if err != nil {
			return "", false
		}
		i := slices.IndexFunc(ms, func(m jsonMember) bool { return m.Key == k })
		if i < 0 {
			return "", false
		}
		raw = ms[i].Val
	}
	var c bytes.Buffer
	if json.Compact(&c, raw) != nil {
		return "", false
	}
	return c.String(), true
}

// jsonSet sets the value at path to val (raw JSON) or removes it (val nil). The members
// keep their order and the file its indent unit.
func jsonSet(b []byte, path []string, val *string) ([]byte, error) {
	ms, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	if ms, err = setMember(ms, path, val); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compactObject(ms), "", jsonIndentOf(b)); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
