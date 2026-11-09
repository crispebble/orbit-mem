package memory

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// 검색 색인(판 5, 설계 7절, ORBIT-25 a2): memory_index·memory_fts는 기억 쓰기와 같은 트랜잭션에서 맞추고,
// 옛 실행 파일이 쓴 기억(updated_at ≠ indexed_at)은 늦은 기억으로 잡혀 다음 쓰기 트랜잭션이 채운다.

// indexRow is a memory's index state: its index row and FTS texts (has is false when there is no row).
type indexRow struct {
	has                    bool
	indexedAt, updatedAt   string
	title, keys, body, fts string
}

func indexOf(t *testing.T, s *Store, id string) indexRow {
	t.Helper()
	var r indexRow
	var at, ti, ke, bo *string
	if err := s.DB().QueryRow(`SELECT CAST(m.updated_at AS TEXT), i.indexed_at, f.title, f.keys, f.body FROM memories m
		LEFT JOIN memory_index i ON i.memory_id = m.id LEFT JOIN memory_fts f ON f.rowid = i.rowid WHERE m.id=?`, id).
		Scan(&r.updatedAt, &at, &ti, &ke, &bo); err != nil {
		t.Fatalf("index of %s: %v", id, err)
	}
	if at != nil {
		r.has, r.indexedAt = true, *at
	}
	if ti != nil {
		r.title, r.keys, r.body, r.fts = *ti, *ke, *bo, "yes"
	}
	return r
}

// wantIndexed checks that the memory's index row is up to date and holds the indexed texts of the memory now.
func wantIndexed(t *testing.T, s *Store, id string) {
	t.Helper()
	m := mustGet(t, s, id)
	r := indexOf(t, s, id)
	keys := indexText(strings.Join(append([]string{m.Topic}, m.Tags...), " "))
	if !r.has || r.indexedAt != r.updatedAt || r.indexedAt != strconv.FormatInt(m.UpdatedAt, 10) || r.fts == "" {
		t.Fatalf("%s not indexed: %+v (updated_at %d)", id, r, m.UpdatedAt)
	}
	if r.title != indexText(m.Title) || r.keys != keys || r.body != indexText(m.Body) {
		t.Fatalf("%s texts: %+v, want title %q keys %q body %q", id, r, indexText(m.Title), keys, indexText(m.Body))
	}
}

func staleCount(t *testing.T, s *Store) int {
	t.Helper()
	n, err := s.StaleIndexCount()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func ftsHits(t *testing.T, s *Store, match string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE memory_fts MATCH ?`, match).Scan(&n); err != nil {
		t.Fatalf("match %s: %v", match, err)
	}
	return n
}

// v4DB makes path look like a memory DB schema 4 left (orbit-mem before the search index): no
// memory_index, memory_fts or save_requests, no sources.trust, meta and user_version 4.
func v4DB(t *testing.T, path string, titlesToKeep ...string) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range titlesToKeep {
		saveFact(t, s, ti)
	}
	if _, err := s.DB().Exec(`DROP TABLE memory_fts; DROP TABLE memory_index; DROP TABLE save_requests;
		ALTER TABLE sources DROP COLUMN trust; UPDATE meta SET value='4' WHERE key='schema.version'; PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	s.Close()
}

// oldBinaryWrites changes and adds memories with SQL only, as orbit-mem 0.16 or older does on a schema 5
// DB: it does not know the index, bumps updated_at and puts user_version back to 4.
func oldBinaryWrites(t *testing.T, path, changeID string, add int) []string {
	t.Helper()
	db := rawDB(t, path)
	// 옛 실행 파일의 열기: 자기 판의 DDL(IF NOT EXISTS)을 다시 돌리고 user_version을 4로 쓴다(판 5 표·열은 모른다).
	if _, err := db.Exec(schema + eventsSchema + linksSchema + labSchema + activitySchema + auditSchema); err != nil {
		t.Fatalf("old DDL on a schema 5 DB: %v", err)
	}
	if changeID != "" {
		if _, err := db.Exec(`UPDATE memories SET body='옛 실행 파일이 고친 본문 legacyedit', version=version+1, updated_at=updated_at+1 WHERE id=?`, changeID); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range add {
		id := NewID("m-")
		ti := fmt.Sprintf("옛 실행 파일 기억 %d", i)
		if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at)
			VALUES(?,?,?,'',?,?,?,?,'["old"]',1,?,?)`, id, KindFact, ScopeGlobal, StateCandidate, ti, TitleKey(ti), "본문 "+ti, 1700000000000+i, 1700000000000+i); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return ids
}

func TestIndexSchema_새_DB는_판5이고_색인_표와_그림자_표와_새_열이_꼴에_있다(t *testing.T) {
	if SchemaVersion != 5 {
		t.Fatalf("SchemaVersion %d", SchemaVersion)
	}
	want, err := expectedShape()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"table:memory_index", "table:memory_fts", "table:memory_fts_data", "table:memory_fts_idx",
		"table:memory_fts_content", "table:memory_fts_docsize", "table:memory_fts_config", "table:save_requests"} {
		if !want.objects[o] {
			t.Errorf("expected shape lacks %s", o)
		}
	}
	for _, c := range []string{"memory_index.memory_id", "memory_index.indexed_at", "memory_fts.title", "memory_fts.keys", "memory_fts.body",
		"sources.trust", "save_requests.key", "save_requests.args_hash", "save_requests.memory_id", "save_requests.action", "save_requests.at"} {
		if !want.columns[c] {
			t.Errorf("expected shape lacks column %s", c)
		}
	}
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info, err := s.Schema(); err != nil || info != (SchemaInfo{Binary: 5, DB: 5, State: SchemaOK}) {
		t.Fatalf("schema %+v %v", info, err)
	}
	var uv int
	if err := s.DB().QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil || uv != 5 {
		t.Fatalf("user_version %d %v", uv, err)
	}
	if v := s.Meta(metaSchemaVersion); v != "5" {
		t.Fatalf("meta %q", v)
	}
	// 옛 줄은 빈 등급(''), 새 열을 모르는 옛 실행 파일의 INSERT도 그대로 된다.
	if _, err := s.DB().Exec(`INSERT INTO sources(id,memory_id,kind,at) VALUES('s-x','m-x','quote',1)`); err != nil {
		t.Fatal(err)
	}
	var trust string
	if err := s.DB().QueryRow(`SELECT trust FROM sources WHERE id='s-x'`).Scan(&trust); err != nil || trust != "" {
		t.Fatalf("trust %q %v", trust, err)
	}
	if ok := hasIndexTables(t, s); !ok {
		t.Fatalf("index tables %v", ok)
	}
	// 판 4 DB는 색인 표가 없어 older다(쓰기 열기가 이전한다).
	old := filepath.Join(t.TempDir(), "old.db")
	v4DB(t, old, "a")
	r, info, err := OpenRead(old)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if info != (SchemaInfo{Binary: 5, DB: 4, State: SchemaOlder}) {
		t.Fatalf("v4 info %+v", info)
	}
	if ok := hasIndexTables(t, r); ok {
		t.Fatalf("v4 index tables %v", ok)
	}
}

func TestIndexMigrate_판4_DB를_열면_사본을_남기고_판5로_올려_기억을_모두_색인한다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	v4DB(t, path, "첫 기억 deploy", "둘째 기억 설계서를", "셋째 기억")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	copies, _ := filepath.Glob(filepath.Join(home, "backups", "before-schema-*"))
	if len(copies) != 1 {
		t.Fatalf("before-schema copies %v", copies)
	}
	if ci, err := ReadCopyInfo(copies[0]); err != nil || ci.SchemaVersion != 4 || ci.Memories != 3 {
		t.Fatalf("copy %+v %v", ci, err)
	}
	if info, err := s.Schema(); err != nil || info != (SchemaInfo{Binary: 5, DB: 5, State: SchemaOK}) {
		t.Fatalf("schema %+v %v", info, err)
	}
	n, _ := s.Count()
	if n != 3 || countRows(t, s, "memory_index") != n || countRows(t, s, "memory_fts") != n || staleCount(t, s) != 0 {
		t.Fatalf("memories %d index %d fts %d stale %d", n, countRows(t, s, "memory_index"), countRows(t, s, "memory_fts"), staleCount(t, s))
	}
	ms, _ := s.List()
	for _, m := range ms {
		wantIndexed(t, s, m.ID)
	}
	if ftsHits(t, s, `"설계 계서"`) != 1 || ftsHits(t, s, `"deploy"*`) != 1 {
		t.Fatal("migrated index does not find the bigrams or words")
	}
	if _, err := s.DB().Exec(`INSERT INTO memory_fts(memory_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("integrity-check: %v", err)
	}
}

func TestIndex_쓰기_다섯_곳_뒤_색인이_그_기억의_지금_글과_updated_at을_가진다(t *testing.T) {
	s := supStore(t)
	// 1) 저장(INSERT): 상태와 무관하게 모두 색인한다(후보도).
	in := aiLearns(KindDecision, "배포 절차 deploy", "release-flow")
	in.Body, in.Tags = "설계서를 먼저 읽는다", []string{"ops", "배포"}
	m, _ := mustSave(t, s, in)
	wantIndexed(t, s, m.ID)
	// 2) 같은 제목 다시 저장(sightTx → writeTx).
	again := aiLearns(KindDecision, "배포 절차 deploy", "release-flow")
	again.Source.Ref = "s2"
	m2, _ := mustSave(t, s, again)
	if m2.ID != m.ID || m2.UpdatedAt == m.UpdatedAt {
		t.Fatalf("sight: %+v", m2)
	}
	wantIndexed(t, s, m.ID)
	// 3) 고침(writeTx): 본문이 바뀌면 옛 토큰은 색인에서 빠진다.
	body := "새로운 본문 rollback"
	if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body, Reason: "고침"}); err != nil {
		t.Fatal(err)
	}
	wantIndexed(t, s, m.ID)
	if ftsHits(t, s, `"rollback"`) != 1 || ftsHits(t, s, `"설계 계서"`) != 0 {
		t.Fatal("update did not replace the indexed body")
	}
	hidden := StateHidden
	if _, err := s.Update(UpdateInput{ID: m.ID, State: &hidden, Reason: "숨김"}); err != nil {
		t.Fatal(err)
	}
	wantIndexed(t, s, m.ID)
	// 4) 나누기(split.go 조각 INSERT, 원래 기억은 writeTx).
	a, b := "화면 일은 그 시스템의 UI 키트로 만든다. "+strings.Repeat("키트 설명. ", 50), "키트끼리 가져다 쓰지 않는다. "+strings.Repeat("이유 설명. ", 50)
	sp := onidotFolder(userSays(KindRule, "UI 키트 규칙 묶음", "ui 키트"))
	sp.Body, sp.Tags = longBody(a, b), []string{"ui"}
	orig, _ := mustSave(t, s, sp)
	plan := Plan{Items: []PlanItem{{ID: orig.ID, Version: orig.Version, AppliesTo: "product:forma", Basis: "ai:두 주장",
		Split: []PlanPart{{Title: "화면은 그 시스템의 UI 키트로 만든다", Topic: "ui 키트 사용", Body: a}, {Title: "UI 키트끼리 가져다 쓰지 않는다", Topic: "ui 키트 간 의존", Body: b}}}}}
	rep, err := s.ApplyPlan(plan, planMap, false)
	if err != nil || len(rep.Applied) != 1 || len(rep.Applied[0].Parts) != 2 {
		t.Fatalf("split %+v %v", rep, err)
	}
	wantIndexed(t, s, orig.ID)
	for _, id := range rep.Applied[0].Parts {
		wantIndexed(t, s, id)
	}
	// 5) 들여오기(transfer.go INSERT): 내보낸 updated_at을 그대로 쓴다.
	imp := Memory{ID: NewID("m-"), Kind: KindFact, Scope: ScopeGlobal, State: StateConfirmed, Title: "들여온 기억", Body: "가져온 본문",
		Tags: []string{"imported"}, Topic: "import-topic", Version: 3, CreatedAt: 1600000000000, UpdatedAt: 1600000000500}
	if res, err := s.Import(imp, "옮김", false); err != nil || res != ImportDone {
		t.Fatalf("import %s %v", res, err)
	}
	wantIndexed(t, s, imp.ID)
	if r := indexOf(t, s, imp.ID); r.indexedAt != "1600000000500" {
		t.Fatalf("import indexed_at %q", r.indexedAt)
	}
	// 6) 제품 이름 바꿈(productrename.go UPDATE).
	pm, err := s.Save(SaveInput{Kind: KindDecision, Title: "옛 제품 결정", Scope: "product:old", Topic: "old-topic", Source: Source{Kind: SourceQuote, Excerpt: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameProduct("old", "new", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, s, pm.ID); got.Scope != "product:new" || got.UpdatedAt == pm.UpdatedAt {
		t.Fatalf("rename %+v", got)
	}
	wantIndexed(t, s, pm.ID)
	n, _ := s.Count()
	if staleCount(t, s) != 0 || countRows(t, s, "memory_index") != n || countRows(t, s, "memory_fts") != n {
		t.Fatalf("stale %d index %d fts %d memories %d", staleCount(t, s), countRows(t, s, "memory_index"), countRows(t, s, "memory_fts"), n)
	}
}

func TestIndex_옛_실행_파일이_쓴_기억은_늦은_기억으로_잡히고_판은_meta_5_그대로다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, s, "고칠 기억")
	saveFact(t, s, "그대로 둘 기억")
	ms, _ := s.List()
	var changed, kept string
	for _, m := range ms {
		if m.Title == "고칠 기억" {
			changed = m.ID
		} else {
			kept = m.ID
		}
	}
	s.Close()
	added := oldBinaryWrites(t, path, changed, 2)
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info, _ := s.Schema(); info.DB != 5 || info.State != SchemaOK {
		t.Fatalf("schema %+v", info)
	}
	ids, err := staleIDs(s.DB(), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{changed}, added...)
	slices.Sort(ids)
	slices.Sort(want)
	if !slices.Equal(ids, want) || slices.Contains(ids, kept) {
		t.Fatalf("stale %v, want %v", ids, want)
	}
	if staleCount(t, s) != 3 {
		t.Fatalf("stale count %d", staleCount(t, s))
	}
	// 읽기 전용 열기에서도 같은 목록을 읽는다(doctor·읽기 op 훑기).
	r, _, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if n, err := r.StaleIndexCount(); err != nil || n != 3 {
		t.Fatalf("read only stale %d %v", n, err)
	}
}

func TestIndex_다음_쓰기_트랜잭션이_늦은_줄을_한_번에_200개까지_채운다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, s, "기준 기억")
	s.Close()
	oldBinaryWrites(t, path, "", 250)
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := staleCount(t, s); n != 250 {
		t.Fatalf("stale before %d", n)
	}
	saveFact(t, s, "새 기억 하나") // save INSERT 길: 자기 줄 + 늦은 줄 200
	if n := staleCount(t, s); n != 50 {
		t.Fatalf("stale after a save %d", n)
	}
	ms, _ := s.List()
	body := "고친 본문"
	if _, err := s.Update(UpdateInput{ID: ms[0].ID, Body: &body}); err != nil { // writeTx 길
		t.Fatal(err)
	}
	if n := staleCount(t, s); n != 0 {
		t.Fatalf("stale after an update %d", n)
	}
	all, _ := s.List()
	for _, m := range all {
		wantIndexed(t, s, m.ID)
	}
}

func TestIndex_열기와_읽기_op는_늦은_줄을_채우지_않는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, s, "기억 하나")
	s.Close()
	oldBinaryWrites(t, path, "", 3)
	for _, o := range readOpens {
		r, info, err := o.open(path)
		if err != nil || info.State != SchemaOK {
			t.Fatalf("%s: %+v %v", o.name, info, err)
		}
		if _, err := r.Find(FindInput{Query: "옛 실행 파일"}); err != nil {
			t.Fatalf("%s find: %v", o.name, err)
		}
		_, _ = r.List()
		if n := staleCount(t, r); n != 3 {
			t.Fatalf("%s stale %d", o.name, n)
		}
		r.Close()
	}
	w, err := Open(path) // 판 5 DB의 쓰기 열기도 채우지 않는다(채우기는 쓰기 트랜잭션 끝에서만)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if n := staleCount(t, w); n != 3 {
		t.Fatalf("write open stale %d", n)
	}
}

func TestReindex_색인을_비우고_다시_만들며_멱등이다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saveFact(t, s, "첫 기억")
	saveFact(t, s, "둘째 기억")
	ms, _ := s.List()
	// 색인을 망가뜨린다: 글이 틀린 줄, 기억이 없는 줄, 늦은 줄.
	if _, err := s.DB().Exec(`UPDATE memory_fts SET body='엉뚱 zzz' WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id=?)`, ms[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO memory_index(memory_id, indexed_at) VALUES('m-gone','1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO memory_fts(rowid,title,keys,body) VALUES(last_insert_rowid(),'gone','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE memories SET updated_at=updated_at+5 WHERE id=?`, ms[1].ID); err != nil {
		t.Fatal(err)
	}
	dump := func() string {
		var out string
		if err := s.DB().QueryRow(`SELECT group_concat(x, char(10)) FROM (SELECT i.memory_id || '|' || i.indexed_at || '|' || f.title || '|' || f.keys || '|' || f.body AS x
			FROM memory_index i JOIN memory_fts f ON f.rowid = i.rowid ORDER BY i.memory_id)`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	n, err := s.Reindex()
	if err != nil || n != 2 {
		t.Fatalf("reindex %d %v", n, err)
	}
	first := dump()
	for _, m := range ms {
		wantIndexed(t, s, m.ID)
	}
	if countRows(t, s, "memory_index") != 2 || countRows(t, s, "memory_fts") != 2 || ftsHits(t, s, `"zzz"`) != 0 || staleCount(t, s) != 0 {
		t.Fatalf("after reindex: %s", first)
	}
	if n, err := s.Reindex(); err != nil || n != 2 || dump() != first {
		t.Fatalf("second reindex %d %v\n%s\n%s", n, err, first, dump())
	}
	if _, err := s.DB().Exec(`INSERT INTO memory_fts(memory_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("integrity-check: %v", err)
	}
}

func TestIndex_판4_사본으로_복구된_DB에서도_열려_있던_저장소의_쓰기가_성공한다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	dir := filepath.Join(home, "backups")
	old := filepath.Join(t.TempDir(), "v4.db")
	v4DB(t, old, "판 4 기억")
	a, err := Open(path) // 복구 전부터 떠 있던 프로세스(MCP 서버 같은)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	saveFact(t, a, "복구 전 기억")
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Restore(old, dir); err != nil {
		t.Fatal(err)
	}
	b.Close()
	if ok := hasIndexTables(t, a); ok {
		t.Fatalf("index tables after restore %v", ok)
	}
	m, err := a.Save(SaveInput{Kind: KindFact, Title: "복구 뒤 저장", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}})
	if err != nil {
		t.Fatalf("save after restore: %v", err)
	}
	body := "복구 뒤 고침"
	if _, err := a.Update(UpdateInput{ID: m.ID, Body: &body}); err != nil {
		t.Fatalf("update after restore: %v", err)
	}
	// 색인 표가 없으면 기억 모두가 늦은 기억이다(읽기는 훑기로 본다).
	if n, err := a.StaleIndexCount(); err != nil || n != 2 {
		t.Fatalf("stale %d %v", n, err)
	}
	if n, err := a.Reindex(); err == nil {
		t.Fatalf("reindex without the tables = %d, want an error", n)
	}
	// 출처 등급(sources.trust)도 판 4 사본에는 없다: 읽기는 kind로 읽고, 종류가 주는 등급의 저장은 열 없이 넣는다.
	if got, err := a.Get(m.ID); err != nil || got.Trust != TrustAgent || len(got.Sources) != 1 {
		t.Fatalf("get after restore: %+v %v", got, err)
	}
	if found, err := a.Find(FindInput{Query: "복구 뒤", AllProjects: true}); err != nil || len(found) == 0 || found[0].Trust != TrustAgent {
		t.Fatalf("find after restore: %+v %v", found, err)
	}
	owner, err := a.Save(SaveInput{Kind: KindFact, Title: "복구 뒤 저장", Scope: ScopeGlobal, TrustLimit: TrustOwner, Source: Source{Kind: SourceSession, Ref: "s"}})
	if err != nil || owner.ID != m.ID || owner.Trust != TrustAgent { // owner를 적을 칸이 없으면 낮춰(종류의 등급으로) 남긴다
		t.Fatalf("owner save after restore: %+v %v", owner, err)
	}
	// 멱등 표(save_requests)도 판 4 사본에는 없다: 키를 기록하지 않고 저장·고침·건너뛰기는 성공한다. 같은 인자의
	// 재시도는 같은 제목 단축이 새 기억을 만들지 않게 막는다.
	idem := &Idem{Key: "restore-key", Hash: "h"}
	for range 2 {
		got, err := a.Save(SaveInput{Kind: KindFact, Title: "복구 뒤 저장", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}, Idem: idem})
		if err != nil || got.ID != m.ID {
			t.Fatalf("idempotent save after restore: %+v %v", got, err)
		}
	}
	if _, ok, err := a.SaveRequest("restore-key"); err != nil || ok {
		t.Fatalf("save request after restore %v %v", ok, err)
	}
	if _, err := a.Update(UpdateInput{ID: m.ID, Body: &body, Idem: &Idem{Key: "restore-update", Hash: "h"}}); err != nil {
		t.Fatalf("idempotent update after restore: %v", err)
	}
	if got, replay, err := a.RecordSkip(Idem{Key: "restore-skip", Hash: "h"}, m.ID); err != nil || got.ID != m.ID || replay != nil {
		t.Fatalf("skip after restore: %+v %+v %v", got, replay, err)
	}
	if n, err := a.PruneSaveRequests(SaveRequestDays); err != nil || n != 0 {
		t.Fatalf("prune after restore %d %v", n, err)
	}
	// 낮춘 등급(믿을 수 없음)은 적을 칸이 없으면 저장하지 않는다: agent로 올려 남기지 않는다.
	if _, err := a.Save(SaveInput{Kind: KindFact, Title: "복구 뒤 웹 글", Scope: ScopeGlobal, Source: Source{Kind: SourceFile, Ref: "web", Trust: TrustUntrusted}}); !errors.Is(err, ErrTrustNotStored) {
		t.Fatalf("untrusted save after restore err %v", err)
	}
	a.Close()
	// 다음 쓰기 열기가 이전하고 전부 채운다.
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if staleCount(t, w) != 0 || countRows(t, w, "memory_index") != 2 {
		t.Fatalf("after reopen stale %d index %d", staleCount(t, w), countRows(t, w, "memory_index"))
	}
	wantIndexed(t, w, m.ID)
	// 이전 뒤에는 키를 다시 기록한다.
	if _, _, err := w.RecordSkip(Idem{Key: "restore-skip", Hash: "h"}, m.ID); err != nil {
		t.Fatal(err)
	}
	if r, ok, err := w.SaveRequest("restore-skip"); err != nil || !ok || r.Action != ApplySkip {
		t.Fatalf("save request after reopen %+v %v %v", r, ok, err)
	}
}

func TestIndex_두_연결이_동시에_써도_색인_줄은_기억마다_하나이고_늦은_줄이_없다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	var stores []*Store
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		stores = append(stores, s)
	}
	saveFact(t, stores[0], "같이 고칠 기억")
	ms, _ := stores[0].List()
	shared := ms[0].ID
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for w, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := range 25 {
				if _, err := s.Save(SaveInput{Kind: KindFact, Title: fmt.Sprintf("연결 %d 기억 %d", w, i), Scope: ScopeGlobal,
					Source: Source{Kind: SourceSession, Ref: "s"}}); err != nil {
					errs <- err
				}
				body := fmt.Sprintf("연결 %d 고침 %d", w, i)
				if _, err := s.Update(UpdateInput{ID: shared, Body: &body}); err != nil {
					errs <- err
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	s := stores[0]
	n, _ := s.Count()
	if n != 51 || countRows(t, s, "memory_index") != n || countRows(t, s, "memory_fts") != n || staleCount(t, s) != 0 {
		t.Fatalf("memories %d index %d fts %d stale %d", n, countRows(t, s, "memory_index"), countRows(t, s, "memory_fts"), staleCount(t, s))
	}
	wantIndexed(t, s, shared)
}

func TestReadCopyInfo_판은_meta_schema_version을_읽는다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, s, "a")
	s.Close()
	rawExec(t, path, `PRAGMA user_version=4`) // 옛 실행 파일이 user_version을 되돌린 DB
	if ci, err := ReadCopyInfo(path); err != nil || ci.SchemaVersion != 5 || ci.Memories != 1 {
		t.Fatalf("copy info %+v %v", ci, err)
	}
	legacy := filepath.Join(t.TempDir(), "legacy.db")
	legacyDB(t, legacy, "b") // meta 키 없음 → user_version 3
	if ci, err := ReadCopyInfo(legacy); err != nil || ci.SchemaVersion != 3 {
		t.Fatalf("legacy copy info %+v %v", ci, err)
	}
}

func TestIndexMigrate_판4_이전이_실패하면_표도_채우기도_남지_않고_판4_그대로다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	v4DB(t, path, "남을 기억")
	// FTS5 그림자 표 이름을 미리 차지해 가상 표 만들기를 실패시킨다(memory_index는 그 앞에서 만든다).
	rawExec(t, path, `CREATE TABLE memory_fts_data(id INTEGER PRIMARY KEY, block BLOB)`)
	before := fileShape(t, path)
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open migrated a DB whose FTS table cannot be made")
	}
	if !strings.Contains(err.Error(), "memory_fts_data") {
		t.Fatalf("not the FTS table failure: %v", err)
	}
	if after := fileShape(t, path); after != before {
		t.Fatalf("a failed migration changed the DB\nbefore %s\nafter  %s", before, after)
	}
	if v := metaVersion(t, path); v != "4" {
		t.Fatalf("meta %q", v)
	}
	if got := titles(t, path); got != "남을 기억" {
		t.Fatalf("memories %q", got)
	}
}

func TestIndex_VACUUM과_VACUUM_INTO_사본에서도_색인_줄과_FTS_rowid가_맞는다(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := range 6 {
		saveFact(t, s, fmt.Sprintf("기억 %d 설계서를", i))
	}
	ms, _ := s.List()
	hidden := StateHidden
	if _, err := s.Update(UpdateInput{ID: ms[2].ID, State: &hidden}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	cp, err := s.Snapshot(filepath.Join(home, "backups"), "check")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Open(cp)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, st := range []*Store{s, c} {
		if staleCount(t, st) != 0 || countRows(t, st, "memory_fts") != 6 || ftsHits(t, st, `"설계 계서"`) != 6 {
			t.Fatalf("%s: stale %d fts %d", st.Path(), staleCount(t, st), countRows(t, st, "memory_fts"))
		}
		for _, m := range ms {
			wantIndexed(t, st, m.ID)
		}
	}
}

func TestFillIndex_늦은_줄을_한도만큼_채우고_0이면_전부_채운다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saveFact(t, s, "기준")
	s.Close()
	oldBinaryWrites(t, path, "", 5)
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.FillIndex(2); err != nil || n != 2 || staleCount(t, s) != 3 {
		t.Fatalf("fill 2: %d %v stale %d", n, err, staleCount(t, s))
	}
	if n, err := s.FillIndex(0); err != nil || n != 3 || staleCount(t, s) != 0 {
		t.Fatalf("fill all: %d %v stale %d", n, err, staleCount(t, s))
	}
	if n, err := s.FillIndex(0); err != nil || n != 0 {
		t.Fatalf("fill again: %d %v", n, err)
	}
}

// hasIndexTables says whether s's DB has the search index tables (a schema 4 copy restored into it has none).
func hasIndexTables(t *testing.T, s *Store) bool {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('memory_index','memory_fts')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 2
}

func TestIndex_FTS_줄이_빠진_기억은_늦은_줄이고_채우면_다시_찾는다(t *testing.T) {
	s := supStore(t)
	gone, _ := mustSave(t, s, aiLearns(KindFact, "배포 전에 드라이런을 돌린다", ""))
	mustSave(t, s, aiLearns(KindFact, "커밋 전에 시험을 돌린다", ""))
	if n, err := s.StaleIndexCount(); err != nil || n != 0 {
		t.Fatalf("stale before %d %v", n, err)
	}
	// memory_index 줄은 있는데 그 rowid의 memory_fts 줄이 없다(색인이 깨진 DB).
	if _, err := s.DB().Exec(`DELETE FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id=?)`, gone.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.StaleIndexCount(); err != nil || n != 1 {
		t.Fatalf("stale %d %v, want 1", n, err)
	}
	// 고치기 전에도 찾기는 늦은 줄을 훑어 찾는다.
	if got, err := s.Find(FindInput{Query: "드라이런", AllProjects: true}); err != nil || len(got) != 1 || got[0].ID != gone.ID {
		t.Fatalf("find before fill %v %v", titlesOf(got), err)
	}
	if n, err := s.FillIndex(0); err != nil || n != 1 {
		t.Fatalf("fill %d %v", n, err)
	}
	if n, err := s.StaleIndexCount(); err != nil || n != 0 {
		t.Fatalf("stale after %d %v", n, err)
	}
	var fts int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE rowid=(SELECT rowid FROM memory_index WHERE memory_id=?)`, gone.ID).Scan(&fts); err != nil || fts != 1 {
		t.Fatalf("fts rows %d %v", fts, err)
	}
	if got, err := s.Find(FindInput{Query: "드라이런", AllProjects: true}); err != nil || len(got) != 1 || got[0].ID != gone.ID {
		t.Fatalf("find after fill %v %v", titlesOf(got), err)
	}
}
