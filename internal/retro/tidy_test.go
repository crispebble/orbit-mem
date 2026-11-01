package retro

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// tidyEnv는 그래프 정리를 켠 환경에 기억을 심는다.
//
//	orbit 셋(하나는 Orbit 표기), studio 둘, 주제 태그 없는 옛 기억 하나, 일지 하나(정리 대상 아님)
func tidyEnv(t *testing.T, now time.Time) (*env, map[string]memory.Memory) {
	t.Helper()
	e := setup(t, now)
	e.r.Cfg.GraphTidyMemories, e.r.Cfg.GraphTidyUntagged, e.r.Cfg.GraphTidyProposals = 10, 5, 0
	ms := map[string]memory.Memory{}
	at := now.AddDate(0, 0, -5)
	save := func(name, kind string, tags ...string) {
		at = at.Add(time.Minute)
		e.s.Now = func() time.Time { return at }
		m, err := e.s.Save(memory.SaveInput{Kind: kind, Title: name, Body: name + " 본문", Tags: tags, Scope: memory.ScopeGlobal,
			State: memory.StateConfirmed, Source: memory.Source{Kind: memory.SourceSession}})
		if err != nil {
			t.Fatal(err)
		}
		ms[name] = m
	}
	save("회고는 새벽 네 시에 돈다", memory.KindDecision, "orbit")
	save("회고 시각은 설정에서 바꾼다", memory.KindRule, "Orbit")
	save("회고 결과는 제안으로만 반영한다", memory.KindDecision, "orbit", "제안")
	save("화면은 Studio 키트로 만든다", memory.KindRule, "studio")
	save("화면 크기는 토큰으로 정한다", memory.KindRule, "studio")
	save("옛 서버 지식", memory.KindFact, "imported:server-knowledge", "space:onidot")
	save("2026-10-20 일지: 무엇", memory.KindJournal)
	e.s.Now = func() time.Time { return now }
	return e, ms
}

// section is the part of the material under heading, up to the next heading.
func section(text, heading string) string {
	_, rest, ok := strings.Cut(text, heading)
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func pendingOf(t *testing.T, e *env, kind string) []memory.Proposal {
	t.Helper()
	return proposalsOf(t, e, memory.StatusPending, kind)
}

// autoOf is the changes an automation applied without approval (A2), kept as proposals with status auto.
func autoOf(t *testing.T, e *env, kind string) []memory.Proposal {
	t.Helper()
	return proposalsOf(t, e, memory.StatusAuto, kind)
}

func proposalsOf(t *testing.T, e *env, status, kind string) []memory.Proposal {
	t.Helper()
	all, _ := e.s.Proposals(status)
	var out []memory.Proposal
	for _, p := range all {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// 설계 · Orbit 3차 O3-2 「연결 채우기」「주제 태그 없는 기억」: 하루 회고에 클러스터 한 묶음과 주제 태그 없는 기억,
// 주제 태그 목록이 붙는다. 회고가 낸 link·retag 제안이 남고, 다음 하루 회고는 다음 클러스터를 본다.
func TestTidy_하루_회고에_클러스터_한_묶음씩_붙는다(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e, ms := tidyEnv(t, now)
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "회고 시각 확인해 줘"))
	a, b := ms["회고는 새벽 네 시에 돈다"], ms["회고 시각은 설정에서 바꾼다"]
	untagged := ms["옛 서버 지식"]
	var text, report string
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{Summary: "회고 시각", Journal: &JournalIn{Title: "회고 시각 확인", Body: "-"},
			Proposals: []ProposalIn{
				{Kind: "link", Title: "회고 시각의 결정과 바꾸는 법", MemoryIDs: []string{a.ID, b.ID}, Relation: "sequence", Note: "정한 시각을 바꾸는 절차"},
				{Kind: "retag", Title: "옛 서버 지식에 주제 태그", MemoryIDs: []string{untagged.ID}, ToTags: []string{"orbit"}},
			}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil || res.Status != StatusDone {
		t.Fatalf("daily %+v %v", res, err)
	}
	for _, want := range []string{"## 그래프 정리", "orbit", a.ID, b.ID, ms["회고 결과는 제안으로만 반영한다"].ID, "## 주제 태그가 없는 기억", untagged.ID,
		"## 주제 태그 목록", "studio (2)"} {
		if !strings.Contains(text, want) {
			t.Errorf("재료에 %q가 없습니다", want)
		}
	}
	if tidy := section(text, "## 그래프 정리"); strings.Contains(tidy, ms["화면은 Studio 키트로 만든다"].ID) || strings.Contains(tidy, "일지") {
		t.Errorf("다른 클러스터나 일지는 이번 묶음이 아닙니다")
	}
	if t.Failed() {
		t.Fatalf("재료:\n%s", text)
	}
	// 정리(link·retag)는 승인 없이 적용되고 원장에 남는다(A2).
	links, retags := autoOf(t, e, memory.ProposalLink), autoOf(t, e, memory.ProposalRetag)
	if len(links) != 1 || links[0].Payload.Note != "앞뒤 절차: 정한 시각을 바꾸는 절차" || len(retags) != 1 {
		t.Fatalf("제안 link %+v retag %+v (%s)", links, retags, report)
	}

	// 다음 날은 다음 클러스터(studio)다. 태그 달기 제안이 걸린 기억은 다시 싣지 않는다.
	e.r.Now = func() time.Time { return now.AddDate(0, 0, 1) }
	e.talk(t, "2026-10-27", claudeTurn("2026-10-27", "10:00", "user", "화면 크기 확인"))
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "화면", Journal: &JournalIn{Title: "화면", Body: "-"}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-27"); err != nil || res.Status != StatusDone {
		t.Fatalf("daily2 %+v %v", res, err)
	}
	if tidy := section(text, "## 그래프 정리"); !strings.Contains(tidy, ms["화면은 Studio 키트로 만든다"].ID) || strings.Contains(tidy, a.ID) {
		t.Errorf("둘째 날은 studio 묶음이어야 합니다:\n%s", text)
	}
	if strings.Contains(text, "## 주제 태그가 없는 기억") {
		t.Errorf("제안이 걸린 기억은 다시 싣지 않습니다:\n%s", text)
	}
}

// 회당 정리 제안 상한을 넘는 link·retag는 빼고 알린다. 잇는 관계(근거·절차·예외)가 없는 link는 받지 않는다.
// 거절한 쌍은 다시 나오지 않는다.
func TestTidy_상한과_관계와_거절한_쌍(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e, ms := tidyEnv(t, now)
	e.r.Cfg.GraphTidyProposals = 1
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "회고 확인"))
	a, b, c := ms["회고는 새벽 네 시에 돈다"], ms["회고 시각은 설정에서 바꾼다"], ms["회고 결과는 제안으로만 반영한다"]
	old, err := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalLink, Title: "예전 잇기", MemoryIDs: []string{a.ID, c.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Decide(memory.DecideInput{ID: old.ID, Decision: "reject", Note: "관계 없음"}); err != nil {
		t.Fatal(err)
	}
	var report string
	e.ai = func(id string) error {
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{Summary: "회고", Journal: &JournalIn{Title: "회고", Body: "-"},
			Proposals: []ProposalIn{
				{Kind: "link", Title: "같은 태그라서", MemoryIDs: []string{b.ID, c.ID}, Note: "둘 다 orbit"},
				{Kind: "link", Title: "거절한 쌍", MemoryIDs: []string{c.ID, a.ID}, Relation: "basis", Note: "다시"},
				{Kind: "link", Title: "결정과 근거", MemoryIDs: []string{a.ID, b.ID}, Relation: "basis", Note: "같은 결정"},
				{Kind: "retag", Title: "넘치는 태그 달기", MemoryIDs: []string{ms["옛 서버 지식"].ID}, ToTags: []string{"orbit"}},
				{Kind: "direction", Title: "정리와 무관한 제안", Body: "상한에 세지 않는다"},
			}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil || res.Status != StatusDone {
		t.Fatalf("daily %+v %v", res, err)
	}
	links := autoOf(t, e, memory.ProposalLink)
	if len(links) != 1 || links[0].Title != "결정과 근거" {
		t.Errorf("link: %+v", links)
	}
	if len(autoOf(t, e, memory.ProposalRetag)) != 0 || len(pendingOf(t, e, memory.ProposalDirection)) != 1 {
		t.Errorf("상한을 넘은 retag는 빠지고 direction은 남아야 합니다")
	}
	for _, want := range []string{"같은 태그라서: 잇는 관계", "넘치는 태그 달기: 회당 정리 제안 상한 1건"} {
		if !strings.Contains(report, want) {
			t.Errorf("보고에 %q가 없습니다:\n%s", want, report)
		}
	}
}

// 「orb retro tidy」: 대화가 없어도 그래프 정리만 한 회 돈다. 일지는 받지 않는다.
// 하루 회고는 대화가 없으면 정리만으로는 돌지 않는다(정리는 하루 회고에 붙을 뿐이다).
func TestTidy_정리만_한_회_돌리기(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e, ms := tidyEnv(t, now)
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil || res.Status != StatusEmpty || len(e.calls) != 0 {
		t.Fatalf("대화 없는 하루 회고 %+v %v calls=%d", res, err, len(e.calls))
	}
	if _, err := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalRetag, Title: "studio 묶기", Payload: memory.Payload{FromTags: []string{"studio"}, ToTags: []string{"onidot-studio"}}}); err != nil {
		t.Fatal(err)
	}
	var text string
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "정리",
			Proposals: []ProposalIn{{Kind: "retag", Title: "studio 묶기", FromTags: []string{"orbit"}, ToTags: []string{"orbit-cli"}}}})
		return err
	}
	res, err := e.r.RunTidy(context.Background())
	if err != nil || res.Status != StatusDone || res.Kind != KindTidy || len(e.calls) != 1 {
		t.Fatalf("정리 %+v %v calls=%d", res, err, len(e.calls))
	}
	if !strings.Contains(text, "## 그래프 정리") || strings.Contains(text, "## 이미 있는 기억") || !strings.Contains(section(text, "## 주제 태그 목록"), "대기 중인 묶기: studio → onidot-studio") {
		t.Errorf("정리 재료:\n%s", text)
	}
	if st, _ := getStatus(e.s, e.calls[0]); st.Kind != KindTidy {
		t.Errorf("정리 회고여야 합니다: %+v", st)
	}
	retags, applied := pendingOf(t, e, memory.ProposalRetag), autoOf(t, e, memory.ProposalRetag)
	if len(retags) != 1 || len(applied) != 1 || len(applied[0].MemoryIDs) != 3 {
		t.Fatalf("retag: pending %+v applied %+v", retags, applied)
	}
	_ = ms
}

// runTidyOnce runs one tidy and returns its material.
func runTidyOnce(t *testing.T, e *env, sub Submission) (string, Result) {
	t.Helper()
	var text string
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		if sub.Summary == "" {
			sub.Summary = "정리"
		}
		_, err := Submit(e.s, e.r.Paths, id, sub)
		return err
	}
	res, err := e.r.RunTidy(context.Background())
	if err != nil {
		t.Fatalf("정리 %+v %v", res, err)
	}
	return text, res
}

// 검토 지적 1: 클러스터의 마지막 묶음이 기억 하나여도 커서가 멈추지 않는다(앞 묶음과 겹쳐 둘 이상을 보인다).
// 검토 지적 7: 한 바퀴 돈 뒤에는 바뀐 클러스터만 다시 본다. 볼 것이 없으면 호스트를 부르지 않는다.
func TestTidy_마지막_묶음과_한_바퀴_뒤(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e, ms := tidyEnv(t, now)
	e.r.Cfg.GraphTidyMemories, e.r.Cfg.GraphTidyUntagged = 2, 0
	retro := ms["회고 결과는 제안으로만 반영한다"] // orbit 셋 중 마지막(만든 순서)
	text, _ := runTidyOnce(t, e, Submission{})
	if !strings.Contains(section(text, "## 그래프 정리"), "(3개 중 1~2째)") {
		t.Fatalf("첫 묶음:\n%s", text)
	}
	text, _ = runTidyOnce(t, e, Submission{})
	if tidy := section(text, "## 그래프 정리"); !strings.Contains(tidy, "(3개 중 2~3째)") || !strings.Contains(tidy, retro.ID) {
		t.Fatalf("마지막 묶음은 앞과 겹쳐 둘:\n%s", text)
	}
	text, _ = runTidyOnce(t, e, Submission{})
	if !strings.Contains(section(text, "## 그래프 정리"), "studio") {
		t.Fatalf("orbit을 마치면 studio:\n%s", text)
	}
	calls := len(e.calls)
	if res, err := e.r.RunTidy(context.Background()); err != nil || res.Status != StatusEmpty || len(e.calls) != calls {
		t.Fatalf("한 바퀴 뒤 바뀐 것이 없으면 돌지 않는다: %+v %v", res, err)
	}
	if _, err := e.s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "회고 실패는 다음 실행에서 다시 돈다", Tags: []string{"orbit"}, Scope: memory.ScopeGlobal,
		State: memory.StateConfirmed, Source: memory.Source{Kind: memory.SourceSession}}); err != nil {
		t.Fatal(err)
	}
	text, _ = runTidyOnce(t, e, Submission{})
	if !strings.Contains(section(text, "## 그래프 정리"), "orbit") {
		t.Fatalf("기억이 늘어난 클러스터는 다시 본다:\n%s", text)
	}
}

// 검토 지적 6: 연결 후보를 꺼도(graphTidyMemories 0) 태그 달기·묶기 재료(주제 태그 목록)는 보인다.
func TestTidy_연결을_꺼도_태그_목록은_보인다(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e, _ := tidyEnv(t, now)
	e.r.Cfg.GraphTidyMemories = 0
	text, _ := runTidyOnce(t, e, Submission{})
	if strings.Contains(text, "## 그래프 정리:") || !strings.Contains(text, "## 주제 태그가 없는 기억") || !strings.Contains(text, "## 주제 태그 목록") {
		t.Fatalf("재료:\n%s", text)
	}
}

// 검토 지적 5: 정리 제안과 일반 제안을 합쳐 제출 상한을 넘으면 제출 전체를 거부하지 않고 넘친 정리 제안만 뺀다.
func TestTidy_제출_상한을_넘친_정리_제안만_뺀다(t *testing.T) {
	now := time.Date(2026, 10, 27, 4, 0, 0, 0, kst)
	e, ms := tidyEnv(t, now)
	e.r.Cfg.GraphTidyProposals = 20
	e.talk(t, "2026-10-26", claudeTurn("2026-10-26", "10:00", "user", "회고 확인"))
	var props []ProposalIn
	for i := range 3 {
		props = append(props, ProposalIn{Kind: "direction", Title: fmt.Sprintf("방향 %d", i), Body: "근거"})
	}
	for i := range 19 {
		props = append(props, ProposalIn{Kind: "retag", Title: fmt.Sprintf("태그 달기 %d", i), MemoryIDs: []string{ms["옛 서버 지식"].ID}, ToTags: []string{fmt.Sprintf("주제%d", i)}})
	}
	var text, report string
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{Summary: "회고", Journal: &JournalIn{Title: "회고", Body: "-"}, Proposals: props})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-26"); err != nil || res.Status != StatusDone {
		t.Fatalf("daily %+v %v", res, err)
	}
	if len(pendingOf(t, e, memory.ProposalDirection)) != 3 || len(autoOf(t, e, memory.ProposalRetag)) != 17 {
		t.Errorf("방향 %d, 태그 %d (%s)", len(pendingOf(t, e, memory.ProposalDirection)), len(autoOf(t, e, memory.ProposalRetag)), report)
	}
	if !strings.Contains(report, "제출 상한 20건") || !strings.Contains(text, "일반 제안과 합쳐 20건") {
		t.Errorf("보고·안내:\n%s\n%s", report, text)
	}
}
