package main

// S4(설계 · orbit-mem 2판 11절): place --plan은 AI가 만든 이전 계획을 미리 보거나 적용한다.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
)

func TestPlacePlan_미리_보고_적용한다(t *testing.T) {
	home(t)
	cfg := config.Default()
	cfg.Products = place.Map{"atlas": {Match: []string{"github.com/onidot-labs/atlas"}}}
	if err := config.Save(paths().Config(), cfg); err != nil {
		t.Fatal(err)
	}
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.SaveWith(memory.SaveInput{Kind: memory.KindLesson, Title: "Atlas 댓글은 밑줄", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "밑줄"}, Who: memory.Who{Actor: memory.ActorAI}})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	plan := memory.Plan{Name: "t", Items: []memory.PlanItem{{ID: m.ID, Version: m.Version, AppliesTo: "product:atlas", Topic: "atlas 댓글 표시", Basis: "rule1:atlas"}}}
	b, _ := json.Marshal(plan)
	file := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	var rep memory.PlanReport
	out, errOut, code := run(t, "", "place", "--plan", file, "--dry-run", "--json")
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || len(rep.Applied) != 1 || rep.Applied[0].LedgerID != "" {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	out, errOut, code = run(t, "", "place", "--plan", file, "--json")
	rep = memory.PlanReport{}
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || len(rep.Applied) != 1 || rep.Applied[0].LedgerID == "" {
		t.Fatalf("apply: %d %s %s", code, out, errOut)
	}
	out, _, code = run(t, "", "show", m.ID, "--json")
	var got struct{ Items []memory.Memory }
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Items) != 1 || got.Items[0].AppliesTo != "product:atlas" || got.Items[0].Topic != "atlas 댓글 표시" {
		t.Fatalf("show: %d %s", code, out)
	}
}

// S4-8: place --journals는 살아 있는 일지를 활동 기록으로 옮긴다(미리 보기는 바꾸지 않는다).
func TestPlaceJournals_일지를_활동_기록으로_옮긴다(t *testing.T) {
	home(t)
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := s.SaveWith(memory.SaveInput{Kind: memory.KindJournal, Title: "2026-11-04 하루 회고", Body: "정리만 했다", Source: memory.Source{Kind: memory.SourceSession, Ref: "r"}, Who: memory.Who{Actor: memory.ActorAI}})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	var rep memory.PlanReport
	out, errOut, code := run(t, "", "place", "--journals", "--dry-run", "--json")
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || len(rep.Applied) != 1 || rep.Applied[0].LedgerID != "" {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	out, errOut, code = run(t, "", "place", "--journals")
	if code != 0 || !strings.Contains(out, "1") {
		t.Fatalf("apply: %d %s %s", code, out, errOut)
	}
	out, _, code = run(t, "", "activity", "--project", "all", "--json")
	if code != 0 || !strings.Contains(out, "journal:"+j.ID) || !strings.Contains(out, "정리만 했다") {
		t.Fatalf("activity: %d %s", code, out)
	}
}

// S4-12: audit은 「A — B」 제목에 두 주제를 붙인 규칙·결정을 보인다(제품 이름은 설정의 제품 지도).
func TestAudit_두_주제를_붙인_제목을_보인다(t *testing.T) {
	home(t)
	cfg := config.Default()
	cfg.Products = place.Map{"atlas": {Match: []string{"github.com/onidot-labs/atlas"}}, "forma": {Match: []string{"github.com/onidot-labs/forma"}}}
	if err := config.Save(paths().Config(), cfg); err != nil {
		t.Fatal(err)
	}
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.SaveWith(memory.SaveInput{Kind: memory.KindRule, Title: "Atlas 문서는 Space에 쓴다 — Forma 부품은 생성기에서", Topic: "x", Source: memory.Source{Kind: memory.SourceQuote, Excerpt: "x"}})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "", "audit")
	if code != 0 || !strings.Contains(out, m.ID) || !strings.Contains(out, "products") {
		t.Fatalf("audit: %d %s %s", code, out, errOut)
	}
}

// S4 검토 A10: 계획 파일의 모르는 칸은 거절하고(오타가 조용히 무시되지 않게), 사람용 출력은 건너뛴 이유를 한 줄로 센다.
// 활동 기록의 요약은 길면 잘라서 보인다.
func TestPlacePlan_모르는_칸은_거절하고_건너뛴_이유를_한_줄로_보인다(t *testing.T) {
	home(t)
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.SaveWith(memory.SaveInput{Kind: memory.KindLesson, Title: "교훈", Source: memory.Source{Kind: memory.SourceSession, Ref: "r"}, Who: memory.Who{Actor: memory.ActorAI}})
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := s.SaveWith(memory.SaveInput{Kind: memory.KindJournal, Title: "하루", Body: strings.Repeat("가", 2000), Source: memory.Source{Kind: memory.SourceSession, Ref: "r"}, Who: memory.Who{Actor: memory.ActorAI}})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"items":[{"id":"`+m.ID+`","appliesTo":"user","topik":"오타"}]}`), 0o600)
	if _, errOut, code := run(t, "", "place", "--plan", bad); code == 0 || !strings.Contains(errOut, "topik") {
		t.Fatalf("unknown field accepted: %d %s", code, errOut)
	}
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"items":[{"id":"`+m.ID+`","appliesTo":"user"},{"id":"`+m.ID+`","appliesTo":"product:nope"}]}`), 0o600)
	out, errOut, code := run(t, "", "place", "--plan", good)
	if code != 0 || !strings.Contains(out, "already there 1") || !strings.Contains(out, "unknown product 1") {
		t.Fatalf("summary: %d %s %s", code, out, errOut)
	}
	if _, _, code := run(t, "", "place", "--journals"); code != 0 {
		t.Fatal("journals")
	}
	out, _, _ = run(t, "", "activity", "--project", "all", "--json")
	var res struct {
		Items []struct{ Session, Summary string }
	}
	if json.Unmarshal([]byte(out), &res) != nil || len(res.Items) != 1 || res.Items[0].Session != "journal:"+j.ID ||
		len([]rune(res.Items[0].Summary)) > 601 || len([]rune(res.Items[0].Summary)) < 500 {
		t.Fatalf("summary clip: %s", out[:min(len(out), 300)])
	}
}

// 재검토 N2: 계획 파일 뒤에 다른 JSON이나 쓰레기가 붙어 있으면 거절한다(첫 값만 읽고 0건으로 지나가지 않게).
func TestPlacePlan_뒤에_붙은_데이터는_거절한다(t *testing.T) {
	home(t)
	dir := t.TempDir()
	for name, body := range map[string]string{"쓰레기": `{"items":[]} garbage`, "두 계획": `{"items":[]}{"items":[]}`} {
		file := filepath.Join(dir, "plan.json")
		os.WriteFile(file, []byte(body), 0o600)
		if _, errOut, code := run(t, "", "place", "--plan", file, "--dry-run"); code == 0 {
			t.Fatalf("%s accepted: %s", name, errOut)
		}
	}
}
