package memory

// 제품 이름 바꾸기(2026-11-05, 옛 제품 이름 폐기): 제품 지도의 키를 바꿀 때 그 제품에 묶인 기억(product:<옛>)을
// 모두 product:<새>로 옮긴다. 판단이 아니라 이름 바꾸기라 자동 변경(원장)으로 남기지 않고, 기억마다 판을 올려
// 이력(action rename)에 남긴다.
//
// 지키는 것:
//   - 한 트랜잭션이다. 사이에 끼우는 일(설정 파일 쓰기)이 실패하면 아무것도 옮기지 않는다.
//   - 상태(후보·확정·낡음·숨김)와 승격(상시 핵심)에 관계없이 옮긴다. 내용·주제·승격은 바꾸지 않는다.
//   - 새 제품 자리에 기억이 이미 있으면 거절한다(같은 주제의 현재값이 둘이 될 수 있다).

import (
	"fmt"

	"github.com/onidot-labs/orbit-mem/internal/place"
)

// ProductRename is what a product rename moved (or, in a dry run, would move).
type ProductRename struct {
	From     string          `json:"from"`
	To       string          `json:"to"`
	DryRun   bool            `json:"dryRun"`
	Memories []RenamedMemory `json:"memories"`
}

// RenamedMemory is one memory a product rename moved.
type RenamedMemory struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Pinned bool   `json:"pinned,omitempty"`
}

// RenameProduct moves every memory of product:from to product:to in one transaction. between runs inside
// the transaction after the moves and before the commit (the caller writes the product map there); when it
// fails nothing is moved. A dry run reports what would move and calls nothing.
func (s *Store) RenameProduct(from, to string, dry bool, between func() error) (ProductRename, error) {
	res := ProductRename{From: "product:" + from, To: "product:" + to, DryRun: dry, Memories: []RenamedMemory{}}
	if !place.ValidName(from) || !place.ValidName(to) || from == to {
		return res, fmt.Errorf("%w: product names %q → %q", ErrInvalid, from, to)
	}
	tx, err := s.db.Begin() // _txlock=immediate: 다른 프로세스의 저장과 차례로 돈다
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	var taken int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM memories WHERE scope=?`, res.To).Scan(&taken); err != nil {
		return res, err
	}
	if taken > 0 {
		return res, fmt.Errorf("%w: %d memories already apply to %s", ErrInvalid, taken, res.To)
	}
	rows, err := tx.Query(`SELECT id FROM memories WHERE scope=? ORDER BY created_at, id`, res.From)
	if err != nil {
		return res, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return res, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	now := s.nowMs()
	reason := fmt.Sprintf("제품 이름 바꿈: %s → %s", res.From, res.To)
	for _, id := range ids {
		m, err := getTx(tx, id)
		if err != nil {
			return res, err
		}
		res.Memories = append(res.Memories, RenamedMemory{ID: m.ID, Title: m.Title, State: m.State, Pinned: m.Pinned})
		if dry {
			continue
		}
		// writeTx는 쓰지 않는다: 승격을 다시 판정하지 않고 범위만 바꾼다(같은 기억들이 같은 한도 안에서 옮겨 간다).
		m.Scope, m.Version, m.UpdatedAt = res.To, m.Version+1, now
		if _, err := tx.Exec(`UPDATE memories SET scope=?, version=?, updated_at=? WHERE id=?`, m.Scope, m.Version, m.UpdatedAt, m.ID); err != nil {
			return res, err
		}
		if err := versionTx(tx, m, "rename", reason, now); err != nil {
			return res, err
		}
	}
	if dry {
		return res, nil
	}
	if between != nil {
		if err := between(); err != nil {
			return res, err
		}
	}
	return res, tx.Commit()
}
