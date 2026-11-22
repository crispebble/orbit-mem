package memory

import (
	"slices"
	"testing"
)

// 입력 조건(명세 T-2): tools 칸의 Bash(git commit)는 Bash 호출의 입력에 그 낱말이 이어서 나올 때만 걸린다. 낱말은 빈칸·따옴표·
// 셸 기호·경로 구분자로 나누고 대소문자를 가리지 않는다. 입력이 없으면(호스트가 주지 않음) 도구 이름만으로 판정한다(T-4).
func TestTriggeredByInput_조건_낱말이_입력에_이어서_나올_때만_걸린다(t *testing.T) {
	commit := Memory{Tools: []string{"Bash(git commit)"}}
	cases := []struct {
		name, tool, input string
		m                 Memory
		exact, ok         bool
	}{
		{"조건 낱말 그대로", "Bash", "git commit -m x", commit, true, true},
		{"빈칸이 둘이어도 낱말은 같다", "Bash", "git  commit", commit, true, true},
		{"앞 명령 뒤에 이어 와도 된다", "Bash", "cd /w && git commit -am x", commit, true, true},
		{"따옴표 안의 낱말도 낱말이다", "Bash", `echo "git commit"`, commit, true, true},
		{"대소문자를 가리지 않는다", "Bash", "GIT Commit", commit, true, true},
		{"다른 명령에는 걸리지 않는다", "Bash", "ls -la", commit, false, false},
		{"commit-tree는 commit이 아니다", "Bash", "git commit-tree HEAD^{tree}", commit, false, false},
		{"낱말이 떨어져 있으면 걸리지 않는다", "Bash", "git -C /w commit", commit, false, false},
		{"다른 도구에는 걸리지 않는다", "Edit", "git commit", commit, false, false},
		{"입력이 없으면 이름만으로 판정한다", "Bash", "", commit, true, true},
		{"입력이 빈칸뿐이면 없는 것이다", "Bash", "  ", commit, true, true},
		{"조건 여럿은 하나만 맞아도 된다", "Bash", "git push origin x", Memory{Tools: []string{"Bash(git commit)", "Bash(git push)"}}, true, true},
		{"조건 여럿이 모두 안 맞으면 안 걸린다", "Bash", "git status", Memory{Tools: []string{"Bash(git commit)", "Bash(git push)"}}, false, false},
		{"조건 없는 이름은 입력과 관계없다", "Bash", "ls", Memory{Tools: []string{"Bash"}}, true, true},
		{"접두는 입력과 관계없다", "mcp__atlas__write_page", "x", Memory{Tools: []string{"mcp__atlas__*"}}, false, true},
		{"경로 조건은 파일 이름 낱말에 맞는다", "Edit", "/w/app/package.json", Memory{Tools: []string{"Edit(package.json)"}}, true, true},
		{"경로 조건은 다른 파일에 맞지 않는다", "Edit", "/w/app/package.json.bak", Memory{Tools: []string{"Edit(package.json)"}}, false, false},
		{"경로 조건은 폴더 낱말로도 쓴다", "Edit", "/w/web/src/app.ts", Memory{Tools: []string{"Edit(web/src)"}}, true, true},
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

// 옛 판정(TriggeredBy, 입력 없음)은 조건이 달린 트리거에 걸리지 않는다: 옛 실행 파일이 이 꼴을 읽을 때와 같다.
func TestTriggeredBy_조건이_달린_트리거는_옛_판정에_걸리지_않는다(t *testing.T) {
	if _, ok := (Memory{Tools: []string{"Bash(git commit)"}}).TriggeredBy("Bash"); ok {
		t.Fatal("TriggeredBy matched a conditioned trigger")
	}
}

func TestTrigger_입력_조건을_다듬어_남기고_틀린_꼴은_거절한다(t *testing.T) {
	got, err := cleanTools([]string{"Bash( git   commit )", "Bash(git commit)", "Edit(package.json)", "Bash"})
	if err != nil || !slices.Equal(got, []string{"Bash(git commit)", "Edit(package.json)", "Bash"}) {
		t.Fatalf("cleanTools = %v %v", got, err)
	}
	for _, bad := range []string{"Bash()", "Bash(  )", "mcp__atlas__*(x)", "Bash(git (commit))", "Bash(git commit", "(git commit)", "Bash(a\nb)", "Bash(" + longCond() + ")"} {
		if _, err := cleanTools([]string{bad}); err == nil {
			t.Errorf("cleanTools accepted %q", bad)
		}
	}
}

// longCond is one condition over maxToolCondRunes.
func longCond() string {
	b := make([]rune, maxToolCondRunes+1)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// 도구 입력 점수(명세 T-1)는 요청 회상의 점수 함수다. 명령의 한 글자 플래그·숫자(-n, =1)는 접두 토큰으로 「node」「16」 같은
// 낱말에 우연히 맞으므로 질의에 넣지 않는다. 입력과 낱말을 나누지 않는 기억은 점수가 없다.
func TestToolInputScores_관련_있는_기억만_점수를_받고_한_글자_플래그는_세지_않는다(t *testing.T) {
	ms := []Memory{
		{ID: "rg", Title: "백그라운드 셸에서 rg 명령은 검색 경로를 꼭 준다", Body: "경로가 없으면 표준 입력을 기다린다"},
		{ID: "dom", Title: "happy-dom 요소를 assert에 넘기면 메모리가 터진다", Body: "2026-11-16 node 시험에서 겪었다"},
	}
	got := ToolInputScores(ms, "rg -n foo internal")
	if got["rg"] < 0.9 || got["dom"] != 0 {
		t.Fatalf("rg call scores %v", got)
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
