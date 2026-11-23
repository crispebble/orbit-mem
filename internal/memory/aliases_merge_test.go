package memory

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// 합치기는 합치는 기억들의 별칭을 합집합으로 잇는다(정규화 기준 중복 없이, 최대 개수까지 앞에서부터). 같은 제목으로 합쳐 그
// 기억에 쌓을 때도 같다.
func TestProposal_합치기는_별칭을_합집합으로_잇는다(t *testing.T) {
	s, _ := newTestStore(t)
	save := func(title string, aliases ...string) Memory {
		m, err := s.Save(SaveInput{Kind: KindRule, Title: title, Body: title + " 본문", Aliases: aliases, Scope: ScopeGlobal, State: StateConfirmed,
			Source: Source{Kind: SourceSession, Ref: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a := save("첫째 규칙", "릴리스", "Go-Live")
	b := save("둘째 규칙", "go-live", "출시")
	p, _ := s.Propose(ProposalInput{Kind: ProposalMerge, Title: "합치기", MemoryIDs: []string{a.ID, b.ID},
		Payload: Payload{MergedTitle: "새 제목의 규칙", MergedBody: "합친 본문"}})
	res, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if m := getAliased(t, s, res.Created); strings.Join(m.Aliases, "|") != "릴리스|Go-Live|출시" {
		t.Fatalf("합친 기억 별칭 = %v", m.Aliases)
	}

	c := save("셋째 규칙", "가나", "다라")
	d := save("셋째 규칙을 잇는 규칙", "마바")
	p, _ = s.Propose(ProposalInput{Kind: ProposalMerge, Title: "같은 제목으로 합치기", MemoryIDs: []string{c.ID, d.ID},
		Payload: Payload{MergedTitle: "셋째 규칙", MergedBody: "합친 본문"}})
	if _, err := s.Decide(DecideInput{ID: p.ID, Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	if m := getAliased(t, s, c.ID); strings.Join(m.Aliases, "|") != "가나|다라|마바" {
		t.Fatalf("같은 제목 합치기 별칭 = %v", m.Aliases)
	}

	// 넘치면 앞에서부터 최대 개수까지.
	var many []string
	for i := 0; i < MaxAliases; i++ {
		many = append(many, fmt.Sprintf("긴 별칭 %02d", i))
	}
	e := save("넷째 규칙", many...)
	f := save("다섯째 규칙", "넘치는 별칭")
	p, _ = s.Propose(ProposalInput{Kind: ProposalMerge, Title: "넘치게 합치기", MemoryIDs: []string{e.ID, f.ID},
		Payload: Payload{MergedTitle: "넷째와 다섯째", MergedBody: "합친 본문"}})
	res, err = s.Decide(DecideInput{ID: p.ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if m := getAliased(t, s, res.Created); len(m.Aliases) != MaxAliases || m.Aliases[0] != "긴 별칭 00" || m.Aliases[MaxAliases-1] != "긴 별칭 09" {
		t.Fatalf("넘친 합치기 별칭 = %v", m.Aliases)
	}
}

// 사용자 말로 확정된 기억의 별칭은 사용자 말 없이 AI가 바꾸지 못한다(본문·태그와 같은 보호).
func TestUpdate_사용자_말_기억의_별칭은_AI가_말_없이_바꾸지_못한다(t *testing.T) {
	s, _ := newTestStore(t)
	q, err := s.Save(SaveInput{Kind: KindRule, Title: "커밋은 경로를 명시한다", Scope: ScopeGlobal, Aliases: []string{"커밋 규칙"},
		Source: Source{Kind: SourceQuote, Excerpt: "커밋은 경로를 명시해"}})
	if err != nil {
		t.Fatal(err)
	}
	next := []string{"바꾼 별칭"}
	if _, err := s.Update(UpdateInput{ID: q.ID, Aliases: &next, Who: Who{Actor: ActorAI}, Sources: []Source{{Kind: SourceSession, Ref: "s2"}}}); !errors.Is(err, ErrProtected) {
		t.Fatalf("AI update without the user's words = %v, want ErrProtected", err)
	}
	if got := getAliased(t, s, q.ID); strings.Join(got.Aliases, "|") != "커밋 규칙" {
		t.Fatalf("aliases changed: %v", got.Aliases)
	}
	if _, err := s.Update(UpdateInput{ID: q.ID, Aliases: &next, Who: Who{Actor: ActorAI, UserQuote: "별칭을 바꿔 줘"}, Sources: []Source{{Kind: SourceSession, Ref: "s3"}}}); err != nil {
		t.Fatalf("with the user's words: %v", err)
	}
}

// 제어 문자가 든 별칭은 거절한다(태그는 제어 문자를 받지만 별칭은 색인과 응답에 그대로 실리므로 막는다). 공백류(탭·줄바꿈)는
// 공백 하나로 접는다.
func TestSave_제어_문자가_든_별칭은_거절한다(t *testing.T) {
	s, _ := newTestStore(t)
	for _, a := range []string{"앞\x00뒤", "벨\a", "이스케이프\x1b[0m"} {
		_, err := s.Save(SaveInput{Kind: KindFact, Title: "제어 문자 시험", Scope: ScopeGlobal, Aliases: []string{a},
			Source: Source{Kind: SourceSession, Ref: "x"}})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", a, err)
		}
	}
	got, err := cleanAliases([]string{"앞\t뒤\n끝"})
	if err != nil || len(got) != 1 || got[0] != "앞 뒤 끝" {
		t.Fatalf("whitespace fold = %q %v", got, err)
	}
}

// 가져오기는 다른 곳에서 쓴 파일을 읽으므로 비밀값이나 제어 문자가 든 별칭은 들이지 않는다(기억 자체는 들인다).
func TestImport_비밀값과_제어_문자가_든_별칭은_들이지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	m := Memory{ID: "m-importalias1", Kind: KindFact, Scope: ScopeGlobal, State: StateCandidate, Title: "가져온 기억", Version: 1,
		Aliases: []string{"정상 별칭", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789", "제어\x00문자"}}
	if res, err := s.Import(m, "옮김", false); err != nil || res != ImportDone {
		t.Fatalf("import = %q %v", res, err)
	}
	if got := getAliased(t, s, m.ID); strings.Join(got.Aliases, "|") != "정상 별칭" {
		t.Fatalf("imported aliases = %v", got.Aliases)
	}
}
