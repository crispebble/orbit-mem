package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 가까운 기억(1-6 b6dn): memory_save가 막지 않고 저장할 때만 같은 범위의 가까운 기억 id·제목을 최대 3개 한 줄(near)로
// 붙인다. 막을 때(saved:false)·같은 제목·같은 주제·resolve로 고른 저장·가까운 것이 없을 때는 붙이지 않는다.

// 「배포 공지는 팀 채널에 미리 알린다」와 비슷함(similar.go): 영어로 0.408 · 하루 전 0.365 · 백업 0.204 · 위키 0.204 ·
// 커밋 0. 모두 기본 막음 문턱(0.45) 밑이다.
var nearProbe = "배포 공지는 팀 채널에 미리 알린다"

func nearArgs(title string) map[string]any { return deployArgs(title, "") }

func TestSave_막지_않고_저장하면_가까운_기억_id와_제목을_점수_순으로_최대_3개_붙인다(t *testing.T) {
	cs, _ := connectWith(t, Options{Project: appScope, ProjectLabel: "github.com/me/app", SaveNearMin: 0.2})
	var ids []string
	for _, ti := range []string{"배포 공지는 하루 전에 올린다", "배포 공지는 영어로 쓴다", "배포 전에 백업을 확인한다", "배포 기록은 위키에 남긴다"} {
		ids = append(ids, saveMCP(t, cs, nearArgs(ti)).Memory.ID)
	}
	far := saveMCP(t, cs, nearArgs("커밋 메시지는 한글로 쓴다")).Memory.ID
	out := saveMCP(t, cs, nearArgs(nearProbe))
	if !out.Saved || len(out.Similar) != 0 || out.Near == "" {
		t.Fatalf("saved with close memories: %+v", out)
	}
	if !strings.HasPrefix(out.Near, i18n.TIn(i18n.KO, "memory.save.near", "")) {
		t.Fatalf("near line prefix: %q", out.Near)
	}
	if strings.Contains(out.Near, far) || strings.Contains(out.Near, out.Memory.ID) {
		t.Fatalf("far or the saved memory itself in near: %q", out.Near)
	}
	n := 0
	for _, id := range ids {
		if strings.Contains(out.Near, id) {
			n++
		}
	}
	if n != memory.SimilarLimit {
		t.Fatalf("near names %d memories, want %d: %q", n, memory.SimilarLimit, out.Near)
	}
	// 점수 순: 영어로(0.408) → 하루 전(0.365) → 백업·위키(0.204) 가운데 하나.
	if i, j := strings.Index(out.Near, ids[1]), strings.Index(out.Near, ids[0]); i < 0 || j < 0 || i > j || !strings.Contains(out.Near, ids[0]+" 배포 공지는 하루 전에 올린다 · ") {
		t.Fatalf("near not ranked: %q", out.Near)
	}
}

func TestSave_막을_때_같은_제목_resolve_add_가까운_것이_없을_때는_near가_없다(t *testing.T) {
	cs, _ := connect(t)
	saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	saveMCP(t, cs, nearArgs("배포 공지는 하루 전에 올린다"))
	blocked := saveMCP(t, cs, deployArgs(deployReword[0], deployReword[1]))
	if blocked.Saved || blocked.Near != "" {
		t.Fatalf("blocked save: %+v", blocked)
	}
	added := saveMCP(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", "add"))
	if !added.Saved || added.Near != "" {
		t.Fatalf("resolve add: %+v", added)
	}
	same := saveMCP(t, cs, nearArgs("배포 공지는 하루 전에 올린다"))
	if !same.Saved || same.Near != "" {
		t.Fatalf("same title: %+v", same)
	}
	none := saveMCP(t, cs, nearArgs("커밋 메시지는 한글로 쓴다"))
	if !none.Saved || none.Near != "" {
		t.Fatalf("nothing close: %+v", none)
	}
	raw, _ := json.Marshal(none)
	if strings.Contains(string(raw), `"near"`) {
		t.Fatalf("empty near is in the answer: %s", raw)
	}
}

func TestSave_가까운_하한은_Options의_saveNearMin을_쓰고_LoadSaveNearMin이_이긴다(t *testing.T) {
	at := func(v float64) func() float64 { return func() float64 { return v } }
	for i, c := range []struct {
		o    Options
		near bool
	}{{Options{SaveNearMin: 0.3}, true}, {Options{SaveNearMin: 0.4}, false}, {Options{LoadSaveNearMin: at(0.4)}, false},
		{Options{SaveNearMin: 0.4, LoadSaveNearMin: at(0.3)}, true}} {
		c.o.Project, c.o.ProjectLabel = appScope, "github.com/me/app"
		cs, _ := connectWith(t, c.o)
		saveMCP(t, cs, nearArgs("배포 공지는 하루 전에 올린다")) // 아래와 0.365
		if out := saveMCP(t, cs, nearArgs(nearProbe)); !out.Saved || c.near != (out.Near != "") {
			t.Errorf("options %d: near %q, want near %v", i, out.Near, c.near)
		}
	}
}

func TestNearLine_제목을_잘라_글자_상한을_지키고_같은_입력이면_같은_줄이다(t *testing.T) {
	long := strings.Repeat("아주 긴 제목의 낱말 ", 20)
	var near []memory.Similar
	for i := 0; i < memory.SimilarLimit; i++ {
		near = append(near, memory.Similar{ID: memory.NewID("m"), Title: long, Score: 0.3})
	}
	for _, l := range []i18n.Lang{i18n.KO, i18n.EN} {
		line := nearLineIn(l, near)
		if n := utf8.RuneCountInString(line); n > nearLineRunes {
			t.Errorf("%s: %d runes > %d: %q", l, n, nearLineRunes, line)
		}
		if strings.Count(line, "…") != memory.SimilarLimit {
			t.Errorf("%s: titles not cut: %q", l, line)
		}
		for _, m := range near {
			if !strings.Contains(line, m.ID) {
				t.Errorf("%s: id %s missing: %q", l, m.ID, line)
			}
		}
		if line != nearLineIn(l, near) {
			t.Errorf("%s: same input, other line", l)
		}
	}
	short := []memory.Similar{{ID: "m-aaaaaaaaaaaa", Title: "짧은  제목\n둘째 줄", Score: 0.3}, {ID: "m-bbbbbbbbbbbb", Title: "남의 글", Trust: memory.TrustUntrusted}}
	line := nearLineIn(i18n.KO, short)
	if !strings.Contains(line, "m-aaaaaaaaaaaa 짧은 제목 둘째 줄") || strings.Contains(line, "…") || !strings.Contains(line, memory.TrustUntrusted) {
		t.Errorf("short titles: %q", line)
	}
	if nearLineIn(i18n.KO, nil) != "" {
		t.Error("no close memories gave a line")
	}
	if en := nearLineIn(i18n.EN, short); en == line || !strings.HasPrefix(en, i18n.TIn(i18n.EN, "memory.save.near", "")) {
		t.Errorf("en line: %q", en)
	}
}
