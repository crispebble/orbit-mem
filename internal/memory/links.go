package memory

import (
	"database/sql"
	"errors"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// 기억끼리의 명시 연결(설계 「Orbit 2차 — 기억 그래프」). 연결은 방향이 없고 양 끝을 id 순(a < b)으로 둔다.
// 떼어도 지우지 않고 뗀 때·뗀 쪽·사유를 남긴다. 연결을 바꿔도 기억의 판은 그대로다.

// LinkRelated is the one link kind so far: "this memory is related to that one".
const LinkRelated = "related"

// Who made or removed a link.
const (
	LinkOriginAI          = "ai"           // AI가 memory_save·memory_update로
	LinkOriginRetro       = "retro"        // 승인한 회고 제안
	LinkOriginUser        = "user"         // 사용자가 화면에서
	LinkOriginRetroRevert = "retro_revert" // 원장 되돌리기(뗀 쪽으로만)
)

// LinkOrigins are the origins a link can be made by.
var LinkOrigins = []string{LinkOriginAI, LinkOriginRetro, LinkOriginUser}

// Event kinds for links.
const (
	EventMemoryLinked   = "memory.linked"
	EventMemoryUnlinked = "memory.unlinked"
)

const linksSchema = `
CREATE TABLE IF NOT EXISTS memory_links (
  id TEXT PRIMARY KEY, a TEXT NOT NULL, b TEXT NOT NULL, kind TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
  origin TEXT NOT NULL, origin_ref TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
  removed_at INTEGER NOT NULL DEFAULT 0, removed_origin TEXT NOT NULL DEFAULT '', removed_reason TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS memory_links_live ON memory_links(a, b, kind) WHERE removed_at = 0;
CREATE INDEX IF NOT EXISTS memory_links_b ON memory_links(b) WHERE removed_at = 0;
`

// Link is one stored link between two memories.
type Link struct {
	ID        string `json:"id"`
	A         string `json:"a"`
	B         string `json:"b"`
	Kind      string `json:"kind"`
	Note      string `json:"note,omitempty"`
	Origin    string `json:"origin"`
	OriginRef string `json:"originRef,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// MemoryLink is a live link seen from one of its memories.
type MemoryLink struct {
	ID       string `json:"id"`
	MemoryID string `json:"memoryId"` // 상대 기억
	Kind     string `json:"kind"`
	Note     string `json:"note,omitempty"`
	Origin   string `json:"origin"`
	At       int64  `json:"at"`
}

// LinkInput asks for a link between From and To.
type LinkInput struct {
	From, To  string
	Kind      string // 비우면 related
	Note      string
	Origin    string
	OriginRef string // AI면 세션, 회고면 제안 id
}

func pair(x, y string) (string, string) {
	if x < y {
		return x, y
	}
	return y, x
}

const linkCols = `id,a,b,kind,note,origin,origin_ref,created_at`

func scanLink(row interface{ Scan(...any) error }) (Link, error) {
	var l Link
	err := row.Scan(&l.ID, &l.A, &l.B, &l.Kind, &l.Note, &l.Origin, &l.OriginRef, &l.CreatedAt)
	return l, err
}

// Link links two memories. A live link of the same pair is returned as is (created=false).
func (s *Store) Link(in LinkInput) (Link, bool, error) {
	if in.Kind == "" {
		in.Kind = LinkRelated
	}
	if in.Kind != LinkRelated || !contains(LinkOrigins, in.Origin) {
		return Link{}, false, i18n.Errorf("memory.err.link_kind_origin", ErrInvalid, LinkRelated)
	}
	if in.From == in.To {
		return Link{}, false, i18n.Errorf("memory.err.link_self", ErrInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Link{}, false, err
	}
	defer tx.Rollback()
	for _, id := range []string{in.From, in.To} {
		if _, err := getTx(tx, id); err != nil {
			return Link{}, false, err
		}
	}
	a, b := pair(in.From, in.To)
	l, err := scanLink(tx.QueryRow(`SELECT `+linkCols+` FROM memory_links WHERE a=? AND b=? AND kind=? AND removed_at=0`, a, b, in.Kind))
	if err == nil {
		return l, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, err
	}
	l = Link{ID: NewID("l-"), A: a, B: b, Kind: in.Kind, Note: ClipRunes(MaskSecrets(in.Note), 300), Origin: in.Origin,
		OriginRef: in.OriginRef, CreatedAt: s.nowMs()}
	if _, err := tx.Exec(`INSERT INTO memory_links(`+linkCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		l.ID, l.A, l.B, l.Kind, l.Note, l.Origin, l.OriginRef, l.CreatedAt); err != nil {
		return Link{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Link{}, false, err
	}
	_ = s.RecordEvent(Event{Kind: EventMemoryLinked}, map[string]any{"linkId": l.ID, "a": l.A, "b": l.B, "origin": l.Origin})
	return l, true, nil
}

// Unlink removes the live link between two memories (keeping the row). No link is not an error.
func (s *Store) Unlink(x, y, origin, reason string) (bool, error) {
	a, b := pair(x, y)
	var id string
	err := s.db.QueryRow(`SELECT id FROM memory_links WHERE a=? AND b=? AND removed_at=0`, a, b).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.UnlinkID(id, origin, reason)
}

// UnlinkID removes a live link by id (keeping the row). An already removed or unknown link is not an error.
func (s *Store) UnlinkID(id, origin, reason string) (bool, error) {
	res, err := s.db.Exec(`UPDATE memory_links SET removed_at=?, removed_origin=?, removed_reason=? WHERE id=? AND removed_at=0`,
		s.nowMs(), origin, ClipRunes(MaskSecrets(reason), 300), id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	_ = s.RecordEvent(Event{Kind: EventMemoryUnlinked}, map[string]any{"linkId": id, "origin": origin})
	return true, nil
}

// linked says whether two memories have a live link.
func (s *Store) linked(x, y string) (bool, error) {
	a, b := pair(x, y)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_links WHERE a=? AND b=? AND removed_at=0`, a, b).Scan(&n)
	return n > 0, err
}

// linkByID reads a link, live or removed.
func (s *Store) linkByID(id string) (Link, error) {
	l, err := scanLink(s.db.QueryRow(`SELECT `+linkCols+` FROM memory_links WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

// LinksOf returns the live links of one memory, oldest first.
func (s *Store) LinksOf(id string) ([]MemoryLink, error) {
	rows, err := s.db.Query(`SELECT `+linkCols+` FROM memory_links WHERE (a=? OR b=?) AND removed_at=0 ORDER BY created_at, id`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryLink{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		other := l.B
		if l.B == id {
			other = l.A
		}
		out = append(out, MemoryLink{ID: l.ID, MemoryID: other, Kind: l.Kind, Note: l.Note, Origin: l.Origin, At: l.CreatedAt})
	}
	return out, rows.Err()
}

// Links returns every live link, oldest first.
func (s *Store) Links() ([]Link, error) {
	rows, err := s.db.Query(`SELECT ` + linkCols + ` FROM memory_links WHERE removed_at=0 ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
