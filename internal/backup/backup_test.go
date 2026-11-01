package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// 보관 정리는 orbit-mem이 만든 하루 사본 이름 꼴(memory-YYYYMMDD.sqlite)만 지운다.
func TestPrune_이름_꼴에_맞는_하루_사본만_지운다(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	for d := 1; d <= 20; d++ {
		touch(t, filepath.Join(dir, fmt.Sprintf("memory-202610%02d.sqlite", d)), now.AddDate(0, 0, d-31))
	}
	keepOut := []string{
		"memory-keep.sqlite", "memory-20200101.sqlite.tmp", "memory-2020010.sqlite", "memory-202001011.sqlite",
		"before-0.3.0-memory.sqlite", "before-migrate-20261027T143544.sqlite", "before-schema-20261027T143544.sqlite",
		"work-20261001.sqlite", "notes.txt", "Memory-20200101.sqlite", "memory-20200101.sqlite-wal",
	}
	for _, n := range keepOut {
		touch(t, filepath.Join(dir, n), now.AddDate(-5, 0, 0)) // 가장 오래돼 보여도 지우면 안 된다
	}
	if err := os.Mkdir(filepath.Join(dir, "memory-20200102.sqlite"), 0o700); err != nil { // 이름이 맞아도 폴더는 건드리지 않는다
		t.Fatal(err)
	}
	removed, err := Prune(dir, PrefixMemory, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 6 {
		t.Fatalf("removed = %v", removed)
	}
	got := map[string]bool{}
	for _, n := range names(t, dir) {
		got[n] = true
	}
	for _, n := range append(keepOut, "memory-20200102.sqlite") {
		if !got[n] {
			t.Fatalf("%s was removed: %v", n, names(t, dir))
		}
	}
	for d := 1; d <= 20; d++ {
		n := fmt.Sprintf("memory-202610%02d.sqlite", d)
		if want := d > 6; got[n] != want {
			t.Fatalf("%s kept=%v want %v", n, got[n], want)
		}
	}
}

func TestPrune_keep이_1보다_작으면_지우지_않는다(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "memory-20261001.sqlite"), time.Now())
	if removed, err := Prune(dir, PrefixMemory, 0); err != nil || len(removed) != 0 {
		t.Fatalf("removed = %v %v", removed, err)
	}
}

func TestPrune_폴더가_없으면_조용히_넘어간다(t *testing.T) {
	if removed, err := Prune(filepath.Join(t.TempDir(), "none"), PrefixMemory, 14); err != nil || len(removed) != 0 {
		t.Fatalf("removed = %v %v", removed, err)
	}
}

// 24시간 규칙은 마지막 기억 하루 사본의 시각으로 정한다. 작업 기록 사본과 이름을 정한 사본은 세지 않는다.
func TestDue_마지막_기억_사본이_24시간보다_오래됐을_때만(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	if !Due(filepath.Join(t.TempDir(), "none"), now) {
		t.Fatal("no folder must be due")
	}
	dir := t.TempDir()
	if !Due(dir, now) {
		t.Fatal("empty folder must be due")
	}
	touch(t, filepath.Join(dir, "work-20261101.sqlite"), now.Add(-time.Hour))
	touch(t, filepath.Join(dir, "before-schema-20261101T090000.sqlite"), now.Add(-time.Hour))
	touch(t, filepath.Join(dir, "memory-keep.sqlite"), now.Add(-time.Hour))
	if !Due(dir, now) {
		t.Fatal("only work copies and snapshots must still be due")
	}
	touch(t, filepath.Join(dir, "memory-20261030.sqlite"), now.Add(-25*time.Hour))
	if !Due(dir, now) {
		t.Fatal("25h old copy must be due")
	}
	touch(t, filepath.Join(dir, "memory-20261031.sqlite"), now.Add(-23*time.Hour))
	if Due(dir, now) {
		t.Fatal("23h old copy must not be due")
	}
	last, ok := Last(dir, PrefixMemory)
	if !ok || last.Name != "memory-20261031.sqlite" || !last.At.Equal(now.Add(-23*time.Hour)) {
		t.Fatalf("last = %+v %v", last, ok)
	}
}

// 시계가 뒤로 간 PC에서 미래 시각의 사본이 영영 새 사본을 막지 않는다.
func TestDue_미래_시각의_사본은_24시간이_지나면_다시_만든다(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "memory-20261103.sqlite"), now.Add(48*time.Hour))
	if !Due(dir, now) {
		t.Fatal("a copy 48h in the future must not hold back a new one")
	}
}

func TestList_사본을_종류와_함께_최신순으로(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	touch(t, filepath.Join(dir, "memory-20261031.sqlite"), now.Add(-30*time.Hour))
	touch(t, filepath.Join(dir, "memory-20261101.sqlite"), now.Add(-time.Hour))
	touch(t, filepath.Join(dir, "work-20261101.sqlite"), now.Add(-2*time.Hour))
	touch(t, filepath.Join(dir, "before-restore-20261101T080000.sqlite"), now.Add(-3*time.Hour))
	touch(t, filepath.Join(dir, "before-0.3.0-memory.sqlite"), now.Add(-100*time.Hour))
	touch(t, filepath.Join(dir, "notes.txt"), now)
	touch(t, filepath.Join(dir, ".lock"), now)
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range list {
		got = append(got, c.Name+":"+c.Kind)
	}
	want := []string{"memory-20261101.sqlite:memory", "work-20261101.sqlite:work", "before-restore-20261101T080000.sqlite:snapshot",
		"memory-20261031.sqlite:memory", "before-0.3.0-memory.sqlite:snapshot"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("list\n got %v\nwant %v", got, want)
	}
	if list[0].Path != filepath.Join(dir, "memory-20261101.sqlite") || list[0].Size != 1 {
		t.Fatalf("first = %+v", list[0])
	}
}

func TestLock_한_번에_하나만_잡는다(t *testing.T) {
	dir := t.TempDir()
	unlock, ok, err := Lock(dir, 0)
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, ok2, err := Lock(dir, 0); err != nil || ok2 {
		t.Fatalf("second lock must fail at once: %v %v", ok2, err)
	}
	start := time.Now()
	if _, ok3, err := Lock(dir, 150*time.Millisecond); err != nil || ok3 || time.Since(start) < 100*time.Millisecond {
		t.Fatalf("waiting lock: %v %v after %v", ok3, err, time.Since(start))
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		unlock()
	}()
	unlock4, ok4, err := Lock(dir, 5*time.Second)
	if err != nil || !ok4 {
		t.Fatalf("lock after release: %v %v", ok4, err)
	}
	unlock4()
}

func TestLock_폴더가_없으면_만든다(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "backups")
	unlock, ok, err := Lock(dir, 0)
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	unlock()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("dir: %v", err)
	}
}
