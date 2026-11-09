package memory

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// 멱등 저장(계획 D9·M7). 같은 idempotencyKey로 다시 오면 한 번만 적용한다: 키 확인과 기록은 기억 쓰기와 같은
// 트랜잭션이고, 적용된 것(저장·update·supersede·skip)만 기록한다.

var ai = Who{Actor: ActorAI}

func idemSave(title, key, hash string) SaveInput {
	return SaveInput{Kind: KindFact, Title: title, Body: "본문 " + title, Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "s"}, Idem: &Idem{Key: key, Hash: hash}, Who: ai}
}

func saveRequestOf(t *testing.T, s *Store, key string) (SaveRequest, bool) {
	t.Helper()
	r, ok, err := s.SaveRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	return r, ok
}

func versionsOf(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_versions WHERE memory_id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIdem_같은_키_같은_해시로_다시_저장하면_쓰지_않고_지금_기억을_돌려준다(t *testing.T) {
	s, _ := newTestStore(t)
	m, rep, err := s.SaveWith(idemSave("멱등 기억", "k1", "h1"))
	if err != nil || rep.Replayed != nil {
		t.Fatalf("first save %+v %+v %v", m, rep, err)
	}
	r, ok := saveRequestOf(t, s, "k1")
	if !ok || r.MemoryID != m.ID || r.Action != ApplySave || r.Hash != "h1" || r.At == 0 {
		t.Fatalf("record %+v %v", r, ok)
	}
	body := "나중에 고친 본문"
	if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body}); err != nil {
		t.Fatal(err)
	}
	sources, versions := sourceCount(t, s, m.ID), versionsOf(t, s, m.ID)
	again, rep, err := s.SaveWith(idemSave("멱등 기억", "k1", "h1"))
	if err != nil || again.ID != m.ID || again.Body != body || rep.Replayed == nil || rep.Replayed.Action != ApplySave {
		t.Fatalf("replay %+v %+v %v", again, rep, err)
	}
	// 같은 제목 단축(출처·날 더하기)도 하지 않는다: 아무것도 쓰지 않는다.
	if sourceCount(t, s, m.ID) != sources || versionsOf(t, s, m.ID) != versions || countRows(t, s, "save_requests") != 1 {
		t.Fatalf("replay wrote: sources %d versions %d requests %d", sourceCount(t, s, m.ID), versionsOf(t, s, m.ID), countRows(t, s, "save_requests"))
	}
	if n, _ := s.Count(); n != 1 {
		t.Fatalf("count %d", n)
	}
	// 키 없는 저장은 지금과 같고 기록을 남기지 않는다.
	if _, err := s.Save(SaveInput{Kind: KindFact, Title: "키 없는 기억", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}}); err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, "save_requests") != 1 {
		t.Fatal("save without a key recorded")
	}
}

func TestIdem_같은_키에_다른_해시나_다른_동작이면_충돌_오류이고_아무것도_쓰지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	m, err := s.Save(idemSave("처음 기억", "k1", "h1"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Save(idemSave("다른 기억", "k1", "h2"))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("other hash err %v", err)
	}
	for _, l := range []i18n.Lang{i18n.EN, i18n.KO} {
		if msg := i18n.TIn(l, "memory.err.idempotency_conflict"); msg == "" || msg == "memory.err.idempotency_conflict" {
			t.Fatalf("message %s %q", l, msg)
		}
	}
	body := "고침"
	if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body, Who: ai, Idem: &Idem{Key: "k1", Hash: "h1"}}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("update with a save key err %v", err)
	}
	if _, _, err := s.RecordSkip(Idem{Key: "k1", Hash: "h1"}, m.ID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("skip with a save key err %v", err)
	}
	if n, _ := s.Count(); n != 1 || mustGet(t, s, m.ID).Body != "본문 처음 기억" || versionsOf(t, s, m.ID) != 1 {
		t.Fatalf("conflict wrote: count %d", n)
	}
}

func TestIdem_키와_해시가_비면_입력_오류다(t *testing.T) {
	s, _ := newTestStore(t)
	for _, idem := range []Idem{{Key: "", Hash: "h"}, {Key: "  ", Hash: "h"}, {Key: "k", Hash: ""}} {
		in := idemSave("빈 키", "", "")
		in.Idem = &idem
		if _, err := s.Save(in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("idem %+v err %v", idem, err)
		}
	}
	if n, _ := s.Count(); n != 0 {
		t.Fatalf("count %d", n)
	}
}

func TestIdem_실패한_저장은_기록하지_않아_같은_키로_다시_할_수_있다(t *testing.T) {
	s, _ := newTestStore(t)
	in := idemSave("핵심에 못 오르는 후보", "k1", "h1")
	in.Pin = true // 후보는 핵심에 오르지 못한다: 저장 전체가 거절된다
	if _, err := s.Save(in); err == nil {
		t.Fatal("pin of a candidate saved")
	}
	if _, ok := saveRequestOf(t, s, "k1"); ok {
		t.Fatal("failed save recorded")
	}
	m, err := s.Save(idemSave("핵심에 못 오르는 후보", "k1", "h2"))
	if err != nil || m.ID == "" {
		t.Fatalf("retry %+v %v", m, err)
	}
	if r, ok := saveRequestOf(t, s, "k1"); !ok || r.MemoryID != m.ID || r.Hash != "h2" {
		t.Fatalf("record %+v", r)
	}
}

func TestIdem_skip은_아무것도_쓰지_않고_키만_기록한다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveDoc(t, s, "고른 기억", "본문", "")
	versions := versionsOf(t, s, m.ID)
	got, replay, err := s.RecordSkip(Idem{Key: "k1", Hash: "h1"}, m.ID)
	if err != nil || got.ID != m.ID || replay != nil {
		t.Fatalf("skip %+v %+v %v", got, replay, err)
	}
	if r, ok := saveRequestOf(t, s, "k1"); !ok || r.Action != ApplySkip || r.MemoryID != m.ID {
		t.Fatalf("record %+v %v", r, ok)
	}
	got, replay, err = s.RecordSkip(Idem{Key: "k1", Hash: "h1"}, m.ID)
	if err != nil || got.ID != m.ID || replay == nil || replay.Action != ApplySkip {
		t.Fatalf("skip again %+v %+v %v", got, replay, err)
	}
	if versionsOf(t, s, m.ID) != versions || countRows(t, s, "save_requests") != 1 {
		t.Fatal("skip wrote the memory")
	}
	// 없는 기억은 건너뛸 수 없고 기록하지 않는다.
	if _, _, err := s.RecordSkip(Idem{Key: "k2", Hash: "h"}, "m-none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("skip of a missing memory err %v", err)
	}
	if _, ok := saveRequestOf(t, s, "k2"); ok {
		t.Fatal("skip of a missing memory recorded")
	}
	if _, _, err := s.RecordSkip(Idem{Key: "", Hash: "h"}, m.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("skip without a key err %v", err)
	}
}

func TestIdem_update는_한_번만_적용하고_사용자_말_보호를_그대로_거친다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveDoc(t, s, "고칠 기억", "옛 본문", "")
	body := "새 본문"
	idem := &Idem{Key: "k1", Hash: "h1"}
	got, replay, err := s.UpdateWith(UpdateInput{ID: m.ID, Body: &body, ExpectedVersion: m.Version, Who: ai, Idem: idem})
	if err != nil || got.Body != body || replay != nil {
		t.Fatalf("update %+v %+v %v", got, replay, err)
	}
	versions := versionsOf(t, s, m.ID)
	// 다시 오면 판본 확인(ExpectedVersion은 이제 낡음)보다 먼저 기록을 보고 지금 기억을 돌려준다.
	got, replay, err = s.UpdateWith(UpdateInput{ID: m.ID, Body: &body, ExpectedVersion: m.Version, Who: ai, Idem: idem})
	if err != nil || got.Version != m.Version+1 || replay == nil || replay.Action != ApplyUpdate || versionsOf(t, s, m.ID) != versions {
		t.Fatalf("replay %+v %+v %v", got, replay, err)
	}
	if r, ok := saveRequestOf(t, s, "k1"); !ok || r.MemoryID != m.ID || r.Action != ApplyUpdate {
		t.Fatalf("record %+v", r)
	}
	// Update도 같은 키를 받는다(UpdateWith와 같은 길).
	if again, err := s.Update(UpdateInput{ID: m.ID, Body: &body, Who: ai, Idem: idem}); err != nil || again.Version != m.Version+1 {
		t.Fatalf("Update replay %+v %v", again, err)
	}
	// 사용자 말 기억: AI가 사용자 말 없이 고치면 거절되고 키는 기록되지 않는다.
	q := quoted(t, s, "사용자 말 규칙")
	if _, _, err := s.UpdateWith(UpdateInput{ID: q.ID, Body: &body, Who: ai, Idem: &Idem{Key: "k2", Hash: "h"}}); !errors.Is(err, ErrProtected) {
		t.Fatalf("protected err %v", err)
	}
	if _, ok := saveRequestOf(t, s, "k2"); ok {
		t.Fatal("refused update recorded")
	}
	withWords := Who{Actor: ActorAI, UserQuote: "그 규칙 본문을 바꿔"}
	if got, _, err := s.UpdateWith(UpdateInput{ID: q.ID, Body: &body, Who: withWords, Idem: &Idem{Key: "k2", Hash: "h2"}}); err != nil || got.Body != body {
		t.Fatalf("update with words %+v %v", got, err)
	}
}

func TestIdem_Replaces는_옛_판을_같은_트랜잭션에서_낡게_하고_한_번만_적용한다(t *testing.T) {
	s, _ := newTestStore(t)
	old := saveDoc(t, s, "옛 판", "옛 본문", "")
	in := idemSave("새 판", "k1", "h1")
	in.Replaces = old.ID
	m, rep, err := s.SaveWith(in)
	if err != nil || len(rep.Superseded) != 1 || rep.Superseded[0] != old.ID {
		t.Fatalf("supersede %+v %+v %v", m, rep, err)
	}
	if o := mustGet(t, s, old.ID); o.State != StateStale || o.SupersededBy != m.ID {
		t.Fatalf("old %+v", o)
	}
	if r, ok := saveRequestOf(t, s, "k1"); !ok || r.MemoryID != m.ID || r.Action != ApplySupersede {
		t.Fatalf("record %+v", r)
	}
	again, rep, err := s.SaveWith(in)
	if err != nil || again.ID != m.ID || rep.Replayed == nil || rep.Replayed.Action != ApplySupersede {
		t.Fatalf("replay %+v %+v %v", again, rep, err)
	}
	if n, _ := s.Count(); n != 2 {
		t.Fatalf("count %d", n)
	}
	if _, err := s.Save(SaveInput{Kind: KindFact, Title: "없는 옛 판", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}, Replaces: "m-none"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaces a missing memory err %v", err)
	}
	if n, _ := s.Count(); n != 2 {
		t.Fatalf("count %d", n)
	}
}

func TestIdem_Replaces는_사용자_말과_믿을_수_없는_출처의_막기를_그대로_거친다(t *testing.T) {
	s, _ := newTestStore(t)
	q := quoted(t, s, "사용자 말 판")
	in := idemSave("AI가 바꾼 판", "k1", "h1")
	in.Replaces = q.ID
	if _, err := s.Save(in); !errors.Is(err, ErrProtected) {
		t.Fatalf("AI over the user's words err %v", err)
	}
	if n, _ := s.Count(); n != 1 || mustGet(t, s, q.ID).State != StateConfirmed {
		t.Fatalf("refused supersede wrote: count %d", n)
	}
	if _, ok := saveRequestOf(t, s, "k1"); ok {
		t.Fatal("refused supersede recorded")
	}
	// 출처가 믿을 수 없는 것뿐인 새 판은 agent 기억을 낡게 하지 못한다(D7): 대체 대신 거절이다.
	agent := saveDoc(t, s, "AI가 배운 판", "본문", "")
	web := idemSave("웹 글이 바꾼 판", "k2", "h")
	web.Source = Source{Kind: SourceFile, Ref: "web", Trust: TrustUntrusted}
	web.Replaces = agent.ID
	if _, err := s.Save(web); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("untrusted over agent err %v", err)
	}
	if n, _ := s.Count(); n != 2 || mustGet(t, s, agent.ID).State != StateCandidate {
		t.Fatalf("refused untrusted supersede wrote: count %d", n)
	}
	// 같은 제목 단축 길에서도 막기를 거친다.
	short := idemSave("AI가 배운 판", "k3", "h")
	short.Source = Source{Kind: SourceFile, Ref: "web", Trust: TrustUntrusted}
	short.Replaces = q.ID
	if _, err := s.Save(short); !errors.Is(err, ErrProtected) {
		t.Fatalf("short path over the user's words err %v", err)
	}
	// 사용자 말을 함께 주면 바꾼다.
	in.Who = Who{Actor: ActorAI, UserQuote: "그 규칙을 새 판으로 바꿔"}
	in.Idem = &Idem{Key: "k4", Hash: "h"}
	m, err := s.Save(in)
	if err != nil {
		t.Fatalf("with words %v", err)
	}
	if o := mustGet(t, s, q.ID); o.State != StateStale || o.SupersededBy != m.ID {
		t.Fatalf("old %+v", o)
	}
}

func TestIdem_같은_키로_두_연결이_동시에_저장해도_기억은_하나다(t *testing.T) {
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
	const keys, callers = 15, 4 // 키마다 연결 둘에서 넷이 한꺼번에 부른다
	for k := range keys {
		key := fmt.Sprintf("k%d", k)
		// 키마다 제목을 달리 해 같은 제목 단축이 아니라 키가 막는지 본다: 부르는 쪽마다 Body만 같고 제목은 같다.
		in := idemSave(fmt.Sprintf("동시 기억 %d", k), key, "h")
		start := make(chan struct{})
		var wg sync.WaitGroup
		ids := make(chan string, callers)
		errs := make(chan error, callers)
		for c := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				m, err := stores[c%2].Save(in)
				if err != nil {
					errs <- err
					return
				}
				ids <- m.ID
			}()
		}
		close(start)
		wg.Wait()
		close(ids)
		close(errs)
		for err := range errs {
			t.Fatalf("key %s: %v", key, err)
		}
		var first string
		for id := range ids {
			if first == "" {
				first = id
			}
			if id != first {
				t.Fatalf("key %s: two memories %s %s", key, first, id)
			}
		}
		// 같은 제목 단축이었다면 출처가 부른 수만큼 붙었다: 키가 막았으면 출처 하나, 판 하나다.
		if n := sourceCount(t, stores[0], first); n != 1 || versionsOf(t, stores[0], first) != 1 {
			t.Fatalf("key %s: sources %d versions %d", key, n, versionsOf(t, stores[0], first))
		}
	}
	if n, _ := stores[0].Count(); n != keys || countRows(t, stores[0], "save_requests") != keys {
		t.Fatalf("memories %d requests %d", n, countRows(t, stores[0], "save_requests"))
	}
}

func TestIdem_하루_정리가_30일_넘은_기록을_지운다(t *testing.T) {
	s, c := newTestStore(t)
	m := saveDoc(t, s, "기록할 기억", "본문", "")
	if _, _, err := s.RecordSkip(Idem{Key: "old", Hash: "h"}, m.ID); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(2 * 24 * time.Hour)
	if _, _, err := s.RecordSkip(Idem{Key: "new", Hash: "h"}, m.ID); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(29 * 24 * time.Hour) // old는 31일, new는 29일
	r := s.Tidy(TidyOptions{})
	if len(r.Errors) != 0 {
		t.Fatalf("tidy errors %v", r.Errors)
	}
	if _, ok := saveRequestOf(t, s, "old"); ok {
		t.Fatal("31 days old request kept")
	}
	if _, ok := saveRequestOf(t, s, "new"); !ok {
		t.Fatal("29 days old request dropped")
	}
	if n, err := s.PruneSaveRequests(SaveRequestDays); err != nil || n != 0 {
		t.Fatalf("prune again %d %v", n, err)
	}
	// 지운 키는 다시 쓸 수 있다(오래된 재시도는 새 요청이다).
	if _, _, err := s.RecordSkip(Idem{Key: "old", Hash: "other"}, m.ID); err != nil {
		t.Fatalf("reuse pruned key %v", err)
	}
}
