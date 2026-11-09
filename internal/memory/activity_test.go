package memory

import (
	"errors"
	"path/filepath"
	"testing"
)

func activityStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// 세션 끝은 있는 줄의 끝 시각·마지막 시각만 고친다: 프로젝트·요청·시작 시각은 그대로다(프로젝트 판정이 필요 없다).
func TestEndActivity_있는_줄은_끝_시각만_고친다(t *testing.T) {
	s := activityStore(t)
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S1", Cwd: "/w/a", ProjectScope: "project:/w/a", ProjectLabel: "/w/a", Parent: "claude:P0", At: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityPrompt, Host: "claude", Session: "S1", Prompt: "첫 요청", At: 2000}); err != nil {
		t.Fatal(err)
	}
	found, err := s.EndActivity(ActivityEvent{Kind: ActivityEnd, Host: "claude", Session: "S1", Cwd: "/w/a", At: 3000})
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	as, _ := s.Activities(ActivityFilter{})
	if len(as) != 1 {
		t.Fatalf("%+v", as)
	}
	a := as[0]
	if a.EndedAt != 3000 || a.LastAt != 3000 || a.StartedAt != 1000 || a.Prompts != 1 || a.FirstPrompt != "첫 요청" ||
		a.ProjectScope != "project:/w/a" || a.ProjectLabel != "/w/a" || a.Cwd != "/w/a" || a.Parent != "claude:P0" || a.Launcher != "human" {
		t.Fatalf("%+v", a)
	}
	// 늦게 온 끝(시계가 뒤)은 마지막 시각을 되돌리지 않는다
	if found, err := s.EndActivity(ActivityEvent{Kind: ActivityEnd, Host: "claude", Session: "S1", At: 2500}); err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if as, _ = s.Activities(ActivityFilter{}); as[0].LastAt != 3000 || as[0].EndedAt != 2500 || as[0].Cwd != "/w/a" {
		t.Fatalf("%+v", as[0])
	}
	// 끝난 줄은 다음 시작(재개)이 다시 연다
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S1", At: 4000}); err != nil {
		t.Fatal(err)
	}
	if as, _ = s.Activities(ActivityFilter{}); as[0].EndedAt != 0 {
		t.Fatalf("%+v", as[0])
	}
}

// 줄이 없으면(시작 훅이 빠진 세션) 아무것도 쓰지 않고 「없음」을 알린다: 호출자가 프로젝트를 판정해 새로 쓴다.
// 같은 열쇠의 다른 호스트 줄은 고치지 않는다.
func TestEndActivity_줄이_없으면_없음을_알린다(t *testing.T) {
	s := activityStore(t)
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "codex", Session: "S1", At: 1000}); err != nil {
		t.Fatal(err)
	}
	found, err := s.EndActivity(ActivityEvent{Kind: ActivityEnd, Host: "claude", Session: "S1", At: 2000})
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	as, _ := s.Activities(ActivityFilter{})
	if len(as) != 1 || as[0].Host != "codex" || as[0].EndedAt != 0 {
		t.Fatalf("%+v", as)
	}
	if _, err := s.EndActivity(ActivityEvent{Kind: ActivityEnd, Host: "claude"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("세션 없는 끝 = %v", err)
	}
}

// 세션 안의 op가 git 없이 다시 쓰는 프로젝트(0.17 r0): 열린 줄의 폴더·키·라벨이다. 끝난 줄은 끝이 폴더만 고쳤을 수 있어
// (EndActivity는 프로젝트를 그대로 둔다) 폴더와 키가 한 판정에서 온 짝이 아닐 수 있다: 주지 않는다. 키 없는 줄도 주지 않는다.
func TestSessionProject_열린_줄의_폴더와_프로젝트(t *testing.T) {
	s := activityStore(t)
	if _, ok := s.SessionProject("claude", "S1"); ok {
		t.Fatal("줄이 없는데 줬다")
	}
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S1", Cwd: "/w/a", ProjectScope: "project:/w/a", ProjectLabel: "/w/a", At: 1000}); err != nil {
		t.Fatal(err)
	}
	if a, ok := s.SessionProject("claude", "S1"); !ok || a.Cwd != "/w/a" || a.ProjectScope != "project:/w/a" || a.ProjectLabel != "/w/a" {
		t.Fatalf("%+v %v", a, ok)
	}
	if _, ok := s.SessionProject("codex", "S1"); ok {
		t.Fatal("다른 호스트의 줄을 줬다")
	}
	if _, err := s.EndActivity(ActivityEvent{Kind: ActivityEnd, Host: "claude", Session: "S1", Cwd: "/w/b", At: 2000}); err != nil {
		t.Fatal(err)
	}
	if a, ok := s.SessionProject("claude", "S1"); ok {
		t.Fatalf("끝난 줄을 줬다 %+v", a)
	}
	// 재개(시작)는 줄을 다시 연다
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S1", Cwd: "/w/b", ProjectScope: "project:/w/b", ProjectLabel: "/w/b", At: 3000}); err != nil {
		t.Fatal(err)
	}
	if a, ok := s.SessionProject("claude", "S1"); !ok || a.Cwd != "/w/b" || a.ProjectScope != "project:/w/b" {
		t.Fatalf("%+v %v", a, ok)
	}
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S2", At: 1000}); err != nil {
		t.Fatal(err)
	}
	if a, ok := s.SessionProject("claude", "S2"); ok {
		t.Fatalf("키 없는 줄을 줬다 %+v", a)
	}
}

// J-1(0.17 r6): 프로젝트를 모르는 호출(git이 답하지 못함)은 줄의 프로젝트를 비운다: 재개한 세션의 옛 키가 새 폴더와
// 짝지어 다시 쓰이지 않고(SessionProject가 주지 않는다), 다음 호출이 다시 판정한다. 폴더는 남긴다.
func TestRecordActivity_프로젝트를_모르면_줄의_프로젝트를_비운다(t *testing.T) {
	s := activityStore(t)
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S1", Cwd: "/w/a", ProjectScope: "project:/w/a", ProjectLabel: "/w/a", At: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S1", Cwd: "/w/b", ProjectScope: "project:/w/b", ProjectLabel: "/w/b", ProjectUnknown: true, At: 2000}); err != nil {
		t.Fatal(err)
	}
	if a, ok := s.SessionProject("claude", "S1"); ok {
		t.Fatalf("모르는 프로젝트의 줄을 줬다 %+v", a)
	}
	as, _ := s.Activities(ActivityFilter{})
	if len(as) != 1 || as[0].ProjectScope != "" || as[0].ProjectLabel != "" || as[0].Cwd != "/w/b" {
		t.Fatalf("%+v", as)
	}
	// 프로젝트가 빈 이벤트(폴더 없는 요청)는 전처럼 있던 것을 둔다
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityPrompt, Host: "claude", Session: "S1", Cwd: "/w/b", ProjectScope: "project:/w/b", ProjectLabel: "/w/b", Prompt: "x", At: 3000}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityPrompt, Host: "claude", Session: "S1", Prompt: "y", At: 4000}); err != nil {
		t.Fatal(err)
	}
	if a, ok := s.SessionProject("claude", "S1"); !ok || a.ProjectScope != "project:/w/b" {
		t.Fatalf("%+v %v", a, ok)
	}
}

// 프로젝트 없는 줄(시작이 프로젝트를 몰랐다)의 끝은 줄이 없을 때처럼 「없음」을 알린다: 호출자가 프로젝트를 판정해 쓴다.
func TestEndActivity_프로젝트_없는_줄은_없음을_알린다(t *testing.T) {
	s := activityStore(t)
	if err := s.RecordActivity(ActivityEvent{Kind: ActivityStart, Host: "claude", Session: "S1", Cwd: "/w/a", At: 1000}); err != nil {
		t.Fatal(err)
	}
	found, err := s.EndActivity(ActivityEvent{Kind: ActivityEnd, Host: "claude", Session: "S1", Cwd: "/w/a", At: 2000})
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if as, _ := s.Activities(ActivityFilter{}); len(as) != 1 || as[0].EndedAt != 0 {
		t.Fatalf("%+v", as)
	}
}
