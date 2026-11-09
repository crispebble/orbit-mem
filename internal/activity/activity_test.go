package activity

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

func store(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// 활동 기록을 최신순으로 주고, 질의(요청 글·프로젝트)·호스트·프로젝트·시각·개수로 거른다.
// 옛 작업 기록(work.db)은 더 읽지 않는다(2026-11-08, ORBIT-13).
func TestFind_활동_기록을_최신순으로_거른다(t *testing.T) {
	s := store(t)
	for _, e := range []memory.ActivityEvent{
		{Kind: memory.ActivityPrompt, Host: "claude", Session: "A", Cwd: "/w/a", ProjectScope: "project:a", ProjectLabel: "github.com/x/a", Launcher: "human", Prompt: "로그인 고치기", At: 1000},
		{Kind: memory.ActivityPrompt, Host: "codex", Session: "B", Cwd: "/w/b", ProjectScope: "project:b", ProjectLabel: "github.com/x/b", Launcher: "human", Prompt: "배포", At: 3000},
		{Kind: memory.ActivityPrompt, Host: "claude", Session: "C", Cwd: "/w/a", ProjectScope: "project:a", ProjectLabel: "github.com/x/a", Launcher: "human", Prompt: "다시 로그인", At: 6000},
	} {
		if err := s.RecordActivity(e); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Find(s, Query{})
	if err != nil || len(res.Items) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if r := res.Items[0]; r.Session != "C" || r.Source != SourceActivity || r.Prompts != 1 || r.LastAt != 6000 || r.Project != "github.com/x/a" {
		t.Fatalf("첫 줄 %+v", r)
	}
	for name, c := range map[string]struct {
		q    Query
		want []string
	}{
		"질의":    {Query{Query: "로그인"}, []string{"C", "A"}},
		"호스트":   {Query{Host: "codex"}, []string{"B"}},
		"프로젝트":  {Query{ProjectScope: "project:a"}, []string{"C", "A"}},
		"since": {Query{Since: 2500}, []string{"C", "B"}},
		"limit": {Query{Limit: 1}, []string{"C"}},
	} {
		res, err := Find(s, c.q)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range res.Items {
			got = append(got, r.Session)
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: %v, 기대 %v", name, got, c.want)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"":                          {},
		"2026-10-30":                time.Date(2026, 10, 30, 0, 0, 0, 0, time.Local),
		"2026-10-30T10:00:00+09:00": time.Date(2026, 10, 30, 10, 0, 0, 0, time.FixedZone("", 9*3600)),
		"3d":                        now.Add(-72 * time.Hour),
		"6h":                        now.Add(-6 * time.Hour),
	} {
		got, err := ParseSince(in, now)
		w := int64(0)
		if !want.IsZero() {
			w = want.UnixMilli()
		}
		if err != nil || got != w {
			t.Errorf("%q: %d %v, 기대 %d", in, got, err, w)
		}
	}
	if _, err := ParseSince("어제", now); err == nil {
		t.Error("모르는 꼴")
	}
}
