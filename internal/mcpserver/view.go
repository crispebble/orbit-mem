package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// view.go is the one place that shapes what memory_find and memory_get show: an index
// of the hits and a size-capped view of a memory. The MCP server and `orbit-mem host
// call` both reach it through the handlers, so they give the same result. The shape is
// a matter of this surface, not of the memory domain, so it lives here and not in
// internal/memory.
//
// Every limit is on the JSON the SDK sends (its TextContent text is the structured
// content as encoding/json writes it, so `<` costs 6 runes and `"` costs 2), not on the
// raw text.

const (
	// memory_find
	findDefaultLimit = 8
	findMaxLimit     = 30
	// findItemBudget is the serialized size of one hit that keeps the default 8 hits
	// within 2,000 runes: (2,000 - `{"items":[]}` 12 - 7 commas) / 8.
	findItemBudget   = 247
	findTitleRunes   = 100
	findAppliesRunes = 60
	findSummaryRunes = 200

	// memory_get
	getBodyRunes = 2000
	// getBodyMin is the room the body keeps before other fields are cut; getTitleFloor is the
	// shortest the title is cut to.
	getBodyMin    = 1000
	getTitleFloor = 100
	// getItemBudget is the serialized size of one view that keeps a one-memory result
	// within 2,500 runes: 2,500 - `{"items":[` 10 - `]}` 2.
	getItemBudget   = 2488
	getStaleRunes   = 100
	getSources      = 2
	getRefRunes     = 60
	getExcerptRunes = 80
	getLinks        = 5
	getLinkTitle    = 40

	// bodyMore is what a body cut short ends with.
	bodyMore = "… (전문은 full: true)"
)

// Detail values of memory_find.
const (
	DetailIndex   = "index"
	DetailSummary = "summary"
)

// jsonLen is the length in runes of what encoding/json writes for v.
func jsonLen(v any) int {
	b, _ := json.Marshal(v)
	return utf8.RuneCount(b)
}

// oneLine joins the lines of s into one.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip cuts s to at most n runes, the last one being "…" when something was cut.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// fitClip is the longest clip(s, n), n in [1, max], that ok accepts; ok must be monotone
// (a longer clip is never smaller). When even one rune does not fit it gives "…" and false.
func fitClip(s string, max int, ok func(string) bool) (string, bool) {
	if utf8.RuneCountInString(s) < max {
		max = utf8.RuneCountInString(s)
	}
	lo, hi, best := 1, max, ""
	for lo <= hi {
		mid := (lo + hi) / 2
		if c := clip(s, mid); ok(c) {
			best, lo = c, mid+1
		} else {
			hi = mid - 1
		}
	}
	if best == "" {
		return "…", false
	}
	return best, true
}

// FindItem is one hit of memory_find. By default it is an index line: enough to pick
// what to read with memory_get. detail "summary" adds the excerpt, tags and pin.
type FindItem struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	State     string   `json:"state"`
	AppliesTo string   `json:"appliesTo"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Pinned    bool     `json:"pinned,omitempty"`
	Trust     string   `json:"trust,omitempty"` // 믿을 수 없는 기억(untrusted·system)만
}

// findDetail checks the detail argument of memory_find.
func findDetail(d string) (string, error) {
	switch d {
	case "", DetailIndex:
		return DetailIndex, nil
	case DetailSummary:
		return DetailSummary, nil
	}
	return "", fmt.Errorf("detail은 index·summary입니다")
}

// findLimit is the number of hits asked for: 8 by default, 30 at most.
func findLimit(n int) int {
	switch {
	case n <= 0:
		return findDefaultLimit
	case n > findMaxLimit:
		return findMaxLimit
	}
	return n
}

// findItem shapes one hit. The index line is held to findItemBudget serialized runes,
// whatever the characters are: the title is cut first, then the applies-to.
func findItem(m memory.Memory, detail string) FindItem {
	it := FindItem{ID: m.ID, Kind: m.Kind, State: m.State,
		AppliesTo: clip(oneLine(m.AppliesTo), findAppliesRunes), Title: clip(oneLine(m.Title), findTitleRunes), Trust: untrustedOf(m)}
	if detail == DetailSummary {
		it.Summary = clip(oneLine(m.Body), findSummaryRunes)
		it.Tags = m.Tags
		it.Pinned = m.Pinned
		return it
	}
	if jsonLen(it) <= findItemBudget {
		return it
	}
	title := it.Title
	fits, ok := fitClip(title, findTitleRunes, func(c string) bool { it.Title = c; return jsonLen(it) <= findItemBudget })
	it.Title = fits
	if ok {
		return it
	}
	applies := it.AppliesTo
	it.AppliesTo, _ = fitClip(applies, findAppliesRunes, func(c string) bool { it.AppliesTo = c; return jsonLen(it) <= findItemBudget })
	return it
}

// SourceView is a source as memory_get shows it.
type SourceView struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

// MemoryView is a memory as memory_get shows it. By default it is held to about 2,500
// serialized runes (a body of 2,000 runes at most); full: true gives the whole body, every
// source and every link.
type MemoryView struct {
	ID           string       `json:"id"`
	Kind         string       `json:"kind"`
	State        string       `json:"state"`
	AppliesTo    string       `json:"appliesTo"`
	Title        string       `json:"title"`
	Topic        string       `json:"topic,omitempty"`
	Body         string       `json:"body"`
	Tags         []string     `json:"tags"`
	Version      int          `json:"version"`
	UpdatedAt    int64        `json:"updatedAt"`
	Pinned       bool         `json:"pinned,omitempty"`
	Retention    string       `json:"retention,omitempty"`
	ReviewAt     int64        `json:"reviewAt,omitempty"`
	ExpiresAt    int64        `json:"expiresAt,omitempty"`
	Tools        []string     `json:"tools,omitempty"`
	SupersededBy string       `json:"supersededBy,omitempty"`
	StaleReason  string       `json:"staleReason,omitempty"`
	Sources      []SourceView `json:"sources,omitempty"`
	SourcesMore  int          `json:"sourcesMore,omitempty"` // 보이지 않은 출처 수
	// Links are lines "〈상대 제목〉 — 〈상대 id〉" (full: with " · 〈note〉"). By default at most
	// 5 lines, then "외 N개".
	Links []string `json:"links,omitempty"`
	Trust string   `json:"trust,omitempty"` // 믿을 수 없는 기억(untrusted·system)만
}

// untrustedOf is the trust of m when it is not to be believed, else "": the views carry it only then.
func untrustedOf(m memory.Memory) string {
	if memory.Untrusted(m.Trust) {
		return m.Trust
	}
	return ""
}

// linkTitles gives the title of the memory at the other end of each link; "" when it is gone.
type linkTitles func(id string) string

// linkLines writes the links of m. Cut, it lists the first getLinks with titles of getLinkTitle
// runes and counts the rest.
func linkLines(links []memory.MemoryLink, title linkTitles, full bool) []string {
	if len(links) == 0 {
		return nil
	}
	shown := links
	if !full && len(shown) > getLinks {
		shown = shown[:getLinks]
	}
	out := make([]string, 0, len(shown)+1)
	for _, l := range shown {
		t := oneLine(title(l.MemoryID))
		if !full {
			t = clip(t, getLinkTitle)
		}
		line := l.MemoryID
		if t != "" {
			line = t + " — " + l.MemoryID
		}
		if full && l.Note != "" {
			line += " · " + oneLine(l.Note)
		}
		out = append(out, line)
	}
	if len(shown) < len(links) {
		out = append(out, fmt.Sprintf("외 %d개", len(links)-len(shown)))
	}
	return out
}

// memoryView shapes m. With full the body, sources and links come whole; without it the
// result is held to getItemBudget serialized runes, leaving out the least needed parts
// by cutting the body (at most 2,000 runes) to the room left. Only when that room is under
// 1,000 runes are other parts left out, in this order: links, sources, tags and tools,
// stale reason and topic, title (not below 100 runes); the body then takes the room left.
func memoryView(m memory.Memory, title linkTitles, full bool) MemoryView {
	v := MemoryView{ID: m.ID, Kind: m.Kind, State: m.State, AppliesTo: m.AppliesTo, Title: m.Title, Topic: m.Topic,
		Body: m.Body, Tags: m.Tags, Version: m.Version, UpdatedAt: m.UpdatedAt, Pinned: m.Pinned, Retention: m.Retention,
		ReviewAt: m.ReviewAt, ExpiresAt: m.ExpiresAt, Tools: m.Tools, SupersededBy: m.SupersededBy, StaleReason: m.StaleReason, Trust: untrustedOf(m)}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	if full {
		for _, s := range m.Sources {
			v.Sources = append(v.Sources, SourceView{Kind: s.Kind, Ref: s.Ref, Excerpt: s.Excerpt})
		}
		v.Links = linkLines(m.Links, title, true)
		return v
	}

	// appliesTo와 topic은 키라서 자르지 않는다. topic은 줄이기 단계에서 통째로만 뺀다.
	v.StaleReason = clip(oneLine(v.StaleReason), getStaleRunes)
	srcs := m.Sources
	if len(srcs) > getSources { // 최근 것
		srcs = srcs[len(srcs)-getSources:]
	}
	for _, s := range srcs {
		v.Sources = append(v.Sources, SourceView{Kind: s.Kind, Ref: clip(oneLine(s.Ref), getRefRunes), Excerpt: clip(oneLine(s.Excerpt), getExcerptRunes)})
	}
	v.SourcesMore = len(m.Sources) - len(srcs)
	v.Links = linkLines(m.Links, title, false)

	// 본문은 나머지 칸을 그대로 둔 채 남은 자리(최대 2,000룬)까지 보인다.
	head := []rune(m.Body)
	if len(head) > getBodyRunes {
		head = head[:getBodyRunes]
	}
	cut := len([]rune(m.Body)) > len(head)
	withBody := func(w MemoryView, n int) MemoryView {
		if n < len(head) || cut {
			w.Body = string(head[:n]) + bodyMore
		} else {
			w.Body = string(head)
		}
		return w
	}
	// 예산 안에 드는 가장 긴 본문 룬 수. 빈 본문도 안 들면 0.
	room := func(w MemoryView) int {
		n := sort.Search(len(head)+1, func(n int) bool { return jsonLen(withBody(w, n)) > getItemBudget })
		if n > 0 {
			n--
		}
		return n
	}
	// 본문 자리가 1,000룬보다 작을 때만 나머지 칸을 차례로 줄인다.
	need := len(head)
	if need > getBodyMin {
		need = getBodyMin
	}
	steps := []func(){
		func() {
			if len(m.Links) > 0 {
				v.Links = []string{fmt.Sprintf("외 %d개", len(m.Links))}
			}
		},
		func() { v.Sources, v.SourcesMore = nil, len(m.Sources) },
		func() { v.Tags, v.Tools = []string{}, nil },
		func() { v.StaleReason, v.Topic = "", "" },
		func() {
			v.Title, _ = fitClip(oneLine(m.Title), utf8Count(m.Title), func(c string) bool {
				w := v
				w.Title = c
				return utf8Count(c) <= getTitleFloor || room(w) >= need
			})
		},
	}
	n := room(v)
	for _, step := range steps {
		if n >= need {
			break
		}
		step()
		n = room(v)
	}
	// 모든 칸을 줄이고 본문을 0까지 줄여도 넘는 비정상 자료에서만 appliesTo를 자른다.
	if jsonLen(withBody(v, 0)) > getItemBudget {
		applies := oneLine(m.AppliesTo)
		v.AppliesTo, _ = fitClip(applies, utf8Count(applies), func(c string) bool {
			w := v
			w.AppliesTo = c
			return jsonLen(withBody(w, 0)) <= getItemBudget
		})
		n = room(v)
	}
	return withBody(v, n)
}

func utf8Count(s string) int { return utf8.RuneCountInString(s) }
