package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// ONID-124: 하네스 실험실은 openorbit의 개발 도구다. orbit-mem에는 lab 명령이 없다.
func TestLab_명령은_없다(t *testing.T) {
	home(t)
	_, errOut, code := run(t, "", "lab", "list")
	if code != 2 || !strings.Contains(errOut, "lab") {
		t.Fatalf("lab은 모르는 명령이어야 합니다: code=%d err=%q", code, errOut)
	}
	_, errOut, _ = run(t, "", "help")
	for _, line := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "orbit-mem lab") {
			t.Fatalf("도움말에 lab이 남았습니다: %q", line)
		}
	}
}

// storeList runs retro-store and decodes its one JSON value of any shape.
func storeAny(t *testing.T, stdin string, args ...string) any {
	t.Helper()
	out, errOut, code := run(t, stdin, append([]string{"retro-store"}, args...)...)
	if code != 0 {
		t.Fatalf("retro-store %v: code=%d err=%q", args, code, errOut)
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("retro-store %v: not JSON: %q", args, out)
	}
	return v
}

// ONID-124 L3: 실험실(개발 도구)이 부르는 저장 API. 교정 인용·대기 제안을 읽고, 실험 기록·원장·제안을 받는다.
func TestRetroStore_실험_저장_API(t *testing.T) {
	storeHome(t)
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCorrection(memory.CorrectionInput{Quote: "뭐알려달라는거야? 이해 못하겠어", Host: "claude", Session: "S"}); err != nil {
		t.Fatal(err)
	}
	var ev int64
	s.DB().QueryRow(`SELECT MAX(id) FROM events WHERE kind=?`, memory.EventCorrection).Scan(&ev)
	src := t.TempDir()
	skill := filepath.Join(src, "skills", "plain-writing", "SKILL.md")
	os.MkdirAll(filepath.Dir(skill), 0o755)
	os.WriteFile(skill, []byte("원래\n"), 0o644)
	cfg := config.Default()
	cfg.PluginSource = src
	if err := config.Save(paths().Config(), cfg); err != nil {
		t.Fatal(err)
	}
	waiting := []string{}
	for _, title := range []string{"묻기 규칙", "버릴 것", "닫힐 것"} {
		p, err := s.Propose(memory.ProposalInput{Kind: memory.ProposalApplyFile, Title: title, Lab: true,
			Payload: memory.Payload{Path: skill, Content: "원래\n" + title + "\n"}})
		if err != nil {
			t.Fatal(err)
		}
		waiting = append(waiting, p.ID)
	}
	s.Close()

	// 교정 인용 읽기: 없는 id는 종료 1
	got := storeAny(t, "", "experiment", "corrections", "--ids", itoa(ev))
	list, _ := got.([]any)
	if len(list) != 1 || list[0].(map[string]any)["quote"] != "뭐알려달라는거야? 이해 못하겠어" || list[0].(map[string]any)["id"] != float64(ev) {
		t.Fatalf("corrections = %v", got)
	}
	if _, _, code := run(t, "", "retro-store", "experiment", "corrections", "--ids", itoa(ev+100)); code != 1 {
		t.Fatalf("없는 교정은 종료 1: %d", code)
	}
	if _, _, code := run(t, "", "retro-store", "experiment", "corrections", "--ids", "x"); code != 1 {
		t.Fatalf("숫자가 아닌 id는 종료 1: %d", code)
	}

	// 실험실 대기 제안 읽기(본문·내용·기준 해시 포함)
	cands, _ := storeAny(t, "", "experiment", "candidates").([]any)
	if len(cands) != 3 {
		t.Fatalf("candidates = %v", cands)
	}
	c0 := cands[0].(map[string]any)
	payload, _ := c0["payload"].(map[string]any)
	if c0["status"] != memory.StatusLab || payload["path"] != skill || payload["baseHash"] != memory.FileHash(skill) {
		t.Fatalf("candidate = %v", c0)
	}

	// 실험 없이 닫기(옛 lab drop)
	if v := storeJSON(t, "", "experiment", "drop", "--id", waiting[1], "--note", "필요 없음"); v["dropped"] != waiting[1] {
		t.Fatalf("drop = %v", v)
	}
	if _, _, code := run(t, "", "retro-store", "experiment", "drop", "--id", waiting[1]); code != 1 {
		t.Fatalf("이미 닫힌 제안은 종료 1: %d", code)
	}

	// 진행 중 기록 → 판정이 난 기록(원장 줄 + 대기 제안 풀기)
	x := memory.LabExperiment{ID: "x-0123456789ab", TaskID: "t-0123456789ab", Status: memory.LabRunning, Runs: 1,
		Files: []string{"skills/plain-writing/SKILL.md"}, Hashes: map[string]string{"current": "a", "changed": "b"}, CreatedAt: 1000}
	body := marshalString(t, map[string]any{"experiment": x})
	if v := storeJSON(t, body, "experiment", "save"); v["status"] != memory.LabRunning {
		t.Fatalf("save running = %v", v)
	}
	x.Status, x.Verdict, x.CostUSD, x.FinishedAt = memory.LabDone, memory.VerdictHelped, 0.3, 2000
	x.Picks = []memory.LabPick{{Round: 1, A: "changed", Pick: "A", Winner: "changed", Reason: "기준에 더 맞음"}}
	runs := []memory.LabRun{{Candidate: "current", Round: 1, CostUSD: 0.1, OK: true}, {Candidate: "changed", Round: 1, CostUSD: 0.1, OK: true},
		{Candidate: "judge", Round: 1, CostUSD: 0.1, OK: true}}
	body = marshalString(t, map[string]any{"experiment": x, "runs": runs,
		"ledger":  map[string]any{"targets": []string{skill}, "change": "파일: SKILL.md\n+LABMARK", "basis": "실험실 요약"},
		"release": map[string]any{"proposalId": waiting[0], "summary": "실험실 x-0123456789ab: 도움됨"}})
	v := storeJSON(t, body, "experiment", "save")
	if v["status"] != memory.LabDone || v["ledgerId"] == "" || v["ledgerId"] == nil {
		t.Fatalf("save done = %v", v)
	}
	if pids, _ := v["proposalIds"].([]any); len(pids) != 1 || pids[0] != waiting[0] {
		t.Fatalf("proposalIds = %v", v["proposalIds"])
	}
	s, _ = openStore()
	l, err := s.LedgerEntry(v["ledgerId"].(string))
	if err != nil || l.ProposalKind != memory.LedgerKindLab || l.Verdict != memory.VerdictHelped || !strings.Contains(l.Change, "LABMARK") ||
		!strings.Contains(l.Basis, x.ID) {
		t.Fatalf("원장 %+v %v", l, err)
	}
	// 실험실이 도움됨으로 판정한 하네스 변경은 대기 없이 자동 변경으로 적용된다(설계 · orbit-mem 2판 18.3).
	p0, _ := s.GetProposal(waiting[0])
	if p0.Status != memory.StatusAuto || !strings.Contains(p0.Body, "도움됨") {
		t.Fatalf("풀린 제안 %+v", p0)
	}
	s.Close()

	// 같은 판정을 다시 보내도 원장 줄을 또 만들지 않는다(끝난 실험은 다시 쓰지 않는다).
	if _, errOut, code := run(t, body, "retro-store", "experiment", "save"); code != 1 || !strings.Contains(errOut, "already done") {
		t.Fatalf("다시 저장 code=%d %q", code, errOut)
	}
	s, _ = openStore()
	if n := labLines(s); n != 1 {
		t.Fatalf("원장 줄 %d", n)
	}
	s.Close()

	// 판정과 맞지 않는 제안 처리, 둘 이상의 처리, 대기 제안이 아닌 것, 원본 밖 파일은 아무것도 쓰기 전에 거절한다.
	exp := func(id, verdict string) map[string]any {
		return map[string]any{"id": id, "taskId": x.TaskID, "status": "done", "verdict": verdict, "runs": 1, "createdAt": 7000}
	}
	led := map[string]any{"targets": []string{skill}, "change": "d", "basis": "b"}
	outside := filepath.Join(t.TempDir(), "AGENTS.md")
	for name, bad := range map[string]map[string]any{
		"해로움인데 풀기":   {"experiment": exp("x-bad1", "harmful"), "ledger": led, "release": map[string]any{"proposalId": waiting[2], "summary": "s"}},
		"도움됨인데 닫기":   {"experiment": exp("x-bad2", "helped"), "ledger": led, "close": map[string]any{"proposalId": waiting[2], "note": "n"}},
		"효과 없음인데 제안": {"experiment": exp("x-bad3", "no_effect"), "ledger": led, "propose": []map[string]any{{"title": "t", "path": skill, "content": "c"}}},
		"처리 둘":       {"experiment": exp("x-bad4", "helped"), "ledger": led, "release": map[string]any{"proposalId": waiting[0], "summary": "s"}, "propose": []map[string]any{{"title": "t", "path": skill, "content": "c2"}}},
		"대기 아닌 제안":   {"experiment": exp("x-bad5", "helped"), "ledger": led, "release": map[string]any{"proposalId": waiting[0], "summary": "s"}},
		"원본 밖 파일":    {"experiment": exp("x-bad6", "helped"), "ledger": led, "propose": []map[string]any{{"title": "t", "path": outside, "content": "c"}}},
		"상대 경로":      {"experiment": exp("x-bad7", "helped"), "ledger": led, "propose": []map[string]any{{"title": "t", "path": "skills/x.md", "content": "c"}}},
	} {
		if _, errOut, code := run(t, marshalString(t, bad), "retro-store", "experiment", "save"); code != 1 {
			t.Fatalf("%s: code=%d %q", name, code, errOut)
		}
	}
	s, _ = openStore()
	if n := labLines(s); n != 1 {
		t.Fatalf("거절한 기록이 원장에 남았습니다: %d", n)
	}
	if xs, _ := s.LabExperiments(0, 0); len(xs) != 1 {
		t.Fatalf("거절한 기록이 실험으로 남았습니다: %d", len(xs))
	}
	s.Close()

	// 해로움: 대기 제안을 닫는다
	y := memory.LabExperiment{ID: "x-ba9876543210", TaskID: x.TaskID, Status: memory.LabDone, Verdict: memory.VerdictHarmful, Runs: 1,
		Files: x.Files, CreatedAt: 3000, FinishedAt: 4000}
	body = marshalString(t, map[string]any{"experiment": y, "ledger": map[string]any{"targets": []string{skill}, "change": "d", "basis": "b"},
		"close": map[string]any{"proposalId": waiting[2], "note": "실험실 x-ba9876543210: 해로움"}})
	storeJSON(t, body, "experiment", "save")
	s, _ = openStore()
	if p2, _ := s.GetProposal(waiting[2]); p2.Status != memory.StatusRejected || !strings.Contains(p2.Note, "해로움") {
		t.Fatalf("닫힌 제안 %+v", p2)
	}
	s.Close()

	// 도움됨 + 변경 폴더: 바뀐 파일마다 자동 변경으로 적용하고 원장에 남긴다(설계 · orbit-mem 2판 18.3)
	z := memory.LabExperiment{ID: "x-aaaaaaaaaaaa", TaskID: x.TaskID, Status: memory.LabDone, Verdict: memory.VerdictHelped, Runs: 1,
		Files: x.Files, CreatedAt: 5000, FinishedAt: 6000}
	body = marshalString(t, map[string]any{"experiment": z, "ledger": map[string]any{"targets": []string{skill}, "change": "d", "basis": "b"},
		"propose": []map[string]any{{"title": "실험실 통과: skills/plain-writing/SKILL.md", "body": "요약 x-aaaaaaaaaaaa", "path": skill, "content": "원래\n새 줄\n"}}})
	v = storeJSON(t, body, "experiment", "save")
	pids, _ := v["proposalIds"].([]any)
	if len(pids) != 1 {
		t.Fatalf("새 제안 = %v", v)
	}
	s, _ = openStore()
	np, _ := s.GetProposal(pids[0].(string))
	if np.Status != memory.StatusAuto || np.Kind != memory.ProposalApplyFile || np.Payload.Path != skill {
		t.Fatalf("새 제안 %+v", np)
	}
	if b, _ := os.ReadFile(skill); string(b) != "원래\n새 줄\n" {
		t.Fatalf("파일 %q", b)
	}
	if rs, _ := s.LabRuns(x.ID); len(rs) != 3 || rs[0].Experiment != x.ID {
		t.Fatalf("실행 기록 %+v", rs)
	}
	s.Close()

	// 목록(최신 먼저)·하나 읽기(실행 포함)
	xs, _ := storeAny(t, "", "experiment", "list").([]any)
	if len(xs) != 3 || xs[0].(map[string]any)["id"] != z.ID {
		t.Fatalf("list = %v", xs)
	}
	g := storeJSON(t, "", "experiment", "get", "--id", x.ID)
	if g["experiment"].(map[string]any)["verdict"] != memory.VerdictHelped || len(g["runs"].([]any)) != 3 {
		t.Fatalf("get = %v", g)
	}
	if _, _, code := run(t, "", "retro-store", "experiment", "get", "--id", "x-none"); code != 1 {
		t.Fatalf("없는 실험은 종료 1: %d", code)
	}

	// 잘못된 기록: 경로 문자가 든 id, 판정 없는 원장
	for _, bad := range []string{
		marshalString(t, map[string]any{"experiment": map[string]any{"id": "../x", "taskId": "t", "status": "running", "runs": 1, "createdAt": 1}}),
		marshalString(t, map[string]any{"experiment": map[string]any{"id": "x-1", "taskId": "t", "status": "running", "runs": 1, "createdAt": 1},
			"ledger": map[string]any{"targets": []string{}, "change": "", "basis": ""}}),
		`{`,
	} {
		if _, _, code := run(t, bad, "retro-store", "experiment", "save"); code != 1 {
			t.Fatalf("bad save %q: code %d", bad, code)
		}
	}
}

// ONID-124: 실험 설정은 개발 도구로 갔다. 옛 config.json에 남은 lab 키는 지우지 않고 무시한다.
func TestConfig_옛_실험_설정_키는_무시한다(t *testing.T) {
	h := home(t)
	old := `{"labRuns": 3, "labRunUSD": 1, "labExperimentUSD": 10, "labModel": "sonnet", "labJudgeModel": "sonnet"}`
	if err := os.WriteFile(filepath.Join(h, "config.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := run(t, "", "config", "show"); code != 0 {
		t.Fatalf("옛 lab 키(같은 모델)가 있어도 설정을 읽어야 합니다: %q %q", out, errOut)
	}
	if _, _, code := run(t, "", "config", "set", "labRuns", "4"); code == 0 {
		t.Fatal("labRuns는 더 이상 orbit-mem 설정이 아닙니다")
	}
}

// labLines counts the ledger lines the harness lab wrote (the automatic file
// changes it releases have their own lines).
func labLines(s *memory.Store) int {
	es, _ := s.Ledger(memory.LedgerFilter{Limit: 50})
	n := 0
	for _, e := range es {
		if e.ProposalKind == memory.LedgerKindLab {
			n++
		}
	}
	return n
}
