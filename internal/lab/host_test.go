package lab

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestParseHostOutput_Claude_JSON에서_비용과_결과를_읽는다(t *testing.T) {
	// claude 2.1.303 실제 출력 모양: type이 맨 앞이 아니고 한 줄짜리 객체다.
	out := "경고 한 줄\n" + `{"duration_api_ms":1078,"stop_reason":"end_turn","total_cost_usd":0.0123,"usage":{"input_tokens":1200,"cache_creation_input_tokens":0,"cache_read_input_tokens":300,"output_tokens":45,"iterations":[{"input_tokens":1}]},"result":"제출했습니다","type":"result","is_error":false}` + "\n"
	r := ParseHostOutput(out)
	if !r.Known || r.CostUSD != 0.0123 || r.InputTokens != 1500 || r.OutputTokens != 45 || r.Text != "제출했습니다" {
		t.Fatalf("parsed = %+v", r)
	}
	if r := ParseHostOutput("codex 출력"); r.Known || r.Text != "codex 출력" {
		t.Fatalf("plain = %+v", r)
	}
}

func TestExecHost_입력을_주고_출력을_모으며_취소하면_자식까지_끝낸다(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh 없음")
	}
	out, err := ExecHost(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "cat; echo err >&2"}, Stdin: "입력\n"})
	if err != nil || out != "입력\nerr\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	// 손자 프로세스가 출력 파이프를 쥔 채 남아도 취소하면 프로세스 묶음째 끝난다.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ExecHost(ctx, Cmd{Name: "sh", Args: []string{"-c", "sleep 30 & sleep 30"}}); err == nil {
		t.Fatal("cancelled run must fail")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("cancel took %v: the process group was not killed", d)
	}
}
