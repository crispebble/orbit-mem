package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// AU-6: 감사 목록 CLI, 하네스 개선 후보 읽기(실험실용), 설정의 기준 횟수, CLI의 쓰는 쪽.

func TestAudit_감사_목록과_되돌리기와_확인함(t *testing.T) {
	home(t)
	s := seed(t)
	clock := time.Now().Add(-72 * time.Hour) // 확인함은 지금 시각으로 남는다: 사건은 그 전이다
	s.Now = func() time.Time { return clock }
	in := memory.SaveInput{Kind: "lesson", Title: "plist는 지우고 다시 건다", Source: memory.Source{Kind: "session"}}
	m, _ := s.Save(in)
	clock = clock.Add(24 * time.Hour)
	if got, _ := s.Save(in); got.State != memory.StateConfirmed {
		t.Fatalf("state %s", got.State)
	}
	for _, sess := range []string{"a", "b", "c"} {
		if err := s.RecordCorrection(memory.CorrectionInput{Quote: "리뷰어가 근거 없이 승인했어", Session: sess, TargetKind: memory.TargetKindRole, Target: "orbit:reviewer"}); err != nil {
			t.Fatal(err)
		}
	}

	out, _, code := run(t, "", "audit", "--json", "--days", "3650")
	var a memory.Audit
	if code != 0 || json.Unmarshal([]byte(out), &a) != nil || len(a.Changes) != 1 || len(a.Corrections) != 1 {
		t.Fatalf("audit json (%d): %s", code, out)
	}
	out, _, code = run(t, "", "audit", "--days", "3650")
	if code != 0 || !strings.Contains(out, a.Changes[0].LedgerID) || !strings.Contains(out, "orbit:reviewer") {
		t.Fatalf("audit text: %s", out)
	}

	// 실험실(개발 도구)은 하네스 개선 후보를 JSON으로 읽기만 한다.
	out, _, code = run(t, "", "audit", "harness", "--json")
	var hc []memory.RepeatedCorrection
	if code != 0 || json.Unmarshal([]byte(out), &hc) != nil || len(hc) != 1 || hc[0].Target != "orbit:reviewer" || hc[0].TargetKind != "role" || hc[0].Sessions != 3 {
		t.Fatalf("harness: %s", out)
	}

	if _, errOut, code := run(t, "", "audit", "revert", a.Changes[0].LedgerID); code != 0 {
		t.Fatalf("revert: %s", errOut)
	}
	if got, _ := s.Get(m.ID); got.State != memory.StateCandidate {
		t.Fatalf("되돌린 뒤 %s", got.State)
	}
	if _, errOut, code := run(t, "", "audit", "dismiss", a.Corrections[0].Key); code != 0 {
		t.Fatalf("dismiss: %s", errOut)
	}
	out, _, _ = run(t, "", "audit", "harness", "--json")
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("확인함 뒤 harness: %s", out)
	}
}

func TestConfig_기준_횟수는_설정으로_받는다(t *testing.T) {
	home(t)
	if _, errOut, code := run(t, "", "config", "set", "confirmFollowedVerified", "4"); code != 0 {
		t.Fatalf("set: %s", errOut)
	}
	if _, _, code := run(t, "", "config", "set", "correctionRepeatSessions", "0"); code == 0 {
		t.Fatal("0을 받았습니다")
	}
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Policy.FollowedVerified != 4 || s.Policy.SeenDays != 2 || s.Policy.CorrectionSessions != 3 {
		t.Fatalf("policy = %+v", s.Policy)
	}
}

func TestEdit_회고_실행_안의_CLI는_사용자_인용_기억을_고치지_못한다(t *testing.T) {
	home(t)
	s := seed(t)
	rule, _ := s.Save(memory.SaveInput{Kind: "rule", Title: "답은 한글", Source: memory.Source{Kind: "quote", Excerpt: "답은 한글로 해"}})
	if _, errOut, code := run(t, "", "edit", rule.ID, "--title", "답은 한글 존댓말"); code != 0 {
		t.Fatalf("사람 CLI: %s", errOut)
	}
	t.Setenv("ORBIT_RETRO_ID", "r-1")
	if _, _, code := run(t, "", "edit", rule.ID, "--title", "답은 영어"); code == 0 {
		t.Fatal("회고 실행 안에서 사용자 인용 기억을 고쳤습니다")
	}
}

// AU-5: 서로 다른 작업 셋에서 나온 절차 교훈은 하네스 개선 후보로 감사 목록·harness --json에 보이고, 확인함으로 닫힌다.
func TestAudit_작업_셋에서_나온_절차는_하네스_후보다(t *testing.T) {
	home(t)
	s := seed(t)
	clock := time.Now().Add(-time.Hour)
	s.Now = func() time.Time { return clock }
	m, err := s.Save(memory.SaveInput{Kind: "lesson", Title: "마이그레이션은 dry-run부터", Tags: []string{"procedure"}, Source: memory.Source{Kind: memory.SourceWork, Ref: "w-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"w-2", "w-3"} {
		clock = clock.Add(time.Minute)
		s.AddSource(m.ID, memory.Source{Kind: memory.SourceWork, Ref: ref})
	}
	out, _, code := run(t, "", "audit", "harness", "--json")
	var hc []memory.RepeatedCorrection
	if code != 0 || json.Unmarshal([]byte(out), &hc) != nil || len(hc) != 1 || hc[0].TargetKind != "procedure" || hc[0].Target != m.ID || hc[0].Sessions != 3 || !hc[0].Harness {
		t.Fatalf("harness: %s", out)
	}
	out, _, code = run(t, "", "audit")
	if code != 0 || !strings.Contains(out, "correction:procedure:"+m.ID) || !strings.Contains(out, "마이그레이션은 dry-run부터") || !strings.Contains(out, i18n.T("cli.audit.works", 3)) {
		t.Fatalf("audit text: %s", out)
	}
	if _, errOut, code := run(t, "", "audit", "dismiss", hc[0].Key); code != 0 {
		t.Fatalf("dismiss: %s", errOut)
	}
	if out, _, _ = run(t, "", "audit", "harness", "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("확인함 뒤 harness: %s", out)
	}
}
