// Package backup knows the copies of the memory DB in the backups folder: their
// names, which of them the daily rotation may remove, when the next daily copy is
// due and the lock that keeps two processes from copying at once (ONID-136).
//
// The folder may hold files a person put there (before-0.3.0-memory.sqlite, notes):
// only the daily copy names orbit-mem writes are ever removed.
package backup

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Prefixes of the daily copies: memory-YYYYMMDD.sqlite, work-YYYYMMDD.sqlite (the old work log).
const (
	PrefixMemory = "memory"
	PrefixWork   = "work"
)

// Kinds of copies in a listing.
const (
	KindMemory   = "memory"   // 하루 사본(memory-YYYYMMDD.sqlite)
	KindWork     = "work"     // 옛 작업 기록 하루 사본(work-YYYYMMDD.sqlite)
	KindSnapshot = "snapshot" // 이름을 정한 사본(before-*.sqlite): 순환에서 빠진다
)

// Interval is how old the last daily memory copy may get before the next one is due.
const Interval = 24 * time.Hour

// LockName is the lock file in the backups folder.
const LockName = ".backup.lock"

var daily = regexp.MustCompile(`^(memory|work)-[0-9]{8}\.sqlite$`)

// Copy is one copy in the backups folder.
type Copy struct {
	Name string    `json:"name"`
	Path string    `json:"path"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"` // 파일을 마지막으로 쓴 때
	Size int64     `json:"size"`
}

// IsDaily says whether name is a daily copy of prefix that the rotation may remove.
func IsDaily(name, prefix string) bool {
	return daily.MatchString(name) && strings.HasPrefix(name, prefix+"-")
}

func kindOf(name string) string {
	switch {
	case IsDaily(name, PrefixMemory):
		return KindMemory
	case IsDaily(name, PrefixWork):
		return KindWork
	case strings.HasPrefix(name, "before-") && strings.HasSuffix(name, ".sqlite"):
		return KindSnapshot
	}
	return ""
}

// List is every orbit-mem copy in dir (daily copies and named snapshots), newest first.
// Other files and folders are left out. A missing dir is an empty list.
func List(dir string) ([]Copy, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Copy{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Copy{}
	for _, e := range entries {
		kind := kindOf(e.Name())
		if kind == "" || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // 읽는 사이에 지워졌다
		}
		out = append(out, Copy{Name: e.Name(), Path: filepath.Join(dir, e.Name()), Kind: kind, At: info.ModTime(), Size: info.Size()})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// Last is the newest daily copy of prefix.
func Last(dir, prefix string) (Copy, bool) {
	list, _ := List(dir)
	for _, c := range list {
		if IsDaily(c.Name, prefix) {
			return c, true
		}
	}
	return Copy{}, false
}

// Count is the number of daily copies of prefix.
func Count(dir, prefix string) int {
	list, _ := List(dir)
	n := 0
	for _, c := range list {
		if IsDaily(c.Name, prefix) {
			n++
		}
	}
	return n
}

// Due says whether a daily memory copy is due: there is none, or the newest is
// Interval or more away from now. A copy dated in the future (the clock went
// back) counts by the same distance, so it does not hold back copies forever.
func Due(dir string, now time.Time) bool {
	last, ok := Last(dir, PrefixMemory)
	if !ok {
		return true
	}
	age := now.Sub(last.At)
	if age < 0 {
		age = -age
	}
	return age >= Interval
}

// Prune removes the oldest daily copies of prefix beyond keep, by the date in the
// name. Only regular files with the exact daily name are touched; keep below 1
// removes nothing. It returns the removed names.
func Prune(dir, prefix string, keep int) ([]string, error) {
	removed := []string{}
	if keep < 1 {
		return removed, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return removed, nil
	}
	if err != nil {
		return removed, err
	}
	var names []string
	for _, e := range entries {
		if IsDaily(e.Name(), prefix) && e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // 이름의 날짜순
	var firstErr error
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			removed = append(removed, names[0])
		}
		names = names[1:]
	}
	return removed, firstErr
}

// Lock takes the backups folder's lock (creating the folder), waiting up to wait.
// ok is false when another process still holds it after wait. The lock is the
// OS file lock, so a crashed holder never leaves it taken.
func Lock(dir string, wait time.Duration) (unlock func(), ok bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, LockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	deadline := time.Now().Add(wait)
	for {
		got, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, false, err
		}
		if got {
			return func() { unlockFile(f); f.Close() }, true, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, false, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}
