package main

// S4(설계 · orbit-mem 2판 11절): place --plan은 AI가 만든 이전 계획을 미리 보거나 적용한다.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
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
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got.Items) != 1 || got.Items[0].Scope != "product:atlas" || got.Items[0].Topic != "atlas 댓글 표시" {
		t.Fatalf("show: %d %s", code, out)
	}
}
