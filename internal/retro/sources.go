package retro

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onidot-labs/orbit/internal/memory"
)

// SourceCheck is a changed source file whose memories hold no excerpt to look
// for. The daily retrospective's model reads the file and judges each memory
// (설계 · Orbit 3차 O3-1, U4). The material names the file; its content is
// read through retro_read_file and never copied into the memory database.
type SourceCheck struct {
	Path     string  `json:"path"`
	Memories []Brief `json:"memories"`
}

// SourceVerdictIn is the model's verdict on one memory of a changed source file.
type SourceVerdictIn struct {
	Path     string `json:"path" jsonschema:"판정 대상 파일 절대경로(재료에 있는 그대로)"`
	MemoryID string `json:"memoryId"`
	Verdict  string `json:"verdict" jsonschema:"ok(아직 맞음)·edit(고쳐야 함: newTitle·newBody)·stale(낡음)"`
	Reason   string `json:"reason,omitempty" jsonschema:"판정 근거 한 줄. 파일 내용을 옮겨 적지 않는다"`
	NewTitle string `json:"newTitle,omitempty" jsonschema:"edit: 새 제목"`
	NewBody  string `json:"newBody,omitempty" jsonschema:"edit: 새 본문"`
}

type sourceRow struct {
	id, memoryID, title, excerpt string
}

func recordCheck(s *memory.Store, outcome string, memories int, proposed bool) {
	_ = s.RecordEvent(memory.Event{Kind: memory.EventSourceCheck}, map[string]any{"outcome": outcome, "memories": memories, "proposed": proposed})
}

// checkSources looks at every live memory whose source file changed since it
// was recorded. An excerpt still in the file only moves the hash on; a file
// that is gone is only marked (its evidence was deleted, the memory is not
// wrong for that); an excerpt that left the file is one stale proposal per
// file. Memories without an excerpt come back for the model to judge.
// Pending proposals of the old whole-file check give way to this judgment.
func (r *Runner) checkSources() []SourceCheck {
	_, _ = r.Store.SettleLegacySourceProposals()
	rows, err := r.Store.DB().Query(`SELECT s.id, s.memory_id, s.ref, s.excerpt, s.hash, m.title FROM sources s JOIN memories m ON m.id=s.memory_id
		WHERE s.kind='file' AND s.hash<>'' AND m.state IN ('candidate','confirmed') ORDER BY s.ref, s.memory_id, s.at`)
	if err != nil {
		return nil
	}
	byPath := map[string][]sourceRow{}
	hashes := map[string]string{}
	var paths []string
	for rows.Next() {
		var row sourceRow
		var ref, hash string
		if rows.Scan(&row.id, &row.memoryID, &ref, &row.excerpt, &hash, &row.title) != nil || !filepath.IsAbs(ref) {
			continue
		}
		cur, ok := hashes[ref]
		if !ok {
			cur = memory.FileHash(ref)
			hashes[ref] = cur
		}
		if cur == hash {
			continue
		}
		if _, ok := byPath[ref]; !ok {
			paths = append(paths, ref)
		}
		byPath[ref] = append(byPath[ref], row)
	}
	rows.Close()
	var checks []SourceCheck
	for _, path := range paths {
		if c, ok := r.checkFile(path, hashes[path], byPath[path]); ok {
			checks = append(checks, c)
		}
	}
	return checks
}

// checkFile settles one changed file and says whether the model must judge
// some of its memories.
func (r *Runner) checkFile(path, cur string, rows []sourceRow) (SourceCheck, bool) {
	setHash := func(memoryIDs map[string]bool) {
		for _, row := range rows {
			if memoryIDs[row.memoryID] {
				_ = r.Store.SetSourceHash(row.id, cur)
			}
		}
	}
	all := map[string]bool{}
	for _, row := range rows {
		all[row.memoryID] = true
	}
	if cur == "absent" {
		setHash(all)
		recordCheck(r.Store, memory.SourceAbsent, len(all), false)
		return SourceCheck{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return SourceCheck{}, false // 읽지 못하면 해시를 두고 다음 회고가 다시 본다
	}
	content := string(b)
	gone := map[string][]string{} // 기억 → 사라진 발췌
	blank := map[string]bool{}
	for _, row := range rows {
		switch {
		case strings.TrimSpace(row.excerpt) == "":
			blank[row.memoryID] = true
		case !memory.ExcerptIn(row.excerpt, content):
			gone[row.memoryID] = append(gone[row.memoryID], row.excerpt)
		}
	}
	kept, judge := map[string]bool{}, map[string]bool{}
	titles := map[string]string{}
	for _, row := range rows {
		titles[row.memoryID] = row.title
		switch {
		case gone[row.memoryID] != nil:
		case blank[row.memoryID]:
			judge[row.memoryID] = true
		default:
			kept[row.memoryID] = true
		}
	}
	if len(kept) > 0 {
		setHash(kept)
		recordCheck(r.Store, memory.SourceKept, len(kept), false)
	}
	if len(gone) > 0 {
		ids := sortedKeys(gone)
		var body strings.Builder
		fmt.Fprintf(&body, "근거 파일 %s 이 바뀌어, 아래 기억이 기댄 발췌가 파일에서 사라졌습니다. 기억이 아직 맞는지 보고 낡음으로 정리할지 정해 주세요.\n", path)
		for _, id := range ids {
			fmt.Fprintf(&body, "\n- %s (%s) 사라진 발췌:\n", titles[id], id)
			for _, ex := range gone[id] {
				fmt.Fprintf(&body, "  > %s\n", strings.ReplaceAll(memory.ClipRunes(strings.TrimSpace(ex), 300), "\n", "\n  > "))
			}
		}
		_, err := r.Store.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: staleTitle("근거 발췌가 사라짐", path, ids, titles),
			Body: strings.TrimRight(body.String(), "\n"), MemoryIDs: ids})
		if err == nil || errors.Is(err, memory.ErrDuplicateProposal) {
			set := map[string]bool{}
			for _, id := range ids {
				set[id] = true
			}
			setHash(set)
		}
		recordCheck(r.Store, memory.SourceGone, len(ids), err == nil)
	}
	if len(judge) == 0 {
		return SourceCheck{}, false
	}
	c := SourceCheck{Path: path}
	for _, id := range sortedKeys(judge) {
		if m, err := r.Store.Get(id); err == nil {
			c.Memories = append(c.Memories, brief(m, true))
		}
	}
	return c, len(c.Memories) > 0
}

func staleTitle(lead, path string, ids []string, titles map[string]string) string {
	if len(ids) == 1 {
		return lead + ": " + titles[ids[0]]
	}
	return fmt.Sprintf("%s: %s (기억 %d개)", lead, filepath.Base(path), len(ids))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m Material) sourceCheck(path string) (SourceCheck, bool) {
	for _, c := range m.SourceChecks {
		if c.Path == path {
			return c, true
		}
	}
	return SourceCheck{}, false
}

// onlySources says whether the daily material holds nothing but files to judge.
func (m Material) onlySources() bool {
	return m.Kind == KindDaily && len(m.SourceChecks) > 0 &&
		len(m.Sessions) == 0 && len(m.Summaries) == 0 && len(m.HostMemory) == 0 && len(m.Reclassify) == 0 && len(m.SessionResults) == 0
}

// applySources takes the model's verdicts on changed source files. A verdict
// counts only for a file and memory in the material and a file the model read
// that has not changed since; the rest come back as notes and stay for the
// next retrospective. Stale verdicts on one file make one proposal.
func applySources(s *memory.Store, mat Material, readHashOf func(string) string, in []SourceVerdictIn, retroID string) (int, []string) {
	var notes []string
	proposed := 0
	done := map[string]bool{}
	stale := map[string][]SourceVerdictIn{}
	var stalePaths []string
	titles := map[string]string{}
	setHash := func(path, memoryID, hash string) {
		rows, err := s.DB().Query(`SELECT id FROM sources WHERE memory_id=? AND kind='file' AND ref=? AND hash<>''`, memoryID, path)
		if err != nil {
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			_ = s.SetSourceHash(id, hash)
		}
	}
	for _, v := range in {
		c, ok := mat.sourceCheck(v.Path)
		var target Brief
		found := false
		for _, m := range c.Memories {
			if m.ID == v.MemoryID {
				target, found = m, true
			}
		}
		if !ok || !found {
			notes = append(notes, fmt.Sprintf("%s · %s: 판정 대상이 아닙니다", v.Path, v.MemoryID))
			continue
		}
		key := v.Path + "\x00" + v.MemoryID
		if done[key] {
			continue
		}
		read := readHashOf(v.Path)
		if read == "" {
			notes = append(notes, fmt.Sprintf("%s · %s: 파일을 읽지 않고 판정해 뺐습니다", v.Path, v.MemoryID))
			continue
		}
		if read != memory.FileHash(v.Path) {
			notes = append(notes, fmt.Sprintf("%s: 읽은 뒤 파일이 바뀌어 뺐습니다. 다음 회고가 다시 봅니다", v.Path))
			continue
		}
		reason := memory.ClipRunes(memory.MaskSecrets(strings.TrimSpace(v.Reason)), 300)
		switch v.Verdict {
		case memory.SourceOK:
			setHash(v.Path, v.MemoryID, read)
			recordCheck(s, memory.SourceOK, 1, false)
		case memory.SourceEdit:
			body := "근거 파일 " + v.Path + " 이 바뀌어 기억을 고칠 것을 제안합니다."
			if reason != "" {
				body += "\n판정 근거: " + reason
			}
			_, err := s.Propose(memory.ProposalInput{Kind: memory.ProposalEdit, Title: "근거 파일이 바뀌어 고칠 기억: " + target.Title, Body: body,
				MemoryIDs: []string{v.MemoryID}, RetroID: retroID, Payload: memory.Payload{Title: strings.TrimSpace(v.NewTitle), Body: v.NewBody}})
			if err != nil && !errors.Is(err, memory.ErrDuplicateProposal) {
				notes = append(notes, fmt.Sprintf("%s: %v", target.Title, err))
				continue
			}
			if err == nil {
				proposed++
			}
			setHash(v.Path, v.MemoryID, read)
			recordCheck(s, memory.SourceEdit, 1, err == nil)
		case memory.SourceStale:
			if stale[v.Path] == nil {
				stalePaths = append(stalePaths, v.Path)
			}
			v.Reason = reason
			stale[v.Path] = append(stale[v.Path], v)
			titles[v.MemoryID] = target.Title
		default:
			notes = append(notes, fmt.Sprintf("%s · %s: verdict는 ok·edit·stale입니다(%s)", v.Path, v.MemoryID, v.Verdict))
			continue
		}
		done[key] = true
	}
	for _, path := range stalePaths {
		vs := stale[path]
		var ids []string
		var body strings.Builder
		fmt.Fprintf(&body, "근거 파일 %s 이 바뀌어, 회고가 아래 기억을 낡았다고 판정했습니다. 낡음으로 정리할지 정해 주세요.\n", path)
		for _, v := range vs {
			ids = append(ids, v.MemoryID)
			fmt.Fprintf(&body, "\n- %s (%s)", titles[v.MemoryID], v.MemoryID)
			if v.Reason != "" {
				fmt.Fprintf(&body, ": %s", v.Reason)
			}
		}
		sort.Strings(ids)
		_, err := s.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: staleTitle("근거 파일이 바뀌어 낡음", path, ids, titles),
			Body: body.String(), MemoryIDs: ids, RetroID: retroID})
		if err != nil && !errors.Is(err, memory.ErrDuplicateProposal) {
			notes = append(notes, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if err == nil {
			proposed++
		}
		read := readHashOf(path)
		for _, id := range ids {
			setHash(path, id, read)
		}
		recordCheck(s, memory.SourceStale, len(ids), err == nil)
	}
	return proposed, notes
}
