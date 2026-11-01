package retro

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// legacyEnv는 옛 기억 판정을 켠 환경에 기억을 심는다.
//
//	지금 확정 기억 하나(git), 옮겨 온 옛 기억 다섯(orbit 셋·git 하나·studio 하나), 옛 일지 하나(판정 대상 아님)
func legacyEnv(t *testing.T, now time.Time) (*env, map[string]memory.Memory) {
	t.Helper()
	e := setup(t, now)
	e.r.Cfg.LegacyMemories, e.r.Cfg.LegacyBatches = 10, 1
	e.r.Cfg.RetiredNames = []string{"onidot-orbit-plugin", "doraft-labs"}
	ms := map[string]memory.Memory{}
	at := now.AddDate(0, 0, -30)
	save := func(name, kind, state string, tags ...string) {
		at = at.Add(time.Minute)
		e.s.Now = func() time.Time { return at }
		m, err := e.s.Save(memory.SaveInput{Kind: kind, Title: name, Body: name + " 본문", Tags: tags, Scope: memory.ScopeGlobal,
			State: state, Source: memory.Source{Kind: memory.SourceSession}})
		if err != nil {
			t.Fatal(err)
		}
		ms[name] = m
	}
	save("커밋은 경로를 명시해서 스테이징한다", memory.KindRule, memory.StateConfirmed, "git")
	save("회고는 새벽 네 시에 돈다", memory.KindFact, memory.StateCandidate, "orbit", "imported:claude-memory")
	save("커밋할 때 경로를 명시해서 스테이징한다 git add 금지", memory.KindRule, memory.StateConfirmed, "git", "imported:server-knowledge", "space:onidot")
	save("onidot-orbit-plugin 저장소에서 스킬을 고친다", memory.KindFact, memory.StateCandidate, "orbit", "imported:server-knowledge")
	save("화면 크기는 토큰으로 정한다", memory.KindRule, memory.StateConfirmed, "studio", "imported:server-knowledge")
	save("회고 결과는 제안으로만 반영한다", memory.KindDecision, memory.StateCandidate, "orbit", "imported:claude-memory")
	save("2026-09-01 일지: 옛 일", memory.KindJournal, memory.StateConfirmed, "imported:server-knowledge")
	e.s.Now = func() time.Time { return now }
	return e, ms
}

func runLegacyOnce(t *testing.T, e *env, sub Submission) (text, report string, res Result) {
	t.Helper()
	e.ai = func(id string) error {
		text = readAll(t, e, id)
		if sub.Summary == "" {
			sub.Summary = "옛 기억 판정"
		}
		var err error
		report, err = Submit(e.s, e.r.Paths, id, sub)
		return err
	}
	res, err := e.r.RunLegacy(context.Background())
	if err != nil {
		t.Fatalf("옛 기억 판정 %+v %v", res, err)
	}
	return text, report, res
}

func legacyEvents(t *testing.T, e *env) map[string]map[string]any {
	t.Helper()
	rows, err := e.s.DB().Query(`SELECT data FROM events WHERE kind=?`, memory.EventLegacyJudged)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var data string
		rows.Scan(&data)
		var d map[string]any
		json.Unmarshal([]byte(data), &d)
		out[d["memoryId"].(string)] = d
	}
	return out
}

// 설계 · Orbit 3차 O3-3 끝 기준 1: 옛 기억을 대표 태그 클러스터별로 묶어 판정하고, 확정·합치기·낡음은 근거와 함께
// 제안으로, 그대로 둠은 사건으로 남는다. 판정이 빠진 기억은 다음 묶음에 다시 나오고, 모두 판정하면 더 돌지 않는다.
func TestLegacy_옛_기억을_클러스터별로_판정해_제안과_사건을_남긴다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, ms := legacyEnv(t, now)
	cur := ms["커밋은 경로를 명시해서 스테이징한다"]
	cand, dup, old := ms["회고는 새벽 네 시에 돈다"], ms["커밋할 때 경로를 명시해서 스테이징한다 git add 금지"], ms["onidot-orbit-plugin 저장소에서 스킬을 고친다"]
	keep, missing := ms["화면 크기는 토큰으로 정한다"], ms["회고 결과는 제안으로만 반영한다"]

	text, report, res := runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{
		{MemoryID: cand.ID, Verdict: "confirm", Reason: "지금 회고 시각과 같다"},
		{MemoryID: dup.ID, Verdict: "merge", With: []string{cur.ID}, MergedTitle: "커밋은 경로를 명시해서 스테이징한다", MergedBody: "git add -A 금지", Reason: "지금 규칙과 같은 말"},
		{MemoryID: old.ID, Verdict: "stale", Reason: "보관한 저장소만 다룬다"},
		{MemoryID: keep.ID, Verdict: "keep", Reason: "아직 맞고 확정 기억이다"},
		{MemoryID: cur.ID, Verdict: "stale", Reason: "묶음 밖"},
	}})
	if res.Status != StatusDone || res.Kind != KindLegacy {
		t.Fatalf("결과 %+v", res)
	}
	for _, want := range []string{"## 옛 기억 판정", "대상 5개 중 판정 0개", "### 클러스터 「orbit」", "### 클러스터 「git」", "### 클러스터 「그 밖」",
		cand.ID, dup.ID, old.ID, keep.ID, missing.ID, "## 지금 있는 기억", cur.ID, "## 보관한 저장소·옛 이름", "onidot-orbit-plugin"} {
		if !strings.Contains(text, want) {
			t.Errorf("재료에 %q가 없습니다", want)
		}
	}
	if strings.Contains(section(text, "## 옛 기억 판정"), cur.ID) || strings.Contains(text, "옛 일") {
		t.Errorf("지금 기억과 일지는 판정 대상이 아닙니다")
	}
	if strings.Contains(text, "## 이미 있는 기억") {
		t.Errorf("옛 기억 판정은 공통 기억 목록 대신 겹칠 수 있는 기억만 싣습니다")
	}
	if t.Failed() {
		t.Fatalf("재료:\n%s", text)
	}

	// 합치기·낡음은 승인 없이 적용되고 원장에 남는다(A2). 증거 없는 확정은 사용자 결정을 기다린다.
	confirms, merges, stales := pendingOf(t, e, memory.ProposalConfirm), autoOf(t, e, memory.ProposalMerge), autoOf(t, e, memory.ProposalStale)
	if got, _ := e.s.Get(old.ID); got.State != memory.StateStale {
		t.Errorf("낡음이 적용되지 않았습니다: %s", got.State)
	}
	if all, _ := e.s.Ledger(memory.LedgerFilter{}); len(all) != 2 {
		t.Errorf("원장 %d줄(합치기·낡음 둘이어야 함)", len(all))
	}
	if len(confirms) != 1 || confirms[0].MemoryIDs[0] != cand.ID || !strings.HasPrefix(confirms[0].Title, "옛 기억 확정: ") || !strings.Contains(confirms[0].Body, "지금 회고 시각과 같다") {
		t.Errorf("확정 %+v", confirms)
	}
	if len(merges) != 1 || strings.Join(merges[0].MemoryIDs, ",") != cur.ID+","+dup.ID || merges[0].Payload.MergedBody != "git add -A 금지" || !strings.HasPrefix(merges[0].Title, "옛 기억 합치기: ") {
		t.Errorf("합치기 %+v", merges)
	}
	if len(stales) != 1 || stales[0].MemoryIDs[0] != old.ID || !strings.Contains(stales[0].Body, "보관한 저장소만 다룬다") {
		t.Errorf("낡음 %+v", stales)
	}
	ev := legacyEvents(t, e)
	if len(ev) != 4 || ev[keep.ID]["verdict"] != "keep" || ev[keep.ID]["reason"] != "아직 맞고 확정 기억이다" || ev[old.ID]["proposalId"] != stales[0].ID {
		t.Errorf("판정 사건 %+v", ev)
	}
	for _, want := range []string{"판정 4건", "판정이 없는 옛 기억 1건", cur.ID + ": 이번 묶음의 옛 기억이 아닙니다"} {
		if !strings.Contains(report, want) {
			t.Errorf("보고에 %q가 없습니다:\n%s", want, report)
		}
	}

	// 판정이 빠진 기억만 다음 묶음에 나온다.
	text, _, _ = runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{{MemoryID: missing.ID, Verdict: "keep", Reason: "후보로 둔다"}}})
	if batch := section(text, "## 옛 기억 판정"); !strings.Contains(batch, missing.ID) || strings.Contains(batch, cand.ID) || !strings.Contains(batch, "대상 5개 중 판정 4개") {
		t.Fatalf("둘째 묶음:\n%s", text)
	}
	calls := len(e.calls)
	if res, err := e.r.RunLegacy(context.Background()); err != nil || res.Status != StatusEmpty || len(e.calls) != calls {
		t.Fatalf("모두 판정하면 호스트를 부르지 않는다: %+v %v", res, err)
	}
	if c, _ := e.s.CountLegacy(); c.Left != 0 || c.Judged != 5 || c.By["keep"] != 2 {
		t.Fatalf("옛 기억 수 %+v", c)
	}
}

// 판정마다 근거가 필요하고, 합치기는 합칠 기억과 합친 제목이 있어야 한다. 받지 못한 판정은 다음 묶음에 다시 나온다.
// 확정 기억에 confirm을 내면 그대로 둠으로 남는다. 이미 대기 중인 같은 제안이 있으면 새로 쌓지 않고 판정만 남긴다.
func TestLegacy_받지_못한_판정과_겹치는_제안(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, ms := legacyEnv(t, now)
	cand, dup, old := ms["회고는 새벽 네 시에 돈다"], ms["커밋할 때 경로를 명시해서 스테이징한다 git add 금지"], ms["onidot-orbit-plugin 저장소에서 스킬을 고친다"]
	keep, other := ms["화면 크기는 토큰으로 정한다"], ms["회고 결과는 제안으로만 반영한다"]
	pre, err := e.s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "후보 확정: 회고 결과", MemoryIDs: []string{other.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, report, _ := runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{
		{MemoryID: cand.ID, Verdict: "confirm"},
		{MemoryID: dup.ID, Verdict: "merge", Reason: "같은 말"},
		{MemoryID: old.ID, Verdict: "drop", Reason: "?"},
		{MemoryID: keep.ID, Verdict: "confirm", Reason: "맞다"},
		{MemoryID: other.ID, Verdict: "confirm", Reason: "지금도 그렇다"},
	}})
	ev := legacyEvents(t, e)
	if len(ev) != 2 || ev[keep.ID]["verdict"] != "keep" || ev[other.ID]["proposalId"] != pre.ID {
		t.Fatalf("판정 사건 %+v (%s)", ev, report)
	}
	if n := len(pendingOf(t, e, memory.ProposalConfirm)); n != 1 {
		t.Errorf("대기 중인 확정 제안은 하나여야 합니다: %d", n)
	}
	for _, want := range []string{cand.ID + ": 근거(reason)", dup.ID + ": 합치기는 합칠 기억(with)과 합친 제목", old.ID + ": verdict는", "판정이 없는 옛 기억 3건"} {
		if !strings.Contains(report, want) {
			t.Errorf("보고에 %q가 없습니다:\n%s", want, report)
		}
	}
	if c, _ := e.s.CountLegacy(); c.Left != 3 || c.Total != 5 {
		t.Errorf("옛 기억 수 %+v", c)
	}
}

// 설계 · Orbit 3차 O3-3 끝 기준 2(A2로 바뀜): 자동 정리는 원장에 남고 되돌릴 수 있다. 합친 기억은 옛 표식만 빼고 태그를 잇는다.
func TestLegacy_승인한_합치기는_원장에_남고_되돌린다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, ms := legacyEnv(t, now)
	cur, dup := ms["커밋은 경로를 명시해서 스테이징한다"], ms["커밋할 때 경로를 명시해서 스테이징한다 git add 금지"]
	runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{
		{MemoryID: dup.ID, Verdict: "merge", With: []string{cur.ID}, MergedTitle: "커밋은 경로를 명시해서 스테이징한다", MergedBody: "git add -A 금지", Reason: "같은 말"}}})
	// 합치기는 승인 없이 적용되고 원장에 남는다(A2).
	merges := autoOf(t, e, memory.ProposalMerge)
	if len(merges) != 1 {
		t.Fatalf("자동 합치기 %+v", merges)
	}
	var res struct{ Created, LedgerID string }
	all, _ := e.s.Ledger(memory.LedgerFilter{})
	for _, l := range all {
		if l.ProposalID == merges[0].ID {
			res.LedgerID = l.ID
			res.Created = strings.Join(l.Before.Created, ",")
		}
	}
	// 지금 기억의 제목을 살려 합치면 그 기억에 합친 본문·태그가 새 판으로 쌓인다.
	merged, _ := e.s.Get(cur.ID)
	if res.Created != "" || merged.Body != "git add -A 금지" || strings.Join(merged.Tags, ",") != "git,space:onidot" || merged.Kind != memory.KindRule {
		t.Errorf("합친 기억 created=%q %+v", res.Created, merged)
	}
	if res.LedgerID == "" {
		t.Fatal("원장 줄이 없습니다")
	}
	if _, err := e.s.RevertChange(res.LedgerID, memory.Who{}, "감사 목록에서 되돌림"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{cur.ID, dup.ID} {
		if m, _ := e.s.Get(id); m.State != memory.StateConfirmed || m.SupersededBy != "" {
			t.Errorf("되돌린 뒤 %s %+v", id, m)
		}
	}
	if m, _ := e.s.Get(cur.ID); m.Body != cur.Body || strings.Join(m.Tags, ",") != "git" {
		t.Errorf("되돌린 지금 기억 %+v", m)
	}
}

// 예약 회고(orb retro run)는 하루에 옛 기억 묶음을 legacyBatches개까지 돌린다. 같은 날 다시 돌려도 더 돌지 않고, 0이면 끈다.
func TestLegacy_예약_회고는_하루_묶음_수까지_돈다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, _ := legacyEnv(t, now)
	e.r.Cfg.LegacyMemories, e.r.Cfg.LegacyBatches = 2, 2
	legacyRuns := func() int {
		n := 0
		for _, c := range e.calls {
			if strings.Contains(c.Stdin+strings.Join(c.Args, " "), "옛 기억 판정") {
				n++
			}
		}
		return n
	}
	e.ai = func(id string) error {
		_, err := Submit(e.s, e.r.Paths, id, Submission{Summary: "판정 없음"})
		return err
	}
	if _, err := e.r.CatchUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := legacyRuns(); n != 2 {
		t.Fatalf("첫 예약 실행 옛 기억 묶음 %d번, 2번이어야 합니다", n)
	}
	if _, err := e.r.CatchUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := legacyRuns(); n != 2 {
		t.Fatalf("같은 날 다시 돌려도 더 돌지 않습니다: %d번", n)
	}
	e.r.Now = func() time.Time { return now.AddDate(0, 0, 1) }
	e.r.Cfg.LegacyBatches = 0
	if _, err := e.r.CatchUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := legacyRuns(); n != 2 {
		t.Fatalf("legacyBatches 0이면 돌지 않습니다: %d번", n)
	}
}

// 검토 지적 2·3·5: 같은 지금 기억에 합치는 합치기는 하나만 대기한다(뒤 것이 앞 것의 합친 본문을 덮지 않게).
// 합친 본문이 없는 합치기, 다른 합치기의 with에 든 기억에 따로 낸 판정은 받지 않는다(다음 묶음에 다시 나온다).
func TestLegacy_겹치는_합치기와_빈_본문은_받지_않는다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, ms := legacyEnv(t, now)
	cur := ms["커밋은 경로를 명시해서 스테이징한다"]
	dup, cand := ms["커밋할 때 경로를 명시해서 스테이징한다 git add 금지"], ms["회고는 새벽 네 시에 돈다"]
	old, other := ms["onidot-orbit-plugin 저장소에서 스킬을 고친다"], ms["회고 결과는 제안으로만 반영한다"]
	_, report, _ := runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{
		{MemoryID: dup.ID, Verdict: "merge", With: []string{cur.ID}, MergedTitle: "커밋은 경로를 명시해서 스테이징한다", MergedBody: "git add -A 금지", Reason: "같은 말"},
		{MemoryID: cand.ID, Verdict: "merge", With: []string{cur.ID}, MergedTitle: "커밋 규칙", MergedBody: "다른 합친 본문", Reason: "같은 기억에 또"},
		{MemoryID: old.ID, Verdict: "merge", With: []string{other.ID}, MergedTitle: "회고 결과", Reason: "본문 없음"},
		{MemoryID: other.ID, Verdict: "stale", With: nil, Reason: "합치기에 든 기억"},
	}})
	if merges := autoOf(t, e, memory.ProposalMerge); len(merges) != 1 || merges[0].MemoryIDs[1] != dup.ID {
		t.Fatalf("합치기 %+v (%s)", merges, report)
	}
	for _, want := range []string{cand.ID + ": 합칠 기억 " + cur.ID + "는 이미 다른 합치기", old.ID + ": 합치기는 합칠 기억(with)과 합친 제목(mergedTitle)·본문(mergedBody)이 필요합니다"} {
		if !strings.Contains(report, want) {
			t.Errorf("보고에 %q가 없습니다:\n%s", want, report)
		}
	}
	if ev := legacyEvents(t, e); len(ev) != 2 || ev[other.ID]["verdict"] != "stale" {
		t.Errorf("판정 사건 %+v", ev)
	}

	// 다음 묶음에서: 앞 합치기는 이미 적용됐으므로(A2) 같은 지금 기억에 다시 합칠 수 있다(앞 본문을 덮지 않고 새 판으로 쌓인다).
	_, report, _ = runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{
		{MemoryID: cand.ID, Verdict: "merge", With: []string{cur.ID}, MergedTitle: "커밋 규칙", MergedBody: "본문", Reason: "다시"}}})
	if n := len(autoOf(t, e, memory.ProposalMerge)); n != 2 {
		t.Fatalf("합치기 %d건 (%s)", n, report)
	}
}

// 검토 지적 5: 같은 제출에서 합치기의 with에 든 옛 기억에 다른 판정(keep 말고)을 내면 그 판정은 받지 않는다.
func TestLegacy_합치기에_든_기억의_다른_판정(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, ms := legacyEnv(t, now)
	a, b := ms["회고는 새벽 네 시에 돈다"], ms["회고 결과는 제안으로만 반영한다"]
	_, report, _ := runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{
		{MemoryID: b.ID, Verdict: "stale", Reason: "낡음"},
		{MemoryID: a.ID, Verdict: "merge", With: []string{b.ID}, MergedTitle: "회고", MergedBody: "합친 본문", Reason: "같은 회고 이야기"},
	}})
	if len(autoOf(t, e, memory.ProposalStale)) != 0 || len(autoOf(t, e, memory.ProposalMerge)) != 1 {
		t.Fatalf("낡음 %d, 합치기 %d (%s)", len(autoOf(t, e, memory.ProposalStale)), len(autoOf(t, e, memory.ProposalMerge)), report)
	}
	if !strings.Contains(report, b.ID+": 다른 합치기("+a.ID+")에 든 기억이라 keep만 받습니다") {
		t.Errorf("보고:\n%s", report)
	}
}

// 검토 지적 7: 이미 판정한 기억에 다시 낸 판정(실패한 제출을 다시 받을 때)은 새로 남기지 않는다.
func TestLegacy_이미_판정한_기억은_다시_남기지_않는다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, ms := legacyEnv(t, now)
	a := ms["회고는 새벽 네 시에 돈다"]
	if err := e.s.RecordEvent(memory.Event{Kind: memory.EventLegacyJudged}, map[string]any{"memoryId": a.ID, "verdict": "keep"}); err != nil {
		t.Fatal(err)
	}
	mat := Material{Kind: KindLegacy, Period: "2026-10-31", Legacy: &Legacy{Groups: []LegacyGroup{{Memories: []LegacyMemory{{TidyMemory: TidyMemory{ID: a.ID}}}}}}}
	n, p, notes := applyLegacy(e.s, mat, "r-x", []LegacyVerdictIn{{MemoryID: a.ID, Verdict: "stale", Reason: "다시"}})
	if n != 0 || p != 0 || len(pendingOf(t, e, memory.ProposalStale)) != 0 {
		t.Fatalf("다시 판정 %d·%d %v", n, p, notes)
	}
	if v, _ := e.s.LegacyJudged(); v[a.ID] != "keep" {
		t.Errorf("판정 %v", v)
	}
}

// 검토 지적 1(끝 기준 3): 첫 묶음을 돌리기 전 상태별 기억 수를 한 번 남기고, 그 뒤 묶음은 바꾸지 않는다.
func TestLegacy_정리_시작_때_상태별_수를_남긴다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, _ := legacyEnv(t, now)
	runLegacyOnce(t, e, Submission{})
	base, ok := e.s.LegacyBaseline()
	if !ok || base.Day != "2026-10-31" || base.States[memory.StateConfirmed] != 4 || base.States[memory.StateCandidate] != 3 {
		t.Fatalf("시작 수 %+v %v", base, ok)
	}
	e.s.SetState(ms0(t, e), memory.StateStale, "x")
	runLegacyOnce(t, e, Submission{})
	if again, _ := e.s.LegacyBaseline(); again.States[memory.StateStale] != 0 {
		t.Fatalf("시작 수는 한 번만 남깁니다: %+v", again)
	}
}

func ms0(t *testing.T, e *env) string {
	t.Helper()
	all, _ := e.s.List(memory.StateCandidate)
	return all[0].ID
}

// 검토 지적 8: 호스트가 로그인되어 있지 않아 실패한 실행은 그날 몫에서 빼고 다시 돈다.
func TestLegacy_로그인되지_않아_실패한_실행은_몫에서_뺀다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, _ := legacyEnv(t, now)
	signedOut := true
	e.r.Exec = func(ctx context.Context, c Cmd) (string, error) {
		e.calls = append(e.calls, c)
		if signedOut {
			return `{"total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0},"is_error":true,"result":"Not logged in · Please run /login","type":"result"}`, errors.New("exit 1")
		}
		return "", nil
	}
	if _, err := e.r.legacyDaily(context.Background()); !errors.Is(err, ErrHostSignedOut) {
		t.Fatalf("로그아웃 실패여야 합니다: %v", err)
	}
	signedOut = false
	calls := len(e.calls)
	e.r.legacyDaily(context.Background())
	if len(e.calls) != calls+1 {
		t.Fatalf("로그인 뒤 같은 날 다시 돌아야 합니다: %d → %d", calls, len(e.calls))
	}
	e.r.legacyDaily(context.Background())
	if len(e.calls) != calls+1 {
		t.Fatalf("로그인 뒤 실패(제출 없음)는 몫을 씁니다: %d", len(e.calls))
	}
}

// 한국어로 쓰던 때 남은 로그아웃 실패도 영어로 바꾼 뒤 몫에서 뺀다(ONID-61).
func TestLegacy_다른_언어로_남은_로그아웃_실패도_몫에서_뺀다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, _ := legacyEnv(t, now)
	signedOut := true
	e.r.Exec = func(ctx context.Context, c Cmd) (string, error) {
		e.calls = append(e.calls, c)
		if signedOut {
			return `{"total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0},"is_error":true,"result":"Not logged in · Please run /login","type":"result"}`, errors.New("exit 1")
		}
		return "", nil
	}
	i18n.Set(i18n.KO)
	defer i18n.Set(i18n.EN)
	if _, err := e.r.legacyDaily(context.Background()); !errors.Is(err, ErrHostSignedOut) {
		t.Fatalf("로그아웃 실패여야 합니다: %v", err)
	}
	i18n.Set(i18n.EN)
	signedOut = false
	calls := len(e.calls)
	e.r.legacyDaily(context.Background())
	if len(e.calls) != calls+1 {
		t.Fatalf("언어를 바꿔도 같은 날 다시 돌아야 합니다: %d → %d", calls, len(e.calls))
	}
}

// 끝 기준 2: 옛 기억 확정(증거가 없어 사람이 승인)·낡음(자동 적용)도 원장에 남고 되돌리면 옛 상태로 돌아온다.
func TestLegacy_승인한_확정과_낡음도_되돌린다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e, ms := legacyEnv(t, now)
	cand, old := ms["회고는 새벽 네 시에 돈다"], ms["onidot-orbit-plugin 저장소에서 스킬을 고친다"]
	runLegacyOnce(t, e, Submission{Legacy: []LegacyVerdictIn{
		{MemoryID: cand.ID, Verdict: "confirm", Reason: "맞다"}, {MemoryID: old.ID, Verdict: "stale", Reason: "보관한 저장소"}}})
	var lines []string
	for _, p := range pendingOf(t, e, memory.ProposalConfirm) {
		res, err := e.s.Decide(memory.DecideInput{ID: p.ID, Decision: "approve"})
		if err != nil || res.LedgerID == "" {
			t.Fatalf("승인 %+v %v", res, err)
		}
		lines = append(lines, res.LedgerID)
	}
	for _, p := range autoOf(t, e, memory.ProposalStale) {
		lines = append(lines, p.ID)
	}
	if len(lines) != 2 {
		t.Fatalf("확정 승인·자동 낡음 %v", lines)
	}
	for _, ref := range lines {
		if _, err := e.s.RevertChange(ref, memory.Who{}, "되돌리기"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{cand.ID, old.ID} {
		if m, _ := e.s.Get(id); m.State != memory.StateCandidate {
			t.Errorf("되돌린 뒤 %s %s", id, m.State)
		}
	}
}
