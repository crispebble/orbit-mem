package memory

// 상시 핵심(설계 · orbit-mem 2판 4.4·D2, S3 명세 2.6): 세션 시작에 늘 들어가는 작은 묶음. 승격은 확정과 따로이고,
// 층(user·제품)마다 글자 수 한도가 있다. 한도를 넘는 승격은 자르지 않고 거절한다.

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/place"
)

// Core limits in characters (D2): the user layer and each product layer.
const (
	UserCoreRunes    = 3000
	ProductCoreRunes = 2000
)

// CoreLimit is the limit of the core layer of a stored scope, or 0 when memories of that scope cannot be
// pinned (a repository, product:?).
func CoreLimit(scope string) int {
	switch {
	case scope == ScopeGlobal:
		return UserCoreRunes
	case place.ProductOf(scope) != "":
		return ProductCoreRunes
	}
	return 0
}

// CoreEntry is how a memory goes into the session start: its title and id on one line, its body indented.
func CoreEntry(m Memory) string {
	entry := fmt.Sprintf("- %s (%s)\n", m.Title, m.ID)
	if text := strings.TrimSpace(m.Body); text != "" {
		entry += "  " + strings.ReplaceAll(text, "\n", "\n  ") + "\n"
	}
	return entry
}

// CoreRunes is the length of CoreEntry, what a memory takes of its core layer.
func CoreRunes(m Memory) int { return utf8.RuneCountInString(CoreEntry(m)) }

// CoreUsed is the length of a core layer.
func CoreUsed(core []Memory) int {
	n := 0
	for _, m := range core {
		n += CoreRunes(m)
	}
	return n
}

// Core is the core layer of a stored scope: its confirmed pinned memories, most recently changed first.
func (s *Store) Core(scope string) ([]Memory, error) { return coreTx(s.db, scope) }

func coreTx(q querier, scope string) ([]Memory, error) {
	rows, err := q.Query(`SELECT `+memoryCols+` FROM memories WHERE scope=? AND pinned=1 AND state='confirmed' ORDER BY updated_at DESC, id`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, fillTrust(q, out, `memory_id IN (SELECT id FROM memories WHERE scope=? AND pinned=1 AND state='confirmed')`, scope)
}

// ErrCoreFull is a pin past the limit of its core layer.
var ErrCoreFull = errors.New("core full")

// CoreFullError says which layer is full, how much it holds and what is in it, so the caller merges or
// unpins and pins again.
type CoreFullError struct {
	Layer       string   // 저장 범위(global·product:<이름>)
	Limit, Used int      // 한도와 지금 쓴 글자 수
	Need        int      // 승격하려던 기억의 글자 수
	Items       []Memory // 지금 핵심
}

func (e *CoreFullError) Unwrap() error { return ErrCoreFull }

func (e *CoreFullError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "상시 핵심 한도를 넘어 승격하지 않았습니다: %s 핵심 %s/%s자, 이 기억 %s자. 자르지 않습니다. 핵심을 합치거나 내린(memory_update action=unpin) 뒤 다시 승격하세요.\n지금 %s 핵심:",
		place.AppliesTo(e.Layer), comma(e.Used), comma(e.Limit), comma(e.Need), place.AppliesTo(e.Layer))
	for _, m := range e.Items {
		fmt.Fprintf(&b, "\n- %s · %s (%s자)", m.ID, m.Title, comma(CoreRunes(m)))
	}
	return b.String()
}

// comma writes n with thousands separators (3,000).
func comma(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// pinnable says why m cannot be pinned, or nil.
func pinnable(m Memory) error {
	if CoreLimit(m.Scope) == 0 {
		return i18n.Errorf("memory.err.pin_layer", ErrInvalid, place.AppliesTo(m.Scope))
	}
	if m.State != StateConfirmed {
		return i18n.Errorf("memory.err.pin_state", ErrInvalid, m.State)
	}
	if Untrusted(m.Trust) { // 출처가 믿을 수 없는 것뿐인 기억은 핵심에 오르지 않는다(D7)
		return fmt.Errorf("%w: %s", ErrUntrusted, m.ID)
	}
	return nil
}

// coreRoomTx checks, in the transaction that writes, that m fits its core layer next to the others.
func coreRoomTx(q querier, m Memory) error {
	core, err := coreTx(q, m.Scope)
	if err != nil {
		return err
	}
	var keep []Memory
	for _, c := range core {
		if c.ID != m.ID {
			keep = append(keep, c)
		}
	}
	limit, used, need := CoreLimit(m.Scope), CoreUsed(keep), CoreRunes(m)
	if used+need > limit {
		sort.SliceStable(keep, func(i, j int) bool { return CoreRunes(keep[i]) > CoreRunes(keep[j]) })
		return &CoreFullError{Layer: m.Scope, Limit: limit, Used: used, Need: need, Items: keep}
	}
	return nil
}

// stillPinned says whether a pinned memory keeps its core place after a change: it must stay a confirmed
// user or product memory and fit its layer (moved, grown, demoted or replaced, it leaves the core).
func stillPinned(q querier, m Memory) bool {
	return pinnable(m) == nil && coreRoomTx(q, m) == nil
}

// Pin puts a confirmed user or product memory into the core of its layer, if it fits (D2). Pinning a memory
// that holds the user's words needs the user's words (who.UserQuote), like any change to it (A2).
func (s *Store) Pin(id string, who Who) (Memory, error) { return s.setPinned(id, true, who, true) }

// Unpin takes a memory out of the core. It stays confirmed and comes through the work recall. Unpinning a
// memory that holds the user's words needs the user's words too.
func (s *Store) Unpin(id string, who Who) (Memory, error) { return s.setPinned(id, false, who, true) }

func (s *Store) setPinned(id string, on bool, who Who, guard bool) (Memory, error) {
	tx, err := s.db.Begin() // _txlock=immediate: 검사와 쓰기 사이에 다른 프로세스가 끼지 못한다
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	m, err := getTx(tx, id)
	if err != nil {
		return m, err
	}
	if m.Pinned == on {
		return s.Get(id)
	}
	now := s.nowMs()
	if guard {
		if err := guardTx(tx, who, now, id); err != nil {
			return m, err
		}
	}
	if err := pinTx(tx, &m, on, who, now); err != nil {
		return m, err
	}
	if err := tx.Commit(); err != nil {
		return m, err
	}
	return s.Get(id)
}

// pinTx pins or unpins m in tx, checking the layer and its limit when pinning.
func pinTx(tx *sql.Tx, m *Memory, on bool, who Who, now int64) error {
	if on {
		if err := pinnable(*m); err != nil {
			return err
		}
		if err := coreRoomTx(tx, *m); err != nil {
			return err
		}
	}
	m.Pinned = on
	m.Version++
	m.UpdatedAt = now
	action, reason := "pin", "상시 핵심으로 승격"
	if !on {
		action, reason = "unpin", "상시 핵심에서 내림"
	}
	if who.actor() != ActorUser {
		reason += " (" + who.actor() + ")"
	}
	return writeTx(tx, *m, action, reason, now)
}

// inheritPin passes the core place of the pinned memories a save superseded to the new value when it fits,
// and says which pinned one left the core when it does not.
func (s *Store) inheritPin(m Memory, superseded []string, who Who) (Memory, string) {
	if m.Pinned {
		return m, ""
	}
	var gone string
	for _, id := range superseded {
		if old, err := s.Get(id); err == nil && old.Pinned {
			gone = id
			break
		}
	}
	if gone == "" {
		return m, ""
	}
	// 사용자의 새 말이 옛 핵심을 대체했으면 새 판이 그 자리를 잇는다: 저장 자체가 근거라 사용자 말을 다시 묻지 않는다.
	if pm, err := s.setPinned(m.ID, true, who, false); err == nil {
		return pm, ""
	}
	return m, gone
}
