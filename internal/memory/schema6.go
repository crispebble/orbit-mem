package memory

import (
	"database/sql"
	"time"
)

// 스키마 6(명세 · orbit-mem 기억 정확성과 회상 품질 개선 6.3). 열과 표를 더하기만 한다: 스키마 5 실행 파일은 버전이 높은
// DB를 거절하므로(ErrSchemaNewer) 되돌릴 때는 이행 전에 남긴 사본(Open의 before-schema)으로 돌아간다.
//
//   - sighting_sessions: 다시 나온 기록을 (기억, 세션, 날)로 둔다. AI가 배운 것은 세션과 날이 모두 다른 두 번이어야
//     확정한다(E-1). 옛 표 sightings는 (기억, 날)만 알아 그대로 두고 계속 쓴다.
//   - utterances·utterance_shingles: 사용자 발언의 조각 해시(원문은 남기지 않는다). 사용자 말 출처 대조(S-4).
//   - supersede_reverts: 되돌린 자동 대체 쌍. 같은 쌍을 다시 자동 대체하지 않는다(C-3).
//   - memories.aliases(별칭, Q-1), sources.rechecked(서버가 출처를 다시 확인함, C-1)는 addColumns가 더한다.
const schema6 = `
CREATE TABLE IF NOT EXISTS sighting_sessions (memory_id TEXT NOT NULL, session TEXT NOT NULL, day TEXT NOT NULL, at INTEGER NOT NULL,
  PRIMARY KEY(memory_id, session, day)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS utterances (id TEXT PRIMARY KEY, host TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS utterances_at ON utterances(at);
CREATE TABLE IF NOT EXISTS utterance_shingles (hash TEXT NOT NULL, utterance_id TEXT NOT NULL,
  PRIMARY KEY(hash, utterance_id)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS utterance_shingles_u ON utterance_shingles(utterance_id);
CREATE TABLE IF NOT EXISTS supersede_reverts (old_id TEXT NOT NULL, new_id TEXT NOT NULL, ledger_id TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL, PRIMARY KEY(old_id, new_id)) WITHOUT ROWID;
PRAGMA user_version=6;
`

// legacySession is the session of the sightings schema 5 kept by (memory, day) only, and of a later save
// that brings no session (recordSightingTx). They count as one session, so two old days, or two saves of an
// unknown session, alone never make "seen in another session".
const legacySession = "legacy"

// backfill6Tx fills what a DB below schema 6 lacks, in migrate's transaction, so a failure leaves the DB as
// it was and the next write open tries again. Migrate calls it only when the version it read under the write
// lock is below 6, so of two processes opening the same old DB together only the first fills.
//
//  1. Every old sighting goes into sighting_sessions under legacySession, at 0.
//  2. A live (candidate or confirmed), not volatile memory without a review date gets the last check plus
//     DefaultReviewDays (L-1): the latest of its creation, its last change and its last sighting day.
//     State, version and updated_at stay (E-3); nothing goes into memory_versions.
//  3. Every memory is marked late and then indexed again, so the index of every memory follows this
//     binary's text rules (Q-3). The marks are put here, not left to the UPDATE trigger of step 2: that
//     trigger marks only the memories step 2 changed.
func backfill6Tx(tx *sql.Tx) error {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO sighting_sessions(memory_id, session, day, at)
		SELECT memory_id, ?, day, 0 FROM sightings`, legacySession); err != nil {
		return err
	}
	if err := fillReviewAtTx(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO memory_index_late(memory_id) SELECT id FROM memories`); err != nil {
		return err
	}
	_, err := fillStaleTx(tx, 0)
	return err
}

// fillReviewAtTx is step 2 of backfill6Tx. A sighting day counts from its local midnight (Day writes the
// local calendar day); a day that does not parse is left out.
func fillReviewAtTx(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT m.id, max(m.created_at, m.updated_at), (SELECT max(s.day) FROM sightings s WHERE s.memory_id = m.id)
		FROM memories m WHERE m.review_at = 0 AND m.retention <> ? AND m.state IN (?, ?)`, RetentionVolatile, StateCandidate, StateConfirmed)
	if err != nil {
		return err
	}
	type due struct {
		id string
		at int64
	}
	var dues []due
	for rows.Next() {
		var id string
		var last int64
		var day sql.NullString
		if err := rows.Scan(&id, &last, &day); err != nil {
			rows.Close()
			return err
		}
		if day.Valid {
			if d, err := time.ParseInLocation("2006-01-02", day.String, time.Local); err == nil {
				last = max(last, d.UnixMilli())
			}
		}
		dues = append(dues, due{id, last + DefaultReviewDays*dayMs})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range dues {
		if _, err := tx.Exec(`UPDATE memories SET review_at=? WHERE id=?`, d.at, d.id); err != nil {
			return err
		}
	}
	return nil
}
