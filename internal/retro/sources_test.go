package retro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// fileMemory saves a confirmed memory standing on a file, as the old Knowledge
// migration does: the file's hash at that time and, when given, an excerpt.
func fileMemory(t *testing.T, e *env, title, path, excerpt string) memory.Memory {
	t.Helper()
	m, err := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Body: title + " 본문", Scope: memory.ScopeGlobal, State: memory.StateConfirmed,
		Source: memory.Source{Kind: memory.SourceFile, Ref: path, Excerpt: excerpt, Hash: memory.FileHash(path)}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func sourceHash(t *testing.T, e *env, id string) string {
	t.Helper()
	m, err := e.s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range m.Sources {
		if src.Kind == memory.SourceFile {
			return src.Hash
		}
	}
	t.Fatalf("no file source on %s", id)
	return ""
}

type checkEvent struct {
	Outcome  string `json:"outcome"`
	Memories int    `json:"memories"`
	Proposed bool   `json:"proposed"`
}

func sourceEvents(t *testing.T, e *env) []checkEvent {
	t.Helper()
	evs, err := e.s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: memory.EventSourceCheck})
	if err != nil {
		t.Fatal(err)
	}
	var out []checkEvent
	for _, ev := range evs {
		var c checkEvent
		json.Unmarshal(ev.Data, &c)
		out = append(out, c)
	}
	return out
}

func TestSources_발췌가_남은_파일이_바뀌면_제안하지_않고_해시만_갱신한다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "AGENTS.md")
	w(t, f, "# 규칙\n- 커밋은 경로를 명시한다.\n")
	m := fileMemory(t, e, "커밋 경로", f, "커밋은 경로를 명시한다.")
	w(t, f, "# 규칙\n\n- 응답은 존댓말로 한다.\n- 커밋은   경로를\n  명시한다.\n")

	res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusEmpty || len(e.calls) != 0 {
		t.Fatalf("a kept excerpt needs no host: %+v calls=%d", res, len(e.calls))
	}
	if pending, _ := e.s.Proposals(memory.StatusPending); len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
	if got := sourceHash(t, e, m.ID); got != memory.FileHash(f) {
		t.Fatalf("hash not updated: %s", got)
	}
	if evs := sourceEvents(t, e); len(evs) != 1 || evs[0] != (checkEvent{Outcome: memory.SourceKept, Memories: 1}) {
		t.Fatalf("events = %+v", evs)
	}
}

func TestSources_발췌가_사라진_파일은_같은_파일의_기억을_한_제안으로_묶는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "rules.md")
	w(t, f, "- 탭은 두 칸이다.\n- 배포는 금요일에 하지 않는다.\n- 커밋은 경로를 명시한다.\n")
	a := fileMemory(t, e, "탭 두 칸", f, "탭은 두 칸이다.")
	b := fileMemory(t, e, "금요일 배포 금지", f, "배포는 금요일에 하지 않는다.")
	c := fileMemory(t, e, "커밋 경로", f, "커밋은 경로를 명시한다.")
	w(t, f, "- 탭은 네 칸이다.\n- 커밋은 경로를 명시한다.\n")

	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	pending, _ := e.s.Proposals(memory.StatusPending)
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	p := pending[0]
	if p.Kind != memory.ProposalStale || strings.Join(p.MemoryIDs, ",") != strings.Join(sorted(a.ID, b.ID), ",") {
		t.Fatalf("proposal = %+v", p)
	}
	for _, want := range []string{f, "탭은 두 칸이다.", "배포는 금요일에 하지 않는다."} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("body must show %q: %s", want, p.Body)
		}
	}
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if got := sourceHash(t, e, id); got != memory.FileHash(f) {
			t.Fatalf("%s hash not updated: %s", id, got)
		}
	}
	got := map[string]checkEvent{}
	for _, ev := range sourceEvents(t, e) {
		got[ev.Outcome] = ev
	}
	if got[memory.SourceGone] != (checkEvent{Outcome: memory.SourceGone, Memories: 2, Proposed: true}) || got[memory.SourceKept].Memories != 1 {
		t.Fatalf("events = %+v", got)
	}
	// 다음 회고는 같은 변경을 다시 제안하지 않는다
	e.r.Now = func() time.Time { return time.Date(2026, 10, 28, 4, 0, 0, 0, kst) }
	e.r.Run(context.Background(), KindDaily, "2026-10-27")
	if again, _ := e.s.Proposals(memory.StatusPending); len(again) != 1 {
		t.Fatalf("pending after next run = %+v", again)
	}
}

func TestSources_파일이_없어지면_제안하지_않고_다시_생기면_다시_본다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "evidence.json")
	w(t, f, `{"ok":true}`)
	m := fileMemory(t, e, "증거", f, `{"ok":true}`)
	os.Remove(f)

	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if pending, _ := e.s.Proposals(memory.StatusPending); len(pending) != 0 {
		t.Fatalf("a deleted evidence file is no proposal: %+v", pending)
	}
	if got := sourceHash(t, e, m.ID); got != "absent" {
		t.Fatalf("hash = %s", got)
	}
	if evs := sourceEvents(t, e); len(evs) != 1 || evs[0] != (checkEvent{Outcome: memory.SourceAbsent, Memories: 1}) {
		t.Fatalf("events = %+v", evs)
	}

	w(t, f, `{"ok":false}`)
	e.r.Now = func() time.Time { return time.Date(2026, 10, 28, 4, 0, 0, 0, kst) }
	e.r.Run(context.Background(), KindDaily, "2026-10-27")
	if pending, _ := e.s.Proposals(memory.StatusPending); len(pending) != 1 || !strings.Contains(pending[0].Body, `{"ok":true}`) {
		t.Fatalf("a file that came back changed is checked again: %+v", pending)
	}
}

func TestSources_발췌가_없으면_회고_모델이_파일을_읽고_판정한다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	dir := t.TempDir()
	f := filepath.Join(dir, "config.md")
	other := filepath.Join(dir, "other.md")
	w(t, f, "포트는 8080")
	w(t, other, "로그는 7일")
	ok := fileMemory(t, e, "로그 경로", f, "")
	fix := fileMemory(t, e, "포트 번호", f, "")
	old1 := fileMemory(t, e, "옛 설정 가", f, "")
	old2 := fileMemory(t, e, "옛 설정 나", f, "")
	unread := fileMemory(t, e, "로그 보관", other, "")
	const marker = "포트는 9090 비밀표식-7f3a"
	w(t, f, marker)
	w(t, other, "로그는 30일")

	var page string
	var readErr error
	e.ai = func(id string) error {
		page, _, _ = ReadPage(e.s, e.r.Paths, id, 1)
		var text string
		text, readErr = ReadTarget(e.r.Paths, id, f, 1)
		if !strings.Contains(text, marker) {
			t.Errorf("read = %q", text)
		}
		report, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "근거 파일 판정",
			Sources: []SourceVerdictIn{
				{Path: f, MemoryID: ok.ID, Verdict: "ok", Reason: "로그 경로는 그대로"},
				{Path: f, MemoryID: fix.ID, Verdict: "edit", Reason: "포트가 바뀜", NewBody: "포트는 9090"},
				{Path: f, MemoryID: old1.ID, Verdict: "stale", Reason: "설정이 없어짐"},
				{Path: f, MemoryID: old2.ID, Verdict: "stale", Reason: "설정이 없어짐"},
				{Path: other, MemoryID: unread.ID, Verdict: "ok"},
			}})
		if err == nil && !strings.Contains(report, "읽지 않고") {
			t.Errorf("report = %s", report)
		}
		return err
	}
	res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if err != nil || res.Status != StatusDone {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if readErr != nil {
		t.Fatalf("the model must be able to read a judged file: %v", readErr)
	}
	for _, want := range []string{f, ok.ID, "포트 번호", other} {
		if !strings.Contains(page, want) {
			t.Fatalf("material must list %q: %s", want, page)
		}
	}
	if strings.Contains(page, "9090") {
		t.Fatalf("material must not copy the file: %s", page)
	}

	pending, _ := e.s.Proposals(memory.StatusPending)
	kinds := map[string][]memory.Proposal{}
	for _, p := range pending {
		kinds[p.Kind] = append(kinds[p.Kind], p)
	}
	if len(pending) != 2 || len(kinds[memory.ProposalEdit]) != 1 || len(kinds[memory.ProposalStale]) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	if e := kinds[memory.ProposalEdit][0]; e.MemoryIDs[0] != fix.ID || e.Payload.Body != "포트는 9090" {
		t.Fatalf("edit = %+v", e)
	}
	if s := kinds[memory.ProposalStale][0]; strings.Join(s.MemoryIDs, ",") != strings.Join(sorted(old1.ID, old2.ID), ",") {
		t.Fatalf("stale = %+v", s)
	}
	for _, id := range []string{ok.ID, fix.ID, old1.ID, old2.ID} {
		if got := sourceHash(t, e, id); got != memory.FileHash(f) {
			t.Fatalf("%s hash not updated", id)
		}
	}
	if got := sourceHash(t, e, unread.ID); got == memory.FileHash(other) {
		t.Fatal("a verdict without reading the file must not count")
	}
	counts := map[string]int{}
	for _, ev := range sourceEvents(t, e) {
		counts[ev.Outcome] += ev.Memories
	}
	if counts[memory.SourceOK] != 1 || counts[memory.SourceEdit] != 1 || counts[memory.SourceStale] != 2 {
		t.Fatalf("events = %v", counts)
	}
	// 파일 내용 사본은 기억 DB에 남지 않는다(회사 PC 보호)
	e.s.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, name := range []string{e.r.Paths.DB(), e.r.Paths.DB() + "-wal"} {
		if b, err := os.ReadFile(name); err == nil && bytes.Contains(b, []byte("비밀표식-7f3a")) {
			t.Fatalf("file content copied into %s", name)
		}
	}
	var runs []map[string]any
	evs, _ := e.s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: memory.EventRetroRun})
	for _, ev := range evs {
		var d map[string]any
		json.Unmarshal(ev.Data, &d)
		runs = append(runs, d)
	}
	if len(runs) != 1 || runs[0]["sourceChecks"] != float64(2) {
		t.Fatalf("retro.run must count the judged files: %+v", runs)
	}
}

func TestSources_판정_대상이_아닌_파일과_기억은_받지_않는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "a.md")
	w(t, f, "v1")
	m := fileMemory(t, e, "가", f, "")
	stranger := fileMemory(t, e, "나", filepath.Join(t.TempDir(), "never.md"), "")
	w(t, f, "v2")
	e.ai = func(id string) error {
		if _, err := ReadTarget(e.r.Paths, id, "/etc/hosts", 1); err == nil {
			t.Error("a file outside the material must not be readable")
		}
		ReadTarget(e.r.Paths, id, f, 1)
		report, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "x", Sources: []SourceVerdictIn{
			{Path: f, MemoryID: stranger.ID, Verdict: "stale"},
			{Path: f, MemoryID: m.ID, Verdict: "maybe"},
		}})
		if err == nil && (!strings.Contains(report, stranger.ID) || !strings.Contains(report, "maybe")) {
			t.Errorf("report = %s", report)
		}
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil || res.Status != StatusDone {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if pending, _ := e.s.Proposals(memory.StatusPending); len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
}

func TestSources_옛_출처_파일이_바뀜_제안은_새_판정으로_대체한다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	dir := t.TempDir()
	keptFile, goneFile, deleted := filepath.Join(dir, "kept.md"), filepath.Join(dir, "gone.md"), filepath.Join(dir, "deleted.json")
	w(t, keptFile, "가 규칙")
	w(t, goneFile, "나 규칙")
	w(t, deleted, "증거")
	kept := fileMemory(t, e, "가", keptFile, "가 규칙")
	gone := fileMemory(t, e, "나", goneFile, "나 규칙")
	absent := fileMemory(t, e, "다", deleted, "증거")
	w(t, keptFile, "머리말\n가 규칙")
	w(t, goneFile, "다른 규칙")
	os.Remove(deleted)
	var legacy []string
	for _, m := range []memory.Memory{kept, gone, absent} {
		p, err := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: "출처 파일이 바뀜: " + m.Title,
			Body: "근거 파일이 이 기억을 만든 뒤 바뀌었습니다.", MemoryIDs: []string{m.ID}})
		if err != nil {
			t.Fatal(err)
		}
		legacy = append(legacy, p.ID)
	}
	unused, _ := e.s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "오래 쓰지 않음", Scope: memory.ScopeGlobal, Source: memory.Source{Kind: memory.SourceSession}})
	other, err := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: "오래 쓰지 않은 기억", MemoryIDs: []string{unused.ID}})
	if err != nil {
		t.Fatal(err)
	}

	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	for _, id := range legacy {
		if p, _ := e.s.GetProposal(id); p.Status != memory.StatusSettled || !strings.Contains(p.Note, "새 출처 판정") {
			t.Fatalf("legacy %s = %+v", id, p)
		}
	}
	pending, _ := e.s.Proposals(memory.StatusPending)
	if len(pending) != 2 {
		t.Fatalf("pending = %+v", pending)
	}
	byID := map[string]memory.Proposal{}
	for _, p := range pending {
		byID[p.ID] = p
	}
	if _, ok := byID[other.ID]; !ok {
		t.Fatalf("other stale proposals stay: %+v", pending)
	}
	for _, p := range pending {
		if p.ID != other.ID && (p.MemoryIDs[0] != gone.ID || !strings.Contains(p.Body, "나 규칙")) {
			t.Fatalf("the vanished excerpt comes back as a new proposal: %+v", p)
		}
	}
}

func TestSources_거절한_옛_제안이_새_판정을_막지_않는다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "rules.md")
	w(t, f, "가 규칙")
	m := fileMemory(t, e, "가", f, "가 규칙")
	old, _ := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: "출처 파일이 바뀜: 가", MemoryIDs: []string{m.ID}})
	e.s.Decide(memory.DecideInput{ID: old.ID, Decision: "reject", Note: "아직 맞음"})
	w(t, f, "다른 규칙")
	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if pending, _ := e.s.Proposals(memory.StatusPending); len(pending) != 1 || pending[0].Payload.Path != f {
		t.Fatalf("a vanished excerpt must come back despite an old rejection: %+v", pending)
	}
}

func TestSources_대화가_있는_날은_판정만_내도_일지가_필요하다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "a.md")
	w(t, f, "v1")
	m := fileMemory(t, e, "가", f, "")
	w(t, f, "v2")
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "안녕"))
	var first error
	e.ai = func(id string) error {
		ReadTarget(e.r.Paths, id, f, 1)
		_, first = Submit(e.s, e.r.Paths, id, Submission{Summary: "x", Sources: []SourceVerdictIn{{Path: f, MemoryID: m.ID, Verdict: "ok"}}})
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "x", Journal: &JournalIn{Title: "인사", Body: "-"}, Sources: []SourceVerdictIn{{Path: f, MemoryID: m.ID, Verdict: "ok"}}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil || res.Status != StatusDone {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if first == nil || !strings.Contains(first.Error(), "journal") {
		t.Fatalf("first = %v", first)
	}
}

func TestSources_판정_파일은_비밀값을_가리고_쪽으로_나눠_다_읽어야_판정이_선다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "big.md")
	w(t, f, "v1")
	m := fileMemory(t, e, "가", f, "")
	big := "password: hunter2hunter2\n" + strings.Repeat("설정 줄입니다.\n", 6000) + "끝 줄\n"
	w(t, f, big)
	var pages []string
	var report string
	e.ai = func(id string) error {
		p1, err := ReadTarget(e.r.Paths, id, f, 1)
		if err != nil {
			return err
		}
		pages = append(pages, p1)
		// 첫 쪽만 읽고 낸 판정은 서지 않는다
		report, _ = Submit(e.s, e.r.Paths, id, Submission{Summary: "x", Sources: []SourceVerdictIn{{Path: f, MemoryID: m.ID, Verdict: "ok"}}})
		return nil
	}
	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if strings.Contains(pages[0], "hunter2") || !strings.Contains(pages[0], "(1/") {
		t.Fatalf("page 1 = %.200s", pages[0])
	}
	if !strings.Contains(report, "다 읽지 않고") || sourceHash(t, e, m.ID) == memory.FileHash(f) {
		t.Fatalf("a verdict on part of the file must not count: %s", report)
	}

	e.r.Now = func() time.Time { return time.Date(2026, 10, 28, 4, 0, 0, 0, kst) }
	e.ai = func(id string) error {
		for n := 1; ; n++ {
			text, err := ReadTarget(e.r.Paths, id, f, n)
			if err != nil {
				return err
			}
			if strings.Contains(text, "끝 줄") {
				break
			}
		}
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "x", Sources: []SourceVerdictIn{{Path: f, MemoryID: m.ID, Verdict: "ok"}}})
		return err
	}
	e.r.Run(context.Background(), KindDaily, "2026-10-27")
	if sourceHash(t, e, m.ID) != memory.FileHash(f) {
		t.Fatal("a verdict after reading every page counts")
	}
}

func TestSources_고칠_것이_없는_edit_판정은_맞음으로_보고_비밀값을_가린다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	f := filepath.Join(t.TempDir(), "a.md")
	w(t, f, "v1")
	same := fileMemory(t, e, "가", f, "")
	fix := fileMemory(t, e, "나", f, "")
	w(t, f, "v2")
	e.ai = func(id string) error {
		ReadTarget(e.r.Paths, id, f, 1)
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "x", Sources: []SourceVerdictIn{
			{Path: f, MemoryID: same.ID, Verdict: "edit"},
			{Path: f, MemoryID: fix.ID, Verdict: "edit", NewBody: "토큰은 sk-abcdefghijklmnopqrstuvwxyz0123 이다"},
		}})
		return err
	}
	e.r.Run(context.Background(), KindDaily, "2026-10-26")
	if sourceHash(t, e, same.ID) != memory.FileHash(f) {
		t.Fatal("an edit with nothing to change must settle like ok")
	}
	pending, _ := e.s.Proposals(memory.StatusPending)
	if len(pending) != 1 || strings.Contains(pending[0].Payload.Body, "sk-abc") {
		t.Fatalf("pending = %+v", pending)
	}
	counts := map[string]int{}
	for _, ev := range sourceEvents(t, e) {
		counts[ev.Outcome] += ev.Memories
	}
	if counts[memory.SourceOK] != 1 || counts[memory.SourceEdit] != 1 {
		t.Fatalf("events = %v", counts)
	}
}

func TestSources_한_번에_판정할_기억_수에_상한을_두고_나머지는_다음_회고가_본다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 27, 4, 0, 0, 0, kst))
	dir := t.TempDir()
	for i := 0; i < maxSourceJudgments+3; i++ {
		f := filepath.Join(dir, fmt.Sprintf("f%02d.md", i))
		w(t, f, "v1")
		fileMemory(t, e, fmt.Sprintf("기억 %d", i), f, "")
		w(t, f, "v2")
	}
	checks := e.r.checkSources()
	n := 0
	for _, c := range checks {
		n += len(c.Memories)
	}
	if n != maxSourceJudgments {
		t.Fatalf("judged = %d", n)
	}
}

func sorted(ids ...string) []string {
	out := append([]string(nil), ids...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
