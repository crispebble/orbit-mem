package work

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 다른 프로세스로 훅 사건을 쓰는 시험이 이 시험 바이너리를 다시 띄운다.
func TestMain(m *testing.M) {
	if db := os.Getenv("WORK_HELPER_DB"); db != "" {
		os.Exit(helperWrite(db))
	}
	os.Exit(m.Run())
}

// helperWrite writes WORK_HELPER_N task events for session WORK_HELPER_SESSION
// through Record, as a hook process would.
func helperWrite(db string) int {
	sess := os.Getenv("WORK_HELPER_SESSION")
	n, _ := strconv.Atoi(os.Getenv("WORK_HELPER_N"))
	spool := os.Getenv("WORK_HELPER_SPOOL")
	for _, e := range helperEvents(sess, n) {
		if _, err := Record(db, spool, e); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

func helperEvents(sess string, n int) []Event {
	var out []Event
	at := time.Date(2026, 10, 28, 5, 0, 0, 0, time.UTC).UnixMilli()
	for i := 1; i <= n; i++ {
		id := strconv.Itoa(i)
		at++
		out = append(out, NewEvent(KindTaskCreate, "claude", sess, at, TaskCreate{List: sess, ID: id, Subject: "단계 " + id}))
		at++
		out = append(out, NewEvent(KindTaskUpdate, "claude", sess, at, TaskUpdate{List: sess, ID: id, Status: str("in_progress")}))
		at++
		out = append(out, NewEvent(KindTaskUpdate, "claude", sess, at, TaskUpdate{List: sess, ID: id, Status: str("completed")}))
	}
	return out
}

func spawnWriter(t *testing.T, db, spool, sess string, n int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "WORK_HELPER_DB="+db, "WORK_HELPER_SESSION="+sess, "WORK_HELPER_N="+strconv.Itoa(n), "WORK_HELPER_SPOOL="+spool)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// graphOf strips run-specific values so two graphs can be compared.
func graphOf(t *testing.T, s *Store, host, session string) string {
	t.Helper()
	var ids []string
	rows, _ := s.db.Query(`SELECT id FROM works WHERE host=? AND session=? ORDER BY opened_at, id`, host, session)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []string
	for _, id := range ids {
		w, _, err := s.GetWork(id, false, ReadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, st := range w.Steps {
			var after []string
			for _, a := range st.After {
				after = append(after, a.Source)
			}
			parts = append(parts, fmt.Sprintf("%s:%s:%d:%v", st.Title, st.Status, st.Wave, after))
		}
		out = append(out, w.State+"["+strings.Join(parts, ",")+"]")
	}
	return strings.Join(out, " ")
}

// 끝 기준: 두 세션이 별도 프로세스로 사건을 연달아 쓰면 잃은 사건 0건, 두 그래프가 순차 적용 결과와 같다.
func TestConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "work.db")
	spool := filepath.Join(dir, "spool")
	if s, err := Open(db); err == nil {
		s.Close()
	}
	const n = 30
	a := spawnWriter(t, db, spool, "SA", n)
	b := spawnWriter(t, db, spool, "SB", n)
	if err := a.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := b.Wait(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Spool = spool
	if _, err := s.Drain(); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ledger WHERE derived=0`).Scan(&count)
	if count != 2*3*n {
		t.Errorf("원장 사건 %d건, %d건이어야 합니다", count, 2*3*n)
	}
	ref, err := Open(filepath.Join(dir, "ref.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Close()
	for _, sess := range []string{"SA", "SB"} {
		for _, e := range helperEvents(sess, n) {
			if _, err := ref.Append(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, sess := range []string{"SA", "SB"} {
		if got, want := graphOf(t, s, "claude", sess), graphOf(t, ref, "claude", sess); got != want {
			t.Errorf("%s 그래프가 순차 적용과 다릅니다\n%s\n%s", sess, got, want)
		}
	}
}

// 끝 기준: DB를 잠근 상태에서 들어온 훅 사건은 스풀에 남고, 잠금을 푼 뒤 첫 조회에서 그래프에 반영된다.
func TestLockedDatabaseSpools(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "work.db")
	spool := filepath.Join(dir, "spool")
	holder, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := holder.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	e := NewEvent(KindTaskCreate, "claude", "S1", 1, TaskCreate{List: "S1", ID: "1", Subject: "잠금 중 만든 단계"})
	if _, err := Record(db, spool, e); err != nil {
		t.Fatalf("잠금 중 기록: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(spool, "*.json"))
	if len(files) != 1 {
		t.Fatalf("스풀 파일 %d개", len(files))
	}
	_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
	conn.Close()
	holder.Close()

	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Spool = spool
	if n, err := s.Drain(); err != nil || n != 1 {
		t.Fatalf("비우기 %d %v", n, err)
	}
	page, _ := s.ListWorks(Filter{}, ReadOptions{})
	if len(page.Items) != 1 || page.Items[0].Title != "잠금 중 만든 단계" {
		t.Errorf("스풀 사건이 반영되지 않았습니다: %+v", page.Items)
	}
	if files, _ := filepath.Glob(filepath.Join(spool, "*")); len(files) != 0 {
		t.Errorf("스풀이 비지 않았습니다: %v", files)
	}
}

// 끝 기준: work_list를 작은 limit으로 넘기는 도중 다른 작업이 갱신돼도 빠지거나 두 번 나오는 작업이 없고,
// 갱신된 작업은 첫 쪽 seq의 work_changes로 받는다. cwdPrefix는 경로 구분자 단위로 거른다.
func TestWorkListPaging(t *testing.T) {
	w := newTW(t)
	for i := 0; i < 7; i++ {
		sess := fmt.Sprintf("S%d", i)
		e := NewEvent(KindTaskCreate, "claude", sess, int64(1000+i), TaskCreate{List: sess, ID: "1", Subject: "단계"})
		e.Cwd = []string{"/a/b", "/a/bc", "/a/b/c"}[i%3]
		if _, err := w.s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	first, err := w.s.ListWorks(Filter{Limit: 3}, ReadOptions{})
	if err != nil || len(first.Items) != 3 || first.NextCursor == "" {
		t.Fatalf("첫 쪽: %d %v", len(first.Items), err)
	}
	seen := map[string]int{}
	for _, it := range first.Items {
		seen[it.ID]++
	}
	// 넘기는 동안 두 작업이 바뀐다: 이미 본 것 하나, 아직 안 본 것 하나
	var unseen string
	all, _ := w.s.ListWorks(Filter{Limit: 50}, ReadOptions{})
	for _, it := range all.Items {
		if seen[it.ID] == 0 {
			unseen = it.ID
		}
	}
	for _, id := range []string{first.Items[0].ID, unseen} {
		var sess string
		_ = w.s.db.QueryRow(`SELECT session FROM works WHERE id=?`, id).Scan(&sess)
		w.ev(KindTaskUpdate, "claude", sess, TaskUpdate{List: sess, ID: "1", Status: str("in_progress")})
	}
	cursor := first.NextCursor
	for cursor != "" {
		p, err := w.s.ListWorks(Filter{Limit: 3, Cursor: cursor}, ReadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Seq != first.Seq {
			t.Errorf("뒤 쪽 seq가 첫 쪽과 다릅니다")
		}
		for _, it := range p.Items {
			seen[it.ID]++
		}
		cursor = p.NextCursor
	}
	ch, err := w.s.Changes(context.Background(), w.s.EncodeSeq(first.Seq), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ch.WorkIDs {
		seen[id]++
	}
	for _, it := range all.Items {
		if seen[it.ID] == 0 {
			t.Errorf("빠진 작업 %s", it.ID)
		}
	}
	for id, n := range seen {
		if n > 1 && id != first.Items[0].ID {
			t.Errorf("쪽에서 두 번 나온 작업 %s (%d)", id, n)
		}
	}
	if !contains(ch.WorkIDs, unseen) || !contains(ch.WorkIDs, first.Items[0].ID) {
		t.Errorf("work_changes가 바뀐 작업을 주지 않았습니다: %v", ch.WorkIDs)
	}
	p, _ := w.s.ListWorks(Filter{CwdPrefix: []string{"/a/b"}, Limit: 50}, ReadOptions{})
	for _, it := range p.Items {
		if it.Cwd == "/a/bc" {
			t.Error("/a/b가 /a/bc에 맞았습니다")
		}
	}
	if len(p.Items) != 5 {
		t.Errorf("/a/b 아래 작업 %d개", len(p.Items))
	}
	if _, err := w.s.ListWorks(Filter{Cursor: "망가진"}, ReadOptions{}); err != ErrBadCursor {
		t.Errorf("모양이 틀린 커서: %v", err)
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// 끝 기준: work_sessions를 작은 limit으로 부르면 nextCursor를 따라 모든 세션과 한 세션의 모든 사건을 빠짐없이, 겹침 없이 읽는다.
func TestSessionPaging(t *testing.T) {
	w := newTW(t)
	for i := 0; i < 5; i++ {
		w.start("claude", fmt.Sprintf("S%d", i), SessionStart{Surface: "cli"})
	}
	w.start("codex", "C0", SessionStart{Surface: "cli"})
	var got []string
	cursor := ""
	for {
		p, err := w.s.ListSessions(Filter{Limit: 2, Cursor: cursor}, ReadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range p.Sessions {
			got = append(got, s.Host+"/"+s.SessionID)
		}
		if cursor = p.NextCursor; cursor == "" {
			break
		}
	}
	sort.Strings(got)
	eq(t, "세션", got, []string{"claude/S0", "claude/S1", "claude/S2", "claude/S3", "claude/S4", "codex/C0"})
	// 상태로 거르기
	w.claude(KindTurnStart, Turn{Prompt: "p"})
	p, _ := w.s.ListSessions(Filter{States: []string{"running"}}, ReadOptions{})
	if len(p.Sessions) != 1 || p.Sessions[0].SessionID != "S1" {
		t.Errorf("상태 거르기: %+v", p.Sessions)
	}
	// 한 세션의 사건
	activeTask(w, "1", "2", "3")
	w.claude(KindTurnEnd, Turn{Prompt: "p"})
	full, _ := w.s.SessionEvents("S1", "", 200, ReadOptions{})
	var seqs []string
	cursor = ""
	first := true
	for {
		d, err := w.s.SessionEvents("S1", cursor, 2, ReadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if first != (d.Runs != nil) {
			t.Errorf("runs·evidence는 첫 쪽에만 있어야 합니다")
		}
		first = false
		for _, e := range d.Events {
			seqs = append(seqs, e.Seq)
		}
		if cursor = d.NextCursor; cursor == "" {
			break
		}
	}
	var want []string
	for _, e := range full.Events {
		want = append(want, e.Seq)
	}
	eq(t, "사건", seqs, want)
	if len(want) < 6 {
		t.Errorf("사건이 너무 적습니다: %d", len(want))
	}
}

// 끝 기준: 다른 프로세스가 사건을 쓰면 대기 시간이 끝나기 전에 바뀐 작업 id를 준다.
// 바뀐 것이 없으면 대기 시간 뒤 빈 결과. 백업에서 되살린 DB의 옛 seq, 모양이 틀린 커서는 reset.
// waitMax보다 큰 wait는 waitMax 안에 답하고, wait를 넘기지 않으면 바로 답한다.
func TestChangesWait(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "work.db")
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seq, _ := s.LastSeq()
	after := s.EncodeSeq(seq)

	start := time.Now()
	c, err := s.Changes(context.Background(), after, 0)
	if err != nil || c.Reset || len(c.WorkIDs) != 0 || time.Since(start) > time.Second {
		t.Fatalf("wait 없음: %+v %v %v", c, err, time.Since(start))
	}
	if c.Seq != seq {
		t.Errorf("빈 결과의 seq가 바뀌었습니다")
	}
	start = time.Now()
	c, _ = s.Changes(context.Background(), after, 300*time.Millisecond)
	if el := time.Since(start); len(c.WorkIDs) != 0 || el < 300*time.Millisecond || el > 2*time.Second {
		t.Errorf("빈 대기: %+v %v", c, el)
	}

	done := make(chan Changes, 1)
	go func() {
		c, _ := s.Changes(context.Background(), after, 10*time.Second)
		done <- c
	}()
	time.Sleep(200 * time.Millisecond)
	start = time.Now()
	w := spawnWriter(t, db, "", "SX", 1)
	if err := w.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-done:
		if len(c.WorkIDs) != 1 || len(c.Sessions) != 1 || c.Sessions[0] != "SX" || c.Reset {
			t.Errorf("바뀐 것: %+v", c)
		}
		if time.Since(start) > 5*time.Second {
			t.Errorf("늦게 알렸습니다: %v", time.Since(start))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("다른 프로세스의 쓰기를 알아채지 못했습니다")
	}

	for _, bad := range []string{"", "망가진", s.EncodeSeq(1 << 40), encodeSeq("deadbeef", 1)} {
		c, _ := s.Changes(context.Background(), bad, time.Second)
		if !c.Reset || c.WorkIDs != nil {
			t.Errorf("%q: reset이어야 합니다: %+v", bad, c)
		}
	}

	// 백업에서 되살림: 백업 사본은 새 정체를 받으므로 옛 커서가 이어지지 않는다
	last, _ := s.LastSeq()
	old := s.EncodeSeq(last)
	backup := filepath.Join(dir, "backup.db")
	if err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	r, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.ID() == s.ID() {
		t.Error("백업 사본이 같은 정체입니다")
	}
	if c, _ := r.Changes(context.Background(), old, 0); !c.Reset {
		t.Errorf("되살린 DB의 옛 seq: %+v", c)
	}
	// wait가 상한보다 크면 상한까지만
	if WaitMax > time.Minute {
		t.Errorf("WaitMax %v는 Studio callTimeoutMs(60초)보다 짧아야 합니다", WaitMax)
	}
}

// 끝 기준: 작업 기록 패키지가 네트워크 패키지를 쓰지 않는다. 이 모듈의 패키지가 직접 가져오는 것을 본다.
// SQLite 드라이버(modernc.org/sqlite)가 net/url·uuid를 통해 net을 끌어오지만 연결을 열지 않는다.
func TestNoNetworkDependency(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", ".").Output()
	if err != nil {
		t.Skip("go list를 쓸 수 없어 의존 시험을 건너뜁니다: " + err.Error())
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !strings.HasPrefix(f[0], "github.com/onidot-labs/orbit/") {
			continue
		}
		for _, imp := range f[1:] {
			if imp == "net" || strings.HasPrefix(imp, "net/") || strings.Contains(imp, "modelcontextprotocol") {
				t.Errorf("%s가 네트워크 패키지 %s를 가져옵니다", f[0], imp)
			}
		}
	}
}

func TestRebuildKeepsGraph(t *testing.T) {
	w := newTW(t)
	w.start("claude", "S1", SessionStart{Surface: "cli"})
	activeTask(w, "1", "2")
	w.claude(KindTaskUpdate, TaskUpdate{List: "S1", ID: "2", AddBlockedBy: []string{"1"}})
	w.claude(KindRunStart, RunStart{Agent: "a1", Type: "orbit:worker"})
	w.claude(KindCommand, Command{Text: "go test", OK: true, Agent: "a1"})
	w.claude(KindWaitOpen, Wait{Key: "k", Tool: "Bash"})
	w.claude(KindSessionEnd, SessionEnd{})
	snap := func() string {
		page, _ := w.s.ListWorks(Filter{}, ReadOptions{})
		d, _ := w.s.SessionEvents("S1", "", 200, ReadOptions{})
		var docs []any
		for _, it := range page.Items {
			doc, _, _ := w.s.GetWork(it.ID, false, ReadOptions{})
			docs = append(docs, doc)
		}
		b, _ := json.Marshal([]any{page, docs, d})
		return string(b)
	}
	before := snap()
	if err := w.s.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if after := snap(); after != before {
		t.Errorf("다시 만든 그래프가 다릅니다\n%s\n%s", before, after)
	}
}

// 설계 「저장」: 일일 백업은 기억 DB와 작업 기록 DB를 함께 복사한다.
func TestDailyBackupRotates(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "work.db")
	if p, err := DailyBackup(db, filepath.Join(dir, "backups"), 2, time.Now()); err != nil || p != "" {
		t.Fatalf("DB가 없으면 건너뜁니다: %q %v", p, err)
	}
	s, _ := Open(db)
	s.Close()
	for i := 0; i < 3; i++ {
		p, err := DailyBackup(db, filepath.Join(dir, "backups"), 2, time.Date(2026, 10, 26+i, 4, 0, 0, 0, time.Local))
		if err != nil || p == "" {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "backups", "work-*.sqlite"))
	if len(files) != 2 || !strings.HasSuffix(files[1], "work-20261028.sqlite") {
		t.Errorf("남은 백업: %v", files)
	}
}

func execGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return string(out), err
}
