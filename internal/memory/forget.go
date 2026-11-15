package memory

// 기억 완전 삭제(명세 · memory_forget 5.1·5.3·5.4). stale은 회상에서 빼기만 하지만 Forget은 기억과 딸린 행을 DB에서
// 지운다. 지키는 것:
//   - 전부 아니면 전무다: 검사·지우기·forgotten 기록이 한 트랜잭션이고, 없는 id가 하나라도 있으면 아무것도 지우지 않는다.
//   - 사람이 아닌 쪽은 사용자 말이 있어야 지우고, 자동화는 늘 거절한다(하루 정리·자동 변경은 기억을 지우지 못한다).
//   - 지운 id와 legacy_id는 forgotten에 남아 들여오기·옮기기·저장 재시도로 되살아나지 않는다. 제목·본문은 남기지 않는다.
//   - 지운 기억이 대체한 옛 판은 낡음 그대로 두고 대체 표시만 비운다. 옛 판의 판 이력은 고쳐 쓰지 않는다(명세 7절).
// 되돌리기: 이 동작은 되돌릴 수 없다. 잘못 지웠으면 그 전 백업 사본으로 backup restore한다(명세 5.4).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// forgottenSchema keeps the ids of forgotten memories. It adds a table without raising user_version: a
// raised version would keep older binaries from opening the DB (명세 5.4).
const forgottenSchema = `
CREATE TABLE IF NOT EXISTS forgotten (
  id TEXT PRIMARY KEY, legacy_id TEXT, at INTEGER NOT NULL, actor TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '', quote TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS forgotten_legacy ON forgotten(legacy_id) WHERE legacy_id IS NOT NULL;
`

// Forget limits (명세 5.1).
const (
	maxForgetIDs   = 20
	maxForgetRunes = 500 // userQuote·reason 각각
)

// The Korean text Forget and the later steps of forgetting store in the DB. Messages returned to a caller
// come from the catalog instead.
const (
	forgetSuccessorGone = "대체한 기억을 지웠다"
	forgetLedgerChange  = "지운 기억 %s에 대한 변경"
	forgetProposalTitle = "지운 기억 %s에 대한 제안"
)

// How Forget clears a table.
const (
	forgetDelete = "delete" // 그 열이 지울 id인 행을 지운다
	forgetCustom = "custom" // Forget이 따로 처리한다(forgetCustomTx)
)

// idColumn is a column of a table that holds memory ids.
type idColumn struct{ table, column string }

type forgetTable struct {
	idColumn
	how string
}

// forgetTables are the rows Forget deletes (명세 5.3 「행 삭제」). forget_guard_test.go fails when a table
// with a memory id column is in neither this list nor keepTables.
var forgetTables = []forgetTable{
	{idColumn{"memories", "id"}, forgetDelete},
	{idColumn{"memory_versions", "memory_id"}, forgetDelete},
	{idColumn{"sources", "memory_id"}, forgetDelete},
	{idColumn{"sightings", "memory_id"}, forgetDelete},
	{idColumn{"memory_links", "a"}, forgetDelete}, // 뗀 줄(removed_at≠0)도 지운다
	{idColumn{"memory_links", "b"}, forgetDelete},
	{idColumn{"judgments", "memory_id"}, forgetDelete},
	{idColumn{"audit_flags", "memory_id"}, forgetDelete},
	{idColumn{"audit_flags", "other_id"}, forgetDelete},
	{idColumn{"session_marks", "memory_id"}, forgetDelete},
	{idColumn{"memory_index", "memory_id"}, forgetCustom},      // unindexTx: memory_fts 줄도 함께
	{idColumn{"memory_index_late", "memory_id"}, forgetCustom}, // unindexTx
	{idColumn{"activity", "session"}, forgetCustom},            // 일지에서 옮긴 줄: JournalSession(id)
	{idColumn{"events", "data"}, forgetCustom},                 // correction·memory.used만
}

// keepTables are the memory id columns Forget leaves as they are (명세 5.3 「글만 비움」·「id만 남김」).
var keepTables = []idColumn{
	// 감사 목록과 자동 변경 되돌리기가 ledger JOIN proposals로 읽으므로 줄과 id는 남긴다. 제안의 제목·본문은 원장
	// 글과 함께 forgetProposalTitle 등으로 비운다(명세 5.3, 계획 Task 2).
	{"proposals", "memory_ids"},
	{"session_bundles", "memory_ids"}, // 이미 연 세션의 묶음 캐시를 깨지 않는다
	{"save_requests", "memory_id"},    // 같은 키의 재시도를 「지웠다」로 거절하는 근거. 30일 정리에 맡긴다
	{"forgotten", "id"},               // 되살아남을 막는 기록 자체
}

// ForgetInput asks to forget memories.
type ForgetInput struct {
	IDs    []string // 1~20개. 같은 id는 한 번만 센다
	Reason string   // 500자 이하, 비밀값 없이. forgotten에 남는다
	Who    Who      // 사람이 아니면 Who.UserQuote(500자 이하, 비밀값 없이)가 있어야 한다. 자동화는 늘 거절한다
}

// ForgetResult is what Forget did.
type ForgetResult struct {
	Forgotten []string // 지운 기억 id(준 순서, 중복 없이)
	Touched   []string // 지운 기억이 대체했던 옛 판으로 대체 표시를 비운 것(id 순)
}

// ForgetMissingError names the ids Forget did not find; nothing was forgotten. It is ErrNotFound.
type ForgetMissingError struct{ IDs []string }

func (e *ForgetMissingError) Error() string {
	return i18n.T("memory.forget.missing", strings.Join(e.IDs, ", "))
}

func (e *ForgetMissingError) Unwrap() error { return ErrNotFound }

// Forget deletes memories and every row that belongs to them in one transaction, and keeps their ids in
// forgotten. It cannot be undone (a backup copy is the way back).
//
// The person forgets without words; any other writer needs the user's words (in.Who.UserQuote), or the
// error is ErrForgetNeedsWords, which is ErrProtected too; an automation is refused with
// ErrForgetAutomation. 0 or more than 20 ids, or a quote or reason over 500 characters, is ErrInvalid; a
// quote or reason holding a secret is a *RejectError (RejectSecret). An id that is not there makes it a
// *ForgetMissingError and nothing is forgotten.
//
// A memory outside the set that the forgotten ones superseded stays stale: its superseded_by is cleared and
// its stale reason says the successor was forgotten, without a new version (ForgetResult.Touched). Ledger
// and proposal text, saved copies of the forgotten text elsewhere and backup copies are not touched here.
func (s *Store) Forget(in ForgetInput) (ForgetResult, error) {
	ids, err := in.check()
	if err != nil {
		return ForgetResult{}, err
	}
	now := s.nowMs()
	tx, err := s.db.Begin()
	if err != nil {
		return ForgetResult{}, err
	}
	defer tx.Rollback()
	legacy, err := legacyIDsTx(tx, ids)
	if err != nil {
		return ForgetResult{}, err
	}
	touched, err := clearSuccessorTx(tx, ids)
	if err != nil {
		return ForgetResult{}, err
	}
	// 원장·제안의 글은 행을 지우기 전에 비운다(forget_audit.go).
	if err := scrubAuditTx(tx, ids, now); err != nil {
		return ForgetResult{}, err
	}
	list, _ := json.Marshal(ids)
	for _, c := range forgetTables {
		if c.how == forgetCustom {
			err = forgetCustomTx(tx, c.table, ids)
		} else {
			_, err = tx.Exec(`DELETE FROM `+c.table+` WHERE `+c.column+` IN (SELECT value FROM json_each(?))`, string(list))
		}
		if err != nil {
			return ForgetResult{}, err
		}
	}
	for _, id := range ids {
		var lid any // 빈 legacy_id는 NULL: 색인이 NULL을 빼고, 옛 행의 legacy_id를 지우지 않는다
		if legacy[id] != "" {
			lid = legacy[id]
		}
		// 옛 판 실행 파일로 들여와 이미 forgotten에 있는 id가 다시 온 경우가 있다: 새 기록으로 덮는다.
		if _, err := tx.Exec(`INSERT INTO forgotten(id, legacy_id, at, actor, reason, quote) VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET legacy_id=COALESCE(excluded.legacy_id, forgotten.legacy_id), at=excluded.at,
			actor=excluded.actor, reason=excluded.reason, quote=excluded.quote`,
			id, lid, now, in.Who.actor(), strings.TrimSpace(in.Reason), in.Who.quote()); err != nil {
			return ForgetResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ForgetResult{}, err
	}
	return ForgetResult{Forgotten: ids, Touched: touched}, nil
}

// check validates the input before any transaction and gives the ids without repeats, in order.
func (in ForgetInput) check() ([]string, error) {
	// 개수는 중복을 빼기 전에 센다: 21개를 주면 같은 id여도 거절한다.
	if len(in.IDs) == 0 || len(in.IDs) > maxForgetIDs {
		return nil, i18n.Errorf("memory.forget.count", ErrInvalid, maxForgetIDs)
	}
	ids := make([]string, len(in.IDs))
	for i, id := range in.IDs {
		ids[i] = strings.TrimSpace(id)
	}
	if ids = uniq(ids); len(ids) == 0 {
		return nil, i18n.Errorf("memory.forget.count", ErrInvalid, maxForgetIDs)
	}
	switch {
	case in.Who.actor() == ActorAutomation:
		return nil, ErrForgetAutomation
	case !in.Who.human() && in.Who.quote() == "": // 알 수 없는 actor도 사람이 아니다
		return nil, forgetWordsError{}
	}
	for _, text := range []string{in.Who.UserQuote, in.Reason} {
		if utf8.RuneCountInString(text) > maxForgetRunes {
			return nil, i18n.Errorf("memory.forget.too_long", ErrInvalid, maxForgetRunes)
		}
		// forgotten에 지우려던 비밀이 다시 들어가지 않게 한다. memory_save의 다른 거부(지시문·진행 상태)는 「지워」 같은
		// 진짜 사용자 말을 막을 수 있어 쓰지 않는다(명세 5.1).
		if MaskSecrets(text) != text {
			return nil, &RejectError{Reason: RejectSecret}
		}
	}
	return ids, nil
}

// forgetWordsError is ErrForgetNeedsWords that is also ErrProtected, the family callers already treat as
// "bring the user's words". Wrapping ErrProtected with fmt.Errorf would put its text, which speaks of a
// memory holding the user's words, in front of a message about forgetting any memory.
type forgetWordsError struct{}

func (forgetWordsError) Error() string { return ErrForgetNeedsWords.Error() }

func (forgetWordsError) Unwrap() []error { return []error{ErrForgetNeedsWords, ErrProtected} }

// legacyIDsTx reads the legacy id of each memory ids, or returns a *ForgetMissingError for those not there.
func legacyIDsTx(tx *sql.Tx, ids []string) (map[string]string, error) {
	list, _ := json.Marshal(ids)
	rows, err := tx.Query(`SELECT id, COALESCE(legacy_id,'') FROM memories WHERE id IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	legacy := map[string]string{}
	for rows.Next() {
		var id, lid string
		if err := rows.Scan(&id, &lid); err != nil {
			return nil, err
		}
		legacy[id] = lid
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var missing []string
	for _, id := range ids {
		if _, ok := legacy[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, &ForgetMissingError{IDs: missing}
	}
	return legacy, nil
}

// clearSuccessorTx clears superseded_by on the memories outside ids that one of ids superseded and says
// which, in id order. They stay stale with forgetSuccessorGone as the reason.
func clearSuccessorTx(tx *sql.Tx, ids []string) ([]string, error) {
	list, _ := json.Marshal(ids)
	rows, err := tx.Query(`SELECT id FROM memories WHERE superseded_by IN (SELECT value FROM json_each(?))
		AND id NOT IN (SELECT value FROM json_each(?)) ORDER BY id`, string(list), string(list))
	if err != nil {
		return nil, err
	}
	touched := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		touched = append(touched, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range touched {
		// writeTx를 쓰지 않는다: 판 이력을 더하지 않으므로(명세 7절) 판 번호·updated_at은 그대로 둔다. 대체 표시와
		// 낡음 사유만 마지막 판 스냅숏과 달라진다. 행을 고치면 늦은 표시 트리거가 표시하므로 같은 트랜잭션에서 색인을
		// 맞춰 표시를 지운다.
		if _, err := tx.Exec(`UPDATE memories SET superseded_by='', stale_reason=? WHERE id=?`, forgetSuccessorGone, id); err != nil {
			return nil, err
		}
		if err := indexTx(tx, id); err != nil {
			return nil, err
		}
	}
	return touched, nil
}

// forgetCustomTx clears the rows of ids in a forgetTables table marked forgetCustom.
func forgetCustomTx(tx *sql.Tx, table string, ids []string) error {
	switch table {
	case "memory_index", "memory_index_late":
		// unindexTx가 두 표와 memory_fts를 함께 지운다. 두 번째 표에서 다시 불러도 찾을 줄이 없어 아무것도 안 한다.
		for _, id := range ids {
			if err := unindexTx(tx, id); err != nil {
				return err
			}
		}
		return nil
	case "activity":
		for _, id := range ids {
			if _, err := tx.Exec(`DELETE FROM activity WHERE host=? AND session=?`, JournalHost, JournalSession(id)); err != nil {
				return err
			}
		}
		return nil
	case "events":
		return forgetEventsTx(tx, ids)
	}
	return fmt.Errorf("forget: no way to clear %s", table) // forgetTables에 custom 표를 더하고 여기를 빠뜨린 것
}

// forgetEventsTx deletes the correction and memory.used events that point at one of ids, the ones
// cleanVerifiedUses counts. The other events keep the id (명세 5.3 「id만 남김」).
func forgetEventsTx(tx *sql.Tx, ids []string) error {
	var gone []int64
	for _, id := range ids {
		// LIKE는 후보만 넓게 고른다(id의 _·%는 와일드카드다). 대상은 JSON으로 읽어 정확히 가린다.
		rows, err := tx.Query(`SELECT id, data FROM events WHERE kind IN (?,?) AND (data LIKE ? OR data LIKE ?)`,
			EventMemoryUsed, EventCorrection, `%"memoryId":"`+id+`"%`, `%"target":"`+id+`"%`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var eid int64
			var data string
			if err := rows.Scan(&eid, &data); err != nil {
				rows.Close()
				return err
			}
			var d struct {
				MemoryID   string `json:"memoryId"`
				Target     string `json:"target"`
				TargetKind string `json:"targetKind"`
			}
			_ = json.Unmarshal([]byte(data), &d)
			if d.MemoryID == id || (d.Target == id && (d.TargetKind == "" || d.TargetKind == TargetKindMemory)) {
				gone = append(gone, eid)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for _, eid := range gone { // 두 id에 걸린 사건이 두 번 나와도 두 번째 지우기는 0행이다
		if _, err := tx.Exec(`DELETE FROM events WHERE id=?`, eid); err != nil {
			return err
		}
	}
	return nil
}

// IsForgotten says whether a memory with id, or with legacyID when it is not empty, was forgotten. A DB
// without the forgotten table (a copy an older binary made) has forgotten nothing.
func (s *Store) IsForgotten(id, legacyID string) (bool, error) {
	return forgottenTx(s.db, id, legacyID)
}

func forgottenTx(q querier, id, legacyID string) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM forgotten WHERE id=? OR (?<>'' AND legacy_id=?)`, id, legacyID, legacyID).Scan(&n)
	if noForgottenTable(err) {
		return false, nil
	}
	return n > 0, err
}

// noForgottenTable says whether err is SQLite's "no such table" for forgotten.
func noForgottenTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: forgotten")
}
