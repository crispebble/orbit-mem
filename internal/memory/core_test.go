package memory

// 설계 · orbit-mem 2판 4.4·D2, S3 명세 2.6(S3-5): 상시 핵심 승격은 확정된 user·제품 기억만, 층마다 한도 안에서만 된다.
// 넘치면 자르지 않고 거절하며, 거절에 그 층의 핵심 목록과 길이를 싣는다.

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func pinned(t *testing.T, s *Store, id string) bool {
	t.Helper()
	m, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Pinned
}

func TestPin_확정된_user_제품_기억만_승격한다(t *testing.T) {
	s := supStore(t)
	user, _ := mustSave(t, s, userSays(KindRule, "답은 존댓말", "말투"))
	prod := userSays(KindDecision, "왼쪽 얇은 레일 유지", "레일")
	prod.Scope = "product:orbit-code-desktop"
	pm, _ := mustSave(t, s, prod)
	cand, _ := mustSave(t, s, aiLearns(KindRule, "AI가 배운 규칙", "배운 것"))
	repo := userSays(KindRule, "이 저장소는 make로 빌드", "빌드")
	repo.Scope, repo.Project = "project:github.com/a/b", "github.com/a/b"
	rm, _ := mustSave(t, s, repo)
	unknown := userSays(KindRule, "제품 미정 규칙", "미정")
	unknown.Scope, unknown.Project = "product:?", "/x"
	um, _ := mustSave(t, s, unknown)

	for _, id := range []string{user.ID, pm.ID} {
		m, err := s.Pin(id, Who{})
		if err != nil || !m.Pinned {
			t.Fatalf("pin %s: %v %v", id, m.Pinned, err)
		}
		h, _ := s.History(id)
		if h[len(h)-1].Action != "pin" {
			t.Fatalf("history %+v", h[len(h)-1])
		}
	}
	for _, id := range []string{cand.ID, rm.ID, um.ID} {
		if _, err := s.Pin(id, Who{}); err == nil || pinned(t, s, id) {
			t.Fatalf("pinned %s: %v", id, err)
		}
	}
	if m, err := s.Unpin(user.ID, Who{}); err != nil || m.Pinned {
		t.Fatalf("unpin: %v", err)
	}
	if h, _ := s.History(user.ID); h[len(h)-1].Action != "unpin" {
		t.Fatalf("unpin history %+v", h[len(h)-1])
	}
}

func TestPin_한도를_넘으면_거절하고_핵심_목록과_길이를_싣는다(t *testing.T) {
	s := supStore(t)
	body := strings.Repeat("가", 900)
	var ids []string
	for i, title := range []string{"규칙 하나", "규칙 둘", "규칙 셋", "규칙 넷"} {
		in := userSays(KindRule, title, "주제 "+title)
		in.Body = body
		m, _ := mustSave(t, s, in)
		ids = append(ids, m.ID)
		_, err := s.Pin(m.ID, Who{})
		if i < 3 && err != nil {
			t.Fatalf("pin %d: %v", i, err)
		}
		if i == 3 {
			var full *CoreFullError
			if !errors.As(err, &full) {
				t.Fatalf("4th pin: %v", err)
			}
			if full.Layer != ScopeGlobal || full.Limit != UserCoreRunes || len(full.Items) != 3 || full.Need != CoreRunes(mustGet(t, s, m.ID)) || full.Used <= 2*900 {
				t.Fatalf("refusal %+v", full)
			}
			msg := err.Error()
			for _, want := range []string{ids[0], "규칙 하나", "3,000", "unpin"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("message lacks %q:\n%s", want, msg)
				}
			}
			if pinned(t, s, m.ID) {
				t.Fatal("pinned over the limit")
			}
		}
	}
	// 제품 핵심은 제품마다 따로 2,000자다.
	for i, product := range []string{"product:a", "product:a", "product:b"} {
		in := userSays(KindDecision, "결정 "+string(rune('가'+i)), "결정 주제 "+string(rune('가'+i)))
		in.Scope, in.Body = product, body
		m, _ := mustSave(t, s, in)
		if _, err := s.Pin(m.ID, Who{}); err != nil {
			t.Fatalf("product pin %d: %v", i, err)
		}
	}
	in := userSays(KindDecision, "결정 넘침", "넘침")
	in.Scope, in.Body = "product:a", body
	m, _ := mustSave(t, s, in)
	var full *CoreFullError
	if _, err := s.Pin(m.ID, Who{}); !errors.As(err, &full) || full.Limit != ProductCoreRunes || full.Layer != "product:a" {
		t.Fatalf("product over: %v", err)
	}
	core, err := s.Core("product:a")
	if err != nil || len(core) != 2 {
		t.Fatalf("core %d %v", len(core), err)
	}
	if u := CoreUsed(core); u > ProductCoreRunes {
		t.Fatalf("core used %d", u)
	}
}

func mustGet(t *testing.T, s *Store, id string) Memory {
	t.Helper()
	m, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSave_pin이_넘치면_아무것도_저장하지_않는다(t *testing.T) {
	s := supStore(t)
	big := userSays(KindRule, "큰 핵심", "큰")
	big.Body = strings.Repeat("나", 2900)
	m, _ := mustSave(t, s, big)
	if _, err := s.Pin(m.ID, Who{}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.List()
	in := userSays(KindRule, "하나 더", "하나 더")
	in.Body, in.Pin = strings.Repeat("다", 200), true
	var full *CoreFullError
	if _, _, err := s.SaveWith(in); !errors.As(err, &full) {
		t.Fatalf("save pin over: %v", err)
	}
	if after, _ := s.List(); len(after) != len(before) {
		t.Fatalf("saved %d → %d", len(before), len(after))
	}
	// 후보는 pin으로 저장할 수 없다(확정이 먼저다).
	ai := aiLearns(KindLesson, "배운 것", "")
	ai.Pin = true
	if _, _, err := s.SaveWith(ai); err == nil {
		t.Fatal("candidate saved with pin")
	}
	// 한도 안이면 저장과 함께 승격된다.
	ok := userSays(KindRule, "짧은 핵심", "짧은")
	ok.Pin = true
	got, _, err := s.SaveWith(ok)
	if err != nil || !got.Pinned {
		t.Fatalf("save pin: %v %v", got.Pinned, err)
	}
}

func TestSave_대체한_새_판이_승격을_이어받는다(t *testing.T) {
	s := supStore(t)
	old, _ := mustSave(t, s, userSays(KindDecision, "상단바는 레일에", "상단바"))
	if _, err := s.Pin(old.ID, Who{}); err != nil {
		t.Fatal(err)
	}
	next, rep := mustSave(t, s, userSays(KindDecision, "상단바는 제목 막대에", "상단바"))
	if len(rep.Superseded) != 1 || !next.Pinned || rep.Unpinned != "" {
		t.Fatalf("next pinned %v rep %+v", next.Pinned, rep)
	}
	// 새 판이 한도를 넘으면 저장은 되고 핵심에서 빠졌다고 알린다.
	filler := userSays(KindRule, "채우는 규칙", "채움")
	filler.Body = strings.Repeat("라", 2800)
	fm, _ := mustSave(t, s, filler)
	if _, err := s.Pin(fm.ID, Who{}); err != nil {
		t.Fatal(err)
	}
	long := userSays(KindDecision, "상단바는 둘 다", "상단바")
	long.Body = strings.Repeat("마", 500)
	last, rep := mustSave(t, s, long)
	if last.Pinned || rep.Unpinned != next.ID {
		t.Fatalf("over limit: pinned %v rep %+v", last.Pinned, rep)
	}
}

func TestCoreRunes_세션_시작_꼴의_글자_수다(t *testing.T) {
	m := Memory{ID: "m-000000000000", Title: "제목", Body: "본문\n둘째 줄"}
	if got, want := CoreRunes(m), utf8.RuneCountInString(CoreEntry(m)); got != want || !strings.Contains(CoreEntry(m), "  둘째 줄") {
		t.Fatalf("%d %d %q", got, want, CoreEntry(m))
	}
}

// 검토 지적(2026-11-04): 핵심은 승격 경로 밖에서도 지켜진다. 옮기거나 늘리거나 내리면 핵심에서 빠진다.
func TestPin_옮기거나_늘리거나_내리면_핵심에서_빠진다(t *testing.T) {
	s := supStore(t)
	pinnedOne := func(title, scope, body string) Memory {
		in := userSays(KindDecision, title, "주제 "+title)
		in.Scope, in.Body, in.Pin = scope, body, true
		m, _ := mustSave(t, s, in)
		if !m.Pinned {
			t.Fatalf("%s not pinned", title)
		}
		return m
	}
	half := strings.Repeat("가", 930)
	pinnedOne("알파 하나", "product:alpha", half)
	pinnedOne("알파 둘", "product:alpha", half)
	beta := pinnedOne("베타 하나", "product:beta", half)
	// 다른 제품 층으로 옮기면 그 층의 한도를 넘으므로 핵심에서 빠진다.
	to := "product:alpha"
	m, err := s.Update(UpdateInput{ID: beta.ID, Scope: &to, Who: Who{}})
	if err != nil || m.Pinned || CoreUsed(mustCore(t, s, "product:alpha")) > ProductCoreRunes {
		t.Fatalf("moved: pinned %v %v", m.Pinned, err)
	}
	// 본문을 한도 넘게 늘려도 빠진다.
	user := pinnedOne("사용자 핵심", ScopeGlobal, "짧다")
	long := strings.Repeat("나", 3100)
	if m, err := s.Update(UpdateInput{ID: user.ID, Body: &long, Who: Who{}}); err != nil || m.Pinned {
		t.Fatalf("grown: %v %v", m.Pinned, err)
	}
	// 후보로 되돌리거나 낡음으로 돌리면 빠지고, 다시 확정해도 저절로 돌아오지 않는다.
	other := pinnedOne("사용자 핵심 둘", ScopeGlobal, "짧다")
	cand := StateCandidate
	if m, err := s.Update(UpdateInput{ID: other.ID, State: &cand, Who: Who{}}); err != nil || m.Pinned {
		t.Fatalf("restored to candidate: %v %v", m.Pinned, err)
	}
	for _, c := range mustCore(t, s, ScopeGlobal) {
		if c.ID == other.ID {
			t.Fatal("candidate still in the core")
		}
	}
}

func mustCore(t *testing.T, s *Store, scope string) []Memory {
	t.Helper()
	c, err := s.Core(scope)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// 사용자 말을 담은 기억은 AI가 사용자 말 없이 올리거나 내리지 못한다(A2).
func TestPin_사용자_말_기억은_AI가_사용자_말_없이_올리고_내리지_못한다(t *testing.T) {
	s := supStore(t)
	m, _ := mustSave(t, s, userSays(KindRule, "답은 존댓말", "말투"))
	if _, err := s.Pin(m.ID, Who{Actor: ActorAI}); !errors.Is(err, ErrProtected) {
		t.Fatalf("AI pin without quote: %v", err)
	}
	if got, err := s.Pin(m.ID, Who{Actor: ActorAI, UserQuote: "늘 지켜"}); err != nil || !got.Pinned {
		t.Fatalf("AI pin with quote: %v", err)
	}
	if _, err := s.Unpin(m.ID, Who{Actor: ActorAI}); !errors.Is(err, ErrProtected) || !pinned(t, s, m.ID) {
		t.Fatalf("AI unpin without quote: %v", err)
	}
	// AI가 배운 확정 기억(사용자 말이 아님)은 AI가 올리고 내린다.
	ai := aiLearns(KindFact, "빌드는 make", "")
	ai.State = StateConfirmed
	am, _ := mustSave(t, s, ai)
	if got, err := s.Pin(am.ID, Who{Actor: ActorAI}); err != nil || !got.Pinned {
		t.Fatalf("AI pin of AI memory: %v", err)
	}
}
