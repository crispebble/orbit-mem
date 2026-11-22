package bench

import (
	"encoding/json"
	"os"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// Why a memory put in was not a current value at the time (DeadReason). A replay and the audit of the recorded
// injections judge by the same rule, so the two can be compared pair by pair.
const (
	DeadStale   = "stale"   // 낡음: 대체된 것도 이 상태다(memory/supersede.go)
	DeadHidden  = "hidden"  // 거절된 후보
	DeadExpired = "expired" // 날이 지난 휘발 기억(사용자 말이라 상태를 바꾸지 않은 것도)
	DeadGone    = "gone"    // 재생 사본에 없는 기억(지워짐). 감사에서는 판정하지 못함(Unknown)으로 센다
)

// DeadReason says why memory m, as it was at at (ms), was not a current value then: stale (superseded included),
// hidden, or volatile past its days; "" when it was current. It does not judge the topic heads (an older value of a
// topic that is still live).
func DeadReason(m memory.Memory, at int64) string {
	switch {
	case m.State == memory.StateStale:
		return DeadStale
	case m.State == memory.StateHidden:
		return DeadHidden
	case m.Freshness(time.UnixMilli(at)) == memory.FreshExpired:
		return DeadExpired
	}
	return ""
}

// InjectionAudit is what AuditInjections found in the recorded injections of a DB (명세 · 기억 정확성 2절, F-6).
type InjectionAudit struct {
	Events     int `json:"events"`     // injection events read (inject.*, the host's delivered reports left out)
	Injections int `json:"injections"` // ids those events put in, one per event and id
	Dead       int `json:"dead"`       // of them, ids that were not a current value at the event
	DeadEvents int `json:"deadEvents"` // events with a dead id
	Pairs      int `json:"pairs"`      // distinct (session, memory) pairs of the dead ids
	Memories   int `json:"memories"`   // distinct memories of the dead ids
	// Unknown counts the ids put in that the DB no longer has (forgotten): their state at the event is not known,
	// so they are neither dead nor current here.
	Unknown int             `json:"unknown"`
	ByKind  map[string]int  `json:"byKind"` // dead ids per event kind
	ByWhy   map[string]int  `json:"byWhy"`  // dead ids per DeadReason
	Items   []DeadInjection `json:"items"`  // every dead id, in event order
}

// DeadInjection is one dead id put in by one recorded event.
type DeadInjection struct {
	Event    int64  `json:"event"` // the event's id in the DB
	At       int64  `json:"at"`    // the event's time (Unix ms)
	Kind     string `json:"kind"`
	Session  string `json:"session"` // the event's session key ("<session>/<agent>" for a subagent)
	MemoryID string `json:"memoryId"`
	Why      string `json:"why"`
	// Late is how long (ms) it had been dead at the event: since the last change of the version it had then (stale,
	// hidden), or since its expiry (expired).
	Late int64 `json:"late"`
}

// AuditInjections reads the injection events recorded in the memory DB at path and counts, for every id they put
// in, whether the memory was a current value at the event's time (DeadReason on memory.History.StateAt). It works
// on a private copy (CopyDB never opens path; an older schema is migrated on the copy only) and removes the copy
// after; path is not written. The result is the same for the same DB.
func AuditInjections(path string) (InjectionAudit, error) {
	out := InjectionAudit{ByKind: map[string]int{}, ByWhy: map[string]int{}, Items: []DeadInjection{}}
	dir, err := os.MkdirTemp("", "orbit-bench-audit-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	copyPath, err := copyDB(path, dir)
	if err != nil {
		return out, err
	}
	h, err := memory.ReadHistory(copyPath)
	if err != nil {
		return out, err
	}
	s, _, err := memory.OpenReadOnly(copyPath)
	if err != nil {
		return out, err
	}
	defer s.Close()
	events, err := s.Events(memory.EventFilter{FromDay: "", ToDay: "9999-12-31", KindPrefix: "inject."})
	if err != nil {
		return out, err
	}
	pairs, mems := map[string]bool{}, map[string]bool{}
	for _, e := range events {
		if e.Kind == memory.EventInjectDelivered {
			continue
		}
		out.Events++
		var data struct {
			MemoryIDs []string `json:"memoryIds"`
		}
		_ = json.Unmarshal(e.Data, &data) // 칸이 없거나 깨진 사건은 넣은 id가 없는 것으로 센다
		dead := false
		for _, id := range data.MemoryIDs {
			out.Injections++
			m, ok := h.StateAt(id, e.At)
			if !ok {
				out.Unknown++
				continue
			}
			why := DeadReason(m, e.At)
			if why == "" {
				continue
			}
			late := e.At - m.UpdatedAt
			if why == DeadExpired {
				late = e.At - m.ExpiresAt
			}
			out.Items = append(out.Items, DeadInjection{Event: e.ID, At: e.At, Kind: e.Kind, Session: e.Session, MemoryID: id, Why: why, Late: late})
			out.Dead++
			out.ByKind[e.Kind]++
			out.ByWhy[why]++
			pairs[e.Session+"\x00"+id], mems[id] = true, true
			dead = true
		}
		if dead {
			out.DeadEvents++
		}
	}
	out.Pairs, out.Memories = len(pairs), len(mems)
	return out, nil
}
