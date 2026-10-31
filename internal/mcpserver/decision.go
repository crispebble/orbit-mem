package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dcontract "github.com/onidot-labs/openorbit/internal/decision/contract"
	"github.com/onidot-labs/openorbit/internal/work"
)

var decisionContract = contractInfo{Name: dcontract.Name, Major: dcontract.Major, Minor: dcontract.Minor, Supported: dcontract.SupportedMajors}

// addDecisionTools opens the orbit.decision tools (설계 · Studio 4차 「결정 카드 계약」)
// on the reader: Orbit Studio Needs you and orbit-pi (its Telegram channel
// included) read, open, answer and withdraw cards there. The AI host has none:
// an agent asks with its question tool and the hook opens the card.
func addDecisionTools(srv *mcp.Server, w *workTools) {
	add := func(name, desc string, a *mcp.ToolAnnotations, h func(context.Context, args) (any, error)) {
		schema, err := dcontract.InputSchema(dcontract.Major, name)
		if err != nil {
			panic(err)
		}
		srv.AddTool(&mcp.Tool{Name: name, Description: desc, InputSchema: schema, Annotations: a}, contractHandler(decisionContract, schema, h))
	}
	d := decisionTools{w}
	add("decision_list", "결정 카드 목록(orbit.decision). state(기본 open)·source·sessionId·workId·updatedSince로 거른다. 최근에 연 것부터.", readHints(), d.list)
	add("decision_get", "결정 카드 하나.", readHints(), d.get)
	add("decision_changes", "after(앞 응답의 seq, work_changes와 같은 커서) 뒤에 바뀐 카드 id. 바뀐 것이 없으면 wait 밀리초까지 기다린다. reset이면 처음부터 다시 읽는다.", readHints(), d.changes)
	add("decision_open", "결정 카드를 연다. owner는 답을 세션에 넘길 쪽(orbit-pi·studio; 사람이 직접 연 세션의 질문은 훅이 human으로 연다).", writeHints(false, false), d.open)
	add("decision_answer", "열린 카드에 답한다(비교 후 교체). 먼저 답한 쪽이 이기고, 늦은 쪽은 CONFLICT와 먼저 답한 카드를 받는다. owner=human 카드는 CANNOT_RELAY(그 앱에서 답한다). 사용자의 답을 받은 뒤에만 부른다.", writeHints(false, false), d.answer)
	add("decision_withdraw", "열린 카드를 거둔다(묶인 대상이 바뀜, 런타임이 멈춤 등).", writeHints(false, true), d.withdraw)
}

type decisionTools struct{ w *workTools }

func (d decisionTools) store() (*work.Store, error) {
	s, err := d.w.store()
	if err != nil {
		return nil, &callError{Code: "UNAVAILABLE", Message: "작업 기록을 열지 못했습니다: " + err.Error()}
	}
	return s, nil
}

func (d decisionTools) list(_ context.Context, a args) (any, error) {
	s, err := d.store()
	if err != nil {
		return nil, err
	}
	f := work.DecisionFilter{States: a.strs("state"), Sources: a.strs("source"), SessionID: a.str("sessionId"), WorkID: a.str("workId"), Limit: a.int("limit")}
	if v := a.str("updatedSince"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, invalid("updatedSince", "updatedSince는 RFC 3339 시각이어야 합니다.")
		}
		f.Since = t.UnixMilli()
	}
	p, err := s.ListDecisions(f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"seq": s.EncodeSeq(p.Seq), "items": p.Items, "total": p.Total, "truncated": p.Truncated, "limitMax": work.DecisionLimitMax}, nil
}

func (d decisionTools) get(_ context.Context, a args) (any, error) {
	s, err := d.store()
	if err != nil {
		return nil, err
	}
	dec, seq, err := s.GetDecision(a.str("id"))
	return d.result(s, dec, seq, err)
}

func (d decisionTools) changes(ctx context.Context, a args) (any, error) {
	s, err := d.store()
	if err != nil {
		return nil, err
	}
	c, err := s.DecisionChanges(ctx, a.str("after"), time.Duration(a.int("wait"))*time.Millisecond)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"seq": s.EncodeSeq(c.Seq), "reset": c.Reset, "more": c.More, "waitMax": work.WaitMax.Milliseconds()}
	if !c.Reset {
		out["decisionIds"] = c.IDs
	}
	return out, nil
}

// decode reads a structured argument (질문·연결 같은 객체) into v.
func decode(a args, k string, v any) error {
	raw, ok := a[k]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return invalid(k, k+" 값을 읽을 수 없습니다.")
	}
	return nil
}

func (d decisionTools) open(_ context.Context, a args) (any, error) {
	in := work.DecisionInput{ID: a.str("id"), Source: a.str("source"), Owner: a.str("owner"), Context: a.str("context")}
	for k, v := range map[string]any{"questions": &in.Questions, "links": &in.Links, "work": &in.Work, "binding": &in.Binding} {
		if err := decode(a, k, v); err != nil {
			return nil, err
		}
	}
	if n := len(in.Questions); n == 0 || n > work.DecisionQuestionsMax {
		return nil, invalid("questions", "questions는 1~10개입니다.")
	}
	for _, q := range in.Questions {
		if q.Text == "" || len(q.Options) > work.DecisionOptionsMax {
			return nil, invalid("questions", "질문마다 text가 있고 선택지는 20개까지입니다.")
		}
		if q.Recommended != "" && !slices.ContainsFunc(q.Options, func(o work.DecisionOption) bool { return o.ID == q.Recommended }) {
			return nil, invalid("questions", "recommended는 그 질문의 선택지 id여야 합니다.")
		}
	}
	if in.Work != nil && (in.Work.Host == "") != (in.Work.SessionID == "") {
		return nil, invalid("work", "work.host와 work.sessionId는 함께 줍니다.")
	}
	if v := a.str("expiresAt"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, invalid("expiresAt", "expiresAt은 RFC 3339 시각이어야 합니다.")
		}
		in.ExpiresAt = t.UnixMilli()
	}
	s, err := d.store()
	if err != nil {
		return nil, err
	}
	dec, seq, err := s.OpenDecision(in)
	return d.result(s, dec, seq, err)
}

func (d decisionTools) answer(_ context.Context, a args) (any, error) {
	var answers []work.DecisionAnswerItem
	if err := decode(a, "answers", &answers); err != nil {
		return nil, err
	}
	s, err := d.store()
	if err != nil {
		return nil, err
	}
	dec, seq, err := s.AnswerDecision(a.str("id"), answers, a.str("by"))
	return d.result(s, dec, seq, err)
}

func (d decisionTools) withdraw(_ context.Context, a args) (any, error) {
	s, err := d.store()
	if err != nil {
		return nil, err
	}
	dec, seq, err := s.WithdrawDecision(a.str("id"), a.str("reason"))
	return d.result(s, dec, seq, err)
}

func (d decisionTools) result(s *work.Store, dec work.Decision, seq int64, err error) (any, error) {
	var c *work.DecisionConflict
	switch {
	case errors.As(err, &c):
		msg := "카드가 이미 " + c.Current.State + " 상태입니다."
		if c.Current.Answer != nil {
			msg = "이미 " + c.Current.Answer.By + "에서 답했습니다."
		}
		cur := c.Current
		return nil, &callError{Code: "CONFLICT", Message: msg, Decision: &cur}
	case errors.Is(err, work.ErrNotFound):
		return nil, &callError{Code: "NOT_FOUND", Message: "카드가 없습니다.", Field: "id"}
	case errors.Is(err, work.ErrCannotRelay):
		return nil, &callError{Code: "CANNOT_RELAY", Message: "사람이 직접 연 세션의 카드입니다. 질문한 앱에서 답해 주세요.", Field: "id"}
	case errors.Is(err, work.ErrInvalidAnswer):
		return nil, invalid("answers", "answers는 질문마다 하나씩, 같은 순서로 그 질문의 선택지 id나 글을 줍니다(단일 선택은 하나).")
	case err != nil:
		return nil, err
	}
	return map[string]any{"seq": s.EncodeSeq(seq), "decision": dec}, nil
}
