package memory

import (
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// 다시 만든 묶음은 그 묶음을 읽은 뒤 아무도 고치지 않았을 때만 들어간다: 뺀 기억을 저장된 묶음이 아직 갖고 있어야 한다.
// 시작 시각은 그대로이고, 뺀 기억은 MarkBundle로 남는다.
func TestReplaceSessionBundle_옛_묶음이_이미_바뀌었으면_고치지_않는다(t *testing.T) {
	s, c := newTestStore(t)
	if err := s.SaveSessionBundle("h", "s1", "첫 묶음", []string{"m-1", "m-2"}); err != nil {
		t.Fatal(err)
	}
	at := s.SessionBundleAt("h", "s1")
	c.t = c.t.Add(time.Hour)
	if err := s.ReplaceSessionBundle("h", "s1", "다시 만든 묶음", []string{"m-2"}, []string{"m-1"}); err != nil {
		t.Fatal(err)
	}
	if text, ids, _ := s.SessionBundle("h", "s1"); text != "다시 만든 묶음" || !slices.Equal(ids, []string{"m-2"}) || s.SessionBundleAt("h", "s1") != at {
		t.Fatalf("다시 만들기: %q %v at %d (처음 %d)", text, ids, s.SessionBundleAt("h", "s1"), at)
	}
	if marks := s.SessionMarks("h", "s1", MarkBundle); len(marks) != 1 || !marks["m-1"] {
		t.Fatalf("뺀 기억 표시 %v", marks)
	}
	// 같은 옛 묶음을 읽고 늦게 온 다시 만들기: 저장된 묶음은 이미 m-1이 없으니 고치지 않는다.
	if err := s.ReplaceSessionBundle("h", "s1", "늦게 온 묶음", []string{"m-2", "m-3"}, []string{"m-1"}); err != nil {
		t.Fatal(err)
	}
	if text, _, _ := s.SessionBundle("h", "s1"); text != "다시 만든 묶음" {
		t.Fatalf("늦게 온 다시 만들기가 덮어썼습니다: %q", text)
	}
	// 압축·clear로 묶음이 지워진 세션은 다시 만들기가 묶음도 표시도 만들지 않는다.
	if err := s.ResetSession("h", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSessionBundle("h", "s1", "지운 뒤 묶음", nil, []string{"m-2"}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.SessionBundle("h", "s1"); ok || len(s.SessionMarks("h", "s1", MarkBundle)) != 0 {
		t.Fatal("지운 세션에 묶음이나 표시가 생겼습니다")
	}
}

// 여러 연결이 같은 옛 묶음에서 다시 만든 묶음을 한꺼번에 넣는다. 하나만 들어가고 나머지는 아무것도 바꾸지 않는다.
func TestReplaceSessionBundle_동시에_고쳐도_한쪽만_적용된다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	var stores []*Store
	for range 4 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		stores = append(stores, s)
	}
	// 시험 DB에만 두는 기록 표: 묶음 줄이 실제로 고쳐진 횟수를 센다.
	if _, err := stores[0].db.Exec(`CREATE TABLE test_bundle_writes(session TEXT, text TEXT);
		CREATE TRIGGER test_bundle_written AFTER UPDATE ON session_bundles BEGIN
		  INSERT INTO test_bundle_writes VALUES(new.session, new.text); END;`); err != nil {
		t.Fatal(err)
	}
	for round := range 10 {
		session := fmt.Sprintf("s%d", round)
		if err := stores[0].SaveSessionBundle("h", session, "옛 묶음", []string{"m-1", "m-2"}); err != nil {
			t.Fatal(err)
		}
		at := stores[0].SessionBundleAt("h", session)
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, len(stores))
		for i, s := range stores {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = s.ReplaceSessionBundle("h", session, fmt.Sprintf("묶음 %d", i), []string{"m-2"}, []string{"m-1"})
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("%s: 연결 %d: %v", session, i, err)
			}
		}
		text, ids, _ := stores[0].SessionBundle("h", session)
		var writes []string
		rows, err := stores[0].db.Query(`SELECT text FROM test_bundle_writes WHERE session=?`, session)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var w string
			if err := rows.Scan(&w); err != nil {
				t.Fatal(err)
			}
			writes = append(writes, w)
		}
		rows.Close()
		if len(writes) != 1 || writes[0] != text || !slices.Equal(ids, []string{"m-2"}) || stores[0].SessionBundleAt("h", session) != at {
			t.Fatalf("%s: 저장 %q %v, 적용된 쓰기 %q", session, text, ids, writes)
		}
		if marks := stores[0].SessionMarks("h", session, MarkBundle); len(marks) != 1 || !marks["m-1"] {
			t.Fatalf("%s: 뺀 기억 표시 %v", session, marks)
		}
	}
}
