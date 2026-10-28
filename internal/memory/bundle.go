package memory

import "encoding/json"

// SaveSessionBundle keeps the first session-start context made for a session.
// Later calls for the same session keep the first one, so the context stays
// the same for the whole session and the host's prompt cache holds.
func (s *Store) SaveSessionBundle(host, session, text string, ids []string) error {
	b, _ := json.Marshal(nonNil(ids))
	_, err := s.db.Exec(`INSERT INTO session_bundles(host,session,text,memory_ids,at) VALUES(?,?,?,?,?) ON CONFLICT(host,session) DO NOTHING`,
		host, session, text, string(b), s.nowMs())
	return err
}

// SessionBundle reads the session-start context made for a session.
func (s *Store) SessionBundle(host, session string) (string, []string, bool) {
	var text, ids string
	if err := s.db.QueryRow(`SELECT text, memory_ids FROM session_bundles WHERE host=? AND session=?`, host, session).Scan(&text, &ids); err != nil {
		return "", nil, false
	}
	var out []string
	_ = json.Unmarshal([]byte(ids), &out)
	return text, out, true
}

// PruneSessionState drops session bundles and handled session-end queue rows
// older than days. Claude Code keeps conversation logs 30 days by default
// (cleanupPeriodDays), so an older session is not resumed. Queue rows still
// waiting stay; the read spans (retro_coverage) stay too, as they keep turns
// from being read twice.
func (s *Store) PruneSessionState(days int) error {
	cutoff := s.Now().AddDate(0, 0, -days).UnixMilli()
	if _, err := s.db.Exec(`DELETE FROM session_bundles WHERE at<?`, cutoff); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM session_ends WHERE retro_id<>'' AND ended_at<?`, cutoff)
	return err
}
