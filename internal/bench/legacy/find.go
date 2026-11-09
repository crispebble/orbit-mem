package legacy

// A copy of memory_find at 87ab0fb as the bench called it (scope here, no kinds, states or tags), and of
// the find-proxy similar search of the bench (internal/bench/hostpath/hostpath.go at e18b75e, the 1-5 head
// on 87ab0fb). The evaluation's recover and duplicate measures read them, so they are frozen with the
// recall: the live search moved to the FTS5 index in 272b9ca (1-3) and must not move this baseline.
//
// The host of that day also recorded a tool.memory_find event for each call. No recall step reads tool
// events (UsageCounts reads memory.used only), so the copy does not write them.

import (
	"sort"
	"strings"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/project"
)

// The memory_find limits: internal/mcpserver/view.go:25-26 at 87ab0fb (findDefaultLimit·findMaxLimit).
const (
	findDefaultLimit = 8
	findMaxLimit     = 30
)

// similarOf is the number of ids the find-proxy asks for: internal/bench/hostpath/hostpath.go at e18b75e
// (similarOf = bench.SimilarLimit, 3).
const similarOf = 3

// rank is internal/memory/search.go:21 at 87ab0fb (Rank).
type rank struct{ Score, Title float64 }

// relevance is internal/memory/search.go:80-95 at 87ab0fb (Relevance): every query word (up to 16) scores in
// the title, body and tags, and the whole query found as written adds one. An empty query matches everything
// with score 1.
func relevance(d doc, q string) rank {
	list := terms(q, 16)
	if len(list) == 0 {
		return rank{Score: 1}
	}
	title, all := haystack(d)
	var r rank
	for _, t := range list {
		r.Score += termScore(t, all)
		r.Title += termScore(t, title)
	}
	if r.Score > 0 && len(list) > 1 && strings.Contains(all, strings.Join(list, " ")) {
		r.Score++
	}
	return r
}

// findLimit is internal/mcpserver/view.go:127-136 at 87ab0fb: 8 by default, 30 at most.
func findLimit(n int) int {
	switch {
	case n <= 0:
		return findDefaultLimit
	case n > findMaxLimit:
		return findMaxLimit
	}
	return n
}

// here is the place memory_find saw for a cwd at 87ab0fb: the host resolved the folder
// (internal/host/ops.go:149-158, locate) and handed its scope and folder to the tools
// (internal/host/ops.go:324-325), which took place.At(Project, Dir, products)
// (internal/mcpserver/server.go:52-55, Options.here).
func here(cwd string, products place.Map) place.Place {
	var p project.Project
	if cwd != "" {
		p = project.Resolve(cwd)
	}
	place.Known(p)
	return place.At(p.Scope, p.Dir, products)
}

// find is internal/memory/store.go:875-949 at 87ab0fb (Store.Find) with the input memory_find gave
// (internal/mcpserver/server.go:206-235, handlers.find, scope here): candidates and confirmed memories
// this place sees, best first. Kinds and tags were empty and are not copied.
func find(s *memory.Store, pl place.Place, query string, limit int) ([]memory.Memory, error) {
	all, err := s.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return nil, err
	}
	type ranked struct {
		m memory.Memory
		r rank
	}
	var hits []ranked
	for _, m := range all {
		if !pl.Sees(m.Scope, m.Project) {
			continue
		}
		r := relevance(docOf(m), query)
		if r.Score <= 0 {
			continue
		}
		hits = append(hits, ranked{m, r})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.r.Score != b.r.Score {
			return a.r.Score > b.r.Score
		}
		// 후보는 회상에 쓰되 확정보다 뒤에 둔다(A2).
		if ca, cb := a.m.State == memory.StateConfirmed, b.m.State == memory.StateConfirmed; ca != cb {
			return ca
		}
		if a.r.Title != b.r.Title {
			return a.r.Title > b.r.Title
		}
		if a.m.UpdatedAt != b.m.UpdatedAt {
			return a.m.UpdatedAt > b.m.UpdatedAt
		}
		return a.m.ID < b.m.ID
	})
	limit = findLimit(limit)
	var out []memory.Memory
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].m)
	}
	return out, nil
}

// finder is the bench Finder of the frozen path: memory_find of 87ab0fb on the same DB copy.
type finder struct {
	s        *memory.Store
	products place.Map
}

// Find gives the ids of the top hits, as internal/bench/hostpath/hostpath.go at e18b75e read them from
// the memory_find result.
func (f finder) Find(cwd, query string, limit int) ([]string, error) {
	ms, err := find(f.s, here(cwd, f.products), query, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// similar is the find-proxy of internal/bench/hostpath/hostpath.go at e18b75e (similar.Similar): memory_find
// over the title and the body of the draft, top 3.
type similar struct{ f finder }

func (s similar) Similar(cwd string, d bench.Draft) ([]string, error) {
	q := d.Title
	if d.Body != "" {
		q += "\n" + d.Body
	}
	return s.f.Find(cwd, q, similarOf)
}
