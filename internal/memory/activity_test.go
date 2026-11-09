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
