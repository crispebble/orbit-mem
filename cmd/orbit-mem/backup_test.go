package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/backup"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/sqlitedb"
)

// fakeBackground records the background runs a test asks for instead of starting a process.
func fakeBackground(t *testing.T, fail error) *[][]string {
	t.Helper()
	var mu sync.Mutex
	calls := &[][]string{}
	old := startBackground
	startBackground = func(args ...string) error {
		if len(args) > 0 && args[0] == "update" { // 새 판 확인 갱신은 사본과 상관없다
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, args)
		return fail
	}
	t.Cleanup(func() { startBackground = old })
	return calls
}

func dailyCopy(t *testing.T, h string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(h, "backups")
	os.MkdirAll(dir, 0o700)
	at := time.Now().Add(-age)
	p := filepath.Join(dir, "memory-"+at.Format("20060102")+".sqlite")
	os.WriteFile(p, []byte("x"), 0o600)
	os.Chtimes(p, at, at)
	return p
}

// seedRule makes the session-start hook give a context (an empty DB gives none).
func seedRule(t *testing.T) *memory.Store {
	t.Helper()
	s := seed(t)
	pinSave(t, s, memory.SaveInput{Kind: "rule", Title: "답은 존댓말", Body: "반말 금지", Source: memory.Source{Kind: "quote", Excerpt: "존댓말로"}})
	return s
}

const startInput = `{"cwd":"/tmp","hook_event_name":"SessionStart","source":"startup","session_id":"s-1"}`

func hookContext(t *testing.T, out string) string {
	t.Helper()
	var v struct {
		HookSpecificOutput struct{ HookEventName, AdditionalContext string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("hook output = %q %v", out, err)
	}
	return v.HookSpecificOutput.AdditionalContext
}

// 끝난 기준: 세션 시작 훅은 마지막 사본이 24시간보다 오래됐으면 사본을 뒤에서(별도 프로세스) 만들게 하고, 문맥은 그대로 낸다.
func TestHook_세션_시작은_마지막_사본이_24시간보다_오래됐으면_뒤에서_만든다(t *testing.T) {
	h := home(t)
	seedRule(t)
	calls := fakeBackground(t, nil)
	dailyCopy(t, h, 25*time.Hour)
	out, _, code := run(t, startInput, "hook", "session-start", "--host", "claude")
	if code != 0 || !strings.Contains(hookContext(t, out), "답은 존댓말") {
		t.Fatalf("code %d out %q", code, out)
	}
	if fmt.Sprint(*calls) != "[[backup auto]]" {
		t.Fatalf("background = %v", *calls)
	}
}

func TestHook_세션_시작은_사본이_없어도_뒤에서_만든다(t *testing.T) {
	home(t)
	seed(t)
	calls := fakeBackground(t, nil)
	if _, _, code := run(t, startInput, "hook", "session-start", "--host", "codex"); code != 0 {
		t.Fatal(code)
	}
	if fmt.Sprint(*calls) != "[[backup auto]]" {
		t.Fatalf("background = %v", *calls)
	}
}

func TestHook_24시간_안의_사본이_있으면_아무것도_띄우지_않는다(t *testing.T) {
	h := home(t)
	seed(t)
	calls := fakeBackground(t, nil)
	dailyCopy(t, h, 23*time.Hour)
	if _, _, code := run(t, startInput, "hook", "session-start", "--host", "claude"); code != 0 {
		t.Fatal(code)
	}
	if len(*calls) != 0 {
		t.Fatalf("background = %v", *calls)
	}
}

func TestHook_요청과_세션_끝_훅은_사본을_만들지_않는다(t *testing.T) {
	home(t)
	seed(t)
	calls := fakeBackground(t, nil)
	run(t, `{"cwd":"/tmp","prompt":"안녕","session_id":"s-1"}`, "hook", "prompt", "--host", "claude")
	run(t, `{"cwd":"/tmp","session_id":"s-1"}`, "hook", "session-end", "--host", "claude")
	if len(*calls) != 0 {
		t.Fatalf("background = %v", *calls)
	}
}

// 끝난 기준: 설정 키(autoBackup)로 끌 수 있다. 기본은 켜짐.
func TestHook_autoBackup을_끄면_띄우지_않는다(t *testing.T) {
	home(t)
	seed(t)
	calls := fakeBackground(t, nil)
	if _, errOut, code := run(t, "", "config", "set", "autoBackup", "false"); code != 0 {
		t.Fatalf("config set: %d %s", code, errOut)
	}
	run(t, startInput, "hook", "session-start", "--host", "claude")
	if len(*calls) != 0 {
		t.Fatalf("background = %v", *calls)
	}
	if _, _, code := run(t, "", "config", "set", "autoBackup", "true"); code != 0 {
		t.Fatal(code)
	}
	run(t, startInput, "hook", "session-start", "--host", "claude")
	if len(*calls) != 1 {
		t.Fatalf("background after on = %v", *calls)
	}
}

// 끝난 기준: 실패해도 훅은 종료 0이고 문맥을 낸다.
func TestHook_뒤에서_띄우기가_실패해도_종료_0이고_문맥을_낸다(t *testing.T) {
	home(t)
	seedRule(t)
	fakeBackground(t, errors.New("fork failed"))
	out, _, code := run(t, startInput, "hook", "session-start", "--host", "claude")
	if code != 0 {
		t.Fatal(code)
	}
	hookContext(t, out)
}

func TestHook_백업_폴더_자리에_파일이_있어도_종료_0(t *testing.T) {
	h := home(t)
	seedRule(t)
	fakeBackground(t, nil)
	os.WriteFile(filepath.Join(h, "backups"), []byte("x"), 0o600)
	out, _, code := run(t, startInput, "hook", "session-start", "--host", "claude")
	if code != 0 {
		t.Fatal(code)
	}
	hookContext(t, out)
}

// backup auto는 뒤에서 도는 쪽: 24시간 안이면 아무것도 하지 않고, 오래됐으면 만들고 보관 개수를 지킨다.
func TestBackupAuto_24시간_규칙을_따른다(t *testing.T) {
	h := home(t)
	seed(t)
	fresh := dailyCopy(t, h, time.Hour)
	if _, errOut, code := run(t, "", "backup", "auto"); code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if l, _ := backup.List(filepath.Join(h, "backups")); len(l) != 1 {
		t.Fatalf("copies = %+v", l)
	}
	os.Remove(fresh)
	dailyCopy(t, h, 30*time.Hour)
	if _, errOut, code := run(t, "", "backup", "auto"); code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	last, ok := backup.Last(filepath.Join(h, "backups"), backup.PrefixMemory)
	if !ok || time.Since(last.At) > time.Minute {
		t.Fatalf("last = %+v", last)
	}
	if err := memory.CheckCopy(last.Path); err != nil {
		t.Fatal(err)
	}
}

func TestBackupAuto_설치_전이면_DB를_만들지_않는다(t *testing.T) {
	h := home(t)
	if _, _, code := run(t, "", "backup", "auto"); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(h, "memory.db")); !os.IsNotExist(err) {
		t.Fatalf("memory.db made: %v", err)
	}
}

func TestBackupAuto_실패는_logs에_남기고_종료_0(t *testing.T) {
	h := home(t)
	seed(t)
	os.WriteFile(filepath.Join(h, "backups"), []byte("x"), 0o600)
	if _, _, code := run(t, "", "backup", "auto"); code != 0 {
		t.Fatal(code)
	}
	b, err := os.ReadFile(filepath.Join(h, "logs", "backup.log"))
	if err != nil || len(b) == 0 {
		t.Fatalf("log: %v %q", err, b)
	}
}

// 끝난 기준: orbit-mem backup 하나로 만들기·목록·상태·복구.
func TestBackup_만들기는_24시간_안이어도_만든다(t *testing.T) {
	h := home(t)
	seed(t)
	old := dailyCopy(t, h, time.Hour)
	out, errOut, code := run(t, "", "backup")
	if code != 0 || !strings.Contains(out, old) {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if err := memory.CheckCopy(old); err != nil {
		t.Fatalf("today's copy must be rewritten as a real DB: %v", err)
	}
}

func TestBackup_목록과_상태(t *testing.T) {
	h := home(t)
	seed(t)
	dailyCopy(t, h, 26*time.Hour)
	if _, _, code := run(t, "", "backup"); code != 0 {
		t.Fatal(code)
	}
	os.WriteFile(filepath.Join(h, "backups", "notes.txt"), []byte("x"), 0o600)
	out, errOut, code := run(t, "", "backup", "list", "--json")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	var list struct {
		Dir    string
		Copies []struct {
			Name, Kind, Path, At string
			Size                 int64
		}
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || list.Dir != filepath.Join(h, "backups") || len(list.Copies) != 2 ||
		list.Copies[0].Kind != "memory" || list.Copies[0].At == "" {
		t.Fatalf("list = %s %v", out, err)
	}
	out, errOut, code = run(t, "", "backup", "status", "--json")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	var st struct {
		Dir        string
		Auto       bool
		Keep       int
		Count      int
		Last       string
		LastAt     string
		Due        bool
		NextAfter  string
		Interval   string
		LastPath   string
		Snapshots  int
		WorkCopies int
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || !st.Auto || st.Keep != 14 || st.Count != 2 || st.Due || st.LastAt == "" || st.NextAfter == "" ||
		!strings.HasPrefix(st.Last, "memory-") {
		t.Fatalf("status = %s %v", out, err)
	}
	// 사람이 읽는 출력
	onTerminal(t)
	out, _, code = run(t, "", "backup", "status")
	if code != 0 || !strings.Contains(out, st.Last) || !strings.Contains(out, "14") {
		t.Fatalf("status text = %q", out)
	}
	out, _, code = run(t, "", "backup", "list")
	if code != 0 || !strings.Contains(out, st.Last) || strings.Contains(out, "notes.txt") {
		t.Fatalf("list text = %q", out)
	}
}

func liveTitles(t *testing.T, h string) string {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(filepath.Join(h, "memory.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT title FROM memories ORDER BY title`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

// 끝난 기준: 복구는 무엇을 바꾸는지 먼저 보이고, 바꾸기 전 DB를 따로 남기고, 무결성을 검사한 뒤 바꾼다.
func TestBackup_복구(t *testing.T) {
	h := home(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "fact", Title: "a", Source: memory.Source{Kind: "quote", Excerpt: "a"}})
	out, _, code := run(t, "", "backup")
	if code != 0 {
		t.Fatal(code)
	}
	cp, _ := backup.Last(filepath.Join(h, "backups"), backup.PrefixMemory)
	s.Save(memory.SaveInput{Kind: "fact", Title: "b", Source: memory.Source{Kind: "quote", Excerpt: "b"}})

	// --yes 없이(터미널 아님): 무엇을 바꾸는지만 보이고 바꾸지 않는다.
	out, errOut, code := run(t, "", "backup", "restore", cp.Name)
	if code == 0 || !strings.Contains(out, cp.Path) || !strings.Contains(out, filepath.Join(h, "memory.db")) || !strings.Contains(out+errOut, "--yes") {
		t.Fatalf("without --yes: %d %q %q", code, out, errOut)
	}
	if got := liveTitles(t, h); got != "a,b" {
		t.Fatalf("changed without --yes: %q", got)
	}
	// --yes: 먼저 보이고 바꾼다.
	out, errOut, code = run(t, "", "backup", "restore", cp.Name, "--yes")
	if code != 0 || !strings.Contains(out, cp.Path) {
		t.Fatalf("restore: %d %q %q", code, out, errOut)
	}
	if got := liveTitles(t, h); got != "a" {
		t.Fatalf("restored = %q", got)
	}
	var before string
	l, _ := backup.List(filepath.Join(h, "backups"))
	for _, c := range l {
		if strings.HasPrefix(c.Name, "before-restore-") {
			before = c.Path
		}
	}
	if before == "" || !strings.Contains(out, before) {
		t.Fatalf("before-restore copy %q not shown in %q", before, out)
	}
	if info, err := memory.ReadCopyInfo(before); err != nil || info.Memories != 2 {
		t.Fatalf("before-restore = %+v %v", info, err)
	}
}

func TestBackup_복구는_깨진_사본과_없는_사본을_받지_않는다(t *testing.T) {
	h := home(t)
	s := seed(t)
	s.Save(memory.SaveInput{Kind: "fact", Title: "a", Source: memory.Source{Kind: "quote", Excerpt: "a"}})
	os.MkdirAll(filepath.Join(h, "backups"), 0o700)
	bad := filepath.Join(h, "backups", "memory-20200101.sqlite")
	os.WriteFile(bad, []byte("SQLite format 3\x00 깨진 파일"), 0o600)
	for _, name := range []string{"memory-20200101.sqlite", "none.sqlite"} {
		if _, _, code := run(t, "", "backup", "restore", name, "--yes"); code == 0 {
			t.Fatalf("%s accepted", name)
		}
	}
	if got := liveTitles(t, h); got != "a" {
		t.Fatalf("live = %q", got)
	}
	if _, _, code := run(t, "", "backup", "restore"); code != 2 {
		t.Fatalf("no name: %d", code)
	}
}

// 끝난 기준: orb retro 앞의 백업(retro-store backup)도 24시간 안이면 건너뛴다. 결과 꼴(errors·memory·work)은 그대로다.
func TestRetroStore_backup은_24시간_안이면_새로_만들지_않는다(t *testing.T) {
	h, _ := storeHome(t)
	fresh := dailyCopy(t, h, time.Hour)
	b := storeJSON(t, "", "backup")
	if keys(b) != "errors,memory,work" || b["memory"] != fresh {
		t.Fatalf("backup = %v", b)
	}
	if st, _ := os.Stat(fresh); st.Size() != 1 {
		t.Fatal("the fresh copy was rewritten")
	}
	os.Remove(fresh)
	dailyCopy(t, h, 25*time.Hour)
	b = storeJSON(t, "", "backup")
	if err := memory.CheckCopy(b["memory"].(string)); err != nil {
		t.Fatalf("after 24h: %v (%v)", err, b)
	}
}

// 끝난 기준: 들여오기·옮기기 전에는 하루 한 번 규칙과 관계없이 사본을 만든다.
func TestImport와_Migrate는_하루_사본이_있어도_사본을_만든다(t *testing.T) {
	h := home(t)
	seed(t)
	dailyCopy(t, h, time.Hour)
	f := filepath.Join(t.TempDir(), "in.jsonl")
	os.WriteFile(f, []byte(""), 0o600)
	if _, errOut, code := run(t, "", "import", f); code != 0 {
		t.Fatalf("import: %d %s", code, errOut)
	}
	old := filepath.Join(t.TempDir(), "knowledge.sqlite")
	db, _ := sql.Open("sqlite", old)
	db.Exec(`CREATE TABLE t(x)`)
	db.Close()
	run(t, "", "migrate", "--from", old) // 옛 Knowledge 꼴이 아니면 옮기기 자체는 실패해도 된다
	var imp, mig bool
	l, _ := backup.List(filepath.Join(h, "backups"))
	for _, c := range l {
		imp = imp || strings.HasPrefix(c.Name, "before-import-")
		mig = mig || strings.HasPrefix(c.Name, "before-migrate-")
	}
	if !imp || !mig {
		t.Fatalf("copies = %+v", l)
	}
}

// 끝난 기준: doctor는 마지막 사본 시각과 개수를 보인다.
func TestDoctor_마지막_사본_시각과_개수(t *testing.T) {
	h := home(t)
	seed(t)
	newFakePC(t, "claude")
	t.Setenv("ORBIT_HOME", h)
	dailyCopy(t, h, 30*time.Hour)
	dailyCopy(t, h, 3*time.Hour)
	os.WriteFile(filepath.Join(h, "backups", "work-20990101.sqlite"), []byte("x"), 0o600) // 이름순 마지막이어도 기억 사본이 아니다
	out, errOut, code := run(t, "", "doctor", "--json")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	var rep struct {
		LastBackup string
		Backups    struct {
			Last, LastAt string
			Count, Keep  int
			Auto         bool
		}
	}
	json.Unmarshal([]byte(out), &rep)
	if !strings.HasPrefix(filepath.Base(rep.LastBackup), "memory-") || rep.Backups.Count != 2 || rep.Backups.Keep != 14 || !rep.Backups.Auto || rep.Backups.LastAt == "" {
		t.Fatalf("doctor = %s", out)
	}
	onTerminal(t)
	out, _, _ = run(t, "", "doctor")
	if !strings.Contains(out, filepath.Base(rep.LastBackup)) || !strings.Contains(out, "2") {
		t.Fatalf("doctor text = %q", out)
	}
}

// 실제 실행 파일로: 훅은 사본을 기다리지 않고 끝나고, 뒤에서 사본이 생긴다. 여러 프로세스가 함께 돌아도 사본은 하나다.
func TestHook_실제_실행_파일로_뒤에서_사본이_생긴다(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	h := home(t)
	s := seedRule(t)
	for i := 0; i < 29; i++ {
		s.Save(memory.SaveInput{Kind: "fact", Title: fmt.Sprintf("기억 %d", i), Source: memory.Source{Kind: "quote", Excerpt: "x"}})
	}
	bin := filepath.Join(t.TempDir(), "orbit-mem")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	env := append(os.Environ(), "ORBIT_HOME="+h, "ORBIT_LANG=")
	hook := exec.Command(bin, "hook", "session-start", "--host", "claude")
	hook.Env = env
	hook.Stdin = strings.NewReader(startInput)
	out, err := hook.Output()
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	hookContext(t, string(out))
	dir := filepath.Join(h, "backups")
	deadline := time.Now().Add(15 * time.Second)
	var last backup.Copy
	for {
		if l, ok := backup.Last(dir, backup.PrefixMemory); ok {
			if memory.CheckCopy(l.Path) == nil {
				last = l
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no copy appeared in the background")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if info, err := memory.ReadCopyInfo(last.Path); err != nil || info.Memories != 30 {
		t.Fatalf("copy = %+v %v", info, err)
	}
	// 오래된 사본으로 되돌려 놓고 backup auto 프로세스 여섯을 함께 띄운다.
	os.Remove(last.Path)
	dailyCopy(t, h, 48*time.Hour)
	var wg sync.WaitGroup
	codes := make([]error, 6)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := exec.Command(bin, "backup", "auto")
			c.Env = env
			codes[i] = c.Run()
		}(i)
	}
	wg.Wait()
	for i, err := range codes {
		if err != nil {
			t.Fatalf("backup auto %d: %v", i, err)
		}
	}
	l, _ := backup.List(dir)
	var fresh []string
	for _, c := range l {
		if c.Kind == backup.KindMemory && time.Since(c.At) < time.Minute {
			fresh = append(fresh, c.Name)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("fresh copies = %v (all %+v)", fresh, l)
	}
	if b, _ := os.ReadFile(filepath.Join(h, "logs", "backup.log")); strings.Contains(string(b), "error") || strings.Contains(string(b), "실패") {
		t.Fatalf("backup.log = %s", b)
	}
}
