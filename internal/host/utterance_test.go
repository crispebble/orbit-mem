package host_test

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func utteranceCount(t *testing.T, s *memory.Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM utterances`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 요청 회상 훅은 사람 발언(inject.PersonWords가 가른 사람 글)만 사용자 말 대조용으로 기록한다(명세 S-4).
// 출처가 사람이 아니거나 호스트가 만든 틀뿐인 요청은 기록하지 않는다. 붙여 넣은 틀은 사람 글이 아니라 빼고 남긴다.
func TestRecall_사람_발언이_아니면_기록하지_않는다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	a := e.adapter("s-1")
	for _, f := range []map[string]any{
		{"prompt": "로그 파일은 7일 지나면 지워도 돼", "origin": "agent"},
		{"prompt": "로그 파일은 7일 지나면 지워도 돼", "origin": "system"},
		{"prompt": "<task-notification>\n로그 파일은 7일 지나면 지워도 돼\n</task-notification>"},
		{"prompt": "[Subagent hand-back]\n로그 파일은 7일 지나면 지워도 돼"},
		{"prompt": ""},
	} {
		a.recall(host.OpRecall, f)
	}
	e.svc.Wait()
	if n := utteranceCount(t, s); n != 0 {
		t.Fatalf("사람 발언이 아닌데 %d건 기록했다", n)
	}
	if got, _ := s.MatchQuote("로그 파일은 7일 지나면 지워도 돼"); got != memory.QuoteUnchecked {
		t.Fatalf("기록이 없어야 한다: %s", got)
	}

	// 사람이 틀 글을 붙여 넣고 말하면 사람 글만 기록한다.
	a.recall(host.OpRecall, map[string]any{"prompt": "<task-notification>\n배포는 금요일 오후에 하지 마\n</task-notification>\n\n로그 파일은 7일 지나면 지워도 돼"})
	e.svc.Wait()
	if n := utteranceCount(t, s); n != 1 {
		t.Fatalf("사람 발언 기록 = %d", n)
	}
	if got, _ := s.MatchQuote("로그 파일은 7일 지나면 지워도 돼"); got != memory.QuoteMatched {
		t.Fatalf("사람 글 = %s", got)
	}
	if got, _ := s.MatchQuote("배포는 금요일 오후에 하지 마"); got != memory.QuoteNotFound {
		t.Fatalf("붙여 넣은 틀 = %s", got)
	}
}
