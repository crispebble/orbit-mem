package retro

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/project"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
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
	Verdict    string   `json:"verdict,omitempty" jsonschema:"keep(유지, 기본)·hold(보류: 후보로만)·reject(거절: 남기지 않음)"`
	Reason     string   `json:"reason,omitempty" jsonschema:"판정 사유. reject에는 필수"`
	// ConflictsWith marks the candidate as going against these rules of the user (A2): it is not confirmed automatically.
	ConflictsWith []string `json:"conflictsWith,omitempty" jsonschema:"이 후보와 어긋나는 사용자 규칙(확정·인용) id. 어긋남이 표시된 후보는 자동 확정되지 않는다"`
	// Evidence is, in a work retrospective, the verification evidence that checked a project fact (A2 ③).
	Evidence string `json:"evidence,omitempty" jsonschema:"프로젝트 관례(fact)를 이 작업의 검증이 직접 확인했으면 재료 「검증 증거」의 글을 그대로"`
	// Links are the existing memories to read together with this one, made as automatic link changes.
	Links []LinkIn `json:"links,omitempty" jsonschema:"함께 읽어야 할 기존 기억(같은 결정의 근거·앞뒤 절차·예외). 같은 태그·같은 작업이라는 것만으로는 잇지 않는다"`
}

// LinkIn is an existing memory a submitted memory should be read with.
type LinkIn struct {
	ID       string `json:"id" jsonschema:"이을 기존 기억 id"`
	Relation string `json:"relation" jsonschema:"basis(같은 결정의 근거)·sequence(앞뒤 절차)·exception(예외)"`
	Note     string `json:"note,omitempty" jsonschema:"잇는 근거 한 줄"`
}

// ProposalIn is a proposal from the retrospective.
type ProposalIn struct {
	Kind        string   `json:"kind" jsonschema:"stale·merge·supersede·reclassify·edit·link·retag·revert·apply_file(direction은 받지 않는다: flags·patterns에 쓴다). stale·merge·supersede(memoryIds: 대체할 기억, 대신하는 기억)·reclassify·edit·link·retag와 자동 변경의 revert는 바로 적용되고 원장에 남는다. 사용자 말을 담은 기억·지침 파일·사람이 한 변경에 닿으면 적용되지 않는다(대기하지 않는다). 플러그인 원본 apply_file은 실험실이 도움됨으로 판정하면 적용된다"`
	Title       string   `json:"title"`
	Body        string   `json:"body,omitempty" jsonschema:"왜 제안하는지"`
	MemoryIDs   []string `json:"memoryIds,omitempty"`
	NewKind     string   `json:"newKind,omitempty" jsonschema:"reclassify의 새 종류"`
	MergedTitle string   `json:"mergedTitle,omitempty" jsonschema:"merge로 만들 기억 제목"`
	MergedBody  string   `json:"mergedBody,omitempty"`
	Path        string   `json:"path,omitempty" jsonschema:"apply_file 대상 파일 절대경로(반영 대상 목록 안)"`
	Content     string   `json:"content,omitempty" jsonschema:"apply_file의 새 파일 내용 전체"`
	NewTitle    string   `json:"newTitle,omitempty" jsonschema:"edit: 새 제목"`
	NewBody     string   `json:"newBody,omitempty" jsonschema:"edit: 새 본문"`
	NewTags     []string `json:"newTags,omitempty" jsonschema:"edit: 새 태그"`
	NewScope    string   `json:"newScope,omitempty" jsonschema:"edit: 새 범위 global 또는 project"`
	Project     string   `json:"project,omitempty" jsonschema:"edit: 새 범위가 project면 그 작업 디렉터리"`
	LedgerID    string   `json:"ledgerId,omitempty" jsonschema:"revert: 되돌릴 원장 id"`
	Note        string   `json:"note,omitempty" jsonschema:"link: 두 기억을 잇는 근거 한 줄(memoryIds에 이을 기억 둘)"`
	Relation    string   `json:"relation,omitempty" jsonschema:"link: 잇는 관계 basis(같은 결정의 근거)·sequence(앞뒤 절차)·exception(예외)"`
	FromTags    []string `json:"fromTags,omitempty" jsonschema:"retag: 뺄 태그(뜻이 같은 다른 태그를 묶을 때). memoryIds를 비우면 이 태그를 가진 기억 모두"`
	ToTags      []string `json:"toTags,omitempty" jsonschema:"retag: 넣을 태그"`
}

// UsageIn is the session-end retrospective's verdict on one memory put into the session.
type UsageIn struct {
	MemoryID string `json:"memoryId" jsonschema:"이 세션에 넣은 기억 id"`
	Verdict  string `json:"verdict" jsonschema:"followed(따름)·violated(어김)·irrelevant(상관없음)"`
	Note     string `json:"note,omitempty" jsonschema:"근거 한 줄"`
	Verified bool   `json:"verified,omitempty" jsonschema:"이 세션의 일이 실행 검증(시험·빌드·실제 실행)을 통과한 것을 재료에서 확인했으면 true"`
}

// LessonIn is a lesson the weekly retrospective drew from a judged change.
type LessonIn struct {
	LedgerID   string `json:"ledgerId" jsonschema:"교훈을 받는 원장 id(효과 판정이 난 변경)"`
	Signal     string `json:"signal" jsonschema:"적용 신호: 언제 이 교훈을 떠올리는지"`
	Lesson     string `json:"lesson" jsonschema:"교훈 한 줄"`
	Basis      string `json:"basis" jsonschema:"근거: 판정과 관찰 수"`
	Confidence string `json:"confidence" jsonschema:"확신도 high·medium·low"`
}

// CorrectionIn is a moment the user corrected or pointed out the AI's mistake.
type CorrectionIn struct {
	Quote      string `json:"quote" jsonschema:"사용자가 고친 말(재료에서 글자 그대로)"`
	SessionRef string `json:"sessionRef,omitempty" jsonschema:"그 세션(호스트:세션 id, 재료의 세션 머리에 있는 값)"`
	MemoryID   string `json:"memoryId,omitempty" jsonschema:"같은 내용의 기존 기억 id. 없으면 비운다"`
	Note       string `json:"note,omitempty" jsonschema:"무엇을 고쳤는지 한 줄"`
	Skill      string `json:"skill,omitempty" jsonschema:"교정이 하네스 스킬의 일하는 방법을 가리키면 그 스킬 이름(예: orbit:tdd)"`
	Role       string `json:"role,omitempty" jsonschema:"교정이 하네스 역할(에이전트)의 일을 가리키면 그 역할 이름(예: orbit:reviewer)"`
}

// Submission is what the retrospective hands back once.
type Submission struct {
	Summary     string            `json:"summary" jsonschema:"회고 결과 한 줄"`
	Journal     *JournalIn        `json:"journal,omitempty" jsonschema:"하루 회고는 필수"`
	Memories    []MemoryIn        `json:"memories,omitempty"`
	Corrections []CorrectionIn    `json:"corrections,omitempty" jsonschema:"하루 회고: 사용자가 AI를 고치거나 지적한 순간 모두"`
	Flags       []string          `json:"flags,omitempty" jsonschema:"짚을 점: 잘못 가는 방향, 반복된 실수"`
	Patterns    []string          `json:"patterns,omitempty" jsonschema:"주간: 반복 패턴"`
	Proposals   []ProposalIn      `json:"proposals,omitempty"`
	Usage       []UsageIn         `json:"usage,omitempty" jsonschema:"세션 끝: 이 세션에 넣은 기억마다 사용 판정"`
	Lessons     []LessonIn        `json:"lessons,omitempty" jsonschema:"주간: 효과 판정에서 얻은 교훈"`
	Sources     []SourceVerdictIn `json:"sources,omitempty" jsonschema:"하루: 「근거 파일이 바뀐 기억」마다 판정"`
	Legacy      []LegacyVerdictIn `json:"legacy,omitempty" jsonschema:"옛 기억 판정: 「옛 기억 판정」 묶음의 기억마다 판정"`
}

const (
	maxSubmitMemories    = 40
	maxSubmitProposals   = 20
	maxSubmitCorrections = 60
	maxSubmitLessons     = 20
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
	if mat.Kind == KindDaily && !mat.onlySources() && (sub.Journal == nil || strings.TrimSpace(sub.Journal.Title) == "") {
		return "", errors.New("하루 회고는 journal(title, body)이 필요합니다")
	}
	if mat.Kind == KindSession || mat.Kind == KindTidy || mat.Kind == KindLegacy {
		sub.Journal = nil // 세션 끝 회고의 요약은 summary 한 줄이고, 일지는 하루 회고가 쓴다
	}
	// 정리 제안(link·retag)이 제출 상한을 넘기면 제출 전체를 거부하지 않고 넘친 정리 제안만 뒤에서부터 뺀다.
	dropped := 0
	for i := len(sub.Proposals) - 1; i >= 0 && len(sub.Proposals) > maxSubmitProposals && mat.Tidy != nil; i-- {
		if k := sub.Proposals[i].Kind; k == memory.ProposalLink || k == memory.ProposalRetag {
			sub.Proposals = append(sub.Proposals[:i], sub.Proposals[i+1:]...)
			dropped++
		}
	}
	if len(sub.Memories) > maxSubmitMemories || len(sub.Proposals) > maxSubmitProposals || len(sub.Corrections) > maxSubmitCorrections || len(sub.Lessons) > maxSubmitLessons {
		return "", fmt.Errorf("기억은 %d건, 제안은 %d건, 교정은 %d건까지 냅니다. 중요한 것만 골라 다시 제출해 주세요", maxSubmitMemories, maxSubmitProposals, maxSubmitCorrections)
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
	if dropped > 0 {
		notes = append(notes, fmt.Sprintf("정리 제안 %d건은 제출 상한 %d건을 넘어 뺐습니다", dropped, maxSubmitProposals))
	}
	if mat.Kind == KindTidy && len(sub.Memories) > 0 {
		notes = append(notes, "그래프 정리는 기억을 새로 남기지 않습니다")
		sub.Memories = nil
	}
	if mat.Kind == KindLegacy {
		if len(sub.Memories) > 0 {
			notes = append(notes, "옛 기억 판정은 기억을 새로 남기지 않습니다")
			sub.Memories = nil
		}
		if len(sub.Proposals) > 0 {
			notes = append(notes, "옛 기억 판정은 proposals 대신 legacy 판정으로 냅니다")
			sub.Proposals = nil
		}
	}
	work := mat.Work // 작업 회고면 그 작업
	if mat.Kind == KindWork && work == nil {
		return "", errors.New("작업 회고 재료에 작업이 없습니다")
	}
	workScope := project.Project{}
	if work != nil {
		workScope = project.Resolve(work.Cwd)
	}
	journalID := ""
	if sub.Journal != nil && strings.TrimSpace(sub.Journal.Title) != "" {
		title := sub.Journal.Title
		scope, label := memory.ScopeGlobal, ""
		src := memory.Source{Kind: memory.SourceSession, Ref: "retro:" + id}
		switch mat.Kind {
		case KindDaily:
			title = mat.Period + " 일지: " + title
		case KindWork:
			title = mat.Period + " 작업: " + title
			scope, label, src = workScope.Scope, workScope.Label, memory.Source{Kind: memory.SourceWork, Ref: work.Ref}
			// 같은 작업이 남긴 같은 제목 일지(실패한 앞 제출)는 다시 쓰고, 다른 작업의 것이면 작업 id를 단다.
			if own, taken := journalOf(s, scope, title, work.Ref); own != "" {
				journalID = own
			} else if taken {
				title += " (" + work.Ref + ")"
			}
		default:
			title = mat.Period + " 주간: " + title
		}
		if journalID == "" {
			m, err := s.Save(memory.SaveInput{Kind: memory.KindJournal, Title: title, Body: sub.Journal.Body, Scope: scope, Project: label,
				State: memory.StateConfirmed, Day: day, Source: src, Who: memory.Who{Actor: memory.ActorAutomation}})
			if err != nil {
				return "", fmt.Errorf("일지를 남기지 못했습니다: %w", err)
			}
			journalID = m.ID
		}
		report = append(report, "일지 "+journalID)
	}
	created, seen, held, rejected, linked := 0, 0, 0, 0, 0
	for _, in := range sub.Memories {
		ref := in.SessionRef
		if ref == "" {
			ref = "retro:" + id
		}
		if work != nil {
			ref = work.Ref
			in.Tags = workTags(in.Tags)
		}
		verdict := strings.TrimSpace(in.Verdict)
		if verdict == "" {
			verdict = memory.VerdictKeep
		}
		judgment := memory.Judgment{RetroID: id, Session: in.SessionRef, Kind: in.Kind, Title: in.Title, Excerpt: in.Body, Verdict: verdict, Reason: in.Reason}
		if judgment.Session == "" {
			judgment.Session = mat.Session
		}
		if verdict == memory.VerdictReject {
			if _, err := s.RecordJudgment(judgment); err != nil {
				notes = append(notes, fmt.Sprintf("%s: %v", in.Title, err))
				continue
			}
			rejected++
			continue
		}
		if verdict != memory.VerdictKeep && verdict != memory.VerdictHold {
			notes = append(notes, in.Title+": verdict는 keep·hold·reject입니다")
			continue
		}
		src := memory.Source{Kind: memory.SourceSession, Ref: ref, Excerpt: in.Quote}
		if work != nil {
			src.Kind = memory.SourceWork
		}
		if in.Quote != "" && quoteFound(mat, in.Quote, in.Title, in.Body) {
			src.Kind = memory.SourceQuote
		}
		// 작업 회고: 어느 작업에서 나왔는지는 늘 남긴다(인용이어도). 검증 증거가 재료에 그대로 있으면 검증 출처도.
		var extra []memory.Source
		verifiedBy := ""
		if work != nil {
			if src.Kind != memory.SourceWork {
				extra = append(extra, memory.Source{Kind: memory.SourceWork, Ref: work.Ref})
			}
			if ev := strings.TrimSpace(in.Evidence); ev != "" {
				if workEvidenceFound(work, ev) {
					verifiedBy = ev
				} else {
					notes = append(notes, fmt.Sprintf("%s: 증거 %q는 재료 「검증 증거」에 없어 후보로 남김", in.Title, memory.ClipRunes(ev, 60)))
				}
			}
		} else if strings.TrimSpace(in.Evidence) != "" {
			notes = append(notes, in.Title+": evidence는 작업 회고에서만 받습니다")
		}
		if in.SameAs != "" {
			// 어긋남은 다시 나옴(증거 ①)보다 먼저 단다: 표시가 늦으면 이 저장으로 확정된다.
			if cur, err := s.Get(in.SameAs); err == nil && cur.State == memory.StateCandidate {
				for _, rule := range in.ConflictsWith {
					if _, err := s.FlagConflict(cur.ID, rule, "회고 "+id+": 사용자 규칙과 어긋남", memory.Who{Actor: memory.ActorAutomation}); err != nil {
						notes = append(notes, fmt.Sprintf("%s: 어긋남 표시 실패: %v", in.SameAs, err))
					}
				}
			}
			if mat.Kind == KindWeekly {
				// 주간 재료는 그 주 일지라 같은 근거를 다시 센다: 출처만 붙인다.
				if err := s.AddSource(in.SameAs, src); err != nil {
					notes = append(notes, fmt.Sprintf("%s: %v", in.SameAs, err))
					continue
				}
			} else if _, err := s.Seen(in.SameAs, src, day); err != nil {
				notes = append(notes, fmt.Sprintf("%s: %v", in.SameAs, err))
				continue
			}
			judgment.MemoryID, judgment.Verdict = in.SameAs, memory.VerdictKeep
			_, _ = s.RecordJudgment(judgment)
			seen++
			if cur, err := s.Get(in.SameAs); err == nil {
				notes = append(notes, attachWorkSources(s, cur, extra, verifiedBy, ref, workScope.Scope)...)
			}
			n, lnotes := submitLinks(s, id, in.SameAs, in.Title, in.Links)
			linked, notes = linked+n, append(notes, lnotes...)
			continue
		}
		if in.Kind == memory.KindJournal {
			notes = append(notes, in.Title+": 일지는 journal로 냅니다")
			continue
		}
		scope, label := memory.ScopeGlobal, ""
		if in.Scope == "project" && work != nil && in.Project == "" {
			scope, label = workScope.Scope, workScope.Label // 작업 회고의 프로젝트는 그 작업 폴더다
		} else if in.Scope == "project" {
			if in.Project == "" {
				notes = append(notes, in.Title+": project 범위에는 project 경로가 필요해 전역으로 남김")
			} else {
				pr := project.Resolve(in.Project)
				scope, label = pr.Scope, pr.Label
			}
		}
		var conflicts []string
		for _, rule := range in.ConflictsWith {
			if _, err := s.Get(rule); err != nil {
				notes = append(notes, fmt.Sprintf("%s: 어긋나는 규칙 %s가 없어 표시하지 않음", in.Title, rule))
				continue
			}
			conflicts = append(conflicts, rule)
		}
		before, _ := s.Count()
		newExtra := extra
		if verifiedBy != "" && in.Kind == memory.KindFact && scope == workScope.Scope {
			newExtra = append(slices.Clone(extra), memory.Source{Kind: memory.SourceVerification, Ref: ref, Excerpt: verifiedBy})
		}
		m, err := s.Save(memory.SaveInput{Kind: in.Kind, Title: in.Title, Body: in.Body, Tags: in.Tags, Scope: scope, Project: label, Source: src, Day: day,
			Extra: newExtra, ConflictsWith: conflicts, Who: memory.Who{Actor: memory.ActorAutomation}})
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", in.Title, err))
			continue
		}
		after, _ := s.Count()
		isNew := after > before
		if isNew {
			created++
			if verifiedBy != "" && len(newExtra) == len(extra) {
				notes = append(notes, in.Title+": "+verificationOnlyNote)
			}
		} else {
			seen++
			// 같은 제목이 이미 있었다: 함께 붙일 출처는 Save가 붙이지 않으니 여기서 붙인다.
			notes = append(notes, attachWorkSources(s, m, extra, verifiedBy, ref, workScope.Scope)...)
			if cur, err := s.Get(m.ID); err == nil {
				m = cur
			}
		}
		n, lnotes := submitLinks(s, id, m.ID, in.Title, in.Links)
		linked, notes = linked+n, append(notes, lnotes...)
		if m.State != memory.StateCandidate {
			judgment.Verdict = memory.VerdictKeep // 인용으로 바로 확정됐다
		}
		judgment.MemoryID, judgment.Kind, judgment.Title = m.ID, m.Kind, m.Title
		_, _ = s.RecordJudgment(judgment)
		// 확정은 승인 제안이 아니라 증거로 한다(A2): 후보는 회상에 쓰이고 증거가 쌓이면 저장 계층이 스스로 확정한다.
		// 어긋남 표시는 Save가 증거 확인보다 먼저 단다.
		if judgment.Verdict == memory.VerdictHold {
			held++
			continue
		}
	}
	if held+rejected > 0 {
		report = append(report, fmt.Sprintf("보류 %d건, 거절 %d건", held, rejected))
	}
	report = append(report, fmt.Sprintf("새 기억 %d건, 다시 나옴 %d건", created, seen))
	if linked > 0 {
		report = append(report, fmt.Sprintf("연결 %d건", linked))
	}
	proposed := 0
	if mat.Kind == KindSession && len(sub.Proposals) > 0 {
		notes = append(notes, "세션 끝 회고는 제안을 내지 않습니다. 하루·주간 회고가 냅니다")
		sub.Proposals = nil
	}
	tidied, labbed, waiting := 0, 0, 0
	for _, in := range sub.Proposals {
		pi := memory.ProposalInput{Kind: in.Kind, Title: in.Title, Body: in.Body, MemoryIDs: in.MemoryIDs, RetroID: id,
			Payload: memory.Payload{Kind: in.NewKind, MergedTitle: in.MergedTitle, MergedBody: in.MergedBody, Path: in.Path, Content: in.Content,
				Title: in.NewTitle, Body: in.NewBody, Tags: in.NewTags, LedgerID: in.LedgerID, Note: in.Note, FromTags: in.FromTags, ToTags: in.ToTags}}
		tidy := in.Kind == memory.ProposalLink || in.Kind == memory.ProposalRetag
		if mat.Kind == KindWork && !slices.Contains(workProposalKinds, in.Kind) {
			notes = append(notes, fmt.Sprintf("%s: 작업 회고는 %s 제안만 냅니다(%s 뺌)", in.Title, strings.Join(workProposalKinds, "·"), proposalKindName(in.Kind)))
			continue
		}
		if mat.Kind == KindTidy && !tidy {
			notes = append(notes, in.Title+": 그래프 정리는 link·retag 제안만 냅니다")
			continue
		}
		if tidy && mat.Tidy != nil && mat.Tidy.Max > 0 && tidied >= mat.Tidy.Max {
			notes = append(notes, fmt.Sprintf("%s: 회당 정리 제안 상한 %d건을 넘어 뺐습니다", in.Title, mat.Tidy.Max))
			continue
		}
		switch in.Kind {
		case memory.ProposalLink:
			label, ok := linkRelations[in.Relation]
			if !ok {
				notes = append(notes, in.Title+": 잇는 관계(relation: basis·sequence·exception)가 없어 뺐습니다. 같은 태그·같은 세션만으로는 잇지 않습니다")
				continue
			}
			pi.Payload.Note = label + ": " + strings.TrimSpace(in.Note)
		case memory.ProposalEdit:
			switch in.NewScope {
			case "":
			case "global":
				pi.Payload.Scope = memory.ScopeGlobal
			case "project":
				if in.Project == "" {
					notes = append(notes, in.Title+": project 범위에는 project 경로가 필요합니다")
					continue
				}
				pr := project.Resolve(in.Project)
				pi.Payload.Scope, pi.Payload.Project = pr.Scope, pr.Label
			default:
				notes = append(notes, in.Title+": newScope는 global 또는 project입니다")
				continue
			}
		case memory.ProposalConfirm:
			notes = append(notes, in.Title+": 후보 확정은 증거로 저장 계층이 스스로 합니다(제안하지 않습니다)")
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
			// 하네스(플러그인 원본) 변경은 실험실을 통과해야 사용자에게 간다(설계 · Orbit 개선 3차 「스킬 제안」).
			// 실험실은 플러그인 원본 안 파일만 시험할 수 있다. 원본 밖 파일은 지금처럼 바로 대기로 간다.
			pi.Lab = mat.PluginSource != "" && allowedTarget([]string{mat.PluginSource}, in.Path) && memory.FileTarget(in.Path) == memory.TargetHarness
		}
		out, err := s.SubmitChange(pi, memory.Who{Actor: memory.ActorAutomation})
		if err != nil {
			if !errors.Is(err, memory.ErrDuplicateProposal) && !errors.Is(err, memory.ErrAutoSkip) {
				notes = append(notes, fmt.Sprintf("%s: %v", in.Title, err))
			}
			continue
		}
		proposed++
		switch {
		case pi.Lab:
			labbed++
		case out.AskUser:
			waiting++
			notes = append(notes, fmt.Sprintf("%s: %s", in.Title, out.Reason))
		case !out.Applied && out.Reason != "":
			notes = append(notes, fmt.Sprintf("%s: %s", in.Title, out.Reason))
		}
		if tidy {
			tidied++
		}
	}
	if len(sub.Sources) > 0 {
		n, snotes := applySources(s, mat, func(path string) (string, bool) { return judgedHash(p, id, path) }, sub.Sources, id)
		proposed += n
		notes = append(notes, snotes...)
	}
	legacyJudgedN := 0
	if mat.Kind == KindLegacy && mat.Legacy != nil {
		n, lp, lnotes := applyLegacy(s, mat, id, sub.Legacy)
		legacyJudgedN, proposed = n, proposed+lp
		notes = append(notes, lnotes...)
	} else if len(sub.Legacy) > 0 {
		notes = append(notes, "옛 기억 판정(legacy)은 옛 기억 판정 회고에서만 받습니다")
	}
	// 정리(낡음·합치기·대체·연결·태그·고치기·재분류)는 승인 없이 적용되고 원장에 남는다. 사용자 말을 담은 기억과
	// 지침 파일에 닿는 것은 적용하지 않고 까닭을 알린다. 대기 제안은 만들지 않는다(설계 · orbit-mem 2판 18.3).
	var applied int
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM proposals WHERE retro_id=? AND status=?`, id, memory.StatusAuto).Scan(&applied)
	report = append(report, fmt.Sprintf("제안 %d건(그중 자동 적용 %d건, 원장에 남음)", proposed, applied))
	if waiting > 0 {
		report = append(report, fmt.Sprintf("그중 사용자 말을 담은 기억이나 지침 파일에 닿아 적용하지 않은 것 %d건(일하는 중에 사용자에게 묻고 userQuote로 다시)", waiting))
	}
	if labbed > 0 {
		report = append(report, fmt.Sprintf("그중 하네스 변경 실험실 대기 %d건(openorbit 개발 도구 npm run lab -- candidates)", labbed))
	}
	if mat.Kind == KindLegacy {
		report = append(report, fmt.Sprintf("판정 %d건", legacyJudgedN))
	}
	flags := append(append([]string{}, sub.Flags...), sub.Patterns...)
	fb, _ := json.Marshal(nonNil(flags))
	// 완료 표시와 읽은 발화 구간은 함께 남긴다: 하나만 남으면 같은 발화를 다시 읽거나 영영 못 읽는다.
	tx, err := s.DB().Begin()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE retros SET status=?, finished_at=?, summary=?, flags=?, journal_id=? WHERE id=? AND status='submitting'`,
		StatusDone, s.Now().UnixMilli(), sub.Summary, string(fb), journalID, id); err != nil {
		tx.Rollback()
		return "", err
	}
	if err := recordCoverage(tx, id, mat.Read); err != nil {
		tx.Rollback()
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	finished = true
	advanceTidy(s, mat.Tidy, day)
	// 교정과 사용 판정은 완료 표시 뒤에 남긴다: 실패한 제출을 다시 받을 때 두 번 세지 않는다.
	// 그 사이에 프로세스가 죽으면 그 회고의 교정·판정만 빠진다(감수한다).
	if mat.Kind == KindDaily || mat.Kind == KindSession || mat.Kind == KindWork {
		n, corrected, cnotes := recordCorrections(s, mat, id, sub.Corrections)
		notes = append(notes, cnotes...)
		report = append(report, fmt.Sprintf("교정 %d건", n))
		if mat.Kind == KindSession || mat.Kind == KindWork {
			u, unotes := recordUsage(s, mat, id, sub.Usage, corrected)
			notes = append(notes, unotes...)
			report = append(report, fmt.Sprintf("사용 판정 %d건", u))
		}
	} else if len(sub.Corrections) > 0 {
		notes = append(notes, "교정은 하루·세션 끝·작업 회고에서만 받습니다")
		sub.Corrections = nil
	}
	if mat.Kind != KindSession && mat.Kind != KindWork && len(sub.Usage) > 0 {
		notes = append(notes, "사용 판정은 세션 끝·작업 회고에서만 받습니다")
	}
	if mat.Kind == KindWeekly {
		n, lnotes := recordLessons(s, mat, id, sub.Lessons)
		notes = append(notes, lnotes...)
		if n > 0 || len(sub.Lessons) > 0 {
			report = append(report, fmt.Sprintf("교훈 %d건", n))
		}
	} else if len(sub.Lessons) > 0 {
		notes = append(notes, "교훈은 주간 회고에서만 받습니다")
	}
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

// findQuote finds the user's words in the material: in the named session if
// one is given (with or without the host), else in any session.
func findQuote(mat Material, ref, quote string) (transcript.Session, time.Time, bool) {
	host, id, ok := strings.Cut(ref, ":")
	if !ok {
		host, id = "", ref
	}
	for _, s := range mat.Sessions {
		if id != "" && (s.ID != id || (host != "" && s.Host != host)) {
			continue
		}
		for _, t := range s.Turns {
			if t.Role == "user" && strings.Contains(squash(t.Text), quote) {
				return s, t.At, true
			}
		}
	}
	return transcript.Session{}, time.Time{}, false
}

// recordCorrections keeps each correction as an event of the retrospective's
// day, judged against the session's injections (설계 「측정」의 교정 판정).
// Only the user's own words found in the material count, once per session.
// A memory made at or after the correction does not make it a repeat.
func recordCorrections(s *memory.Store, mat Material, retroID string, cs []CorrectionIn) (int, map[string]bool, []string) {
	var notes []string
	corrected := map[string]bool{} // 세션 키 + "\x00" + 기억 id
	n, missing, dup := 0, 0, 0
	seen := map[string]bool{}
	for _, c := range cs {
		q := squash(c.Quote)
		sess, at, ok := findQuote(mat, strings.TrimSpace(c.SessionRef), q)
		if len([]rune(q)) < 2 || !ok {
			missing++
			continue
		}
		key := sess.Host + ":" + sess.ID + "\x00" + q
		if seen[key] {
			dup++
			continue
		}
		seen[key] = true
		injected := ""
		memoryID := c.MemoryID
		if memoryID != "" {
			m, err := s.Get(memoryID)
			switch {
			case err != nil:
				notes = append(notes, fmt.Sprintf("교정 %q: 기억 %s가 없어 새 교정으로 셉니다", memory.ClipRunes(q, 30), memoryID))
				memoryID = ""
			case m.CreatedAt >= at.UnixMilli():
				memoryID = "" // 교정한 뒤에 생긴 기억: 그때는 없던 것이다
			}
		}
		if memoryID != "" {
			switch yes, known := s.InjectedIn(sess.ID, memoryID); {
			case mat.Kind == KindWork && slices.ContainsFunc(mat.Injected, func(b Brief) bool { return b.ID == memoryID }):
				injected = "yes" // 작업에 붙인 기억(recalled)
			case !known:
				injected = "unknown"
			case yes:
				injected = "yes"
			default:
				injected = "no"
			}
		}
		ci := memory.CorrectionInput{Quote: q, Note: c.Note, Host: sess.Host, Session: sess.ID, Day: mat.Period, Injected: injected,
			RetroID: retroID, OccurredAt: at.UnixMilli()}
		switch {
		case memoryID != "":
			ci.TargetKind, ci.Target = memory.TargetKindMemory, memoryID
		case strings.TrimSpace(c.Skill) != "":
			ci.TargetKind, ci.Target = memory.TargetKindSkill, strings.TrimSpace(c.Skill)
		case strings.TrimSpace(c.Role) != "":
			ci.TargetKind, ci.Target = memory.TargetKindRole, strings.TrimSpace(c.Role)
		}
		err := s.RecordCorrection(ci)
		if err != nil {
			notes = append(notes, "교정 기록 실패: "+err.Error())
			continue
		}
		if memoryID != "" {
			corrected[sessionKey(sess)+"\x00"+memoryID] = true
		}
		n++
	}
	if missing > 0 {
		notes = append(notes, fmt.Sprintf("교정 %d건은 인용이 재료에 없는 말(또는 다른 세션의 말)이라 뺐습니다", missing))
	}
	if dup > 0 {
		notes = append(notes, fmt.Sprintf("교정 %d건은 같은 세션의 같은 말이라 중복으로 뺐습니다", dup))
	}
	return n, corrected, notes
}

// recordUsage keeps the session-end retrospective's usage verdicts as
// memory.used events. Only memories put into the session count, once each;
// a memory the session's corrections point at was violated whatever the
// retrospective said (설계 「교정과 맞춰 본다」).
func recordUsage(s *memory.Store, mat Material, retroID string, us []UsageIn, corrected map[string]bool) (int, []string) {
	if len(mat.Sessions) == 0 {
		return 0, nil
	}
	sess := mat.Sessions[0]
	key := sessionKey(sess)
	last := sess.Turns[0].At
	for _, t := range sess.Turns {
		if t.At.After(last) {
			last = t.At
		}
	}
	injected := map[string]bool{}
	for _, b := range mat.Injected {
		injected[b.ID] = true
	}
	var notes []string
	verdicts := map[string]UsageIn{}
	var order []string
	outside := 0
	for _, u := range us {
		switch {
		case !injected[u.MemoryID]:
			outside++
			continue
		case !contains(memory.UsageVerdicts, u.Verdict):
			notes = append(notes, fmt.Sprintf("사용 판정 %s: verdict는 followed·violated·irrelevant입니다", u.MemoryID))
			continue
		}
		if _, dup := verdicts[u.MemoryID]; dup {
			continue
		}
		verdicts[u.MemoryID] = u
		order = append(order, u.MemoryID)
	}
	for _, b := range mat.Injected {
		if !corrected[key+"\x00"+b.ID] {
			continue
		}
		u, ok := verdicts[b.ID]
		if !ok {
			order = append(order, b.ID)
		}
		if u.Verdict != memory.UsageViolated {
			u = UsageIn{MemoryID: b.ID, Verdict: memory.UsageViolated, Note: strings.TrimSpace("같은 세션 교정과 맞춤 " + u.Note)}
		}
		verdicts[b.ID] = u
	}
	n := 0
	for _, id := range order {
		u := verdicts[id]
		verified := u.Verified && u.Verdict == memory.UsageFollowed
		if mat.Work != nil && !mat.Work.Verified {
			verified = false // 검증을 통과하지 못한 작업의 따름은 증거 ②가 아니다
		}
		if err := s.RecordUsage(memory.UsageInput{MemoryID: id, Verdict: u.Verdict, Verified: verified,
			Note: u.Note, Host: sess.Host, Session: sess.ID, Day: mat.Period, RetroID: retroID, OccurredAt: last.UnixMilli()}); err != nil {
			notes = append(notes, "사용 판정 기록 실패: "+err.Error())
			continue
		}
		n++
	}
	if outside > 0 {
		notes = append(notes, fmt.Sprintf("사용 판정 %d건은 이 세션에 넣지 않은 기억이라 뺐습니다", outside))
	}
	if missing := len(mat.Injected) - len(verdicts); missing > 0 {
		notes = append(notes, fmt.Sprintf("넣은 기억 %d건은 판정이 없습니다", missing))
	}
	return n, notes
}

var confidences = []string{"high", "medium", "low"}

// recordLessons keeps the weekly retrospective's lessons as candidate lesson
// memories, each tied to the judged change it came from. A change without
// evidence gets none (설계 「판정 근거가 없으면 교훈을 쓰지 않는다」).
func recordLessons(s *memory.Store, mat Material, retroID string, ls []LessonIn) (int, []string) {
	allowed := map[string]bool{}
	for _, e := range mat.Effects {
		if e.Lesson {
			allowed[e.ID] = true
		}
	}
	var notes []string
	n := 0
	for _, l := range ls {
		lesson := strings.TrimSpace(l.Lesson)
		switch {
		case !allowed[l.LedgerID]:
			notes = append(notes, fmt.Sprintf("교훈 %s: 증거 있는 판정이 난 변경이 아닙니다", l.LedgerID))
			continue
		case lesson == "" || strings.TrimSpace(l.Signal) == "" || strings.TrimSpace(l.Basis) == "" || !contains(confidences, l.Confidence):
			notes = append(notes, fmt.Sprintf("교훈 %s: 적용 신호·교훈·근거·확신도(high·medium·low)가 필요합니다", l.LedgerID))
			continue
		}
		title := memory.ClipRunes(strings.Join(strings.Fields(lesson), " "), 120)
		body := fmt.Sprintf("적용 신호: %s\n교훈: %s\n근거: %s\n확신도: %s\n원장: %s", l.Signal, lesson, l.Basis, l.Confidence, l.LedgerID)
		m, err := s.Save(memory.SaveInput{Kind: memory.KindLesson, Title: title, Body: body, Scope: memory.ScopeGlobal, Day: memory.Day(s.Now()),
			Source: memory.Source{Kind: memory.SourceSession, Ref: "retro:" + retroID, Excerpt: "효과 판정 " + l.LedgerID}})
		if err != nil {
			notes = append(notes, fmt.Sprintf("교훈 %s: %v", l.LedgerID, err))
			continue
		}
		if err := s.SetLedgerLesson(l.LedgerID, m.ID); err != nil {
			notes = append(notes, fmt.Sprintf("교훈 %s: %v", l.LedgerID, err))
			continue
		}
		n++
	}
	return n, notes
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// workProposalKinds are the proposals a work retrospective may make.
var workProposalKinds = []string{memory.ProposalLink, memory.ProposalStale, memory.ProposalSupersede, memory.ProposalMerge, memory.ProposalEdit}

func proposalKindName(k string) string {
	if k == memory.ProposalReclassify {
		return "재분류(reclassify)"
	}
	return k
}

// journalOf finds the journal of title in scope: own is one that work ref
// already left (a failed earlier submission), taken says another one is there.
func journalOf(s *memory.Store, scope, title, ref string) (own string, taken bool) {
	rows, err := s.DB().Query(`SELECT m.id, EXISTS(SELECT 1 FROM sources src WHERE src.memory_id=m.id AND src.kind=? AND src.ref=?)
		FROM memories m WHERE m.kind=? AND m.scope=? AND m.title_key=? ORDER BY m.created_at`, memory.SourceWork, ref, memory.KindJournal, scope, memory.TitleKey(title))
	if err != nil {
		return "", false
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var mine bool
		if rows.Scan(&id, &mine) != nil {
			continue
		}
		if mine {
			return id, true
		}
		taken = true
	}
	return "", taken
}

// verificationOnlyNote says where a work's verification evidence goes.
const verificationOnlyNote = "검증 증거는 이 작업의 프로젝트 범위 사실(fact)에만 붙입니다"

// attachWorkSources adds a work retrospective's extra sources to memory m that
// was already there and, with verification evidence on a project fact,
// confirms it by that evidence (A2 ③).
func attachWorkSources(s *memory.Store, m memory.Memory, extra []memory.Source, verifiedBy, ref, workScope string) []string {
	var notes []string
	for _, src := range extra {
		if err := s.AddSource(m.ID, src); err != nil {
			notes = append(notes, fmt.Sprintf("%s: 출처를 붙이지 못함: %v", m.ID, err))
		}
	}
	if verifiedBy == "" {
		return notes
	}
	if m.Kind != memory.KindFact || m.Scope != workScope {
		return append(notes, m.Title+": "+verificationOnlyNote)
	}
	if err := s.AddSource(m.ID, memory.Source{Kind: memory.SourceVerification, Ref: ref, Excerpt: verifiedBy}); err != nil {
		return append(notes, fmt.Sprintf("%s: 검증 출처를 붙이지 못함: %v", m.ID, err))
	}
	if _, _, err := s.ConfirmByEvidence(m.ID); err != nil {
		notes = append(notes, fmt.Sprintf("%s: 증거 확정 실패: %v", m.ID, err))
	}
	return notes
}

// submitLinks makes each link of a submitted memory an automatic link change
// (kept in the ledger; one touching the user's words is not made).
func submitLinks(s *memory.Store, retroID, memoryID, title string, links []LinkIn) (int, []string) {
	var notes []string
	n := 0
	for _, l := range links {
		label, ok := linkRelations[l.Relation]
		switch {
		case !ok:
			notes = append(notes, fmt.Sprintf("%s: 연결 %s의 관계 %q는 basis·sequence·exception이 아니라 뺐습니다", title, l.ID, l.Relation))
			continue
		case l.ID == memoryID:
			continue
		}
		if _, err := s.Get(l.ID); err != nil {
			notes = append(notes, fmt.Sprintf("%s: 이을 기억 %s가 없어 뺐습니다", title, l.ID))
			continue
		}
		out, err := s.SubmitChange(memory.ProposalInput{Kind: memory.ProposalLink, Title: "연결: " + title, MemoryIDs: []string{memoryID, l.ID}, RetroID: retroID,
			Payload: memory.Payload{Note: label + ": " + strings.TrimSpace(l.Note)}}, memory.Who{Actor: memory.ActorAutomation})
		if err != nil {
			if !errors.Is(err, memory.ErrDuplicateProposal) && !errors.Is(err, memory.ErrAutoSkip) {
				notes = append(notes, fmt.Sprintf("%s: 연결 %s: %v", title, l.ID, err))
			}
			continue
		}
		if out.Applied {
			n++
		} else if out.Reason != "" { // 사용자 말 기억에 닿는 연결은 적용하지 않는다
			notes = append(notes, fmt.Sprintf("%s: 연결 %s: %s", title, l.ID, out.Reason))
		}
	}
	return n, notes
}
