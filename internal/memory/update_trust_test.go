package memory

// 고침의 새 출처(설계 9절, D7, 수용 기준 L-9, 검토 a10 지적 1): 고침에 붙는 새 출처는 고침과 같은 트랜잭션에서
// 붙고, 출처가 믿을 수 없는 것(untrusted·system)뿐인 고침은 owner·agent 기억을 고쳐 쓰지 못한다(대체와 같은 규칙).

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func webSource(trust string) Source {
	return Source{Kind: SourceFile, Ref: "https://example.com/x", Trust: trust}
}

func TestUpdate_믿을_수_없는_새_출처로는_agent_기억을_고쳐_쓰지_못한다(t *testing.T) {
	s := supStore(t)
	for _, tr := range []string{TrustUntrusted, TrustSystem} {
		m, _ := mustSave(t, s, aiLearns(KindLesson, "배포 전에 드라이런 "+tr, ""))
		title, body := "배포는 바로 한다 "+tr, "드라이런 없이 바로 돌린다"
		_, err := s.Update(UpdateInput{ID: m.ID, Title: &title, Body: &body, Who: Who{Actor: ActorAI}, Sources: []Source{webSource(tr)}})
		if !errors.Is(err, ErrUntrusted) {
			t.Fatalf("%s: err %v, want ErrUntrusted", tr, err)
		}
		got := stateOf(t, s, m.ID)
		if got.Title != m.Title || got.Body != m.Body || got.Version != m.Version || len(got.Sources) != 1 || got.Trust != TrustAgent ||
			versionsOf(t, s, m.ID) != 1 {
			t.Fatalf("%s: 바뀌었다 %+v", tr, got)
		}
	}
	// 사용자 말(quote)로 저장한 기억(owner)도 같다.
	q, _ := mustSave(t, s, userSays(KindLesson, "커밋 전에 시험", ""))
	title := "커밋 전에 시험 안 함"
	if _, err := s.Update(UpdateInput{ID: q.ID, Title: &title, Who: Who{Actor: ActorAI}, Sources: []Source{webSource(TrustUntrusted)}}); err == nil {
		t.Fatal("owner 기억을 믿을 수 없는 출처로 고쳤다")
	}
	if got := stateOf(t, s, q.ID); got.Title != q.Title || len(got.Sources) != 1 {
		t.Fatalf("owner 바뀌었다 %+v", got)
	}
}

func TestUpdate_새_출처는_고침과_같은_판에_붙고_등급은_통로_상한을_따른다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, aiLearns(KindLesson, "배포 전에 드라이런", ""))
	title := "배포 전에 드라이런을 꼭"
	got, err := s.Update(UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI},
		Sources: []Source{{Kind: SourceFile, Ref: "/tmp/runbook.md", Trust: TrustOwner}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != title || got.Version != m.Version+1 || len(got.Sources) != 2 || got.Trust != TrustAgent {
		t.Fatalf("update %+v", got)
	}
	for _, src := range got.Sources {
		if SourceTrust(src) == TrustOwner {
			t.Fatalf("상한 없이 owner가 됐다: %+v", got.Sources)
		}
	}
	// 통로 상한이 owner면 그대로 owner다.
	body := "운영 배포 전에"
	got, err = s.Update(UpdateInput{ID: m.ID, Body: &body, Sources: []Source{{Kind: SourceFile, Ref: "/tmp/b.md"}}, TrustLimit: TrustOwner})
	if err != nil || got.Trust != TrustOwner || len(got.Sources) != 3 {
		t.Fatalf("owner limit %+v %v", got, err)
	}
	// 모르는 종류·등급은 거절하고 아무것도 쓰지 않는다.
	for _, src := range []Source{{Kind: "web", Ref: "x"}, {Kind: SourceFile, Ref: "x", Trust: "trusted"}} {
		if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body, Sources: []Source{src}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: err %v", src, err)
		}
	}
	if v := stateOf(t, s, m.ID).Version; v != m.Version+2 {
		t.Fatalf("version %d", v)
	}
}

func TestUpdate_믿을_수_없는_기억은_믿을_수_없는_출처로_고칠_수_있다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, untrustedLearns(KindFact, "웹 글이 말한 포트", ""))
	title := "웹 글이 말한 포트는 8080"
	got, err := s.Update(UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI}, Sources: []Source{webSource(TrustSystem)}})
	if err != nil || got.Title != title || got.Trust != TrustUntrusted || len(got.Sources) != 2 {
		t.Fatalf("update %+v %v", got, err)
	}
}

func TestUpdate_사용자_말이_오면_믿을_수_없는_출처로도_고치고_사용자_말_보호는_그대로다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, aiLearns(KindLesson, "배포 전에 드라이런", ""))
	title := "배포 전에 드라이런을 꼭"
	got, err := s.Update(UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI, UserQuote: "그 글대로 고쳐 줘"},
		Sources: []Source{webSource(TrustUntrusted)}})
	if err != nil || got.Title != title || got.Trust != TrustOwner {
		t.Fatalf("with words %+v %v", got, err)
	}
	// 사용자 말 기억은 사용자 말 없이 고치지 못한다(guardTx): 새 출처가 agent여도.
	q, _ := mustSave(t, s, userSays(KindLesson, "커밋 전에 시험", ""))
	title = "커밋 전에 시험 안 함"
	if _, err := s.Update(UpdateInput{ID: q.ID, Title: &title, Who: Who{Actor: ActorAI}, Sources: []Source{{Kind: SourceSession, Ref: "s2"}}}); !errors.Is(err, ErrProtected) {
		t.Fatalf("protected err %v", err)
	}
	if got := stateOf(t, s, q.ID); got.Title != q.Title || len(got.Sources) != 1 {
		t.Fatalf("protected 바뀌었다 %+v", got)
	}
}

func TestUpdate_출처를_붙이지_못하면_고침도_되돌린다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, aiLearns(KindLesson, "배포 전에 드라이런", ""))
	if _, err := s.db.Exec(`CREATE TRIGGER sources_fail BEFORE INSERT ON sources BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	title := "배포 전에 드라이런을 꼭"
	idem := &Idem{Key: "k-fail", Hash: "sha256:a"}
	if _, _, err := s.UpdateWith(UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI}, Idem: idem,
		Sources: []Source{{Kind: SourceFile, Ref: "/tmp/runbook.md"}}}); err == nil {
		t.Fatal("no error")
	}
	if got := stateOf(t, s, m.ID); got.Title != m.Title || got.Version != m.Version || versionsOf(t, s, m.ID) != 1 {
		t.Fatalf("고침이 남았다 %+v", got)
	}
	if _, ok, err := s.SaveRequest("k-fail"); ok || err != nil {
		t.Fatalf("키가 기록됐다 %v %v", ok, err)
	}
	// 같은 키로 다시 오면 이번에는 고침과 출처를 함께 한 번 적용한다.
	if _, err := s.db.Exec(`DROP TRIGGER sources_fail`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, replay, err := s.UpdateWith(UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI}, Idem: idem,
			Sources: []Source{{Kind: SourceFile, Ref: "/tmp/runbook.md"}}})
		if err != nil || (replay != nil) != (i == 1) || got.Version != m.Version+1 || len(got.Sources) != 2 {
			t.Fatalf("try %d: %+v replay %v %v", i, got, replay, err)
		}
	}
}

func TestUpdate_등급_칸이_없는_DB는_낮춘_출처의_고침을_통째로_거절한다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, aiLearns(KindLesson, "배포 전에 드라이런", ""))
	if _, err := s.db.Exec(`ALTER TABLE sources DROP COLUMN trust`); err != nil {
		t.Fatal(err)
	}
	title := "배포 전에 드라이런을 꼭"
	// 사용자 말이 있어 등급 규칙은 지나지만 낮춘 등급을 적을 칸이 없다(H3): 고침까지 되돌린다.
	_, err := s.Update(UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI, UserQuote: "고쳐 줘"}, Sources: []Source{webSource(TrustUntrusted)}})
	if !errors.Is(err, ErrTrustNotStored) {
		t.Fatalf("err %v", err)
	}
	if got := stateOf(t, s, m.ID); got.Title != m.Title || got.Version != m.Version || len(got.Sources) != 1 {
		t.Fatalf("바뀌었다 %+v", got)
	}
}

func TestUpdate_같은_키로_두_연결이_동시에_고쳐도_고침과_출처는_한_번이다(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	var stores []*Store
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		stores = append(stores, s)
	}
	m, _ := mustSave(t, stores[0], aiLearns(KindLesson, "배포 전에 드라이런", ""))
	const keys, callers = 10, 4
	for k := range keys {
		title := fmt.Sprintf("배포 전에 드라이런 %d", k)
		in := UpdateInput{ID: m.ID, Title: &title, Who: Who{Actor: ActorAI}, Idem: &Idem{Key: fmt.Sprintf("k%d", k), Hash: "h"},
			Sources: []Source{{Kind: SourceFile, Ref: fmt.Sprintf("/tmp/r%d.md", k)}}}
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, callers)
		for c := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, _, err := stores[c%2].UpdateWith(in); err != nil {
					errs <- err
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("key %d: %v", k, err)
		}
		if n, v := sourceCount(t, stores[0], m.ID), versionsOf(t, stores[0], m.ID); n != k+2 || v != k+2 {
			t.Fatalf("key %d: sources %d versions %d", k, n, v)
		}
	}
}
