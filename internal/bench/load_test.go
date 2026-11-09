package bench_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func TestParse는_틀린_사례_파일을_거절한다(t *testing.T) {
	head := `{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app",`
	cases := []struct{ name, in, want string }{
		{"알 수 없는 키", head + `"cases":[{"id":"a","prompt":"p","expcet":[]}]}`, "expcet"},
		{"now 없음", `{"cwd":"/bench/app","cases":[{"id":"a","prompt":"p"}]}`, "now"},
		{"expect 4개", head + memories + `,"cases":[{"id":"a","prompt":"p","expect":["a","b","c","d"]}]}`, "at most 3"},
		{"사람이 쓰지 않은 글에 expect", head + memories + `,"cases":[{"id":"a","prompt":"p","origin":"agent","expect":["a"]}]}`, "expect"},
		{"같은 사례 id", head + `"cases":[{"id":"a","prompt":"p"},{"id":"a","prompt":"q"}]}`, `"a" is repeated`},
		{"없는 기억 키", head + memories + `,"cases":[{"id":"a","prompt":"p","expect":["zz"]}]}`, `"zz"`},
		{"expect와 forbid가 같다", head + memories + `,"cases":[{"id":"a","prompt":"p","expect":["a"],"forbid":["a"]}]}`, "both"},
		{"origin 값", head + `"cases":[{"id":"a","prompt":"p","origin":"bot"}]}`, "origin"},
		{"kind 값", head + `"cases":[{"id":"a","prompt":"p","kind":"x"}]}`, "kind"},
		{"요청에 prompt도 tool도 없음", head + `"cases":[{"id":"a"}]}`, "prompt or a tool"},
		{"위임에 task 없음", head + `"cases":[{"id":"a","kind":"subagent"}]}`, "task"},
		{"중복 저장에 초안 없음", head + `"cases":[{"id":"a","kind":"duplicate"}]}`, "save"},
		{"같은 기억 키", head + `"memories":[{"key":"a","kind":"fact","scope":"global","title":"x"},{"key":"a","kind":"fact","scope":"global","title":"y"}],"cases":[]}`, "repeated"},
		{"cwd 없음", `{"now":"2026-11-03T09:00:00+09:00","cases":[{"id":"a","prompt":"p"}]}`, "cwd"},
		{"뒤에 남은 글", head + `"cases":[]} {}`, "after"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := bench.Parse([]byte(c.in))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestParse는_요청_글이_없는_사례의_origin은_보지_않는다(t *testing.T) {
	head := `{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app",`
	for name, c := range map[string]string{
		"도구만 있는 사례": `{"id":"a","tool":"Bash","origin":"agent","expect":["a"]}`,
		"위임 사례":     `{"id":"a","kind":"subagent","task":"t","origin":"agent","expect":["a"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			in := head + memories + `,"cases":[` + c + `]}`
			if _, err := bench.Parse([]byte(in)); err != nil {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestParse는_seed를_글자나_숫자로_받는다(t *testing.T) {
	for _, seed := range []string{`"s1"`, `42`} {
		set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00Z","cwd":"/x","seed":` + seed + `,"cases":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(set.Seed) != strings.Trim(seed, `"`) {
			t.Errorf("seed %q", set.Seed)
		}
	}
}

func TestPrepare는_기억을_고정_id로_넣고_칸을_옮긴다(t *testing.T) {
	fixHome(t)
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s","memories":[
	 {"key":"rule","kind":"rule","scope":"here","quote":true,"title":"규칙 하나","body":"본문","ageDays":10,"followed":2,"topic":"주제  A"},
	 {"key":"vol","kind":"fact","scope":"global","title":"휘발","retention":"volatile","ttlDays":2,"ageDays":1},
	 {"key":"dur","kind":"decision","scope":"project:/bench/other","title":"영구","retention":"durable","reviewAfterDays":30,"tools":["Bash","mcp__x__*"],"state":"stale"},
	 {"key":"cand","kind":"lesson","scope":"global","title":"후보"}],
	 "cases":[{"id":"c","prompt":"p","expect":["rule"],"allow":["vol"],"forbid":["cand"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.IDOf("rule") != "m-000000000001" || p.IDOf("cand") != "m-000000000004" {
		t.Errorf("ids %s %s", p.IDOf("rule"), p.IDOf("cand"))
	}
	c := p.Set.Cases[0]
	if c.Expect[0] != "m-000000000001" || c.Allow[0] != "m-000000000002" || c.Forbid[0] != "m-000000000004" {
		t.Errorf("case refs %+v", c)
	}
	if set.Cases[0].Expect[0] != "rule" {
		t.Error("Prepare must not change the set it was given")
	}
	db, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := memory.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := set.At()
	day := int64(24 * time.Hour / time.Millisecond)
	rule, _ := s.Get("m-000000000001")
	at := now.AddDate(0, 0, -10).UnixMilli()
	if rule.CreatedAt != at || rule.UpdatedAt != at {
		t.Errorf("ageDays: created %d updated %d, want %d", rule.CreatedAt, rule.UpdatedAt, at)
	}
	if rule.Scope != "project:/bench/app" || rule.State != memory.StateConfirmed || rule.Topic != memory.TopicKey("주제  A") {
		t.Errorf("rule %+v", rule)
	}
	vol, _ := s.Get("m-000000000002")
	if vol.Retention != memory.RetentionVolatile || vol.ExpiresAt != vol.CreatedAt+2*day || vol.State != memory.StateCandidate {
		t.Errorf("vol %+v", vol)
	}
	dur, _ := s.Get("m-000000000003")
	if dur.Retention != memory.RetentionDurable || dur.ReviewAt != dur.CreatedAt+30*day || dur.State != memory.StateStale || len(dur.Tools) != 2 {
		t.Errorf("dur %+v", dur)
	}
	core, err := s.Core("project:/bench/app")
	if err != nil || len(core) != 0 {
		t.Errorf("project scope has no core layer: %v %v", core, err)
	}
	if len(rule.Sources) == 0 || rule.Sources[0].Kind != memory.SourceQuote {
		t.Errorf("quote source: %+v", rule.Sources)
	}
	p2, _ := bench.Prepare(set, "")
	defer p2.Close()
	if p2.IDOf("rule") != p.IDOf("rule") {
		t.Error("ids are fixed")
	}
}

func TestPrepare는_pinned를_상시_핵심으로_올리고_followed를_사건으로_남긴다(t *testing.T) {
	fixHome(t)
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s","memories":[
	 {"key":"core","kind":"rule","scope":"global","quote":true,"title":"늘 지킨다","followed":3,"pinned":true}],"cases":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	db, _ := p.Copy()
	s, _, err := memory.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	core, err := s.Core("global")
	if err != nil || len(core) != 1 || core[0].ID != "m-000000000001" {
		t.Errorf("core %v %v", core, err)
	}
	usage, err := s.UsageCounts("2026-01-01", "2026-12-31")
	if err != nil || usage["m-000000000001"].Followed != 3 {
		t.Errorf("usage %v %v", usage, err)
	}
}

func TestPrepare는_같은_종류_범위_제목이_조용히_버려지면_오류다(t *testing.T) {
	fixHome(t)
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","memories":[
	 {"key":"a","kind":"fact","scope":"global","title":"같은 제목"},
	 {"key":"b","kind":"fact","scope":"global","title":"같은  제목"}],"cases":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bench.Prepare(set, ""); err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Errorf("err = %v, want it to name the memory b", err)
	}
}

func TestPrepare는_같은_제목의_옛_판을_범위를_달리해_둔다(t *testing.T) {
	fixHome(t)
	set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","memories":[
	 {"key":"new","kind":"decision","scope":"global","title":"기본 모델","topic":"모델","ageDays":1},
	 {"key":"old","kind":"decision","scope":"project:/bench/app","title":"기본 모델","topic":"모델","ageDays":30}],"cases":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
}

func writeDB(t *testing.T) (dir, path string) {
	t.Helper()
	fixHome(t)
	dir = t.TempDir()
	path = filepath.Join(dir, "memory.db")
	return
}

func TestPrepare는_DB_사본에서_id를_확인하고_원본은_쓰지_않는다(t *testing.T) {
	_, path := writeDB(t)
	// 쓰는 연결을 열어 둔 채 WAL 프레임이 남은 원본: 닫으면 WAL이 정리되므로 닫지 않는다.
	w, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	saved, err := w.Save(memory.SaveInput{Kind: "fact", Title: "WAL에만 있는 기억", Scope: "global", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path + "-wal"); err != nil || st.Size() == 0 {
		t.Fatalf("the test needs WAL frames left: %v", err)
	}
	before := sums(t, path)
	set, err := bench.Parse([]byte(fmt.Sprintf(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","cases":[{"id":"c","prompt":"p","expect":[%q]}]}`, saved.ID)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Set.Cases[0].Expect[0] != saved.ID {
		t.Errorf("a DB given keeps real ids: %v", p.Set.Cases[0].Expect)
	}
	copyPath, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := memory.OpenReadOnly(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.Exists(saved.ID) {
		t.Error("the copy lacks the memory that was only in the WAL")
	}
	after := sums(t, path)
	for name, sum := range before {
		if after[name] != sum {
			t.Errorf("%s changed while preparing a copy", name)
		}
	}
	if len(before) != 3 {
		t.Errorf("checked %d files, want the db, -wal and -shm", len(before))
	}
}

func sums(t *testing.T, path string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatalf("%s: %v", suffix, err)
		}
		out[suffix] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return out
}

func TestPrepare는_DB에_없는_id를_가리키면_오류다(t *testing.T) {
	_, path := writeDB(t)
	w, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	for _, field := range []string{"expect", "allow", "forbid"} {
		set, err := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","cases":[{"id":"c","prompt":"p","` + field + `":["m-nope"]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bench.Prepare(set, path); err == nil || !strings.Contains(err.Error(), "m-nope") {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

func TestPrepare는_DB도_기억도_없거나_둘_다_있으면_오류다(t *testing.T) {
	_, path := writeDB(t)
	none, _ := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00Z","cwd":"/x","cases":[]}`))
	if _, err := bench.Prepare(none, ""); err == nil {
		t.Error("neither memories nor a DB")
	}
	both, _ := bench.Parse([]byte(`{"now":"2026-11-03T09:00:00Z","cwd":"/x","memories":[{"key":"a","kind":"fact","scope":"global","title":"t"}],"cases":[]}`))
	if _, err := bench.Prepare(both, path); err == nil {
		t.Error("memories and a DB")
	}
	if _, err := bench.Prepare(none, filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("missing DB")
	}
}
