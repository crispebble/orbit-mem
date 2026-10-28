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
