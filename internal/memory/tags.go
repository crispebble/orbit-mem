package memory

import (
	"regexp"
	"sort"
	"strings"
)

// TagKey is the tag as reads compare it: width and case folded, surrounding
// spaces trimmed and "_" read as "-" (설계 · Orbit 3차 O3-2 「읽을 때 맞추기」,
// U5). The stored tag stays as written.
func TagKey(tag string) string {
	return strings.ReplaceAll(Normalize(strings.TrimSpace(tag)), "_", "-")
}

// ImportedPrefix starts the tag that marks a memory moved from the old
// Knowledge or Claude memory (imported:server-knowledge, imported:claude-memory).
const ImportedPrefix = "imported:"

// IsImported says whether a memory was moved from an old store.
func IsImported(m Memory) bool {
	for _, t := range m.Tags {
		if strings.HasPrefix(TagKey(t), ImportedPrefix) {
			return true
		}
	}
	return false
}

// DomainPrefix starts the tag that puts a memory in a field-knowledge pack of
// this PC (명세 · Orbit 3차 O3-5 「PC 기억 분야 묶음」): 분야:<이름>.
const DomainPrefix = "분야:"

// DomainOf is the field-knowledge pack a memory belongs to, or "".
func DomainOf(m Memory) string {
	for _, t := range m.Tags {
		if k := TagKey(t); strings.HasPrefix(k, DomainPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(k, DomainPrefix))
		}
	}
	return ""
}

// markerTag is a mark the old Knowledge and Claude memory imports left, and
// kindTags are kind names: neither says what a memory is about (설계 · Orbit
// 2차 — 기억 그래프 「제외 태그」, the same rule as the Studio graph view).
var (
	markerTag = regexp.MustCompile(`^(imported|space|since|type|옛|superseded-by|분야):`)
	kindTags  = map[string]bool{"규칙": true, "사실": true, "결정": true, "교훈": true, "일지": true, "회고": true,
		"rule": true, "fact": true, "decision": true, "lesson": true, "journal": true}
)

// IsTopicKey says whether a tag key names a topic.
func IsTopicKey(key string) bool { return key != "" && !markerTag.MatchString(key) && !kindTags[key] }

// TopicKeys is a memory's topic tag keys in order, each once.
func TopicKeys(tags []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tags {
		k := TagKey(t)
		if IsTopicKey(k) && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func hasTagKey(tags []string, key string) bool {
	for _, t := range tags {
		if TagKey(t) == key {
			return true
		}
	}
	return false
}

// TagLabels picks, for every tag key, the spelling most memories use (the
// first in string order on a tie), so a key reads the way it is usually written.
func TagLabels(ms []Memory) map[string]string {
	count := map[string]map[string]int{}
	for _, m := range ms {
		seen := map[string]bool{}
		for _, t := range m.Tags {
			t = strings.TrimSpace(t)
			if seen[t] {
				continue
			}
			seen[t] = true
			k := TagKey(t)
			if count[k] == nil {
				count[k] = map[string]int{}
			}
			count[k][t]++
		}
	}
	out := make(map[string]string, len(count))
	for k, spellings := range count {
		best, n := "", 0
		for s, c := range spellings {
			if c > n || c == n && s < best {
				best, n = s, c
			}
		}
		out[k] = best
	}
	return out
}

// Clustering splits memories by representative topic tag.
type Clustering struct {
	Picked   []string          // 고른 태그 열쇠, 고른 순서
	Of       map[string]string // 기억 id → 클러스터 태그 열쇠, 그 밖이면 ""
	Other    int               // 그 밖(고른 태그가 없는 기억, 주제 태그가 없는 기억 포함)
	Untagged int               // 주제 태그가 하나도 없는 기억
}

// Clusters picks representative tags in covering order: each time, the topic
// tag most of the not yet covered memories carry (the smaller key on a tie),
// as long as it newly covers at least minNew memories. A memory belongs to the
// first picked tag it carries, else to "other". minNew <= 1 covers every
// tagged memory. The same memories give the same result (설계 · Orbit 2차 —
// 기억 그래프 「대표 태그」; 개수 상한 대신 새로 묶는 수, 2026-10-30 사용자 결정).
func Clusters(ms []Memory, minNew int) Clustering {
	c := Clustering{Of: make(map[string]string, len(ms))}
	keys := make([][]string, len(ms))
	var uncovered []int
	for i, m := range ms {
		keys[i] = TopicKeys(m.Tags)
		if len(keys[i]) == 0 {
			c.Untagged++
			continue
		}
		uncovered = append(uncovered, i)
	}
	for len(uncovered) > 0 {
		count := map[string]int{}
		for _, i := range uncovered {
			for _, k := range keys[i] {
				count[k]++
			}
		}
		best, n := "", 0
		for k, v := range count {
			if v > n || v == n && k < best {
				best, n = k, v
			}
		}
		if n < minNew {
			break
		}
		c.Picked = append(c.Picked, best)
		rest := uncovered[:0]
		for _, i := range uncovered {
			if !contains(keys[i], best) {
				rest = append(rest, i)
			}
		}
		uncovered = rest
	}
	order := make(map[string]int, len(c.Picked))
	for i, k := range c.Picked {
		order[k] = i
	}
	for i, m := range ms {
		pick, at := "", len(c.Picked)
		for _, k := range keys[i] {
			if o, ok := order[k]; ok && o < at {
				pick, at = k, o
			}
		}
		c.Of[m.ID] = pick
		if pick == "" {
			c.Other++
		}
	}
	return c
}

// ClusterMinNew is how many memories a tag must newly cover to become a
// cluster of the graph: 1% of the memories, at least 2 (2026-10-30 사용자 결정).
// The count follows each person's memories instead of a fixed number.
func ClusterMinNew(memories int) int { return max(2, (memories+50)/100) }

// Members lists the memories of one cluster in the order of ms.
func (c Clustering) Members(ms []Memory, key string) []Memory {
	var out []Memory
	for _, m := range ms {
		if c.Of[m.ID] == key {
			out = append(out, m)
		}
	}
	return out
}

// TagCount is a topic tag and how many memories carry it.
type TagCount struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// TopicTagCounts counts the topic tags of ms by key, most used first.
func TopicTagCounts(ms []Memory) []TagCount {
	labels := TagLabels(ms)
	n := map[string]int{}
	for _, m := range ms {
		for _, k := range TopicKeys(m.Tags) {
			n[k]++
		}
	}
	out := make([]TagCount, 0, len(n))
	for k, v := range n {
		out = append(out, TagCount{Key: k, Label: labels[k], Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}
