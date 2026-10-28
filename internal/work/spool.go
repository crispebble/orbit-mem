package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onidot-labs/orbit/internal/sqlitedb"
)

// SpoolEvent leaves an event the hook could not write (database locked or
// being migrated). The next process that opens the database drains it first
// (설계 「실패·중단·재개」). Each event is its own file so writers never share one.
func SpoolEvent(dir string, e Event) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%d-%s", time.Now().UnixNano(), os.Getpid(), newDBID())
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name+".json"))
}

// Drain applies spooled events in the order they were written and removes
// them. A file another process already claimed is left to that process.
func (s *Store) Drain() (int, error) {
	if s.Spool == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(s.Spool)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			names = append(names, n)
		} else if orig, ok := orphanClaim(n); ok {
			// 가져간 프로세스가 지우기 전에 끝났다: 되돌려 다시 처리한다
			if os.Rename(filepath.Join(s.Spool, n), filepath.Join(s.Spool, orig)) == nil {
				names = append(names, orig)
			}
		}
	}
	sort.Strings(names)
	n := 0
	for _, name := range names {
		src := filepath.Join(s.Spool, name)
		claimed := filepath.Join(s.Spool, "."+name+fmt.Sprintf(".%d.claim", os.Getpid()))
		if os.Rename(src, claimed) != nil {
			continue // 다른 프로세스가 가져갔다
		}
		b, err := os.ReadFile(claimed)
		var e Event
		if err == nil {
			err = json.Unmarshal(b, &e)
		}
		if err != nil {
			// 읽을 수 없는 파일은 지우지 않고 옆으로 둔다
			_ = os.Rename(claimed, filepath.Join(s.Spool, name+".bad"))
			continue
		}
		e.Seq = 0
		if _, err := s.Append(e); err != nil {
			if sqlitedb.IsBusy(err) {
				_ = os.Rename(claimed, src) // 다음 번에 다시
				return n, err
			}
			// 다시 해도 같은 결과인 사건(이 판이 읽지 못하는 모양 등)은 옆으로 두고 다음으로 간다.
			// 남겨 두면 뒤의 모든 기록이 스풀에 멈춘다.
			_ = os.Rename(claimed, filepath.Join(s.Spool, name+".bad"))
			continue
		}
		_ = os.Remove(claimed)
		n++
	}
	return n, nil
}

// orphanClaim reads a claim file name (.<name>.<pid>.claim) and says whether the
// process that took it has ended.
func orphanClaim(n string) (string, bool) {
	if !strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".claim") {
		return "", false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(n, "."), ".claim")
	i := strings.LastIndex(rest, ".")
	if i < 0 {
		return "", false
	}
	pid, err := strconv.Atoi(rest[i+1:])
	if err != nil || pid == os.Getpid() || alive(pid) {
		return "", false
	}
	return rest[:i], true
}

// alive says whether a process exists. Anything but "no such process" counts
// as alive, so a claim is never taken from a running process.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || !(errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH))
}

// Record appends the event, or spools it when the database cannot take it now.
// It drains earlier spooled events first so they keep their order.
func Record(path, spool string, e Event) (Result, error) {
	s, err := Open(path)
	if err != nil {
		return Result{}, spoolOr(spool, e, err)
	}
	defer s.Close()
	s.Spool = spool
	if _, err := s.Drain(); err != nil {
		return Result{}, spoolOr(spool, e, err)
	}
	res, err := s.Append(e)
	if err != nil {
		return Result{}, spoolOr(spool, e, err)
	}
	return res, nil
}

func spoolOr(dir string, e Event, cause error) error {
	if dir == "" {
		return cause
	}
	if err := SpoolEvent(dir, e); err != nil {
		return fmt.Errorf("%v; 스풀에도 남기지 못했습니다: %w", cause, err)
	}
	return nil
}

// RecordAll appends events from one hook call with one connection. Events the
// database cannot take now go to the spool in order. It waits for the lock at
// most half the time left before until, so the hook ends with every event either
// written or spooled; a hook cut off in the middle of a write would lose it.
func RecordAll(path, spool string, evs []Event, until time.Time) []Result {
	out := make([]Result, len(evs))
	busy := min(BusyTimeout, time.Until(until)/2)
	if busy < time.Millisecond {
		for _, e := range evs {
			_ = spoolOr(spool, e, errors.New("훅 시간이 모자랍니다"))
		}
		return out
	}
	s, err := open(path, busy)
	if err != nil {
		for _, e := range evs {
			_ = spoolOr(spool, e, err)
		}
		return out
	}
	defer s.Close()
	s.Spool = spool
	if _, err := s.Drain(); err != nil {
		for _, e := range evs {
			_ = spoolOr(spool, e, err)
		}
		return out
	}
	for i, e := range evs {
		res, err := s.Append(e)
		if err != nil {
			for _, rest := range evs[i:] {
				_ = spoolOr(spool, rest, err)
			}
			return out
		}
		out[i] = res
	}
	return out
}
