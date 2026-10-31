package memory

import (
	"encoding/json"
	"math"
	"sort"
)

// Importance of a memory for the memory cluster (orbit.memory 1.3, 설계 · Studio
// 4차 화면 3): 0~1 from how often it was used (put into a session, read,
// found), kept (따름 판정), its links, its sources and whether it is
// confirmed. A violated judgment lowers it; a stale memory counts half.
// 가중치는 제안이다. 기억 그래프에서 점 크기를 재 보고 고친다.
const (
	weightConfirmed = 0.25
	weightUses      = 0.25
	weightFollowed  = 0.2
	weightLinks     = 0.15
	weightSources   = 0.15
	weightViolated  = 0.1
)

// saturate maps a count onto 0~1, reaching 1 at full.
func saturate(n, full int) float64 {
	if n <= 0 {
		return 0
	}
	return math.Min(1, math.Log1p(float64(n))/math.Log1p(float64(full)))
}

type importanceInputs struct {
	uses, followed, violated, links, sources map[string]int
	kinds                                    map[string][]string
}

func (s *Store) importanceInputs(links []Link) (importanceInputs, error) {
	in := importanceInputs{uses: map[string]int{}, followed: map[string]int{}, violated: map[string]int{}, links: map[string]int{},
		sources: map[string]int{}, kinds: map[string][]string{}}
	for _, l := range links {
		in.links[l.A]++
		in.links[l.B]++
	}
	rows, err := s.db.Query(`SELECT kind, data FROM events WHERE kind IN (?,?,?,?,?)`,
		EventInjectSessionStart, EventInjectPrompt, EventToolPrefix+"memory_get", EventToolPrefix+"memory_find", EventMemoryUsed)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var kind, data string
		if rows.Scan(&kind, &data) != nil {
			continue
		}
		var d struct {
			MemoryIDs []string `json:"memoryIds"`
			IDs       []string `json:"ids"`
			MemoryID  string   `json:"memoryId"`
			Verdict   string   `json:"verdict"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		if kind == EventMemoryUsed {
			switch d.Verdict {
			case UsageFollowed:
				in.followed[d.MemoryID]++
			case UsageViolated:
				in.violated[d.MemoryID]++
			}
			continue
		}
		for _, id := range append(d.MemoryIDs, d.IDs...) {
			in.uses[id]++
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT memory_id, kind FROM sources ORDER BY memory_id`)
	if err != nil {
		return in, err
	}
	defer rows.Close()
	seen := map[[2]string]bool{}
	for rows.Next() {
		var id, kind string
		if rows.Scan(&id, &kind) != nil {
			continue
		}
		in.sources[id]++
		if !seen[[2]string{id, kind}] {
			seen[[2]string{id, kind}] = true
			in.kinds[id] = append(in.kinds[id], kind)
		}
	}
	for id := range in.kinds {
		sort.Strings(in.kinds[id])
	}
	return in, rows.Err()
}

func (in importanceInputs) of(m Memory) float64 {
	v := weightUses*saturate(in.uses[m.ID], 20) + weightFollowed*saturate(in.followed[m.ID], 5) +
		weightLinks*saturate(in.links[m.ID], 4) + weightSources*saturate(in.sources[m.ID], 3) - weightViolated*saturate(in.violated[m.ID], 5)
	if m.State == StateConfirmed {
		v += weightConfirmed
	}
	if m.State == StateStale {
		v /= 2
	}
	v = math.Max(0, math.Min(1, v))
	return math.Round(v*1000) / 1000
}
