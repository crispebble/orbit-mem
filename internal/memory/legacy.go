package memory

import "encoding/json"

// LegacyJudged is the old memories judged so far and each one's verdict.
func (s *Store) LegacyJudged() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT data FROM events WHERE kind=? ORDER BY id`, EventLegacyJudged)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var d struct{ MemoryID, Verdict string }
		if json.Unmarshal([]byte(data), &d) != nil || d.MemoryID == "" {
			continue
		}
		if _, seen := out[d.MemoryID]; !seen {
			out[d.MemoryID] = d.Verdict // 판정은 한 번만 센다: 먼저 남은 것
		}
	}
	return out, rows.Err()
}

// LegacyLeft is the old memories (not journals) still candidate or confirmed
// without a judgment.
func (s *Store) LegacyLeft(judged map[string]string) ([]Memory, error) {
	active, err := s.List(StateCandidate, StateConfirmed)
	if err != nil {
		return nil, err
	}
	var out []Memory
	for _, m := range active {
		if _, done := judged[m.ID]; !done && m.Kind != KindJournal && IsImported(m) {
			out = append(out, m)
		}
	}
	return out, nil
}

// LegacyCount is where the old-memory judgment stands.
type LegacyCount struct {
	Total  int            `json:"total"`  // 판정할 옛 기억(판정한 것 포함)
	Judged int            `json:"judged"` // 판정한 것
	Left   int            `json:"left"`   // 아직 판정하지 않은 것
	By     map[string]int `json:"by"`     // 판정별 수
}

// CountLegacy counts (설계 · Orbit 3차 O3-3 끝 기준 1) the old memories to judge: those judged and those left.
// An old memory another way made stale or merged before it was judged needs none.
func (s *Store) CountLegacy() (LegacyCount, error) {
	c := LegacyCount{By: map[string]int{}}
	judged, err := s.LegacyJudged()
	if err != nil {
		return c, err
	}
	for _, v := range judged {
		c.By[v]++
	}
	left, err := s.LegacyLeft(judged)
	if err != nil {
		return c, err
	}
	c.Judged, c.Left = len(judged), len(left)
	c.Total = c.Judged + c.Left
	return c, nil
}

const legacyBaselineMeta = "legacy_baseline"

// LegacyBaseline is the memory count by state when the old-memory judgment
// began (설계 · Orbit 3차 O3-3 끝 기준 3: 정리 전후 수).
type LegacyBaseline struct {
	Day    string         `json:"day"`
	States map[string]int `json:"states"`
}

// LegacyBaseline reads the counts kept before the first batch.
func (s *Store) LegacyBaseline() (LegacyBaseline, bool) {
	var b LegacyBaseline
	if json.Unmarshal([]byte(s.Meta(legacyBaselineMeta)), &b) != nil || b.Day == "" {
		return b, false
	}
	return b, true
}

// KeepLegacyBaseline keeps the counts by state now, once: later calls leave
// the first one.
func (s *Store) KeepLegacyBaseline() error {
	if _, ok := s.LegacyBaseline(); ok {
		return nil
	}
	states, err := s.StateCounts()
	if err != nil {
		return err
	}
	b, _ := json.Marshal(LegacyBaseline{Day: Day(s.Now()), States: states})
	return s.SetMeta(legacyBaselineMeta, string(b))
}
