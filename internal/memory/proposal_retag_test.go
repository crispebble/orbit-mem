package memory

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// 끝 기준 2: 뜻이 같은 태그를 묶는 retag 제안. 대상을 주지 않으면 그 태그(표기 차이 무시)를 가진 기억이 대상이다.
// 승인하면 대상 기억마다 새 판이 쌓이고, 원장 되돌리기로 옛 태그로 돌아온다.
func TestRetag_뜻이_같은_태그를_묶고_원장으로_되돌린다(t *testing.T) {
	s, _ := newTestStore(t)
	a := taggedMemory(t, s, "A 기억", "onidot-studio", "ui")
	b := taggedMemory(t, s, "B 기억", "Onidot_Studio")
	c := taggedMemory(t, s, "C 기억", "studio", "onidot-studio")
	d := taggedMemory(t, s, "D 기억", "orbit")
	p, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "onidot-studio를 studio로", Body: "같은 제품을 가리킵니다", RetroID: "r-1",
		Payload: Payload{FromTags: []string{"onidot-studio"}, ToTags: []string{"studio"}}})
	if err != nil {
		t.Fatal(err)
	}
	ids := append([]string(nil), p.MemoryIDs...)
	if len(ids) != 3 || !strings.Contains(p.Body, "onidot-studio") || !strings.Contains(p.Body, a.ID) {
		t.Fatalf("제안: %+v", p)
	}
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{a.ID: {"studio", "ui"}, b.ID: {"studio"}, c.ID: {"studio"}, d.ID: {"orbit"}}
	for id, tags := range want {
		got, _ := s.Get(id)
		if !reflect.DeepEqual(got.Tags, tags) {
			t.Errorf("%s 태그: %v, 바라는 것 %v", id, got.Tags, tags)
		}
	}
	if len(res.Changed) != 3 {
		t.Errorf("바뀐 기억: %v", res.Changed)
	}
	hist, _ := s.History(a.ID)
	if len(hist) != 2 || hist[1].Action != "retag" || !reflect.DeepEqual(hist[0].Memory.Tags, []string{"onidot-studio", "ui"}) {
		t.Errorf("옛 판이 남아야 합니다: %+v", hist)
	}
	e, err := s.LedgerEntry(res.LedgerID)
	if err != nil {
		t.Fatal(err)
	}
	if e.ProposalKind != ProposalRetag || len(e.Before.Memories) != 3 || !strings.Contains(e.Change, "studio") {
		t.Errorf("원장: %+v", e)
	}
	rv, err := s.Propose(ProposalInput{Kind: ProposalRevert, Title: "되돌리기", Payload: Payload{LedgerID: res.LedgerID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: rv.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	for id, tags := range map[string][]string{a.ID: {"onidot-studio", "ui"}, b.ID: {"Onidot_Studio"}, c.ID: {"studio", "onidot-studio"}} {
		got, _ := s.Get(id)
		if !reflect.DeepEqual(got.Tags, tags) {
			t.Errorf("되돌린 %s 태그: %v, 바라는 것 %v", id, got.Tags, tags)
		}
	}
}

// 끝 기준 2: 주제 태그가 없는 기억에 태그를 다는 retag. 표식 태그는 그대로 두고, 이미 있는 태그(표기만 다름)는 겹쳐 달지 않는다.
func TestRetag_주제_태그가_없는_기억에_태그를_단다(t *testing.T) {
	s, _ := newTestStore(t)
	m := taggedMemory(t, s, "옛 기억", "imported:server-knowledge", "space:onidot", "UI")
	p, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "주제 태그 달기", MemoryIDs: []string{m.ID}, Payload: Payload{ToTags: []string{"orbit", "ui"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(m.ID)
	if !reflect.DeepEqual(got.Tags, []string{"imported:server-knowledge", "space:onidot", "UI", "orbit"}) {
		t.Errorf("태그: %v", got.Tags)
	}
}

// 태그는 10개까지다. 달 태그가 밀려나지 않게 기존 태그 끝에서 덜어 낸다.
func TestRetag_태그_상한에서도_새_태그가_남는다(t *testing.T) {
	s, _ := newTestStore(t)
	full := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"}
	m := taggedMemory(t, s, "가득 찬 기억", full...)
	p, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "태그 달기", MemoryIDs: []string{m.ID}, Payload: Payload{ToTags: []string{"orbit"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(m.ID)
	if len(got.Tags) != 10 || got.Tags[9] != "orbit" {
		t.Errorf("태그: %v", got.Tags)
	}
}

// 바뀌는 것이 없거나 바꿀 태그가 없으면 받지 않는다. 고쳐서 승인할 수 없다. 거절한 묶기는 다시 나오지 않는다.
func TestRetag_입력과_거절(t *testing.T) {
	s, _ := newTestStore(t)
	a := taggedMemory(t, s, "A 기억", "studio")
	b := taggedMemory(t, s, "B 기억", "onidot-studio")
	cases := []ProposalInput{
		{Kind: ProposalRetag, Title: "빈 것", MemoryIDs: []string{a.ID}},
		{Kind: ProposalRetag, Title: "이미 있음", MemoryIDs: []string{a.ID}, Payload: Payload{ToTags: []string{"Studio"}}},
		{Kind: ProposalRetag, Title: "가진 기억 없음", Payload: Payload{FromTags: []string{"없는-태그"}, ToTags: []string{"x"}}},
		{Kind: ProposalRetag, Title: "대상 없음", Payload: Payload{ToTags: []string{"x"}}},
	}
	for _, in := range cases {
		if _, err := s.Propose(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", in.Title, err)
		}
	}
	in := ProposalInput{Kind: ProposalRetag, Title: "studio로", Payload: Payload{FromTags: []string{"onidot-studio"}, ToTags: []string{"studio"}}}
	p, err := s.Propose(in)
	if err != nil {
		t.Fatal(err)
	}
	title := "다른 제목"
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve", Title: &title}); !errors.Is(err, ErrInvalid) {
		t.Errorf("고쳐서 승인: %v", err)
	}
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "reject", Note: "다른 것"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(b.ID); !reflect.DeepEqual(got.Tags, []string{"onidot-studio"}) || got.Version != b.Version {
		t.Errorf("거절했는데 바뀜: %+v", got)
	}
	// 표기만 달라도, 새 기억이 그 태그를 갖게 되어도 거절한 묶기는 다시 나오지 않는다.
	taggedMemory(t, s, "C 기억", "Onidot_Studio")
	again := ProposalInput{Kind: ProposalRetag, Title: "다시", Payload: Payload{FromTags: []string{"ONIDOT_studio"}, ToTags: []string{"Studio"}}}
	if _, err := s.Propose(again); !errors.Is(err, ErrDuplicateProposal) {
		t.Errorf("거절한 묶기: %v", err)
	}
}

// O3-1 묶음 결정에 그대로 들어간다.
func TestRetag_묶음_결정(t *testing.T) {
	s, _ := newTestStore(t)
	a := taggedMemory(t, s, "A 기억", "imported:server-knowledge")
	b := taggedMemory(t, s, "B 기억", "since:2026-01")
	var ids []string
	for _, m := range []Memory{a, b} {
		p, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "태그 달기 " + m.Title, MemoryIDs: []string{m.ID}, Payload: Payload{ToTags: []string{"orbit"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	res := s.DecideMany(ids, "approve", "")
	if len(res.Decided) != 2 || len(res.Failed) != 0 {
		t.Fatalf("묶음 결정: %+v", res)
	}
	for _, m := range []Memory{a, b} {
		if got, _ := s.Get(m.ID); got.Tags[len(got.Tags)-1] != "orbit" {
			t.Errorf("%s: %v", m.ID, got.Tags)
		}
	}
}

// 기다리는 사이 다른 길로 이미 바뀌어 할 일이 없으면 정리(settled)로 닫는다.
func TestRetag_이미_이뤄졌으면_정리로_닫는다(t *testing.T) {
	s, _ := newTestStore(t)
	m := taggedMemory(t, s, "A 기억", "onidot-studio")
	p, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "studio로", Payload: Payload{FromTags: []string{"onidot-studio"}, ToTags: []string{"studio"}}})
	if err != nil {
		t.Fatal(err)
	}
	tags := []string{"studio"}
	if _, err := s.Update(UpdateInput{ID: m.ID, Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CloseSettled(); err != nil || n != 1 {
		t.Fatalf("정리: %d %v", n, err)
	}
	if got, _ := s.GetProposal(p.ID); got.Status != StatusSettled {
		t.Errorf("상태: %s", got.Status)
	}
}

// 같은 태그를 이미 다른 대기 묶기가 빼고 있으면 뺄 태그 구성만 달리한 묶기를 다시 받지 않는다
// (첫 실행 실측: 「교정」 묶기가 뺄 태그를 조금 달리해 두 번 나왔다).
func TestRetag_뺄_태그가_겹치는_대기_묶기는_다시_받지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	taggedMemory(t, s, "A 기억", "교정", "correction")
	taggedMemory(t, s, "B 기억", "user-correction")
	if _, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "교정 묶기", Payload: Payload{FromTags: []string{"교정", "correction", "user-correction"}, ToTags: []string{"사용자교정"}}}); err != nil {
		t.Fatal(err)
	}
	again := ProposalInput{Kind: ProposalRetag, Title: "교정 묶기 다시", Payload: Payload{FromTags: []string{"Correction", "교정"}, ToTags: []string{"사용자교정"}}}
	if _, err := s.Propose(again); !errors.Is(err, ErrDuplicateProposal) {
		t.Errorf("겹치는 묶기: %v", err)
	}
	// 겹치지 않는 묶기와 태그 달기는 받는다.
	taggedMemory(t, s, "C 기억", "hooks")
	if _, err := s.Propose(ProposalInput{Kind: ProposalRetag, Title: "훅 묶기", Payload: Payload{FromTags: []string{"hooks"}, ToTags: []string{"훅"}}}); err != nil {
		t.Errorf("다른 묶기: %v", err)
	}
}
