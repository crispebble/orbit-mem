package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// 지난 시각의 기억 상태(bench 전용, 1-6 b6r 2차). 재생 사례는 사례보다 나중에 뜬 DB 사본 위에서 돈다: 그 요청 뒤에 만든
// 기억, 그 뒤에 낡거나 대체된 기억의 새 상태가 그대로 보이면 회상이 그 시각에 없던 것을 넣을 수 있다. History는 사본
// 하나(bench의 틀)를 한 번 읽어 기억마다의 판(memory_versions의 스냅숏)과 시각이 있는 줄을 들고, Rewinder는 실행용 사본을
// 사례 시각의 상태로 맞춘다. 사본이 다른 프로세스가 쓴 것처럼 바뀌므로(쓰기 연결 따로) 회상 경로는 고칠 것이 없다.
//
// 시각 at의 상태:
//   - 기억: created_at ≤ at인 기억만 있다. 내용·상태는 at 이전 마지막 판의 스냅숏이다(종류·범위·상태·제목·본문·태그·판·
//     고친 시각·대체한 판·낡은 까닭·상시 핵심). 판이 at 뒤에만 있으면(옛 판에서 옮긴 기억) 첫 판이다. 스냅숏에 없는 칸
//     (주제·도구·보존·검토·기한·적용 대상)은 지금 값을 쓴다. 판 이력(memory_versions)은 at 이전 판까지다.
//   - 출처(sources)는 at 이전 것(하나도 없으면 첫 출처), 다시 나옴(sightings)은 at의 날 이전 것.
//   - 연결(memory_links)은 at 이전에 만든 것만, at 뒤에 지운 것은 살아 있다. 두 끝이 모두 있을 때만.
//   - 사건(events)은 틀에 있던 것 중 at 이전 것만. 실행이 쓴 사건·세션 표시·활동 기록은 건드리지 않는다.
//
// 틀에 없는 줄(실행이 쓴 줄)은 기억 표(memories·memory_versions·sources·sightings·memory_links)에 생기지 않는다:
// 회상 경로는 이 표들을 읽기만 한다.

// History is what a memory DB copy held over time, read once.
type History struct {
	cols   map[string][]string
	mems   map[string]*memHistory
	ids    []string
	links  []linkHistory
	events []eventHistory
}

type memHistory struct {
	created   int64
	current   []any
	versions  []versionHistory // by version
	sources   []timedRow       // by at
	sightings []sightingRow    // by day
}

type versionHistory struct {
	at   int64
	row  []any
	snap versionSnapshot
}

type versionSnapshot struct {
	Kind         string   `json:"kind"`
	Scope        string   `json:"scope"`
	State        string   `json:"state"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	Tags         []string `json:"tags"`
	Version      int      `json:"version"`
	UpdatedAt    int64    `json:"updatedAt"`
	SupersededBy string   `json:"supersededBy"`
	StaleReason  string   `json:"staleReason"`
	Pinned       bool     `json:"pinned"`
}

type timedRow struct {
	at  int64
	row []any
}

type sightingRow struct {
	day string
	row []any
}

type linkHistory struct {
	id, a, b         string
	created, removed int64
	row              []any
}

type eventHistory struct {
	id  int64
	at  int64
	row []any
}

// historyTables are the tables History reads, with the column that orders their rows.
var historyTables = map[string]string{
	"memories":        "id",
	"memory_versions": "memory_id, version",
	"sources":         "memory_id, at, rowid",
	"sightings":       "memory_id, day",
	"memory_links":    "created_at, id",
	"events":          "id",
}

// ReadHistory reads the history of the memory DB at path (read only). The DB must have this binary's schema.
func ReadHistory(path string) (*History, error) {
	s, info, err := OpenReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if info.State != SchemaOK {
		return nil, fmt.Errorf("history: the DB schema is %q, not ok", info.State)
	}
	h := &History{cols: map[string][]string{}, mems: map[string]*memHistory{}}
	rows := map[string][][]any{}
	for table, order := range historyTables {
		cols, data, err := readTable(s.db, table, order)
		if err != nil {
			return nil, fmt.Errorf("history: %s: %w", table, err)
		}
		h.cols[table], rows[table] = cols, data
	}
	col := func(table, name string) int { return slices.Index(h.cols[table], name) }
	for _, r := range rows["memories"] {
		id := r[col("memories", "id")].(string)
		h.mems[id] = &memHistory{created: asInt(r[col("memories", "created_at")]), current: r}
		h.ids = append(h.ids, id)
	}
	for _, r := range rows["memory_versions"] {
		m := h.mems[asString(r[col("memory_versions", "memory_id")])]
		if m == nil {
			continue
		}
		v := versionHistory{at: asInt(r[col("memory_versions", "at")]), row: r}
		if err := json.Unmarshal([]byte(asString(r[col("memory_versions", "snapshot")])), &v.snap); err != nil {
			return nil, fmt.Errorf("history: snapshot of %v: %w", r[0], err)
		}
		m.versions = append(m.versions, v)
	}
	for _, r := range rows["sources"] {
		if m := h.mems[asString(r[col("sources", "memory_id")])]; m != nil {
			m.sources = append(m.sources, timedRow{at: asInt(r[col("sources", "at")]), row: r})
		}
	}
	for _, r := range rows["sightings"] {
		if m := h.mems[asString(r[col("sightings", "memory_id")])]; m != nil {
			m.sightings = append(m.sightings, sightingRow{day: asString(r[col("sightings", "day")]), row: r})
		}
	}
	for _, m := range h.mems {
		if len(m.versions) == 0 {
			return nil, errors.New("history: a memory has no version")
		}
		sort.SliceStable(m.sources, func(i, j int) bool { return m.sources[i].at < m.sources[j].at })
	}
	for _, r := range rows["memory_links"] {
		h.links = append(h.links, linkHistory{id: asString(r[col("memory_links", "id")]), a: asString(r[col("memory_links", "a")]),
			b: asString(r[col("memory_links", "b")]), created: asInt(r[col("memory_links", "created_at")]),
			removed: asInt(r[col("memory_links", "removed_at")]), row: r})
	}
	for _, r := range rows["events"] {
		h.events = append(h.events, eventHistory{id: asInt(r[col("events", "id")]), at: asInt(r[col("events", "at")]), row: r})
	}
	return h, nil
}

func readTable(db *sql.DB, table, order string) ([]string, [][]any, error) {
	rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY ` + order)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	return cols, out, rows.Err()
}

func asInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// Existed reports whether memory id was made by at (ms).
func (h *History) Existed(id string, at int64) bool {
	m := h.mems[id]
	return m != nil && m.created <= at
}

// memPhase is which rows of a memory a time shows: the version index (-1: no memory), how many sources and
// sightings.
type memPhase struct{ version, sources, sightings int }

func (m *memHistory) phase(at int64, day string) memPhase {
	if m.created > at {
		return memPhase{version: -1}
	}
	v := sort.Search(len(m.versions), func(i int) bool { return m.versions[i].at > at }) - 1
	src := sort.Search(len(m.sources), func(i int) bool { return m.sources[i].at > at })
	if src == 0 && len(m.sources) > 0 {
		src = 1
	}
	sight := sort.Search(len(m.sightings), func(i int) bool { return m.sightings[i].day > day })
	return memPhase{version: max(v, 0), sources: src, sightings: sight}
}

func (m *memHistory) latest() memPhase {
	return memPhase{version: len(m.versions) - 1, sources: len(m.sources), sightings: len(m.sightings)}
}

// rowAt is the memories row of version v: the current row with the snapshot's columns.
func (h *History) rowAt(m *memHistory, v int) []any {
	if v == len(m.versions)-1 {
		return m.current
	}
	row := slices.Clone(m.current)
	snap := m.versions[v].snap
	tags := snap.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	pinned := 0
	if snap.Pinned {
		pinned = 1
	}
	set := map[string]any{"kind": snap.Kind, "scope": snap.Scope, "state": snap.State, "title": snap.Title, "title_key": TitleKey(snap.Title),
		"body": snap.Body, "tags": string(tagsJSON), "version": int64(snap.Version), "updated_at": snap.UpdatedAt,
		"superseded_by": snap.SupersededBy, "stale_reason": snap.StaleReason, "pinned": int64(pinned)}
	for i, c := range h.cols["memories"] {
		if v, ok := set[c]; ok {
			row[i] = v
		}
	}
	return row
}

// Rewinder puts one DB copy at the state of a time.
type Rewinder struct {
	db     *sql.DB
	h      *History
	mems   map[string]memPhase
	links  []int8 // 0 none, 1 live, 2 removed
	events []bool
}

// Rewind opens a writer on the DB copy at path, which must hold what the history was read from (its rows
// may have been changed since by the recall paths only in the tables the history does not keep).
func (h *History) Rewind(path string) (*Rewinder, error) {
	db, err := sqlitedb.OpenWith(path, sqlitedb.Options{Busy: writeBusy, Pragmas: storePragmas, Existing: true})
	if err != nil {
		return nil, err
	}
	r := &Rewinder{db: db, h: h, mems: map[string]memPhase{}, links: make([]int8, len(h.links)), events: make([]bool, len(h.events))}
	for id, m := range h.mems {
		r.mems[id] = m.latest()
	}
	for i, l := range h.links {
		r.links[i] = 1
		if l.removed != 0 {
			r.links[i] = 2
		}
	}
	for i := range r.events {
		r.events[i] = true
	}
	return r, nil
}

// Close closes the writer.
func (r *Rewinder) Close() error { return r.db.Close() }

// To puts the copy at the state of at (ms), in one transaction, writing only what differs from the state it
// was last put at.
func (r *Rewinder) To(at int64) error {
	day := Day(time.UnixMilli(at))
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exists := map[string]bool{}
	for _, id := range r.h.ids {
		m := r.h.mems[id]
		want := m.phase(at, day)
		exists[id] = want.version >= 0
		if want == r.mems[id] {
			continue
		}
		if err := r.putMemory(tx, id, m, want); err != nil {
			return fmt.Errorf("rewind %s: %w", id, err)
		}
		r.mems[id] = want
	}
	for i, l := range r.h.links {
		var want int8
		switch {
		case l.created > at || !exists[l.a] || !exists[l.b]:
		case l.removed != 0 && l.removed <= at:
			want = 2
		default:
			want = 1
		}
		if want == r.links[i] {
			continue
		}
		if err := r.putLink(tx, l, want); err != nil {
			return err
		}
		r.links[i] = want
	}
	for i, e := range r.h.events {
		want := e.at <= at
		if want == r.events[i] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM events WHERE id=?`, e.id); err != nil {
			return err
		}
		if want {
			if err := insertRow(tx, "events", r.h.cols["events"], e.row); err != nil {
				return err
			}
		}
		r.events[i] = want
	}
	return tx.Commit()
}

func (r *Rewinder) putMemory(tx *sql.Tx, id string, m *memHistory, p memPhase) error {
	for _, q := range []string{`DELETE FROM memories WHERE id=?`, `DELETE FROM memory_versions WHERE memory_id=?`,
		`DELETE FROM sources WHERE memory_id=?`, `DELETE FROM sightings WHERE memory_id=?`} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	if p.version < 0 {
		return unindexTx(tx, id)
	}
	if err := insertRow(tx, "memories", r.h.cols["memories"], r.h.rowAt(m, p.version)); err != nil {
		return err
	}
	for _, v := range m.versions[:p.version+1] {
		if err := insertRow(tx, "memory_versions", r.h.cols["memory_versions"], v.row); err != nil {
			return err
		}
	}
	for _, s := range m.sources[:p.sources] {
		if err := insertRow(tx, "sources", r.h.cols["sources"], s.row); err != nil {
			return err
		}
	}
	for _, s := range m.sightings[:p.sightings] {
		if err := insertRow(tx, "sightings", r.h.cols["sightings"], s.row); err != nil {
			return err
		}
	}
	return indexTx(tx, id)
}

func (r *Rewinder) putLink(tx *sql.Tx, l linkHistory, want int8) error {
	if _, err := tx.Exec(`DELETE FROM memory_links WHERE id=?`, l.id); err != nil {
		return err
	}
	if want == 0 {
		return nil
	}
	row := slices.Clone(l.row)
	if want == 1 {
		for i, c := range r.h.cols["memory_links"] {
			switch c {
			case "removed_at":
				row[i] = int64(0)
			case "removed_origin", "removed_reason":
				row[i] = ""
			}
		}
	}
	return insertRow(tx, "memory_links", r.h.cols["memory_links"], row)
}

func insertRow(tx *sql.Tx, table string, cols []string, row []any) error {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	_, err := tx.Exec(`INSERT INTO `+table+`(`+strings.Join(cols, ",")+`) VALUES(`+marks+`)`, row...)
	return err
}

// unindexTx drops the search index rows of a memory that is no longer in the DB.
func unindexTx(tx *sql.Tx, id string) error {
	var rowid int64
	err := tx.QueryRow(`SELECT rowid FROM memory_index WHERE memory_id=?`, id).Scan(&rowid)
	switch {
	case noIndexTable(err):
		return nil
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		if _, err := tx.Exec(`DELETE FROM memory_fts WHERE rowid=?`, rowid); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM memory_index WHERE rowid=?`, rowid); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM memory_index_late WHERE memory_id=?`, id)
	if noIndexTable(err) {
		return nil
	}
	return err
}
