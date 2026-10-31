package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/work"
)

// Question tools as decision cards (orbit.decision, 설계 · Studio 4차 S4-0).
// 실측(Claude Code 2.1.303, 이 PC 대화 기록 40개): AskUserQuestion의 tool_input은
// {questions:[{question, header, multiSelect, options:[{label, description}]}]}이고
// 사후 tool_response는 {questions, answers:{<질문 글>: 답}, annotations}다. 답은 단일
// 선택이면 고른 라벨이나 직접 쓴 글, 다중 선택이면 라벨 배열(직접 쓴 글이 섞일 수 있음).
// 옛 판은 다중 선택을 ", "로 이은 글로 준다. claude -p(비대화형)에는 AskUserQuestion이
// 없다. Codex request_user_input은 질문마다 id를 주고 답을 그 id로 준다.

type questionInput struct {
	Questions []struct {
		ID          string `json:"id"`
		Question    string `json:"question"`
		Header      string `json:"header"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// questionID is the card id of one question tool call: the same for its
// pre- and post-call hooks.
func (t *translator) questionID() string {
	ref := t.in.ToolUse
	if ref == "" {
		ref = toolKey(t.in.Tool, t.in.ToolInput)
	}
	h := sha256.Sum256([]byte(t.env.Host + "\x00" + t.in.Session + "\x00" + ref))
	return "d-" + hex.EncodeToString(h[:6])
}

func (t *translator) questions() ([]work.DecisionQuestion, questionInput) {
	var in questionInput
	_ = json.Unmarshal(t.in.ToolInput, &in)
	var qs []work.DecisionQuestion
	for i, q := range in.Questions {
		if i >= work.DecisionQuestionsMax {
			break
		}
		dq := work.DecisionQuestion{Text: q.Question, Header: q.Header, MultiSelect: q.MultiSelect}
		for j, o := range q.Options {
			if j >= work.DecisionOptionsMax {
				break
			}
			dq.Options = append(dq.Options, work.DecisionOption{Label: o.Label, Detail: o.Description})
		}
		qs = append(qs, dq)
	}
	return qs, in
}

// openQuestion opens a card for a question tool (사전 훅). 추천은 질문 도구 입력에 없다.
func (t *translator) openQuestion() {
	if t.env.NoDecisions {
		return
	}
	qs, _ := t.questions()
	if len(qs) == 0 {
		return
	}
	d := work.CleanDecision(work.DecisionInput{ID: t.questionID(), Source: work.DecisionQuestionSource, Owner: work.OwnerHuman, Questions: qs,
		Work: &work.DecisionWork{Host: t.env.Host, SessionID: t.in.Session, WaitKey: toolKey(t.in.Tool, t.in.ToolInput)}, Prompt: t.in.prompt()})
	t.add(work.KindDecisionOpen, keyOf("decision", t.in.ToolUse), d)
}

// closeQuestion closes the card with the answer given in the app that asked
// (사후 훅), or withdraws it when the call failed (사용자가 거절·중단).
func (t *translator) closeQuestion(failed bool) {
	if t.env.NoDecisions {
		return
	}
	if qs, _ := t.questions(); len(qs) == 0 {
		return // 사전 훅도 카드를 열지 않았다
	}
	id := t.questionID()
	if failed {
		t.add(work.KindDecisionWithdraw, keyOf("decision-close", t.in.ToolUse), work.DecisionWithdrawEvent{ID: id, Reason: "declined"})
		return
	}
	by := "host"
	if t.surface() == "cli" {
		by = "terminal"
	}
	t.add(work.KindDecisionAnswer, keyOf("decision-close", t.in.ToolUse), work.DecisionAnswerEvent{ID: id, Answers: work.CleanAnswers(t.answers()), By: by})
}

// answers reads the tool response into one answer per question: chosen
// option ids, and text for what matched no option.
func (t *translator) answers() []work.DecisionAnswerItem {
	qs, in := t.questions()
	var resp struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	_ = json.Unmarshal(t.in.ToolResponse, &resp)
	out := make([]work.DecisionAnswerItem, 0, len(qs))
	for i, q := range qs {
		raw, ok := resp.Answers[in.Questions[i].Question]
		if !ok && in.Questions[i].ID != "" {
			raw = resp.Answers[in.Questions[i].ID]
		}
		out = append(out, answerItem(q, values(raw)))
	}
	return out
}

// values reads an answer: a string, a list, or Codex's {answers:[…]}.
func values(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj struct {
		Answers []string `json:"answers"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Answers
	}
	return nil
}

func answerItem(q work.DecisionQuestion, vals []string) work.DecisionAnswerItem {
	label := map[string]string{}
	for i, o := range q.Options {
		label[o.Label] = strconv.Itoa(i + 1) // CleanDecision이 붙이는 id와 같다
	}
	// 옛 판의 다중 선택은 ", "로 이은 글이다: 나눈 조각이 모두 라벨이면 나눈다
	if q.MultiSelect && len(vals) == 1 && strings.Contains(vals[0], ", ") {
		parts := strings.Split(vals[0], ", ")
		all := true
		for _, p := range parts {
			if _, ok := label[p]; !ok {
				all = false
			}
		}
		if all {
			vals = parts
		}
	}
	var a work.DecisionAnswerItem
	var text []string
	for _, v := range vals {
		if id, ok := label[v]; ok {
			a.OptionIDs = append(a.OptionIDs, id)
		} else if strings.TrimSpace(v) != "" {
			text = append(text, v)
		}
	}
	a.Text = strings.Join(text, "\n") // 가리기·자르기는 CleanAnswers
	return a
}
