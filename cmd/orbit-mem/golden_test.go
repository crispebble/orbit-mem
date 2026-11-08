package main

// 회상 출력 고정 시험(특성 시험): 훅·inject·tool이 지금 내는 stdout을 바이트 그대로 testdata/golden에 두고 대조한다.
// 이후 DB 열기·훅·inject·tool을 다시 짜도 회상 동작이 같음을 이 시험으로 본다. 처음 만들거나 일부러 바꿀 때만
//   go test ./cmd/orbit-mem -run TestRecallGolden -update-golden
// 로 갱신한다. 기억 id·날짜·임시 경로는 같은 길이(경로는 자리표)로 바꿔 두어 결정적이다.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/version"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite cmd/orbit-mem/testdata/golden")

type goldenWorld struct {
	t    *testing.T
	home string
	dir  string // 프로젝트 폴더(git 저장소, 제품 demo)
	s    *memory.Store
	ids  map[string]string // 기억 id → 이름
}

var (
	goldenIDRe   = regexp.MustCompile(`m-[0-9a-f]{12}`)
	goldenDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	goldenMsRe   = regexp.MustCompile(`("(?:updatedAt|createdAt|at)": )\d{13}`)
)

// newGoldenWorld makes the fixed memory set: user core, product core, confirmed rule, decisions,
// a tool-trigger rule, a candidate, and (for the correction line) a decision a later save replaces.
func newGoldenWorld(t *testing.T) *goldenWorld {
	t.Helper()
	h := home(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "work", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	cfg := config.Default()
	cfg.Products = place.Map{"demo": {Match: []string{filepath.ToSlash(dir)}}}
	if err := config.Save(paths().Config(), cfg); err != nil {
		t.Fatal(err)
	}
	w := &goldenWorld{t: t, home: h, dir: dir, s: seed(t), ids: map[string]string{}}
	// 만든 시각은 지금 기준 상대값이다(오래된 후보가 낡아 빠지는 시간 의존을 피한다). 순서만 고정한다.
	now := time.Now()
	at := func(hoursAgo int) int64 { return now.Add(-time.Duration(hoursAgo) * time.Hour).UnixMilli() }
	quote := func(x string) memory.Source { return memory.Source{Kind: "quote", Excerpt: x} }
	w.save("userpin", memory.SaveInput{Kind: "rule", Scope: memory.ScopeGlobal, Title: "답은 존댓말", Body: "반말을 쓰지 않는다", Topic: "말투", Source: quote("존댓말로 해"), At: at(10)}, true)
	w.save("productpin", memory.SaveInput{Kind: "decision", Scope: "product:demo", Title: "demo는 포트-어댑터 구조", Body: "도메인은 바깥을 모른다", Topic: "demo 구조", Source: quote("포트-어댑터로"), At: at(9)}, true)
	w.save("rule", memory.SaveInput{Kind: "rule", Scope: "product:demo", Title: "커밋 제목은 영어 명령형", Body: "첫 줄은 동사로 시작한다", Topic: "커밋 제목", Source: quote("영어 명령형으로"), At: at(8)}, false)
	w.save("decision", memory.SaveInput{Kind: "decision", Scope: "product:demo", Title: "배포 전에 스모크 시험을 돈다", Body: "배포 전 확인 절차", Topic: "배포 절차", Source: quote("스모크 먼저"), At: at(7)}, false)
	w.save("old", memory.SaveInput{Kind: "decision", Scope: "product:demo", Title: "시험은 모의 DB로 돈다", Body: "시험 DB는 모의로", Topic: "시험 DB", Source: quote("모의 DB로"), At: at(6)}, false)
	w.save("tool", memory.SaveInput{Kind: "rule", Scope: "product:demo", Title: "문서는 Atlas 공간에 쓴다", Body: "write_page 전에 위치를 확인한다", Topic: "문서 위치", Tools: []string{"mcp__atlas__write_page"}, Source: quote("Atlas에 써"), At: at(5)}, false)
	w.save("candidate", memory.SaveInput{Kind: "lesson", Scope: "product:demo", Title: "배포 스크립트는 드라이런이 먼저", Body: "배포 스크립트를 처음 돌릴 때는 드라이런으로 본다", Tags: []string{"배포"}, Source: memory.Source{Kind: "session", Ref: "golden"}, At: at(4)}, false)
	return w
}

func (w *goldenWorld) save(name string, in memory.SaveInput, pin bool) memory.Memory {
	w.t.Helper()
	time.Sleep(2 * time.Millisecond) // 갱신 시각이 서로 달라야 최근순 정렬이 id 순서(무작위)로 갈리지 않는다
	m, err := w.s.Save(in)
	if err != nil {
		w.t.Fatalf("%s: %v", name, err)
	}
	if pin {
		if _, err := w.s.Pin(m.ID, memory.Who{}); err != nil {
			w.t.Fatalf("pin %s: %v", name, err)
		}
	}
	w.ids[m.ID] = name
	return m
}

// normalize keeps the length of what it replaces where it can: an id becomes m- plus its name padded
// with _ to 12 characters, a date becomes 0000-00-00. Paths become <DIR>, <HOME> and the version <VERSION>.
func (w *goldenWorld) normalize(s string) string {
	s = goldenIDRe.ReplaceAllStringFunc(s, func(id string) string {
		name, ok := w.ids[id]
		if !ok {
			name = fmt.Sprintf("unnamed%d", len(w.ids))
			w.ids[id] = name
		}
		return "m-" + fmt.Sprintf("%-12s", name)[:12]
	})
	s = strings.ReplaceAll(s, strings.ReplaceAll(w.dir, `/`, `\/`), "<DIR>") // JSON이 슬래시를 이스케이프하는 경우를 대비
	s = strings.ReplaceAll(s, w.dir, "<DIR>")
	s = strings.ReplaceAll(s, w.home, "<HOME>")
	s = goldenDateRe.ReplaceAllString(s, "0000-00-00")
	s = goldenMsRe.ReplaceAllString(s, "${1}0000000000000")
	return strings.ReplaceAll(s, version.Version, "<VERSION>")
}

// check compares the normalized output with testdata/golden/<name>; -update-golden writes it.
func (w *goldenWorld) check(name, out string) {
	w.t.Helper()
	got := w.normalize(out)
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			w.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatalf("%s: %v (처음이면 -update-golden)", name, err)
	}
	if string(want) != got {
		w.t.Errorf("%s differs\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// hook runs orbit-mem hook and checks it ends 0 with the stderr quiet.
func (w *goldenWorld) hook(event, host, stdin string, extra ...string) string {
	w.t.Helper()
	out, errOut, code := run(w.t, stdin, append([]string{"hook", event, "--host", host}, extra...)...)
	if code != 0 || errOut != "" {
		w.t.Fatalf("hook %s: code %d err %q", event, code, errOut)
	}
	return out
}

func (w *goldenWorld) hookIn(session, source, prompt, tool, agent string) string {
	m := map[string]string{"session_id": session, "cwd": w.dir, "source": source, "prompt": prompt, "tool_name": tool, "agent_id": agent}
	return jsonString(w.t, m)
}

// 훅 입력은 폴더가 필요하다: Claude Code는 CLAUDE_PROJECT_DIR, Codex는 cwd로 읽는다. 둘 다 같은 폴더로 고정한다.
func (w *goldenWorld) claudeEnv() { w.t.Setenv("CLAUDE_PROJECT_DIR", w.dir) }

func TestRecallGoldenSessionStartClaude(t *testing.T) {
	w := newGoldenWorld(t)
	w.claudeEnv()
	w.check("hook-session-start-claude-startup.golden", w.hook("session-start", "claude", w.hookIn("g1", "startup", "", "", "")))
	w.check("hook-session-start-claude-resume.golden", w.hook("session-start", "claude", w.hookIn("g1", "resume", "", "", "")))
	// resume은 같은 묶음이다. 그 사이 확정된 새 결정은 compact(묶음 새로)에서만 들어온다.
	w.save("afterstart", memory.SaveInput{Kind: "decision", Scope: "product:demo", Title: "릴리스는 목요일에 낸다", Body: "릴리스 요일", Topic: "릴리스 요일", Source: memory.Source{Kind: "quote", Excerpt: "목요일에"}}, false)
	w.check("hook-session-start-claude-compact.golden", w.hook("session-start", "claude", w.hookIn("g1", "compact", "", "", "")))
}

func jsonString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// save after the session started must be later by the clock the bundle time is kept in (ms).
func (w *goldenWorld) later() { time.Sleep(5 * time.Millisecond) }

func TestRecallGoldenPromptClaude(t *testing.T) {
	w := newGoldenWorld(t)
	w.claudeEnv()
	const ask = "배포 전에 스모크 시험을 돌리고 배포 스크립트는 드라이런으로 본다"
	w.hook("session-start", "claude", w.hookIn("g2", "startup", "", "", ""))
	w.check("hook-prompt-claude-first.golden", w.hook("prompt", "claude", w.hookIn("g2", "", ask, "", "")))
	w.check("hook-prompt-claude-repeat.golden", w.hook("prompt", "claude", w.hookIn("g2", "", ask, "", "")))
	w.check("hook-prompt-claude-nomatch.golden", w.hook("prompt", "claude", w.hookIn("g2", "", "오늘 점심은 뭐가 좋을까", "", "")))
	// 세션 중 같은 주제의 새 값(정정 줄)과 다른 곳에서 확정된 새 기억(새 기억 줄). 세션 시작에 「최근 결정」 줄이 없으니(T-3)
	// 옛 값은 요청 회상으로 먼저 세션에 알린다.
	w.hook("prompt", "claude", w.hookIn("g2", "", "시험은 모의 DB로 돈다 맞나", "", ""))
	w.later()
	w.save("replaced", memory.SaveInput{Kind: "decision", Scope: "product:demo", Title: "시험은 실제 DB로 돈다", Body: "모의 대신 실제 DB", Topic: "시험 DB", Source: memory.Source{Kind: "quote", Excerpt: "실제 DB로"}}, false)
	w.save("fresh", memory.SaveInput{Kind: "rule", Scope: "product:demo", Title: "릴리스 노트는 한국어로", Body: "릴리스 노트 문서는 한국어", Topic: "릴리스 노트", Source: memory.Source{Kind: "quote", Excerpt: "한국어로"}}, false)
	w.check("hook-prompt-claude-news.golden", w.hook("prompt", "claude", w.hookIn("g2", "", ask, "", "")))
}

func TestRecallGoldenPreToolClaude(t *testing.T) {
	w := newGoldenWorld(t)
	w.claudeEnv()
	w.hook("session-start", "claude", w.hookIn("g3", "startup", "", "", ""))
	w.check("hook-pre-tool-claude-hit.golden", w.hook("pre-tool", "claude", w.hookIn("g3", "", "", "mcp__atlas__write_page", "")))
	w.check("hook-pre-tool-claude-repeat.golden", w.hook("pre-tool", "claude", w.hookIn("g3", "", "", "mcp__atlas__write_page", "")))
	w.check("hook-pre-tool-claude-miss.golden", w.hook("pre-tool", "claude", w.hookIn("g3", "", "", "Bash", "")))
	w.check("hook-pre-tool-claude-agent.golden", w.hook("pre-tool", "claude", w.hookIn("g3", "", "", "mcp__atlas__write_page", "sub1")))
	w.check("hook-pre-tool-claude-agent-repeat.golden", w.hook("pre-tool", "claude", w.hookIn("g3", "", "", "mcp__atlas__write_page", "sub1")))
}

func TestRecallGoldenSubagentStartClaude(t *testing.T) {
	w := newGoldenWorld(t)
	w.claudeEnv()
	w.check("hook-subagent-start-claude.golden", w.hook("subagent-start", "claude", w.hookIn("g4", "", "", "", "sub1")))
}

func TestRecallGoldenCodex(t *testing.T) {
	w := newGoldenWorld(t) // Codex는 CLAUDE_PROJECT_DIR를 주지 않는다: 폴더는 훅 입력의 cwd다
	w.check("hook-session-start-codex.golden", w.hook("session-start", "codex", w.hookIn("c1", "startup", "", "", "")))
	w.check("hook-prompt-codex.golden", w.hook("prompt", "codex", w.hookIn("c1", "", "배포 전에 스모크 시험을 돌린다", "", "")))
}

func TestRecallGoldenPluginVersionNote(t *testing.T) {
	w := newGoldenWorld(t)
	w.claudeEnv()
	w.check("hook-session-start-claude-other-plugin.golden", w.hook("session-start", "claude", w.hookIn("p1", "startup", "", "", ""), "--plugin-version", "0.3.9"))
}

func TestRecallGoldenInject(t *testing.T) {
	w := newGoldenWorld(t)
	inj := func(stdin string, args ...string) string {
		t.Helper()
		out, errOut, code := run(t, stdin, append([]string{"inject"}, args...)...)
		if code != 0 || errOut != "" {
			t.Fatalf("inject %v: code %d err %q", args, code, errOut)
		}
		return out
	}
	w.check("inject-session-start-full.golden", inj("", "session-start", "--host", "pi", "--cwd", w.dir, "--session", "i1"))
	w.check("inject-session-start-brief.golden", inj("", "session-start", "--host", "pi", "--cwd", w.dir, "--session", "i2", "--rules", "brief"))
	w.check("inject-prompt.golden", inj("배포 전에 스모크 시험을 돌리고 배포 스크립트는 드라이런으로 본다", "prompt", "--host", "pi", "--cwd", w.dir, "--session", "i1"))
	w.check("inject-prompt-brief.golden", inj("배포 전에 스모크 시험을 돌리고 배포 스크립트는 드라이런으로 본다", "prompt", "--host", "pi", "--cwd", w.dir, "--session", "i2", "--rules", "brief"))
	w.check("inject-session-start-compact.golden", inj("", "session-start", "--host", "pi", "--cwd", w.dir, "--session", "i1", "--source", "compact"))
}

// 세션 폴더(CLAUDE_PROJECT_DIR)와 훅 입력의 cwd가 다를 때: 회상 위치는 세션 폴더의 제품이고,
// 호스트 기억 안내 줄은 날 cwd의 (겹치는) 설정으로 판정한다.
func TestRecallGoldenSessionDirDiffersFromCwd(t *testing.T) {
	w := newGoldenWorld(t)
	w.claudeEnv()
	other := filepath.Join(filepath.Dir(w.dir), "other")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Claude Code 설정 폴더가 있어야 겹침을 본다(home이 가리킨 자리에 만든다).
	if err := os.MkdirAll(os.Getenv("CLAUDE_CONFIG_DIR"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(other, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"enabledPlugins": {"claude-mem@thedotmack": true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := func(session, source, prompt string) string {
		return jsonString(t, map[string]string{"session_id": session, "cwd": other, "source": source, "prompt": prompt})
	}
	w.check("hook-session-start-claude-cwd-differs.golden", w.hook("session-start", "claude", in("d1", "startup", "")))
	w.check("hook-prompt-claude-cwd-differs.golden", w.hook("prompt", "claude", in("d1", "", "배포 전에 스모크 시험을 돌리고 배포 스크립트는 드라이런으로 본다")))
}

// DB가 없으면 session-start는 빈 주입을, delivered는 아무 출력 없이 0으로 끝난다.
func TestRecallGoldenInjectWithoutDB(t *testing.T) {
	home(t)
	out, errOut, code := run(t, "", "inject", "session-start", "--host", "pi")
	if code != 0 || errOut != "" || out != "{\n  \"memoryIds\": [],\n  \"text\": \"\"\n}\n" {
		t.Fatalf("session-start: code %d out %q err %q", code, out, errOut)
	}
	out, errOut, code = run(t, "", "inject", "delivered", "--host", "pi", "--session", "s1", "--ids", "a,b")
	if code != 0 || errOut != "" || out != "" {
		t.Fatalf("delivered: code %d out %q err %q", code, out, errOut)
	}
}

// tool memory_find는 종료 코드와 결과 id 목록(순서 포함)만 고정한다. 결과 세부 꼴은 바뀔 수 있다.
func TestRecallGoldenToolMemoryFind(t *testing.T) {
	w := newGoldenWorld(t)
	out, errOut, code := run(t, `{"query":"배포 스모크 시험"}`, "tool", "memory_find", "--host", "pi", "--cwd", w.dir, "--session", "t1")
	if code != 0 || errOut != "" {
		t.Fatalf("tool memory_find: code %d err %q", code, errOut)
	}
	var res struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got []string
	for _, it := range res.Items {
		got = append(got, w.ids[it.ID])
	}
	want := []string{"decision", "old", "candidate"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids %v, want %v", got, want)
	}
}
