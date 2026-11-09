package bench_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// folderState is every file of a folder with its sha256: a file made next to the source shows up.
func folderState(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return out
}

func sameFolder(t *testing.T, before, after map[string]string) {
	t.Helper()
	for name, sum := range before {
		got, ok := after[name]
		switch {
		case !ok:
			t.Errorf("%s is gone", name)
		case got != sum:
			t.Errorf("%s changed", name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			t.Errorf("%s was made next to the source", name)
		}
	}
}

// walDB makes a DB in WAL mode in a folder of its own with one memory. With open, the writing
// connection stays open, so the memory is only in the -wal; without it the connection is closed
// and SQLite removes the -wal and the -shm.
func walDB(t *testing.T, open bool) (dir, path, id string) {
	t.Helper()
	fixHome(t)
	dir = t.TempDir()
	path = filepath.Join(dir, "memory.db")
	w, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := w.Save(memory.SaveInput{Kind: "fact", Title: "WAL에만 있을 수 있는 기억", Scope: "global", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if open {
		t.Cleanup(func() { w.Close() })
		if st, err := os.Stat(path + "-wal"); err != nil || st.Size() == 0 {
			t.Fatalf("the test needs WAL frames left: %v", err)
		}
	} else {
		w.Close()
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(path + suffix); err == nil {
				t.Fatalf("the test needs a closed DB without %s", suffix)
			}
		}
	}
	head := make([]byte, 20)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Read(head); err != nil {
		t.Fatal(err)
	}
	if head[18] != 2 || head[19] != 2 {
		t.Fatalf("the test needs a DB in WAL mode: header %v", head[18:20])
	}
	return dir, path, saved.ID
}

func hasMemory(t *testing.T, db, id string) {
	t.Helper()
	s, _, err := memory.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.Exists(id) {
		t.Errorf("the copy lacks %s", id)
	}
}

var walCases = []struct {
	name string
	open bool
}{{"닫힌_WAL_DB", false}, {"쓰는_연결이_열린_WAL_DB", true}}

func TestPrepare는_DB_원본_폴더에_아무것도_만들거나_바꾸지_않는다(t *testing.T) {
	for _, tc := range walCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, id := walDB(t, tc.open)
			before := folderState(t, dir)
			set, err := bench.Parse([]byte(fmt.Sprintf(`{"now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","cases":[{"id":"c","prompt":"p","expect":[%q]}]}`, id)))
			if err != nil {
				t.Fatal(err)
			}
			p, err := bench.Prepare(set, path)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			cp, err := p.Copy()
			if err != nil {
				t.Fatal(err)
			}
			hasMemory(t, cp, id)
			sameFolder(t, before, folderState(t, dir))
		})
	}
}

// onlyInWAL checks the premise of the open case: the DB file alone does not hold the memory.
func onlyInWAL(t *testing.T, path, id string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	alone := filepath.Join(t.TempDir(), "alone.db")
	if err := os.WriteFile(alone, b, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := memory.Open(alone)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Exists(id) {
		t.Fatal("the test needs the memory to be only in the -wal")
	}
}

func TestCopyDB는_원본을_열지_않고_WAL까지_담은_사본을_쓴다(t *testing.T) {
	for _, tc := range walCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, id := walDB(t, tc.open)
			if tc.open {
				onlyInWAL(t, path, id)
			}
			before := folderState(t, dir)
			dst := filepath.Join(t.TempDir(), "copy.db")
			if err := bench.CopyDB(path, dst); err != nil {
				t.Fatal(err)
			}
			hasMemory(t, dst, id)
			sameFolder(t, before, folderState(t, dir))
			if fi, err := os.Stat(dst); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("copy: %v %v", err, fi)
			}
		})
	}
}

func TestCopyDB는_복사_중_원본이_바뀌면_다시_뜬다(t *testing.T) {
	_, path, _ := walDB(t, true)
	w, err := memory.Open(path) // 두 번째 쓰는 연결: 복사와 확인 사이에 쓴다
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var late string
	tries := 0
	defer bench.SetAfterCopy(func(try int) {
		tries = try
		if try == 1 {
			saved, err := w.Save(memory.SaveInput{Kind: "fact", Title: "복사 도중에 쓴 기억", Scope: "global", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "y"}})
			if err != nil {
				t.Error(err)
			}
			late = saved.ID
		}
	})()
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := bench.CopyDB(path, dst); err != nil {
		t.Fatal(err)
	}
	if tries != 2 {
		t.Errorf("copied %d times, want 2 (the first copy is stale)", tries)
	}
	hasMemory(t, dst, late)
}

func TestCopyDB는_끝내_바뀌는_원본이면_사본_없이_오류다(t *testing.T) {
	_, path, _ := walDB(t, true)
	w, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer bench.SetAfterCopy(func(try int) {
		if _, err := w.Save(memory.SaveInput{Kind: "fact", Title: fmt.Sprintf("쉬지 않는 쓰기 %d", try), Scope: "global", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "z"}}); err != nil {
			t.Error(err)
		}
	})()
	defer bench.SetRetryWait(func(int) time.Duration { return 0 })()
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := bench.CopyDB(path, dst); !errors.Is(err, bench.ErrCopyBusy) {
		t.Fatalf("err = %v, want ErrCopyBusy", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a copy was written from a DB that never held still")
	}
}

func TestCopyDB는_없는_원본이면_오류다(t *testing.T) {
	if err := bench.CopyDB(filepath.Join(t.TempDir(), "missing.db"), filepath.Join(t.TempDir(), "c.db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}

// The open WAL case does not catch a CopyDB that opens the source directly: the writing connection
// has already made the -wal and the -shm. The closed WAL case is the core defense of this
// regression: opening the source there would make them in the 0500 folder and fail.
func TestCopyDB는_읽기_전용_원본_폴더에서도_사본을_쓰고_폴더를_그대로_둔다(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a 0500 folder does not stop writes on Windows or for root (uid 0), so the test would pass without proving anything")
	}
	for _, tc := range walCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, id := walDB(t, tc.open)
			before := folderState(t, dir)
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(dir, 0o700) })
			dst := filepath.Join(t.TempDir(), "copy.db")
			if err := bench.CopyDB(path, dst); err != nil {
				t.Fatal(err)
			}
			hasMemory(t, dst, id)
			sameFolder(t, before, folderState(t, dir))
		})
	}
}

func TestCopyDB는_롤백_저널이_있는_원본도_사본으로_열고_원본을_바꾸지_않는다(t *testing.T) {
	dir, path, id := walDB(t, false)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[18], b[19] = 1, 1 // 롤백 모드 헤더
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-journal", make([]byte, 512), 0o600); err != nil {
		t.Fatal(err)
	}
	before := folderState(t, dir)
	// The journal is only 0 bytes of padding, which SQLite does not treat as a hot journal, so
	// look at the files of the private copy directly: the -journal must be copied with the DB.
	raw := filepath.Join(t.TempDir(), "raw.db")
	if err := bench.CopyFiles(path, raw); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(raw + "-journal"); err != nil || st.Size() != 512 {
		t.Errorf("the -journal was not copied next to the DB: %v %v", err, st)
	}
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := bench.CopyDB(path, dst); err != nil {
		t.Fatal(err)
	}
	hasMemory(t, dst, id)
	sameFolder(t, before, folderState(t, dir))
}

func TestCopyDB는_다시_뜨기_전에_시도마다_늘어나는_짧은_대기를_둔다(t *testing.T) {
	_, path, _ := walDB(t, true)
	w, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer bench.SetAfterCopy(func(try int) {
		if _, err := w.Save(memory.SaveInput{Kind: "fact", Title: fmt.Sprintf("쉬지 않는 쓰기 %d", try), Scope: "global", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "z"}}); err != nil {
			t.Error(err)
		}
	})()
	var waited []int
	defer bench.SetRetryWait(func(try int) time.Duration { waited = append(waited, try); return 0 })()
	if err := bench.CopyDB(path, filepath.Join(t.TempDir(), "copy.db")); !errors.Is(err, bench.ErrCopyBusy) {
		t.Fatalf("err = %v", err)
	}
	if fmt.Sprint(waited) != "[1 2 3 4]" {
		t.Errorf("waited after tries %v, want [1 2 3 4] (not after the last)", waited)
	}
	if bench.DefaultRetryWait(1) <= 0 || bench.DefaultRetryWait(2) <= bench.DefaultRetryWait(1) || bench.DefaultRetryWait(99) > 200*time.Millisecond {
		t.Errorf("default wait: %v %v %v", bench.DefaultRetryWait(1), bench.DefaultRetryWait(2), bench.DefaultRetryWait(99))
	}
}
