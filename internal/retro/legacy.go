package retro

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onidot-labs/orbit/internal/memory"
)

// KindLegacy is a retrospective that judges memories moved from the old
// Knowledge and Claude memory (설계 · Orbit 3차 O3-3, oni retro legacy).
const KindLegacy = "legacy"

// Verdicts on one old memory. confirm, merge and stale become proposals the
// user decides; keep stays only as a judgment event.
const (
	LegacyConfirm = "confirm"
	LegacyMerge   = "merge"
	LegacyStale   = "stale"
	LegacyKeep    = "keep"
)

const (
	legacyBodyBytes  = 800 // 옛 기억 본문을 재료에 싣는 길이
	legacyRelated    = 3   // 옛 기억마다 싣는 겹칠 수 있는 지금 기억 수
	legacyRelatedMax = 90
)

// LegacyVerdictIn is the retrospective's judgment on one old memory.
type LegacyVerdictIn struct {
	MemoryID    string   `json:"memoryId" jsonschema:"「옛 기억 판정」 묶음의 기억 id"`
	Verdict     string   `json:"verdict" jsonschema:"confirm(후보 확정)·merge(합치기)·stale(낡음)·keep(그대로 둠)"`
	Reason      string   `json:"reason" jsonschema:"판정 근거 한 줄(필수)"`
	With        []string `json:"with,omitempty" jsonschema:"merge: 함께 합칠 기억 id(지금 있는 기억이나 이 묶음의 다른 기억)"`
	MergedTitle string   `json:"mergedTitle,omitempty" jsonschema:"merge: 합친 기억 제목"`
	MergedBody  string   `json:"mergedBody,omitempty" jsonschema:"merge: 합친 기억 본문"`
}

// LegacyMemory is an old memory as the judgment shows it.
type LegacyMemory struct {
	TidyMemory
	Pending []string `json:"pending,omitempty"` // 이 기억에 걸린 대기 제안 종류
}

// LegacyGroup is the part of a batch in one representative-tag cluster.
type LegacyGroup struct {
	Cluster  string         `json:"cluster"` // 태그 열쇠, 그 밖이면 ""
	Label    string         `json:"label"`
	Memories []LegacyMemory `json:"memories"`
}

// Legacy is the material of an old-memory judgment: one batch of old memories
// not judged yet, split by the graph's representative-tag clusters, the
// current memories they may repeat, and the names of retired products.
type Legacy struct {
	Total   int           `json:"total"`  // 판정할 옛 기억(판정한 것 포함)
	Judged  int           `json:"judged"` // 이미 판정한 옛 기억
	Groups  []LegacyGroup `json:"groups"`
	Related []TidyMemory  `json:"related,omitempty"`
	Retired []string      `json:"retired,omitempty"`
}

func (l *Legacy) empty() bool { return l == nil || len(l.Groups) == 0 }

func (l *Legacy) has(id string) bool {
	for _, g := range l.Groups {
		for _, m := range g.Memories {
			if m.ID == id {
				return true
			}
		}
	}
	return false
}

func (l *Legacy) size() int {
	n := 0
	for _, g := range l.Groups {
		n += len(g.Memories)
	}
	return n
}

// legacyMaterial picks the next batch of old memories, or nil when every one
// is judged. The batch walks the graph's clusters in picked order (then 「그
// 밖」) so memories on one topic are judged together, and fills up from the
// next cluster when one runs out.
func (r *Runner) legacyMaterial() (*Legacy, error) {
	judged, err := r.Store.LegacyJudged()
	if err != nil {
		return nil, err
	}
	left, err := r.Store.LegacyLeft(judged)
	if err != nil || len(left) == 0 {
		return nil, err
	}
	active, err := r.Store.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return nil, err
	}
	var live []memory.Memory
	for _, m := range active {
		if m.Kind != memory.KindJournal {
			live = append(live, m)
		}
	}
	byAge := func(ms []memory.Memory) {
		sort.SliceStable(ms, func(i, j int) bool {
			if ms[i].CreatedAt != ms[j].CreatedAt {
				return ms[i].CreatedAt < ms[j].CreatedAt
			}
			return ms[i].ID < ms[j].ID
		})
	}
	byAge(live)
	byAge(left)
	c := memory.Clusters(live, memory.ClusterMinNew(len(live)))
	labels := memory.TagLabels(live)
	pending, err := r.Store.Proposals(memory.StatusPending)
	if err != nil {
		return nil, err
	}
	waits := map[string][]string{}
	for _, p := range pending {
		for _, id := range p.MemoryIDs {
			waits[id] = append(waits[id], p.Kind)
		}
	}
	l := &Legacy{Judged: len(judged), Retired: r.Cfg.RetiredNames}
	l.Total = l.Judged + len(left)
	n := max(r.Cfg.LegacyMemories, 1)
	var batch []memory.Memory
	for _, key := range append(append([]string{}, c.Picked...), "") {
		if len(batch) >= n {
			break
		}
		g := LegacyGroup{Cluster: key, Label: labels[key]}
		if key == "" {
			g.Label = "그 밖"
		}
		for _, m := range left {
			if len(batch) >= n {
				break
			}
			if c.Of[m.ID] != key {
				continue
			}
			lm := LegacyMemory{TidyMemory: tidyMemory(m), Pending: waits[m.ID]}
			lm.Body = clipBytes(memory.MaskSecrets(m.Body), legacyBodyBytes)
			g.Memories = append(g.Memories, lm)
			batch = append(batch, m)
		}
		if len(g.Memories) > 0 {
			l.Groups = append(l.Groups, g)
		}
	}
	// 겹칠 수 있는 지금 기억: 옛 기억이 아닌 후보·확정 기억 중 옛 기억 하나하나와 강하게 맞는 것.
	var docs []memory.Doc
	current := map[string]memory.Memory{}
	for _, m := range live {
		if !memory.IsImported(m) {
			docs = append(docs, memory.Doc{ID: m.ID, Title: m.Title, Body: m.Body, Tags: m.Tags})
			current[m.ID] = m
		}
	}
	seen := map[string]bool{}
	for _, m := range batch {
		for _, d := range memory.StrongMatches(docs, m.Title+" "+memory.ClipRunes(m.Body, 200), legacyRelated) {
			if seen[d.ID] || len(l.Related) >= legacyRelatedMax {
				continue
			}
			seen[d.ID] = true
			cm := tidyMemory(current[d.ID])
			l.Related = append(l.Related, cm)
		}
	}
	return l, nil
}

// RunLegacy judges one batch of old memories now. With nothing left to judge
// it returns StatusEmpty without calling the host or keeping a run. Callers
// hold the lock (RunLegacyOne takes it).
func (r *Runner) RunLegacy(ctx context.Context) (Result, error) {
	mat := Material{Kind: KindLegacy, Period: dayOf(r.now())}
	l, err := r.legacyMaterial()
	if err != nil || l == nil {
		return Result{Kind: KindLegacy, Period: mat.Period, Status: StatusEmpty}, err
	}
	mat.Legacy = l
	if err := r.commonMaterial(&mat); err != nil {
		return Result{Kind: KindLegacy, Period: mat.Period}, err
	}
	mat.Existing = nil // 겹칠 수 있는 기억은 「지금 있는 기억」에 따로 싣는다
	return r.launch(ctx, mat, r.Cfg.RetroModel)
}

// RunLegacyOne takes the lock and judges one batch of old memories.
func (r *Runner) RunLegacyOne(ctx context.Context) (Result, error) {
	unlock, err := r.lock()
	if err != nil {
		return Result{Kind: KindLegacy, Period: dayOf(r.now())}, err
	}
	defer unlock()
	return r.RunLegacy(ctx)
}

// legacyDaily runs the day's old-memory batches up to legacyBatches, counting
// the runs already made today (a failed run counts: it is not retried the
// same day). It stops when nothing is left or a run fails.
func (r *Runner) legacyDaily(ctx context.Context) ([]Result, error) {
	today := dayOf(r.now())
	var ran int
	if err := r.Store.DB().QueryRow(`SELECT COUNT(*) FROM retros WHERE kind=? AND period=?`, KindLegacy, today).Scan(&ran); err != nil {
		return nil, err
	}
	var results []Result
	for ; ran < r.Cfg.LegacyBatches; ran++ {
		res, err := r.RunLegacy(ctx)
		if res.Status == StatusEmpty && err == nil {
			break
		}
		results = append(results, res)
		if err != nil {
			return results, fmt.Errorf("옛 기억 판정: %w", err)
		}
	}
	return results, nil
}

// applyLegacy turns the judgments on the batch into proposals and keeps each
// as an event. A judgment it cannot take leaves the memory for a later batch.
func applyLegacy(s *memory.Store, mat Material, retroID string, in []LegacyVerdictIn) (judged, proposed int, notes []string) {
	l := mat.Legacy
	done := map[string]bool{}
	for _, v := range in {
		id := strings.TrimSpace(v.MemoryID)
		if !l.has(id) {
			notes = append(notes, id+": 이번 묶음의 옛 기억이 아닙니다")
			continue
		}
		if done[id] {
			continue
		}
		reason := strings.TrimSpace(v.Reason)
		if reason == "" {
			notes = append(notes, id+": 근거(reason)가 필요합니다")
			continue
		}
		m, err := s.Get(id)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		verdict := strings.TrimSpace(v.Verdict)
		var p memory.ProposalInput
		switch verdict {
		case LegacyConfirm:
			if m.State != memory.StateCandidate {
				verdict = LegacyKeep // 이미 확정된 기억은 그대로 두는 것과 같다
				break
			}
			p = memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "옛 기억 확정: " + m.Title, MemoryIDs: []string{id}}
		case LegacyMerge:
			var with []string
			for _, w := range v.With {
				if w = strings.TrimSpace(w); w != "" && w != id && !contains(with, w) {
					with = append(with, w)
				}
			}
			if len(with) == 0 || strings.TrimSpace(v.MergedTitle) == "" {
				notes = append(notes, id+": 합치기는 합칠 기억(with)과 합친 제목(mergedTitle)이 필요합니다")
				continue
			}
			bad := ""
			for _, w := range with {
				if o, err := s.Get(w); err != nil || o.State != memory.StateCandidate && o.State != memory.StateConfirmed {
					bad = w
					break
				}
			}
			if bad != "" {
				notes = append(notes, fmt.Sprintf("%s: 합칠 기억 %s가 없거나 후보·확정 기억이 아닙니다", id, bad))
				continue
			}
			// 지금 기억을 앞에 둔다: 합친 기억의 종류·범위·태그 순서가 지금 기억을 따른다.
			p = memory.ProposalInput{Kind: memory.ProposalMerge, Title: "옛 기억 합치기: " + m.Title, MemoryIDs: append(with, id),
				Payload: memory.Payload{MergedTitle: strings.TrimSpace(v.MergedTitle), MergedBody: v.MergedBody}}
		case LegacyStale:
			p = memory.ProposalInput{Kind: memory.ProposalStale, Title: "옛 기억 낡음: " + m.Title, MemoryIDs: []string{id}}
		case LegacyKeep:
		default:
			notes = append(notes, id+": verdict는 confirm·merge·stale·keep입니다")
			continue
		}
		proposalID := ""
		if p.Kind != "" {
			p.Body, p.RetroID = "옛 기억 판정 근거: "+reason, retroID
			got, err := s.Propose(p)
			switch {
			case err == nil:
				proposalID = got.ID
				proposed++
			case errors.Is(err, memory.ErrDuplicateProposal):
				if same, ok := s.SameProposal(p); ok {
					proposalID = same.ID
				}
			default:
				notes = append(notes, fmt.Sprintf("%s: %v", id, err))
				continue
			}
		}
		if err := s.RecordEvent(memory.Event{Kind: memory.EventLegacyJudged, Day: mat.Period}, map[string]any{
			"memoryId": id, "verdict": verdict, "reason": memory.ClipRunes(memory.MaskSecrets(reason), 300),
			"proposalId": proposalID, "retroId": retroID}); err != nil {
			notes = append(notes, "판정 기록 실패: "+err.Error())
			continue
		}
		done[id] = true
		judged++
	}
	if missing := l.size() - len(done); missing > 0 {
		notes = append(notes, fmt.Sprintf("판정이 없는 옛 기억 %d건은 다음 묶음에 다시 나옵니다", missing))
	}
	return judged, proposed, notes
}

func renderLegacy(b *strings.Builder, l *Legacy) {
	if l.empty() {
		return
	}
	fmt.Fprintf(b, "## 옛 기억 판정 (대상 %d개 중 판정 %d개, 이번 묶음 %d개)\n\n", l.Total, l.Judged, l.size())
	b.WriteString("옛 Knowledge·Claude 메모리에서 판정 없이 옮겨 온 기억이다. 기억마다 legacy에 판정 하나와 근거(reason) 한 줄을 낸다.\n")
	b.WriteString("- confirm: 후보인데 지금도 맞고 다시 쓸 것(확정 제안). 이미 확정된 기억이 맞으면 keep이다.\n")
	b.WriteString("- merge: 「지금 있는 기억」이나 이 묶음의 다른 기억과 같은 말(with에 그 id, mergedTitle·mergedBody에 합친 내용).\n")
	b.WriteString("- stale: 보관한 저장소·옛 제품·옛 이름만 다루거나, 지금 기억과 어긋나 더 맞지 않는 것(낡음 제안).\n")
	b.WriteString("- keep: 그대로 둔다(아직 맞는 확정 기억, 아직 판단할 수 없는 후보).\n\n")
	for _, g := range l.Groups {
		fmt.Fprintf(b, "### 클러스터 「%s」\n\n", g.Label)
		for _, m := range g.Memories {
			fmt.Fprintf(b, "- %s [%s/%s] %s · 태그 %s", m.ID, m.Kind, m.State, m.Title, strings.Join(m.Tags, ", "))
			if len(m.Pending) > 0 {
				fmt.Fprintf(b, " · 대기 제안 %s", strings.Join(m.Pending, ", "))
			}
			b.WriteString("\n")
			if body := strings.TrimSpace(m.Body); body != "" {
				fmt.Fprintf(b, "  %s\n", strings.ReplaceAll(body, "\n", "\n  "))
			}
		}
		b.WriteString("\n")
	}
	if len(l.Related) > 0 {
		b.WriteString("## 지금 있는 기억 (옛 기억과 겹칠 수 있는 것, 판정 대상 아님)\n\n")
		for _, m := range l.Related {
			fmt.Fprintf(b, "- %s [%s/%s] %s · 태그 %s\n", m.ID, m.Kind, m.State, m.Title, strings.Join(m.Tags, ", "))
			if body := strings.TrimSpace(m.Body); body != "" {
				fmt.Fprintf(b, "  %s\n", strings.ReplaceAll(body, "\n", "\n  "))
			}
		}
		b.WriteString("\n")
	}
	if len(l.Retired) > 0 {
		b.WriteString("## 보관한 저장소·옛 이름 (이것만 다루는 기억은 낡음 후보)\n\n")
		for _, n := range l.Retired {
			fmt.Fprintf(b, "- %s\n", n)
		}
		b.WriteString("\n")
	}
}
