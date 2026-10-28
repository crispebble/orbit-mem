package work

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
		if strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
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
			_ = os.Rename(claimed, src) // 다음 번에 다시
			return n, err
		}
		_ = os.Remove(claimed)
		n++
	}
	return n, nil
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
