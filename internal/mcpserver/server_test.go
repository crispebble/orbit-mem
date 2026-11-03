package mcpserver

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

func connect(t *testing.T) (*mcp.ClientSession, *memory.Store) {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New(s, Options{Project: "project:github.com/me/app", ProjectLabel: "github.com/me/app", Version: "test"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, s
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s decode: %v (%s)", name, err, b)
		}
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestTools_기억_여섯개와_활동_기록(t *testing.T) {
	cs, _ := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := "activity_find,memory_find,memory_get,memory_save,memory_update,proposal_decide,proposals"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v", names)
	}
}

func TestSave_찾기_읽기_고치기(t *testing.T) {
	cs, _ := connect(t)
	var saved MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "커밋은 경로를 명시한다", "body": "git add -A 금지",
		"scope": "project", "source": map[string]any{"kind": "quote", "excerpt": "커밋은 경로 명시해"}}, &saved)
	if saved.Memory.State != "confirmed" || saved.Memory.Scope != "project:github.com/me/app" {
		t.Fatalf("saved = %+v", saved.Memory)
	}
	var cand MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "lesson", "title": "시험은 단독으로 돌린다",
		"source": map[string]any{"kind": "session", "ref": "claude:abc"}}, &cand)
	if cand.Memory.State != "candidate" || cand.Memory.Scope != "global" {
		t.Fatalf("cand = %+v", cand.Memory)
	}

	var found FindOut
	call(t, cs, "memory_find", map[string]any{"query": "커밋"}, &found)
	if len(found.Items) != 1 || found.Items[0].ID != saved.Memory.ID {
		t.Fatalf("found = %+v", found)
	}

	var got GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{saved.Memory.ID, cand.Memory.ID}}, &got)
	if len(got.Items) != 2 || len(got.Items[0].Sources) != 1 {
		t.Fatalf("get = %+v", got)
	}

	var upd MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": saved.Memory.ID, "expectedVersion": 1, "body": "git add -A·git commit -a 금지", "reason": "보강",
		"userQuote": "git commit -a도 금지라고 더해"}, &upd)
	if upd.Memory.Version != 2 {
		t.Fatalf("upd = %+v", upd.Memory)
	}
	res := call(t, cs, "memory_update", map[string]any{"id": saved.Memory.ID, "expectedVersion": 1, "body": "x", "userQuote": "x로 바꿔"}, nil)
	if !res.IsError || !strings.Contains(text(res), "먼저 고쳤습니다") {
		t.Fatalf("conflict not reported: %s", text(res))
	}
	var stale MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": cand.Memory.ID, "action": "stale", "reason": "더 이상 맞지 않음"}, &stale)
	if stale.Memory.State != "stale" {
		t.Fatalf("stale = %+v", stale.Memory)
	}
}

func TestSave_잘못된_입력은_도구_오류로_알린다(t *testing.T) {
	cs, _ := connect(t)
	res := call(t, cs, "memory_save", map[string]any{"kind": "todo", "title": "x", "source": map[string]any{"kind": "quote"}}, nil)
	if !res.IsError {
		t.Fatal("bad kind accepted")
	}
}

func TestProposals_보기와_결정(t *testing.T) {
	cs, s := connect(t)
	m, _ := s.Save(memory.SaveInput{Kind: "fact", Title: "후보", Scope: "global", Source: memory.Source{Kind: "session"}})
	p, _ := s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "후보 확정", MemoryIDs: []string{m.ID}})
	var list ProposalsOut
	call(t, cs, "proposals", map[string]any{}, &list)
	if len(list.Items) != 1 || list.Items[0].ID != p.ID || len(list.Items[0].Memories) != 1 {
		t.Fatalf("list = %+v", list)
	}
	var dec DecideOut
	call(t, cs, "proposal_decide", map[string]any{"id": p.ID, "decision": "approve", "title": "고친 후보"}, &dec)
	if dec.Proposal.Status != "approved" {
		t.Fatalf("dec = %+v", dec)
	}
	got, _ := s.Get(m.ID)
	if got.State != "confirmed" || got.Title != "고친 후보" {
		t.Fatalf("memory = %+v", got)
	}
}

func TestProposalDecide_ids로_여럿을_결정하고_실패한_것은_이유와_돌려준다(t *testing.T) {
	cs, s := connect(t)
	var ids []string
	for _, title := range []string{"가", "나", "다"} {
		m, _ := s.Save(memory.SaveInput{Kind: "fact", Title: title, Scope: "global", Source: memory.Source{Kind: "session"}})
		p, _ := s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: title + " 확정", MemoryIDs: []string{m.ID}})
		ids = append(ids, p.ID)
	}
	s.Decide(memory.DecideInput{ID: ids[1], Decision: "reject"})

	var dec DecideOut
	res := call(t, cs, "proposal_decide", map[string]any{"ids": append(ids, "p-없음"), "decision": "approve", "note": "묶음"}, &dec)
	if res.IsError {
		t.Fatalf("batch must not be a tool error: %s", text(res))
	}
	if len(dec.Decided) != 2 || len(dec.Failed) != 2 || dec.Proposal.ID != "" {
		t.Fatalf("dec = %+v", dec)
	}
	if !strings.Contains(text(res), "2건 결정") || !strings.Contains(text(res), "2건 실패") {
		t.Fatalf("text = %s", text(res))
	}
	pending, _ := s.Proposals(memory.StatusPending)
	if len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}

	res = call(t, cs, "proposal_decide", map[string]any{"ids": []string{ids[0]}, "decision": "approve", "title": "고침"}, nil)
	if !res.IsError || !strings.Contains(text(res), "하나씩") {
		t.Fatalf("editing in a batch must fail: %s", text(res))
	}
	res = call(t, cs, "proposal_decide", map[string]any{"decision": "approve"}, nil)
	if !res.IsError {
		t.Fatalf("no id must fail: %s", text(res))
	}
	res = call(t, cs, "proposal_decide", map[string]any{"ids": []string{" ", ""}, "decision": "approve"}, nil)
	if !res.IsError {
		t.Fatalf("blank ids must fail: %s", text(res))
	}
	res = call(t, cs, "proposal_decide", map[string]any{"ids": []string{ids[2]}, "decision": "maybe"}, nil)
	if !res.IsError || !strings.Contains(text(res), "approve 또는 reject") {
		t.Fatalf("a bad decision must fail as a whole: %s", text(res))
	}
}

func toolEvents(t *testing.T, s *memory.Store) map[string][]map[string]any {
	t.Helper()
	evs, err := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: "tool."})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]map[string]any{}
	for _, e := range evs {
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		out[e.Kind] = append(out[e.Kind], d)
	}
	return out
}

func TestTools_호출을_사건으로_남긴다(t *testing.T) {
	cs, s := connect(t)
	call(t, cs, "memory_find", map[string]any{"query": "없는 말 token=sk-abcdefghijklmnopqrstuvwx"}, nil)
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "답은 존댓말", "source": map[string]any{"kind": "quote", "excerpt": "존댓말로 답해"}}, nil)
	call(t, cs, "memory_find", map[string]any{"query": "존댓말"}, nil)
	call(t, cs, "memory_get", map[string]any{"ids": []string{"m-없음"}}, nil)

	ev := toolEvents(t, s)
	finds := ev["tool.memory_find"]
	if len(finds) != 2 {
		t.Fatalf("finds = %+v", ev)
	}
	if finds[0]["results"].(float64) != 0 || finds[0]["ok"] != true || strings.Contains(finds[0]["query"].(string), "sk-abcdefghijklmnop") {
		t.Errorf("empty find = %+v", finds[0])
	}
	if finds[1]["results"].(float64) != 1 || len(finds[1]["ids"].([]any)) != 1 {
		t.Errorf("hit find = %+v", finds[1])
	}
	saves := ev["tool.memory_save"]
	if len(saves) != 1 || saves[0]["sourceKind"] != "quote" || saves[0]["state"] != "confirmed" {
		t.Errorf("saves = %+v", saves)
	}
	if gets := ev["tool.memory_get"]; len(gets) != 1 || gets[0]["ok"] != false {
		t.Errorf("failed get must be recorded as not ok: %+v", gets)
	}
	id := finds[1]["ids"].([]any)[0].(string)
	call(t, cs, "memory_get", map[string]any{"ids": []string{id}}, nil)
	if gets := toolEvents(t, s)["tool.memory_get"]; len(gets) != 2 || len(gets[1]["ids"].([]any)) != 1 || gets[1]["ids"].([]any)[0] != id {
		t.Errorf("get must record the ids it returned: %+v", gets)
	}
}

// hints is what a tool's MCP annotations say about it.
type hints struct{ read, destructive, idempotent bool }

// checkHints checks the annotations of each tool in want. Every Orbit tool
// stays on this PC, so none is open-world.
func checkHints(t *testing.T, cs *mcp.ClientSession, want map[string]hints) {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		w, ok := want[tl.Name]
		if !ok {
			continue
		}
		delete(want, tl.Name)
		a := tl.Annotations
		if a == nil || a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: 바깥 세계 도구가 아님을 밝혀야 합니다: %+v", tl.Name, a)
			continue
		}
		if a.ReadOnlyHint != w.read {
			t.Errorf("%s: 읽기 전용 %v, 기대 %v", tl.Name, a.ReadOnlyHint, w.read)
		}
		if w.read {
			continue
		}
		if a.DestructiveHint == nil || *a.DestructiveHint != w.destructive {
			t.Errorf("%s: 파괴적 %v, 기대 %v", tl.Name, a.DestructiveHint, w.destructive)
		}
		if a.IdempotentHint != w.idempotent {
			t.Errorf("%s: 멱등 %v, 기대 %v", tl.Name, a.IdempotentHint, w.idempotent)
		}
	}
	for name := range want {
		t.Errorf("%s: 도구 목록에 없습니다", name)
	}
}

// 기억·제안 도구는 MCP 주석으로 성질을 밝힌다. 주석이 없으면 Codex는 파괴적·바깥 세계 도구로 보고 승인을 요구해,
// 승인 정책 never에서 호출이 실패한다(ONID-8). memory_update는 판본을 남기며 고치므로 파괴적이지 않고,
// proposal_decide는 파일 반영·되돌리기로 사용자 파일을 덮거나 지우므로 파괴적이다. 결정된 제안은 다시 결정되지 않는다.
func TestToolAnnotations(t *testing.T) {
	cs, _ := connect(t)
	checkHints(t, cs, map[string]hints{
		"memory_find":     {read: true},
		"memory_get":      {read: true},
		"proposals":       {read: true},
		"memory_save":     {},
		"memory_update":   {},
		"proposal_decide": {destructive: true, idempotent: true},
	})
}

// 설계 · Orbit 3차 O3-2 끝 기준 1: memory_find의 태그 거르기는 표기 차이(대소문자·공백·-/_)를 무시한다.
func TestFind_태그로_거르고_표기_차이는_무시한다(t *testing.T) {
	cs, s := connect(t)
	for _, in := range []memory.SaveInput{
		{Kind: memory.KindRule, Title: "화면은 Studio 키트로", Tags: []string{"Claude_Code", "ui"}},
		{Kind: memory.KindRule, Title: "화면 크기는 토큰으로", Tags: []string{"claude-code"}},
		{Kind: memory.KindRule, Title: "화면 글은 존댓말로", Tags: []string{"orbit"}},
	} {
		in.Scope, in.Source = memory.ScopeGlobal, memory.Source{Kind: memory.SourceSession}
		if _, err := s.Save(in); err != nil {
			t.Fatal(err)
		}
	}
	var found FindOut
	call(t, cs, "memory_find", map[string]any{"query": "화면", "tags": []string{" CLAUDE-CODE "}}, &found)
	if len(found.Items) != 2 {
		t.Fatalf("태그 거르기: %+v", found.Items)
	}
	for _, it := range found.Items {
		if it.Title == "화면 글은 존댓말로" {
			t.Errorf("다른 태그 기억이 나왔습니다: %+v", it)
		}
	}
}

// 저장소 이름이 바뀌면 프로젝트 범위 기억을 새 원격 이름으로 옮길 수 있어야 한다(ONID-107).
func TestUpdate_project에_디렉터리를_주면_그_저장소의_범위로_옮긴다(t *testing.T) {
	cs, _ := connect(t)
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/me/renamed.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	var saved MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "옛 저장소 기억", "scope": "project",
		"source": map[string]any{"kind": "session"}}, &saved)
	var moved MemoryOut
	res := call(t, cs, "memory_update", map[string]any{"id": saved.Memory.ID, "scope": "project", "project": dir, "reason": "저장소 이름 변경"}, &moved)
	if res.IsError {
		t.Fatalf("update: %s", text(res))
	}
	if moved.Memory.Scope != "project:github.com/me/renamed" || moved.Memory.Project != "github.com/me/renamed" {
		t.Fatalf("moved = %+v", moved.Memory)
	}
}

// 저장 거부 목록·상대 시간·보존 등급·주제(기획 · Orbit Code K15).
func TestSave_거부_목록과_상대_시간과_보존_등급(t *testing.T) {
	cs, s := connect(t)
	res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "배포 토큰", "body": "token: ghp_abcdefghijklmnopqrstuvwxyz0123",
		"source": map[string]any{"kind": "session"}}, nil)
	if !res.IsError || !strings.Contains(text(res), "비밀값") {
		t.Fatalf("secret saved: %s", text(res))
	}
	res = call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "S4 진행률 3/7 완료", "source": map[string]any{"kind": "session"}}, nil)
	if !res.IsError || !strings.Contains(text(res), "진행 상태") {
		t.Fatalf("progress saved: %s", text(res))
	}
	var out MemoryOut
	res = call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "계약 만료는 3주 뒤", "retention": "durable", "reviewAfterDays": 30,
		"topic": "계약 만료", "source": map[string]any{"kind": "session"}}, &out)
	if res.IsError || strings.Contains(out.Memory.Title, "3주 뒤") || len(out.Rewrites) != 1 || out.Memory.Retention != "durable" || out.Memory.Topic != "계약 만료" || out.Memory.ReviewAt == 0 {
		t.Fatalf("save = %s %+v", text(res), out)
	}
	res = call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "이번 주만", "retention": "volatile", "ttlDays": 9, "source": map[string]any{"kind": "session"}}, nil)
	if !res.IsError {
		t.Fatal("ttl 9 saved")
	}
	// 재검토함: 내용은 그대로 두고 재검토일만 민다.
	before, _ := s.Get(out.Memory.ID)
	var upd MemoryOut
	res = call(t, cs, "memory_update", map[string]any{"id": out.Memory.ID, "action": "reviewed"}, &upd)
	if res.IsError || upd.Memory.Version != before.Version+1 || upd.Memory.Title != before.Title {
		t.Fatalf("reviewed = %s %+v", text(res), upd)
	}
}
