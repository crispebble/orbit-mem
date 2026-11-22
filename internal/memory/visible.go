package memory

// 보임 판정(명세 6.2 변경 축): 「이 기억이 현재 값으로 보일 수 있는가」를 한 곳에서 가른다. 주입·도구 회상·
// 하위 에이전트·진단·측정이 모두 Viewer를 쓰므로, 판정 규칙(만료·옛 버전·어긋남)이 늘어도 한 곳만 고친다.

import (
	"errors"
	"time"
)

// Why says why a memory does not show as a current value; WhyCurrent ("") when it does.
type Why string

const (
	WhyCurrent  Why = ""
	WhyStale    Why = "stale"               // 낡음(대체된 것 포함)
	WhyRejected Why = "rejected"            // 거절한 후보
	WhyGone     Why = "gone"                // 지웠거나 없는 id
	WhyExpired  Why = "expired"             // 날이 지난 휘발 기억. 글은 inject.ExcludedExpired와 같다
	WhyOlder    Why = "superseded_by_topic" // 같은 범위·주제의 더 새 값이 있다. 글은 inject.ExcludedSuperseded와 같다
)

// maxSuccessorHops bounds the walk along superseded_by, so a broken chain cannot loop.
const maxSuccessorHops = 8

// Visibility is the verdict on one memory at the Viewer's time.
type Visibility struct {
	Why Why
	// Successor is the id of the live (candidate or confirmed) value that replaces the memory now, or "".
	Successor string
	// ReviewDue is set when the review day has passed. The memory still shows as current.
	ReviewDue bool
	// Conflicts holds the ids of the newer values that disagree with the memory, while it is a current value
	// (명세 C-5). Of fills it only for a Viewer made by WithConflicts, and only for the ids that call named.
	Conflicts []string
}

// Current reports whether the memory may show as a current value.
func (v Visibility) Current() bool { return v.Why == WhyCurrent }

// Viewer judges memories against one reading of the store: the topic heads and the time are fixed when it
// is made, so a verdict does not change between two calls of one operation. Make a new one per operation.
type Viewer struct {
	s         *Store
	now       time.Time
	heads     TopicHeads
	conflicts map[string][]string // WithConflicts가 한 번에 읽은 열린 어긋남(옛 id → 새 id들)
}

// Viewer reads the topic heads and the clock (Store.Now) and returns the judge. It only reads. When the
// heads cannot be read the Viewer is still usable and judges no memory an older value of its topic; the
// error is returned beside it for a caller that cares.
func (s *Store) Viewer() (Viewer, error) {
	heads, err := s.TopicHeads()
	return Viewer{s: s, now: s.Now(), heads: heads}, err
}

// Of judges m. The order is the state first (stale, rejected), then the days (a volatile memory past them
// shows as expired and its state is left alone, a user's words included), then an older value of its
// topic. Only a memory with a superseded_by costs a DB read (Successor).
func (v Viewer) Of(m Memory) Visibility {
	out := Visibility{Why: v.Why(m), ReviewDue: m.Freshness(v.now) == FreshReviewDue}
	if next, ok := v.Successor(m); ok {
		out.Successor = next.ID
	}
	if out.Why == WhyCurrent {
		out.Conflicts = v.conflicts[m.ID] // 이미 읽어 둔 것만 본다: 기억마다 DB를 치지 않는다
	}
	return out
}

// WithConflicts is the Viewer with the open conflicts of ids read in one query (OpenConflictsOf), so Of fills
// Visibility.Conflicts for them without reading again. Of never reads conflicts itself: a Viewer judges many
// memories (every live one, on a recall), and a read per memory would cost a query each. A caller that shows
// a few memories names them here once. When the read fails the Viewer is returned unchanged beside the error.
func (v Viewer) WithConflicts(ids []string) (Viewer, error) {
	if v.s == nil { // 만들지 않은 Viewer(순위용 빈 값)는 읽을 곳이 없다
		return v, nil
	}
	got, err := v.s.OpenConflictsOf(ids)
	if err != nil {
		return v, err
	}
	v.conflicts = got
	return v, nil
}

// Why is Of's verdict without the successor, so it reads nothing. A list that only marks which lines are not
// current uses it, and asks for a successor only for the memory it shows whole.
func (v Viewer) Why(m Memory) Why {
	switch {
	case m.State == StateStale:
		return WhyStale
	case m.State == StateHidden:
		return WhyRejected
	case m.Freshness(v.now) == FreshExpired:
		return WhyExpired
	case v.heads.Superseding(m) != "":
		return WhyOlder
	}
	return WhyCurrent
}

// OfID reads the memory and judges it. An id the DB does not have (forgotten or never kept), or one it
// cannot read, is WhyGone with an empty Memory.
func (v Viewer) OfID(id string) (Memory, Visibility) {
	m, err := v.s.Get(id)
	if err != nil {
		return Memory{}, Visibility{Why: WhyGone}
	}
	return m, v.Of(m)
}

// Successor follows superseded_by from m to the live (candidate or confirmed) value that replaces it now,
// a few steps at most. false when m has no replacement, the chain ends in nothing live, or a step cannot be read.
func (v Viewer) Successor(m Memory) (Memory, bool) {
	n, ok, _ := v.LiveSuccessor(m)
	return n, ok
}

// LiveSuccessor is Successor that tells a read that failed apart from no successor: a step the DB does not have
// (forgotten) ends the chain with false and no error, and any other read error is returned with false. A caller
// that acts on "no successor" for good (a void line told once) waits for a later read on an error.
func (v Viewer) LiveSuccessor(m Memory) (Memory, bool, error) {
	next := m.SupersededBy
	for range maxSuccessorHops {
		if next == "" {
			return Memory{}, false, nil
		}
		n, err := v.s.Get(next)
		if errors.Is(err, ErrNotFound) {
			return Memory{}, false, nil
		}
		if err != nil {
			return Memory{}, false, err
		}
		if n.State == StateCandidate || n.State == StateConfirmed {
			return n, true, nil
		}
		next = n.SupersededBy
	}
	return Memory{}, false, nil
}

// Superseding is the id of the newer value of m's topic that stands in for it, or "" when m is the current
// value or has no topic.
func (v Viewer) Superseding(m Memory) string { return v.heads.Superseding(m) }
