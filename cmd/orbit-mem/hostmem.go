package main

// 겹치는 기억(설계 · orbit-mem 2판 9절·17절): 기억은 orbit-mem 하나만 쓴다. 판정은 hostmem_detect.go의
// hostMemoryOverlaps 하나다. 끌지는 사용자가 정한다: install과 세션 시작 훅은 묻게만 하고, 끄는 것은
// 사용자가 동의한 뒤 host-memory off가 한다. 어떤 명령도 기억 파일을 지우지 않는다.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/i18n"
)

// overlapsToAsk are the overlaps that are on, to put to the user: none once they chose to
// keep them (hostMemory keep).
func overlapsToAsk(cfg config.Config, cwd string) ([]hostOverlap, []hostWarning) {
	if cfg.HostMemory == config.HostMemoryKeep {
		return nil, nil
	}
	all, warns := hostMemoryOverlaps(cwd)
	var on []hostOverlap
	for _, o := range all {
		if o.On {
			on = append(on, o)
		}
	}
	return on, warns
}

// askUser is one question install puts to the user through their agent (17.4).
type askUser struct {
	ID        string `json:"id"`
	Question  string `json:"question"`
	Reason    string `json:"reason"`
	Apply     string `json:"apply"`               // the command to run once the user agrees; empty when blockedBy
	BlockedBy string `json:"blockedBy,omitempty"` // a project or managed settings file keeps it on: orbit-mem does not change it
	Keep      string `json:"keep"`
}

func hostTitle(h string) string {
	if h == "codex" {
		return "Codex"
	}
	return "Claude Code"
}

func overlapQuestion(o hostOverlap) string {
	switch {
	case o.ID == overlapClaude:
		return i18n.T("cli.hostmem.ask.claude")
	case o.ID == overlapCodex:
		return i18n.T("cli.hostmem.ask.codex")
	case o.Kind == kindMCP:
		return i18n.T("cli.hostmem.ask.mcp", hostTitle(o.Host), o.ID[strings.Index(o.ID, ":")+1:], o.Name)
	}
	return i18n.T("cli.hostmem.ask.plugin", hostTitle(o.Host), o.ID[strings.Index(o.ID, ":")+1:], o.Name)
}

func askUsers(ov []hostOverlap) []askUser {
	out := []askUser{}
	for _, o := range ov {
		q := askUser{ID: o.ID, Question: overlapQuestion(o), Reason: i18n.T("cli.hostmem.ask.reason"), Apply: o.Off, Keep: keepCommand}
		switch {
		case o.LockedBy != "":
			q.Apply, q.BlockedBy = "", o.LockedBy
			q.Reason = i18n.T("cli.hostmem.ask.reason_locked", o.LockedBy)
		case o.Manual:
			q.Reason = i18n.T("cli.hostmem.ask.reason_manual")
		}
		out = append(out, q)
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
		if q.BlockedBy != "" {
			fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.ask.line_locked", q.Question, q.BlockedBy))
			continue
		}
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.ask.line", q.Question, q.Apply))
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.ask.tail", i18n.T("cli.hostmem.ask.reason"), keepCommand))
}

// hostMemoryNote is the session-start note line while a memory system overlaps orbit-mem and the
// user has not chosen to keep it (17.4), else "". The AI reads it and asks the user.
func hostMemoryNote(cfg config.Config, cwd string) string {
	if cfg.RecallMode() == config.RecallOff {
		return ""
	}
	ov, _ := overlapsToAsk(cfg, cwd)
	if len(ov) == 0 {
		return ""
	}
	ids := make([]string, len(ov))
	for i, o := range ov {
		ids[i] = o.ID
	}
	return i18n.T("cli.hostmem.note", strings.Join(ids, ", "), keepCommand)
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
	case o.ID == overlapCodex:
		for _, k := range codexMemoryKeys {
			edits = append(edits, edit{[]string{k[0], k[1]}, &no})
		}
		return "toml", edits
	case o.Host == "codex":
		return "toml", []edit{{[]string{o.table, "enabled"}, &no}}
	}
	return "json", []edit{{[]string{"enabledPlugins", strings.TrimPrefix(o.ID, overlapPluginPrefix)}, &no}}
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
	Unchanged []string     `json:"unchanged"`        // already off (off) or nothing recorded (on)
	Kept      []hostResult `json:"kept,omitempty"`   // on: a person changed the value since; left as it is
	Locked    []hostResult `json:"locked,omitempty"` // off: a project or managed settings file (file) keeps it on; left as it is
	Manual    []hostResult `json:"manual,omitempty"` // off: the host turns it off itself; file is its command
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
	all, warns := hostMemoryOverlaps("")
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
		return a.printHostStatus(sel, warns, hostMemorySetting(cfg), a.wantJSON(*asJSON))
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
		switch {
		case !o.On:
			out.Unchanged = append(out.Unchanged, o.ID)
			continue
		case o.LockedBy != "":
			out.Locked = append(out.Locked, hostResult{ID: o.ID, File: o.LockedBy})
			continue
		case o.Manual:
			out.Manual = append(out.Manual, hostResult{ID: o.ID, File: o.Off})
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

func (a *app) printHostStatus(ov []hostOverlap, warns []hostWarning, setting string, asJSON bool) error {
	if asJSON {
		if ov == nil {
			ov = []hostOverlap{}
		}
		return a.json(map[string]any{"setting": setting, "overlaps": ov, "warnings": warns})
	}
	if len(ov) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.none"))
	}
	for _, o := range ov {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.line", o.ID, onOff(o.On), o.Settings))
		if o.Source != nil && o.Source.Scope != "user" { // 사용자 설정 밖(기본값·환경·프로젝트·관리)에서 온 값이면 출처를 보인다
			fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.source", o.Source.Value, o.Source.Scope, o.Source.Path))
		}
		switch {
		case o.LockedBy != "":
			fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.locked", o.LockedBy))
		case o.On:
			fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.off", o.Off))
		}
		for _, s := range o.Stores {
			fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.store", s.Path, s.Files, strings.TrimSpace(sizeText(s.Bytes))))
		}
	}
	for _, w := range warns {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.status.warning", w.Path, w.Reason))
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
	for _, k := range out.Locked {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.off.locked", k.ID, k.File))
	}
	for _, k := range out.Manual {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.off.manual", k.ID, k.File))
	}
	if len(out.Changed)+len(out.Unchanged)+len(out.Kept)+len(out.Locked)+len(out.Manual) == 0 {
		fmt.Fprintln(a.stdout, say.nothing)
	}
	if len(out.Changed) > 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.hostmem.after"))
	}
	return nil
}
