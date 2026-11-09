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
// 트리거는 두지 않는다(SQL은 바이그램을 만들지 못한다). 색인은 기억을 쓰는 같은 트랜잭션에서 맞추고, 상태와
// 무관하게 모든 기억을 넣는다(거르기는 읽을 때 memories와 JOIN).
//
// indexed_at은 색인할 때의 memories.updated_at(10진 글)이다. 줄이 없거나 indexed_at ≠ updated_at이면 늦은
// 기억이다: 색인을 모르는 옛 실행 파일(0.16 이하)이 판 5 DB에 쓴 기억이고, 그 쓰기는 모두 updated_at을 올린다.
// memory_index 줄은 있는데 그 rowid의 memory_fts 줄이 없는 기억(깨진 색인)도 늦은 기억이다.
// 늦은 줄은 쓰기 트랜잭션 끝(writeTx·save)에서 한 번에 staleFillLimit개까지 채운다. 열기·읽기 op는 채우지 않는다.

const indexSchema = `
CREATE TABLE IF NOT EXISTS memory_index (rowid INTEGER PRIMARY KEY, memory_id TEXT NOT NULL UNIQUE, indexed_at TEXT NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(title, keys, body, tokenize='unicode61');
CREATE TABLE IF NOT EXISTS save_requests (
  key TEXT PRIMARY KEY, args_hash TEXT NOT NULL, memory_id TEXT NOT NULL, action TEXT NOT NULL, at TEXT NOT NULL);
PRAGMA user_version=5;
`

// staleFillLimit is how many late index rows one write transaction fills besides its own.
const staleFillLimit = 200

// staleSQL lists the memories whose index row is missing or older than the memory, or whose FTS row is gone
// (a broken index: the search would miss it with no late row to say so), newest first. The FTS check is a
// rowid lookup per memory.
const staleSQL = `SELECT m.id FROM memories m LEFT JOIN memory_index i ON i.memory_id = m.id
	WHERE i.memory_id IS NULL OR i.indexed_at <> CAST(m.updated_at AS TEXT)
	OR NOT EXISTS (SELECT 1 FROM memory_fts f WHERE f.rowid = i.rowid) ORDER BY m.updated_at DESC, m.id`

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
	// 첫 문장이 두 표를 함께 본다: 어느 하나라도 없으면 아무것도 쓰기 전에 건너뛴다.
	err := tx.QueryRow(`SELECT (SELECT rowid FROM memory_index WHERE memory_id=?), (SELECT COUNT(*) FROM memory_fts WHERE rowid=0)`, id).Scan(&rowid, new(int))
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
	_, err = tx.Exec(`INSERT INTO memory_fts(rowid, title, keys, body) VALUES(?,?,?,?)`, rowid.Int64, ti, ke, bo)
	return err
}

// fillStaleTx indexes up to limit late memories (all of them when limit is 0 or less) and says how many.
// Without the index tables it does nothing.
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
	return len(ids), nil
}

// syncIndexTx is the end of a write: the written memory's row, then up to staleFillLimit late rows.
func syncIndexTx(tx execQuerier, id string) error {
	if err := indexTx(tx, id); err != nil {
		return err
	}
	_, err := fillStaleTx(tx, staleFillLimit)
	return err
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

// StaleIndexCount is how many memories are late: those with no index row, one older than the memory, or
// one whose FTS row is gone (staleSQL). A search reads them by scanning, beside the index. Without the index
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
	n, err := fillStaleTx(tx, 0)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
