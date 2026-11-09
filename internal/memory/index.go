package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// 검색 색인(판 5, 설계 7절, ORBIT-25). memory_index는 기억마다 한 줄이고 그 rowid가 memory_fts의 rowid다
// (memories.id는 TEXT 기본 키라 VACUUM에 암묵 rowid가 바뀌므로 쓰지 않는다). memory_fts에는 Go가 만든 가공 글
// (낱말 + 한·중·일 바이그램, textindex.go)을 넣는다: title = 제목, keys = 주제 + 태그, body = 본문.
// SQL은 바이그램을 만들지 못하므로 색인 글은 기억을 쓰는 같은 트랜잭션에서 Go가 맞춘다. 상태와 무관하게 모든
// 기억을 넣는다(거르기는 읽을 때 memories와 JOIN).
//
// 늦은 표시(memory_index_late): memories에 줄이 들어오거나 줄이 고쳐지면 트리거가 그 기억 id를 표시한다. 트리거는 DB에 있으므로 색인을 모르는 옛 실행 파일(0.16 이하)이 판 5 DB에 쓴 기억도 표시된다. 이
// 실행 파일은 색인을 맞추는 같은 트랜잭션에서 표시를 지운다(indexTx). 그래서 쓰기 끝과 찾기는 늦은 줄을 표시만
// 읽어 찾고(늦은 줄 수에 비례), 늦은 줄이 없으면 기억 수와 무관하다. 표시 없이 깨진 색인은 찾기의 개수 비교 보호
// 장치(indexCountsSQL)가 잡아 그 찾기만 전체 검사로 훑고, doctor(IndexHealth의 Broken)가 알린다.
//
// indexed_at은 색인할 때의 memories.updated_at(10진 글)이다. 전체 검사(staleSQL)는 표시 말고도 색인 줄이 없거나
// indexed_at ≠ updated_at이거나 memory_index 줄의 memory_fts 줄이 없는(깨진 색인) 기억을 늦은 줄로 본다. 기억
// 전부를 훑으므로 쓰기·찾기마다 돌리지 않고 doctor(StaleIndexCount), 채우기(FillIndex: migrate·install·복구),
// 다시 만들기(Reindex), 표시가 없던 판 5 DB의 이전(표시 채우기)에서만 돌린다.
// 늦은 줄은 쓰기 트랜잭션 끝(writeTx·save)에서 한 번에 staleFillLimit개까지 채운다. 열기·읽기 op는 채우지 않는다.

const indexSchema = `
CREATE TABLE IF NOT EXISTS memory_index (rowid INTEGER PRIMARY KEY, memory_id TEXT NOT NULL UNIQUE, indexed_at TEXT NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(title, keys, body, tokenize='unicode61');
CREATE TABLE IF NOT EXISTS memory_index_late (memory_id TEXT PRIMARY KEY) WITHOUT ROWID;
CREATE TRIGGER IF NOT EXISTS memory_index_late_insert AFTER INSERT ON memories BEGIN
  INSERT INTO memory_index_late(memory_id) SELECT NEW.id WHERE NOT EXISTS (SELECT 1 FROM memory_index_late WHERE memory_id = NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS memory_index_late_update AFTER UPDATE ON memories BEGIN
  INSERT INTO memory_index_late(memory_id) SELECT NEW.id WHERE NOT EXISTS (SELECT 1 FROM memory_index_late WHERE memory_id = NEW.id);
END;
CREATE TABLE IF NOT EXISTS save_requests (
  key TEXT PRIMARY KEY, args_hash TEXT NOT NULL, memory_id TEXT NOT NULL, action TEXT NOT NULL, at TEXT NOT NULL);
PRAGMA user_version=5;
`

// 늦은 표시 트리거(위 두 개)는 memories의 모든 INSERT·UPDATE에 표시를 남긴다. 어느 칸이 바뀌었는지 보지 않는다:
// 색인할 칸이 늘어도 고칠 목록이 없고, 열 이름에 기대지 않아 옛 판의 열 꼴(DROP COLUMN으로 만든 시험 DB 포함)과
// 무관하다. 이 실행 파일의 쓰기는 모두 같은 트랜잭션에서 indexTx가 표시를 지우고, 옛 실행 파일의 쓰기는 늘
// updated_at을 올리므로 늦은 줄이 아닌 표시는 남지 않는다. 표시를 넣는 문장은 충돌할 일이 없어(NOT EXISTS) 바깥
// 문장의 충돌 처리 절(옛 실행 파일의 UPDATE OR ROLLBACK 같은)이 이 문장을 실패로 바꾸지 않는다.

// lateObjectsSQL counts the late mark table and its two triggers (3 when all are there).
const lateObjectsSQL = `SELECT COUNT(*) FROM sqlite_master WHERE (type='table' AND name='memory_index_late')
	OR (type='trigger' AND name IN ('memory_index_late_insert','memory_index_late_update'))`

// staleFillLimit is how many late index rows one write transaction fills besides its own.
const staleFillLimit = 200

// staleSQL is the full check: the memories marked late, or whose index row is missing or older than the
// memory, or whose FTS row is gone (a broken index), newest first. It reads every memory.
const staleSQL = `SELECT m.id FROM memories m LEFT JOIN memory_index i ON i.memory_id = m.id
	WHERE i.memory_id IS NULL OR i.indexed_at <> CAST(m.updated_at AS TEXT)
	OR NOT EXISTS (SELECT 1 FROM memory_fts f WHERE f.rowid = i.rowid)
	OR EXISTS (SELECT 1 FROM memory_index_late l WHERE l.memory_id = m.id) ORDER BY m.updated_at DESC, m.id`

// lateSQL lists the marked memories, newest first; a mark whose memory is gone has gone = 1. It reads the
// marks only (the LEFT JOIN keeps them as the outer loop).
const lateSQL = `SELECT l.memory_id, m.id IS NULL FROM memory_index_late l LEFT JOIN memories m ON m.id = l.memory_id
	ORDER BY m.updated_at DESC, l.memory_id LIMIT ?`

type execQuerier interface {
	querier
	Exec(string, ...any) (sql.Result, error)
}

// noIndexTable says whether err is SQLite's "no such table" for the index tables: a DB a schema 4 copy
// was restored into while this process had it open. Checking the error of the first statement costs
// nothing on the usual path, unlike reading sqlite_master in every transaction.
func noIndexTable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// "memory_index"는 "memory_index_late"의 앞부분이기도 하다: 표시 표가 없는 DB(표시 전 판 5 사본으로 복구한
	// 직후)도 색인이 없는 DB처럼 다룬다(쓰기는 색인을 건너뛰고 다음 쓰기 열기가 표시를 채우며, 찾기는 모두 훑는다).
	return strings.Contains(msg, "no such table: memory_index") || strings.Contains(msg, "no such table: memory_fts")
}

// indexFields are the three FTS texts of a memory.
func indexFields(title, topic, tags, body string) (string, string, string) {
	var ts []string
	if json.Unmarshal([]byte(tags), &ts) != nil {
		ts = []string{tags}
	}
	return indexText(title), indexText(strings.Join(append([]string{topic}, ts...), " ")), indexText(body)
}

// indexTx makes memory id's index row match the memory as it is in tx. Without the index tables it does nothing.
func indexTx(tx execQuerier, id string) error {
	var rowid sql.NullInt64
	var marked int
	// 첫 문장이 세 표를 함께 본다: 어느 하나라도 없으면 아무것도 쓰기 전에 건너뛴다.
	err := tx.QueryRow(`SELECT (SELECT rowid FROM memory_index WHERE memory_id=?), (SELECT COUNT(*) FROM memory_fts WHERE rowid=0),
		(SELECT COUNT(*) FROM memory_index_late WHERE memory_id=?)`, id, id).Scan(&rowid, new(int), &marked)
	if noIndexTable(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var title, topic, tags, body, at string
	if err := tx.QueryRow(`SELECT title, topic, tags, body, CAST(updated_at AS TEXT) FROM memories WHERE id=?`, id).Scan(&title, &topic, &tags, &body, &at); err != nil {
		return err
	}
	if rowid.Valid {
		if _, err := tx.Exec(`DELETE FROM memory_fts WHERE rowid=?`, rowid.Int64); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE memory_index SET indexed_at=? WHERE rowid=?`, at, rowid.Int64); err != nil {
			return err
		}
	} else {
		res, err := tx.Exec(`INSERT INTO memory_index(memory_id, indexed_at) VALUES(?,?)`, id, at)
		if err != nil {
			return err
		}
		if rowid.Int64, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	ti, ke, bo := indexFields(title, topic, tags, body)
	if _, err := tx.Exec(`INSERT INTO memory_fts(rowid, title, keys, body) VALUES(?,?,?,?)`, rowid.Int64, ti, ke, bo); err != nil {
		return err
	}
	if marked > 0 {
		_, err = tx.Exec(`DELETE FROM memory_index_late WHERE memory_id=?`, id)
	}
	return err
}

// fillStaleTx indexes up to limit late memories by the full check (all of them when limit is 0 or less) and
// says how many. It also drops the marks of memories that are gone. Without the index tables it does nothing.
func fillStaleTx(tx execQuerier, limit int) (int, error) {
	ids, err := staleIDs(tx, limit)
	if noIndexTable(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := indexTx(tx, id); err != nil {
			return 0, err
		}
	}
	_, err = tx.Exec(`DELETE FROM memory_index_late WHERE memory_id NOT IN (SELECT id FROM memories)`)
	return len(ids), err
}

// fillLateTx indexes up to limit marked memories and drops the marks of memories that are gone. It reads
// the marks only. Without the index tables it does nothing.
func fillLateTx(tx execQuerier, limit int) error {
	rows, err := tx.Query(lateSQL, limit)
	if noIndexTable(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var ids, gone []string
	for rows.Next() {
		var id string
		var g bool
		if err := rows.Scan(&id, &g); err != nil {
			rows.Close()
			return err
		}
		if g {
			gone = append(gone, id)
		} else {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range gone {
		if _, err := tx.Exec(`DELETE FROM memory_index_late WHERE memory_id=?`, id); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if err := indexTx(tx, id); err != nil {
			return err
		}
	}
	return nil
}

// markStaleTx marks every memory the full check finds late. Migrate runs it when the marks were missing
// (a schema 5 DB made before them, or their table or a trigger was dropped): writes made then left no mark.
func markStaleTx(tx execQuerier) error {
	_, err := tx.Exec(`INSERT INTO memory_index_late(memory_id) SELECT s.id FROM (` + staleSQL + `) s
		WHERE NOT EXISTS (SELECT 1 FROM memory_index_late l WHERE l.memory_id = s.id)`)
	return err
}

// syncIndexTx is the end of a write: the written memory's row, then up to staleFillLimit marked rows.
func syncIndexTx(tx execQuerier, id string) error {
	if err := indexTx(tx, id); err != nil {
		return err
	}
	return fillLateTx(tx, staleFillLimit)
}

func staleIDs(q querier, limit int) ([]string, error) {
	query := staleSQL
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// StaleIndexCount is how many memories are late by the full check (staleSQL): marked late, with no index
// row, one older than the memory, or one whose FTS row is gone. FillIndex indexes exactly these. A search
// reads the marked ones by scanning, beside the index; the unmarked ones (a broken index) it scans only while
// the row counts disagree (indexCountsSQL), and doctor reports them apart (IndexHealth). Without the index
// tables every memory is late. It only reads, so it works on a read-only store.
func (s *Store) StaleIndexCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM (` + staleSQL + `)`).Scan(&n)
	if noIndexTable(err) {
		return s.Count()
	}
	return n, err
}

// FillIndex indexes up to limit late memories (all when limit is 0 or less) in one write transaction and
// says how many. A DB without the index tables is left alone (0).
func (s *Store) FillIndex(limit int) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, err := fillStaleTx(tx, limit)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// errNoIndex is why Reindex refuses a DB without the index tables: the next write open migrates it.
var errNoIndex = errors.New("no search index tables (schema older than 5)")

// Reindex empties the search index and builds it again from every memory in one write transaction, and
// says how many memories it indexed. Running it again gives the same index. A DB without the index tables
// is an error: the next write open migrates and fills it.
func (s *Store) Reindex() (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM memory_fts`); err != nil {
		if noIndexTable(err) {
			err = errNoIndex
		}
		return 0, i18n.Errorf("memory.err.db_upgrade", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_index`); err != nil {
		if noIndexTable(err) {
			err = errNoIndex
		}
		return 0, i18n.Errorf("memory.err.db_upgrade", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_index_late`); err != nil { // 모두 다시 색인하므로 늦은 표시도 없다
		if noIndexTable(err) {
			err = errNoIndex
		}
		return 0, i18n.Errorf("memory.err.db_upgrade", err)
	}
	n, err := fillStaleTx(tx, 0)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// 개수 비교 보호 장치(찾기). 찾기는 늦은 표시와 색인만 보므로, 표시 없이 색인이 깨진 기억(memory_fts 줄이나
// memory_index 줄이 지워짐)은 그대로면 놓친다. 그래서 찾기는 같은 읽기 트랜잭션에서 줄 수를 견준다(indexCountsSQL):
//
//	기억 수 − 색인 줄 없이 표시만 있는 기억 수 = 색인 줄 수,   FTS 줄 수 = 색인 줄 수
//
// 색인이 온전하면(표시 없는 기억마다 색인 줄과 FTS 줄이 있고, 남는 줄이 없으면) 늘 맞는다. 옛 실행 파일이 넣은
// 기억은 색인 줄이 없어도 표시가 있어 왼쪽에서 빠지므로 늦은 줄만으로는 어긋나지 않는다. 어긋나면 그 찾기만 전체
// 검사(staleSQL)로 늦은 줄을 지금 글로 훑어 결과를 맞춘다(기억 수에 비례해 느리다). 어긋남을 고치는 것은
// doctor(index-broken) → migrate --reindex다.
//
// 못 잡는 것(찾기가 놓칠 수 있고 doctor의 IndexHealth가 센다): 표시 없이 indexed_at만 어긋난 색인 줄(수가 그대로다),
// 빠진 줄과 남는 줄이 같은 수로 겹친 것(기억 하나의 색인 줄이 지워지고 기억 없는 색인 줄이 하나 있음 등), FTS 글만
// 바뀐 것. 이 실행 파일과 옛 실행 파일의 쓰기로는 생기지 않는다(옛 실행 파일의 memories 쓰기는 트리거가 표시한다).
//
// FTS 줄 수는 memory_fts_docsize의 줄 수로 센다. FTS5 공식 문서(fts5.html 9.3 "Document Sizes Table")는 FTS 줄마다
// 같은 rowid의 %_docsize 줄이 하나 있다고 적는다(columnsize=0이 아닐 때. memory_fts는 기본값이다). docsize 줄은 몇
// 바이트라 COUNT(*)가 걷는 쪽수가 가장 적다. memory_fts의 COUNT(*)는 %_content(본문 전부)를 읽는다. 내부 표는 읽기만
// 한다. 묶어 쓰는 SQLite(modernc)가 바뀌어 이 관계가 깨지면 TestIndexGuard_docsize_줄은_FTS_줄과_rowid가_같다가 먼저
// 깨지고, 표가 없으면 오류가 noIndexTable이라 찾기는 모두 훑는다(느리지만 맞다).
//
// 값: COUNT(*)는 표의 b-tree 쪽만 걷고 줄을 풀지 않는다. 표시 쪽은 표시 수에 비례한다.
const indexCountsSQL = `SELECT (SELECT COUNT(*) FROM memories), (SELECT COUNT(*) FROM memory_index),
	(SELECT COUNT(*) FROM memory_fts_docsize),
	(SELECT COUNT(*) FROM memory_index_late l CROSS JOIN memories m ON m.id = l.memory_id
		WHERE NOT EXISTS (SELECT 1 FROM memory_index i WHERE i.memory_id = l.memory_id))`

// indexCounts says how many memories there are (the IDF's N) and whether the row counts of the index agree
// (see indexCountsSQL). Without the index tables the error is noIndexTable.
func indexCounts(q querier) (total int, agree bool, err error) {
	var indexed, fts, lateNoRow int
	if err := q.QueryRow(indexCountsSQL).Scan(&total, &indexed, &fts, &lateNoRow); err != nil {
		return 0, false, err
	}
	return total, total-lateNoRow == indexed && fts == indexed, nil
}

// IndexHealth splits the full check for doctor.
type IndexHealth struct {
	// Late is the marked memories (an older binary wrote them, or the marks were filled by migrate): a search
	// reads them by scanning, so it finds them; the next write or migrate indexes them.
	Late int
	// Broken is the index damage without a mark: unmarked memories with no index row, no FTS row or an
	// index row older than the memory; index rows without their memory; marked memories whose index row lost
	// its FTS row; FTS rows without an index row. A search scans every memory while the row counts disagree
	// and can miss what they do not show (see indexCountsSQL); migrate --reindex mends it.
	Broken int
}

// indexHealthSQL reads every memory and index row: doctor only.
const indexHealthSQL = `SELECT
	(SELECT COUNT(*) FROM memory_index_late l CROSS JOIN memories m ON m.id = l.memory_id),
	(SELECT COUNT(*) FROM (` + staleSQL + `) s WHERE NOT EXISTS (SELECT 1 FROM memory_index_late l WHERE l.memory_id = s.id))
	+ (SELECT COUNT(*) FROM memory_index i WHERE NOT EXISTS (SELECT 1 FROM memories m WHERE m.id = i.memory_id))
	+ (SELECT COUNT(*) FROM memory_index i JOIN memory_index_late l ON l.memory_id = i.memory_id
		WHERE EXISTS (SELECT 1 FROM memories m WHERE m.id = i.memory_id) AND NOT EXISTS (SELECT 1 FROM memory_fts f WHERE f.rowid = i.rowid))
	+ (SELECT COUNT(*) FROM memory_fts f WHERE NOT EXISTS (SELECT 1 FROM memory_index i WHERE i.rowid = f.rowid))`

// IndexHealth counts the late rows and the broken index (see IndexHealth) in one read. Without the index
// tables every memory is late (a search scans them all). It only reads, so it works on a read-only store.
func (s *Store) IndexHealth() (IndexHealth, error) {
	var h IndexHealth
	err := s.db.QueryRow(indexHealthSQL).Scan(&h.Late, &h.Broken)
	if noIndexTable(err) {
		n, err := s.Count()
		return IndexHealth{Late: n}, err
	}
	return h, err
}
