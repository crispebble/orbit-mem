package main

// 설계 · orbit-mem 2판 17절 「설치할 때 겹치는 기억 알리기」(HM-1~HM-9). 모든 시험은 가짜 HOME에서 돈다.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// hostPC is a fake HOME with Claude Code's and Codex's settings folders, a project folder
// the checks run in, and a managed settings folder.
type hostPC struct{ home, claude, codex, proj, managed string }

func newHostPC(t *testing.T) hostPC {
	t.Helper()
	home(t)
	h := hostPC{home: t.TempDir()}
	h.claude, h.codex = filepath.Join(h.home, ".claude"), filepath.Join(h.home, ".codex")
	h.proj, h.managed = filepath.Join(h.home, "proj"), filepath.Join(h.home, "managed")
	claudeManagedDir = func() string { return h.managed }
	hostCwd = func() (string, error) { return h.proj, nil }
	for _, d := range []string{h.claude, h.codex, h.proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", h.home)
	t.Setenv("PATH", t.TempDir()) // 진짜 claude·codex를 부르지 않는다
	t.Setenv("CLAUDE_CONFIG_DIR", h.claude)
	t.Setenv("CODEX_HOME", h.codex)
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "")
	return h
}

func (h hostPC) settings() string { return filepath.Join(h.claude, "settings.json") }
func (h hostPC) toml() string     { return filepath.Join(h.codex, "config.toml") }

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type statusOut struct {
	Setting  string `json:"setting"`
	Overlaps []struct {
		ID, Host, Settings, Off string
		On                      bool
		Stores                  []struct {
			Path  string
			Files int
			Bytes int64
		}
	} `json:"overlaps"`
}

func hostStatus(t *testing.T) statusOut {
	t.Helper()
	out, errOut, code := run(t, "", "host-memory", "status", "--json")
	var st statusOut
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil {
		t.Fatalf("status %d %q %q", code, out, errOut)
	}
	return st
}

func onIDs(st statusOut) []string {
	var ids []string
	for _, o := range st.Overlaps {
		if o.On {
			ids = append(ids, o.ID)
		}
	}
	return ids
}

func backupOf(path string) string { return path + ".bak-orbit-mem-" + time.Now().Format("2006-01-02") }

// HM-1
func TestHostMemory_status는_겹침과_저장소를_보이고_파일을_바꾸지_않는다(t *testing.T) {
	h := newHostPC(t)
	settings := `{"autoMemoryEnabled": true, "enabledPlugins": {"claude-mem@thedotmack": true, "orbit-mem@orbit-mem": true}}`
	write(t, h.settings(), settings)
	write(t, filepath.Join(h.claude, "projects", "-a", "memory", "MEMORY.md"), "0123456789")
	write(t, filepath.Join(h.claude, "projects", "-b", "memory", "x.md"), "abc")
	write(t, h.toml(), "[features]\nmemories = true\n")
	write(t, filepath.Join(h.codex, "memories", "MEMORY.md"), "12345")
	write(t, filepath.Join(h.home, ".claude-mem", "claude-mem.db"), "1234567")
	st := hostStatus(t)
	if got := onIDs(st); !slices.Equal(got, []string{"claude-auto-memory", "claude-plugin:claude-mem@thedotmack", "codex-memories"}) {
		t.Fatalf("on: %v", got)
	}
	if st.Setting != "ask" {
		t.Fatalf("setting %q", st.Setting)
	}
	want := map[string][2]int64{"claude-auto-memory": {2, 13}, "codex-memories": {1, 5}, "claude-plugin:claude-mem@thedotmack": {1, 7}}
	for _, o := range st.Overlaps {
		if len(o.Stores) != 1 || [2]int64{int64(o.Stores[0].Files), o.Stores[0].Bytes} != want[o.ID] || o.Off == "" {
			t.Fatalf("%s: %+v", o.ID, o)
		}
	}
	if read(t, h.settings()) != settings || read(t, h.toml()) != "[features]\nmemories = true\n" {
		t.Fatal("status changed a settings file")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("ORBIT_HOME"), "host-memory.json")); err == nil {
		t.Fatal("status wrote a record")
	}
	if _, err := os.Stat(backupOf(h.settings())); err == nil {
		t.Fatal("status made a backup")
	}
}

func TestHostMemory_status는_설정_폴더가_없으면_아무것도_없다고_한다(t *testing.T) {
	home(t)
	if st := hostStatus(t); len(st.Overlaps) != 0 {
		t.Fatalf("%+v", st)
	}
}

// HM-2
func TestHostMemory_off_claude는_두_키를_끄고_다른_키와_들여쓰기를_보존한다(t *testing.T) {
	h := newHostPC(t)
	before := "{\n    \"model\": \"opus\",\n    \"statusLine\": {\n        \"command\": \"a && b <c>\"\n    },\n    \"autoMemoryEnabled\": true\n}\n"
	write(t, h.settings(), before)
	write(t, h.toml(), "[features]\nmemories = true\n")
	mem := filepath.Join(h.claude, "projects", "-a", "memory", "MEMORY.md")
	write(t, mem, "x")
	if out, errOut, code := run(t, "", "host-memory", "off", "--host", "claude"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	want := "{\n    \"model\": \"opus\",\n    \"statusLine\": {\n        \"command\": \"a && b <c>\"\n    },\n    \"autoMemoryEnabled\": false,\n    \"autoDreamEnabled\": false\n}\n"
	if got := read(t, h.settings()); got != want {
		t.Fatalf("settings:\n%s", got)
	}
	if read(t, backupOf(h.settings())) != before {
		t.Fatal("backup is not the file before")
	}
	if read(t, h.toml()) != "[features]\nmemories = true\n" {
		t.Fatal("--host claude changed Codex")
	}
	if read(t, mem) != "x" {
		t.Fatal("memory file changed")
	}
	if got := onIDs(hostStatus(t)); !slices.Equal(got, []string{"codex-memories"}) {
		t.Fatalf("on after off: %v", got)
	}
}

func TestHostMemory_off는_같은_날_백업을_덮어쓰지_않는다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": true}`)
	write(t, backupOf(h.settings()), "earlier")
	run(t, "", "host-memory", "off", "--host", "claude")
	if read(t, backupOf(h.settings())) != "earlier" || read(t, backupOf(h.settings())+"-2") != `{"autoMemoryEnabled": true}` {
		t.Fatal("backup overwritten or missing")
	}
}

func TestHostMemory_off는_settings_json이_없으면_만든다(t *testing.T) {
	h := newHostPC(t)
	run(t, "", "host-memory", "off", "--host", "claude")
	if got := read(t, h.settings()); got != "{\n  \"autoMemoryEnabled\": false,\n  \"autoDreamEnabled\": false\n}\n" {
		t.Fatalf("%q", got)
	}
}

// HM-3
func TestHostMemory_off_codex는_세_값을_끄고_다른_줄을_보존한다(t *testing.T) {
	h := newHostPC(t)
	before := "model = \"gpt-5\"\n# my settings\n[features]\nmemories = true # on\nweb_search = true\n\n[plugins.\"orbit-mem@orbit-mem\"]\nenabled = true\n"
	write(t, h.toml(), before)
	write(t, h.settings(), `{"autoMemoryEnabled": true}`)
	if out, errOut, code := run(t, "", "host-memory", "off", "--host", "codex"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	want := "model = \"gpt-5\"\n# my settings\n[features]\nmemories = false # on\nweb_search = true\n\n[plugins.\"orbit-mem@orbit-mem\"]\nenabled = true\n\n[memories]\ngenerate_memories = false\nuse_memories = false\n"
	if got := read(t, h.toml()); got != want {
		t.Fatalf("config.toml:\n%s", got)
	}
	if read(t, backupOf(h.toml())) != before || read(t, h.settings()) != `{"autoMemoryEnabled": true}` {
		t.Fatal("backup or Claude settings wrong")
	}
	if codexMemories(h.toml()) {
		t.Fatal("still on")
	}
}

func TestTOML_값_바꾸기는_점_키와_있는_표를_다룬다(t *testing.T) {
	no := "false"
	cases := []struct{ in, table, key, want string }{
		{"features.memories = true\n[x]\na = 1\n", "features", "memories", "features.memories = false\n[x]\na = 1\n"},
		{"[memories]\nuse_memories = true\n\n[y]\n", "memories", "generate_memories", "[memories]\nuse_memories = true\ngenerate_memories = false\n\n[y]\n"},
		{"", "features", "memories", "[features]\nmemories = false\n"},
		{"a = 1", "features", "memories", "a = 1\n\n[features]\nmemories = false\n"},
	}
	for _, c := range cases {
		if got := tomlSet(c.in, c.table, c.key, &no); got != c.want {
			t.Errorf("%q → %q, want %q", c.in, got, c.want)
		}
	}
	if got := tomlSet("[features]\nmemories = false\nweb = true\n", "features", "memories", nil); got != "[features]\nweb = true\n" {
		t.Errorf("delete: %q", got)
	}
	if v, ok := tomlGet("[features]\nmemories = true # x\n", "features", "memories"); !ok || v != "true" {
		t.Errorf("get %q %v", v, ok)
	}
}

// HM-4
func TestHostMemory_on은_orbit_mem이_끈_값만_되돌린다(t *testing.T) {
	h := newHostPC(t)
	settings := "{\n  \"autoMemoryEnabled\": true,\n  \"other\": 1\n}\n"
	toml := "[features]\nmemories = true\n"
	write(t, h.settings(), settings)
	write(t, h.toml(), toml)
	mem := filepath.Join(h.codex, "memories", "MEMORY.md")
	write(t, mem, "keep me")
	run(t, "", "host-memory", "off")
	// 사람이 끈 뒤에 한 값을 손으로 바꿨다: on은 그 값을 건드리지 않는다.
	write(t, h.toml(), strings.Replace(read(t, h.toml()), "generate_memories = false", "generate_memories = true", 1))
	if out, errOut, code := run(t, "", "host-memory", "on"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	if got := read(t, h.settings()); got != settings {
		t.Fatalf("settings not restored:\n%s", got)
	}
	if got := read(t, h.toml()); got != "[features]\nmemories = true\n\n[memories]\ngenerate_memories = true\n" {
		t.Fatalf("config.toml:\n%s", got)
	}
	if read(t, mem) != "keep me" {
		t.Fatal("memory file changed")
	}
	// 남은 기록이 없으면 아무것도 바꾸지 않는다.
	write(t, h.settings(), `{"autoMemoryEnabled": false}`)
	out, _, code := run(t, "", "host-memory", "on", "--json")
	var res struct{ Changed []any }
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || len(res.Changed) != 0 || read(t, h.settings()) != `{"autoMemoryEnabled": false}` {
		t.Fatalf("on without a record changed something: %s", out)
	}
}

// HM-5
func TestHostMemory_기억_플러그인은_이름으로_찾고_orbit_mem은_빼고_하나만_끈다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": true, "enabledPlugins": {"claude-mem@other-market": true, "orbit-mem@orbit-mem": true, "frontend@x": true}}`)
	ov, _ := hostMemoryOverlaps("")
	var ids []string
	for _, o := range ov {
		ids = append(ids, o.ID)
	}
	if !slices.Equal(ids, []string{"claude-auto-memory", "claude-plugin:claude-mem@other-market", "codex-memories"}) {
		t.Fatalf("overlaps %v", ids)
	}
	if _, _, code := run(t, "", "host-memory", "off", "--only", "claude-plugin:claude-mem@other-market"); code != 0 {
		t.Fatal(code)
	}
	var v struct {
		AutoMemoryEnabled bool
		EnabledPlugins    map[string]bool
	}
	json.Unmarshal([]byte(read(t, h.settings())), &v)
	if !v.AutoMemoryEnabled || v.EnabledPlugins["claude-mem@other-market"] || !v.EnabledPlugins["orbit-mem@orbit-mem"] || !v.EnabledPlugins["frontend@x"] {
		t.Fatalf("%+v", v)
	}
	if _, errOut, code := run(t, "", "host-memory", "off", "--only", "nope"); code == 0 || !strings.Contains(errOut, "nope") {
		t.Fatalf("unknown id accepted: %d %s", code, errOut)
	}
}

// HM-6
func TestInstall_겹치는_기억을_사용자에게_묻고_바꾸지_않는다(t *testing.T) {
	newFakePC(t, "claude")
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "")
	settings := filepath.Join(claude, "settings.json")
	write(t, settings, `{"model": "opus"}`)
	dest := t.TempDir()
	type asks struct {
		AskUser []struct{ ID, Question, Reason, Apply, Keep string } `json:"askUser"`
	}
	parse := func(out string) asks {
		var v asks
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("install json %q: %v", out, err)
		}
		return v
	}
	out, errOut, code := run(t, "", "install", "--json", "--bin-dir", dest)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
	v := parse(out)
	if len(v.AskUser) != 1 {
		t.Fatalf("askUser %+v", v)
	}
	a := v.AskUser[0]
	if a.ID != "claude-auto-memory" || a.Question == "" || a.Reason == "" || a.Apply != "orbit-mem host-memory off --only claude-auto-memory" || a.Keep != "orbit-mem config set hostMemory keep" {
		t.Fatalf("ask %+v", a)
	}
	if read(t, settings) != `{"model": "opus"}` {
		t.Fatal("install changed Claude settings")
	}
	human, _, _ := run(t, "", "install", "--bin-dir", dest)
	if !strings.Contains(human, "Check with the user") || !strings.Contains(human, a.Question) || !strings.Contains(human, a.Apply) || !strings.Contains(human, a.Keep) {
		t.Fatalf("human install:\n%s", human)
	}
	dryOut, _, _ := run(t, "", "install", "--dry-run", "--json", "--bin-dir", dest)
	if dry := parse(dryOut); len(dry.AskUser) != 1 {
		t.Fatalf("dry-run askUser %+v", dry)
	}
	run(t, "", "config", "set", "hostMemory", "keep")
	o, _, _ := run(t, "", "install", "--json", "--bin-dir", dest)
	if !strings.Contains(o, `"askUser": []`) {
		t.Fatalf("keep still asks:\n%s", o)
	}
	if h, _, _ := run(t, "", "install", "--bin-dir", dest); strings.Contains(h, "Check with the user") {
		t.Fatalf("keep still asks:\n%s", h)
	}
}

func TestInstall_한국어로는_질문도_한국어다(t *testing.T) {
	newFakePC(t)
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "")
	t.Setenv("ORBIT_LANG", "ko")
	out, _, _ := run(t, "", "install", "--dry-run", "--bin-dir", t.TempDir())
	if !strings.Contains(out, "사용자에게 확인할 것") || !strings.Contains(out, "끌까요?") {
		t.Fatalf("%s", out)
	}
}

// HM-7
func TestHook_세션_시작은_겹치는_기억을_한_줄_알리고_keep이면_조용하다(t *testing.T) {
	h := newHostPC(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	start := func(session string) string {
		out, _, _ := run(t, `{"cwd":"/tmp","session_id":"`+session+`","source":"startup"}`, "hook", "session-start", "--host", "claude")
		return eventContext(t, out, "SessionStart")
	}
	write(t, h.settings(), `{"autoMemoryEnabled": false, "autoDreamEnabled": false}`)
	if ctx := start("a"); strings.Contains(ctx, "host-memory") {
		t.Fatalf("no overlap but noted:\n%s", ctx)
	}
	write(t, h.settings(), `{"enabledPlugins": {"claude-mem@thedotmack": true}}`)
	ctx := start("b")
	last := ctx[strings.LastIndex(strings.TrimRight(ctx, "\n"), "\n")+1:]
	if !strings.Contains(last, "claude-auto-memory") || !strings.Contains(last, "claude-plugin:claude-mem@thedotmack") ||
		!strings.Contains(last, "orbit-mem host-memory off") || !strings.Contains(last, "orbit-mem config set hostMemory keep") {
		t.Fatalf("last line %q of:\n%s", last, ctx)
	}
	if strings.Count(ctx, "host-memory off") != 1 {
		t.Fatalf("noted more than once:\n%s", ctx)
	}
	run(t, "", "config", "set", "hostMemory", "keep")
	if ctx := start("c"); strings.Contains(ctx, "host-memory") {
		t.Fatalf("keep but noted:\n%s", ctx)
	}
}

func TestHook_겹침_줄과_판_줄을_더해도_세션_시작은_6000자_이하다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"enabledPlugins": {"claude-mem@thedotmack": true}}`)
	write(t, h.toml(), "[features]\nmemories = true\n")
	s := seed(t)
	for i := 0; i < 200; i++ {
		s.Save(memory.SaveInput{Kind: "rule", Title: fmt.Sprintf("규칙 %d", i), Body: strings.Repeat("본문 ", 200), Source: memory.Source{Kind: "quote", Excerpt: "x"}})
	}
	out, _, _ := run(t, `{"cwd":"/tmp","session_id":"s1","source":"startup"}`, "hook", "session-start", "--host", "claude", "--plugin-version", "0.0.1")
	ctx := eventContext(t, out, "SessionStart")
	if n := utf8.RuneCountInString(ctx); n > 6000 || !strings.Contains(ctx, "0.0.1") || !strings.Contains(ctx, "host-memory off") {
		t.Fatalf("session start %d chars:\n%s", n, ctx)
	}
}

// HM-8
func TestDoctor_기억_플러그인도_문제로_보이고_고치는_법은_명령이다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": false, "enabledPlugins": {"claude-mem@thedotmack": true}}`)
	problems := func() map[string]string {
		out, _, _ := run(t, "", "doctor", "--json")
		var rep struct{ Problems []struct{ ID, Fix string } }
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("doctor %q: %v", out, err)
		}
		m := map[string]string{}
		for _, p := range rep.Problems {
			m[p.ID] = p.Fix
		}
		return m
	}
	got := problems()
	if fix := got["claude-memory-plugin"]; !strings.Contains(fix, "orbit-mem host-memory off --only claude-plugin:claude-mem@thedotmack") {
		t.Fatalf("problems %v", got)
	}
	if _, ok := got["claude-auto-memory"]; ok {
		t.Fatalf("auto memory is off: %v", got)
	}
	write(t, h.settings(), `{}`)
	if fix := problems()["claude-auto-memory"]; !strings.Contains(fix, "orbit-mem host-memory off --only claude-auto-memory") {
		t.Fatalf("fix %q", fix)
	}
	run(t, "", "config", "set", "hostMemory", "keep")
	if got := problems(); got["claude-auto-memory"] != "" || got["claude-memory-plugin"] != "" {
		t.Fatalf("keep but warned: %v", got)
	}
}

func TestConfig_hostMemory는_keep과_ask만_받는다(t *testing.T) {
	home(t)
	if _, _, code := run(t, "", "config", "set", "hostMemory", "off"); code == 0 {
		t.Fatal("accepted off")
	}
	if _, _, code := run(t, "", "config", "set", "hostMemory", "ask"); code != 0 {
		t.Fatal("refused ask")
	}
}

func TestJSON_값_바꾸기는_순서와_글자를_보존한다(t *testing.T) {
	no := "false"
	got, err := jsonSet([]byte(`{"b":"<x>&","a":{"z":1}}`), []string{"a", "y"}, &no)
	if err != nil || string(got) != "{\n  \"b\": \"<x>&\",\n  \"a\": {\n    \"z\": 1,\n    \"y\": false\n  }\n}\n" {
		t.Fatalf("%q %v", got, err)
	}
	got, _ = jsonSet(got, []string{"a", "y"}, nil)
	if string(got) != "{\n  \"b\": \"<x>&\",\n  \"a\": {\n    \"z\": 1\n  }\n}\n" {
		t.Fatalf("delete: %q", got)
	}
	if _, err := jsonSet([]byte(`[1]`), []string{"a"}, &no); err == nil {
		t.Fatal("not an object accepted")
	}
}

func TestTOML_CRLF_줄끝도_읽고_줄끝을_지킨다(t *testing.T) {
	no := "false"
	in := "[features]\r\nmemories = true\r\nweb = true\r\n"
	if v, ok := tomlGet(in, "features", "memories"); !ok || v != "true" {
		t.Fatalf("get %q %v", v, ok)
	}
	if got := tomlSet(in, "features", "memories", &no); got != "[features]\r\nmemories = false\r\nweb = true\r\n" {
		t.Fatalf("set %q", got)
	}
	if got := tomlSet(in, "memories", "use_memories", &no); got != in+"\r\n[memories]\r\nuse_memories = false\r\n" {
		t.Fatalf("new table %q", got)
	}
	if got := tomlSet(in, "features", "extra", &no); got != "[features]\r\nmemories = true\r\nweb = true\r\nextra = false\r\n" {
		t.Fatalf("insert %q", got)
	}
}

func TestHostMemory_off가_도중에_실패해도_끈_것은_기록에_남는다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": true}`)
	write(t, h.toml(), "features = { memories = true }\nmemories.use_memories = true\n")
	if _, errOut, code := run(t, "", "host-memory", "off"); code == 0 || !strings.Contains(errOut, "config.toml") {
		t.Fatalf("inline table accepted: %d %s", code, errOut)
	}
	if got := read(t, h.toml()); got != "features = { memories = true }\nmemories.use_memories = true\n" {
		t.Fatalf("config.toml changed: %q", got)
	}
	if _, err := os.Stat(backupOf(h.toml())); err == nil {
		t.Fatal("backup made for a file it did not change")
	}
	if _, _, code := run(t, "", "host-memory", "on"); code != 0 {
		t.Fatal(code)
	}
	if got := read(t, h.settings()); got != "{\n  \"autoMemoryEnabled\": true\n}\n" {
		t.Fatalf("claude not put back: %q", got)
	}
}

func TestTOML_점_키로만_있는_표에는_점_키로_더한다(t *testing.T) {
	no := "false"
	in := "memories.use_memories = true\n[x]\na = 1\n"
	if got := tomlSet(in, "memories", "generate_memories", &no); got != "memories.use_memories = true\nmemories.generate_memories = false\n[x]\na = 1\n" {
		t.Fatalf("%q", got)
	}
	if !tomlInline("features = { memories = true }\n", "features") || tomlInline("[features]\nmemories = true\n", "features") {
		t.Fatal("inline")
	}
}

func TestHostMemory_off는_심볼릭_링크를_따라가_원본을_고친다(t *testing.T) {
	h := newHostPC(t)
	real := filepath.Join(h.home, "dotfiles", "settings.json")
	write(t, real, `{"autoMemoryEnabled": true}`)
	if err := os.Symlink(real, h.settings()); err != nil {
		t.Skip(err)
	}
	run(t, "", "host-memory", "off", "--host", "claude")
	if fi, err := os.Lstat(h.settings()); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced")
	}
	if !strings.Contains(read(t, real), `"autoMemoryEnabled": false`) {
		t.Fatalf("%s", read(t, real))
	}
}

type overlapOut struct {
	ID, Kind, Name, Settings, Off, LockedBy string
	On, Manual                              bool
	Source                                  *settingValue
}

func overlapByID(t *testing.T, id string) overlapOut {
	t.Helper()
	out, _, _ := run(t, "", "host-memory", "status", "--json")
	var st struct {
		Overlaps []overlapOut
		Warnings []hostWarning
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	for _, o := range st.Overlaps {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("no %s in %s", id, out)
	return overlapOut{}
}

func TestHostMemory_Claude_설정은_우선순위대로_읽고_출처를_보인다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": false}`)
	project := filepath.Join(h.proj, ".claude", "settings.json")
	write(t, project, `{"autoMemoryEnabled": true}`)
	o := overlapByID(t, "claude-auto-memory")
	if !o.On || o.Source == nil || o.Source.Scope != "project" || o.LockedBy != project {
		t.Fatalf("project over user: %+v %+v", o, o.Source)
	}
	// 더 위 설정이 켰으면 off는 아무것도 바꾸지 않고 그 파일을 알린다.
	out, _, code := run(t, "", "host-memory", "off", "--json")
	if code != 0 || !strings.Contains(out, `"locked"`) || !strings.Contains(out, project) || read(t, h.settings()) != `{"autoMemoryEnabled": false}` {
		t.Fatalf("off with a project lock (%d): %s", code, out)
	}
	local := filepath.Join(h.proj, ".claude", "settings.local.json")
	write(t, local, `{"autoMemoryEnabled": false}`)
	if o := overlapByID(t, "claude-auto-memory"); o.On || o.Source.Path != local {
		t.Fatalf("local over project: %+v", o)
	}
	managed := filepath.Join(h.managed, "managed-settings.json")
	write(t, managed, `{"autoMemoryEnabled": true}`)
	if o := overlapByID(t, "claude-auto-memory"); !o.On || o.Source.Scope != "managed" || o.LockedBy != managed {
		t.Fatalf("managed over local: %+v", o)
	}
	drop := filepath.Join(h.managed, "managed-settings.d", "10-memory.json")
	write(t, drop, `{"autoMemoryEnabled": false}`)
	if o := overlapByID(t, "claude-auto-memory"); o.On || o.Source.Path != drop {
		t.Fatalf("drop-in over managed file: %+v", o)
	}
	os.Remove(drop)
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1")
	if o := overlapByID(t, "claude-auto-memory"); o.On || o.Source.Scope != "env" {
		t.Fatalf("env: %+v", o)
	}
}

func TestHostMemory_로컬_설정은_git_루트에서_읽는다(t *testing.T) {
	h := newHostPC(t)
	os.MkdirAll(filepath.Join(h.proj, ".git"), 0o755)
	sub := filepath.Join(h.proj, "pkg", "a")
	os.MkdirAll(sub, 0o755)
	hostCwd = func() (string, error) { return sub, nil }
	write(t, filepath.Join(h.proj, ".claude", "settings.local.json"), `{"autoMemoryEnabled": false, "enabledPlugins": {"claude-mem@thedotmack": true}}`)
	if o := overlapByID(t, "claude-auto-memory"); o.On || o.Source.Scope != "local" {
		t.Fatalf("%+v", o)
	}
	if o := overlapByID(t, "claude-plugin:claude-mem@thedotmack"); !o.On || o.LockedBy == "" {
		t.Fatalf("plugin from local: %+v", o)
	}
}

func TestHostMemory_읽지_못한_설정은_경고로_남고_판정은_이어진다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": false,}`)
	write(t, h.toml(), "[features]\nmemories = true\n")
	out, _, code := run(t, "", "host-memory", "status", "--json")
	var st struct {
		Overlaps []overlapOut
		Warnings []hostWarning
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || len(st.Warnings) != 1 || st.Warnings[0].Path != h.settings() {
		t.Fatalf("%d %s", code, out)
	}
	ins, _, _ := run(t, "", "install", "--dry-run", "--json", "--no-hosts", "--bin-dir", t.TempDir())
	if !strings.Contains(ins, h.settings()) || !strings.Contains(ins, "codex-memories") {
		t.Fatalf("install json without the warning: %s", ins)
	}
	doc, _, _ := run(t, "", "doctor", "--json")
	if !strings.Contains(doc, "host-settings-unreadable") {
		t.Fatalf("doctor: %s", doc)
	}
}

func TestInstall_json은_실패해도_JSON_하나를_낸다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{}`)
	blocker := filepath.Join(h.home, "not-a-dir")
	write(t, blocker, "x")
	out, errOut, code := run(t, "", "install", "--json", "--no-hosts", "--bin-dir", filepath.Join(blocker, "bin"))
	var res struct {
		OK      bool
		Error   string
		AskUser []askUser
	}
	if code != 1 || json.Unmarshal([]byte(out), &res) != nil || res.OK || res.Error == "" || len(res.AskUser) != 1 || errOut == "" {
		t.Fatalf("%d out=%q err=%q", code, out, errOut)
	}
	out, _, code = run(t, "", "install", "--json", "--no-such-flag")
	if code != 2 || json.Unmarshal([]byte(out), &res) != nil || res.OK || res.Error == "" {
		t.Fatalf("bad flag: %d %q", code, out)
	}
	ok, _, code := run(t, "", "install", "--json", "--dry-run", "--bin-dir", t.TempDir())
	if code != 0 || !strings.Contains(ok, `"ok": true`) {
		t.Fatalf("dry run: %d %s", code, ok)
	}
}

func TestHostMemory_Claude_MCP_기억_서버는_알리고_끄기는_호스트에_맡긴다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": false}`)
	state := filepath.Join(h.claude, ".claude.json")
	stateText := `{"mcpServers": {"kg": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-memory"]}, "other": {"command": "npx", "args": ["x"]}},
		"projects": {"` + h.proj + `": {"mcpServers": {"cloud": {"type": "http", "url": "https://mcp.supermemory.ai/mcp"}}}}}`
	write(t, state, stateText)
	write(t, filepath.Join(h.proj, ".mcp.json"), `{"mcpServers": {"notes": {"command": "uvx", "args": ["basic-memory", "mcp"]}}}`)
	kg := overlapByID(t, "claude-mcp:kg")
	if !kg.On || !kg.Manual || kg.Name != "server-memory" || kg.Off != "claude mcp remove kg --scope user" {
		t.Fatalf("kg %+v", kg)
	}
	if c := overlapByID(t, "claude-mcp:cloud"); c.Name != "supermemory" || c.Off != "claude mcp remove cloud --scope local" {
		t.Fatalf("cloud %+v", c)
	}
	if n := overlapByID(t, "claude-mcp:notes"); n.Name != "basic-memory" || n.Off != "claude mcp remove notes --scope project" {
		t.Fatalf("notes %+v", n)
	}
	out, _, code := run(t, "", "host-memory", "off", "--only", "claude-mcp:kg", "--json")
	if code != 0 || !strings.Contains(out, `"manual"`) || read(t, state) != stateText {
		t.Fatalf("off touched ~/.claude.json (%d): %s", code, out)
	}
	write(t, state, strings.Replace(stateText, `"mcpServers": {"cloud"`, `"disabledMcpServers": ["cloud"], "mcpServers": {"cloud"`, 1))
	if c := overlapByID(t, "claude-mcp:cloud"); c.On {
		t.Fatalf("disabled for this project but on: %+v", c)
	}
	ins, _, _ := run(t, "", "install", "--dry-run", "--json", "--no-hosts", "--bin-dir", t.TempDir())
	if !strings.Contains(ins, "claude mcp remove kg --scope user") {
		t.Fatalf("install askUser: %s", ins)
	}
}

func TestHostMemory_Codex_기억_플러그인과_MCP를_찾아_끄고_되돌린다(t *testing.T) {
	h := newHostPC(t)
	before := "[mcp_servers.mem]\ncommand = \"uvx\"\nargs = [\"mem0-mcp-server\"]\n\n[mcp_servers.figma]\nurl = \"https://mcp.figma.com/mcp\"\n\n[plugins.\"supermemory@supermemory-plugins\"]\nenabled = true\n\n[plugins.\"orbit-mem@orbit-mem\"]\nenabled = true\n"
	write(t, h.toml(), before)
	var ids []string
	ov, _ := hostMemoryOverlaps("")
	for _, o := range ov {
		if o.Host == "codex" && o.On {
			ids = append(ids, o.ID)
		}
	}
	if !slices.Equal(ids, []string{"codex-mcp:mem", "codex-plugin:supermemory@supermemory-plugins"}) {
		t.Fatalf("codex overlaps %v", ids)
	}
	if _, errOut, code := run(t, "", "host-memory", "off", "--host", "codex"); code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	want := "[mcp_servers.mem]\ncommand = \"uvx\"\nargs = [\"mem0-mcp-server\"]\nenabled = false\n\n[mcp_servers.figma]\nurl = \"https://mcp.figma.com/mcp\"\n\n[plugins.\"supermemory@supermemory-plugins\"]\nenabled = false\n\n[plugins.\"orbit-mem@orbit-mem\"]\nenabled = true\n"
	if got := read(t, h.toml()); got != want {
		t.Fatalf("config.toml:\n%s", got)
	}
	run(t, "", "host-memory", "on", "--host", "codex")
	if got := read(t, h.toml()); got != before {
		t.Fatalf("not put back:\n%s", got)
	}
}

func TestHostMemory_아는_기억_목록은_orbit_mem을_빼고_이름으로_찾는다(t *testing.T) {
	for id, want := range map[string]string{"claude-mem@thedotmack": "claude-mem", "mem0@mem0-plugins": "mem0", "claude-supermemory@supermemory-plugins": "supermemory", "orbit-mem@orbit-mem": "", "memory@x": ""} {
		k, ok := memoryPluginOf(id)
		if (want == "") == ok || k.Name != want {
			t.Errorf("%s → %q %v", id, k.Name, ok)
		}
	}
	if _, ok := memoryServerOf("npx", []string{"-y", "@upstash/context7-mcp"}, ""); ok {
		t.Error("context7 is not a memory server")
	}
}

func TestHostMemory_MCP는_토큰으로_찾고_경로_인자는_보지_않는다(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		url     string
		want    string
	}{
		{"npx", []string{"-y", "@modelcontextprotocol/server-memory@latest"}, "", "server-memory"},
		{"uvx", []string{"--prerelease=allow", "basic-memory", "mcp"}, "", "basic-memory"},
		{"uvx", []string{"basic-memory==0.15"}, "", "basic-memory"},
		{"", nil, "https://mcp.supermemory.ai/mcp", "supermemory"},
		{"npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/Users/me/basic-memory"}, "", ""},
		{"node", []string{"/opt/mem0-mcp-server/index.js"}, "", ""},
	}
	for _, c := range cases {
		k, ok := memoryServerOf(c.command, c.args, c.url)
		if (c.want == "") == ok || k.Name != c.want {
			t.Errorf("%v %v %s → %q %v", c.command, c.args, c.url, k.Name, ok)
		}
	}
}

func TestTOML_여러_줄_배열과_따옴표_뒤_주석을_읽는다(t *testing.T) {
	ts := tomlTables("[mcp_servers.kg]\ncommand = \"npx\" # basic-memory\nargs = [\n  \"-y\",\n  \"@modelcontextprotocol/server-memory\",\n]\n")
	if len(ts) != 1 || ts[0].Keys["command"] != `"npx"` || !strings.Contains(ts[0].Keys["args"], "server-memory") {
		t.Fatalf("%+v", ts)
	}
	if got := tomlStrings(ts[0].Keys["args"]); !slices.Equal(got, []string{"-y", "@modelcontextprotocol/server-memory"}) {
		t.Fatalf("%q", got)
	}
}

func TestHostMemory_mcp_json_서버는_승인된_것만_켜진_것으로_본다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"autoMemoryEnabled": false}`)
	write(t, filepath.Join(h.proj, ".mcp.json"), `{"mcpServers": {"notes": {"command": "uvx", "args": ["basic-memory", "mcp"]}}}`)
	write(t, filepath.Join(h.claude, ".claude.json"), `{"mcpServers": {"notes2": {"command": "uvx", "args": ["basic-memory", "mcp"]}}, "projects": {"`+h.proj+`": {"disabledMcpServers": ["notes"]}}}`)
	if o := overlapByID(t, "claude-mcp:notes"); o.On {
		t.Fatalf("not approved but on: %+v", o)
	}
	local := filepath.Join(h.proj, ".claude", "settings.local.json")
	write(t, local, `{"enabledMcpjsonServers": ["notes"]}`)
	if o := overlapByID(t, "claude-mcp:notes"); !o.On {
		t.Fatalf("approved, and disabledMcpServers is for user/local servers only: %+v", o)
	}
	write(t, local, `{"enableAllProjectMcpServers": true, "disabledMcpjsonServers": ["notes"]}`)
	if o := overlapByID(t, "claude-mcp:notes"); o.On {
		t.Fatalf("refused but on: %+v", o)
	}
}

func TestInstall_json_실패에도_진행한_단계를_싣고_help와_json_false는_따로_다룬다(t *testing.T) {
	h := newHostPC(t)
	blocker := filepath.Join(h.home, "not-a-dir")
	write(t, blocker, "x")
	out, _, _ := run(t, "", "install", "--json", "--no-hosts", "--bin-dir", filepath.Join(blocker, "bin"))
	var res struct {
		Binary string
		Steps  []string
	}
	if json.Unmarshal([]byte(out), &res) != nil || res.Binary == "" || len(res.Steps) == 0 {
		t.Fatalf("%s", out)
	}
	if out, _, code := run(t, "", "install", "--json=false", "--bogus"); code == 0 || strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("--json=false gave JSON: %d %q", code, out)
	}
	if out, errOut, code := run(t, "", "install", "--json", "--help"); code != 2 || strings.TrimSpace(out) != "" || !strings.Contains(errOut, "-dry-run") {
		t.Fatalf("--help: %d %q %q", code, out, errOut)
	}
}

func TestHostMemory_설정의_env_블록으로_끈_auto_memory도_꺼짐이다(t *testing.T) {
	h := newHostPC(t)
	write(t, h.settings(), `{"env": {"CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1"}}`)
	if o := overlapByID(t, "claude-auto-memory"); o.On || o.Source == nil || o.Source.Scope != "user" {
		t.Fatalf("%+v %+v", o, o.Source)
	}
}
