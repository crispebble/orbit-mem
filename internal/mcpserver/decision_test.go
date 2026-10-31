package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dcontract "github.com/onidot-labs/openorbit/internal/decision/contract"
	"github.com/onidot-labs/openorbit/internal/work"
)

// decisionCall calls a decision tool, checks the response against the
// orbit.decision 1판 Schema and, with ORBIT_WRITE_EXAMPLES=1, writes it as a
// contract example.
func decisionCall(t *testing.T, cs *mcp.ClientSession, example, name string, args map[string]any) map[string]any {
	t.Helper()
	res := callRaw(t, cs, name, args)
	def := name
	if res.IsError {
		def = "error"
	}
	b, _ := json.Marshal(res.StructuredContent)
	v, err := dcontract.ResponseValidator(1, def)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(b); err != nil {
		t.Errorf("%s 응답이 orbit.decision 1판 Schema에 맞지 않습니다: %v\n%s", name, err, b)
	}
	if example != "" && os.Getenv("ORBIT_WRITE_EXAMPLES") == "1" {
		ex := map[string]any{"tool": name, "args": args, "result": json.RawMessage(b)}
		if res.IsError {
			ex["isError"] = true
		}
		out, _ := json.MarshalIndent(ex, "", "  ")
		_ = os.WriteFile(filepath.Join("..", "decision", "contract", "v1", "examples", example+".json"), append(out, '\n'), 0o644)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// 끝 기준(설계 · Studio 4차 「결정 카드 계약」): 열기·목록·변경 기다리기·답(비교 후 교체)·거두기와
// 사람이 직접 연 세션 카드의 CANNOT_RELAY가 계약대로 돈다.
func TestDecisionTools(t *testing.T) {
	e := newWorkEnv(t)
	seedWork(t, e)
	cs := readerHost(t, e)

	first := decisionCall(t, cs, "decision_changes.reset", "decision_changes", map[string]any{})
	if first["reset"] != true {
		t.Fatalf("after 없으면 reset: %v", first)
	}
	opened := decisionCall(t, cs, "decision_open.ops", "decision_open", map[string]any{
		"source": "ops", "owner": "orbit-pi",
		"questions": []any{map[string]any{"text": "atlas#12 배포 실패를 어떻게 할까요?", "header": "운영 감시", "options": []any{
			map[string]any{"id": "proceed", "label": "진행", "detail": "분석한 수정으로 PR을 엽니다"},
			map[string]any{"id": "session", "label": "세션으로 가져오기"},
			map[string]any{"id": "record", "label": "기록만"}}, "recommended": "proceed", "reason": "원인이 마이그레이션 순서로 확인됐습니다"}},
		"context": "lynx가 main 배포 실패를 알렸습니다.", "links": []any{map[string]any{"kind": "pr", "ref": "https://github.com/onidot-labs/atlas/pull/12", "label": "atlas#12"}},
		"binding": map[string]any{"kind": "pr-head", "ref": "onidot-labs/atlas#12@3b9e0c1"}, "expiresAt": "2099-01-01T00:00:00.000Z"})
	card, _ := opened["decision"].(map[string]any)
	id, _ := card["id"].(string)
	if id == "" || card["state"] != "open" {
		t.Fatalf("열기 = %v", opened)
	}
	changed := decisionCall(t, cs, "decision_changes.changes", "decision_changes", map[string]any{"after": first["seq"], "wait": 0})
	if ids, _ := changed["decisionIds"].([]any); len(ids) != 1 || ids[0] != id {
		t.Fatalf("변경 = %v", changed)
	}
	if l := decisionCall(t, cs, "decision_list.open", "decision_list", map[string]any{}); l["total"] != float64(1) {
		t.Fatalf("목록 = %v", l)
	}
	ans := decisionCall(t, cs, "decision_answer.ok", "decision_answer", map[string]any{"id": id, "answers": []any{map[string]any{"optionIds": []any{"proceed"}}}, "by": "studio"})
	if d, _ := ans["decision"].(map[string]any); d["state"] != "answered" {
		t.Fatalf("답 = %v", ans)
	}
	late := decisionCall(t, cs, "error.conflict", "decision_answer", map[string]any{"id": id, "answers": []any{map[string]any{"optionIds": []any{"record"}}}, "by": "telegram"})
	if errCode(late) != "CONFLICT" || !strings.Contains(late["error"].(map[string]any)["message"].(string), "studio") {
		t.Fatalf("늦은 답 = %v", late)
	}
	if got := decisionCall(t, cs, "decision_get", "decision_get", map[string]any{"id": id}); got["decision"].(map[string]any)["answer"].(map[string]any)["by"] != "studio" {
		t.Fatalf("읽기 = %v", got)
	}
	// 받은 카드를 묶인 대상이 바뀌어 거둔다
	p2 := decisionCall(t, cs, "", "decision_open", map[string]any{"source": "plugin", "owner": "studio", "questions": []any{map[string]any{"text": "켤까요?", "options": []any{map[string]any{"label": "예"}, map[string]any{"label": "아니요"}}}}})
	w := decisionCall(t, cs, "decision_withdraw", "decision_withdraw", map[string]any{"id": p2["decision"].(map[string]any)["id"], "reason": "binding_changed"})
	if d := w["decision"].(map[string]any); d["state"] != "withdrawn" || d["closeReason"] != "binding_changed" {
		t.Fatalf("거둠 = %v", w)
	}
	// 오류
	if m := decisionCall(t, cs, "error.not-found", "decision_get", map[string]any{"id": "d-none"}); errCode(m) != "NOT_FOUND" {
		t.Fatalf("없는 카드 = %v", m)
	}
	if m := decisionCall(t, cs, "error.invalid-argument", "decision_open", map[string]any{"source": "ops", "owner": "orbit-pi", "questions": []any{}}); errCode(m) != "INVALID_ARGUMENT" {
		t.Fatalf("빈 질문 = %v", m)
	}
	if m := decisionCall(t, cs, "", "decision_open", map[string]any{"source": "ops", "owner": "nobody", "questions": []any{}}); errCode(m) != "INVALID_ARGUMENT" {
		t.Fatalf("모르는 owner = %v", m)
	}
	if m := decisionCall(t, cs, "error.unsupported-major", "decision_list", map[string]any{"major": 2}); errCode(m) != "UNSUPPORTED_MAJOR" {
		t.Fatalf("판 = %v", m)
	}
}

// 사람이 직접 연 세션에서 훅이 연 카드: Studio는 답을 넘길 수 없다(CANNOT_RELAY).
func TestDecisionHumanCard(t *testing.T) {
	e := newWorkEnv(t)
	seedWork(t, e)
	s, err := work.Open(e.db)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC).UnixMilli()
	_, err = s.Append(work.NewEvent(work.KindDecisionOpen, "claude", "S1", at, work.CleanDecision(work.DecisionInput{ID: "d-3f2a9c1e7b04", Source: "question", Owner: "human",
		Questions: []work.DecisionQuestion{{Text: "어느 쪽으로 할까요?", Header: "방식", Options: []work.DecisionOption{{Label: "가"}, {Label: "나"}}},
			{Text: "무엇을 켤까요?", Header: "기능", MultiSelect: true, Options: []work.DecisionOption{{Label: "A"}, {Label: "B"}}}},
		Work: &work.DecisionWork{Host: "claude", SessionID: "S1", WaitKey: "k-ask"}, Prompt: "P1"})))
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	cs := readerHost(t, e)
	l := decisionCall(t, cs, "decision_list.human-question", "decision_list", map[string]any{"sessionId": "S1"})
	items, _ := l["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("목록 = %v", l)
	}
	d := items[0].(map[string]any)
	if w := d["work"].(map[string]any); w["stepId"] == nil || w["workId"] == nil {
		t.Fatalf("진행 단계에 묶여야 합니다: %v", d)
	}
	m := decisionCall(t, cs, "error.cannot-relay", "decision_answer", map[string]any{"id": "d-3f2a9c1e7b04", "answers": []any{map[string]any{"optionIds": []any{"1"}}, map[string]any{"optionIds": []any{"1", "2"}}}, "by": "studio"})
	if errCode(m) != "CANNOT_RELAY" {
		t.Fatalf("사람 카드 답 = %v", m)
	}
}
