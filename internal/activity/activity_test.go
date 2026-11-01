package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/sqlitedb"
)

// legacyDB writes a work.db with the columns the old work record kept (sessions and works).
func legacyDB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "work.db")
	db, err := sqlitedb.Open(p, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE sessions (host TEXT NOT NULL, id TEXT NOT NULL, surface TEXT NOT NULL DEFAULT 'unknown', cwd TEXT NOT NULL DEFAULT '',
		  project_scope TEXT NOT NULL DEFAULT '', project_label TEXT NOT NULL DEFAULT '', started_at INTEGER NOT NULL, last_seen INTEGER NOT NULL,
		  ended_at INTEGER NOT NULL DEFAULT 0, launcher TEXT NOT NULL DEFAULT '', PRIMARY KEY(host, id))`,
		`CREATE TABLE works (id TEXT PRIMARY KEY, host TEXT NOT NULL, session TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', opened_at INTEGER NOT NULL)`,
		`INSERT INTO sessions(host,id,cwd,project_scope,project_label,started_at,last_seen,ended_at,launcher) VALUES
		  ('claude','OLD1','/w/a','project:a','github.com/x/a',1000,2000,2500,''),
		  ('codex','OLD2','/w/b','project:b','github.com/x/b',3000,4000,0,'orbit-pi'),
		  ('claude','NEW1','/w/a','project:a','github.com/x/a',5000,5100,0,'human')`,
		`INSERT INTO works(id,host,session,title,opened_at) VALUES ('w1','claude','OLD1','로그인 고치기',1100),('w2','claude','OLD1','',1200),('w3','claude','NEW1','옛 작업',5050)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func hashOf(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func store(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// 끝 기준(M3·M4): 새 활동 기록과 옛 work.db 세션을 함께, 출처를 붙여 최신순으로 준다.
// 같은 호스트+세션이 둘에 다 있으면 새 기록 하나로 합치고 옛 작업 제목을 붙인다. work.db에는 쓰지 않는다.
func TestFind_새_기록과_옛_work_db(t *testing.T) {
	s := store(t)
	wdb := legacyDB(t)
	before := hashOf(t, wdb)
	if err := s.RecordActivity(memory.ActivityEvent{Kind: memory.ActivityPrompt, Host: "claude", Session: "NEW1", Cwd: "/w/a", ProjectScope: "project:a", ProjectLabel: "github.com/x/a", Launcher: "human", Prompt: "다시 로그인", At: 6000}); err != nil {
		t.Fatal(err)
	}
	res, err := Find(s, wdb, Query{})
	if err != nil || res.LegacyError != "" {
		t.Fatal(err, res.LegacyError)
	}
	if len(res.Items) != 3 {
		t.Fatalf("%+v", res.Items)
	}
	if r := res.Items[0]; r.Session != "NEW1" || r.Source != SourceActivity || r.Prompts != 1 || len(r.Works) != 1 || r.Works[0] != "옛 작업" || r.LastAt != 6000 {
		t.Fatalf("합친 줄 %+v", r)
	}
	if r := res.Items[1]; r.Session != "OLD2" || r.Source != SourceWorkDB || r.Launcher != "orbit-pi" || r.Host != "codex" {
		t.Fatalf("옛 줄 %+v", r)
	}
	if r := res.Items[2]; r.Session != "OLD1" || r.Launcher != "human" || len(r.Works) != 1 || r.Works[0] != "로그인 고치기" || r.EndedAt != 2500 || r.Project != "github.com/x/a" {
		t.Fatalf("옛 줄(launcher 빈 값은 human, 빈 제목은 뺀다) %+v", r)
	}
	// 거르기: 질의(작업 제목·요청 글·프로젝트), 호스트, 프로젝트, 시각, 개수
	for name, c := range map[string]struct {
		q    Query
		want []string
	}{
		"질의-옛 제목": {Query{Query: "로그인"}, []string{"NEW1", "OLD1"}},
		"질의-요청 글": {Query{Query: "다시"}, []string{"NEW1"}},
		"호스트":     {Query{Host: "codex"}, []string{"OLD2"}},
		"프로젝트":    {Query{ProjectScope: "project:a"}, []string{"NEW1", "OLD1"}},
		"since":   {Query{Since: 3500}, []string{"NEW1", "OLD2"}},
		"limit":   {Query{Limit: 1}, []string{"NEW1"}},
	} {
		res, err := Find(s, wdb, c.q)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range res.Items {
			got = append(got, r.Session)
		}
		if len(got) != len(c.want) || (len(got) > 0 && (got[0] != c.want[0] || got[len(got)-1] != c.want[len(c.want)-1])) {
			t.Errorf("%s: %v, 기대 %v", name, got, c.want)
		}
	}
	if after := hashOf(t, wdb); after != before {
		t.Fatal("work.db가 바뀌었습니다")
	}
}

// 끝 기준: work.db가 없으면 새 기록만, 깨졌으면 새 기록과 legacyError를 준다(실패하지 않는다).
func TestFind_work_db가_없거나_깨짐(t *testing.T) {
	s := store(t)
	s.RecordActivity(memory.ActivityEvent{Kind: memory.ActivityStart, Host: "claude", Session: "S1", At: 10})
	missing := filepath.Join(t.TempDir(), "work.db")
	res, err := Find(s, missing, Query{})
	if err != nil || len(res.Items) != 1 || res.LegacyError != "" {
		t.Fatalf("없음: %+v %v", res, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("없는 work.db를 만들었습니다")
	}
	broken := filepath.Join(t.TempDir(), "work.db")
	os.WriteFile(broken, []byte("not sqlite"), 0o600)
	res, err = Find(s, broken, Query{})
	if err != nil || len(res.Items) != 1 || res.LegacyError == "" {
		t.Fatalf("깨짐: %+v %v", res, err)
	}
	if n, err := LegacyCount(missing); n != 0 || err != nil {
		t.Fatalf("LegacyCount 없음 %d %v", n, err)
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
