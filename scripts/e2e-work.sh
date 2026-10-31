#!/bin/sh
# 작업 그래프 호스트 실행 끝 기준(설계 · Orbit 2차 「끝 기준」)을 새 헤드리스 세션으로 확인한다.
# claude 세션은 이 체크아웃의 orb와 플러그인을 빈 ORBIT_HOME으로 붙이고, 설치된 OpenOrbit 플러그인은 그 세션에서 끈다.
# 호스트 계정의 사용량이 든다.
#
#   scripts/e2e-work.sh [작업 디렉터리]          # claude 세션 A·B
#   E2E_CODEX=1 scripts/e2e-work.sh [디렉터리]   # codex 세션 C도(설치·신뢰한 같은 판 플러그인: 아래 안내)
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
E=${1:-$(mktemp -d)}
mkdir -p "$E"
E=$(cd "$E" && pwd -P)
model=${E2E_MODEL:-sonnet}

go build -o "$E/bin/orb" "$root/cmd/orb"
"$E/bin/orb" plugin build "$root/internal/orbitplugin/source" --out "$E/market" --var "orb=$E/bin/orb" --var "orbcmd=$E/bin/orb" >/dev/null
echo '{"enabledPlugins":{"orbit@orbit":false}}' >"$E/settings.json"

repo() { # $1: 디렉터리
	rm -rf "$1" && mkdir -p "$1" && cd "$1"
	git init -q && git config user.email e2e@example.com && git config user.name e2e
	printf 'module e2e\n\ngo 1.27\n' >go.mod
	printf 'package e2e\n\nfunc Add(a, b int) int { return a + b }\n' >calc.go
	cat >calc_test.go <<'EOT'
package e2e

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}

func TestBroken(t *testing.T) {
	t.Fatal("일부러 실패하는 시험")
}
EOT
	git add go.mod calc.go calc_test.go && git commit -qm init
}

claude_run() { # $1: 이름, $2: 프롬프트, 나머지: claude 인자
	name=$1 prompt=$2
	shift 2
	mkdir -p "$E/$name/home"
	repo "$E/$name/repo"
	# --allowedTools는 값을 여럿 받아 뒤의 인자를 삼키므로 프롬프트는 표준 입력으로 준다
	printf '%s' "$prompt" | ORBIT_HOME="$E/$name/home" claude -p --model "$model" --settings "$E/settings.json" --plugin-dir "$E/market/plugins/orbit" \
		--output-format stream-json --verbose "$@" >"$E/claude-$name.jsonl" 2>"$E/claude-$name.err"
}

claude_run a "작업 목록 도구(TaskCreate·TaskUpdate)로 단계를 만들고 진행하세요. 단계 셋: 'calc.go 읽기', 'calc_test.go 읽기', '두 파일 요약'. 셋째는 첫째와 둘째를 선행(blockedBy)으로 둡니다. 그다음 차례로 각 단계를 진행으로 바꾸고 해당 파일을 Read로 읽은 뒤 완료로 바꿉니다. 마지막 단계에서는 두 파일을 한 줄로 요약하고 완료로 바꿉니다." --allowedTools Read mcp__plugin_orbit_orbit__work_title

claude_run b "다음을 순서대로 하세요.
1) TaskCreate로 단계 하나 'calc 시험 정리'를 만들고 진행(in_progress)으로 바꿉니다.
이 저장소는 시험용이라 기본 브랜치에 바로 커밋해도 됩니다. 셸 명령은 하나씩 따로 실행합니다(묶지 않음).
2) Agent 도구로 subagent_type 'orbit:worker' 서브에이전트를 run_in_background: true로 띄웁니다. 맡길 일: 이 저장소에서 'go test -run TestAdd ./...'(성공해야 함)과 'go test ./...'(TestBroken 때문에 실패해야 함)을 차례로 실행하고, calc_test.go에서 TestBroken 함수를 지운 뒤 'git add calc_test.go'와 'git commit -m \"test: Remove the broken test\"'로 커밋하고 커밋 sha를 보고. 서브에이전트가 끝날 때까지 기다리고 결과를 확인합니다.
3) Agent 도구로 subagent_type 'orbit:gate' 서브에이전트를 띄워 다음 완료 주장을 감사하게 합니다: '이 저장소에는 곱셈 함수 Mul과 그 시험 TestMul이 있고 모든 시험이 통과한다.' (실제로는 Mul이 없으므로 판정을 있는 그대로 받습니다.)
4) calc.go 맨 위에 '// Package e2e is a tiny calculator.' 주석을 넣고 'git add calc.go'와 'git commit -m \"docs: Describe the package\"'로 커밋합니다.
5) 단계를 완료(completed)로 바꿉니다. 마지막에 서브에이전트 커밋 sha와 gate의 전체 판정을 한 줄로 알려 주세요." \
	--allowedTools "Bash(go:*)" "Bash(git:*)" "Bash(cat:*)" "Bash(echo:*)" "Bash(head:*)" "Bash(grep:*)" "Bash(ls:*)" \
	Read Edit Write mcp__plugin_orbit_orbit__work_title --permission-mode acceptEdits

if [ "${E2E_CODEX:-}" = 1 ]; then
	# Codex는 플러그인 훅을 /hooks에서 사용자가 신뢰해야 돌고, 신뢰는 플러그인 이름과 훅 내용에 묶인다.
	# 그래서 세션 C는 이 마켓이 아니라 이 PC에 설치해 신뢰한 OpenOrbit 플러그인(orb install)으로 돈다.
	# 설치본 orb가 이 체크아웃과 같은 판이어야 이 체크아웃을 확인한 것이 된다.
	want=$("$E/bin/orb" version)
	got=$(orb version 2>/dev/null || true)
	if [ "$got" != "$want" ]; then
		echo "설치된 orb($got)가 이 체크아웃($want)과 달라 Codex 세션을 건너뜁니다. 이 판을 orb install로 설치하고 codex /hooks에서 OpenOrbit 훅을 신뢰한 뒤 다시 돌립니다." >&2
	else
		mkdir -p "$E/c/home"
		repo "$E/c/repo"
		ORBIT_HOME="$E/c/home" codex exec --json -s workspace-write -c 'tools.update_plan.enabled=true' \
			"update_plan 도구로 세 단계 계획('calc.go 읽기','calc_test.go 읽기','두 파일 요약')을 만들고, 단계마다 진행으로 바꾼 뒤 일을 하고 완료로 바꾸세요. 첫 계획을 만든 뒤 붙는 OpenOrbit 안내가 있으면 따르세요." \
			</dev/null >"$E/codex-c.jsonl" 2>"$E/codex-c.err"
	fi
fi

cd "$root"
ORBIT_E2E_DIR="$E" go test ./internal/work/ -run E2E -count=1 -v
echo "기록: $E"
