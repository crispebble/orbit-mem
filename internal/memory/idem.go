package memory

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// 멱등 저장(계획 D9·M7, 설계 6.1). 저장을 받는 쪽이 준 키(idempotencyKey)와 인자 해시를 save_requests에 남겨
// 같은 키로 다시 와도 한 번만 적용한다. 키 확인과 기록은 기억을 쓰는 같은 트랜잭션에서 한다: 쓰기 연결은
// BEGIN IMMEDIATE(_txlock=immediate)라 같은 키의 두 연결은 차례로 지나고, 뒤의 것은 앞의 기록을 본다.
// 적용된 것(저장, update, supersede, skip)만 기록한다. 실패한 쓰기는 트랜잭션과 함께 기록도 사라진다.
// 다시 오면 쓰지 않고 기록한 기억을 지금 상태로 돌려준다. 같은 키에 다른 인자(해시)나 다른 동작이면 충돌이다.
// 키는 부르는 쪽(호스트)과 묶지 않는다. 오래된 줄은 하루 정리가 SaveRequestDays일 뒤에 지운다.
//
// save_requests 표가 없는 DB(판 4 사본으로 복구된 뒤 이미 떠 있던 프로세스, H3)에서는 키를 기록하지 않고
// 저장한다: 저장을 실패시키면 기억을 잃고, 다음 쓰기 열기가 이전으로 표를 만든다. 그 사이 같은 인자의 재시도는
// 같은 제목 단축이 새 기억을 만들지 않게 막는다(출처·날은 더해질 수 있다).

// Idem is the idempotency key of a write and the hash of the arguments that came with it.
type Idem struct {
	Key  string
	Hash string
}

// Actions a save request records.
const (
	ApplySave      = "save"      // Save(바로 저장, 같은 제목 단축·같은 주제 대체 포함)
	ApplyUpdate    = "update"    // Update로 고른 기억을 고침
	ApplySupersede = "supersede" // Save + Replaces: 고른 기억을 낡게 하고 새로 저장
	ApplySkip      = "skip"      // 아무것도 쓰지 않음(고른 기억을 돌려줌)
)

// SaveRequest is an applied write recorded under its idempotency key.
type SaveRequest struct {
	Key      string
	Hash     string
	MemoryID string // 저장·고친·건너뛴 기억(supersede면 새 판)
	Action   string
	At       int64 // 적용한 시각(ms)
}

// ErrIdempotencyConflict is a write that comes again with an applied key but other arguments.
var ErrIdempotencyConflict = i18n.Err("memory.err.idempotency_conflict")

// SaveRequestDays is how many days an applied key is kept.
const SaveRequestDays = 30

// maxIdemKey is the longest idempotency key in bytes.
const maxIdemKey = 200

func (i *Idem) validate() error {
	if i == nil {
		return nil
	}
	if strings.TrimSpace(i.Key) == "" || len(i.Key) > maxIdemKey || !utf8.ValidString(i.Key) || i.Hash == "" {
		return i18n.Errorf("memory.err.idempotency_key", ErrInvalid, maxIdemKey)
	}
	return nil
}

// noSaveRequests says whether err is SQLite's "no such table" for save_requests (H3).
func noSaveRequests(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: save_requests")
}

func saveRequestTx(q querier, key string) (SaveRequest, bool, error) {
	r := SaveRequest{Key: key}
	var at string
	err := q.QueryRow(`SELECT args_hash, memory_id, action, at FROM save_requests WHERE key=?`, key).Scan(&r.Hash, &r.MemoryID, &r.Action, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows), noSaveRequests(err):
		return SaveRequest{}, false, nil
	case err != nil:
		return SaveRequest{}, false, err
	}
	r.At, _ = strconv.ParseInt(at, 10, 64)
	return r, true, nil
}

// replayTx is the earlier application of idem for a write doing action, or nil when the key is new (or
// there is no key). The same key with another hash or action is ErrIdempotencyConflict.
func replayTx(q querier, idem *Idem, action string) (*SaveRequest, error) {
	if idem == nil {
		return nil, nil
	}
	r, ok, err := saveRequestTx(q, idem.Key)
	if err != nil || !ok {
		return nil, err
	}
	if r.Hash != idem.Hash || r.Action != action {
		return nil, ErrIdempotencyConflict
	}
	return &r, nil
}

// recordTx keeps the applied write under its key in the write's transaction. Without the table (H3) the
// write goes on unrecorded.
func recordTx(tx execQuerier, idem *Idem, memoryID, action string, now int64) error {
	if idem == nil {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO save_requests(key, args_hash, memory_id, action, at) VALUES(?,?,?,?,?)`,
		idem.Key, idem.Hash, memoryID, action, strconv.FormatInt(now, 10))
	if noSaveRequests(err) {
		return nil
	}
	return err
}

// SaveRequest reads the applied write recorded under key.
func (s *Store) SaveRequest(key string) (SaveRequest, bool, error) { return saveRequestTx(s.db, key) }

// RecordSkip records under idem that the writer chose to save nothing and keep memory id as it is. It
// writes nothing else and returns the memory now, with the earlier application when the key came before.
func (s *Store) RecordSkip(idem Idem, id string) (Memory, *SaveRequest, error) {
	if err := idem.validate(); err != nil {
		return Memory{}, nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Memory{}, nil, err
	}
	defer tx.Rollback()
	replay, err := replayTx(tx, &idem, ApplySkip)
	if err != nil {
		return Memory{}, nil, err
	}
	if replay != nil {
		id = replay.MemoryID
	} else {
		if _, err := getTx(tx, id); err != nil {
			return Memory{}, nil, err
		}
		if err := recordTx(tx, &idem, id, ApplySkip, s.nowMs()); err != nil {
			return Memory{}, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Memory{}, nil, err
	}
	m, err := s.Get(id)
	return m, replay, err
}

// PruneSaveRequests drops the keys applied more than days ago and says how many it dropped.
func (s *Store) PruneSaveRequests(days int) (int, error) {
	cutoff := s.Now().AddDate(0, 0, -days).UnixMilli()
	res, err := s.db.Exec(`DELETE FROM save_requests WHERE CAST(at AS INTEGER) < ?`, cutoff)
	if noSaveRequests(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
