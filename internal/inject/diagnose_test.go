package inject

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
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
}

func TestDiagnose_적격이_아닌_까닭(t *testing.T) {
	s := store(t)
	v, _ := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "배포 동결", Retention: memory.RetentionVolatile, TTLDays: 1, Source: memory.Source{Kind: memory.SourceSession}})
	old, _ := s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: "기본 모델 sonnet", Topic: "기본 모델", Source: memory.Source{Kind: memory.SourceSession}})
	s.Now = func() time.Time { return time.Date(2026, 10, 29, 9, 0, 0, 0, time.Local) }
	cur, _ := s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: "기본 모델 opus", Topic: "기본 모델", Source: memory.Source{Kind: memory.SourceSession}})
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
	in := DiagnoseInput{ID: m.ID, Scope: here, Prompt: prompt, Host: "orbit-code", Session: "c-1", Options: Options{Brief: true}}
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
	if err := s.RecordEvent(memory.Event{Kind: memory.EventInjectSessionStart, Host: "claude", Session: "s-1"}, map[string]any{"memoryIds": []string{r.ID}}); err != nil {
		t.Fatal(err)
	}
	d := Diagnose(s, config.Default(), DiagnoseInput{ID: r.ID, Scope: here, Host: "claude", Session: "s-1"})
	st := stages(d)
	if d.Path != "session_rules" || st[StageSelected].Code != "in_bundle" || st[StageInjected].Status != Yes || st[StageDelivered].Code != "hook_returned" {
		t.Fatalf("%+v", d)
	}
}

func TestDiagnose_짧은_꼴의_확정_규칙은_세션_묶음으로_보고_전달은_세션에_한_번이다(t *testing.T) {
	s := store(t)
	r := save(t, s, memory.KindRule, "답은 한글 존댓말", "반말 금지", memory.ScopeGlobal, true)
	in := DiagnoseInput{ID: r.ID, Scope: here, Prompt: "날씨 알려줘", Host: "orbit-code", Session: "c-1", Options: Options{Brief: true}}
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

func TestPrompt_짧은_꼴에서_잘린_규칙_본문은_전문_자리를_알린다(t *testing.T) {
	s := store(t)
	r := save(t, s, memory.KindRule, "배포 전 확인 순서", strings.Repeat("확인한다. ", 200), memory.ScopeGlobal, true)
	inj := PromptInjectionWith(s, config.Default(), here, "배포 전 확인 순서 알려줘", Options{Brief: true})
	if !slices.Contains(inj.MemoryIDs, r.ID) || !strings.Contains(inj.Text, "memory_get") {
		t.Fatalf("%s", inj.Text)
	}
}
