package work

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func askQuestions() []DecisionQuestion {
	return []DecisionQuestion{
		{Text: "어느 쪽으로 할까요?", Header: "방식", Options: []DecisionOption{{ID: "1", Label: "가"}, {ID: "2", Label: "나"}}},
		{Text: "무엇을 켤까요?", Header: "기능", MultiSelect: true, Options: []DecisionOption{{ID: "1", Label: "A"}, {ID: "2", Label: "B"}, {ID: "3", Label: "C"}}},
	}
}

// 훅이 연 질문 카드: 질문·다중 선택이 그대로 있고, 그 세션의 진행 단계에 묶인다.
func TestDecisionOpenFromHook(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{Surface: "cli"})
	activeTask(w, "1")
	w.claude(KindDecisionOpen, DecisionInput{ID: "d-1", Source: DecisionQuestionSource, Owner: OwnerHuman, Questions: askQuestions(),
		Work: &DecisionWork{Host: "claude", SessionID: "S1", WaitKey: "k1"}, Prompt: "P1"})
	d, _, err := w.s.GetDecision("d-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.State != DecisionOpenState || d.Source != "question" || d.Owner != "human" || len(d.Questions) != 2 || !d.Questions[1].MultiSelect {
		t.Fatalf("카드 = %+v", d)
	}
	work := w.only()
	if d.Work == nil || d.Work.WorkID != work.ID || d.Work.StepID != work.Steps[0].ID || d.Work.WaitKey != "k1" {
		t.Fatalf("작업 묶음 = %+v", d.Work)
	}
	if d.CreatedAt == "" || d.Answer != nil {
		t.Fatalf("시각·답 = %+v", d)
	}
}

// 먼저 답한 쪽이 이기고, 늦은 쪽은 충돌과 먼저 답한 내용을 받는다.
func TestDecisionAnswerCompareAndSwap(t *testing.T) {
	w := newTW(t)
	d, _, err := w.s.OpenDecision(DecisionInput{Source: DecisionOpsSource, Owner: OwnerOrbitPi, Questions: askQuestions()[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID == "" || d.State != "open" {
		t.Fatalf("열기 = %+v", d)
	}
	got, _, err := w.s.AnswerDecision(d.ID, []DecisionAnswerItem{{OptionIDs: []string{"2"}}}, "studio")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "answered" || got.Answer == nil || got.Answer.By != "studio" || got.Answer.Answers[0].OptionIDs[0] != "2" {
		t.Fatalf("답 = %+v", got)
	}
	_, _, err = w.s.AnswerDecision(d.ID, []DecisionAnswerItem{{OptionIDs: []string{"1"}}}, "telegram")
	var c *DecisionConflict
	if !errors.As(err, &c) || c.Current.Answer == nil || c.Current.Answer.By != "studio" || c.Current.State != "answered" {
		t.Fatalf("충돌 = %v", err)
	}
	if _, _, err := w.s.WithdrawDecision(d.ID, "binding_changed"); !errors.As(err, &c) {
		t.Fatalf("답한 카드 거두기 = %v", err)
	}
}

func TestDecisionAnswerValidation(t *testing.T) {
	w := newTW(t)
	d, _, _ := w.s.OpenDecision(DecisionInput{Source: DecisionPluginSource, Owner: OwnerStudio, Questions: askQuestions()})
	cases := map[string][]DecisionAnswerItem{
		"질문 수가 다름": {{OptionIDs: []string{"1"}}},
		"없는 선택지":   {{OptionIDs: []string{"9"}}, {Text: "x"}},
		"단일 선택에 둘": {{OptionIDs: []string{"1", "2"}}, {Text: "x"}},
		"빈 답":      {{}, {OptionIDs: []string{"1"}}},
	}
	for name, a := range cases {
		if _, _, err := w.s.AnswerDecision(d.ID, a, "studio"); !errors.Is(err, ErrInvalidAnswer) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := w.s.AnswerDecision(d.ID, []DecisionAnswerItem{{Text: "직접 쓴 답"}, {OptionIDs: []string{"1", "3"}}}, "studio"); err != nil {
		t.Fatalf("다른 답·다중 선택: %v", err)
	}
	if _, _, err := w.s.AnswerDecision("d-none", nil, "studio"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 카드: %v", err)
	}
}

// 사람이 직접 연 세션의 카드는 답을 넘길 수 없다. 그 앱에서 답하면 훅이 닫는다.
func TestDecisionHumanOwnerCannotRelay(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{Surface: "desktop"})
	w.claude(KindDecisionOpen, DecisionInput{ID: "d-1", Source: "question", Owner: OwnerHuman, Questions: askQuestions()[:1],
		Work: &DecisionWork{Host: "claude", SessionID: "S1", WaitKey: "k1"}})
	if _, _, err := w.s.AnswerDecision("d-1", []DecisionAnswerItem{{OptionIDs: []string{"1"}}}, "studio"); !errors.Is(err, ErrCannotRelay) {
		t.Fatalf("사람 세션 카드에 답 = %v", err)
	}
	w.claude(KindDecisionAnswer, DecisionAnswerEvent{ID: "d-1", Answers: []DecisionAnswerItem{{OptionIDs: []string{"1"}}}, By: "terminal"})
	d, _, _ := w.s.GetDecision("d-1")
	if d.State != "answered" || d.Answer.By != "terminal" {
		t.Fatalf("훅이 닫음 = %+v", d)
	}
}

// 비동기 훅: 사후(답)가 열기보다 먼저 와도 카드는 답한 상태로 남고 질문이 채워진다.
func TestDecisionAnswerBeforeOpen(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{})
	w.evAt(w.at+2000, KindDecisionAnswer, "claude", "S1", DecisionAnswerEvent{ID: "d-1", Answers: []DecisionAnswerItem{{Text: "가"}}, By: "host"})
	w.evAt(w.at+1000, KindDecisionOpen, "claude", "S1", DecisionInput{ID: "d-1", Source: "question", Owner: OwnerHuman, Questions: askQuestions()[:1],
		Work: &DecisionWork{Host: "claude", SessionID: "S1"}})
	d, _, err := w.s.GetDecision("d-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "answered" || len(d.Questions) != 1 || d.Answer == nil {
		t.Fatalf("늦은 열기 = %+v", d)
	}
	// 거절(사후 실패)이 먼저 온 경우도 같다
	w.evAt(w.at+4000, KindDecisionWithdraw, "claude", "S1", DecisionWithdrawEvent{ID: "d-2", Reason: "declined"})
	w.evAt(w.at+3000, KindDecisionOpen, "claude", "S1", DecisionInput{ID: "d-2", Source: "question", Owner: OwnerHuman, Questions: askQuestions()[:1]})
	if d, _, _ := w.s.GetDecision("d-2"); d.State != "withdrawn" || d.CloseReason != "declined" || len(d.Questions) != 1 {
		t.Fatalf("거절 뒤 늦은 열기 = %+v", d)
	}
}

// 턴·세션이 끝나면 그 세션의 열린 카드는 거둔다.
func TestDecisionWithdrawnOnTurnAndSessionEnd(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{})
	w.claude(KindTurnStart, Turn{Prompt: "P1"})
	w.claude(KindDecisionOpen, DecisionInput{ID: "d-1", Source: "question", Owner: OwnerHuman, Questions: askQuestions()[:1],
		Work: &DecisionWork{Host: "claude", SessionID: "S1"}, Prompt: "P1"})
	w.claude(KindTurnEnd, Turn{Prompt: "P1"})
	if d, _, _ := w.s.GetDecision("d-1"); d.State != "withdrawn" || d.CloseReason != "turn_ended" {
		t.Fatalf("턴 끝 = %+v", d)
	}
	// 이미 끝난 턴의 늦은 열기는 거둔 채로 연다
	w.claude(KindDecisionOpen, DecisionInput{ID: "d-late", Source: "question", Owner: OwnerHuman, Questions: askQuestions()[:1],
		Work: &DecisionWork{Host: "claude", SessionID: "S1"}, Prompt: "P1"})
	if d, _, _ := w.s.GetDecision("d-late"); d.State != "withdrawn" {
		t.Fatalf("늦은 열기 = %+v", d)
	}
	w.claude(KindTurnStart, Turn{Prompt: "P2"})
	w.claude(KindDecisionOpen, DecisionInput{ID: "d-2", Source: "permission", Owner: OwnerOrbitPi, Questions: askQuestions()[:1],
		Work: &DecisionWork{Host: "claude", SessionID: "S1"}, Prompt: "P2"})
	w.claude(KindSessionEnd, SessionEnd{Reason: "other"})
	if d, _, _ := w.s.GetDecision("d-2"); d.State != "withdrawn" || d.CloseReason != "session_ended" {
		t.Fatalf("세션 끝 = %+v", d)
	}
	open, err := w.s.ListDecisions(DecisionFilter{})
	if err != nil || len(open.Items) != 0 {
		t.Fatalf("열린 카드 = %+v %v", open.Items, err)
	}
}

func TestDecisionExpires(t *testing.T) {
	w := newTW(t)
	exp := w.s.Now().Add(time.Minute).UnixMilli()
	d, _, _ := w.s.OpenDecision(DecisionInput{Source: "ops", Owner: OwnerOrbitPi, Questions: askQuestions()[:1], ExpiresAt: exp})
	base := w.s.Now()
	w.s.Now = func() time.Time { return base.Add(2 * time.Minute) }
	got, _, _ := w.s.GetDecision(d.ID)
	if got.State != "expired" || got.ExpiresAt == "" {
		t.Fatalf("만료 = %+v", got)
	}
	var c *DecisionConflict
	if _, _, err := w.s.AnswerDecision(d.ID, []DecisionAnswerItem{{OptionIDs: []string{"1"}}}, "studio"); !errors.As(err, &c) || c.Current.State != "expired" {
		t.Fatalf("만료 뒤 답 = %v", err)
	}
	page, _ := w.s.ListDecisions(DecisionFilter{States: []string{"expired"}})
	if len(page.Items) != 1 {
		t.Fatalf("만료 거르기 = %+v", page.Items)
	}
}

func TestDecisionListAndChanges(t *testing.T) {
	w := newTW(t)
	ch, _ := w.s.DecisionChanges(context.Background(), "", 0)
	if !ch.Reset {
		t.Fatal("after 없으면 reset")
	}
	a, _, _ := w.s.OpenDecision(DecisionInput{Source: "ops", Owner: OwnerOrbitPi, Questions: askQuestions()[:1],
		Binding: &DecisionBinding{Kind: "pr-head", Ref: "onidot-labs/atlas#12@abc"}, Context: "운영 감시 분석", Links: []DecisionLink{{Kind: "pr", Ref: "https://github.com/onidot-labs/atlas/pull/12"}}})
	cur := w.s.EncodeSeq(mustSeq(t, w.s))
	base := w.s.Now()
	w.s.Now = func() time.Time { return base.Add(time.Second) }
	b, _, _ := w.s.OpenDecision(DecisionInput{Source: "plugin", Owner: OwnerStudio, Questions: askQuestions()[:1]})
	ch, err := w.s.DecisionChanges(context.Background(), cur, 0)
	if err != nil || ch.Reset || len(ch.IDs) != 1 || ch.IDs[0] != b.ID {
		t.Fatalf("변경 = %+v %v", ch, err)
	}
	// 기다리는 동안 다른 연결(다른 프로세스)이 쓴 변경을 받는다
	other, err := Open(w.s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _, _ = other.AnswerDecision(a.ID, []DecisionAnswerItem{{OptionIDs: []string{"1"}}}, "telegram")
	}()
	ch, err = w.s.DecisionChanges(context.Background(), w.s.EncodeSeq(ch.Seq), 2*time.Second)
	if err != nil || len(ch.IDs) != 1 || ch.IDs[0] != a.ID {
		t.Fatalf("기다린 변경 = %+v %v", ch, err)
	}
	page, _ := w.s.ListDecisions(DecisionFilter{States: []string{"open", "answered"}})
	if len(page.Items) != 2 || page.Items[0].ID != b.ID {
		t.Fatalf("목록(최근 순) = %+v", page.Items)
	}
	if got, _, _ := w.s.GetDecision(a.ID); got.Binding == nil || got.Binding.Ref == "" || got.Context == "" || len(got.Links) != 1 {
		t.Fatalf("묶음·근거 = %+v", got)
	}
	// 다시 만들어도 같은 카드
	if err := w.s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	again, _, _ := w.s.GetDecision(a.ID)
	if again.State != "answered" || again.Answer.By != "telegram" {
		t.Fatalf("다시 만든 카드 = %+v", again)
	}
}

func mustSeq(t *testing.T, s *Store) int64 {
	t.Helper()
	n, err := s.LastSeq()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// 누가 세션을 띄웠는지(origin.launcher)가 세션과 그 세션이 연 작업에 남는다.
func TestLauncher(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{Surface: "headless", Launcher: "studio"})
	activeTask(w, "1")
	if got := w.only().Origin.Launcher; got != "studio" {
		t.Fatalf("작업 launcher = %q", got)
	}
	if got := w.sessionDoc("claude", "S1").Launcher; got != "studio" {
		t.Fatalf("세션 launcher = %q", got)
	}
}

// 검토 지적: 턴 끝은 그 턴의 카드만 거두고, 사용자가 실제로 답했으면 도착 순서와 상관없이 answered로 끝난다.
func TestDecisionTurnOrdering(t *testing.T) {
	open := func(id, prompt string) DecisionInput {
		return DecisionInput{ID: id, Source: "question", Owner: OwnerHuman, Questions: askQuestions()[:1], Work: &DecisionWork{Host: "claude", SessionID: "S1"}, Prompt: prompt}
	}
	answer := DecisionAnswerEvent{ID: "d-1", Answers: []DecisionAnswerItem{{OptionIDs: []string{"1"}}}, By: "terminal"}
	type step struct {
		kind    string
		payload any
	}
	orders := map[string][]step{
		"열기·답·턴끝": {{KindDecisionOpen, open("d-1", "P1")}, {KindDecisionAnswer, answer}, {KindTurnEnd, Turn{Prompt: "P1"}}},
		"턴끝·열기·답": {{KindTurnEnd, Turn{Prompt: "P1"}}, {KindDecisionOpen, open("d-1", "P1")}, {KindDecisionAnswer, answer}},
		"열기·턴끝·답": {{KindDecisionOpen, open("d-1", "P1")}, {KindTurnEnd, Turn{Prompt: "P1"}}, {KindDecisionAnswer, answer}},
		"턴끝·답·열기": {{KindTurnEnd, Turn{Prompt: "P1"}}, {KindDecisionAnswer, answer}, {KindDecisionOpen, open("d-1", "P1")}},
	}
	for name, steps := range orders {
		w := newTW(t)
		w.start("claude", "S1", SessionStart{})
		w.claude(KindTurnStart, Turn{Prompt: "P1"})
		for _, s := range steps {
			w.claude(s.kind, s.payload)
		}
		if d, _, _ := w.s.GetDecision("d-1"); d.State != "answered" || len(d.Questions) != 1 {
			t.Errorf("%s: %+v", name, d)
		}
	}
	// 앞 턴의 늦은 Stop은 새 턴의 카드를 거두지 않는다
	w := newTW(t)
	w.start("claude", "S1", SessionStart{})
	w.claude(KindTurnStart, Turn{Prompt: "P2"})
	w.claude(KindDecisionOpen, open("d-2", "P2"))
	w.claude(KindTurnEnd, Turn{Prompt: "P1"})
	if d, _, _ := w.s.GetDecision("d-2"); d.State != "open" {
		t.Fatalf("앞 턴 Stop이 새 턴 카드를 거뒀습니다: %+v", d)
	}
	// 자리만 있는 카드는 읽을 수 없다
	w.claude(KindDecisionWithdraw, DecisionWithdrawEvent{ID: "d-ghost", Reason: "declined"})
	if _, _, err := w.s.GetDecision("d-ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("자리 카드 읽기 = %v", err)
	}
}

// 검토 지적: 답 글은 비밀값을 가리고 자른다.
func TestDecisionAnswerTextIsCleaned(t *testing.T) {
	w := newTW(t)
	d, _, _ := w.s.OpenDecision(DecisionInput{Source: "ops", Owner: OwnerOrbitPi, Questions: askQuestions()[:1]})
	got, _, err := w.s.AnswerDecision(d.ID, []DecisionAnswerItem{{Text: "토큰은 ghp_abcdefghijklmnopqrstuvwxyz0123456789 입니다 " + strings.Repeat("가", 3000)}}, "studio")
	if err != nil {
		t.Fatal(err)
	}
	text := got.Answer.Answers[0].Text
	if strings.Contains(text, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") || len([]rune(text)) > 2000 {
		t.Fatalf("답 글 = %d자 %q", len([]rune(text)), text[:80])
	}
}

// 검토 지적: 한 번에 다 담지 못하면 잘린 사건의 카드는 다음 부름에 다시 나온다.
func TestDecisionChangesPageBoundary(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{})
	w.claude(KindTurnStart, Turn{Prompt: "P1"})
	start := w.s.EncodeSeq(mustSeq(t, w.s))
	n := ChangesMax + 3
	for i := 0; i < n; i++ {
		w.claude(KindDecisionOpen, DecisionInput{ID: fmt.Sprintf("d-%04d", i), Source: "question", Owner: OwnerHuman, Questions: askQuestions()[:1],
			Work: &DecisionWork{Host: "claude", SessionID: "S1"}, Prompt: "P1"})
	}
	w.claude(KindTurnEnd, Turn{Prompt: "P1"}) // 한 사건이 카드 n개를 바꾼다
	seen := map[string]int{}
	cur := start
	for i := 0; i < 10; i++ {
		ch, err := w.s.DecisionChanges(context.Background(), cur, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ch.IDs {
			seen[id]++
		}
		cur = w.s.EncodeSeq(ch.Seq)
		if !ch.More {
			break
		}
	}
	if len(seen) != n {
		t.Fatalf("받은 카드 %d개, %d개여야 합니다", len(seen), n)
	}
}
