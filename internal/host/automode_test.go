package host_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// host call의 memory_save는 부를 때마다 읽은 설정의 자동 모드(명세 C-9)로 대체를 가른다: 근거만 적힌 검증 사실은 켬이면
// 사용자 말 사실을 대체하고, 끔이면 어긋남으로 둔다.
func TestCall_memory_save는_부를_때의_자동_모드를_따른다(t *testing.T) {
	e := newEnv(t)
	s := e.store()
	a := e.adapter("c-1")
	saveStated := func(title, topic string) mcpserver.SaveOut {
		t.Helper()
		args, _ := json.Marshal(map[string]any{"kind": "fact", "appliesTo": "product:app", "title": title, "topic": topic,
			"source": map[string]any{"kind": "verification", "ref": "go test ./...", "excerpt": "ok  example.com/app  0.2s"}})
		out, err := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(args)})
		if err != nil {
			t.Fatal(err)
		}
		return out.(host.CallOut).Result.(mcpserver.SaveOut)
	}
	userFact := func(title, topic string) memory.Memory {
		return save(t, s, memory.SaveInput{Kind: memory.KindFact, Title: title, Topic: topic, Scope: "product:app"})
	}
	on := userFact("시험은 make test", "시험 명령")
	e.cfg.AutoMode = true
	if out := saveStated("시험은 go test", "시험 명령"); !slices.Contains(out.Superseded, on.ID) {
		t.Fatalf("켬: %+v", out.MemoryOut)
	}
	off := userFact("빌드는 make all", "빌드 명령")
	e.cfg.AutoMode = false
	if out := saveStated("빌드는 go build", "빌드 명령"); !slices.Contains(out.Conflicts, off.ID) || slices.Contains(out.Superseded, off.ID) {
		t.Fatalf("끔: %+v", out.MemoryOut)
	}
}

// host call의 memory_save가 서버가 근거 파일을 다시 확인한 검증 사실을 받아도, 자동 모드 끔이면 같은 범위·주제의 사용자 말
// 사실을 대체하지 않고 어긋남으로 남긴다(다시 확인했는지는 어긋남이 없을 때의 즉시 확정만 가른다).
func TestCall_memory_save는_다시_확인된_근거도_자동_모드_끔이면_사용자_말_사실을_대체하지_않는다(t *testing.T) {
	repo := gitRepo(t, "git@github.com:me/app.git")
	home := filepath.Join(filepath.Dir(repo), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	file := hostFile(t, filepath.Join(repo, "Makefile"))
	e := newEnv(t)
	e.cfg.AutoMode = false
	s := e.store()
	old := save(t, s, memory.SaveInput{Kind: memory.KindFact, Title: "배포는 make deploy", Topic: "배포 명령", Scope: "project:github.com/me/app"})
	a := e.adapter("c-recheck")
	a.cwd = repo
	saveVerified := func(title, topic string) mcpserver.SaveOut {
		t.Helper()
		args, _ := json.Marshal(map[string]any{"kind": "fact", "title": title, "topic": topic, "appliesTo": "repo:github.com/me/app",
			"source": map[string]any{"kind": "verification", "ref": file, "excerpt": hostLine}})
		out, herr := a.do(host.OpCall, map[string]any{"name": "memory_save", "args": json.RawMessage(args)})
		if herr != nil {
			t.Fatalf("save: %v", herr)
		}
		return out.(host.CallOut).Result.(mcpserver.SaveOut)
	}
	// 대조군: 어긋남이 없는 주제는 서버가 파일을 읽어 확인해 즉시 확정된다(이 시험에서 재확인이 실제로 일어난다).
	if c := saveVerified("릴리스 점검은 release.sh", "릴리스 점검"); c.Memory.State != memory.StateConfirmed {
		t.Fatalf("재확인이 일어나지 않았다: %s", c.Memory.State)
	}
	r := saveVerified("배포는 release.sh", "배포 명령")
	if !slices.Contains(r.Conflicts, old.ID) || slices.Contains(r.Superseded, old.ID) {
		t.Fatalf("어긋남으로 남지 않았다: %+v", r.MemoryOut)
	}
	if r.Memory.State != memory.StateCandidate {
		t.Fatalf("새 사실이 확정됐다: %s", r.Memory.State)
	}
	if got, err := s.Get(old.ID); err != nil || got.State != memory.StateConfirmed || got.SupersededBy != "" {
		t.Fatalf("옛 사실이 바뀌었다: %+v %v", got, err)
	}
}
