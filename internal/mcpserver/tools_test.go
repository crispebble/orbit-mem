package mcpserver

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/activity"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// host 계약 v1(ORBIT-23 a3, T-25): 호스트가 CLI로 부르는 도구에 activity_find가 든다. 이름은 orbit-mem 이름뿐이다.
func TestHostTools_activity_find를_받고_다른_이름은_받지_않는다(t *testing.T) {
	want := []string{"memory_find", "memory_get", "memory_save", "memory_update", "activity_find"}
	if !slices.Equal(HostTools, want) {
		t.Fatalf("HostTools = %v", HostTools)
	}
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := NewTools(s, Options{}).Call("memory_search", []byte(`{}`), "host-x", "s-1"); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("memory_search = %v", err)
	}
}

// activity_find도 다른 도구처럼 그 호스트·세션의 tool.activity_find 사건을 남긴다(결과 수와 세션 id).
func TestCall_activity_find는_활동을_찾고_사건을_남긴다(t *testing.T) {
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, sess := range []string{"a-1", "a-2"} {
		if err := s.RecordActivity(memory.ActivityEvent{Kind: memory.ActivityStart, Host: "host-x", Session: sess, ProjectScope: "project:a", Launcher: "human", At: 1}); err != nil {
			t.Fatal(err)
		}
	}
	tools := NewTools(s, Options{Project: "project:a"})
	out, err := tools.Call("activity_find", []byte(`{"project":"all","query":"a-"}`), "host-x", "s-1")
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(activity.Result)
	if !ok || len(res.Items) != 2 {
		t.Fatalf("result = %#v", out)
	}
	if _, err := tools.Call("activity_find", []byte(`{"project":"bogus"}`), "host-x", "s-1"); err == nil {
		t.Fatal("잘못된 project는 도구 오류")
	}
	evs, _ := s.Events(memory.EventFilter{FromDay: "2000-01-01", ToDay: "2999-12-31", KindPrefix: memory.EventToolPrefix})
	if len(evs) != 2 || evs[0].Kind != "tool.activity_find" || evs[0].Host != "host-x" || evs[0].Session != "s-1" || evs[0].Scope != "project:a" {
		t.Fatalf("events = %+v", evs)
	}
	var d struct {
		Results int      `json:"results"`
		IDs     []string `json:"ids"`
		OK      bool     `json:"ok"`
		Project string   `json:"project"`
	}
	_ = json.Unmarshal(evs[0].Data, &d)
	slices.Sort(d.IDs)
	if d.Results != 2 || !slices.Equal(d.IDs, []string{"a-1", "a-2"}) || !d.OK || d.Project != "all" {
		t.Fatalf("data = %s", evs[0].Data)
	}
	var bad struct{ OK bool }
	if _ = json.Unmarshal(evs[1].Data, &bad); bad.OK {
		t.Fatalf("실패 사건 = %s", evs[1].Data)
	}
}
