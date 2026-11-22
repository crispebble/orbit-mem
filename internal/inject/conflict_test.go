package inject

// 어긋남·알림 표시(명세 C-2·C-5·C-6·C-8): 어긋남이 있는 기억 줄에만 새 사실을 나란히 붙이고, 확인 지시는 자동 모드가 꺼져 있을 때
// 세션마다 쌍마다 한 번이다. 자동 대체 알림 줄과 넘어간 어긋남 개수 줄은 0건이면 없다.

import (
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const (
	conflictOldTitle = "배포는 금요일 오후에만 한다"
	conflictNewTitle = "배포는 화요일 오전에 한다"
	conflictPrompt   = "금요일 오후 배포 일정을 알려 줘"
	askMark          = "사용자에게 한 번 확인"
	sideMark         = "어긋나는 새 사실"
)

// conflictPair saves a user's fact and a session-learned fact on its topic: the save leaves them as a conflict.
func conflictPair(t *testing.T, s *memory.Store, tools ...string) (old, nw memory.Memory) {
	t.Helper()
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: conflictOldTitle, Body: "릴리스는 금요일 오후 배포 창에서만 나간다", Topic: "배포 일정", Scope: here, Tools: tools,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: conflictOldTitle}})
	if err != nil {
		t.Fatal(err)
	}
	nw, rep, err := s.SaveWith(memory.SaveInput{Kind: memory.KindFact, Title: conflictNewTitle, Body: "오전 배포 창으로 바뀌었다", Topic: "배포 일정", Scope: here,
		Who: memory.Who{Actor: memory.ActorAI}, Source: memory.Source{Kind: memory.SourceSession, Ref: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Conflicts) != 1 || rep.Conflicts[0] != old.ID {
		t.Fatalf("어긋남이 남지 않았습니다: %+v", rep)
	}
	return old, nw
}

// lineOf is the recall line that names id (a line starting with "- "), with the lines indented under it.
func lineOf(text, id string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") && strings.Contains(l, id) {
			out := l
			for _, next := range lines[i+1:] {
				if !strings.HasPrefix(next, "  ") {
					break
				}
				out += "\n" + next
			}
			return out
		}
	}
	return ""
}

func TestRecallLine_어긋남이_있으면_새_사실을_나란히_보인다(t *testing.T) {
	s := store(t)
	old, nw := conflictPair(t, s)
	got := PromptInjectionFor(s, config.Default(), here, conflictPrompt, "claude", "A", Options{})
	line := lineOf(got.Text, old.ID)
	want := sideMark + ": 「" + conflictNewTitle + "」(" + nw.ID + ")"
	if !strings.Contains(line, want) {
		t.Fatalf("옛 기억 줄에 새 사실이 없습니다 (%q):\n%s", want, got.Text)
	}
	if nl := lineOf(got.Text, nw.ID); strings.Contains(nl, sideMark) {
		t.Fatalf("새 쪽 줄에는 붙지 않습니다:\n%s", got.Text)
	}
}

func TestRecallLine_자동_모드_끔이면_확인_지시가_세션에_쌍마다_한_번이다(t *testing.T) {
	s := store(t)
	old, _ := conflictPair(t, s, "Bash")
	cfg := config.Default()
	SessionBundle(s, cfg, here, "claude", "A")
	first := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "A", Options{})
	if !strings.Contains(lineOf(first.Text, old.ID), askMark) {
		t.Fatalf("확인 지시가 없습니다:\n%s", first.Text)
	}
	RecordShown(s, "claude", "A", first)
	// 같은 쌍이 도구 직전에 또 실려도 지시는 다시 붙지 않는다. 새 사실은 나란히 계속 보인다.
	tool := ToolInjection(s, cfg, here, "claude", "A", "Bash", "")
	if !strings.Contains(tool.Text, old.ID) || !strings.Contains(tool.Text, sideMark) || strings.Contains(tool.Text, askMark) {
		t.Fatalf("도구 직전 줄:\n%s", tool.Text)
	}
	// 다른 세션은 따로 센다.
	other := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "B", Options{})
	if !strings.Contains(lineOf(other.Text, old.ID), askMark) {
		t.Fatalf("다른 세션에서 지시가 빠졌습니다:\n%s", other.Text)
	}
	// 재개해 다시 같은 요청을 해도(compact 뒤 reset) 지시를 한 번 더 받는다: reset은 세션 표시를 지운다.
	ResetSession(s, "claude", "A")
	SessionBundle(s, cfg, here, "claude", "A")
	again := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "A", Options{})
	if !strings.Contains(lineOf(again.Text, old.ID), askMark) {
		t.Fatalf("reset 뒤에는 다시 묻는다:\n%s", again.Text)
	}
}

func TestRecallLine_도구_직전에_먼저_실리면_지시는_도구_줄에_붙고_요청_회상에는_다시_붙지_않는다(t *testing.T) {
	s := store(t)
	old, _ := conflictPair(t, s, "Bash")
	cfg := config.Default()
	SessionBundle(s, cfg, here, "claude", "A")
	tool := ToolInjection(s, cfg, here, "claude", "A", "Bash", "")
	if !strings.Contains(lineOf(tool.Text, old.ID), askMark) {
		t.Fatalf("도구 줄에 지시가 없습니다:\n%s", tool.Text)
	}
	RecordShown(s, "claude", "A", tool)
	req := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "A", Options{})
	line := lineOf(req.Text, old.ID)
	if !strings.Contains(line, sideMark) || strings.Contains(line, askMark) {
		t.Fatalf("요청 회상 줄:\n%s", req.Text)
	}
}

func TestRecallLine_자동_모드_켬이면_확인_지시가_없다(t *testing.T) {
	s := store(t)
	old, nw := conflictPair(t, s, "Bash")
	cfg := config.Default()
	cfg.AutoMode = true
	SessionBundle(s, cfg, here, "claude", "A")
	got := PromptInjectionFor(s, cfg, here, conflictPrompt, "claude", "A", Options{})
	line := lineOf(got.Text, old.ID)
	if !strings.Contains(line, sideMark+": 「"+conflictNewTitle+"」("+nw.ID+")") || strings.Contains(line, askMark) {
		t.Fatalf("켬: 나란히는 보이고 지시는 없어야 합니다:\n%s", got.Text)
	}
	tool := ToolInjection(s, cfg, here, "claude", "A", "Bash", "")
	if !strings.Contains(tool.Text, sideMark) || strings.Contains(tool.Text, askMark) {
		t.Fatalf("도구 줄:\n%s", tool.Text)
	}
}

func TestRecallLine_어긋남_없는_기억에는_아무것도_붙지_않는다(t *testing.T) {
	s := store(t)
	conflictPair(t, s)
	plain, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "점검은 월요일 새벽에 한다", Body: "정기 점검 창", Topic: "점검 일정", Scope: here,
		Tools: []string{"Bash"}, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "점검은 월요일 새벽에 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	req := PromptInjectionFor(s, cfg, here, "월요일 새벽 점검 일정을 알려 줘", "claude", "A", Options{})
	line := lineOf(req.Text, plain.ID)
	if line == "" || strings.Contains(line, sideMark) || strings.Contains(line, askMark) {
		t.Fatalf("어긋남 없는 줄:\n%s", req.Text)
	}
	tool := ToolInjection(s, cfg, here, "claude", "B", "Bash", "")
	if !strings.Contains(tool.Text, plain.ID) || strings.Contains(tool.Text, sideMark) || strings.Contains(tool.Text, askMark) {
		t.Fatalf("도구 줄:\n%s", tool.Text)
	}
}

func TestRecallLine_사용자_말로_정하면_표시가_사라진다(t *testing.T) {
	s := store(t)
	old, nw := conflictPair(t, s)
	body := "사용자가 확인한 본문"
	if _, err := s.Update(memory.UpdateInput{ID: nw.ID, Body: &body, Who: memory.Who{Actor: memory.ActorAI, UserQuote: "화요일이 맞아"},
		Sources: []memory.Source{{Kind: memory.SourceQuote, Excerpt: "화요일이 맞아"}}}); err != nil {
		t.Fatal(err)
	}
	got := PromptInjectionFor(s, config.Default(), here, conflictPrompt, "claude", "A", Options{})
	if strings.Contains(lineOf(got.Text, old.ID), sideMark) {
		t.Fatalf("풀린 어긋남이 남았습니다:\n%s", got.Text)
	}
}

func TestSessionStart_핵심_기억에_어긋남이_있으면_새_사실과_확인_지시가_세션에_한_번_붙는다(t *testing.T) {
	s := store(t)
	core, err := s.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 항상 한글로 한다", Body: "영어로 답하지 않는다", Topic: "답 언어", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "답은 항상 한글로 한다"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pin(core.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	nw, rep, err := s.SaveWith(memory.SaveInput{Kind: memory.KindRule, Title: "답은 영어로 한다", Topic: "답 언어", Scope: memory.ScopeGlobal,
		Who: memory.Who{Actor: memory.ActorAI}, Source: memory.Source{Kind: memory.SourceSession, Ref: "s"}})
	if err != nil || len(rep.Conflicts) != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	got := SessionBundleWith(s, config.Default(), here, "claude", "A", nil)
	if !strings.Contains(got.Text, sideMark+": 「답은 영어로 한다」("+nw.ID+")") || !strings.Contains(got.Text, askMark) {
		t.Fatalf("핵심 줄:\n%s", got.Text)
	}
	RecordShown(s, "claude", "A", got)
	// 같은 세션의 이어받기는 묶음을 그대로 돌려준다(같은 글).
	if again := SessionBundleWith(s, config.Default(), here, "claude", "A", nil); again.Text != got.Text {
		t.Fatalf("묶음이 달라졌습니다:\n%s\n---\n%s", got.Text, again.Text)
	}
	// 어긋남이 없는 세션 시작에는 아무것도 붙지 않는다.
	clean := store(t)
	c2, _ := clean.Save(memory.SaveInput{Kind: memory.KindRule, Title: "답은 항상 한글로 한다", Body: "영어로 답하지 않는다", Topic: "답 언어", Scope: memory.ScopeGlobal,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "답은 항상 한글로 한다"}})
	if _, err := clean.Pin(c2.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	if text := SessionBundleWith(clean, config.Default(), here, "claude", "A", nil).Text; strings.Contains(text, sideMark) || strings.Contains(text, askMark) {
		t.Fatalf("어긋남 없는 핵심:\n%s", text)
	}
}

// verifiedReplace saves a user's fact and then a checked fact on its topic, so the second replaces the first by itself.
func verifiedReplace(t *testing.T, s *memory.Store, topic string) (old, nw memory.Memory) {
	t.Helper()
	old, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "빌드는 make all로 한다 " + topic, Topic: topic, Scope: here,
		Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "빌드는 make all로 한다 " + topic}})
	if err != nil {
		t.Fatal(err)
	}
	nw, rep, err := s.SaveWith(memory.SaveInput{Kind: memory.KindFact, Title: "빌드는 go build로 한다 " + topic, Topic: topic, Scope: here,
		Who:    memory.Who{Actor: memory.ActorAI},
		Source: memory.Source{Kind: memory.SourceVerification, Ref: "/repo/Makefile", Excerpt: "build: go build ./...", Rechecked: true}})
	if err != nil || len(rep.Superseded) != 1 {
		t.Fatalf("자동 대체가 일어나지 않았습니다: %v %+v", err, rep)
	}
	return old, nw
}

func TestSessionStart_자동_대체_알림은_한_번만_나온다(t *testing.T) {
	s := store(t)
	old, nw := verifiedReplace(t, s, "빌드 명령")
	cfg := config.Default()
	want := "사용자 말 사실 1건을 검증된 사실로 바꿨다: 「" + old.Title + "」 → 「" + nw.Title + "」"
	first := SessionBundleWith(s, cfg, here, "claude", "A", nil)
	if !strings.Contains(first.Text, want) {
		t.Fatalf("알림이 없습니다 (%q):\n%s", want, first.Text)
	}
	// 같은 세션을 이어받으면 같은 글이고, 다른 새 세션에는 다시 나오지 않는다.
	if resumed := SessionBundleWith(s, cfg, here, "claude", "A", nil); resumed.Text != first.Text {
		t.Fatalf("이어받은 묶음이 달라졌습니다:\n%s", resumed.Text)
	}
	if next := SessionBundleWith(s, cfg, here, "claude", "B", nil); strings.Contains(next.Text, "검증된 사실로 바꿨다") {
		t.Fatalf("알린 뒤 다시 나왔습니다:\n%s", next.Text)
	}
	// 새로 자동 대체되면 그것만 나온다.
	later(s, time.Minute)
	old2, nw2 := verifiedReplace(t, s, "시험 명령")
	third := SessionBundleWith(s, cfg, here, "claude", "C", nil)
	if !strings.Contains(third.Text, "「"+old2.Title+"」 → 「"+nw2.Title+"」") || strings.Contains(third.Text, old.Title) {
		t.Fatalf("새 대체만 나와야 합니다:\n%s", third.Text)
	}
}

func TestSessionStart_자동_대체_알림은_요청과_도구_경로에서_표시를_쓰지_않는다(t *testing.T) {
	s := store(t)
	verifiedReplace(t, s, "빌드 명령")
	cfg := config.Default()
	// 묶음이 없는 세션의 요청 회상은 세션 시작이 넣을 것을 가늠하려 세션 시작 글을 만들지만 알림 표시는 쓰지 않는다.
	PromptInjectionFor(s, cfg, here, "빌드 명령을 알려 줘", "claude", "", Options{})
	SessionStart(s, cfg, here)
	SessionStartInjection(s, cfg, here)
	ToolInjection(s, cfg, here, "claude", "A", "Bash", "")
	if at := s.AutoNoticeAt(here); at != 0 {
		t.Fatalf("세션 시작 밖에서 표시를 썼습니다: %d", at)
	}
	if first := SessionBundleWith(s, cfg, here, "claude", "A", nil); !strings.Contains(first.Text, "검증된 사실로 바꿨다") {
		t.Fatalf("세션 시작에는 나와야 합니다:\n%s", first.Text)
	}
	if s.AutoNoticeAt(here) == 0 {
		t.Fatal("세션 시작이 표시를 쓰지 않았습니다")
	}
}

func TestSessionStart_넘어간_어긋남_개수_줄은_0건이면_없다(t *testing.T) {
	const countMark = "묻지 않고 넘어간 어긋남"
	s := store(t)
	on := config.Default()
	on.AutoMode = true
	if text := SessionBundleWith(s, on, here, "claude", "A", nil).Text; strings.Contains(text, countMark) {
		t.Fatalf("0건인데 줄이 있습니다:\n%s", text)
	}
	conflictPair(t, s)
	if text := SessionBundleWith(s, on, here, "claude", "B", nil).Text; !strings.Contains(text, countMark+" 1건") {
		t.Fatalf("1건 줄이 없습니다:\n%s", text)
	}
	// 자동 모드가 꺼져 있으면 어긋남은 작업에 걸릴 때 묻는다: 개수 줄은 없다.
	if text := SessionBundleWith(s, config.Default(), here, "claude", "C", nil).Text; strings.Contains(text, countMark) {
		t.Fatalf("끔인데 줄이 있습니다:\n%s", text)
	}
}

// 자동 모드를 켰을 때 자동 대체된 것은 끈 뒤에도 그대로이고, 끈 뒤 새로 생긴 같은 꼴의 어긋남은 확인 지시가 붙는다.
func TestAutoMode_끈_뒤에도_이미_대체된_것은_그대로이고_새_어긋남은_묻는다(t *testing.T) {
	s := store(t)
	s.Policy.AutoMode = true
	stated := func(title, topic string) memory.SaveInput {
		return memory.SaveInput{Kind: memory.KindFact, Title: title, Topic: topic, Scope: here, Who: memory.Who{Actor: memory.ActorAI},
			Source: memory.Source{Kind: memory.SourceVerification, Ref: "go test ./...", Excerpt: "ok  example.com/app  0.4s"}}
	}
	userFact := func(title, topic string) memory.Memory {
		m, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: title, Topic: topic, Scope: here, Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	// 켠 동안: 근거만 적힌 검증 사실이 사용자 말 사실을 대체한다.
	oldOn := userFact("시험은 make test로 한다", "시험 명령")
	newOn, rep, err := s.SaveWith(stated("시험은 go test로 한다", "시험 명령"))
	if err != nil || len(rep.Superseded) != 1 {
		t.Fatalf("켠 동안 자동 대체: %v %+v", err, rep)
	}
	s.Policy.AutoMode = false
	if m, _ := s.Get(oldOn.ID); m.State != memory.StateStale || m.SupersededBy != newOn.ID {
		t.Fatalf("끈 뒤에 대체가 바뀌었습니다: %+v", m)
	}
	// 끈 뒤: 같은 꼴의 새 사실은 대체하지 못하고 어긋남이 남아, 회상 줄에 지시가 붙는다.
	oldOff := userFact("빌드는 make all로 한다", "빌드 명령")
	newOff, rep, err := s.SaveWith(stated("빌드는 go build로 한다", "빌드 명령"))
	if err != nil || len(rep.Superseded) != 0 || len(rep.Conflicts) != 1 {
		t.Fatalf("끈 뒤 어긋남: %v %+v", err, rep)
	}
	cfg := config.Default() // 끔
	got := PromptInjectionFor(s, cfg, here, "make all 빌드는 어떻게 하지", "claude", "A", Options{})
	line := lineOf(got.Text, oldOff.ID)
	if !strings.Contains(line, sideMark+": 「"+newOff.Title+"」("+newOff.ID+")") || !strings.Contains(line, askMark) {
		t.Fatalf("끈 뒤의 어긋남에 지시가 없습니다:\n%s", got.Text)
	}
	if m, _ := s.Get(oldOn.ID); m.State != memory.StateStale {
		t.Fatalf("이미 대체된 것이 되돌아왔습니다: %+v", m)
	}
}

// 자동 대체를 되돌린 뒤: 옛 기억은 확정, 새 기억은 후보로 같은 주제에 남는다. 현재 값은 주제의 머리 하나(확정이 앞선다)라 회상에는
// 옛 기억만 실린다. 되돌린 옛 기억에 제목만 바꿔 다시 오는 새 기억은 어긋남으로 남아 옛 기억 줄 옆에 보인다.
func TestRecallLine_자동_대체를_되돌린_뒤에도_현재_값은_하나이고_제목만_바꾼_새_기억은_어긋남으로_보인다(t *testing.T) {
	s := store(t)
	old, nw := verifiedReplace(t, s, "빌드 명령")
	var autoLine string
	entries, err := s.Ledger(memory.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ProposalKind == memory.ProposalSupersede {
			autoLine = e.ID
		}
	}
	if _, err := s.RevertChange(autoLine, memory.Who{}, "되돌림"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	got := PromptInjectionFor(s, cfg, here, "make all 빌드 명령을 알려 줘", "claude", "A", Options{})
	if lineOf(got.Text, old.ID) == "" || lineOf(got.Text, nw.ID) != "" || strings.Contains(got.Text, sideMark) {
		t.Fatalf("되돌린 뒤에는 옛 기억만, 표시 없이 실려야 합니다:\n%s", got.Text)
	}
	renamed, rep, err := s.SaveWith(memory.SaveInput{Kind: memory.KindFact, Title: "빌드는 go build를 쓴다 빌드 명령", Topic: "빌드 명령", Scope: here,
		Who:    memory.Who{Actor: memory.ActorAI},
		Source: memory.Source{Kind: memory.SourceVerification, Ref: "/repo/Makefile", Excerpt: "build: go build ./...", Rechecked: true}})
	// 되돌린 옛 기억은 대체하지 않고 어긋남으로 두고, 후보로 내려 앉은 앞의 새 기억은 이 저장이 대체한다.
	if err != nil || len(rep.Conflicts) != 1 || rep.Conflicts[0] != old.ID || len(rep.Superseded) != 1 || rep.Superseded[0] != nw.ID {
		t.Fatalf("제목만 바꾼 새 기억은 옛 기억과 어긋남이어야 합니다: %v %+v", err, rep)
	}
	got = PromptInjectionFor(s, cfg, here, "make all 빌드 명령을 알려 줘", "claude", "B", Options{})
	if line := lineOf(got.Text, old.ID); !strings.Contains(line, sideMark+": 「"+renamed.Title+"」("+renamed.ID+")") {
		t.Fatalf("어긋남이 붙지 않았습니다:\n%s", got.Text)
	}
}
