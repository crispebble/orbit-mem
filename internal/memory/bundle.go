package memory

import "encoding/json"

// SaveSessionBundle keeps the first session-start context made for a session.
// Later calls for the same session keep the first one, so the context stays
// the same for the whole session and the host's prompt cache holds. Only
// ReplaceSessionBundle changes it, when a memory in it stopped being current.
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

// PruneSessionState drops the session bundles older than days. Claude Code keeps
// conversation logs 30 days by default (cleanupPeriodDays), so an older session is
// not resumed. The old retrospective tables (session_ends, retro_coverage) are no
// longer written and stay as they are.
func (s *Store) PruneSessionState(days int) error {
	cutoff := s.Now().AddDate(0, 0, -days).UnixMilli()
	_, err := s.db.Exec(`DELETE FROM session_bundles WHERE at<?`, cutoff)
	return err
}

// ReplaceSessionBundle puts text and ids in the place of a session's bundle that holds a memory no longer
// shown as a current value (명세 F-1); dropped are the ids the new bundle took out. It keeps the bundle's start
// time: the session's correction and new memory lines are measured from it.
//
// It replaces only a bundle that still holds every id of dropped. Two hooks that rebuild the same old bundle at
// once both pass it, and the second finds those ids already gone and writes nothing, so one rebuild is kept; a
// bundle a compaction or /clear made anew since is left alone in the same way. A session with no bundle gets
// none. With no dropped id it writes nothing: such a bundle cannot be told apart from any other, so it could
// overwrite one it was not made from. When it replaces, the same transaction marks dropped MarkBundle, so the
// session news can still tell the session once why they left. The return is nil whether or not it replaced;
// only a failed write is an error.
func (s *Store) ReplaceSessionBundle(host, session, text string, ids, dropped []string) error {
	if session == "" || len(dropped) == 0 {
		return nil
	}
	newIDs, _ := json.Marshal(nonNil(ids))
	gone, _ := json.Marshal(nonNil(dropped))
	tx, err := s.db.Begin() // _txlock=immediate: 바꾸기와 표시가 다른 연결의 쓰기와 차례로 돈다
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 옛 묶음 글을 비교하는 대신 뺀 id가 아직 있는지를 본다: 같은 옛 묶음을 읽은 두 훅이 만든 글은 같으므로, 먼저 들어간
	// 쪽이 그 id를 이미 뺐다면 뒤엣것은 덮을 까닭이 없다.
	res, err := tx.Exec(`UPDATE session_bundles SET text=?, memory_ids=? WHERE host=? AND session=?
		AND NOT EXISTS (SELECT 1 FROM json_each(?) d WHERE d.value NOT IN (SELECT value FROM json_each(session_bundles.memory_ids)))`,
		text, string(newIDs), host, session, string(gone))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	now := s.nowMs()
	for _, id := range dropped {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO session_marks(host,session,memory_id,kind,at) VALUES(?,?,?,?,?)`, host, session, id, MarkBundle, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NotCurrent lists, in the order given, the ids that do not show as a current value now (Of): stale, rejected,
// expired, an older value of its topic, or gone from the DB. It reads the memories in one query, without their
// sources. When they cannot be read it lists nothing and returns the error: a read that fails must not count as
// the memories being gone.
func (v Viewer) NotCurrent(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	b, _ := json.Marshal(ids)
	rows, err := v.s.db.Query(`SELECT `+memoryCols+` FROM memories WHERE id IN (SELECT value FROM json_each(?))`, string(b))
	if err != nil {
		return nil, err
	}
	found := map[string]Memory{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		found[m.ID] = m
	}
	rows.Close() // 연결이 하나뿐이라 Of가 후계를 읽기 전에 닫는다
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		if m, ok := found[id]; !ok || !v.Of(m).Current() {
			out = append(out, id)
		}
	}
	return out, nil
}
