package mcpserver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 사용자 말 대조(명세 S-4)와 거절된 제목의 재저장(S-2). 발언과 발췌는 지어낸 중립 문구다.

const heardWords = "로그 파일은 7일 지나면 지워도 돼"

// quoteEnv is a memory_save caller over a store whose clock the test moves.
type quoteEnv struct {
	t   *testing.T
	cs  *mcp.ClientSession
	s   *memory.Store
	now time.Time
}

func newQuoteEnv(t *testing.T, o Options) *quoteEnv {
	t.Helper()
	cs, s := connectWith(t, o)
	e := &quoteEnv{t: t, cs: cs, s: s, now: time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local)}
	s.Now = func() time.Time { return e.now }
	return e
}

func (e *quoteEnv) later() { e.now = e.now.Add(time.Minute) }

// hear records what the user said, a minute after the last step.
func (e *quoteEnv) hear(words string) {
	e.t.Helper()
	e.later()
	if err := e.s.RecordUtterance("claude", words); err != nil {
		e.t.Fatal(err)
	}
}

// save calls memory_save and fails the test on a tool error.
func (e *quoteEnv) save(args map[string]any) SaveOut {
	e.t.Helper()
	out, res := e.try(args)
	if res.IsError {
		e.t.Fatalf("save: %s", text(res))
	}
	return out
}

func (e *quoteEnv) try(args map[string]any) (SaveOut, *mcp.CallToolResult) {
	e.t.Helper()
	e.later()
	var out SaveOut
	res := call(e.t, e.cs, "memory_save", args, &out)
	return out, res
}

// fact is the memory_save arguments of a user fact titled title with body, whose source is of kind, with
// excerpt when given.
func fact(title, body, kind, excerpt string) map[string]any {
	src := map[string]any{"kind": kind, "ref": "s"}
	if excerpt != "" {
		src["excerpt"] = excerpt
	}
	return map[string]any{"kind": "fact", "title": title, "body": body, "appliesTo": "user", "source": src}
}

func warned(out SaveOut, parts ...string) bool {
	for _, w := range out.Warnings {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(w, p)
		}
		if ok {
			return true
		}
	}
	return false
}

var aiServer = Options{Project: "project:a", ProjectLabel: "a", Version: "t", Actor: memory.ActorAI}

func TestMemorySave_대조에_없는_quote는_AI_출처_후보로_저장하고_알린다(t *testing.T) {
	e := newQuoteEnv(t, aiServer)
	e.hear(heardWords)
	out := e.save(fact("배포는 금요일에 하지 않는다", "금요일 배포 금지", "quote", "금요일엔 배포하지 마"))
	if !out.Saved || out.Memory.State != memory.StateCandidate {
		t.Fatalf("저장 = %+v", out)
	}
	if len(out.Memory.Sources) != 1 || out.Memory.Sources[0].Kind != memory.SourceSession {
		t.Fatalf("출처 = %+v", out.Memory.Sources)
	}
	if !warned(out, "사용자 발언", "session") {
		t.Fatalf("알림이 없습니다: %v", out.Warnings)
	}
	// 기록에 있는 발췌는 지금처럼 사용자 말로 바로 확정하고 알림이 없다.
	ok := e.save(fact("로그 보관은 7일", "7일 지나면 지운다", "quote", heardWords))
	if !ok.Saved || ok.Memory.State != memory.StateConfirmed || ok.Memory.Sources[0].Kind != memory.SourceQuote {
		t.Fatalf("대조 통과 = %+v", ok)
	}
	if warned(ok, "사용자 발언") || warned(ok, "대조") {
		t.Fatalf("대조 통과에 알림이 붙었습니다: %v", ok.Warnings)
	}
}

// 발언 기록이 없으면(기록하지 않는 호스트) 지금 동작(사용자 말로 확정)을 지키고 「대조 못 함」을 알린다.
func TestMemorySave_발언_기록이_없으면_quote를_그대로_두고_대조_못_함을_알린다(t *testing.T) {
	e := newQuoteEnv(t, aiServer)
	out := e.save(fact("로그 보관은 7일", "7일 지나면 지운다", "quote", heardWords))
	if !out.Saved || out.Memory.State != memory.StateConfirmed || out.Memory.Sources[0].Kind != memory.SourceQuote {
		t.Fatalf("저장 = %+v", out)
	}
	if !warned(out, "대조 못 함", "기록이 없어") || warned(out, "오류") {
		t.Fatalf("알림이 없습니다: %v", out.Warnings)
	}
}

// 대조 중 오류는 통과도 「기록 없음」도 아니다: 출처를 session으로 낮춰 후보로 저장하고, 기록 없음과 다른 문구로 알린다.
func TestMemorySave_대조_중_오류면_사용자_말로_인정하지_않고_오류를_알린다(t *testing.T) {
	e := newQuoteEnv(t, aiServer)
	e.hear(heardWords)
	if _, err := e.s.DB().Exec(`DROP TABLE utterance_shingles`); err != nil {
		t.Fatal(err)
	}
	out := e.save(fact("로그 보관은 7일", "7일 지나면 지운다", "quote", heardWords))
	if !out.Saved || out.Memory.State != memory.StateCandidate || out.Memory.Sources[0].Kind != memory.SourceSession {
		t.Fatalf("저장 = %+v", out)
	}
	if !warned(out, "오류") || warned(out, "대조 못 함") {
		t.Fatalf("알림 = %v", out.Warnings)
	}
}

// 아주 큰 발췌(낱말 34,000개)도 대조가 열리지 않는다: 저장될 8KB만 나눠 대조하고, 기록에 없으면 후보다.
func TestMemorySave_아주_큰_quote도_기록에_없으면_확정되지_않는다(t *testing.T) {
	e := newQuoteEnv(t, aiServer)
	e.hear(heardWords)
	var made []string
	for i := range 34000 {
		made = append(made, fmt.Sprintf("낱말%d", i))
	}
	out := e.save(fact("로그 보관은 7일", "7일 지나면 지운다", "quote", strings.Join(made, " ")))
	if out.Memory.State == memory.StateConfirmed || out.Memory.Sources[0].Kind != memory.SourceSession {
		t.Fatalf("저장 = state %s sources %v", out.Memory.State, out.Memory.Sources[0].Kind)
	}
}

// 기록에 없어 session으로 낮춘 출처는 AI 출처처럼 걸러진다: 진행 상황 글은 거절되고 상대 시간은 날짜로 바뀐다.
func TestMemorySave_기록에_없는_quote는_AI_출처처럼_거른다(t *testing.T) {
	e := newQuoteEnv(t, aiServer)
	e.hear(heardWords)
	_, res := e.try(fact("진행률 3/7 완료", "단계 셋까지 끝남", "quote", "진행률 3/7 완료"))
	if !res.IsError {
		t.Fatalf("진행 상황 글이 저장됐습니다: %s", text(res))
	}
	out := e.save(fact("배포 창 닫기", "3주 뒤에 배포 창을 닫는다", "quote", "3주 뒤에 배포 창을 닫는다"))
	if len(out.Rewrites) == 0 || strings.Contains(out.Memory.Body, "3주 뒤") {
		t.Fatalf("상대 시간이 그대로입니다: body=%q rewrites=%v", out.Memory.Body, out.Rewrites)
	}
}

// 사람의 통로(TrustLimit owner)로 온 사용자 말은 사람이 직접 쓴 것이라 대조하지 않는다.
func TestMemorySave_사람_통로의_quote는_대조하지_않는다(t *testing.T) {
	o := aiServer
	o.Actor, o.TrustLimit = memory.ActorUser, memory.TrustOwner
	e := newQuoteEnv(t, o)
	e.hear(heardWords)
	out := e.save(fact("배포는 금요일에 하지 않는다", "금요일 배포 금지", "quote", "금요일엔 배포하지 마"))
	if out.Memory.State != memory.StateConfirmed || out.Memory.Sources[0].Kind != memory.SourceQuote || warned(out, "대조") {
		t.Fatalf("저장 = %+v", out)
	}
}

// reject saves a fact from the session and rejects it (hidden), as the audit list does.
func (e *quoteEnv) reject(title string) memory.Memory {
	e.t.Helper()
	first := e.save(fact(title, "7일 지나면 지운다", "session", ""))
	e.later()
	m, err := e.s.SetState(first.Memory.ID, memory.StateHidden, "거절")
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func TestMemorySave_거절된_제목을_대조_통과_quote로_다시_저장하면_되살려_확정한다(t *testing.T) {
	e := newQuoteEnv(t, aiServer)
	rejected := e.reject("로그 보관은 7일")
	e.hear(heardWords)
	out := e.save(fact("로그 보관은 7일", "7일 지나면 지운다", "quote", heardWords))
	if !out.Saved || out.Memory.ID != rejected.ID || out.Memory.State != memory.StateConfirmed {
		t.Fatalf("되살림 = %+v", out)
	}
	if !warned(out, "되살려", rejected.ID) || warned(out, "본문") {
		t.Fatalf("되살림 알림 = %v", out.Warnings)
	}
	// 본문이 다른 되살림은 거절했던 때의 본문이 남았다고 함께 알린다.
	e2 := newQuoteEnv(t, aiServer)
	other := e2.reject("로그 보관은 7일")
	e2.hear(heardWords)
	out = e2.save(fact("로그 보관은 7일", "14일 지나면 지운다", "quote", heardWords))
	if out.Memory.ID != other.ID || out.Memory.State != memory.StateConfirmed || out.Memory.Body != "7일 지나면 지운다" {
		t.Fatalf("되살림 = %+v", out.Memory)
	}
	if !warned(out, "되살려", "본문", other.ID) {
		t.Fatalf("옛 본문 알림 = %v", out.Warnings)
	}
}

func TestMemorySave_거절된_제목의_그밖_저장은_저장하지_않았다고_사유와_답한다(t *testing.T) {
	cases := []struct {
		name, kind, excerpt string
		heardBefore         bool // 거절하기 전에 한 말(되살리지 않는다)
		heardAfter          bool
		reason              []string
	}{
		{"AI가 배운 것", "session", "", false, true, []string{"저장하지 않았습니다", "거절된"}},
		{"기록에 없는 사용자 말", "quote", "로그는 하루만 둬", false, true, []string{"저장하지 않았습니다", "거절된"}},
		{"대조 못 한 사용자 말", "quote", heardWords, false, false, []string{"저장하지 않았습니다", "대조하지 못해"}},
		{"거절 전에 한 말", "quote", heardWords, true, false, []string{"저장하지 않았습니다", "거절한 뒤"}},
		{"두 낱말 사용자 말", "quote", "지워도 돼", false, true, []string{"저장하지 않았습니다", "세 낱말"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newQuoteEnv(t, aiServer)
			if c.heardBefore {
				e.hear(heardWords)
			}
			rejected := e.reject("로그 보관은 7일")
			if c.heardAfter {
				e.hear(heardWords)
				e.hear("지워도 돼")
			}
			out := e.save(fact("로그 보관은 7일", "7일 지나면 지운다", c.kind, c.excerpt))
			if out.Saved {
				t.Fatalf("saved=true: %+v", out)
			}
			if out.Memory.ID != rejected.ID || out.Memory.State != memory.StateHidden {
				t.Fatalf("기억 = %+v", out.Memory)
			}
			if !warned(out, append(c.reason, rejected.ID)...) {
				t.Fatalf("사유가 없습니다: %v", out.Warnings)
			}
			now, err := e.s.Get(rejected.ID)
			if err != nil {
				t.Fatal(err)
			}
			if now.Version != rejected.Version || len(now.Sources) != len(rejected.Sources) {
				t.Fatalf("저장하지 않았다면서 썼습니다: %+v", now)
			}
		})
	}
}
