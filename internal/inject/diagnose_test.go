package inject

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func stages(d Diagnosis) map[string]Stage {
	out := map[string]Stage{}
	for _, st := range d.Stages {
		out[st.Name] = st
	}
	return out
}

func TestDiagnose_없는_기억은_저장_단계에서_멈춘다(t *testing.T) {
	s := store(t)
	d := Diagnose(s, config.Default(), DiagnoseInput{ID: "m-none", Scope: here, Prompt: "아무 말"})
	if d.StoppedAt != StageStored || d.LastProven != "none" || stages(d)[StageStored].Code != "not_found" {
		t.Fatalf("%+v", d)
	}
}

func TestDiagnose_요청과_약하게_겹치면_선택_단계에서_멈춘다(t *testing.T) {
	s := store(t)
	m := save(t, s, memory.KindLesson, "codex exec는 stdin을 닫는다", "", memory.ScopeGlobal, false)
	d := Diagnose(s, config.Default(), DiagnoseInput{ID: m.ID, Scope: here, Prompt: "codex 설치 방법"})
	st := stages(d)[StageSelected]
	if d.StoppedAt != StageSelected || d.LastProven != StageEligible || st.Code != "weak_match" || st.Args[0] != "codex" {
		t.Fatalf("%+v", d)
	}
	// 두 문턱의 값과 재료: 맞은 낱말, 요청 낱말, 점수(문턱), 맞은 비율(문턱).
	if len(st.Args) != 6 || st.Args[1] != "codex 설치 방법" || st.Args[3] != fmt.Sprintf("%.2f", config.DefaultRecallMinScore) || st.Args[4] != "0.33" ||
		st.Args[5] != fmt.Sprintf("%.2f", config.DefaultRecallMinCoverage) {
		t.Fatalf("args %v", st.Args)
	}
}

// 이 세션에 이미 요청 회상으로 넣은 기억은 다시 넣지 않는다: 진단이 그 까닭을 말한다.
func TestDiagnose_이_세션에_이미_보인_기억은_already_shown이다(t *testing.T) {
	s := store(t)
	cfg := config.Default()
	m := save(t, s, memory.KindRule, "이 앱은 pnpm으로 설치한다", "npm install을 쓰지 않는다", here, true)
	SessionBundle(s, cfg, here, "claude", "A")
	RecordShown(s, "claude", "A", PromptInjectionFor(s, cfg, here, "pnpm으로 설치해 줘", "claude", "A", Options{}))
	d := Diagnose(s, cfg, DiagnoseInput{ID: m.ID, Scope: here, Prompt: "pnpm으로 설치해 줘", Host: "claude", Session: "A"})
	if st := stages(d)[StageSelected]; st.Status != Yes || st.Code != "matched" {
		t.Fatalf("selected %+v", st)
	}
	if st := stages(d)[StageInjected]; st.Status != No || st.Code != "already_shown" {
		t.Fatalf("injected %+v", st)
	}
}

func TestDiagnose_적격이_아닌_까닭(t *testing.T) {
	s := store(t)
	v, _ := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포 동결", Retention: memory.RetentionVolatile, TTLDays: 1, Source: memory.Source{Kind: memory.SourceSession}})
	old, _ := s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: "기본 모델 sonnet", Topic: "기본 모델", Source: memory.Source{Kind: memory.SourceSession}})
	s.Now = func() time.Time { return time.Date(2026, 10, 29, 9, 0, 0, 0, time.Local) }
	// 저장 때 대체하지 않는 새 판(이번 주만)은 읽을 때 고르기(TopicHeads)로 옛 판을 가린다.
	cur, _ := s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: "기본 모델 opus", Topic: "기본 모델", Retention: memory.RetentionVolatile,
		Source: memory.Source{Kind: memory.SourceSession}})
	other := save(t, s, memory.KindFact, "다른 앱 사실", "", "project:github.com/me/other", false)
	for id, want := range map[string]string{v.ID: "expired", old.ID: "superseded", other.ID: "out_of_scope"} {
		d := Diagnose(s, config.Default(), DiagnoseInput{ID: id, Scope: here, Prompt: "배포 동결 기본 모델"})
		if st := stages(d)[StageEligible]; d.StoppedAt != StageEligible || st.Code != want {
			t.Errorf("%s: %+v", want, d)
		}
	}
	d := Diagnose(s, config.Default(), DiagnoseInput{ID: old.ID, Scope: here})
	if stages(d)[StageEligible].Args[0] != cur.ID {
		t.Fatalf("superseded by %v", stages(d)[StageEligible].Args)
	}
}

func TestDiagnose_Orbit_Code_세션의_여섯_단계(t *testing.T) {
	s := store(t)
	m := save(t, s, memory.KindLesson, "codex exec는 stdin을 닫는다", "", memory.ScopeGlobal, false)
	prompt := "codex exec stdin 닫힘 문제"
	in := DiagnoseInput{ID: m.ID, Scope: here, Prompt: prompt, Host: "orbit-code", Session: "c-1"}
	d := Diagnose(s, config.Default(), in)
	if st := stages(d); st[StageSelected].Status != Yes || st[StageInjected].Code != "would_inject" || st[StageDelivered].Code != "no_record" {
		t.Fatalf("before: %+v", d)
	}
	ev := func(kind string, data map[string]any) {
		if err := s.RecordEvent(memory.Event{Kind: kind, Host: "orbit-code", Session: "c-1"}, data); err != nil {
			t.Fatal(err)
		}
	}
	ev(memory.EventInjectPrompt, map[string]any{"memoryIds": []string{m.ID}})
	if d := Diagnose(s, config.Default(), in); d.StoppedAt != StageDelivered || stages(d)[StageDelivered].Code != "not_delivered" {
		t.Fatalf("injected only: %+v", d)
	}
	ev(memory.EventInjectDelivered, map[string]any{"memoryIds": []string{m.ID}})
	ev(memory.EventToolPrefix+"memory_get", map[string]any{"ids": []string{m.ID}})
	d = Diagnose(s, config.Default(), in)
	if d.LastProven != StageUsed || d.StoppedAt != "" || stages(d)[StageUsed].Code != "opened" {
		t.Fatalf("after: %+v", d)
	}
}

func TestDiagnose_훅_호스트는_돌려준_문맥이_전달이고_확정_규칙은_세션_묶음으로_본다(t *testing.T) {
	s := store(t)
	r := save(t, s, memory.KindRule, "답은 한글 존댓말", "반말 금지", memory.ScopeGlobal, true)
	if _, err := s.Pin(r.ID, memory.Who{}); err != nil { // 세션 시작에는 상시 핵심만 든다(S3-R6)
		t.Fatal(err)
	}
	if err := s.RecordEvent(memory.Event{Kind: memory.EventInjectSessionStart, Host: "claude", Session: "s-1"}, map[string]any{"memoryIds": []string{r.ID}}); err != nil {
		t.Fatal(err)
	}
	d := Diagnose(s, config.Default(), DiagnoseInput{ID: r.ID, Scope: here, Host: "claude", Session: "s-1"})
	st := stages(d)
	if d.Path != "session_rules" || st[StageSelected].Code != "in_bundle" || st[StageInjected].Status != Yes || st[StageDelivered].Code != "hook_returned" {
		t.Fatalf("%+v", d)
	}
}

func TestDiagnose_상시_핵심_확정_규칙은_세션_묶음으로_보고_전달은_세션에_한_번이다(t *testing.T) {
	s := store(t)
	r := save(t, s, memory.KindRule, "답은 한글 존댓말", "반말 금지", memory.ScopeGlobal, true)
	if _, err := s.Pin(r.ID, memory.Who{}); err != nil { // 세션 시작에는 상시 핵심만 든다(S3-R6)
		t.Fatal(err)
	}
	in := DiagnoseInput{ID: r.ID, Scope: here, Prompt: "날씨 알려줘", Host: "orbit-code", Session: "c-1"}
	if err := s.RecordEvent(memory.Event{Kind: memory.EventInjectSessionStart, Host: "orbit-code", Session: "c-1"}, map[string]any{"memoryIds": []string{r.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEvent(memory.Event{Kind: memory.EventInjectDelivered, Host: "orbit-code", Session: "c-1"}, map[string]any{"memoryIds": []string{r.ID}}); err != nil {
		t.Fatal(err)
	}
	d := Diagnose(s, config.Default(), in)
	st := stages(d)
	if d.Path != "session_rules" || st[StageSelected].Code != "in_bundle" || st[StageDelivered].Status != Yes || d.StoppedAt != "" {
		t.Fatalf("%+v", d)
	}
}

func TestPrompt_잘린_규칙_본문은_전문_자리를_알린다(t *testing.T) {
	s := store(t)
	r := save(t, s, memory.KindRule, "배포 전 확인 순서", strings.Repeat("확인한다. ", 200), memory.ScopeGlobal, true)
	inj := PromptInjectionWith(s, config.Default(), here, "배포 전 확인 순서 알려줘", Options{})
	if !slices.Contains(inj.MemoryIDs, r.ID) || !strings.Contains(inj.Text, "memory_get") {
		t.Fatalf("%s", inj.Text)
	}
}

// 기획 · orbit-mem M-4: 훅 호스트(claude·codex)만 「훅이 돌려줌」으로 보고, 이름을 모르는 다른 호스트는 모두
// inject delivered로 알려야 전달됨이다.
func TestDiagnose_훅_호스트_밖의_호스트는_전달_사건이_있어야_전달됨이다(t *testing.T) {
	for host, want := range map[string]string{"claude": "hook_returned", "codex": "hook_returned", "openclaw": "not_delivered", "host-x": "not_delivered"} {
		s := store(t)
		m := save(t, s, memory.KindLesson, "codex exec는 stdin을 닫는다", "", memory.ScopeGlobal, false)
		if err := s.RecordEvent(memory.Event{Kind: memory.EventInjectPrompt, Host: host, Session: "h-1"}, map[string]any{"memoryIds": []string{m.ID}}); err != nil {
			t.Fatal(err)
		}
		d := Diagnose(s, config.Default(), DiagnoseInput{ID: m.ID, Scope: here, Prompt: "codex exec stdin 닫힘 문제", Host: host, Session: "h-1"})
		if got := stages(d)[StageDelivered].Code; got != want {
			t.Errorf("%s: delivered = %s, want %s", host, got, want)
		}
	}
}
