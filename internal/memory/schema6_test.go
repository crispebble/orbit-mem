package memory

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// 스키마 6 이행(명세 6.3, L-1 기본값, Q-3, E-3): 5 → 6은 표·열 추가와 채우기·다시 색인을 한 트랜잭션에서 한다.

// v5Memories are the memories v5DB leaves, by role.
type v5Memories struct {
	confirmed, cand, candNoSight, volatile, durable, stale Memory
}

// 고정 시각(지역 시간): 만든 날, 고친 날, 다시 나온 날.
var (
	v5Created = time.Date(2026, 9, 1, 10, 0, 0, 0, time.Local).UnixMilli()
	v5Updated = time.Date(2026, 9, 10, 10, 0, 0, 0, time.Local).UnixMilli()
)

const (
	v5SightLate  = "2026-09-20" // cand의 마지막 다시 나온 날: 고친 시각보다 늦다
	v5SightEarly = "2026-08-15" // confirmed의 다시 나온 날: 만든 시각보다 이르다
)

// v5DB makes path look like a memory DB schema 5 left: no schema 6 tables or columns, meta and
// user_version 5, sightings only by (memory, day), review_at 0 on the memories that had no review
// cadence, and an index that matches the memories with no late marks.
func v5DB(t *testing.T, path string) v5Memories {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var f v5Memories
	f.confirmed, _ = mustSave(t, s, SaveInput{Kind: KindFact, Title: "배포 창은 화요일이다", Body: "화요일 오전에만 배포한다", Scope: ScopeGlobal,
		Source: Source{Kind: SourceQuote, Excerpt: "배포 창은 화요일이다"}})
	f.cand, _ = mustSave(t, s, SaveInput{Kind: KindFact, Title: "드라이런 먼저 돌린다", Body: "배포 스크립트는 드라이런으로 확인한다", Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "s"}, Who: Who{Actor: ActorAI}})
	f.candNoSight, _ = mustSave(t, s, aiLearns(KindLesson, "롤백은 이전 태그로", ""))
	f.volatile, _ = mustSave(t, s, SaveInput{Kind: KindFact, Title: "이번 주 동결", Scope: ScopeGlobal, Retention: RetentionVolatile, TTLDays: 3,
		Source: Source{Kind: SourceSession, Ref: "s"}, Who: Who{Actor: ActorAI}})
	f.durable, _ = mustSave(t, s, SaveInput{Kind: KindFact, Title: "저장소 이름 규칙", Scope: ScopeGlobal, Retention: RetentionDurable,
		Source: Source{Kind: SourceSession, Ref: "s"}, Who: Who{Actor: ActorAI}})
	f.stale, _ = mustSave(t, s, aiLearns(KindFact, "옛 빌드 명령", ""))
	if _, err := s.SetState(f.stale.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	db := s.DB()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE memories SET created_at=?, updated_at=?`, []any{v5Created, v5Created}},
		{`UPDATE memories SET updated_at=? WHERE id IN (?,?)`, []any{v5Updated, f.cand.ID, f.candNoSight.ID}},
		// 스키마 5에서 재검토 주기가 없던 기억은 review_at이 0이다(영구는 저장할 때 정해졌다).
		{`UPDATE memories SET review_at=0 WHERE retention<>'durable'`, nil},
		{`DELETE FROM sightings`, nil},
		{`INSERT INTO sightings(memory_id, day) VALUES(?,?),(?,?),(?,?)`, []any{f.cand.ID, "2026-09-05", f.cand.ID, v5SightLate, f.confirmed.ID, v5SightEarly}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	if _, err := s.FillIndex(0); err != nil { // 스키마 5 DB의 색인은 기억과 맞고 늦은 표시가 없다
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE IF EXISTS sighting_sessions`, `DROP TABLE IF EXISTS utterances`, `DROP TABLE IF EXISTS utterance_shingles`,
		`DROP TABLE IF EXISTS supersede_reverts`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, c := range [][2]string{{"memories", "aliases"}, {"sources", "rechecked"}} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, c[0], c[1]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			if _, err := db.Exec(`ALTER TABLE ` + c[0] + ` DROP COLUMN ` + c[1]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`UPDATE meta SET value='5' WHERE key='schema.version'; PRAGMA user_version=5`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	return f
}

type memState struct {
	state     string
	version   int
	updatedAt int64
	reviewAt  int64
}

func memStates(t *testing.T, path string) map[string]memState {
	t.Helper()
	rows, err := rawDB(t, path).Query(`SELECT id, state, version, updated_at, review_at FROM memories`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]memState{}
	for rows.Next() {
		var id string
		var m memState
		if err := rows.Scan(&id, &m.state, &m.version, &m.updatedAt, &m.reviewAt); err != nil {
			t.Fatal(err)
		}
		out[id] = m
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasTable(t *testing.T, path, name string) bool {
	t.Helper()
	var n int
	if err := rawDB(t, path).QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func hasColumn(t *testing.T, path, table, column string) bool {
	t.Helper()
	var n int
	if err := rawDB(t, path).QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

var schema6Tables = []string{"sighting_sessions", "utterances", "utterance_shingles", "supersede_reverts"}

func TestOpen_스키마5_DB를_6으로_올리면_기억_수가_같고_새_표가_있다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	v5DB(t, path)
	before := len(memStates(t, path))
	if before != 6 {
		t.Fatalf("fixture memories %d", before)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.Count(); n != before {
		t.Fatalf("memories %d after the upgrade, %d before", n, before)
	}
	if info, err := s.Schema(); err != nil || info != (SchemaInfo{Binary: 6, DB: 6, State: SchemaOK}) {
		t.Fatalf("schema %+v %v", info, err)
	}
	if v := s.Meta(metaSchemaVersion); v != "6" {
		t.Fatalf("meta schema.version %q", v)
	}
	if v := userVersion(t, path); v != 6 {
		t.Fatalf("user_version %d", v)
	}
	for _, name := range schema6Tables {
		if !hasTable(t, path, name) {
			t.Errorf("no table %s", name)
		}
	}
	if !hasColumn(t, path, "memories", "aliases") || !hasColumn(t, path, "sources", "rechecked") {
		t.Fatal("schema 6 columns missing")
	}
	// 옛 줄은 기본값을 받는다: 별칭 없음, 다시 확인하지 않은 출처.
	var bad int
	if err := s.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM memories WHERE aliases<>'[]') + (SELECT COUNT(*) FROM sources WHERE rechecked<>0)`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("old rows without defaults: %d %v", bad, err)
	}
}

func TestOpen_재검토일이_빈_기억에_마지막_확인_더하기_90일을_채운다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	f := v5DB(t, path)
	durableAt := memStates(t, path)[f.durable.ID].reviewAt
	if durableAt == 0 {
		t.Fatal("fixture: a durable memory has its review date")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	got := memStates(t, path)
	sight, err := time.ParseInLocation("2006-01-02", v5SightLate, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		id   string
		want int64
	}{
		// 마지막 확인은 만든 시각·고친 시각·마지막 다시 나온 날 가운데 가장 늦은 것이다.
		{"다시 나온 날이 가장 늦은 후보", f.cand.ID, sight.UnixMilli() + 90*dayMs},
		{"고친 시각이 가장 늦은 후보", f.candNoSight.ID, v5Updated + 90*dayMs},
		{"다시 나온 날이 만든 시각보다 이른 확정", f.confirmed.ID, v5Created + 90*dayMs},
		{"휘발은 채우지 않는다", f.volatile.ID, 0},
		{"이미 재검토일이 있으면 그대로", f.durable.ID, durableAt},
		{"낡은 기억은 채우지 않는다", f.stale.ID, 0},
	} {
		if got[c.id].reviewAt != c.want {
			t.Errorf("%s: review_at %d, want %d", c.name, got[c.id].reviewAt, c.want)
		}
	}
}

func TestOpen_옛_sightings를_legacy_세션으로_옮긴다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	f := v5DB(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.DB().Query(`SELECT memory_id, session, day, at FROM sighting_sessions ORDER BY memory_id, day`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var id, session, day string
		var at int64
		if err := rows.Scan(&id, &session, &day, &at); err != nil {
			t.Fatal(err)
		}
		if session != "legacy" || at != 0 {
			t.Errorf("%s %s: session %q at %d, want legacy at 0", id, day, session, at)
		}
		got[id+" "+day] = true
	}
	want := []string{f.cand.ID + " 2026-09-05", f.cand.ID + " " + v5SightLate, f.confirmed.ID + " " + v5SightEarly}
	if len(got) != len(want) {
		t.Fatalf("sighting_sessions %v, want %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("no legacy row %s", w)
		}
	}
	// 옛 표는 그대로 남는다(스키마 6 뒤에도 계속 쓴다).
	if n := countRows(t, s, "sightings"); n != 3 {
		t.Fatalf("sightings %d after the upgrade", n)
	}
}

func TestOpen_올린_뒤_모든_기억이_다시_색인된다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	f := v5DB(t, path)
	// 옛 버전의 낱말 나누기로 만든 색인 글: 색인 줄의 시각은 기억과 맞고 늦은 표시도 없어 전체 검사로도 늦지 않다.
	// 업그레이드는 이런 줄까지 지금 규칙으로 다시 색인해야 한다(Q-3).
	rawExec(t, path, `UPDATE memory_fts SET body='옛색인글' WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id='`+f.cand.ID+`')`)
	if r, _, err := OpenRead(path); err != nil {
		t.Fatal(err)
	} else {
		if n := staleCount(t, r); n != 0 {
			t.Fatalf("fixture: stale %d before the upgrade", n)
		}
		r.Close()
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := staleCount(t, s); n != 0 {
		t.Fatalf("StaleIndexCount %d after the upgrade", n)
	}
	n, _ := s.Count()
	if countRows(t, s, "memory_index") != n || countRows(t, s, "memory_fts") != n || countRows(t, s, "memory_index_late") != 0 {
		t.Fatalf("memories %d index %d fts %d late %d", n, countRows(t, s, "memory_index"), countRows(t, s, "memory_fts"), countRows(t, s, "memory_index_late"))
	}
	ms, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		wantIndexed(t, s, m.ID)
	}
	if ftsHits(t, s, `"옛색인글"`) != 0 {
		t.Fatal("the old index text is still there")
	}
}

func TestOpen_스키마6으로_올리기_전에_사본을_남긴다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	v5DB(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	copies, _ := filepath.Glob(filepath.Join(home, "backups", "before-schema-*"))
	if len(copies) != 1 {
		t.Fatalf("before-schema copies %v", copies)
	}
	ci, err := ReadCopyInfo(copies[0])
	if err != nil || ci.SchemaVersion != 5 || ci.Memories != 6 {
		t.Fatalf("copy %+v %v", ci, err)
	}
	if v := s.Meta(metaSchemaVersion); v != "6" {
		t.Fatalf("meta schema.version %q", v)
	}
}

func TestOpen_업그레이드_뒤_확정_기억의_상태는_그대로다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	f := v5DB(t, path)
	before := memStates(t, path)
	if before[f.confirmed.ID].state != StateConfirmed || before[f.cand.ID].state != StateCandidate || before[f.stale.ID].state != StateStale {
		t.Fatalf("fixture states %+v", before)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	after := memStates(t, path)
	for id, b := range before {
		a := after[id]
		// 채우기는 재검토일만 정한다: 상태·버전·고친 시각(마지막 확인의 근거)은 바꾸지 않는다.
		if a.state != b.state || a.version != b.version || a.updatedAt != b.updatedAt {
			t.Errorf("%s: %+v → %+v", id, b, a)
		}
	}
	var versions int
	if err := rawDB(t, path).QueryRow(`SELECT COUNT(*) FROM memory_versions WHERE memory_id=?`, f.confirmed.ID).Scan(&versions); err != nil || versions != before[f.confirmed.ID].version {
		t.Fatalf("versions of the confirmed memory %d %v", versions, err)
	}
}

// Review Focus 4: 이행 트랜잭션이 중간에 실패하면 DB는 스키마 5 그대로이고, 다시 열면 이행을 다시 시도한다.
// 실패는 채우기 둘째 단계(재검토일 UPDATE)에 거는 시험 DB의 트리거로 넣는다.
func TestOpen_이행이_실패하면_스키마5_그대로_남는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	f := v5DB(t, path)
	rawExec(t, path, `CREATE TRIGGER fail6 BEFORE UPDATE OF review_at ON memories BEGIN SELECT RAISE(ABORT, 'schema 6 test failure'); END`)
	before := memStates(t, path)
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("Open succeeded although the migration failed")
	}
	if v := userVersion(t, path); v != 5 {
		t.Fatalf("user_version %d after a failed migration", v)
	}
	var meta string
	if err := rawDB(t, path).QueryRow(`SELECT value FROM meta WHERE key='schema.version'`).Scan(&meta); err != nil || meta != "5" {
		t.Fatalf("meta schema.version %q %v", meta, err)
	}
	for _, name := range schema6Tables {
		if hasTable(t, path, name) {
			t.Errorf("table %s left by a failed migration", name)
		}
	}
	if hasColumn(t, path, "memories", "aliases") || hasColumn(t, path, "sources", "rechecked") {
		t.Fatal("columns left by a failed migration")
	}
	after := memStates(t, path)
	if len(after) != len(before) {
		t.Fatalf("memories %d → %d", len(before), len(after))
	}
	for id, b := range before {
		if after[id] != b {
			t.Errorf("%s changed by a failed migration: %+v → %+v", id, b, after[id])
		}
	}
	if r, info, err := OpenRead(path); err != nil || info != (SchemaInfo{Binary: 6, DB: 5, State: SchemaOlder}) {
		t.Fatalf("read open %+v %v", info, err)
	} else {
		r.Close()
	}

	rawExec(t, path, `DROP TRIGGER fail6`)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	defer s.Close()
	if info, err := s.Schema(); err != nil || info.State != SchemaOK || info.DB != 6 {
		t.Fatalf("schema after retry %+v %v", info, err)
	}
	if got := memStates(t, path)[f.candNoSight.ID].reviewAt; got != v5Updated+90*dayMs {
		t.Fatalf("review_at after retry %d", got)
	}
}

// 두 프로세스가 같은 스키마 5 DB를 함께 열어도 채우기는 한 번만 돈다: 둘 다 이행이 필요하다고 본 뒤 쓰기 잠금을
// 기다리게 하고(다른 연결이 쥔 잠금), 잠금 안에서 다시 읽은 버전으로 뒤의 쪽이 채우기를 건너뛰는지 본다. 다시 색인한
// 횟수는 시험 DB의 트리거가 memory_index UPDATE마다 남기는 줄로 센다.
func TestOpen_두_번_열어도_채우기는_한_번만_돈다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	f := v5DB(t, path)
	rawExec(t, path, `CREATE TABLE probe6 (memory_id TEXT NOT NULL);
		CREATE TRIGGER probe6_index AFTER UPDATE ON memory_index BEGIN INSERT INTO probe6 VALUES(NEW.memory_id); END`)
	n := len(memStates(t, path))

	lock := rawDB(t, path)
	conn, err := lock.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path)
			if err == nil {
				s.Close()
			}
			errs[i] = err
		}()
	}
	time.Sleep(500 * time.Millisecond) // 둘 다 버전을 읽고 사본을 만든 뒤 쓰기 잠금을 기다리게 한다
	if _, err := conn.ExecContext(context.Background(), `COMMIT`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if copies, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "before-schema-*")); len(copies) != 2 {
		t.Logf("before-schema copies %d (2 when both saw the old schema)", len(copies))
	}
	probes := func() map[string]int {
		rows, err := rawDB(t, path).Query(`SELECT memory_id, COUNT(*) FROM probe6 GROUP BY memory_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var id string
			var c int
			if err := rows.Scan(&id, &c); err != nil {
				t.Fatal(err)
			}
			out[id] = c
		}
		return out
	}
	got := probes()
	if len(got) != n {
		t.Fatalf("reindexed %d memories, want %d: %v", len(got), n, got)
	}
	for id, c := range got {
		if c != 1 {
			t.Errorf("%s reindexed %d times", id, c)
		}
	}

	// 스키마 6 DB를 다시 열면 채우기가 돌지 않는다: 그 뒤의 다시 나옴과 비운 재검토일을 건드리지 않는다.
	rawExec(t, path, `INSERT INTO sightings(memory_id, day) VALUES('`+f.candNoSight.ID+`', '2026-11-01');
		UPDATE memories SET review_at=0 WHERE id='`+f.cand.ID+`'`)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sighting_sessions WHERE memory_id=?`, f.candNoSight.ID).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("legacy rows of a sighting after the upgrade: %d %v", legacy, err)
	}
	if m := mustGet(t, s, f.cand.ID); m.ReviewAt != 0 {
		t.Fatalf("review_at filled again: %d", m.ReviewAt)
	}
	s.Close()
	for id, c := range probes() {
		if c != 1 {
			t.Errorf("%s reindexed %d times after reopening", id, c)
		}
	}
}

// 시각 되감기(bench)는 다시 나온 세션 기록도 다시 나온 기록(sightings)처럼 그 시각의 날까지만 둔다.
func TestRewind_다시_나온_세션_기록도_그_시각까지만_있다(t *testing.T) {
	path, a, b, at := historyFixture(t)
	day := func(ms int64) string { return Day(time.UnixMilli(ms)) }
	rawExec(t, path, `INSERT INTO sighting_sessions(memory_id, session, day, at) VALUES
		('`+a.ID+`', 's-1', '`+day(at[0])+`', `+strconv.FormatInt(at[0], 10)+`),
		('`+b.ID+`', 's-2', '`+day(at[2])+`', `+strconv.FormatInt(at[2], 10)+`)`)
	count := func(id string) int {
		var n int
		if err := rawDB(t, path).QueryRow(`SELECT COUNT(*) FROM sighting_sessions WHERE memory_id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	_, r := rewound(t, path)
	if err := r.To(at[0] - 1); err != nil {
		t.Fatal(err)
	}
	if count(a.ID)+count(b.ID) != 0 {
		t.Fatalf("sighting_sessions before every memory: a %d b %d", count(a.ID), count(b.ID))
	}
	if err := r.To(at[1] + 1); err != nil {
		t.Fatal(err)
	}
	if count(a.ID) != 1 || count(b.ID) != 0 {
		t.Fatalf("sighting_sessions at t2: a %d b %d", count(a.ID), count(b.ID))
	}
	if err := r.To(at[3] + 1); err != nil {
		t.Fatal(err)
	}
	if count(a.ID) != 1 || count(b.ID) != 1 {
		t.Fatalf("sighting_sessions now: a %d b %d", count(a.ID), count(b.ID))
	}
}
