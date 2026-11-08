package memory

// 주제별 현재값과 저장 때 대체(설계 · orbit-mem 2판 4.2·4.3), 도구 트리거(6절), 세션 표시(정정·새 기억·도구 회상을
// 한 세션에 한 번만 알리는 기록). 대체는 상태를 새로 만들지 않고 1판의 stale + superseded_by를 쓴다.

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// supersedeTx replaces, for the memory m just saved, the current value of its scope and
// topic (when topic is true and m has one and is not volatile) and the memories in
// in.Supersedes, by the source rules (4.2). It returns what it replaced and the user's
// words it left alone.
func supersedeTx(tx *sql.Tx, m Memory, in SaveInput, topic bool, now int64) (SaveReport, error) {
	var rep SaveReport
	targets := slices.Clone(in.Supersedes)
	if topic && m.Topic != "" && m.Retention != RetentionVolatile {
		rows, err := tx.Query(`SELECT id FROM memories WHERE scope=? AND topic=? AND state IN ('candidate','confirmed') AND id<>?`, m.Scope, m.Topic, m.ID)
		if err != nil {
			return rep, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return rep, err
			}
			targets = append(targets, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return rep, err
		}
	}
	userWords := in.Source.Kind == SourceQuote
	// 출처가 믿을 수 없는 것뿐인 새 판은 owner·agent 기억을 대체하지 않고 어긋남으로 둔다(D7).
	newTrust, err := memoryTrustTx(tx, m.ID)
	if err != nil {
		return rep, err
	}
	for _, id := range uniq(targets) {
		if id == m.ID {
			continue
		}
		old, err := getTx(tx, id)
		if err != nil {
			return rep, err
		}
		if old.State != StateCandidate && old.State != StateConfirmed {
			continue // 이미 낡았거나 거절된 판
		}
		reason := "같은 주제의 새 판: " + m.Title
		if userWords {
			reason = "사용자 말로 대체: " + m.Title // D1: 새 말 자체가 근거다
		} else if p, err := protectedTx(tx, id); err != nil {
			return rep, err
		} else if p {
			rep.Conflicts = append(rep.Conflicts, id) // AI·자동화는 사용자 말을 대체하지 않는다
			continue
		} else if Untrusted(newTrust) && !Untrusted(old.Trust) {
			rep.Conflicts = append(rep.Conflicts, id)
			rep.untrusted = append(rep.untrusted, id)
			continue
		}
		old.State, old.SupersededBy, old.StaleReason = StateStale, m.ID, reason
		old.Version++
		old.UpdatedAt = now
		if err := writeTx(tx, old, "supersede", reason, now); err != nil {
			return rep, err
		}
		rep.Superseded = append(rep.Superseded, id)
	}
	return rep, nil
}

// maxTools is how many tool triggers a memory keeps.
const maxTools = 10

// toolRE is a tool name (Bash, mcp__atlas__write_page) or a prefix of one ending in *.
var toolRE = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,120}\*?$`)

// cleanTools trims and de-duplicates tool triggers and refuses a malformed one.
func cleanTools(tools []string) ([]string, error) {
	var out []string
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" || slices.Contains(out, t) {
			continue
		}
		if !toolRE.MatchString(t) || t == "*" {
			return nil, i18n.Errorf("memory.err.tool_name", ErrInvalid, t)
		}
		out = append(out, t)
	}
	if len(out) > maxTools {
		return nil, i18n.Errorf("memory.err.tool_count", ErrInvalid, maxTools)
	}
	return out, nil
}

func toolsJSON(tools []string) string {
	b, _ := json.Marshal(nonNil(tools))
	return string(b)
}

// TriggeredBy says whether calling tool should recall m: a trigger names the tool or,
// ending in *, a prefix of it.
func (m Memory) TriggeredBy(tool string) (exact, ok bool) {
	for _, t := range m.Tools {
		if t == tool {
			return true, true
		}
		if p, isPrefix := strings.CutSuffix(t, "*"); isPrefix && strings.HasPrefix(tool, p) {
			ok = true
		}
	}
	return false, ok
}

// Triggered lists the live (candidate or confirmed) memories that have tool triggers.
// The PreToolUse hook runs before every tool call, so it reads only these.
func (s *Store) Triggered() ([]Memory, error) {
	const live = `tools NOT IN ('','[]') AND state IN ('candidate','confirmed')`
	rows, err := s.db.Query(`SELECT ` + memoryCols + ` FROM memories WHERE ` + live + ` ORDER BY updated_at DESC, id`)
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
	return out, fillTrust(s.db, out, `memory_id IN (SELECT id FROM memories WHERE `+live+`)`)
}

// Session marks: what a session was told once, so the hooks do not say it again.
const (
	MarkShown      = "shown"      // 요청 회상으로 넣음
	MarkCorrection = "correction" // 정정 줄로 알림(옛 판 id)
	MarkNew        = "new"        // 새 기억 줄로 알림
	MarkTool       = "tool"       // 도구 호출 직전에 넣음
)

// MarkSession records that the session was given ids as kind.
func (s *Store) MarkSession(host, session, kind string, ids ...string) error {
	if session == "" {
		return nil
	}
	for _, id := range ids {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO session_marks(host,session,memory_id,kind,at) VALUES(?,?,?,?,?)`, host, session, id, kind, s.nowMs()); err != nil {
			return err
		}
	}
	return nil
}

// SessionMarks are the ids the session was given as kind.
func (s *Store) SessionMarks(host, session, kind string) map[string]bool {
	out := map[string]bool{}
	if session == "" {
		return out
	}
	rows, err := s.db.Query(`SELECT memory_id FROM session_marks WHERE host=? AND session=? AND kind=?`, host, session, kind)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// ResetSession forgets the session-start bundle and the marks of a session: after a
// compaction or /clear the session starts over with a bundle made anew.
func (s *Store) ResetSession(host, session string) error {
	if session == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM session_bundles WHERE host=? AND session=?`, host, session); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM session_marks WHERE host=? AND session=?`, host, session)
	return err
}

// SessionBundleAt is when the session's bundle was made (Unix ms), or 0 without one.
func (s *Store) SessionBundleAt(host, session string) int64 {
	var at int64
	_ = s.db.QueryRow(`SELECT at FROM session_bundles WHERE host=? AND session=?`, host, session).Scan(&at)
	return at
}

// ConfirmedSince maps the memories that became confirmed after at (Unix ms) to when. A
// memory already confirmed by then is not one, however often it was saved again or edited.
func (s *Store) ConfirmedSince(at int64) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT memory_id, MIN(at) FROM memory_versions v WHERE at>? AND json_extract(snapshot,'$.state')=?
		AND NOT EXISTS (SELECT 1 FROM memory_versions p WHERE p.memory_id=v.memory_id AND p.at<=? AND json_extract(p.snapshot,'$.state')=?)
		GROUP BY memory_id`, at, StateConfirmed, at, StateConfirmed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var when int64
		if err := rows.Scan(&id, &when); err != nil {
			return nil, err
		}
		out[id] = when
	}
	return out, rows.Err()
}
