package memory

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// 입력 조건(명세 T-2): tools 칸의 Bash(git commit)는 Bash 호출의 입력에 그 낱말이 있을 때만 걸린다. 입력은 ; & | 줄바꿈으로
// 명령 조각을 나누고, 한 조각 안에 조건 낱말이 같은 차례로 나오면(사이에 다른 낱말이 있어도) 맞는다. 빗금으로 이은 경로 조각
// (src/app)은 입력에서도 바로 이어져야 한다. 입력이 없으면(호스트가 주지 않음) 도구 이름만으로 판정한다(T-4).
func TestTriggeredByInput_조건_낱말이_한_명령_안에_차례로_나올_때만_걸린다(t *testing.T) {
	commit := Memory{Tools: []string{"Bash(git commit)"}}
	push := Memory{Tools: []string{"Bash(git push --force)"}}
	cases := []struct {
		name, tool, input string
		m                 Memory
		exact, ok         bool
	}{
		{"조건 낱말 그대로", "Bash", "git commit -m x", commit, true, true},
		{"빈칸이 둘이어도 낱말은 같다", "Bash", "git  commit", commit, true, true},
		{"앞 명령 뒤 조각이어도 된다", "Bash", "cd /w && git commit -am x", commit, true, true},
		{"사이에 선택지가 있어도 된다", "Bash", "git -C /w commit", commit, true, true},
		{"사이에 설정 값이 있어도 된다", "Bash", "git -c user.name=x commit -m y", commit, true, true},
		{"뒤에 온 선택지도 차례만 맞으면 된다", "Bash", "git push origin main --force", push, true, true},
		{"따옴표 안의 낱말도 낱말이다", "Bash", `echo "git commit"`, commit, true, true},
		{"대소문자를 가리지 않는다", "Bash", "GIT Commit", commit, true, true},
		{"줄바꿈 뒤 조각도 명령이다", "Bash", "ls\ngit commit", commit, true, true},
		{"다른 명령에는 걸리지 않는다", "Bash", "ls -la", commit, false, false},
		{"조각이 다르면 걸리지 않는다", "Bash", "git status; ls commit", commit, false, false},
		{"파이프 너머 조각도 다른 명령이다", "Bash", "git log | grep commit", commit, false, false},
		{"차례가 바뀌면 걸리지 않는다", "Bash", "commit git", commit, false, false},
		{"commit-tree는 commit이 아니다", "Bash", "git commit-tree HEAD^{tree}", commit, false, false},
		{"다른 도구에는 걸리지 않는다", "Edit", "git commit", commit, false, false},
		{"입력이 없으면 이름만으로 판정한다", "Bash", "", commit, true, true},
		{"입력이 빈칸뿐이면 없는 것이다", "Bash", "  ", commit, true, true},
		{"조건 여럿은 하나만 맞아도 된다", "Bash", "git push origin x", Memory{Tools: []string{"Bash(git commit)", "Bash(git push)"}}, true, true},
		{"조건 여럿이 모두 안 맞으면 안 걸린다", "Bash", "git status", Memory{Tools: []string{"Bash(git commit)", "Bash(git push)"}}, false, false},
		{"조건 없는 이름이 함께 있으면 그쪽이 이긴다", "Bash", "ls", Memory{Tools: []string{"Bash(git commit)", "Bash"}}, true, true},
		{"조건 없는 이름은 입력과 관계없다", "Bash", "ls", Memory{Tools: []string{"Bash"}}, true, true},
		{"접두는 입력과 관계없다", "mcp__docs__write_page", "x", Memory{Tools: []string{"mcp__docs__*"}}, false, true},
		{"접두가 아닌 도구는 안 걸린다", "Read", "x", Memory{Tools: []string{"mcp__docs__*"}}, false, false},
		{"경로 조건은 파일 이름 낱말에 맞는다", "Edit", "/w/app/package.json", Memory{Tools: []string{"Edit(package.json)"}}, true, true},
		{"경로 조건은 다른 파일에 맞지 않는다", "Edit", "/w/app/package.json.bak", Memory{Tools: []string{"Edit(package.json)"}}, false, false},
		{"점으로 시작하는 파일도 낱말 통째다", "Edit", "/w/.env.local", Memory{Tools: []string{"Edit(.env)"}}, false, false},
		{"이어진 폴더 조각에 맞는다", "Edit", "/w/src/app/x.ts", Memory{Tools: []string{"Edit(src/app)"}}, true, true},
		{"폴더 조각 사이가 떨어지면 맞지 않는다", "Edit", "/w/src/lib/../app/x.ts", Memory{Tools: []string{"Edit(src/app)"}}, false, false},
		{"명령 안의 경로 조각도 이어져야 한다", "Bash", "ls src lib app", Memory{Tools: []string{"Bash(src/app)"}}, false, false},
		{"명령 안의 이어진 경로는 맞는다", "Bash", "rm -rf ./src/app/cache", Memory{Tools: []string{"Bash(rm src/app)"}}, true, true},
		{"입력의 제로폭 문자는 낱말을 가르지 않는다", "Bash", "git com\u200bmit", commit, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exact, ok := c.m.TriggeredByInput(c.tool, c.input)
			if exact != c.exact || ok != c.ok {
				t.Fatalf("TriggeredByInput(%q, %q) = %v %v, want %v %v", c.tool, c.input, exact, ok, c.exact, c.ok)
			}
		})
	}
}

func TestTrigger_입력_조건을_다듬어_남기고_틀린_꼴은_거절한다(t *testing.T) {
	got, err := cleanTools([]string{"Bash( git   commit )", "Bash(git commit)", "Bash(git\u00a0push)", "Edit(package.json)", "Bash"})
	if err != nil || !slices.Equal(got, []string{"Bash(git commit)", "Bash(git push)", "Edit(package.json)", "Bash"}) {
		t.Fatalf("cleanTools = %q %v", got, err)
	}
	for _, bad := range []string{"mcp__docs__*(x)", "Bash(git (commit))", "Bash(git commit", "(git commit)", "Bash(a\nb)", "Bash(" + strings.Repeat("a", maxToolCondRunes+1) + ")"} {
		if _, err := cleanTools([]string{bad}); err == nil {
			t.Errorf("cleanTools accepted %q", bad)
		}
	}
}

// 받아들여도 영영 맞지 않는 조건은 저장할 때 까닭과 올바른 꼴을 알려 거절한다: 낱말이 없는 조건, *를 쓴 조건(조건 낱말은
// 사이를 건너뛰어 맞으므로 *가 필요 없다), ; & |로 명령 여럿을 이은 조건(조건은 명령 하나 안에서 맞춘다), 보이지 않는 글자가 든 조건.
func TestTrigger_영영_맞지_않는_조건은_까닭과_함께_거절한다(t *testing.T) {
	i18n.Set(i18n.KO)
	defer i18n.Set(i18n.EN)
	for _, c := range []struct{ cond, why string }{
		{"Bash()", "낱말이 없어"}, {"Bash(  )", "낱말이 없어"}, {"Bash(;)", "낱말이 없어"}, {"Bash(/)", "낱말이 없어"}, {`Bash("")`, "낱말이 없어"},
		{"Bash(git commit:*)", "*를 쓰지 않습니다"}, {"Bash(git commit *)", "*를 쓰지 않습니다"},
		{"Bash(git add && git commit)", "; & |"}, {"Bash(git log | head)", "; & |"},
		{"Bash(git\u200bcommit)", "보이지 않는 글자"}, {"Bash(git commit\ufeff)", "보이지 않는 글자"},
	} {
		_, err := cleanTools([]string{c.cond})
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err %v", c.cond, err)
			continue
		}
		// 보이지 않는 글자는 지우라는 것이 올바른 꼴이고, 나머지는 예시 꼴을 함께 보인다.
		if msg := err.Error(); !strings.Contains(msg, c.why) || c.why != "보이지 않는 글자" && !strings.Contains(msg, "Bash(git commit)") {
			t.Errorf("%q: message %q, want the reason %q and the right form", c.cond, msg, c.why)
		}
	}
}

// 도구 입력 점수(명세 T-1)는 요청 회상의 점수 함수다. 명령의 한 글자 플래그·숫자(-n, =1)는 접두 토큰으로 본문의 「nginx」나 날짜
// 숫자 같은 낱말에 우연히 맞으므로 질의에 넣지 않는다. 입력과 낱말을 나누지 않는 기억은 점수가 없다.
func TestToolInputScores_관련_있는_기억만_점수를_받고_한_글자_플래그는_세지_않는다(t *testing.T) {
	ms := []Memory{
		{ID: "jq", Title: "jq 필터가 비면 입력 전체를 그대로 낸다", Body: "빈 필터는 점 하나로 적는다"},
		{ID: "web", Title: "nginx 설정을 바꾸면 다시 읽기 전에 문법을 검사한다", Body: "2025-03-14 점검 회의에서 정했다"},
	}
	got := ToolInputScores(ms, "jq -n foo data")
	if got["jq"] < 0.9 || got["web"] != 0 {
		t.Fatalf("jq call scores %v", got)
	}
	if got := ToolInputScores(ms, "go test ./internal/inject -count=1"); len(got) != 0 {
		t.Fatalf("unrelated command scores %v", got)
	}
	if got := ToolInputScores(ms, "  "); got != nil {
		t.Fatalf("blank input scores %v", got)
	}
}

func TestSave_입력_조건이_달린_도구_트리거를_남긴다(t *testing.T) {
	s := supStore(t)
	in := userSays(KindRule, "커밋 전에 시험을 돌린다", "커밋 전 시험")
	in.Tools = []string{"Bash(git commit)"}
	m, _ := mustSave(t, s, in)
	got, err := s.Get(m.ID)
	if err != nil || !slices.Equal(got.Tools, []string{"Bash(git commit)"}) {
		t.Fatalf("tools %v %v", got.Tools, err)
	}
}
