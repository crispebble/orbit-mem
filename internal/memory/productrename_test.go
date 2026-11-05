package memory

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

// 제품 이름 바꾸기: product:<옛> 기억을 모두(상태와 승격에 관계없이) product:<새>로 한 트랜잭션에 옮긴다.
// 내용은 바꾸지 않고 판을 올려 이력에 남긴다. 사이에 끼우는 일(설정 쓰기)이 실패하면 아무것도 옮기지 않는다.

func renameFixture(t *testing.T) (*Store, map[string]Memory) {
	t.Helper()
	s, _ := newTestStore(t)
	out := map[string]Memory{}
	mk := func(key, title, scope string) {
		m, err := s.Save(SaveInput{Kind: KindDecision, Title: title, Body: title + " 본문", Scope: scope, Topic: key,
			Source: Source{Kind: SourceQuote, Excerpt: title}})
		if err != nil {
			t.Fatal(err)
		}
		out[key] = m
	}
	mk("core", "핵심 결정", "product:old")
	mk("plain", "보통 결정", "product:old")
	mk("stale", "낡은 결정", "product:old")
	mk("hidden", "숨긴 결정", "product:old")
	mk("other", "다른 제품 결정", "product:keep")
	mk("user", "사용자 규칙", ScopeGlobal)
	if _, err := s.Pin(out["core"].ID, Who{}); err != nil {
		t.Fatal(err)
	}
	for key, state := range map[string]string{"stale": StateStale, "hidden": StateHidden} {
		if _, err := s.DB().Exec(`UPDATE memories SET state=? WHERE id=?`, state, out[key].ID); err != nil {
			t.Fatal(err)
		}
	}
	for k := range out {
		m, _ := s.Get(out[k].ID)
		out[k] = m
	}
	return s, out
}

func TestRenameProduct_옛_제품의_기억을_모두_옮기고_내용과_승격은_그대로다(t *testing.T) {
	s, before := renameFixture(t)
	called := 0
	res, err := s.RenameProduct("old", "new", false, func() error { called++; return nil })
	if err != nil || called != 1 {
		t.Fatalf("rename: %v (between called %d)", err, called)
	}
	var ids []string
	for _, m := range res.Memories {
		ids = append(ids, m.ID)
	}
	slices.Sort(ids)
	want := []string{before["core"].ID, before["plain"].ID, before["stale"].ID, before["hidden"].ID}
	slices.Sort(want)
	if res.From != "product:old" || res.To != "product:new" || !slices.Equal(ids, want) {
		t.Fatalf("result = %+v", res)
	}
	for _, key := range []string{"core", "plain", "stale", "hidden"} {
		b := before[key]
		m, err := s.Get(b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if m.Scope != "product:new" || m.Version != b.Version+1 || m.State != b.State || m.Pinned != b.Pinned ||
			m.Title != b.Title || m.Body != b.Body || m.Topic != b.Topic || m.Project != b.Project {
			t.Errorf("%s: before %+v\nafter %+v", key, b, m)
		}
		h, err := s.History(b.ID)
		if err != nil || len(h) == 0 {
			t.Fatalf("%s history: %v", key, err)
		}
		last := h[len(h)-1]
		if last.Action != "rename" || last.Version != m.Version || last.Memory.Scope != "product:new" || last.Reason == "" {
			t.Errorf("%s last version = %+v", key, last)
		}
	}
	if m, _ := s.Get(before["core"].ID); !m.Pinned {
		t.Fatal("pinned memory left the core")
	}
	if core, _ := s.Core("product:new"); len(core) != 1 || core[0].ID != before["core"].ID {
		t.Fatalf("core of the new product = %+v", core)
	}
	for _, key := range []string{"other", "user"} {
		if m, _ := s.Get(before[key].ID); m.Scope != before[key].Scope || m.Version != before[key].Version {
			t.Errorf("%s changed: %+v", key, m)
		}
	}
	var left int
	s.DB().QueryRow(`SELECT COUNT(*) FROM memories WHERE scope='product:old'`).Scan(&left)
	if left != 0 {
		t.Fatalf("left behind %d", left)
	}
}

func TestRenameProduct_미리_보기는_아무것도_바꾸지_않는다(t *testing.T) {
	s, before := renameFixture(t)
	res, err := s.RenameProduct("old", "new", true, func() error { t.Fatal("dry run called between"); return nil })
	if err != nil || len(res.Memories) != 4 || !res.DryRun {
		t.Fatalf("dry = %+v %v", res, err)
	}
	for k, b := range before {
		if m, _ := s.Get(b.ID); m.Scope != b.Scope || m.Version != b.Version {
			t.Errorf("%s changed in a dry run: %+v", k, m)
		}
	}
}

func TestRenameProduct_사이_일이_실패하면_하나도_옮기지_않는다(t *testing.T) {
	s, before := renameFixture(t)
	boom := errors.New("config write failed")
	if _, err := s.RenameProduct("old", "new", false, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	for k, b := range before {
		m, _ := s.Get(b.ID)
		if m.Scope != b.Scope || m.Version != b.Version {
			t.Errorf("%s changed after a failed rename: %+v", k, m)
		}
		if h, _ := s.History(b.ID); h[len(h)-1].Action == "rename" {
			t.Errorf("%s has a rename version after rollback", k)
		}
	}
}

func TestRenameProduct_새_제품에_기억이_이미_있거나_이름이_틀리면_거절한다(t *testing.T) {
	s, before := renameFixture(t)
	for _, c := range []struct{ from, to string }{{"old", "keep"}, {"old", "old"}, {"old", ""}, {"", "new"}, {"old", "Bad Name"}, {"?", "new"}} {
		_, err := s.RenameProduct(c.from, c.to, false, func() error { return nil })
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%q → %q: err = %v", c.from, c.to, err)
		}
	}
	for k, b := range before {
		if m, _ := s.Get(b.ID); m.Scope != b.Scope || m.Version != b.Version {
			t.Errorf("%s changed: %+v", k, m)
		}
	}
}

// 다른 프로세스(훅·MCP)가 같은 때 옛 제품에 저장해도 잠금 안에서 차례로 돈다. 쓰는 쪽은 바꾸기 전부터 바꾼 뒤까지
// 쉬지 않고 저장한다: 바꾸기 전에 저장된 것은 모두 옮겨 가고, 옮긴 목록은 정확히 지금 새 자리에 있는 기억이며,
// 잃거나 둘이 된 기억이 없고, 어느 쪽도 잠금 때문에 실패하지 않는다.
func TestRenameProduct_동시에_저장해도_잃거나_어긋나지_않는다(t *testing.T) {
	for round := 0; round < 5; round++ {
		s, _ := newTestStore(t)
		const writers, before, after = 4, 3, 3
		var wg, warm sync.WaitGroup
		errs := make(chan error, 1000)
		renamed := make(chan struct{})
		saved := make([]int, writers)
		warm.Add(writers)
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				extra := -1
				for i := 0; extra != 0; i++ {
					if _, err := s.Save(SaveInput{Kind: KindFact, Title: fmt.Sprintf("사실 %d-%d", w, i), Scope: "product:old",
						Source: Source{Kind: SourceSession, Ref: "x"}}); err != nil {
						errs <- err
						if i < before {
							warm.Done()
						}
						return
					}
					saved[w]++
					if i == before-1 {
						warm.Done()
					}
					if extra > 0 { // 바꾸기가 끝난 것을 본 뒤에 시작한 저장: 옛 자리에 남는다
						extra--
						continue
					}
					select {
					case <-renamed:
						extra = after
					default:
					}
				}
			}(w)
		}
		warm.Wait() // 쓰는 쪽이 모두 저장 중일 때 바꾼다
		var pre int
		s.DB().QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&pre)
		res, err := s.RenameProduct("old", "new", false, func() error { return nil })
		close(renamed)
		wg.Wait()
		close(errs)
		if err != nil {
			t.Fatalf("round %d: rename: %v", round, err)
		}
		for err := range errs {
			t.Fatalf("round %d: save: %v", round, err)
		}
		total := 0
		for _, n := range saved {
			total += n
		}
		var moved, old, all int
		s.DB().QueryRow(`SELECT COUNT(*) FROM memories WHERE scope='product:new'`).Scan(&moved)
		s.DB().QueryRow(`SELECT COUNT(*) FROM memories WHERE scope='product:old'`).Scan(&old)
		s.DB().QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&all)
		if all != total || moved+old != all || len(res.Memories) != moved || moved < pre || old < writers*after {
			t.Fatalf("round %d: saved %d, all %d, before rename %d, moved %d (reported %d), old %d", round, total, all, pre, moved, len(res.Memories), old)
		}
	}
}
