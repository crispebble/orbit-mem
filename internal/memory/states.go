package memory

import "strings"

// StatesOf reads the state (candidate, confirmed, stale, hidden) of each memory in ids, in one query.
// An id the DB does not have (forgotten, or never kept) is left out of the result; no ids give an empty map.
// It reads what the memory is now: a volatile memory past its days keeps its state here (it is not marked
// stale by the day passing), so it reads as it was saved.
func (s *Store) StatesOf(ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT id, state FROM memories WHERE id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, err
		}
		out[id] = state
	}
	return out, rows.Err()
}
