package bench

import (
	"hash/fnv"
	"sort"
)

// Parts of a set.
const (
	PartTrain = "train"
	PartTest  = "test"
	PartAll   = "all"
)

// TrainPercent is the share of the sessions used for learning; the rest is for judging.
const TrainPercent = 70

// Bundles counts the cases a part has by what they test.
type Bundles struct {
	Answered  int `json:"answered"` // request cases with an expect
	Empty     int `json:"empty"`    // request cases that should show nothing
	Subagent  int `json:"subagent"`
	Duplicate int `json:"duplicate"`
}

// Partition gives the part (train or test) of every case. The unit is the session
// (the case id without one): the units are ordered by the FNV-64a hash of seed and key
// and the first 70% (rounded down) are for learning, so a session is never split and
// the same seed always gives the same split.
func Partition(set *Set) []string {
	type unit struct {
		key  string
		hash uint64
	}
	var units []unit
	seen := map[string]bool{}
	for _, c := range set.Cases {
		k := c.SessionKey()
		if seen[k] {
			continue
		}
		seen[k] = true
		h := fnv.New64a()
		h.Write([]byte(string(set.Seed) + "\x00" + k))
		units = append(units, unit{k, h.Sum64()})
	}
	sort.Slice(units, func(i, j int) bool {
		if units[i].hash != units[j].hash {
			return units[i].hash < units[j].hash
		}
		return units[i].key < units[j].key
	})
	train := map[string]bool{}
	for _, u := range units[:len(units)*TrainPercent/100] {
		train[u.key] = true
	}
	parts := make([]string, len(set.Cases))
	for i, c := range set.Cases {
		parts[i] = PartTest
		if train[c.SessionKey()] {
			parts[i] = PartTrain
		}
	}
	return parts
}

// units counts the sessions (or session-less cases) of a part.
func units(cases []Case, parts []string, part string) int {
	seen := map[string]bool{}
	for i, c := range cases {
		if part == PartAll || parts[i] == part {
			seen[c.SessionKey()] = true
		}
	}
	return len(seen)
}

// CountBundles counts the cases of one part (PartAll for every case).
func CountBundles(cases []Case, parts []string, part string) Bundles {
	var b Bundles
	for i, c := range cases {
		if part != PartAll && parts[i] != part {
			continue
		}
		switch {
		case c.CaseKind() == KindSubagent:
			b.Subagent++
		case c.CaseKind() == KindDuplicate:
			b.Duplicate++
		case len(c.Expect) > 0:
			b.Answered++
		default:
			b.Empty++
		}
	}
	return b
}
