package bench

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// Prepared is a set with a DB ready to be copied for every recall path.
type Prepared struct {
	Set  *Set              // a copy of the set whose cases name memories by id
	Keys map[string]string // id → key, only for a DB made from the memories of the file

	ids      map[string]string // key → id
	template string
	dir      string
	n        int
}

const (
	dayMs         = int64(24 * time.Hour / time.Millisecond)
	volatileDays  = 7  // memory.DefaultVolatileDays and MaxVolatileDays
	reviewDefault = 90 // memory.DefaultReviewDays
)

// Prepare makes the DB of a set. With memories in the file it builds a temporary DB
// with fixed ids (m-000000000001 in file order). With a DB path it copies that DB with
// a read only connection (the file, its -wal and its -shm are not written) and checks
// that every id the cases name is in it. Exactly one of the two is given.
func Prepare(set *Set, dbPath string) (p *Prepared, err error) {
	switch {
	case len(set.Memories) > 0 && dbPath != "":
		return nil, errors.New("the case file has memories and a DB is given too: use one")
	case len(set.Memories) == 0 && dbPath == "":
		return nil, errors.New("the case file has no memories and no DB is given")
	}
	dir, err := os.MkdirTemp("", "orbit-bench-")
	if err != nil {
		return nil, err
	}
	p = &Prepared{dir: dir, ids: map[string]string{}, Keys: map[string]string{}}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
			p = nil
		}
	}()
	if dbPath != "" {
		if p.template, err = copyDB(dbPath, dir); err != nil {
			return nil, err
		}
	} else if p.template, err = p.seed(set, dir); err != nil {
		return nil, err
	}
	if p.Set, err = p.resolve(set); err != nil {
		return nil, err
	}
	return p, nil
}

// copyDB copies a DB into dir with CopyDB (the source is never opened) and checks its schema.
func copyDB(path, dir string) (string, error) {
	if err := os.MkdirAll(filepath.Join(dir, "template"), 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "template", "bench.db")
	if err := CopyDB(path, dst); err != nil {
		return "", err
	}
	s, info, err := memory.OpenReadOnly(dst)
	if err != nil {
		return "", err
	}
	s.Close()
	if info.State != memory.SchemaOK {
		return "", fmt.Errorf("the DB schema is %q, not ok", info.State)
	}
	return dst, nil
}

// seed builds the DB of the memories of a set and gives a copy of it that no connection holds.
func (p *Prepared) seed(set *Set, dir string) (string, error) {
	s, err := memory.Open(filepath.Join(dir, "seed.db"))
	if err != nil {
		return "", err
	}
	defer s.Close()
	now := set.At()
	for i, m := range set.Memories {
		id := memoryID(i + 1)
		if err := importMemory(s, set, m, id, now); err != nil {
			return "", fmt.Errorf("memory %q: %w", m.Key, err)
		}
		p.ids[m.Key], p.Keys[id] = id, m.Key
	}
	s.Now = func() time.Time { return now }
	return s.Snapshot(filepath.Join(dir, "template"), "bench")
}

func importMemory(s *memory.Store, set *Set, m MemorySpec, id string, now time.Time) error {
	at := now.AddDate(0, 0, -m.AgeDays)
	atMs := at.UnixMilli()
	scope := m.Scope
	if scope == ScopeHere {
		scope = "project:" + set.CWD
	}
	state := m.State
	src := memory.Source{Kind: memory.SourceSession, Ref: "fixture"}
	if m.Quote {
		src = memory.Source{Kind: memory.SourceQuote, Excerpt: m.Title}
	}
	if state == "" {
		state = memory.StateCandidate
		if m.Quote {
			state = memory.StateConfirmed
		}
	}
	retention, ok := memory.NormalizeRetention(m.Retention)
	if !ok {
		return fmt.Errorf("retention %q", m.Retention)
	}
	mem := memory.Memory{ID: id, Kind: m.Kind, Scope: scope, State: state, Title: m.Title, Body: m.Body, Tags: m.Tags, Version: 1,
		CreatedAt: atMs, UpdatedAt: atMs, Retention: retention, Topic: m.Topic, Tools: m.Tools, Sources: []memory.Source{src}}
	switch {
	case retention == memory.RetentionVolatile:
		ttl := m.TTLDays
		if ttl == 0 {
			ttl = volatileDays
		}
		if ttl < 1 || ttl > volatileDays {
			return fmt.Errorf("ttlDays %d, from 1 to %d", ttl, volatileDays)
		}
		mem.ExpiresAt = atMs + int64(ttl)*dayMs
	case m.TTLDays != 0:
		return errors.New("ttlDays is for volatile memories only")
	}
	review := m.ReviewAfterDays
	if retention == memory.RetentionDurable && review == 0 {
		review = reviewDefault
	}
	if review < 0 {
		return errors.New("reviewAfterDays is negative")
	}
	if review > 0 {
		mem.ReviewDays, mem.ReviewAt = review, atMs+int64(review)*dayMs
	}
	s.Now = func() time.Time { return at }
	res, err := s.Import(mem, "bench fixture", false)
	if err != nil {
		return err
	}
	if res != memory.ImportDone {
		// 같은 종류·범위·제목은 Import가 조용히 버린다: 범위나 종류를 달리해 둔다.
		return fmt.Errorf("import gave %q (the same kind, scope and title as an earlier memory, or a form the store refuses)", res)
	}
	if m.Pinned {
		if _, err := s.Pin(id, memory.Who{}); err != nil {
			return fmt.Errorf("pin: %w", err)
		}
	}
	s.Now = func() time.Time { return now }
	for i := 0; i < m.Followed; i++ {
		if err := s.RecordEvent(memory.Event{Kind: memory.EventMemoryUsed, Day: memory.Day(now)},
			map[string]any{"memoryId": id, "verdict": memory.UsageFollowed}); err != nil {
			return err
		}
	}
	return nil
}

// resolve copies the set with the keys of its cases turned into ids, or, for a DB from
// outside, checks that the ids are in the DB.
func (p *Prepared) resolve(set *Set) (*Set, error) {
	out := *set
	out.Cases = make([]Case, len(set.Cases))
	var db *memory.Store
	if len(p.ids) == 0 {
		var err error
		if db, _, err = memory.OpenReadOnly(p.template); err != nil {
			return nil, err
		}
		defer db.Close()
	}
	ref := func(c Case, r string) (string, error) {
		if db == nil {
			return p.ids[r], nil
		}
		if !db.Exists(r) {
			return "", fmt.Errorf("case %q: no memory with id %q in the DB", c.ID, r)
		}
		return r, nil
	}
	for i, c := range set.Cases {
		for _, list := range []*[]string{&c.Expect, &c.Allow, &c.Forbid} {
			ids := make([]string, 0, len(*list))
			for _, r := range *list {
				id, err := ref(c, r)
				if err != nil {
					return nil, err
				}
				ids = append(ids, id)
			}
			*list = slices.Clip(ids)
			if len(ids) == 0 {
				*list = nil
			}
		}
		out.Cases[i] = c
	}
	return &out, nil
}

// IDOf is the id of a memory key ("" if the DB came from outside).
func (p *Prepared) IDOf(key string) string { return p.ids[key] }

// Copy gives a fresh copy of the DB for one recall path.
func (p *Prepared) Copy() (string, error) {
	in, err := os.Open(p.template)
	if err != nil {
		return "", err
	}
	defer in.Close()
	p.n++
	dst := filepath.Join(p.dir, fmt.Sprintf("run-%d.db", p.n))
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	return dst, out.Close()
}

// Close removes what Prepare made.
func (p *Prepared) Close() error { return os.RemoveAll(p.dir) }
