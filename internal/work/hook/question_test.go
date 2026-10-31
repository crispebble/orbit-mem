package hook

import (
	"testing"

	"github.com/onidot-labs/openorbit/internal/work"
)

// 모양은 이 PC 대화 기록의 AskUserQuestion tool_use 입력과 toolUseResult(Claude Code 2.1.303)를 따른다.
const askInput = `{"questions":[{"question":"색을 고르세요","header":"색","multiSelect":false,"options":[{"label":"빨강","description":"따뜻한 색"},{"label":"파랑"}]},
	{"question":"켤 기능을 고르세요","header":"기능","multiSelect":true,"options":[{"label":"A"},{"label":"B"},{"label":"C"}]}]}`

func askHook(event, response string) []byte {
	b := `{"hook_event_name":"` + event + `","session_id":"S1","cwd":"/repo","prompt_id":"P1","tool_name":"AskUserQuestion","tool_use_id":"toolu_1","tool_input":` + askInput
	if response != "" {
		b += `,"tool_response":` + response
	}
	return []byte(b + `}`)
}

func TestQuestionOpensDecision(t *testing.T) {
	p := Translate(askHook("PreToolUse", ""), env("claude", nil))
	if !has(p, work.KindWaitOpen) {
		t.Fatalf("기다림도 연다: %v", kinds(p))
	}
	d := payload[work.DecisionInput](t, p, work.KindDecisionOpen)
	if d.ID == "" || d.Source != "question" || d.Owner != "human" || d.Prompt != "P1" || len(d.Questions) != 2 {
		t.Fatalf("카드 = %+v", d)
	}
	q0, q1 := d.Questions[0], d.Questions[1]
	if q0.Text != "색을 고르세요" || q0.Header != "색" || q0.MultiSelect || q0.Options[0].ID != "1" || q0.Options[0].Detail != "따뜻한 색" || !q1.MultiSelect || len(q1.Options) != 3 {
		t.Fatalf("질문 = %+v", d.Questions)
	}
	w := payload[work.Wait](t, p, work.KindWaitOpen)
	if d.Work == nil || d.Work.SessionID != "S1" || d.Work.Host != "claude" || d.Work.WaitKey != w.Key {
		t.Fatalf("작업 묶음 = %+v", d.Work)
	}
	if d.Questions[0].Recommended != "" {
		t.Fatal("질문 도구 입력에는 추천이 없다")
	}
}

func TestQuestionAnswerClosesDecision(t *testing.T) {
	open := payload[work.DecisionInput](t, Translate(askHook("PreToolUse", ""), env("claude", nil)), work.KindDecisionOpen)
	resp := `{"questions":[],"answers":{"색을 고르세요":"빨강","켤 기능을 고르세요":["A","C","직접 쓴 것"]},"annotations":{}}`
	p := Translate(askHook("PostToolUse", resp), env("claude", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli"}))
	a := payload[work.DecisionAnswerEvent](t, p, work.KindDecisionAnswer)
	if a.ID != open.ID || a.By != "terminal" || len(a.Answers) != 2 {
		t.Fatalf("답 = %+v", a)
	}
	if got := a.Answers[0]; len(got.OptionIDs) != 1 || got.OptionIDs[0] != "1" || got.Text != "" {
		t.Errorf("단일 선택 = %+v", got)
	}
	if got := a.Answers[1]; len(got.OptionIDs) != 2 || got.OptionIDs[1] != "3" || got.Text != "직접 쓴 것" {
		t.Errorf("다중 선택·직접 쓴 글 = %+v", got)
	}
	// 옛 판: 다중 선택을 ", "로 이은 글, 단일 선택의 직접 쓴 답
	old := `{"answers":{"색을 고르세요":"초록으로 해 주세요","켤 기능을 고르세요":"A, B"}}`
	a = payload[work.DecisionAnswerEvent](t, Translate(askHook("PostToolUse", old), env("claude", map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop"})), work.KindDecisionAnswer)
	if a.By != "host" || a.Answers[0].Text != "초록으로 해 주세요" || len(a.Answers[1].OptionIDs) != 2 {
		t.Errorf("옛 판 답 = %+v", a)
	}
	// 거절·중단은 거둔다
	w := payload[work.DecisionWithdrawEvent](t, Translate(askHook("PostToolUseFailure", ""), env("claude", nil)), work.KindDecisionWithdraw)
	if w.ID != open.ID || w.Reason != "declined" {
		t.Errorf("거둠 = %+v", w)
	}
}

func TestCodexQuestion(t *testing.T) {
	in := `{"hook_event_name":"PreToolUse","session_id":"C1","turn_id":"T1","cwd":"/repo","tool_name":"request_user_input","tool_use_id":"call_9",
		"tool_input":{"questions":[{"id":"mode","header":"방식","question":"어느 방식?","options":[{"label":"빠르게","description":"d"},{"label":"꼼꼼히"}]}]}}`
	d := payload[work.DecisionInput](t, Translate([]byte(in), env("codex", nil)), work.KindDecisionOpen)
	if len(d.Questions) != 1 || d.Questions[0].Text != "어느 방식?" || d.Work.Host != "codex" || d.Prompt != "T1" {
		t.Fatalf("Codex 카드 = %+v", d)
	}
	post := `{"hook_event_name":"PostToolUse","session_id":"C1","turn_id":"T1","cwd":"/repo","tool_name":"request_user_input","tool_use_id":"call_9",
		"tool_input":{"questions":[{"id":"mode","header":"방식","question":"어느 방식?","options":[{"label":"빠르게"},{"label":"꼼꼼히"}]}]},
		"tool_response":{"answers":{"mode":{"answers":["꼼꼼히"]}}}}`
	a := payload[work.DecisionAnswerEvent](t, Translate([]byte(post), env("codex", nil)), work.KindDecisionAnswer)
	if a.ID != d.ID || len(a.Answers) != 1 || len(a.Answers[0].OptionIDs) != 1 || a.Answers[0].OptionIDs[0] != "2" {
		t.Fatalf("Codex 답 = %+v", a)
	}
}

// orbit-pi가 띄운 세션에서 orbit-pi가 직접 부른 훅: 카드는 orbit-pi가 decision_open으로 연다.
func TestQuestionNoDecisions(t *testing.T) {
	e := env("claude", nil)
	e.NoDecisions = true
	p := Translate(askHook("PreToolUse", ""), e)
	if has(p, work.KindDecisionOpen) || !has(p, work.KindWaitOpen) {
		t.Fatalf("카드 없이 기다림만: %v", kinds(p))
	}
	if has(Translate(askHook("PostToolUse", `{"answers":{}}`), e), work.KindDecisionAnswer) {
		t.Fatal("카드 닫기도 없다")
	}
}

func TestLauncherOnSessionStart(t *testing.T) {
	e := env("claude", nil)
	e.Launcher = "studio"
	p := Translate([]byte(`{"hook_event_name":"SessionStart","session_id":"S1","cwd":"/repo","source":"startup"}`), e)
	if s := payload[work.SessionStart](t, p, work.KindSessionStart); s.Launcher != "studio" {
		t.Fatalf("launcher = %q", s.Launcher)
	}
}
