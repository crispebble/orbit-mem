package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Import results (orbit-mem import).
const (
	ImportDone      = "imported"
	ImportExists    = "exists"    // 같은 id가 이미 있다: 그대로 두고 건너뛴다
	ImportDuplicate = "duplicate" // 같은 종류·범위·제목의 숨기지 않은 기억이 이미 있다
	ImportInvalid   = "invalid"   // 종류·상태·범위·제목이 틀렸다
)

// Export reads every memory in the given states (all but hidden when none are given) with
// its sources, days and links, oldest first.
func (s *Store) Export(states ...string) ([]Memory, error) {
	if len(states) == 0 {
		states = []string{StateCandidate, StateConfirmed, StateStale}
	}
	list, err := s.List(states...)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].CreatedAt != list[j].CreatedAt {
			return list[i].CreatedAt < list[j].CreatedAt
		}
		return list[i].ID < list[j].ID
	})
	out := make([]Memory, 0, len(list))
	for _, m := range list {
		full, err := s.Get(m.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, full)
	}
	return out, nil
}

var memoryIDForm = regexp.MustCompile(`^m-[0-9A-Za-z]+$`)

// ValidMemoryID tells whether id has the form NewID("m-") gives (it is used as a file name on export).
func ValidMemoryID(id string) bool { return memoryIDForm.MatchString(id) }

// Import adds an exported memory as it was: the same id, kind, scope, state, texts, times
// and sources, with one history entry "import" (reason). It never changes a memory that is
// already there. With dry, it only says what it would do. Links are left to the caller
// (both ends must be there first).
func (s *Store) Import(m Memory, reason string, dry bool) (string, error) {
	m.Title = strings.TrimSpace(m.Title)
	if !ValidMemoryID(m.ID) || !validKind(m.Kind) || m.Title == "" || strings.ContainsAny(m.Title, "\r\n") ||
		utf8.RuneCountInString(m.Title) > 200 || len(m.Body) > 32<<10 || !validScope(m.Scope) || !contains(States, m.State) {
		return ImportInvalid, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM memories WHERE id=? OR (legacy_id IS NOT NULL AND legacy_id=?)`, m.ID, nullIfEmpty(m.LegacyID)).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return ImportExists, nil
	}
	if m.State != StateHidden {
		if err := tx.QueryRow(`SELECT COUNT(*) FROM memories WHERE kind=? AND scope=? AND title_key=? AND state<>?`, m.Kind, m.Scope, TitleKey(m.Title), StateHidden).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			return ImportDuplicate, nil
		}
	}
	if dry {
		return ImportDone, nil
	}
	now := s.nowMs()
	if m.Version < 1 {
		m.Version = 1
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	if m.UpdatedAt == 0 {
		m.UpdatedAt = m.CreatedAt
	}
	m.Tags = cleanTags(m.Tags)
	tags, _ := json.Marshal(m.Tags)
	if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at,superseded_by,stale_reason,legacy_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Kind, m.Scope, m.Project, m.State, m.Title, TitleKey(m.Title), m.Body, string(tags), m.Version, m.CreatedAt, m.UpdatedAt, m.SupersededBy, m.StaleReason, nullIfEmpty(m.LegacyID)); err != nil {
		return "", err
	}
	for _, src := range m.Sources {
		if !contains(SourceKinds, src.Kind) {
			continue
		}
		if err := addSourceTx(tx, m.ID, src, now); err != nil {
			return "", err
		}
	}
	for _, day := range m.Days {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO sightings(memory_id, day) VALUES(?,?)`, m.ID, day); err != nil {
			return "", err
		}
	}
	if err := versionTx(tx, m, "import", reason, now); err != nil {
		return "", err
	}
	return ImportDone, tx.Commit()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Exists says whether a memory id is there.
func (s *Store) Exists(id string) bool {
	var x string
	err := s.db.QueryRow(`SELECT id FROM memories WHERE id=?`, id).Scan(&x)
	return !errors.Is(err, sql.ErrNoRows) && err == nil
}
