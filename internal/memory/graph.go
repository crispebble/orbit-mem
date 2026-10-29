package memory

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Relations of the memory graph (orbit.memory 1판 계약). link·supersede는 선,
// session·file·tag는 파생 묶음이다. 묶음은 저장하지 않고 읽을 때 만든다.
const (
	RelLink      = "link"
	RelSupersede = "supersede"
	RelSession   = "session"
	RelFile      = "file"
	RelTag       = "tag"
)

var (
	Relations = []string{RelLink, RelSupersede, RelSession, RelFile, RelTag}
	// DefaultVia는 주변을 따라갈 때 기본으로 건너는 관계다. 태그 묶음은 크기가 커서(측정 2026-10-29: 가장 큰 태그 277개) 뺀다.
	DefaultVia = []string{RelLink, RelSupersede, RelSession, RelFile}
	// DefaultGraphStates는 그래프에 기본으로 담는 상태다. 숨김(거절한 후보)은 뺀다.
	DefaultGraphStates = []string{StateCandidate, StateConfirmed, StateStale}
)

// 상한(제안). 근거는 계약 README 「oni가 정한 값」.
const (
	GraphLimitMax = 1000
	GraphDepthMax = 3
)

// GraphInput is memory_graph's arguments after the contract checks. 빈 칸은 기본값이다.
type GraphInput struct {
	Focus                  string
	Depth                  int
	Via                    []string
	Kinds, States, Scopes  []string
	Tags                   []string
	UpdatedFrom, UpdatedTo int64 // 밀리초, 0이면 없음
	Relations              []string
	Limit                  int
}

// GraphNode is a memory without its body.
type GraphNode struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	State        string   `json:"state"`
	Scope        string   `json:"scope"`
	Project      string   `json:"project,omitempty"`
	Title        string   `json:"title"`
	Tags         []string `json:"tags"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
	SupersededBy string   `json:"supersededBy,omitempty"`
}

// GraphEdge is a link (no direction, From < To) or a supersede (old → new).
type GraphEdge struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Relation string `json:"relation,omitempty"`
	From     string `json:"from"`
	To       string `json:"to"`
	Directed bool   `json:"directed"`
	Note     string `json:"note,omitempty"`
	Origin   string `json:"origin,omitempty"`
	At       string `json:"at,omitempty"`
}

// GraphGroup is a derived group: memories with the same tag, session source or file source.
type GraphGroup struct {
	Kind    string   `json:"kind"`
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Host    string   `json:"host,omitempty"`
	Members []string `json:"members"`
}

// Graph is memory_graph's result without the contract head.
type Graph struct {
	Focus     string       `json:"focus,omitempty"`
	Nodes     []GraphNode  `json:"nodes"`
	Edges     []GraphEdge  `json:"edges"`
	Groups    []GraphGroup `json:"groups"`
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated"`
	LimitMax  int          `json:"limitMax"`
	DepthMax  int          `json:"depthMax"`
}

// GraphTime is the contract's time: RFC 3339 UTC with milliseconds.
func GraphTime(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }

var sessionHosts = map[string]string{"claude": "Claude Code", "codex": "Codex"}

// groupKey names a derived group: its kind and key (태그, 세션 ref, 파일 경로).
type groupKey struct{ kind, key string }

// Graph builds the memory graph: nodes filtered (and, with Focus, the
// neighborhood within Depth steps over Via), the links and supersedes between
// them, and derived groups of two or more of them.
func (s *Store) Graph(in GraphInput) (Graph, error) {
	all, err := s.list(States)
	if err != nil {
		return Graph{}, err
	}
	links, err := s.Links()
	if err != nil {
		return Graph{}, err
	}
	groups, err := s.graphGroups(all)
	if err != nil {
		return Graph{}, err
	}
	byID := make(map[string]Memory, len(all))
	for _, m := range all {
		byID[m.ID] = m
	}
	if in.Focus != "" {
		if _, ok := byID[in.Focus]; !ok {
			return Graph{}, ErrNotFound
		}
	}

	pass := graphFilter(in)
	picked := map[string]bool{}
	if in.Focus == "" {
		for _, m := range all {
			if pass(m) {
				picked[m.ID] = true
			}
		}
	} else {
		picked = neighborhood(in, byID, links, groups, pass)
	}

	// 최근에 고친 순(같으면 id 순). all이 이미 그 순서다.
	var ordered []Memory
	for _, m := range all {
		if picked[m.ID] {
			ordered = append(ordered, m)
		}
	}
	out := Graph{Focus: in.Focus, Nodes: []GraphNode{}, Edges: []GraphEdge{}, Groups: []GraphGroup{},
		Total: len(ordered), LimitMax: GraphLimitMax, DepthMax: GraphDepthMax}
	limit := in.Limit
	if limit <= 0 || limit > GraphLimitMax {
		limit = GraphLimitMax
	}
	if len(ordered) > limit {
		out.Truncated = true
		ordered = keepRecent(ordered, limit, in.Focus)
	}
	kept := make(map[string]bool, len(ordered))
	for _, m := range ordered {
		kept[m.ID] = true
		out.Nodes = append(out.Nodes, graphNode(m))
	}

	rel := set(in.Relations, Relations)
	if rel[RelLink] {
		for _, l := range links {
			if kept[l.A] && kept[l.B] {
				out.Edges = append(out.Edges, GraphEdge{ID: l.ID, Kind: RelLink, Relation: l.Kind, From: l.A, To: l.B,
					Note: MaskSecrets(l.Note), Origin: l.Origin, At: GraphTime(l.CreatedAt)})
			}
		}
	}
	if rel[RelSupersede] {
		for _, m := range ordered {
			if m.SupersededBy != "" && kept[m.SupersededBy] {
				out.Edges = append(out.Edges, GraphEdge{ID: "sup-" + m.ID, Kind: RelSupersede, From: m.ID, To: m.SupersededBy, Directed: true})
			}
		}
	}
	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	order := map[string]int{RelTag: 0, RelSession: 1, RelFile: 2}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return order[keys[i].kind] < order[keys[j].kind]
		}
		return keys[i].key < keys[j].key
	})
	for _, k := range keys {
		if !rel[k.kind] {
			continue
		}
		var members []string
		for _, id := range groups[k] {
			if kept[id] {
				members = append(members, id)
			}
		}
		if len(members) < 2 {
			continue
		}
		sort.Strings(members)
		out.Groups = append(out.Groups, groupOf(k, members))
	}
	return out, nil
}

// graphGroups reads the tag, session and file memberships of every memory.
// 세션은 호스트를 아는 출처(claude:·codex:)만, 파일은 절대경로만 묶는다.
func (s *Store) graphGroups(all []Memory) (map[groupKey][]string, error) {
	groups := map[groupKey][]string{}
	add := func(k groupKey, id string) {
		list := groups[k]
		for _, x := range list {
			if x == id {
				return
			}
		}
		groups[k] = append(list, id)
	}
	for _, m := range all {
		for _, t := range m.Tags {
			add(groupKey{RelTag, t}, m.ID)
		}
	}
	rows, err := s.db.Query(`SELECT memory_id, kind, ref FROM sources WHERE kind IN (?, ?) ORDER BY memory_id, at, id`, SourceSession, SourceFile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, ref string
		if err := rows.Scan(&id, &kind, &ref); err != nil {
			return nil, err
		}
		switch {
		case kind == SourceSession && sessionHost(ref) != "":
			add(groupKey{RelSession, ref}, id)
		case kind == SourceFile && filepath.IsAbs(ref):
			add(groupKey{RelFile, ref}, id)
		}
	}
	return groups, rows.Err()
}

func sessionHost(ref string) string {
	host, rest, ok := strings.Cut(ref, ":")
	if !ok || rest == "" || sessionHosts[host] == "" {
		return ""
	}
	return host
}

func groupOf(k groupKey, members []string) GraphGroup {
	g := GraphGroup{Kind: k.kind, Key: k.key, Members: members}
	switch k.kind {
	case RelSession:
		g.Host = sessionHost(k.key)
		_, id, _ := strings.Cut(k.key, ":")
		if r := []rune(id); len(r) > 8 {
			id = string(r[:8])
		}
		g.Label = sessionHosts[g.Host] + " 세션 " + id
	case RelFile:
		g.Label = filepath.Base(k.key)
	default:
		g.Label = k.key
	}
	g.Label = MaskSecrets(g.Label)
	return g
}

func graphNode(m Memory) GraphNode {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	return GraphNode{ID: m.ID, Kind: m.Kind, State: m.State, Scope: m.Scope, Project: m.Project, Title: MaskSecrets(m.Title),
		Tags: tags, CreatedAt: GraphTime(m.CreatedAt), UpdatedAt: GraphTime(m.UpdatedAt), SupersededBy: m.SupersededBy}
}

// graphFilter is kinds·states·scopes·tags·기간. 빈 목록은 모두(상태는 숨김을 뺀 기본).
func graphFilter(in GraphInput) func(Memory) bool {
	kinds, states, scopes := set(in.Kinds, Kinds), set(in.States, DefaultGraphStates), set(in.Scopes, nil)
	tags := set(in.Tags, nil)
	return func(m Memory) bool {
		if !kinds[m.Kind] || !states[m.State] {
			return false
		}
		if len(scopes) > 0 && !scopes[m.Scope] {
			return false
		}
		if in.UpdatedFrom > 0 && m.UpdatedAt < in.UpdatedFrom || in.UpdatedTo > 0 && m.UpdatedAt > in.UpdatedTo {
			return false
		}
		if len(tags) > 0 {
			for _, t := range m.Tags {
				if tags[t] {
					return true
				}
			}
			return false
		}
		return true
	}
}

// neighborhood walks from Focus up to Depth steps over Via. 걸러진 기억은 건너지
// 않는다. focus는 거르기와 상관없이 들어간다.
func neighborhood(in GraphInput, byID map[string]Memory, links []Link, groups map[groupKey][]string, pass func(Memory) bool) map[string]bool {
	via := set(in.Via, DefaultVia)
	depth := in.Depth
	if depth <= 0 {
		depth = 1
	}
	if depth > GraphDepthMax {
		depth = GraphDepthMax
	}
	adj := map[string][]string{}
	edge := func(a, b string) {
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	if via[RelLink] {
		for _, l := range links {
			edge(l.A, l.B)
		}
	}
	if via[RelSupersede] {
		for _, m := range byID {
			if m.SupersededBy != "" {
				edge(m.ID, m.SupersededBy)
			}
		}
	}
	// 묶음은 소속끼리 쌍으로 잇지 않고(태그 하나가 수만 쌍이 된다) 기억 → 묶음 → 기억으로 건넌다.
	memberOf := map[string][]groupKey{}
	for k, ids := range groups {
		if !via[k.kind] {
			continue
		}
		for _, id := range ids {
			memberOf[id] = append(memberOf[id], k)
		}
	}
	ok := func(id string) bool {
		m, has := byID[id]
		return has && pass(m)
	}
	seen := map[string]bool{in.Focus: true}
	usedGroup := map[groupKey]bool{}
	frontier := []string{in.Focus}
	for step := 0; step < depth && len(frontier) > 0; step++ {
		var next []string
		visit := func(id string) {
			if !seen[id] && ok(id) {
				seen[id] = true
				next = append(next, id)
			}
		}
		for _, id := range frontier {
			for _, n := range adj[id] {
				visit(n)
			}
			for _, k := range memberOf[id] {
				if usedGroup[k] {
					continue // 먼저 들른 묶음의 소속은 이미 다 만났다
				}
				usedGroup[k] = true
				for _, n := range groups[k] {
					visit(n)
				}
			}
		}
		frontier = next
	}
	return seen
}

// keepRecent keeps the limit most recent memories, and the focus even when it is older.
func keepRecent(ordered []Memory, limit int, focus string) []Memory {
	head := ordered[:limit]
	if focus == "" {
		return head
	}
	for _, m := range head {
		if m.ID == focus {
			return head
		}
	}
	out := append([]Memory{}, ordered[:limit-1]...)
	for _, m := range ordered[limit-1:] {
		if m.ID == focus {
			return append(out, m)
		}
	}
	return head
}

func set(list, fallback []string) map[string]bool {
	if len(list) == 0 {
		list = fallback
	}
	out := make(map[string]bool, len(list))
	for _, v := range list {
		out[v] = true
	}
	return out
}
