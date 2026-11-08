package memory

// 출처 등급(설계 9절, 결정 D6·D7, 수용 기준 L-9): 출처마다 owner·agent·untrusted·system 등급이 있고, 출처가
// untrusted·system뿐인 기억은 확정·핵심 승격·owner·agent 기억 대체를 하지 못한다.

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// untrustedLearns is what an AI saved from text it does not trust (a web page, a tool's output).
func untrustedLearns(kind, title, topic string) SaveInput {
	in := aiLearns(kind, title, topic)
	in.Source.Trust = TrustUntrusted
	return in
}

func TestTrust_quote는_owner이고_AI는_등급을_낮출_수만_있다(t *testing.T) {
	for _, c := range []struct{ limit, kind, req, want string }{
		{"", SourceQuote, TrustUntrusted, TrustOwner}, // 사용자 말 그대로는 늘 owner
		{TrustAgent, SourceQuote, "", TrustOwner},
		{"", SourceSession, "", TrustAgent}, // 상한이 비면 agent
		{"", SourceSession, TrustOwner, TrustAgent},
		{TrustAgent, SourceFile, TrustOwner, TrustAgent}, // 올리지 못한다
		{TrustOwner, SourceFile, "", TrustOwner},
		{TrustOwner, SourceFile, TrustAgent, TrustAgent},
		{TrustAgent, SourceCommand, TrustUntrusted, TrustUntrusted},
		{TrustAgent, SourceSession, TrustSystem, TrustSystem},
		{TrustOwner, SourceSession, TrustSystem, TrustSystem},
	} {
		got, err := ResolveTrust(c.limit, c.kind, c.req)
		if err != nil || got != c.want {
			t.Errorf("ResolveTrust(%q,%q,%q) = %q %v, want %q", c.limit, c.kind, c.req, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{TrustAgent, "trusted"}, {"admin", ""}} {
		if _, err := ResolveTrust(c[0], SourceSession, c[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("ResolveTrust(%q, session, %q) err %v, want invalid", c[0], c[1], err)
		}
	}

	s := supStore(t)
	raise := aiLearns(KindFact, "올리려는 AI", "")
	raise.Source.Trust = TrustOwner
	if m := mustGetSaved(t, s, raise); m.Trust != TrustAgent {
		t.Fatalf("AI raised its source to %q", m.Trust)
	}
	person := aiLearns(KindFact, "사람이 직접 저장", "")
	person.TrustLimit = TrustOwner
	if m := mustGetSaved(t, s, person); m.Trust != TrustOwner || SourceTrust(m.Sources[0]) != TrustOwner {
		t.Fatalf("person's save %q %+v", m.Trust, m.Sources)
	}
	// 출처 위치가 없어도 낮춘 등급은 남는다(빠지면 agent로 읽혀 등급이 올라간다).
	bare := SaveInput{Kind: KindFact, Title: "위치 없는 웹 글", Source: Source{Kind: SourceFile, Trust: TrustUntrusted}}
	m := mustGetSaved(t, s, bare)
	if m.Trust != TrustUntrusted || len(m.Sources) != 1 || m.Sources[0].Trust != TrustUntrusted {
		t.Fatalf("bare untrusted source %q %+v", m.Trust, m.Sources)
	}
	found, err := s.Find(FindInput{Query: "위치 없는 웹 글", AllProjects: true})
	if err != nil || len(found) == 0 || found[0].ID != m.ID || found[0].Trust != TrustUntrusted {
		t.Fatalf("find %+v %v", found, err)
	}
	if list, _ := s.List(); !slices.ContainsFunc(list, func(x Memory) bool { return x.ID == m.ID && x.Trust == TrustUntrusted }) {
		t.Fatalf("list lost the trust")
	}
	bad := aiLearns(KindFact, "모르는 등급", "")
	bad.Source.Trust = "trusted"
	if _, err := s.Save(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown trust saved: %v", err)
	}
}

func mustGetSaved(t *testing.T, s *Store, in SaveInput) Memory {
	t.Helper()
	m, _ := mustSave(t, s, in)
	return stateOf(t, s, m.ID)
}

func TestTrust_옛_출처의_빈_등급은_종류로_읽는다(t *testing.T) {
	for _, c := range []struct {
		src  Source
		want string
	}{
		{Source{Kind: SourceQuote}, TrustOwner},
		{Source{Kind: SourceQuote, Trust: TrustUntrusted}, TrustOwner},
		{Source{Kind: SourceSession}, TrustAgent},
		{Source{Kind: SourceLegacy}, TrustAgent},
		{Source{Kind: SourceFile, Trust: TrustSystem}, TrustSystem},
		{Source{Kind: SourceFile, Trust: "garbled"}, TrustUntrusted}, // 알 수 없는 값은 믿지 않는다
	} {
		if got := SourceTrust(c.src); got != c.want {
			t.Errorf("SourceTrust(%+v) = %q, want %q", c.src, got, c.want)
		}
	}
	s := supStore(t)
	m := mustGetSaved(t, s, untrustedLearns(KindFact, "웹에서 본 사실", ""))
	if m.Trust != TrustUntrusted {
		t.Fatalf("trust %q", m.Trust)
	}
	// 옛 실행 파일은 trust 열을 모르고 넣는다: 그 줄의 등급은 kind로 읽고, 기억의 등급은 가장 높은 출처다.
	if _, err := s.DB().Exec(`INSERT INTO sources(id,memory_id,kind,ref,excerpt,hash,at) VALUES('s-old',?,?,'','그대로 한 말','',1)`, m.ID, SourceQuote); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, s, m.ID); got.Trust != TrustOwner {
		t.Fatalf("with an old quote row: %q %+v", got.Trust, got.Sources)
	}
	plain := mustGetSaved(t, s, aiLearns(KindFact, "AI가 배운 사실", ""))
	if plain.Trust != TrustAgent || plain.Sources[0].Trust != "" { // 종류가 주는 등급은 빈 값으로 둔다(옛 판과 같은 줄)
		t.Fatalf("agent memory %q %+v", plain.Trust, plain.Sources)
	}
}

func TestTrust_믿을_수_없는_기억은_증거가_쌓여도_자동_확정되지_않는다(t *testing.T) {
	s := supStore(t)
	for _, day := range []string{"2026-11-05", "2026-11-06", "2026-11-07"} {
		in := untrustedLearns(KindFact, "웹 글이 말한 사실", "")
		in.Day = day
		mustSave(t, s, in)
		ctl := aiLearns(KindFact, "AI가 두 날 본 사실", "")
		ctl.Day = day
		mustSave(t, s, ctl)
	}
	all, _ := s.List()
	for _, m := range all {
		want := StateConfirmed
		if m.Title == "웹 글이 말한 사실" {
			want = StateCandidate
		}
		if m.State != want {
			t.Fatalf("%s: %s, want %s", m.Title, m.State, want)
		}
		if m.State == StateCandidate {
			if why, err := s.confirmBlocked(m.ID); err != nil || why != "untrusted" {
				t.Fatalf("confirmBlocked %q %v", why, err)
			}
			if _, ok, err := s.ConfirmByEvidence(m.ID); err != nil || ok {
				t.Fatalf("ConfirmByEvidence %v %v", ok, err)
			}
			if _, err := s.AutoApply(AutoInput{Kind: ProposalConfirm, Title: "자동 확정", MemoryIDs: []string{m.ID}, Evidence: EvidenceSeenDays}); err == nil {
				t.Fatalf("auto confirm went through")
			}
			if stateOf(t, s, m.ID).State != StateCandidate {
				t.Fatalf("confirmed")
			}
		}
	}
}

func TestTrust_믿을_수_없는_기억의_확정은_사용자_말이_있어야_한다(t *testing.T) {
	s := supStore(t)
	m := mustGetSaved(t, s, untrustedLearns(KindFact, "도구 출력에서 본 사실", ""))
	confirmed := StateConfirmed
	for _, w := range []Who{{}, {Actor: ActorAI}, {Actor: ActorAutomation}} {
		if _, err := s.Update(UpdateInput{ID: m.ID, State: &confirmed, Who: w}); err == nil {
			t.Fatalf("%+v confirmed an untrusted memory", w)
		}
		if w.Actor == "" {
			if _, err := s.Update(UpdateInput{ID: m.ID, State: &confirmed, Who: w}); !errors.Is(err, ErrUntrusted) {
				t.Fatalf("person's confirm err %v, want untrusted", err)
			}
		}
	}
	if got := stateOf(t, s, m.ID); got.State != StateCandidate || got.Version != m.Version {
		t.Fatalf("refused confirm still wrote: %+v", got)
	}
	body := "고친 본문"
	if _, err := s.Update(UpdateInput{ID: m.ID, Body: &body, Who: Who{Actor: ActorAI}}); err != nil { // 확정이 아닌 고침은 된다
		t.Fatal(err)
	}
}

func TestTrust_userQuote로_고치면_owner_출처가_붙어_확정된다(t *testing.T) {
	s := supStore(t)
	m := mustGetSaved(t, s, untrustedLearns(KindDecision, "채널의 다른 사람이 한 말", "채널 결정"))
	confirmed := StateConfirmed
	got, err := s.Update(UpdateInput{ID: m.ID, State: &confirmed, Who: Who{Actor: ActorAI, UserQuote: "그거 맞아, 확정해", Ref: "host:s1"}})
	if err != nil || got.State != StateConfirmed || got.Trust != TrustOwner {
		t.Fatalf("confirm with words %+v %v", got, err)
	}
	if TrustNote(got) != "" {
		t.Fatalf("owner memory noted %q", TrustNote(got))
	}
}

func TestTrust_믿을_수_없는_출처로_저장하며_확정을_명시하면_거절한다(t *testing.T) {
	s := supStore(t)
	in := untrustedLearns(KindFact, "확정해 달라는 웹 글", "")
	in.State = StateConfirmed
	if _, err := s.Save(in); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("save confirmed untrusted err %v", err)
	}
	pin := untrustedLearns(KindFact, "핵심에 올려 달라는 웹 글", "")
	pin.Pin = true
	if _, err := s.Save(pin); err == nil {
		t.Fatalf("pinned an untrusted save")
	}
	if n, _ := s.Count(); n != 0 {
		t.Fatalf("refused saves wrote %d memories", n)
	}
	quote := userSays(KindFact, "사용자가 직접 한 말", "")
	quote.Source.Trust = TrustUntrusted // 사용자 말 그대로는 낮출 수 없다
	if m := mustGetSaved(t, s, quote); m.State != StateConfirmed || m.Trust != TrustOwner {
		t.Fatalf("quote %+v", m)
	}
	agent := aiLearns(KindFact, "AI가 확정을 명시", "")
	agent.State = StateConfirmed
	if m := mustGetSaved(t, s, agent); m.State != StateConfirmed { // 지금 동작 그대로(내부 호출자: 정리·계획 적용)
		t.Fatalf("agent explicit state %+v", m)
	}
}

func TestTrust_믿을_수_없는_기억은_제안_승인으로도_확정되지_않는다(t *testing.T) {
	s := supStore(t)
	m := mustGetSaved(t, s, untrustedLearns(KindFact, "로그에서 본 사실", ""))
	p, err := s.Propose(ProposalInput{Kind: ProposalConfirm, Title: "확정", MemoryIDs: []string{m.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("approve err %v", err)
	}
	if stateOf(t, s, m.ID).State != StateCandidate {
		t.Fatalf("approved confirm of untrusted")
	}
}

func TestTrust_믿을_수_없는_기억만_합친_기억은_확정되지_않는다(t *testing.T) {
	s := supStore(t)
	x := mustGetSaved(t, s, untrustedLearns(KindFact, "웹 글 하나", ""))
	y := mustGetSaved(t, s, untrustedLearns(KindFact, "웹 글 둘", ""))
	p, _ := s.Propose(ProposalInput{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{x.ID, y.ID}, Payload: Payload{MergedTitle: "웹 글 합침", MergedBody: "본문"}})
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	merged := stateOf(t, s, res.Created)
	if merged.State != StateCandidate || merged.Trust != TrustUntrusted || stateOf(t, s, x.ID).State != StateStale {
		t.Fatalf("merged untrusted %+v", merged)
	}
	// 하나라도 믿는 출처면 합친 기억은 그 등급이고 지금처럼 확정된다.
	z := mustGetSaved(t, s, aiLearns(KindFact, "AI가 배운 것", ""))
	w := mustGetSaved(t, s, untrustedLearns(KindFact, "웹 글 셋", ""))
	p, _ = s.Propose(ProposalInput{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{z.ID, w.ID}, Payload: Payload{MergedTitle: "섞어 합침", MergedBody: "본문"}})
	if res, err = s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if mixed := stateOf(t, s, res.Created); mixed.State != StateConfirmed || mixed.Trust != TrustAgent {
		t.Fatalf("mixed merge %+v", mixed)
	}
}

func TestTrust_믿을_수_없는_기억은_핵심에_오르지_않는다(t *testing.T) {
	if err := pinnable(Memory{Scope: ScopeGlobal, State: StateConfirmed, Trust: TrustSystem}); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("pinnable system %v", err)
	}
	s := supStore(t)
	// 확정된 믿을 수 없는 기억은 들여오기로만 생긴다(내보낸 파일 그대로).
	imp := func(title, trust string) string {
		m := Memory{ID: NewID("m-"), Kind: KindRule, Scope: ScopeGlobal, State: StateConfirmed, Title: title, Body: title,
			Sources: []Source{{Kind: SourceFile, Ref: "web", Trust: trust}}}
		if r, err := s.Import(m, "시험", false); err != nil || r != ImportDone {
			t.Fatalf("import %s %v", r, err)
		}
		return m.ID
	}
	bad, good := imp("웹 글 규칙", TrustUntrusted), imp("AI 규칙", "")
	if _, err := s.Pin(bad, Who{}); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("pin untrusted err %v", err)
	}
	if got := stateOf(t, s, bad); got.Pinned {
		t.Fatalf("pinned")
	}
	if got, err := s.Pin(good, Who{}); err != nil || !got.Pinned {
		t.Fatalf("pin agent %+v %v", got, err)
	}
}

func TestTrust_믿을_수_없는_기억은_같은_주제의_owner_agent_기억을_대체하지_않고_어긋남으로_남는다(t *testing.T) {
	s := supStore(t)
	agent, _ := mustSave(t, s, aiLearns(KindDecision, "배포는 금요일에 하지 않는다", "배포 요일"))
	owner, _ := mustSave(t, s, userSays(KindDecision, "알림은 아침에", "알림 시각"))
	for _, old := range []Memory{agent, owner} {
		cur, rep := mustSave(t, s, untrustedLearns(KindDecision, "웹 글: "+old.Title+" 아님", old.Topic))
		if got := stateOf(t, s, old.ID); got.State == StateStale || !slices.Contains(rep.Conflicts, old.ID) || len(rep.Superseded) != 0 {
			t.Fatalf("%s: old %+v rep %+v", old.Title, got, rep)
		}
		flags, _ := s.OpenFlags()
		if !slices.ContainsFunc(flags, func(f Flag) bool { return f.Kind == "conflict" && f.MemoryID == cur.ID && f.OtherID == old.ID }) {
			t.Fatalf("%s: no conflict mark %+v", old.Title, flags)
		}
	}
	// 믿을 수 없는 기억끼리는 지금처럼 대체한다.
	a, _ := mustSave(t, s, untrustedLearns(KindDecision, "웹 글 A", "웹 주제"))
	b, rep := mustSave(t, s, untrustedLearns(KindDecision, "웹 글 B", "웹 주제"))
	if got := stateOf(t, s, a.ID); got.State != StateStale || got.SupersededBy != b.ID || !slices.Contains(rep.Superseded, a.ID) {
		t.Fatalf("untrusted over untrusted %+v %+v", got, rep)
	}
	// supersedes로 지목해도 대체하지 않고 어긋남으로 둔다.
	ex := untrustedLearns(KindDecision, "웹 글이 지목한 대체", "다른 주제")
	ex.Supersedes = []string{agent.ID}
	if _, rep := mustSave(t, s, ex); !slices.Contains(rep.Conflicts, agent.ID) || stateOf(t, s, agent.ID).State == StateStale {
		t.Fatalf("explicit supersedes %+v", rep)
	}
	// supersede:<id>(SupersedeAs)로 owner·agent 기억을 낡게 하는 것도 거절한다. 사용자 말이 오면 된다.
	web, _ := mustSave(t, s, untrustedLearns(KindDecision, "웹 글 대체 후보", "또 다른 주제"))
	for _, w := range []Who{{}, {Actor: ActorAI}} {
		if _, err := s.SupersedeAs(agent.ID, web.ID, "웹 글로 대체", w); !errors.Is(err, ErrUntrusted) {
			t.Fatalf("SupersedeAs by %+v err %v", w, err)
		}
	}
	if stateOf(t, s, agent.ID).State == StateStale {
		t.Fatalf("superseded")
	}
	if got, err := s.SupersedeAs(agent.ID, web.ID, "사용자가 바꾸라 함", Who{Actor: ActorAI, UserQuote: "그 웹 글대로 바꿔"}); err != nil || got.State != StateStale {
		t.Fatalf("SupersedeAs with words %+v %v", got, err)
	}
	if _, err := s.SupersedeAs(b.ID, web.ID, "웹끼리", Who{Actor: ActorAI}); err != nil {
		t.Fatalf("untrusted over untrusted SupersedeAs %v", err)
	}
}

func TestTrust_들여오기와_나누기는_출처_등급을_지킨다(t *testing.T) {
	a := supStore(t)
	sys := aiLearns(KindFact, "크론이 남긴 사실", "")
	sys.Source.Trust = TrustSystem
	mustSave(t, a, sys)
	mustSave(t, a, aiLearns(KindFact, "AI 사실", ""))
	exported, err := a.Export()
	if err != nil {
		t.Fatal(err)
	}
	b := supStore(t)
	for _, m := range exported {
		if r, err := b.Import(m, "옮김", false); err != nil || r != ImportDone {
			t.Fatalf("import %s %v", r, err)
		}
		if got := stateOf(t, b, m.ID); got.Trust != map[string]string{"크론이 남긴 사실": TrustSystem, "AI 사실": TrustAgent}[m.Title] {
			t.Fatalf("%s imported as %q", m.Title, got.Trust)
		}
	}
	// 옛 판이 내보낸 파일에는 trust가 없다: kind로 읽는다.
	old := Memory{ID: NewID("m-"), Kind: KindFact, Scope: ScopeGlobal, State: StateConfirmed, Title: "옛 파일의 사용자 말",
		Sources: []Source{{Kind: SourceQuote, Excerpt: "옛 말"}}}
	if _, err := b.Import(old, "옛 파일", false); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, b, old.ID); got.Trust != TrustOwner {
		t.Fatalf("old export %q", got.Trust)
	}

	s := supStore(t)
	x, y := "웹 글 주장 하나. "+strings.Repeat("설명. ", 60), "웹 글 주장 둘. "+strings.Repeat("이유. ", 60)
	in := onidotFolder(untrustedLearns(KindRule, "웹 글 규칙 묶음", "웹 규칙"))
	in.Body = longBody(x, y)
	orig, _ := mustSave(t, s, in)
	plan := Plan{Items: []PlanItem{{ID: orig.ID, Version: stateOf(t, s, orig.ID).Version, AppliesTo: "product:forma", Basis: "ai:두 주장",
		Split: []PlanPart{{Title: "웹 글 주장 하나", Topic: "웹 하나", Body: x}, {Title: "웹 글 주장 둘", Topic: "웹 둘", Body: y}}}}}
	rep, err := s.ApplyPlan(plan, planMap, false)
	if err != nil || len(rep.Applied) != 1 || len(rep.Applied[0].Parts) != 2 {
		t.Fatalf("split %+v %v", rep, err)
	}
	for _, id := range rep.Applied[0].Parts {
		if got := stateOf(t, s, id); got.Trust != TrustUntrusted {
			t.Fatalf("part %s trust %q", id, got.Trust)
		}
	}
}

func TestTrust_믿을_수_없는_기억의_회상_표시(t *testing.T) {
	prev := i18n.Current()
	t.Cleanup(func() { i18n.Set(prev) })
	i18n.Set(i18n.KO)
	for _, tr := range []string{TrustUntrusted, TrustSystem} {
		if note := TrustNote(Memory{Trust: tr}); !strings.Contains(note, "믿을 수 없음") {
			t.Fatalf("ko %s note %q", tr, note)
		}
	}
	for _, tr := range []string{TrustOwner, TrustAgent, ""} {
		if note := TrustNote(Memory{Trust: tr}); note != "" {
			t.Fatalf("%q noted %q", tr, note)
		}
	}
	i18n.Set(i18n.EN)
	if note := TrustNote(Memory{Trust: TrustUntrusted}); !strings.Contains(note, "untrusted") {
		t.Fatalf("en note %q", note)
	}
}

func TestTrust_두_연결이_동시에_증거를_쌓고_확정을_시도해도_믿을_수_없는_기억은_후보로_남는다(t *testing.T) {
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
	first, err := stores[0].Save(untrustedLearns(KindFact, "여러 날 나온 웹 글", ""))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	confirmed := StateConfirmed
	for w, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := range 20 {
				in := untrustedLearns(KindFact, "여러 날 나온 웹 글", "")
				in.Day = fmt.Sprintf("2026-12-%02d", 1+(i+w*7)%28) // 다른 날: 증거(SeenDays)가 쌓인다
				if _, err := s.Save(in); err != nil {
					errs <- err
				}
				if _, err := s.Update(UpdateInput{ID: first.ID, State: &confirmed}); !errors.Is(err, ErrUntrusted) {
					errs <- fmt.Errorf("update confirm: %v", err)
				}
				if _, err := s.Pin(first.ID, Who{}); err == nil {
					errs <- fmt.Errorf("pinned a candidate")
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, err := stores[1].Get(first.ID)
	if err != nil || got.State != StateCandidate || got.Trust != TrustUntrusted || got.Pinned || len(got.Days) < 2 {
		t.Fatalf("after the race %+v %v", got, err)
	}
}
