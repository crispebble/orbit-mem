package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// onTerminal makes the thin commands see a terminal on stdout.
func onTerminal(t *testing.T) {
	t.Helper()
	old := outputIsTerminal
	outputIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { outputIsTerminal = old })
}

func thinSeed(t *testing.T) (*memory.Store, memory.Memory, memory.Memory) {
	t.Helper()
	s := seed(t)
	a, err := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Tags: []string{"말투"}, Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save(memory.SaveInput{Kind: "fact", Title: "운영은 서버A", Body: "배포는 감시기가 한다", Source: memory.Source{Kind: "file", Ref: "/x/README.md"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, a, b
}

func runOK(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, stdin, args...)
	if code != 0 {
		t.Fatalf("%v: 종료 %d %s", args, code, errOut)
	}
	return out
}

// 끝 기준(S6): stdout이 터미널이면 사람용 글, 파이프면 JSON, --json이면 언제나 JSON.
func TestThin_터미널과_파이프(t *testing.T) {
	home(t)
	_, a, _ := thinSeed(t)
	var piped struct {
		Items []struct{ ID, Title string }
	}
	if err := json.Unmarshal([]byte(runOK(t, "", "search", "존댓말")), &piped); err != nil || len(piped.Items) != 1 || piped.Items[0].ID != a.ID {
		t.Fatalf("파이프는 JSON: %+v %v", piped, err)
	}
	onTerminal(t)
	out := runOK(t, "", "search", "존댓말")
	if strings.HasPrefix(strings.TrimSpace(out), "{") || !strings.Contains(out, a.ID) || !strings.Contains(out, "답은 존댓말") {
		t.Fatalf("터미널은 사람용: %q", out)
	}
	if err := json.Unmarshal([]byte(runOK(t, "", "search", "존댓말", "--json")), &piped); err != nil || len(piped.Items) != 1 {
		t.Fatalf("--json: %v", err)
	}
}

// 끝 기준: search·show·list는 memory_find·memory_get과 같은 결과를 준다.
func TestThin_search_show_list(t *testing.T) {
	home(t)
	s, a, b := thinSeed(t)
	if _, err := s.Update(memory.UpdateInput{ID: b.ID, Action: "stale", State: ptr(memory.StateStale), Reason: "옮김"}); err != nil {
		t.Fatal(err)
	}
	var found struct {
		Items []struct{ ID, Kind, State string }
	}
	json.Unmarshal([]byte(runOK(t, "", "search", "서버A")), &found)
	if len(found.Items) != 0 {
		t.Fatalf("낡은 기억은 --stale일 때만: %+v", found)
	}
	json.Unmarshal([]byte(runOK(t, "", "search", "서버A", "--stale")), &found)
	if len(found.Items) != 1 || found.Items[0].State != "stale" {
		t.Fatalf("--stale: %+v", found)
	}
	json.Unmarshal([]byte(runOK(t, "", "search", "--kind", "fact", "--stale", "-n", "5")), &found)
	if len(found.Items) != 1 || found.Items[0].Kind != "fact" {
		t.Fatalf("--kind: %+v", found)
	}

	var got struct {
		Items   []memory.Memory
		History map[string][]memory.Version
	}
	json.Unmarshal([]byte(runOK(t, "", "show", a.ID, "--history")), &got)
	if len(got.Items) != 1 || got.Items[0].Body != "반말 금지" || len(got.History[a.ID]) == 0 {
		t.Fatalf("show: %+v", got)
	}
	if _, errOut, code := run(t, "", "show", "m-none"); code != 1 || !strings.Contains(errOut, "orbit-mem show") {
		t.Fatalf("없는 id: %d %q", code, errOut)
	}
	if _, _, code := run(t, "", "show"); code != 2 {
		t.Fatalf("id 없이: %d", code)
	}

	json.Unmarshal([]byte(runOK(t, "", "list")), &found)
	if len(found.Items) != 1 || found.Items[0].ID != a.ID {
		t.Fatalf("list 기본은 후보·확정: %+v", found)
	}
	json.Unmarshal([]byte(runOK(t, "", "list", "--state", "stale")), &found)
	if len(found.Items) != 1 || found.Items[0].ID != b.ID {
		t.Fatalf("list --state stale: %+v", found)
	}
	json.Unmarshal([]byte(runOK(t, "", "list", "--tag", "말투", "--state", "confirmed", "--state", "stale")), &found)
	if len(found.Items) != 1 || found.Items[0].ID != a.ID {
		t.Fatalf("list --tag: %+v", found)
	}
	// show는 full로 불러 길어도 전문을 보이고 적용 대상을 보인다.
	long := strings.Repeat("긴 본문 한 줄입니다.\n", 400)
	c, err := s.Save(memory.SaveInput{Kind: "fact", Title: "긴 본문 기억", Body: long, Source: memory.Source{Kind: "file", Ref: "/x/long.md"}})
	if err != nil {
		t.Fatal(err)
	}
	onTerminal(t)
	out := runOK(t, "", "show", c.ID)
	if !strings.Contains(out, strings.TrimSpace(long)) || strings.Contains(out, "전문은 full: true") || !strings.Contains(out, "user") {
		t.Fatalf("show는 전문과 적용 대상을 보입니다: %q", out[:200])
	}
	onTerminal(t)
	if out := runOK(t, "", "show", a.ID); !strings.Contains(out, "반말 금지") || strings.HasPrefix(out, "{") {
		t.Fatalf("show 사람용: %q", out)
	}
	if out := runOK(t, "", "list"); !strings.Contains(out, a.ID) {
		t.Fatalf("list 사람용: %q", out)
	}
}

func ptr[T any](v T) *T { return &v }

// 끝 기준: edit·stale은 memory_update와 같은 규칙(판 올림, 이력, 지우지 않음)을 따른다.
func TestThin_edit_stale(t *testing.T) {
	home(t)
	s, a, b := thinSeed(t)
	body := filepath.Join(t.TempDir(), "body.md")
	os.WriteFile(body, []byte("반말 금지, 예외 없음"), 0o600)
	var res struct{ Memory memory.Memory }
	json.Unmarshal([]byte(runOK(t, "", "edit", a.ID, "--title", "답은 언제나 존댓말", "--body-file", body, "--tag", "말투", "--tag", "응답", "--link", b.ID, "--note", "사용자 지시")), &res)
	if res.Memory.Title != "답은 언제나 존댓말" || res.Memory.Body != "반말 금지, 예외 없음" || len(res.Memory.Tags) != 2 || res.Memory.Version != a.Version+1 {
		t.Fatalf("edit: %+v", res.Memory)
	}
	if links, _ := s.LinksOf(a.ID); len(links) != 1 {
		t.Fatalf("--link: %+v", links)
	}
	json.Unmarshal([]byte(runOK(t, "", "edit", a.ID, "--unlink", b.ID)), &res)
	if links, _ := s.LinksOf(a.ID); len(links) != 0 {
		t.Fatalf("--unlink: %+v", links)
	}
	if _, _, code := run(t, "", "edit", a.ID, "--body", "x", "--body-file", body); code != 2 {
		t.Fatalf("--body와 --body-file은 함께 쓰지 않는다: %d", code)
	}
	json.Unmarshal([]byte(runOK(t, "", "stale", b.ID, "--note", "옮김")), &res)
	if res.Memory.State != memory.StateStale {
		t.Fatalf("stale: %+v", res.Memory)
	}
	m, err := s.Get(b.ID)
	h, _ := s.History(b.ID)
	if err != nil || m.State != memory.StateStale || h[len(h)-1].Action != "stale" || h[len(h)-1].Reason != "옮김" {
		t.Fatalf("낡음은 지우지 않고 이력에 남긴다: %+v %+v %v", m, h, err)
	}
}

// 끝 기준: export한 jsonl을 빈 DB에 import하면 같은 기억이 된다. 있는 id는 건너뛰고 이력에 남긴다.
func TestThin_export_import_왕복(t *testing.T) {
	home(t)
	s, a, b := thinSeed(t)
	s.Link(memory.LinkInput{From: a.ID, To: b.ID, Note: "배포 말투", Origin: "user"})
	out := runOK(t, "", "export")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl 줄 %d:\n%s", len(lines), out)
	}
	file := filepath.Join(t.TempDir(), "mem.jsonl")
	runOK(t, "", "export", "--out", file)
	if b2, _ := os.ReadFile(file); string(b2) != out {
		t.Fatalf("--out 파일과 stdout이 다르다")
	}
	md := filepath.Join(t.TempDir(), "md")
	runOK(t, "", "export", "--format", "md", "--out", md)
	page, err := os.ReadFile(filepath.Join(md, a.ID+".md"))
	if err != nil || !strings.Contains(string(page), "# 답은 존댓말") || !strings.Contains(string(page), "반말 금지") {
		t.Fatalf("md: %v %s", err, page)
	}
	if idx, _ := os.ReadFile(filepath.Join(md, "index.md")); !strings.Contains(string(idx), a.ID) {
		t.Fatalf("md 목차: %s", idx)
	}

	// 다른 PC(빈 DB)로
	home(t)
	var rep struct {
		DryRun   bool
		Imported []string
		Skipped  []struct{ ID, Reason string }
	}
	json.Unmarshal([]byte(runOK(t, "", "import", file, "--dry-run")), &rep)
	if !rep.DryRun || len(rep.Imported) != 2 {
		t.Fatalf("미리 보기: %+v", rep)
	}
	s2 := seed(t)
	if n, _ := s2.Count(); n != 0 {
		t.Fatalf("미리 보기가 썼습니다: %d", n)
	}
	json.Unmarshal([]byte(runOK(t, "", "import", file)), &rep)
	if rep.DryRun || len(rep.Imported) != 2 || len(rep.Skipped) != 0 {
		t.Fatalf("들여오기: %+v", rep)
	}
	got, err := s2.Get(a.ID)
	if err != nil || got.Title != a.Title || got.Body != a.Body || got.State != a.State || got.Kind != a.Kind || got.Scope != a.Scope || len(got.Sources) != 1 || got.CreatedAt != a.CreatedAt {
		t.Fatalf("왕복: %+v %v", got, err)
	}
	if links, _ := s2.LinksOf(a.ID); len(links) != 1 {
		t.Fatalf("연결도 들여온다: %+v", links)
	}
	h, _ := s2.History(a.ID)
	if h[len(h)-1].Action != "import" {
		t.Fatalf("들여온 것은 이력에 남는다: %+v", h)
	}
	json.Unmarshal([]byte(runOK(t, "", "import", file)), &rep)
	if len(rep.Imported) != 0 || len(rep.Skipped) != 2 || rep.Skipped[0].Reason != "exists" {
		t.Fatalf("있는 id는 건너뛴다: %+v", rep)
	}
	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	os.WriteFile(bad, []byte("{\"id\":\"m-x\",\"kind\":\"bogus\",\"title\":\"t\",\"scope\":\"global\",\"state\":\"confirmed\"}\nnot json\n"), 0o600)
	json.Unmarshal([]byte(runOK(t, "", "import", bad)), &rep)
	if len(rep.Imported) != 0 || len(rep.Skipped) != 2 || rep.Skipped[0].Reason != "invalid" || rep.Skipped[1].Reason != "invalid" {
		t.Fatalf("틀린 줄은 건너뛴다: %+v", rep)
	}
}

// 끝 기준: activity는 activity_find와 같은 결과(새 기록 + 옛 work.db, 최신순)를 준다.
func TestThin_activity(t *testing.T) {
	home(t)
	seed(t)
	hook(t, hookIn("A1", "/tmp/proj-a", "로그인 고치기"), "prompt", "--host", "claude")
	hook(t, hookIn("A2", "/tmp/proj-b", "문서 쓰기"), "prompt", "--host", "codex")
	var res struct {
		Items []struct{ Source, Host, Session, FirstPrompt string }
	}
	json.Unmarshal([]byte(runOK(t, "", "activity", "--project", "all")), &res)
	if len(res.Items) != 2 || res.Items[0].Session != "A2" || res.Items[0].Source != "activity" {
		t.Fatalf("최신순: %+v", res)
	}
	json.Unmarshal([]byte(runOK(t, "", "activity", "로그인", "--project", "all")), &res)
	if len(res.Items) != 1 || res.Items[0].Session != "A1" {
		t.Fatalf("질의: %+v", res)
	}
	json.Unmarshal([]byte(runOK(t, "", "activity", "--project", "all", "--host", "codex", "--since", "2020-01-01", "-n", "5")), &res)
	if len(res.Items) != 1 || res.Items[0].Host != "codex" {
		t.Fatalf("--host: %+v", res)
	}
	if _, errOut, code := run(t, "", "activity", "--since", "어제"); code != 1 || !strings.Contains(errOut, "orbit-mem activity") {
		t.Fatalf("틀린 --since: %d %q", code, errOut)
	}
	onTerminal(t)
	if out := runOK(t, "", "activity", "--project", "all"); !strings.Contains(out, "A1") || !strings.Contains(out, "codex") {
		t.Fatalf("사람용: %q", out)
	}
}

// 끝 기준: memory find|show는 없앴다.
func TestThin_memory_명령은_없다(t *testing.T) {
	home(t)
	if _, errOut, code := run(t, "", "memory", "find", "x"); code != 2 || !strings.Contains(errOut, "memory") {
		t.Fatalf("%d %q", code, errOut)
	}
}

// id가 폴더 밖을 가리키면 들여오지 않고, 내보내기도 폴더 밖에 쓰지 않는다.
func TestThin_import는_이상한_id를_거절하고_export는_폴더_밖에_쓰지_않는다(t *testing.T) {
	home(t)
	s := seed(t)
	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	line := func(id string) string {
		return `{"id":"` + id + `","kind":"fact","title":"t-` + strings.ReplaceAll(id, "/", "_") + `","scope":"global","state":"confirmed"}` + "\n"
	}
	os.WriteFile(bad, []byte(line("../escaped")+line("a/b")+line("m-ok1")), 0o600)
	var rep struct {
		Imported []string
		Skipped  []struct{ ID, Reason string }
	}
	json.Unmarshal([]byte(runOK(t, "", "import", bad)), &rep)
	if len(rep.Imported) != 1 || rep.Imported[0] != "m-ok1" || len(rep.Skipped) != 2 || rep.Skipped[0].Reason != "invalid" || rep.Skipped[1].Reason != "invalid" {
		t.Fatalf("이상한 id는 invalid: %+v", rep)
	}
	if n, _ := s.Count(); n != 1 {
		t.Fatalf("기억 수 %d", n)
	}
	// export md: 방어선(DB에 이미 이상한 id가 있어도 폴더 안에만 쓴다)
	root := t.TempDir()
	dir := filepath.Join(root, "out")
	if err := exportMarkdown(dir, []memory.Memory{{ID: "../escaped", Title: "x", Kind: "fact", State: "confirmed", Scope: "global"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.md")); err == nil {
		t.Fatal("폴더 밖에 썼습니다")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.md")); err != nil {
		t.Fatalf("폴더 안에는 써야 합니다: %v", err)
	}
}
