package host_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// host call(과 그 옛 이름인 tool 명령)의 memory_save가 서버가 직접 읽는 파일은 요청의 cwd가 속한 git 작업 트리의 최상위 아래뿐이다.
// cwd는 호출자가 고르므로 git 밖 폴더(점 폴더·홈 아래 폴더)를 주면 읽지 않는다. 세션이 이미 시작되어 활동 줄에 프로젝트가 있는
// 세션(그 줄의 값을 git 없이 다시 쓰는 길)에서도 저장소 최상위가 루트다.

const hostLine = "deploy: ./scripts/release.sh"

func hostFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(hostLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// verified saves a verified fact whose evidence is file from the session folder cwd (a session that started there
// when start is true) and says whether the server's own check held: the user's words were replaced and the new
// memory confirmed at once.
func verified(t *testing.T, cwd, file string, start bool) bool {
	t.Helper()
	e := newEnv(t) // 호출마다 새 저장소: 앞 호출이 남긴 기억이 판정에 끼지 않게
	s := e.store()
	pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal}) // 빈 세션은 활동 줄을 남기지 않는다
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포는 make deploy", Topic: "배포 명령", Scope: "project:github.com/me/app",
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "배포는 make deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	a := e.adapter("c-" + old.ID)
	a.cwd = cwd
	if start {
		a.recall(host.OpContext, nil)
		if _, ok := s.SessionProject("host-x", "c-"+old.ID); !ok {
			t.Fatal("세션의 활동 줄이 없다")
		}
	}
	args, _ := json.Marshal(map[string]any{"kind": "fact", "title": "배포는 release.sh " + old.ID, "topic": "배포 명령", "appliesTo": "repo:github.com/me/app",
		"source": map[string]any{"kind": "verification", "ref": file, "excerpt": hostLine}})
	out, herr := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(args)})
	if herr != nil {
		t.Fatalf("save: %v", herr)
	}
	r := out.(host.CallOut).Result.(mcpserver.SaveOut)
	replaced := slices.Contains(r.Superseded, old.ID)
	if confirmed := r.Memory.State == memory.StateConfirmed; replaced != confirmed {
		t.Errorf("대체 %v 확정 %v", replaced, confirmed)
	}
	return replaced
}

func TestCall_memory_save의_재확인_루트는_cwd가_속한_작업_트리_최상위다(t *testing.T) {
	repo := gitRepo(t, "git@github.com:me/app.git")
	home := filepath.Join(filepath.Dir(repo), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// git의 설정 환경 변수(GIT_CONFIG_*)는 쓰지 않는다: 하나라도 있으면 세션의 값을 다시 쓰지 않고 git에 묻는다. 가짜 홈과
	// 시스템 설정 끄기로 사용자 설정에 기대지 않는다.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	top := hostFile(t, filepath.Join(repo, "Makefile"))
	sub := filepath.Join(repo, "pkg", "sub")
	hostFile(t, filepath.Join(sub, "run.sh"))
	hostFile(t, filepath.Join(repo, ".env"))
	aws := hostFile(t, filepath.Join(home, ".aws", "credentials"))
	work := hostFile(t, filepath.Join(home, "work", "notes"))
	for _, start := range []bool{false, true} { // 시작한 세션은 활동 줄의 프로젝트를 git 없이 다시 쓴다
		if !verified(t, repo, top, start) {
			t.Errorf("시작 %v: 저장소 최상위에서 연 세션이 재확인하지 못했다", start)
		}
		if !verified(t, sub, top, start) {
			t.Errorf("시작 %v: 저장소 하위 폴더에서 연 세션이 최상위 파일을 재확인하지 못했다", start)
		}
		if verified(t, repo, filepath.Join(repo, ".env"), start) {
			t.Errorf("시작 %v: 점 파일을 재확인했다", start)
		}
		if verified(t, filepath.Join(home, ".aws"), aws, start) {
			t.Errorf("시작 %v: git 밖 점 폴더를 cwd로 줬는데 재확인했다", start)
		}
		if verified(t, filepath.Join(home, "work"), work, start) {
			t.Errorf("시작 %v: 홈 아래 git 밖 폴더를 cwd로 줬는데 재확인했다", start)
		}
	}
}
