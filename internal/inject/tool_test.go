package inject

// 설계 · orbit-mem 5.2절(B2 도구 직전)·11절 L-4, ORBIT-26 G6: 호출당 기억 2개·기억 줄 합 800자, 세션 합계 2,000자,
// 정확 일치 → 확정 → 이 제품 → 최신 순. 1-6 b6r(사용자 뜻 「기억을 필요할때 찾아서 쓰게해도 한도가 부족하나?」): 기억 줄은
// 제목 위주로 짧게(기억마다 ≤160자, 본문은 짧게 자르고 「본문은 memory_get」), 한도로 넣지 못한 기억은
// 「이 도구에 걸린 기억 더」 한 줄에 제목·id로 가리킨다(5개·400자까지, 넘으면 「외 N개」). 같은 기억은 세션에 한 번 가리킨다.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

const docsTool = "mcp__docs__write_page"

// toolParts splits a tool injection into its head line, its memory blocks (a "- " line and the
// indented body line under it, each with its newline) and the pointer line.
func toolParts(t *testing.T, text string) (head string, blocks []string, more string) {
	t.Helper()
	if text == "" {
		return "", nil, ""
	}
	lines := strings.Split(text, "\n")
	head = lines[0]
	for _, l := range lines[1:] {
		switch {
		case strings.HasPrefix(l, toolPointerHead):
			more = l
		case strings.HasPrefix(l, "- "):
			blocks = append(blocks, l+"\n")
		case strings.HasPrefix(l, "  ") && len(blocks) > 0:
			blocks[len(blocks)-1] += l + "\n"
		default:
			t.Fatalf("unexpected line %q in\n%s", l, text)
		}
	}
	return head, blocks, more
}

// pointerLine is the pointer line for ms, with left more counted.
func pointerLine(ms []memory.Memory, left int) string {
	var items []string
	for _, m := range ms {
		items = append(items, fmt.Sprintf("%s(%s)", m.Title, m.ID))
	}
	line := toolPointerHead + strings.Join(items, " · ")
	if left > 0 {
		line += fmt.Sprintf(" 외 %d개 — memory_find", left)
	}
	return line
}

func ids(ms ...memory.Memory) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func saveTool(t *testing.T, s *memory.Store, title, body, scope string, tools []string, confirmed bool) memory.Memory {
	t.Helper()
	src := memory.Source{Kind: memory.SourceSession, Ref: "x"}
	if confirmed {
		src = memory.Source{Kind: memory.SourceQuote, Excerpt: title}
	}
	m, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: title, Body: body, Scope: scope, Topic: title, Tools: tools, Source: src})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// manyDocs saves n confirmed global memories on the docs prefix, each with a body far over a memory line.
func manyDocs(t *testing.T, s *memory.Store, n int) []memory.Memory {
	t.Helper()
	var out []memory.Memory
	for i := 0; i < n; i++ {
		later(s, time.Minute)
		out = append(out, saveTool(t, s, fmt.Sprintf("문서 도구 규칙 %d", i), strings.Repeat("길다 ", 300), memory.ScopeGlobal, []string{"mcp__docs__*"}, true))
	}
	return out
}

func runeLen(s string) int { return len([]rune(s)) }

func TestToolInjection_호출당_2개_기억마다_160자_합_800자_남은_기억은_제목과_id로_가리킨다(t *testing.T) {
	s := store(t)
	all := manyDocs(t, s, 6)
	slices.Reverse(all) // 최신부터
	got := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	if MaxToolMemories != 2 || MaxToolCallRunes != 800 || MaxToolMemoryRunes != 160 || MaxToolRunes != 2500 {
		t.Fatalf("limits %d %d %d %d", MaxToolMemories, MaxToolCallRunes, MaxToolMemoryRunes, MaxToolRunes)
	}
	head, blocks, more := toolParts(t, got.Text)
	if !strings.Contains(head, docsTool+" 호출 직전]") || !strings.Contains(head, "본문은 memory_get") || len(blocks) != 2 {
		t.Fatalf("head=%q blocks=%d:\n%s", head, len(blocks), got.Text)
	}
	sum := 0
	for _, b := range blocks {
		if n := runeLen(b); n > MaxToolMemoryRunes {
			t.Fatalf("memory line %d runes:\n%s", n, b)
		}
		if !strings.Contains(b, "\n  길다 길다") || !strings.HasSuffix(strings.TrimSpace(b), "…") {
			t.Fatalf("body cut inside the line:\n%s", b)
		}
		sum += runeLen(b)
	}
	if sum > MaxToolCallRunes {
		t.Fatalf("memory lines %d runes", sum)
	}
	// 넣지 못한 넷은 제목·id로 가리킨다. 가리킨 기억도 문맥에 넣은 기억이다(MemoryIDs).
	if more != pointerLine(all[2:], 0) || runeLen(more) > maxToolPointerRunes {
		t.Fatalf("pointer line %q", more)
	}
	if !slices.Equal(got.MemoryIDs, ids(all...)) {
		t.Fatalf("ids %v, want %v", got.MemoryIDs, ids(all...))
	}
	if got.Chars > MaxToolRunes {
		t.Fatalf("hook text %d runes", got.Chars)
	}
}

func TestToolInjection_가리키기_줄은_5개_400자까지이고_넘친_것은_수로_센다(t *testing.T) {
	s := store(t)
	all := manyDocs(t, s, 10)
	slices.Reverse(all)
	got := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	_, _, more := toolParts(t, got.Text)
	if more != pointerLine(all[2:2+maxToolPointers], 10-2-maxToolPointers) || maxToolPointers != 5 {
		t.Fatalf("pointer line %q", more)
	}
	if !slices.Equal(got.MemoryIDs, ids(all[:2+maxToolPointers]...)) {
		t.Fatalf("ids %v", got.MemoryIDs)
	}
	// 긴 제목은 잘라 400자 안에 둔다.
	s2 := store(t)
	for i := 0; i < 9; i++ {
		saveTool(t, s2, fmt.Sprintf("%d %s", i, strings.Repeat("긴 제목 ", 30)), "본문", memory.ScopeGlobal, []string{"mcp__docs__*"}, true)
	}
	long := ToolInjection(s2, config.Default(), here, "claude", "S", docsTool)
	if _, _, more := toolParts(t, long.Text); runeLen(more) > maxToolPointerRunes || !strings.Contains(more, "외 ") {
		t.Fatalf("long pointer line %d runes: %q", runeLen(more), more)
	}
}

func TestToolInjection_정확_일치_확정_이_제품_최신_순이고_넣지_못한_것은_다음_호출_후보다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	cfg.Products = place.Map{"docs": {Match: []string{"github.com/me/app"}}}
	exact := saveTool(t, s, "정확 일치 후보", "a", memory.ScopeGlobal, []string{docsTool}, false)
	later(s, time.Minute)
	localOld := saveTool(t, s, "이 제품 확정 옛것", "b", "product:docs", []string{"mcp__docs__*"}, true)
	later(s, time.Minute)
	globalConfirmed := saveTool(t, s, "전역 확정", "c", memory.ScopeGlobal, []string{"mcp__docs__*"}, true)
	later(s, time.Minute)
	localNew := saveTool(t, s, "이 제품 확정 새것", "d", "product:docs", []string{"mcp__docs__*"}, true)
	later(s, time.Minute)
	candidate := saveTool(t, s, "전역 후보 최신", "e", memory.ScopeGlobal, []string{"mcp__docs__*"}, false)
	saveTool(t, s, "다른 제품 확정", "f", "product:other", []string{"mcp__docs__*"}, true)

	// 가리키기 줄은 아직 가리키지 않은 기억만: 첫 호출이 남은 셋을 가리켰으니 둘째 호출은 다시 가리키지 않는다.
	// 가리킨 기억은 후보로 남아 다음 호출에 줄로 들어온다.
	want := [][]string{ids(exact, localNew, localOld, globalConfirmed, candidate), ids(localOld, globalConfirmed), ids(candidate)}
	wantMore := []string{pointerLine([]memory.Memory{localOld, globalConfirmed, candidate}, 0), "", ""}
	for i := range want {
		got := ToolInjection(s, cfg, here, "claude", "S", docsTool)
		_, _, more := toolParts(t, got.Text)
		if !slices.Equal(got.MemoryIDs, want[i]) || more != wantMore[i] {
			t.Fatalf("call %d ids=%v more=%q:\n%s", i, got.MemoryIDs, more, got.Text)
		}
		RecordShown(s, "claude", "S", got)
	}
	if last := ToolInjection(s, cfg, here, "claude", "S", docsTool); last.Text != "" {
		t.Fatalf("everything shown, still:\n%s", last.Text)
	}
}

func TestToolInjection_세션_합계_2000자에서_멈추고_같은_기억은_한_번_가리키고_새로_걸린_기억이_생기면_다시_가리킨다(t *testing.T) {
	s := store(t)
	all := manyDocs(t, s, 20)
	if MaxToolSessionRunes != 2000 {
		t.Fatalf("session limit %d", MaxToolSessionRunes)
	}
	rev := slices.Clone(all)
	slices.Reverse(rev) // 최신부터
	// 자리가 남은 동안: 첫 호출이 가리킨 기억은 다시 가리키지 않고, 수로만 센 기억은 다음 호출이 가리킨다.
	first := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	_, _, more := toolParts(t, first.Text)
	if more != pointerLine(rev[2:2+maxToolPointers], 20-2-maxToolPointers) {
		t.Fatalf("first pointer line %q", more)
	}
	RecordShown(s, "claude", "S", first)
	second := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	_, blocks, more := toolParts(t, second.Text)
	// 가리킨 기억은 후보로 남아 줄로 들어오고, 아직 가리키지 않은 기억을 새로 가리킨다.
	if len(blocks) != 2 || !slices.Equal(second.MemoryIDs[:2], first.MemoryIDs[2:4]) || !strings.Contains(more, rev[2+maxToolPointers].ID) {
		t.Fatalf("second: %v %q\n%s", second.MemoryIDs, more, second.Text)
	}
	for _, id := range second.MemoryIDs[2:] {
		if slices.Contains(first.MemoryIDs, id) {
			t.Fatalf("second points again at %s", id)
		}
	}
	RecordShown(s, "claude", "S", second)

	// 새로 걸린 기억이 생기면 그것이 가장 새것이라 줄로 먼저 들어온다.
	later(s, time.Minute)
	fresh1 := saveTool(t, s, "새로 걸린 문서 규칙", strings.Repeat("길다 ", 300), memory.ScopeGlobal, []string{"mcp__docs__*"}, true)
	fresh := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	if len(fresh.MemoryIDs) == 0 || fresh.MemoryIDs[0] != fresh1.ID {
		t.Fatalf("new memory must come first:\n%s", fresh.Text)
	}
	RecordShown(s, "claude", "S", fresh)

	total := first.Chars + second.Chars + fresh.Chars
	stopped := 0
	for call := 0; call < 14; call++ {
		got := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
		total += got.Chars
		if total > MaxToolSessionRunes || got.Chars > MaxToolRunes {
			t.Fatalf("call %d: session total %d, call %d\n%s", call, total, got.Chars, got.Text)
		}
		if got.Text == "" {
			stopped++
		}
		RecordShown(s, "claude", "S", got)
	}
	if stopped < 2 {
		t.Fatalf("stopped=%d total=%d", stopped, total)
	}

	// 서브에이전트 키는 주 세션 합계와 따로 센다.
	if sub := ToolInjection(s, config.Default(), here, "claude", "S/a1", docsTool); !strings.Contains(sub.Text, "\n  길다") || len(sub.MemoryIDs) != 2+maxToolPointers {
		t.Fatalf("subagent: %v\n%s", sub.MemoryIDs, sub.Text)
	}
	// reset이 표시(넣음·가리킴·호출)를 지우면 다시 허용되고 다시 가리킨다.
	ResetSession(s, "claude", "S")
	again := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	if _, blocks, more := toolParts(t, again.Text); len(blocks) != 2 || more == "" {
		t.Fatalf("after reset: %v more=%q\n%s", again.MemoryIDs, more, again.Text)
	}
}

func TestToolInjection_세션_합계는_지운_기억과_숨긴_기억을_한_줄_최대로_센다(t *testing.T) {
	s := store(t)
	shown := saveTool(t, s, "곧 숨길 짧은 후보", "짧다", memory.ScopeGlobal, []string{"mcp__docs__*"}, false)
	first := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	if !slices.Equal(first.MemoryIDs, []string{shown.ID}) {
		t.Fatalf("first: %v", first.MemoryIDs)
	}
	RecordShown(s, "claude", "S", first)
	if _, err := s.SetState(shown.ID, memory.StateHidden, "시험"); err != nil {
		t.Fatal(err)
	}
	// 호출 표시가 없는 옛 판 세션: 넣은 기억(지운 n개 + 숨긴 하나)을 한 줄 최대로 센 것이 바닥이다. 세션 합계를 채워
	// 짧은 줄도 가리키기 줄도 들지 않는다.
	var gone []string
	for i := 0; i < MaxToolSessionRunes/MaxToolMemoryRunes; i++ {
		gone = append(gone, fmt.Sprintf("m-gone-%d", i))
	}
	if err := s.MarkSession("claude", "S", memory.MarkTool, gone...); err != nil {
		t.Fatal(err)
	}
	short := saveTool(t, s, "짧은 확정", "짧다", memory.ScopeGlobal, []string{"mcp__docs__*"}, true)
	if got := ToolInjection(s, config.Default(), here, "claude", "S", docsTool); got.Text != "" {
		t.Fatalf("full session, still:\n%s", got.Text)
	}
	// 같은 짧은 줄은 표시가 적은 다른 세션에는 들어간다.
	if other := ToolInjection(s, config.Default(), here, "claude", "T", docsTool); !slices.Equal(other.MemoryIDs, []string{short.ID}) || !strings.Contains(other.Text, "  짧다") {
		t.Fatalf("other session: %v", other.MemoryIDs)
	}
}

func TestToolInjection_아주_긴_도구_이름과_긴_제목도_훅_글_2500자_안이다(t *testing.T) {
	s := store(t)
	for i := 0; i < 4; i++ {
		saveTool(t, s, strings.Repeat("제", 190)+fmt.Sprint(i), strings.Repeat("본문 ", 400), memory.ScopeGlobal, []string{"mcp__docs__*"}, true)
	}
	tool := "mcp__docs__" + strings.Repeat("x", 10000)
	got := ToolInjection(s, config.Default(), here, "claude", "S", tool)
	_, blocks, more := toolParts(t, got.Text)
	if len(blocks) != 2 || len(got.MemoryIDs) != 4 || more == "" || got.Chars > MaxToolRunes || runeLen(more) > maxToolPointerRunes {
		t.Fatalf("blocks=%d ids=%d more=%q chars=%d", len(blocks), len(got.MemoryIDs), more, got.Chars)
	}
	for _, b := range blocks {
		if runeLen(b) > MaxToolMemoryRunes {
			t.Fatalf("memory line %d runes", runeLen(b))
		}
	}
}

func TestToolInjection_짧은_기억은_자르지_않고_지금_꼴_그대로다(t *testing.T) {
	s := store(t)
	m := saveTool(t, s, "문서는 공간에 쓴다", "write_page 전에 위치를 확인한다", memory.ScopeGlobal, []string{docsTool}, true)
	got := ToolInjection(s, config.Default(), here, "claude", "S", docsTool)
	want := "[" + Label + " 기억 · " + docsTool + " 호출 직전] 걸린 기억을 확인하고 따른다. 본문은 memory_get\n" +
		"- 문서는 공간에 쓴다 (" + m.ID + " · 규칙 확정)\n  write_page 전에 위치를 확인한다"
	if got.Text != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got.Text, want)
	}
}

// 1-6 b6r 2차(설계 5.2절 「세션 합계 2,000자에서 멈춘다」): 세션 합계는 기억 줄만이 아니라 훅 글 전체(머리말·기억 줄·가리키기
// 줄)다. 여러 도구·여러 호출에 걸쳐 한 세션이 받은 도구 직전 글의 합은 2,000자를 넘지 않는다.
func TestToolInjection_세션_합계_2000자는_머리말과_가리키기_줄까지_훅_글_전체를_센다(t *testing.T) {
	s := store(t)
	manyDocs(t, s, 30)
	for i := 0; i < 6; i++ {
		saveTool(t, s, fmt.Sprintf("배포 도구 규칙 %d", i), strings.Repeat("짧지 않다 ", 40), memory.ScopeGlobal, []string{"mcp__deploy__*"}, true)
	}
	tools := []string{docsTool, "mcp__deploy__release_with_a_rather_long_tool_name_" + strings.Repeat("x", 300), docsTool, "mcp__docs__read_page"}
	total, emitted, pointedOnly := 0, 0, 0
	for call := 0; call < 40; call++ {
		got := ToolInjection(s, config.Default(), here, "claude", "S", tools[call%len(tools)])
		if got.Text == "" {
			continue
		}
		emitted++
		total += got.Chars
		if total > MaxToolSessionRunes {
			t.Fatalf("call %d: session total %d > %d\n%s", call, total, MaxToolSessionRunes, got.Text)
		}
		if _, blocks, more := toolParts(t, got.Text); len(blocks) == 0 && more != "" {
			pointedOnly++
		}
		RecordShown(s, "claude", "S", got)
	}
	// 머리말 하나와 가리키기 항목 하나도 들지 않을 만큼 찼을 때 멈췄다.
	if emitted < 3 || total+runeLen(toolHead(docsTool))+60 <= MaxToolSessionRunes {
		t.Fatalf("emitted=%d pointedOnly=%d total=%d", emitted, pointedOnly, total)
	}
}
