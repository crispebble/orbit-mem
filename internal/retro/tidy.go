package retro

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// KindTidy is a retrospective that only tidies the memory graph (orbit-mem retro tidy).
const KindTidy = "tidy"

// tidyMeta is where the graph tidy keeps which cluster it reached.
const tidyMeta = "graph_tidy"

// Link relations the retrospective may propose (설계 · Orbit 2차 하네스 문장: 같은 결정의
// 근거, 앞뒤 절차, 예외). The same tag or session alone is no reason to link.
var linkRelations = map[string]string{"basis": "같은 결정의 근거", "sequence": "앞뒤 절차", "exception": "예외"}

// TidyMemory is a memory as the graph tidy shows it.
type TidyMemory struct {
	ID    string   `json:"id"`
	Kind  string   `json:"kind"`
	State string   `json:"state"`
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
	Body  string   `json:"body,omitempty"`
}

// Tidy is the memory graph part of the material (설계 · Orbit 3차 O3-2): one
// batch of one representative-tag cluster to link, memories without a topic
// tag to tag, and the topic tags in use to merge synonyms.
type Tidy struct {
	Cluster  string            `json:"cluster"` // 태그 열쇠
	Label    string            `json:"label"`
	From     int               `json:"from"` // 이 묶음이 클러스터의 몇째 기억부터인지(0부터)
	Size     int               `json:"size"` // 클러스터 기억 수
	Memories []TidyMemory      `json:"memories,omitempty"`
	Links    [][2]string       `json:"links,omitempty"` // 묶음 안의 이미 있는 연결
	Untagged []TidyMemory      `json:"untagged,omitempty"`
	Tags     []memory.TagCount `json:"tags,omitempty"`
	Merging  []string          `json:"merging,omitempty"` // 대기 중인 태그 묶기("뺄 → 넣을")
	Max      int               `json:"max,omitempty"`     // 회당 정리 제안 상한, 0은 없음
}

func (t *Tidy) empty() bool { return t == nil || len(t.Memories) < 2 && len(t.Untagged) == 0 }

// tidyState is the cursor kept in meta: the cluster being walked and where
// the next batch starts, and for each finished cluster the day and its size then.
type tidyState struct {
	Cluster string              `json:"cluster,omitempty"`
	Next    int                 `json:"next,omitempty"`
	Done    map[string]tidyDone `json:"done,omitempty"`
}

type tidyDone struct {
	Day  string `json:"day"`
	Size int    `json:"size"`
}

func readTidyState(s *memory.Store) tidyState {
	var st tidyState
	_ = json.Unmarshal([]byte(s.Meta(tidyMeta)), &st)
	if st.Done == nil {
		st.Done = map[string]tidyDone{}
	}
	return st
}

func tidyMemory(m memory.Memory) TidyMemory {
	return TidyMemory{ID: m.ID, Kind: m.Kind, State: m.State, Title: m.Title, Tags: m.Tags, Body: clipBytes(memory.MaskSecrets(m.Body), 400)}
}

// tidyMaterial picks the next batch of the graph tidy, or nil when it is off
// or has nothing to show. Clusters cover every candidate and confirmed memory
// except journals; the walk goes on in the cluster it stopped in, then takes
// clusters never done in picked order, then, of the clusters whose size
// changed since they were done, the one done longest ago. A cluster that did
// not change is not walked again (it would cost a run to see the same pairs).
func (r *Keeper) tidyMaterial() (*Tidy, error) {
	n, u := r.Cfg.GraphTidyMemories, r.Cfg.GraphTidyUntagged
	if n <= 0 && u <= 0 {
		return nil, nil
	}
	active, err := r.Store.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return nil, err
	}
	var live []memory.Memory
	for _, m := range active {
		if m.Kind != memory.KindJournal {
			live = append(live, m)
		}
	}
	// 만든 순서로 놓아야 큰 클러스터를 나눠 걸을 때 새 기억이 뒤에 붙는다.
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].CreatedAt != live[j].CreatedAt {
			return live[i].CreatedAt < live[j].CreatedAt
		}
		return live[i].ID < live[j].ID
	})
	t := &Tidy{Max: r.Cfg.GraphTidyProposals}
	if n > 0 {
		c := memory.Clusters(live, 0)
		sizes := map[string]int{}
		for _, k := range c.Of {
			sizes[k]++
		}
		st := readTidyState(r.Store)
		pick := ""
		if st.Cluster != "" && sizes[st.Cluster] > st.Next {
			pick, t.From = st.Cluster, st.Next
		} else {
			for _, k := range c.Picked {
				d, done := st.Done[k]
				if sizes[k] < 2 || done && d.Size == sizes[k] {
					continue
				}
				if !done {
					pick = k
					break
				}
				if pick == "" || d.Day < st.Done[pick].Day {
					pick = k
				}
			}
		}
		if pick != "" {
			members := c.Members(live, pick)
			labels := memory.TagLabels(members)
			t.Cluster, t.Label, t.Size = pick, labels[pick], len(members)
			if len(members)-t.From < 2 { // 남은 것이 하나면 앞 묶음과 겹쳐 둘 이상을 본다(쌍이 없으면 커서가 멈춘다)
				t.From = max(0, len(members)-max(n, 2))
			}
			end := min(t.From+n, len(members))
			in := map[string]bool{}
			for _, m := range members[t.From:end] {
				t.Memories = append(t.Memories, tidyMemory(m))
				in[m.ID] = true
			}
			links, err := r.Store.Links()
			if err != nil {
				return nil, err
			}
			for _, l := range links {
				if in[l.A] && in[l.B] {
					t.Links = append(t.Links, [2]string{l.A, l.B})
				}
			}
		}
	}
	// 주제 태그 목록은 태그 달기·묶기의 재료라 연결 후보를 꺼도 싣는다.
	t.Tags = memory.TopicTagCounts(live)
	pending, err := r.Store.Proposals(memory.StatusPending)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		if p.Kind == memory.ProposalRetag && len(p.Payload.FromTags) > 0 {
			t.Merging = append(t.Merging, strings.Join(p.Payload.FromTags, ", ")+" → "+strings.Join(p.Payload.ToTags, ", "))
		}
	}
	if u > 0 {
		for _, m := range live {
			if len(t.Untagged) >= u {
				break
			}
			if len(memory.TopicKeys(m.Tags)) == 0 && !r.hasProposalFor(m.ID, memory.ProposalRetag) {
				t.Untagged = append(t.Untagged, tidyMemory(m))
			}
		}
	}
	if t.empty() {
		return nil, nil
	}
	return t, nil
}

// advanceTidy moves the cursor past the batch a finished retrospective looked at.
func advanceTidy(s *memory.Store, t *Tidy, day string) {
	if t == nil || t.Cluster == "" {
		return
	}
	st := readTidyState(s)
	next := t.From + len(t.Memories)
	if next < t.Size {
		st.Cluster, st.Next = t.Cluster, next
	} else {
		st.Cluster, st.Next = "", 0
		st.Done[t.Cluster] = tidyDone{Day: day, Size: t.Size}
	}
	b, _ := json.Marshal(st)
	_ = s.SetMeta(tidyMeta, string(b))
}

// PrepareTidy prepares one graph tidy for host, with no conversation material.
func (r *Keeper) PrepareTidy(host string) (Prepared, error) {
	mat := Material{Kind: KindTidy, Period: dayOf(r.now())}
	t, err := r.tidyMaterial()
	if err != nil {
		return Prepared{Kind: KindTidy, Period: mat.Period}, err
	}
	mat.Tidy = t
	if err := r.commonMaterial(&mat); err != nil {
		return Prepared{Kind: KindTidy, Period: mat.Period}, err
	}
	mat.Existing = nil // 정리는 기억을 새로 남기지 않는다
	return r.begin(mat, host)
}

func renderTidy(b *strings.Builder, t *Tidy) {
	if t == nil {
		return
	}
	if len(t.Memories) > 0 {
		fmt.Fprintf(b, "## 그래프 정리: 클러스터 「%s」 (%d개 중 %d~%d째)\n\n", t.Label, t.Size, t.From+1, t.From+len(t.Memories))
		b.WriteString("이 묶음 안에서 함께 읽어야 하는 기억 쌍을 link로 낸다. relation은 basis(같은 결정의 근거)·sequence(앞뒤 절차)·exception(예외) 중 하나다.\n")
		b.WriteString("같은 태그·같은 세션이라는 것만으로는 잇지 않는다. 같은 말이면 link가 아니라 merge다. 억지로 만들지 않는다.\n\n")
		for _, m := range t.Memories {
			fmt.Fprintf(b, "- %s [%s/%s] %s · 태그 %s\n", m.ID, m.Kind, m.State, m.Title, strings.Join(m.Tags, ", "))
			if body := strings.TrimSpace(m.Body); body != "" {
				fmt.Fprintf(b, "  %s\n", strings.ReplaceAll(body, "\n", "\n  "))
			}
		}
		if len(t.Links) > 0 {
			b.WriteString("\n이미 이어진 쌍(다시 내지 않는다):\n")
			for _, l := range t.Links {
				fmt.Fprintf(b, "- %s — %s\n", l[0], l[1])
			}
		}
		b.WriteString("\n")
	}
	if len(t.Untagged) > 0 {
		b.WriteString("## 주제 태그가 없는 기억 (retag로 주제 태그를 단다)\n\n")
		b.WriteString("memoryIds에 그 기억, toTags에 달 주제 태그(아래 목록에 있는 표기를 먼저 쓴다). 표식 태그(imported:·space: 등)는 그대로 둔다.\n\n")
		for _, m := range t.Untagged {
			fmt.Fprintf(b, "- %s [%s/%s] %s · 태그 %s\n", m.ID, m.Kind, m.State, m.Title, strings.Join(m.Tags, ", "))
			if body := strings.TrimSpace(m.Body); body != "" {
				fmt.Fprintf(b, "  %s\n", strings.ReplaceAll(body, "\n", "\n  "))
			}
		}
		b.WriteString("\n")
	}
	if len(t.Tags) > 0 {
		b.WriteString("## 주제 태그 목록 (쓰는 기억 수)\n\n")
		b.WriteString("뜻이 같은 다른 태그(예: 같은 제품을 가리키는 두 이름)는 retag로 묶는다: fromTags에 뺄 태그, toTags에 남길 태그 하나, memoryIds는 비운다.\n")
		b.WriteString("대소문자·공백·-와 _만 다른 표기는 이미 같은 태그로 읽으므로 묶지 않는다. 넓은 주제와 좁은 주제처럼 뜻이 다르면 묶지 않는다.\n\n")
		for _, tc := range t.Tags {
			fmt.Fprintf(b, "- %s (%d)\n", tc.Label, tc.Count)
		}
		for _, m := range t.Merging {
			fmt.Fprintf(b, "- 대기 중인 묶기: %s (다시 내지 않는다)\n", m)
		}
		b.WriteString("\n")
	}
	if t.Max > 0 {
		fmt.Fprintf(b, "정리 제안(link·retag)은 이번에 %d건까지 받는다. 제출은 일반 제안과 합쳐 %d건까지다. 확실한 것부터 낸다.\n\n", t.Max, maxSubmitProposals)
	}
}
