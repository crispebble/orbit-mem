package mcpserver

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// 사용자 말 대조(명세 S-4)와 거절된 제목의 재저장(S-2). 발언과 발췌는 지어낸 중립 문구다.

const heardWords = "로그 파일은 7일 지나면 지워도 돼"

// saveFact calls memory_save with a user fact titled title whose source is of kind, with excerpt when given.
func saveFact(t *testing.T, call func(map[string]any) SaveOut, title, kind, excerpt string) SaveOut {
	t.Helper()
	src := map[string]any{"kind": kind, "ref": "s"}
	if excerpt != "" {
		src["excerpt"] = excerpt
	}
	return call(map[string]any{"kind": "fact", "title": title, "body": "7일 지나면 지운다", "appliesTo": "user", "source": src})
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

func quoteSaver(t *testing.T, o Options) (func(map[string]any) SaveOut, *memory.Store) {
	t.Helper()
	cs, s := connectWith(t, o)
	return func(args map[string]any) SaveOut {
		t.Helper()
		var out SaveOut
		if res := call(t, cs, "memory_save", args, &out); res.IsError {
			t.Fatalf("save: %s", text(res))
		}
		return out
	}, s
}

var aiServer = Options{Project: "project:a", ProjectLabel: "a", Version: "t", Actor: memory.ActorAI}

func TestMemorySave_대조에_없는_quote는_AI_출처_후보로_저장하고_알린다(t *testing.T) {
	save, s := quoteSaver(t, aiServer)
	if err := s.RecordUtterance("claude", heardWords); err != nil {
		t.Fatal(err)
	}
	out := saveFact(t, save, "배포는 금요일에 하지 않는다", "quote", "금요일엔 배포하지 마")
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
	ok := saveFact(t, save, "로그 보관은 7일", "quote", heardWords)
	if !ok.Saved || ok.Memory.State != memory.StateConfirmed || ok.Memory.Sources[0].Kind != memory.SourceQuote {
		t.Fatalf("대조 통과 = %+v", ok)
	}
	if warned(ok, "사용자 발언") || warned(ok, "대조") {
		t.Fatalf("대조 통과에 알림이 붙었습니다: %v", ok.Warnings)
	}
}

// 발언 기록이 없으면(기록하지 않는 호스트) 지금 동작(사용자 말로 확정)을 지키고 「대조 못 함」을 알린다.
func TestMemorySave_발언_기록이_없으면_quote를_그대로_두고_대조_못_함을_알린다(t *testing.T) {
	save, _ := quoteSaver(t, aiServer)
	out := saveFact(t, save, "로그 보관은 7일", "quote", heardWords)
	if !out.Saved || out.Memory.State != memory.StateConfirmed || out.Memory.Sources[0].Kind != memory.SourceQuote {
		t.Fatalf("저장 = %+v", out)
	}
	if !warned(out, "대조 못 함") {
		t.Fatalf("알림이 없습니다: %v", out.Warnings)
	}
}

// 사람의 통로(TrustLimit owner)로 온 사용자 말은 사람이 직접 쓴 것이라 대조하지 않는다.
func TestMemorySave_사람_통로의_quote는_대조하지_않는다(t *testing.T) {
	o := aiServer
	o.Actor, o.TrustLimit = memory.ActorUser, memory.TrustOwner
	save, s := quoteSaver(t, o)
	if err := s.RecordUtterance("claude", heardWords); err != nil {
		t.Fatal(err)
	}
	out := saveFact(t, save, "배포는 금요일에 하지 않는다", "quote", "금요일엔 배포하지 마")
	if out.Memory.State != memory.StateConfirmed || out.Memory.Sources[0].Kind != memory.SourceQuote || warned(out, "대조") {
		t.Fatalf("저장 = %+v", out)
	}
}

// rejectFact saves a fact from the session and rejects it (hidden), as the audit list does.
func rejectFact(t *testing.T, save func(map[string]any) SaveOut, s *memory.Store, title string) memory.Memory {
	t.Helper()
	first := saveFact(t, save, title, "session", "")
	m, err := s.SetState(first.Memory.ID, memory.StateHidden, "거절")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMemorySave_거절된_제목을_대조_통과_quote로_다시_저장하면_되살려_확정한다(t *testing.T) {
	save, s := quoteSaver(t, aiServer)
	rejected := rejectFact(t, save, s, "로그 보관은 7일")
	if err := s.RecordUtterance("claude", heardWords); err != nil {
		t.Fatal(err)
	}
	out := saveFact(t, save, "로그 보관은 7일", "quote", heardWords)
	if !out.Saved || out.Memory.ID != rejected.ID || out.Memory.State != memory.StateConfirmed {
		t.Fatalf("되살림 = %+v", out)
	}
}

func TestMemorySave_거절된_제목의_그밖_저장은_저장하지_않았다고_사유와_답한다(t *testing.T) {
	cases := []struct {
		name, kind, excerpt string
		heard               bool
		reason              []string
	}{
		{"AI가 배운 것", "session", "", true, []string{"저장하지 않았습니다", "거절된"}},
		{"기록에 없는 사용자 말", "quote", "로그는 하루만 둬", true, []string{"저장하지 않았습니다", "거절된"}},
		{"대조 못 한 사용자 말", "quote", heardWords, false, []string{"저장하지 않았습니다", "대조하지 못해"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			save, s := quoteSaver(t, aiServer)
			rejected := rejectFact(t, save, s, "로그 보관은 7일")
			if c.heard {
				if err := s.RecordUtterance("claude", heardWords); err != nil {
					t.Fatal(err)
				}
			}
			out := saveFact(t, save, "로그 보관은 7일", c.kind, c.excerpt)
			if out.Saved {
				t.Fatalf("saved=true: %+v", out)
			}
			if out.Memory.ID != rejected.ID || out.Memory.State != memory.StateHidden {
				t.Fatalf("기억 = %+v", out.Memory)
			}
			if !warned(out, append(c.reason, rejected.ID)...) {
				t.Fatalf("사유가 없습니다: %v", out.Warnings)
			}
			now, err := s.Get(rejected.ID)
			if err != nil {
				t.Fatal(err)
			}
			if now.Version != rejected.Version || len(now.Sources) != len(rejected.Sources) {
				t.Fatalf("저장하지 않았다면서 썼습니다: %+v", now)
			}
		})
	}
}
