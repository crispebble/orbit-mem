package mcpserver

// 조회 경로 표시(F-3·F-4·F-5): memory_find·memory_get·연결이 현재 값이 아닌 기억을 현재 값처럼 읽히게 하지 않는다.
// 판정은 memory.Viewer 한 곳이 하고 이 파일은 표시만 본다.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// quoteSave is a memory made of the user's own words; the topic puts it on one chain with its versions.
func quoteSave(t *testing.T, s *memory.Store, title, topic string, ttlDays int) memory.Memory {
	t.Helper()
	in := memory.SaveInput{Kind: memory.KindRule, Title: title, Topic: topic,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}, Who: memory.Who{Actor: memory.ActorAI}}
	if ttlDays > 0 {
		in.Retention, in.TTLDays = memory.RetentionVolatile, ttlDays
	}
	m, _, err := s.SaveWith(in)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func advance(s *memory.Store, d time.Duration) {
	at := s.Now().Add(d)
	s.Now = func() time.Time { return at }
}

func TestMemoryFind_기본은_만료_휘발과_주제의_옛_버전을_뺀다(t *testing.T) {
	cs, s := connect(t)
	base := quoteSave(t, s, "기본은 A", "기본값", 0)
	tmp := quoteSave(t, s, "이번 주만 B", "기본값", 1)
	find := func() map[string]FindItem {
		var out FindOut
		call(t, cs, "memory_find", map[string]any{"scope": "all"}, &out)
		got := map[string]FindItem{}
		for _, it := range out.Items {
			got[it.ID] = it
		}
		return got
	}
	// 임시 값이 머리인 동안 주제의 옛 값은 빠진다.
	got := find()
	if _, ok := got[tmp.ID]; !ok {
		t.Errorf("머리인 임시 값이 빠졌습니다: %v", got)
	}
	if _, ok := got[base.ID]; ok {
		t.Errorf("주제의 옛 버전이 기본 결과에 있습니다: %v", got)
	}
	// 날이 지나면 사용자 말 출처의 임시 값은 상태가 그대로여도 빠지고 옛 값이 현재 값으로 돌아온다.
	advance(s, 48*time.Hour)
	got = find()
	if _, ok := got[tmp.ID]; ok {
		t.Errorf("만료된 휘발 기억이 기본 결과에 있습니다: %v", got)
	}
	if _, ok := got[base.ID]; !ok {
		t.Errorf("만료로 머리가 사라졌는데 옛 값이 돌아오지 않았습니다: %v", got)
	}
	if m, _ := s.Get(tmp.ID); m.State != memory.StateConfirmed {
		t.Errorf("사용자 말 출처의 상태가 바뀌었습니다: %s", m.State)
	}
}

func TestMemoryFind_states_stale이면_폐기됨_만료됨_표시가_붙는다(t *testing.T) {
	cs, s := connect(t)
	base := quoteSave(t, s, "기본은 A", "기본값", 0)
	tmp := quoteSave(t, s, "이번 주만 B", "기본값", 1)
	dead := quoteSave(t, s, "곧 낡을 것", "낡을 것", 0)
	if _, err := s.SetState(dead.ID, memory.StateStale, "틀림"); err != nil {
		t.Fatal(err)
	}
	live := quoteSave(t, s, "그대로 맞는 것", "맞는 것", 0)
	find := func() (map[string]FindItem, string) {
		var out FindOut
		res := call(t, cs, "memory_find", map[string]any{"scope": "all", "states": []string{"candidate", "confirmed", "stale"}}, &out)
		got := map[string]FindItem{}
		for _, it := range out.Items {
			got[it.ID] = it
		}
		return got, text(res)
	}
	got, raw := find()
	if got[dead.ID].Note != "폐기됨" {
		t.Errorf("낡은 기억의 줄: %+v", got[dead.ID])
	}
	if got[base.ID].Note != "옛 버전" {
		t.Errorf("옛 버전의 줄: %+v", got[base.ID])
	}
	if got[tmp.ID].Note != "" || got[live.ID].Note != "" {
		t.Errorf("현재 값의 줄에 표시가 붙었습니다: %+v %+v", got[tmp.ID], got[live.ID])
	}
	var lines struct{ Items []map[string]any }
	if err := json.Unmarshal([]byte(raw), &lines); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines.Items {
		if l["id"] == live.ID {
			if _, has := l["note"]; has {
				t.Errorf("현재 값의 줄에 note 칸이 있습니다: %v", l)
			}
		}
	}
	advance(s, 48*time.Hour)
	got, _ = find()
	if got[tmp.ID].Note != "만료됨" {
		t.Errorf("만료된 휘발 기억의 줄: %+v", got[tmp.ID])
	}
	if got[base.ID].Note != "" {
		t.Errorf("머리가 돌아온 옛 값에 표시가 남았습니다: %+v", got[base.ID])
	}
}

func TestMemoryGet_폐기된_기억은_상태_사유_후계를_머리에_보인다(t *testing.T) {
	cs, s := connect(t)
	old := quoteSave(t, s, "빌드는 make", "빌드 명령", 0)
	next := quoteSave(t, s, "빌드는 task build", "빌드 명령", 0)
	var got GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{old.ID}}, &got)
	n := got.Items[0].Notice
	for _, want := range []string{"폐기됨", "빌드는 task build", next.ID} {
		if !strings.Contains(n, want) {
			t.Errorf("경고 머리에 %q가 없습니다: %q", want, n)
		}
	}
	// 머리 글은 본문보다 먼저 직렬화된다.
	var res GetOut
	raw := text(call(t, cs, "memory_get", map[string]any{"ids": []string{old.ID}}, &res))
	if strings.Index(raw, `"notice"`) > strings.Index(raw, `"body"`) {
		t.Errorf("notice가 body보다 뒤에 있습니다: %s", raw)
	}
	// 현재 값과 만료된 기억
	got = GetOut{}
	call(t, cs, "memory_get", map[string]any{"ids": []string{next.ID}}, &got)
	if got.Items[0].Notice != "" {
		t.Errorf("현재 값에 경고가 붙었습니다: %q", got.Items[0].Notice)
	}
	tmp := quoteSave(t, s, "이번 주만 영어", "보고 언어", 1)
	advance(s, 48*time.Hour)
	got = GetOut{}
	call(t, cs, "memory_get", map[string]any{"ids": []string{tmp.ID}}, &got)
	if !strings.Contains(got.Items[0].Notice, "만료됨") || got.Items[0].State != memory.StateConfirmed {
		t.Errorf("만료된 기억: state=%s notice=%q", got.Items[0].State, got.Items[0].Notice)
	}
}

func TestMemoryGet_연결의_폐기된_상대는_폐기됨과_후계를_보인다(t *testing.T) {
	cs, s := connect(t)
	old := quoteSave(t, s, "빌드는 make", "빌드 명령", 0)
	next := quoteSave(t, s, "빌드는 task build", "빌드 명령", 0)
	orphan := quoteSave(t, s, "곧 낡을 것", "낡을 것", 0)
	if _, err := s.SetState(orphan.ID, memory.StateStale, "틀림"); err != nil {
		t.Fatal(err)
	}
	ok := quoteSave(t, s, "살아 있는 것", "살아 있음", 0)
	host := quoteSave(t, s, "연결을 가진 기억", "연결 주인", 0)
	for _, to := range []string{old.ID, orphan.ID, ok.ID} {
		if _, _, err := s.Link(memory.LinkInput{From: host.ID, To: to, Origin: "ai", OriginRef: "s"}); err != nil {
			t.Fatal(err)
		}
	}
	var got GetOut
	call(t, cs, "memory_get", map[string]any{"ids": []string{host.ID}}, &got)
	by := map[string]string{}
	for _, l := range got.Items[0].Links {
		for _, id := range []string{old.ID, orphan.ID, ok.ID} {
			if strings.Contains(l, id) {
				by[id] = l
			}
		}
	}
	if l := by[old.ID]; !strings.Contains(l, "폐기됨") || !strings.Contains(l, "후계 "+next.ID) {
		t.Errorf("후계가 있는 폐기된 상대: %q", l)
	}
	if l := by[orphan.ID]; !strings.Contains(l, "폐기됨") || strings.Contains(l, "후계") {
		t.Errorf("후계가 없는 폐기된 상대: %q", l)
	}
	if l := by[ok.ID]; strings.Contains(l, "폐기됨") || strings.Contains(l, "만료됨") || !strings.HasSuffix(l, ok.ID) {
		t.Errorf("살아 있는 상대에 표시가 붙었습니다: %q", l)
	}
}
