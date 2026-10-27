package retro

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/project"
)

// JournalIn is the day's journal: what was done, what blocked, what continues.
type JournalIn struct {
	Title string `json:"title" jsonschema:"그날 일을 한 줄로"`
	Body  string `json:"body" jsonschema:"한 일 / 막힌 것 / 이어 갈 것"`
}

// MemoryIn is a memory the retrospective found.
type MemoryIn struct {
	Kind       string   `json:"kind" jsonschema:"rule·fact·decision·lesson"`
	Title      string   `json:"title"`
	Body       string   `json:"body,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Scope      string   `json:"scope,omitempty" jsonschema:"global(기본) 또는 project"`
	Project    string   `json:"project,omitempty" jsonschema:"범위가 project면 그 대화의 작업 디렉터리(재료의 세션 머리에 있는 경로)"`
	SameAs     string   `json:"sameAs,omitempty" jsonschema:"이미 있는 기억과 같은 내용이면 그 id. 새로 만들지 않고 다시 나왔다고 기록한다"`
	Quote      string   `json:"quote,omitempty" jsonschema:"근거가 된 사용자 말을 재료에서 글자 그대로 옮김. 맞으면 바로 확정된다"`
	SessionRef string   `json:"sessionRef,omitempty" jsonschema:"근거 세션(호스트:세션 id)"`
}

// ProposalIn is a proposal from the retrospective.
type ProposalIn struct {
	Kind        string   `json:"kind" jsonschema:"stale·merge·reclassify·apply_file·direction"`
	Title       string   `json:"title"`
	Body        string   `json:"body,omitempty" jsonschema:"왜 제안하는지"`
	MemoryIDs   []string `json:"memoryIds,omitempty"`
	NewKind     string   `json:"newKind,omitempty" jsonschema:"reclassify의 새 종류"`
	MergedTitle string   `json:"mergedTitle,omitempty" jsonschema:"merge로 만들 기억 제목"`
	MergedBody  string   `json:"mergedBody,omitempty"`
	Path        string   `json:"path,omitempty" jsonschema:"apply_file 대상 파일 절대경로(반영 대상 목록 안)"`
	Content     string   `json:"content,omitempty" jsonschema:"apply_file의 새 파일 내용 전체"`
}

// Submission is what the retrospective hands back once.
type Submission struct {
	Summary   string       `json:"summary" jsonschema:"회고 결과 한 줄"`
	Journal   *JournalIn   `json:"journal,omitempty" jsonschema:"하루 회고는 필수"`
	Memories  []MemoryIn   `json:"memories,omitempty"`
	Flags     []string     `json:"flags,omitempty" jsonschema:"짚을 점: 잘못 가는 방향, 반복된 실수"`
	Patterns  []string     `json:"patterns,omitempty" jsonschema:"주간: 반복 패턴"`
	Proposals []ProposalIn `json:"proposals,omitempty"`
}

const (
	maxSubmitMemories  = 40
	maxSubmitProposals = 20
)

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// quoteFound accepts a quote as the user's own words only when it is long
// enough to say something, appears in what the user typed that day, and
// shares a word with the memory it backs.
func quoteFound(mat Material, quote, title, body string) bool {
	q := squash(quote)
	if len([]rune(q)) < 10 {
		return false
	}
	related := false
	doc := memory.Doc{Title: title, Body: body}
	for _, t := range memory.Terms(q, 32) {
		if len([]rune(t)) >= 2 && memory.Relevance(doc, t).Score > 0 {
			related = true
			break
		}
	}
	if !related {
		return false
	}
	for _, s := range mat.Sessions {
		for _, t := range s.Turns {
			if t.Role == "user" && strings.Contains(squash(t.Text), q) {
				return true
			}
		}
	}
	return false
}

// Submit applies a running retrospective's result once and finishes it.
// It returns a short report for the host.
func Submit(s *memory.Store, p config.Paths, id string, sub Submission) (string, error) {
	st, err := getStatus(s, id)
	if err != nil {
		return "", err
	}
	if st.Status != StatusRunning {
		return "", fmt.Errorf("이 회고는 이미 %s 상태입니다. 결과는 한 번만 제출합니다", st.Status)
	}
	mat, err := loadMaterial(p, id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(sub.Summary) == "" {
		return "", errors.New("summary(한 줄)가 필요합니다")
	}
	if mat.Kind == KindDaily && (sub.Journal == nil || strings.TrimSpace(sub.Journal.Title) == "") {
		return "", errors.New("하루 회고는 journal(title, body)이 필요합니다")
	}
	if len(sub.Memories) > maxSubmitMemories || len(sub.Proposals) > maxSubmitProposals {
		return "", fmt.Errorf("기억은 %d건, 제안은 %d건까지 냅니다. 중요한 것만 골라 다시 제출해 주세요", maxSubmitMemories, maxSubmitProposals)
	}
	// 한 번만 받는다: running을 submitting으로 먼저 차지한 호출만 진행한다.
	claim, err := s.DB().Exec(`UPDATE retros SET status='submitting' WHERE id=? AND status=?`, id, StatusRunning)
	if err != nil {
		return "", err
	}
	if n, _ := claim.RowsAffected(); n != 1 {
		return "", errors.New("이 회고는 이미 제출됐습니다. 결과는 한 번만 제출합니다")
	}
	finished := false
	defer func() {
		if !finished {
			_, _ = s.DB().Exec(`UPDATE retros SET status=? WHERE id=? AND status='submitting'`, StatusRunning, id)
		}
	}()
	day := mat.Period
	if mat.Kind == KindWeekly {
		day = memory.Day(s.Now())
	}
	var report []string
	var notes []string
	journalID := ""
	if sub.Journal != nil && strings.TrimSpace(sub.Journal.Title) != "" {
		title := sub.Journal.Title
		if mat.Kind == KindDaily {
			title = mat.Period + " 일지: " + title
		} else {
			title = mat.Period + " 주간: " + title
		}
		m, err := s.Save(memory.SaveInput{Kind: memory.KindJournal, Title: title, Body: sub.Journal.Body, Scope: memory.ScopeGlobal,
			State: memory.StateConfirmed, Day: day, Source: memory.Source{Kind: memory.SourceSession, Ref: "retro:" + id}})
		if err != nil {
			return "", fmt.Errorf("일지를 남기지 못했습니다: %w", err)
		}
		journalID = m.ID
		report = append(report, "일지 "+m.ID)
	}
	created, seen := 0, 0
	for _, in := range sub.Memories {
		ref := in.SessionRef
		if ref == "" {
			ref = "retro:" + id
		}
		src := memory.Source{Kind: memory.SourceSession, Ref: ref, Excerpt: in.Quote}
		if in.Quote != "" && quoteFound(mat, in.Quote, in.Title, in.Body) {
			src.Kind = memory.SourceQuote
		}
		if in.SameAs != "" && mat.Kind == KindWeekly {
			// 주간 재료는 그 주 일지라 같은 근거를 다시 센다: 출처만 붙인다.
			if err := s.AddSource(in.SameAs, src); err != nil {
				notes = append(notes, fmt.Sprintf("%s: %v", in.SameAs, err))
				continue
			}
			seen++
			continue
		}
		if in.SameAs != "" {
			if _, err := s.Seen(in.SameAs, src, day); err != nil {
				notes = append(notes, fmt.Sprintf("%s: %v", in.SameAs, err))
				continue
			}
			seen++
			continue
		}
		if in.Kind == memory.KindJournal {
			notes = append(notes, in.Title+": 일지는 journal로 냅니다")
			continue
		}
		scope, label := memory.ScopeGlobal, ""
		if in.Scope == "project" {
			if in.Project == "" {
				notes = append(notes, in.Title+": project 범위에는 project 경로가 필요해 전역으로 남김")
			} else {
				pr := project.Resolve(in.Project)
				scope, label = pr.Scope, pr.Label
			}
		}
		before, _ := s.Count()
		m, err := s.Save(memory.SaveInput{Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Scope: scope, Project: label, Source: src, Day: day})
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", in.Title, err))
			continue
		}
		after, _ := s.Count()
		if after > before {
			created++
		} else {
			seen++
		}
		if m.State == memory.StateCandidate && after > before {
			_, _ = s.Propose(memory.ProposalInput{Kind: memory.ProposalConfirm, Title: "후보 확정: " + m.Title,
				Body: "회고 " + mat.Period + "에서 나온 후보입니다. " + m.Body, MemoryIDs: []string{m.ID}, RetroID: id})
		}
	}
	report = append(report, fmt.Sprintf("새 기억 %d건, 다시 나옴 %d건", created, seen))
	proposed := 0
	for _, in := range sub.Proposals {
		pi := memory.ProposalInput{Kind: in.Kind, Title: in.Title, Body: in.Body, MemoryIDs: in.MemoryIDs, RetroID: id,
			Payload: memory.Payload{Kind: in.NewKind, MergedTitle: in.MergedTitle, MergedBody: in.MergedBody, Path: in.Path, Content: in.Content}}
		switch in.Kind {
		case memory.ProposalConfirm:
			notes = append(notes, in.Title+": 후보 확정 제안은 Orbit이 만듭니다")
			continue
		case memory.ProposalApplyFile:
			if !allowedTarget(mat.Targets, in.Path) {
				notes = append(notes, in.Title+": 반영 대상 목록 밖 파일이라 뺐습니다")
				continue
			}
			if read := readHash(p, id, in.Path); read != "" && read != memory.FileHash(in.Path) {
				notes = append(notes, in.Title+": 읽은 뒤 파일이 바뀌어 뺐습니다")
				continue
			}
		}
		if _, err := s.Propose(pi); err != nil {
			if !errors.Is(err, memory.ErrDuplicateProposal) {
				notes = append(notes, fmt.Sprintf("%s: %v", in.Title, err))
			}
			continue
		}
		proposed++
	}
	report = append(report, fmt.Sprintf("제안 %d건", proposed))
	flags := append(append([]string{}, sub.Flags...), sub.Patterns...)
	fb, _ := json.Marshal(nonNil(flags))
	if _, err := s.DB().Exec(`UPDATE retros SET status=?, finished_at=?, summary=?, flags=?, journal_id=? WHERE id=? AND status='submitting'`,
		StatusDone, s.Now().UnixMilli(), sub.Summary, string(fb), journalID, id); err != nil {
		return "", err
	}
	finished = true
	for _, h := range mat.HostMemory {
		_, _ = s.DB().Exec(`INSERT INTO absorbed(path,hash,at) VALUES(?,?,?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash, at=excluded.at`, h.Path, h.Hash, s.Now().UnixMilli())
	}
	_, _ = s.CloseSettled()
	out := "회고를 마쳤습니다: " + strings.Join(report, " · ")
	if len(notes) > 0 {
		out += "\n반영하지 못한 것:\n- " + strings.Join(notes, "\n- ")
	}
	_ = os.WriteFile(filepath.Join(p.RetroDir(), id, "report.txt"), []byte(out), 0o600)
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
