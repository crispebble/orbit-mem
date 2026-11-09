package inject

// 1-6 b6r(사용자 결정): 1-4가 뺀 세션 시작 「최근 결정」 줄을 제목 줄로만 다시 넣는다. 고르는 법은 87ab0fb의 세션 시작
// 「최근 결정」 줄(이 폴더에서 보이는 확정 결정, 핵심 층·승격·분야 지식 밖, 최근에 고친 것부터 recentDecisions개)을 따르고,
// 줄 하나는 maxDecisionRunes 안이다. 본문은 넣지 않는다. 이 줄의 기억은 세션 묶음(A층) id라 요청 회상(B1)에서 빠진다.
// 묶음 전체는 MaxSessionStartRunes 안이고 세션 내내 바이트가 같다(T-20).

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const recentHead = "[" + Label + " 기억 · 최근 결정]"

// tick moves the store clock on so the next save is the newest.
func tick(s *memory.Store) {
	now := s.Now().Add(time.Minute)
	s.Now = func() time.Time { return now }
}

func TestA층_최근_결정_줄은_확정_결정의_제목과_id만_최근_것부터_넣는다(t *testing.T) {
	s := store(t)
	var decs []memory.Memory
	for i := 0; i < 7; i++ {
		tick(s)
		decs = append(decs, save(t, s, memory.KindDecision, fmt.Sprintf("결정 %d 제목", i), fmt.Sprintf("결정 본문 %d", i), memory.ScopeGlobal, true))
	}
	tick(s)
	cand := save(t, s, memory.KindDecision, "후보 결정", "", memory.ScopeGlobal, false)
	other := save(t, s, memory.KindDecision, "다른 앱 결정", "", "project:github.com/me/other", true)
	rule := save(t, s, memory.KindRule, "확정 규칙", "", memory.ScopeGlobal, true)
	pinned := save(t, s, memory.KindDecision, "핵심 결정", "핵심 본문", memory.ScopeGlobal, true)
	if _, err := s.Pin(pinned.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	in := SessionStartInjection(s, cfg, here)
	var line string
	for _, l := range strings.Split(in.Text, "\n") {
		if strings.HasPrefix(l, recentHead) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no recent decisions line:\n%s", in.Text)
	}
	// 기본 recentDecisions(5)개: 최근에 고친 결정 6·5·4·3·2 순서.
	last := -1
	for i := 6; i >= 2; i-- {
		entry := fmt.Sprintf("%s(%s)", decs[i].Title, decs[i].ID)
		at := strings.Index(line, entry)
		if at < 0 || at < last {
			t.Fatalf("entry %q missing or out of order in %q", entry, line)
		}
		last = at
	}
	for _, bad := range []memory.Memory{decs[1], decs[0], cand, other, rule} {
		if strings.Contains(in.Text, bad.ID) {
			t.Fatalf("%q in bundle:\n%s", bad.Title, in.Text)
		}
	}
	if strings.Contains(line, "결정 본문") || strings.Contains(line, pinned.ID) {
		t.Fatalf("body or core decision in the line: %q", line)
	}
	if n := runes(line); n > maxDecisionRunes {
		t.Fatalf("line %d runes > %d", n, maxDecisionRunes)
	}
	for i := 2; i <= 6; i++ {
		if !contains(in.MemoryIDs, decs[i].ID) {
			t.Fatalf("decision %d not among the bundle ids %v", i, in.MemoryIDs)
		}
	}
	// 줄의 결정은 묶음에 들었으니 요청 회상 후보가 아니다(세션당 한 번).
	_, why, err := CandidatesAt(s, at(cfg, here), Options{}, in.MemoryIDs)
	if err != nil {
		t.Fatal(err)
	}
	if why[decs[6].ID] != "session_rule" || why[decs[1].ID] == "session_rule" {
		t.Fatalf("recall exclusion: %q %q", why[decs[6].ID], why[decs[1].ID])
	}
}

func TestA층_최근_결정_줄은_긴_제목을_잘라_한도_안에_둔다(t *testing.T) {
	s := store(t)
	long := strings.Repeat("긴 결정 제목 ", 20) // 제목 한도(200자) 가까이
	for i := 0; i < 5; i++ {
		tick(s)
		save(t, s, memory.KindDecision, fmt.Sprintf("%d %s", i, long), "", memory.ScopeGlobal, true)
	}
	in := SessionStartInjection(s, config.Default(), here)
	var line string
	for _, l := range strings.Split(in.Text, "\n") {
		if strings.HasPrefix(l, recentHead) {
			line = l
		}
	}
	if line == "" || runes(line) > maxDecisionRunes || strings.Count(line, "(m-") != 5 {
		t.Fatalf("line %d runes, %d entries: %q", runes(line), strings.Count(line, "(m-"), line)
	}
}

func TestA층_최근_결정_수가_0이면_줄이_없다(t *testing.T) {
	s := store(t)
	dec := save(t, s, memory.KindDecision, "Orbit 먼저 만든다", "", memory.ScopeGlobal, true)
	cfg := config.Default()
	cfg.RecentDecisions = 0
	if in := SessionStartInjection(s, cfg, here); strings.Contains(in.Text, recentHead) || strings.Contains(in.Text, dec.ID) {
		t.Fatalf("recentDecisions=0 still lists:\n%s", in.Text)
	}
}

func TestA층_최근_결정_줄이_든_묶음은_세션_내내_바이트가_같다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindDecision, "첫 결정", "", memory.ScopeGlobal, true)
	cfg := config.Default()
	first := SessionBundle(s, cfg, here, "claude", "s1")
	tick(s)
	newer := save(t, s, memory.KindDecision, "뒤에 생긴 결정", "", memory.ScopeGlobal, true)
	again := SessionBundle(s, cfg, here, "claude", "s1")
	if again.Text != first.Text || strings.Contains(again.Text, newer.ID) {
		t.Fatalf("bundle changed within the session:\n%s\n---\n%s", first.Text, again.Text)
	}
	if !strings.Contains(first.Text, recentHead) {
		t.Fatalf("no recent decisions line:\n%s", first.Text)
	}
}

func TestA층_핵심이_한도를_채우면_최근_결정_줄은_전체_한도_안에서만_쓴다(t *testing.T) {
	s := store(t)
	save(t, s, memory.KindDecision, "결정 하나", "", memory.ScopeGlobal, true)
	// limit 안에 핵심만 겨우 드는 꼴을 흉내: 작은 limit으로 sessionStart를 부른다.
	in := sessionStart(s, config.Default(), here, 60)
	if in.Chars > 60 || strings.Contains(in.Text, recentHead) {
		t.Fatalf("chars %d:\n%s", in.Chars, in.Text)
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
