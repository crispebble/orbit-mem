package retro

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onidot-labs/orbit/internal/memory"
)

// cleanupMaterial adds what the weekly cleanup looks at (설계 「주간 정리」):
// memories not used for a long time, pairs that may say the same thing, the
// week's usage verdicts worth acting on, and judged changes.
func (r *Runner) cleanupMaterial(mat *Material, active []memory.Memory, from, to string) error {
	var live []memory.Memory
	for _, m := range active {
		if m.Kind != memory.KindJournal {
			live = append(live, m)
		}
	}
	last, earliest, err := r.Store.LastUse()
	if err != nil {
		return err
	}
	cutoff := r.now().AddDate(0, 0, -unusedDays).UnixMilli()
	if earliest > 0 && earliest <= cutoff { // 기록이 그만큼 오래되지 않았으면 「안 썼다」고 말하지 않는다
		for _, m := range live {
			if m.CreatedAt < cutoff && last[m.ID] < cutoff && len(mat.Unused) < maxUnused {
				mat.Unused = append(mat.Unused, brief(m, false))
			}
		}
	}
	mat.Pairs = similarPairs(live)

	usage, err := r.Store.UsageCounts(from, to)
	if err != nil {
		return err
	}
	byID := map[string]memory.Memory{}
	for _, m := range live {
		byID[m.ID] = m
	}
	for id, u := range usage {
		m, ok := byID[id]
		if !ok || (u.Violated < 1 && u.Irrelevant < 2) {
			continue
		}
		mat.Usage = append(mat.Usage, UsageLine{Brief: brief(m, false), Usage: u})
	}
	sort.Slice(mat.Usage, func(i, j int) bool {
		a, b := mat.Usage[i], mat.Usage[j]
		if a.Violated != b.Violated {
			return a.Violated > b.Violated
		}
		if a.Irrelevant != b.Irrelevant {
			return a.Irrelevant > b.Irrelevant
		}
		return a.ID < b.ID
	})

	judged, err := r.Store.Ledger(memory.LedgerFilter{Verdicts: []string{memory.VerdictHelped, memory.VerdictHarmful, memory.VerdictNoEffect}})
	if err != nil {
		return err
	}
	for _, e := range judged {
		// 실험 줄은 되돌릴 것도 교훈 받을 승인 변경도 아니다(실험실 판정은 실험실 지표가 보인다).
		if e.RevertedBy != "" || e.RevertOf != "" || e.ProposalKind == memory.LedgerKindLab || len(mat.Effects) >= maxEffects {
			continue
		}
		lesson := e.Evidence && e.LessonID == "" && e.Verdict != memory.VerdictNoEffect
		revert := e.Verdict == memory.VerdictHarmful || (e.Verdict == memory.VerdictNoEffect && e.Evidence)
		if !lesson && !revert {
			continue
		}
		mat.Effects = append(mat.Effects, EffectLine{ID: e.ID, Kind: e.ProposalKind, Target: e.TargetKind, Targets: e.Targets, Verdict: e.Verdict,
			Evidence: e.Evidence, Obs: e.Observation, Change: clipBytes(e.Change, 800), Lesson: lesson})
	}
	return nil
}

// similarPairs pairs memories of the same kind whose titles match each other
// strongly (the same rule the work recall uses).
func similarPairs(live []memory.Memory) [][2]Brief {
	byKind := map[string][]memory.Memory{}
	for _, m := range live {
		byKind[m.Kind] = append(byKind[m.Kind], m)
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var out [][2]Brief
	seen := map[string]bool{}
	for _, k := range kinds {
		ms := byKind[k]
		docs := make([]memory.Doc, len(ms))
		index := map[string]memory.Memory{}
		for i, m := range ms {
			docs[i] = m.Doc()
			index[m.ID] = m
		}
		for _, m := range ms {
			for _, hit := range memory.Strong(docs, m.Title) {
				o := index[hit.Doc.ID]
				if o.ID == m.ID {
					continue
				}
				a, b := m, o
				if b.CreatedAt < a.CreatedAt || (b.CreatedAt == a.CreatedAt && b.ID < a.ID) {
					a, b = b, a
				}
				key := a.ID + "|" + b.ID
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, [2]Brief{brief(a, false), brief(b, false)})
				if len(out) >= maxPairs {
					return out
				}
			}
		}
	}
	return out
}

var verdictNames = map[string]string{memory.VerdictHelped: "도움됨", memory.VerdictHarmful: "해로움", memory.VerdictNoEffect: "효과 없음",
	memory.VerdictMixed: "섞임", memory.VerdictPending: "관찰 중"}

func renderCleanup(b *strings.Builder, mat Material) {
	if len(mat.Unused) > 0 {
		fmt.Fprintf(b, "## 오래 쓰지 않은 기억 (%d일 넘게 넣지도 찾지도 않음)\n\n", unusedDays)
		for _, m := range mat.Unused {
			fmt.Fprintf(b, "- %s [%s/%s/%s] %s\n", m.ID, m.Kind, m.State, shortScope(m.Scope), m.Title)
		}
		b.WriteString("\n")
	}
	if len(mat.Pairs) > 0 {
		b.WriteString("## 겹칠 수 있는 기억 (같은 종류, 제목이 강하게 맞음. 먼저 만든 것 ↔ 나중 것)\n\n")
		for _, p := range mat.Pairs {
			fmt.Fprintf(b, "- %s ↔ %s: %s / %s\n", p[0].ID, p[1].ID, p[0].Title, p[1].Title)
		}
		b.WriteString("\n")
	}
	if len(mat.Usage) > 0 {
		b.WriteString("## 사용 판정 (이번 주, 어김 1회 이상 또는 상관없음 2회 이상)\n\n")
		for _, u := range mat.Usage {
			fmt.Fprintf(b, "- %s [%s/%s] %s: 따름 %d · 어김 %d · 상관없음 %d\n", u.ID, u.Kind, shortScope(u.Scope), u.Title, u.Followed, u.Violated, u.Irrelevant)
		}
		b.WriteString("\n")
	}
	if len(mat.Effects) > 0 {
		b.WriteString("## 효과 판정이 난 변경 (교훈·되돌리기 근거)\n\n")
		for _, e := range mat.Effects {
			ev := "증거 없음"
			if e.Evidence {
				ev = "증거 있음"
			}
			o := e.Obs
			fmt.Fprintf(b, "- %s [%s, %s] %s 변경 · 대상 %s %s\n  관찰: 전 나쁜 신호 %d·상관없음 %d·세션 %d → 후 %d·%d·%d",
				e.ID, verdictNames[e.Verdict], ev, e.Kind, e.Target, strings.Join(e.Targets, ", "),
				o.Before.Bad, o.Before.Irrelevant, o.Before.Sessions, o.After.Bad, o.After.Irrelevant, o.After.Sessions)
			if e.Lesson {
				b.WriteString(" · 교훈 받음")
			}
			fmt.Fprintf(b, "\n  ```diff\n  %s\n  ```\n", strings.ReplaceAll(strings.TrimSpace(e.Change), "\n", "\n  "))
		}
		b.WriteString("\n")
	}
}
