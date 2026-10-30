package retro

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onidot-labs/orbit/internal/memory"
)

// 명세 · O3-5 「스킬 제안과 승인 흐름」: 회고가 낸 하네스(플러그인 원본 스킬·역할·분야 묶음) 파일 반영 제안은
// 대기가 아니라 실험실 대기로 간다. 지침 파일·원본 밖 파일 반영 제안은 지금처럼 대기로 간다.
func TestSubmit_하네스_파일_반영은_실험실_대기로_간다(t *testing.T) {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, kst)
	e := setup(t, now)
	src := t.TempDir()
	skill := filepath.Join(src, "skills", "plain-writing", "SKILL.md")
	guide := filepath.Join(t.TempDir(), "AGENTS.md")
	personal := filepath.Join(t.TempDir(), "skills", "mine", "SKILL.md") // 경로는 스킬처럼 보이지만 플러그인 원본 밖
	w(t, skill, "옛 스킬\n")
	w(t, guide, "옛 지침\n")
	w(t, personal, "옛 개인 스킬\n")
	e.r.Cfg.ApplyTargets = []string{src, guide, personal}
	e.r.Cfg.PluginSource = src
	e.talk(t, "2026-10-30", claudeTurn("2026-10-30", "10:00", "user", "물을 때 번호만 쓰지 말고 풀어서 써"))
	var report string
	e.ai = func(id string) error {
		var err error
		report, err = Submit(e.s, e.r.Paths, id, Submission{Summary: "교정", Journal: &JournalIn{Title: "교정", Body: "-"},
			Proposals: []ProposalIn{
				{Kind: "apply_file", Title: "스킬에 묻기 규칙", Path: skill, Content: "새 스킬\n"},
				{Kind: "apply_file", Title: "지침에 묻기 규칙", Path: guide, Content: "새 지침\n"},
				{Kind: "apply_file", Title: "개인 스킬", Path: personal, Content: "새 개인 스킬\n"},
			}})
		return err
	}
	if res, err := e.r.Run(context.Background(), KindDaily, "2026-10-30"); err != nil || res.Status != StatusDone {
		t.Fatalf("하루 회고 %+v %v", res, err)
	}
	lab, _ := e.s.Proposals(memory.StatusLab)
	pending := pendingOf(t, e, memory.ProposalApplyFile)
	// 검토 지적 4: 실험실은 플러그인 원본 안 파일만 시험한다. 원본 밖 파일은 경로가 스킬처럼 보여도 대기로 간다.
	if len(lab) != 1 || lab[0].Payload.Path != skill || len(pending) != 2 {
		t.Fatalf("실험실 대기 %+v, 대기 %+v", lab, pending)
	}
	if !strings.Contains(report, "실험실 대기 1건") {
		t.Errorf("보고:\n%s", report)
	}
}
