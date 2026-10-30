package memory

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Proposal kinds (설계 「제안과 승인」).
const (
	ProposalConfirm    = "confirm"    // 후보 확정
	ProposalStale      = "stale"      // 낡은 기억 정리
	ProposalMerge      = "merge"      // 합치기
	ProposalReclassify = "reclassify" // 종류 재분류(옛 ISSUE·NOTE)
	ProposalApplyFile  = "apply_file" // 확정 규칙을 플러그인 스킬 원본이나 지침 파일에 반영(diff)
	ProposalDirection  = "direction"  // 방향 제안(주간 회고)
	ProposalEdit       = "edit"       // 기억 하나의 제목·본문·태그·범위 고치기
	ProposalRevert     = "revert"     // 원장 한 줄의 변경 되돌리기
	ProposalLink       = "link"       // 관련 기억 둘 잇기
	ProposalRetag      = "retag"      // 뜻이 같은 태그 묶기, 주제 태그 달기
)

// Proposal statuses.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	StatusSettled  = "settled" // 다른 길로 이미 이뤄져 닫힘
)

var ProposalKinds = []string{ProposalConfirm, ProposalStale, ProposalMerge, ProposalReclassify, ProposalApplyFile, ProposalDirection, ProposalEdit, ProposalRevert, ProposalLink, ProposalRetag}

var (
	ErrDuplicateProposal = errors.New("같은 제안이 이미 있거나 거절됐습니다")
	ErrFileChanged       = errors.New("제안을 만든 뒤 파일이 바뀌었습니다. 회고가 다시 제안하게 두거나 직접 고쳐 주세요")
	ErrDecided           = errors.New("이미 결정한 제안입니다")
	// errAlreadyLinked: 연결 제안을 승인할 때 그 쌍이 이미 다른 길로 이어져 있다. 제안은 정리(settled)로 닫는다.
	errAlreadyLinked = errors.New("이미 이어진 기억입니다")
	// errAlreadyRetagged: 태그 제안을 승인할 때 바꿀 기억이 남지 않았다(다른 길로 이미 바뀜). 정리(settled)로 닫는다.
	errAlreadyRetagged = errors.New("이미 태그가 바뀐 기억입니다")
)

// Payload carries what a proposal needs beyond memory ids.
type Payload struct {
	Kind        string   `json:"kind,omitempty"`        // reclassify: 새 종류, merge: 합친 기억 종류
	MergedTitle string   `json:"mergedTitle,omitempty"` // merge
	MergedBody  string   `json:"mergedBody,omitempty"`  // merge
	Path        string   `json:"path,omitempty"`        // apply_file: 절대경로, stale: 근거 파일 판정의 그 파일
	BaseHash    string   `json:"baseHash,omitempty"`    // apply_file: 제안 때 파일 hash(없던 파일이면 "absent")
	Content     string   `json:"content,omitempty"`     // apply_file: 새 내용 전체
	Scope       string   `json:"scope,omitempty"`       // direction·merge로 만들 기억 범위, edit의 새 범위
	Title       string   `json:"title,omitempty"`       // edit: 새 제목
	Body        string   `json:"body,omitempty"`        // edit: 새 본문(비우면 그대로)
	Tags        []string `json:"tags,omitempty"`        // edit: 새 태그
	Project     string   `json:"project,omitempty"`     // edit: 새 범위가 프로젝트면 그 이름
	LedgerID    string   `json:"ledgerId,omitempty"`    // revert: 되돌릴 원장 줄
	Note        string   `json:"note,omitempty"`        // link: 잇는 근거(연결에 남는다)
	FromTags    []string `json:"fromTags,omitempty"`    // retag: 뺄 태그(표기 차이 무시)
	ToTags      []string `json:"toTags,omitempty"`      // retag: 넣을 태그
}

// Proposal is a suggestion waiting for the user.
type Proposal struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	MemoryIDs []string `json:"memoryIds,omitempty"`
	Payload   Payload  `json:"payload"`
	Status    string   `json:"status"`
	CreatedAt int64    `json:"createdAt"`
	DecidedAt int64    `json:"decidedAt,omitempty"`
	RetroID   string   `json:"retroId,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// ProposalInput is a new proposal.
type ProposalInput struct {
	Kind      string
	Title     string
	Body      string
	MemoryIDs []string
	Payload   Payload
	RetroID   string
}

// FileHash is the hash used to notice that a file changed under a proposal.
func FileHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "absent"
	}
	return hashBytes(b)
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func fingerprint(in ProposalInput) string {
	ids := append([]string(nil), in.MemoryIDs...)
	sort.Strings(ids)
	parts := []string{in.Kind, strings.Join(ids, ",")}
	switch in.Kind {
	case ProposalApplyFile:
		parts = append(parts, in.Payload.Path, in.Payload.Content)
	case ProposalDirection:
		parts = append(parts, TitleKey(in.Title))
	case ProposalMerge:
		parts = append(parts, TitleKey(in.Payload.MergedTitle))
	case ProposalReclassify:
		parts = append(parts, in.Payload.Kind)
	case ProposalEdit:
		parts = append(parts, in.Payload.Title, in.Payload.Body, strings.Join(in.Payload.Tags, ","), in.Payload.Scope)
	case ProposalRevert:
		parts = append(parts, in.Payload.LedgerID)
	case ProposalStale:
		if in.Payload.Path != "" { // 근거 파일 판정: 파일마다 다른 제안이다
			parts = append(parts, in.Payload.Path)
		}
	case ProposalRetag:
		// 태그 묶기는 대상 기억이 아니라 묶는 태그 열쇠의 모음으로 같은 제안을 가린다. 거절한 묶기는
		// 표기·방향이 달라도, 그 태그를 가진 기억이 늘어도 다시 나오지 않는다. 태그 달기는 기억마다 다르다.
		if len(in.Payload.FromTags) > 0 {
			parts = []string{in.Kind, strings.Join(sortedKeys(append(append([]string{}, in.Payload.FromTags...), in.Payload.ToTags...)), ",")}
		} else {
			parts = []string{in.Kind, strings.Join(sortedKeys(in.Payload.ToTags), ","), strings.Join(ids, ",")}
		}
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:16])
}

// SameProposal finds the pending or rejected proposal that stops in from
// being proposed again (Propose's ErrDuplicateProposal), if there is one.
func (s *Store) SameProposal(in ProposalInput) (Proposal, bool) {
	fp := fingerprint(in)
	row := s.db.QueryRow(`SELECT `+proposalCols+` FROM proposals WHERE fingerprint=? AND status IN ('pending','rejected')
		ORDER BY status='pending' DESC LIMIT 1`, fp)
	p, err := scanProposal(row)
	return p, err == nil
}

// Propose stores a pending proposal. The same proposal is not stacked while
// pending, and one the user rejected does not come back.
func (s *Store) Propose(in ProposalInput) (Proposal, error) {
	in.Title = strings.TrimSpace(in.Title)
	if !contains(ProposalKinds, in.Kind) || in.Title == "" {
		return Proposal{}, fmt.Errorf("%w: 제안 종류(%s)와 제목이 필요합니다", ErrInvalid, strings.Join(ProposalKinds, "|"))
	}
	for _, id := range in.MemoryIDs {
		if _, err := getTx(s.db, id); err != nil {
			return Proposal{}, err
		}
	}
	switch in.Kind {
	case ProposalConfirm, ProposalStale, ProposalReclassify:
		if len(in.MemoryIDs) == 0 {
			return Proposal{}, fmt.Errorf("%w: 대상 기억 id가 필요합니다", ErrInvalid)
		}
		if in.Kind == ProposalReclassify && !validKind(in.Payload.Kind) {
			return Proposal{}, fmt.Errorf("%w: 새 종류가 필요합니다", ErrInvalid)
		}
	case ProposalMerge:
		if len(in.MemoryIDs) < 2 || strings.TrimSpace(in.Payload.MergedTitle) == "" {
			return Proposal{}, fmt.Errorf("%w: 합칠 기억 두 개 이상과 합친 제목이 필요합니다", ErrInvalid)
		}
		if in.Payload.Kind == "" {
			first, _ := getTx(s.db, in.MemoryIDs[0])
			in.Payload.Kind = first.Kind
		}
	case ProposalApplyFile:
		if !filepath.IsAbs(in.Payload.Path) {
			return Proposal{}, fmt.Errorf("%w: 반영할 파일은 절대경로여야 합니다", ErrInvalid)
		}
		old, _ := os.ReadFile(in.Payload.Path)
		in.Payload.BaseHash = FileHash(in.Payload.Path)
		diff := LineDiff(string(old), in.Payload.Content, 3)
		if strings.TrimSpace(diff) == "" {
			return Proposal{}, fmt.Errorf("%w: 바뀌는 내용이 없습니다", ErrInvalid)
		}
		in.Body = strings.TrimSpace(in.Body + "\n\n파일: " + in.Payload.Path + "\n```diff\n" + diff + "```")
	case ProposalEdit:
		if len(in.MemoryIDs) != 1 {
			return Proposal{}, fmt.Errorf("%w: 고칠 기억 하나가 필요합니다", ErrInvalid)
		}
		if in.Payload.Scope != "" && !validScope(in.Payload.Scope) {
			return Proposal{}, fmt.Errorf("%w: 범위는 global 또는 project:<키>입니다", ErrInvalid)
		}
		old, _ := getTx(s.db, in.MemoryIDs[0])
		next := editedMemory(old, in.Payload, nil, nil)
		diff := LineDiff(memoryText(old), memoryText(next), 2)
		if strings.TrimSpace(diff) == "" {
			return Proposal{}, fmt.Errorf("%w: 바뀌는 내용이 없습니다", ErrInvalid)
		}
		in.Body = strings.TrimSpace(in.Body + "\n\n```diff\n" + diff + "```")
	case ProposalLink:
		if len(in.MemoryIDs) != 2 || in.MemoryIDs[0] == in.MemoryIDs[1] {
			return Proposal{}, fmt.Errorf("%w: 이을 서로 다른 기억 둘이 필요합니다", ErrInvalid)
		}
		if linked, err := s.linked(in.MemoryIDs[0], in.MemoryIDs[1]); err != nil || linked {
			if err != nil {
				return Proposal{}, err
			}
			return Proposal{}, fmt.Errorf("%w: 이미 이어진 기억입니다", ErrInvalid)
		}
		in.Payload.Note = ClipRunes(MaskSecrets(strings.TrimSpace(in.Payload.Note)), 300)
	case ProposalRetag:
		if err := s.prepareRetag(&in); err != nil {
			return Proposal{}, err
		}
		if busy, err := s.retagBusy(in.Payload.FromTags, in.Payload.ToTags); err != nil || busy != "" {
			if err != nil {
				return Proposal{}, err
			}
			return Proposal{}, fmt.Errorf("%w (대기 중인 %s가 같은 태그를 묶습니다)", ErrDuplicateProposal, busy)
		}
	case ProposalRevert:
		e, err := s.LedgerEntry(in.Payload.LedgerID)
		if err != nil {
			return Proposal{}, err
		}
		if e.RevertedBy != "" {
			return Proposal{}, fmt.Errorf("%w: 이미 되돌린 변경입니다(%s)", ErrInvalid, e.RevertedBy)
		}
		if b := e.Before; len(b.Memories) == 0 && len(b.Created) == 0 && b.File == nil && b.Link == "" {
			return Proposal{}, fmt.Errorf("%w: 이 변경은 되돌릴 것이 없습니다(%s)", ErrInvalid, e.ID)
		}
		if e.TargetKind == TargetMemory {
			in.MemoryIDs = e.Targets
		}
		in.Body = strings.TrimSpace(in.Body + "\n\n되돌릴 변경(" + e.ID + ", 판정 " + e.Verdict + "):\n```diff\n" + e.Change + "```")
	}
	fp := fingerprint(in)
	var status string
	err := s.db.QueryRow(`SELECT status FROM proposals WHERE fingerprint=? AND status IN ('pending','rejected') LIMIT 1`, fp).Scan(&status)
	if err == nil {
		return Proposal{}, fmt.Errorf("%w (%s)", ErrDuplicateProposal, status)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, err
	}
	p := Proposal{ID: NewID("p-"), Kind: in.Kind, Title: in.Title, Body: in.Body, MemoryIDs: in.MemoryIDs, Payload: in.Payload,
		Status: StatusPending, CreatedAt: s.nowMs(), RetroID: in.RetroID}
	ids, _ := json.Marshal(nonNil(p.MemoryIDs))
	payload, _ := json.Marshal(p.Payload)
	_, err = s.db.Exec(`INSERT INTO proposals(id,kind,title,body,memory_ids,payload,fingerprint,status,created_at,retro_id) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Kind, p.Title, p.Body, string(ids), string(payload), fp, p.Status, p.CreatedAt, p.RetroID)
	return p, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

const proposalCols = `id,kind,title,body,memory_ids,payload,status,created_at,decided_at,retro_id,note`

func scanProposal(row interface{ Scan(...any) error }) (Proposal, error) {
	var p Proposal
	var ids, payload string
	if err := row.Scan(&p.ID, &p.Kind, &p.Title, &p.Body, &ids, &payload, &p.Status, &p.CreatedAt, &p.DecidedAt, &p.RetroID, &p.Note); err != nil {
		return p, err
	}
	_ = json.Unmarshal([]byte(ids), &p.MemoryIDs)
	_ = json.Unmarshal([]byte(payload), &p.Payload)
	return p, nil
}

// GetProposal reads one proposal.
func (s *Store) GetProposal(id string) (Proposal, error) {
	p, err := scanProposal(s.db.QueryRow(`SELECT `+proposalCols+` FROM proposals WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("제안이 없습니다: %s", id)
	}
	return p, err
}

// Proposals lists proposals with the given status (all when empty), oldest first.
func (s *Store) Proposals(status string) ([]Proposal, error) {
	q := `SELECT ` + proposalCols + ` FROM proposals`
	var args []any
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	rows, err := s.db.Query(q+` ORDER BY created_at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PendingCount is the number of proposals waiting.
func (s *Store) PendingCount() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM proposals WHERE status='pending'`).Scan(&n)
	return n
}

// DecideInput is the user's answer to a proposal. Title/Body edit before approving.
type DecideInput struct {
	ID       string
	Decision string // approve | reject
	Title    *string
	Body     *string
	Note     string
}

// DecideResult says what changed.
type DecideResult struct {
	Proposal Proposal `json:"proposal,omitzero"`
	Changed  []string `json:"changed,omitempty"`  // 바뀐 기억 id
	Created  string   `json:"created,omitempty"`  // 새로 만든 기억 id
	File     string   `json:"file,omitempty"`     // 바꾼 파일
	LedgerID string   `json:"ledgerId,omitempty"` // 승인한 변경의 원장 줄
	Link     string   `json:"link,omitempty"`     // 이은 연결(link), 뗀 연결(link 되돌리기)
}

// Decide approves or rejects a proposal and applies it.
func (s *Store) Decide(in DecideInput) (DecideResult, error) {
	p, err := s.GetProposal(in.ID)
	if err != nil {
		return DecideResult{}, err
	}
	if p.Status != StatusPending {
		return DecideResult{Proposal: p}, ErrDecided
	}
	res := DecideResult{}
	decision := in.Decision
	if decision == "approve" && (given(in.Title) || given(in.Body)) {
		decision = "edit"
	}
	switch in.Decision {
	case "reject":
		if p.Kind == ProposalConfirm {
			for _, id := range p.MemoryIDs {
				if m, err := getTx(s.db, id); err == nil && m.State == StateCandidate {
					if _, err := s.SetState(id, StateHidden, "제안 거절: "+in.Note); err != nil {
						return res, err
					}
					res.Changed = append(res.Changed, id)
				}
			}
		}
		if err := s.finish(&p, StatusRejected, in.Note); err != nil {
			return res, err
		}
	case "approve":
		before, err := s.snapshotFor(p)
		if err != nil {
			return res, err
		}
		if err := s.apply(p, in, &res); errors.Is(err, errAlreadyLinked) || errors.Is(err, errAlreadyRetagged) {
			note := "이미 이어짐"
			if errors.Is(err, errAlreadyRetagged) {
				note = "이미 태그가 바뀜"
			}
			if err := s.finish(&p, StatusSettled, note); err != nil {
				return res, err
			}
			res.Proposal = p
			return res, nil
		} else if err != nil {
			return res, err
		}
		if p.Kind == ProposalRetag { // 바뀌지 않은 기억은 원장·되돌리기에서 뺀다
			before.Memories = slices.DeleteFunc(before.Memories, func(m Memory) bool { return !slices.Contains(res.Changed, m.ID) })
		}
		if err := s.finish(&p, StatusApproved, in.Note); err != nil {
			return res, err
		}
		if res.LedgerID, err = s.recordLedger(p, before, res); err != nil {
			return res, fmt.Errorf("변경은 반영했지만 원장에 남기지 못했습니다: %w", err)
		}
	default:
		return res, fmt.Errorf("%w: decision은 approve 또는 reject", ErrInvalid)
	}
	res.Proposal = p
	if decision != "approve" {
		if err := s.attachDecision(p, decision, ClipRunes(MaskSecrets(in.Note), 300)); err != nil {
			return res, fmt.Errorf("결정은 반영했지만 판정 기록에 붙이지 못했습니다: %w", err)
		}
	}
	_ = s.RecordEvent(Event{Kind: EventProposalDecided}, map[string]any{"proposalId": p.ID, "proposalKind": p.Kind,
		"decision": decision, "waitMs": p.DecidedAt - p.CreatedAt})
	return res, nil
}

// mergedTags is what a merged memory carries: the tags of the memories it
// merges, in their order and each once as reads compare them, without the
// import marks (imported:*). The merged memory is a new judged memory, not an
// import, and it should stay in its graph clusters.
func mergedTags(s *Store, ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		m, err := getTx(s.db, id)
		if err != nil {
			continue
		}
		for _, t := range m.Tags {
			k := TagKey(t)
			if k == "" || seen[k] || strings.HasPrefix(k, ImportedPrefix) {
				continue
			}
			seen[k] = true
			out = append(out, t)
		}
	}
	return out
}

// given is a field the user actually filled in to edit before approving.
func given(p *string) bool { return p != nil && strings.TrimSpace(*p) != "" }

func (s *Store) finish(p *Proposal, status, note string) error {
	p.Status, p.DecidedAt, p.Note = status, s.nowMs(), note
	_, err := s.db.Exec(`UPDATE proposals SET status=?, decided_at=?, note=? WHERE id=?`, p.Status, p.DecidedAt, p.Note, p.ID)
	return err
}

func (s *Store) apply(p Proposal, in DecideInput, res *DecideResult) error {
	switch p.Kind {
	case ProposalConfirm:
		for _, id := range p.MemoryIDs {
			confirmed := StateConfirmed
			u := UpdateInput{ID: id, State: &confirmed, Reason: "제안 승인 " + p.ID, Action: "confirm"}
			if len(p.MemoryIDs) == 1 {
				u.Title, u.Body = in.Title, in.Body
			}
			if _, err := s.Update(u); err != nil {
				return err
			}
			res.Changed = append(res.Changed, id)
		}
	case ProposalStale:
		for _, id := range p.MemoryIDs {
			if _, err := s.SetState(id, StateStale, "제안 승인 "+p.ID+": "+p.Title); err != nil {
				return err
			}
			res.Changed = append(res.Changed, id)
		}
	case ProposalReclassify:
		for _, id := range p.MemoryIDs {
			k := p.Payload.Kind
			if _, err := s.Update(UpdateInput{ID: id, Kind: &k, Title: in.Title, Body: in.Body, Reason: "재분류 " + p.ID, Action: "reclassify"}); err != nil {
				return err
			}
			res.Changed = append(res.Changed, id)
		}
	case ProposalMerge, ProposalDirection:
		title, body, kind, scope := p.Payload.MergedTitle, p.Payload.MergedBody, p.Payload.Kind, p.Payload.Scope
		if p.Kind == ProposalDirection {
			title, body, kind = p.Title, p.Body, KindDecision
		}
		if in.Title != nil {
			title = *in.Title
		}
		if in.Body != nil {
			body = *in.Body
		}
		if scope == "" {
			scope = ScopeGlobal
			if len(p.MemoryIDs) > 0 {
				if first, err := getTx(s.db, p.MemoryIDs[0]); err == nil {
					scope = first.Scope
				}
			}
		}
		tags := p.Payload.Tags
		if p.Kind == ProposalMerge && len(tags) == 0 {
			tags = mergedTags(s, p.MemoryIDs)
		}
		m, err := s.Save(SaveInput{Kind: kind, Title: title, Body: body, Tags: tags, Scope: scope, State: StateConfirmed,
			Source: Source{Kind: SourceSession, Ref: "proposal:" + p.ID, Excerpt: "제안 승인으로 만듦"}})
		if err != nil {
			return err
		}
		if slices.Contains(p.MemoryIDs, m.ID) {
			// 합친 제목이 합치는 기억 하나의 제목과 같으면 Save는 그 기억을 돌려준다. 새로 만들지 않고 그 기억에
			// 합친 본문·태그를 새 판으로 쌓는다(옛 판은 원장으로 되돌린다).
			u := UpdateInput{ID: m.ID, Body: &body, Reason: "합침 " + p.ID, Action: "merge"}
			if p.Kind == ProposalMerge {
				u.Tags = &tags
			}
			if m, err = s.Update(u); err != nil {
				return err
			}
			res.Changed = append(res.Changed, m.ID)
		} else {
			res.Created = m.ID
		}
		if m.State != StateConfirmed {
			if m, err = s.SetState(m.ID, StateConfirmed, "제안 승인 "+p.ID); err != nil {
				return err
			}
		}
		for _, id := range p.MemoryIDs {
			if id == m.ID {
				continue
			}
			if _, err := s.Supersede(id, m.ID, "합침 "+p.ID); err != nil {
				return err
			}
			res.Changed = append(res.Changed, id)
		}
	case ProposalEdit:
		m, err := getTx(s.db, p.MemoryIDs[0])
		if err != nil {
			return err
		}
		next := editedMemory(m, p.Payload, in.Title, in.Body)
		if _, err := s.Update(UpdateInput{ID: m.ID, Title: &next.Title, Body: &next.Body, Tags: &next.Tags, Scope: &next.Scope, Project: &next.Project,
			Reason: "제안 승인 " + p.ID, Action: "edit"}); err != nil {
			return err
		}
		res.Changed = append(res.Changed, m.ID)
	case ProposalRevert:
		if given(in.Title) || given(in.Body) {
			return fmt.Errorf("%w: 되돌리기는 고쳐서 승인할 수 없습니다", ErrInvalid)
		}
		return s.revert(p, res)
	case ProposalLink:
		if given(in.Title) || given(in.Body) {
			return fmt.Errorf("%w: 연결 제안은 고쳐서 승인할 수 없습니다. 승인한 뒤 memory_update로 근거를 고쳐 주세요", ErrInvalid)
		}
		l, created, err := s.Link(LinkInput{From: p.MemoryIDs[0], To: p.MemoryIDs[1], Note: p.Payload.Note, Origin: LinkOriginRetro, OriginRef: p.ID})
		if err != nil {
			return err
		}
		if !created { // 그 사이 다른 길로 이어졌다. 그 연결은 이 승인의 것이 아니다
			return errAlreadyLinked
		}
		res.Link = l.ID
	case ProposalRetag:
		if given(in.Title) || given(in.Body) {
			return fmt.Errorf("%w: 태그 제안은 고쳐서 승인할 수 없습니다. 거절하고 태그를 직접 고쳐 주세요", ErrInvalid)
		}
		// 한 트랜잭션으로 읽고 쓴다: 중간에 실패해도 반쯤 바뀐 채 원장 없이 남지 않고, 읽은 뒤 바뀐 태그를 덮지 않는다.
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		now := s.nowMs()
		var changed []string
		for _, id := range p.MemoryIDs {
			m, err := getTx(tx, id)
			if err != nil {
				return err
			}
			next := retagged(m.Tags, p.Payload.FromTags, p.Payload.ToTags)
			if slices.Equal(next, m.Tags) {
				continue
			}
			m.Tags, m.Version, m.UpdatedAt = next, m.Version+1, now
			if err := writeTx(tx, m, "retag", "제안 승인 "+p.ID, now); err != nil {
				return err
			}
			changed = append(changed, id)
		}
		if len(changed) == 0 {
			return errAlreadyRetagged
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		res.Changed = append(res.Changed, changed...)
	case ProposalApplyFile:
		if in.Body != nil || in.Title != nil {
			return fmt.Errorf("%w: 파일 반영 제안은 고쳐서 승인할 수 없습니다. 거절하고 파일을 직접 고쳐 주세요", ErrInvalid)
		}
		if FileHash(p.Payload.Path) != p.Payload.BaseHash {
			return ErrFileChanged
		}
		target, err := realTarget(p.Payload.Path)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(target, []byte(p.Payload.Content)); err != nil {
			return err
		}
		res.File = p.Payload.Path
	}
	return nil
}

// CloseSettled closes confirm proposals whose memories are no longer candidates
// (confirmed by repetition, edited by hand or gone stale), link proposals
// whose memories got linked another way and retag proposals with nothing left
// to change, and returns how many.
func (s *Store) CloseSettled() (int, error) {
	pending, err := s.Proposals(StatusPending)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range pending {
		if p.Kind == ProposalRetag {
			if !s.retagPending(p) {
				if err := s.finish(&p, StatusSettled, "이미 태그가 바뀜"); err != nil {
					return n, err
				}
				n++
			}
			continue
		}
		if p.Kind == ProposalLink && len(p.MemoryIDs) == 2 {
			if linked, err := s.linked(p.MemoryIDs[0], p.MemoryIDs[1]); err != nil {
				return n, err
			} else if linked {
				if err := s.finish(&p, StatusSettled, "이미 이어짐"); err != nil {
					return n, err
				}
				n++
			}
			continue
		}
		if p.Kind != ProposalConfirm {
			continue
		}
		settled := true
		for _, id := range p.MemoryIDs {
			if m, err := getTx(s.db, id); err == nil && m.State == StateCandidate {
				settled = false
			}
		}
		if settled {
			if err := s.finish(&p, StatusSettled, "후보가 이미 정리됨"); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// realTarget follows symlinks so a dotfile link keeps pointing at its source
// and the source gets the change.
func realTarget(path string) (string, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		return path, nil
	}
	return filepath.Join(dir, filepath.Base(path)), nil
}

func writeFileAtomic(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".orbit-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// editedMemory is m after an edit proposal, with the user's title and body
// taking the place of the proposal's when given.
func editedMemory(m Memory, pl Payload, title, body *string) Memory {
	if pl.Title != "" {
		m.Title = pl.Title
	}
	if pl.Body != "" {
		m.Body = pl.Body
	}
	if pl.Tags != nil {
		m.Tags = cleanTags(pl.Tags)
	}
	if pl.Scope != "" {
		m.Scope = pl.Scope
		m.Project = ""
		if pl.Scope != ScopeGlobal {
			m.Project = pl.Project
		}
	}
	if given(title) {
		m.Title = strings.TrimSpace(*title)
	}
	if given(body) {
		m.Body = *body
	}
	return m
}

// prepareRetag checks a retag proposal and fills in what it changes: without
// memory ids, every memory in the graph that carries one of the tags to take
// out. Memories it would not change are dropped; the body shows each change.
func (s *Store) prepareRetag(in *ProposalInput) error {
	in.Payload.FromTags, in.Payload.ToTags = cleanTags(in.Payload.FromTags), cleanTags(in.Payload.ToTags)
	if len(in.Payload.ToTags) == 0 {
		return fmt.Errorf("%w: 넣을 태그(toTags)가 필요합니다", ErrInvalid)
	}
	for _, t := range append(append([]string{}, in.Payload.FromTags...), in.Payload.ToTags...) {
		if !IsTopicKey(TagKey(t)) {
			return fmt.Errorf("%w: %q는 주제 태그가 아닙니다(표식 태그·종류 이름은 고치지 않습니다)", ErrInvalid, t)
		}
	}
	hasFrom := func(m Memory) bool {
		return slices.ContainsFunc(in.Payload.FromTags, func(t string) bool { return hasTagKey(m.Tags, TagKey(t)) })
	}
	ids := in.MemoryIDs
	if len(ids) == 0 {
		if len(in.Payload.FromTags) == 0 {
			return fmt.Errorf("%w: 태그를 달 기억 id나 뺄 태그(fromTags)가 필요합니다", ErrInvalid)
		}
		all, err := s.list(DefaultGraphStates)
		if err != nil {
			return err
		}
		for _, m := range all {
			if hasFrom(m) {
				ids = append(ids, m.ID)
			}
		}
		sort.Strings(ids)
	}
	var lines []string
	in.MemoryIDs = nil
	for _, id := range uniq(ids) {
		m, err := getTx(s.db, id)
		if err != nil {
			return err
		}
		next := retagged(m.Tags, in.Payload.FromTags, in.Payload.ToTags)
		if slices.Equal(next, m.Tags) || len(in.Payload.FromTags) > 0 && !hasFrom(m) {
			continue // 묶기는 뺄 태그를 가진 기억만 고친다
		}
		in.MemoryIDs = append(in.MemoryIDs, id)
		lines = append(lines, fmt.Sprintf("- %s %s: %s → %s", id, ClipRunes(m.Title, 60), tagList(m.Tags), tagList(next)))
	}
	if len(in.MemoryIDs) == 0 {
		return fmt.Errorf("%w: 바뀌는 기억이 없습니다", ErrInvalid)
	}
	const shown = 30
	if len(lines) > shown {
		lines = append(lines[:shown], fmt.Sprintf("- 외 %d개", len(lines)-shown))
	}
	head := "넣을 태그: " + strings.Join(in.Payload.ToTags, ", ")
	if len(in.Payload.FromTags) > 0 {
		head = "바꿀 태그: " + strings.Join(in.Payload.FromTags, ", ") + " → " + strings.Join(in.Payload.ToTags, ", ")
	}
	in.Body = strings.TrimSpace(in.Body + "\n\n" + head + fmt.Sprintf("\n대상 기억 %d개:\n", len(in.MemoryIDs)) + strings.Join(lines, "\n"))
	return nil
}

// retagPending says whether a retag proposal still has a memory to change.
func (s *Store) retagPending(p Proposal) bool {
	for _, id := range p.MemoryIDs {
		if m, err := getTx(s.db, id); err == nil && !slices.Equal(retagged(m.Tags, p.Payload.FromTags, p.Payload.ToTags), m.Tags) {
			return true
		}
	}
	return false
}

// retagged is tags with the from tags (any spelling) taken out and the to
// tags put in where the first one was, or at the end. A to tag the memory
// already has in another spelling is not added again. When the result passes
// the tag limit, older tags give way from the end so the new ones stay.
func retagged(tags, from, to []string) []string {
	toKeys := map[string]bool{}
	for _, t := range to {
		toKeys[TagKey(t)] = true
	}
	remove := map[string]bool{}
	for _, t := range from {
		if k := TagKey(t); !toKeys[k] {
			remove[k] = true
		}
	}
	var kept []string
	at := -1
	for _, t := range tags {
		if remove[TagKey(t)] {
			if at < 0 {
				at = len(kept)
			}
			continue
		}
		kept = append(kept, t)
	}
	var add []string
	for _, t := range to {
		if !hasTagKey(kept, TagKey(t)) && !hasTagKey(add, TagKey(t)) {
			add = append(add, t)
		}
	}
	if at < 0 {
		at = len(kept)
	}
	if over := len(kept) + len(add) - maxTags; over > 0 {
		for over > 0 && len(kept) > 0 {
			kept = kept[:len(kept)-1]
			over--
		}
		at = min(at, len(kept))
	}
	out := append(append(append([]string{}, kept[:at]...), add...), kept[at:]...)
	return cleanTags(out)
}

func tagList(tags []string) string {
	if len(tags) == 0 {
		return "(없음)"
	}
	return strings.Join(tags, ", ")
}

func sortedKeys(tags []string) []string {
	var out []string
	for _, t := range tags {
		if k := TagKey(t); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// retagBusy is the pending tag merge that already works on these tags, or "":
// one that takes out a tag this one takes out or puts in, or puts in a tag
// this one takes out (the reverse or a chain). Two merges into the same tag
// from different tags do not clash.
func (s *Store) retagBusy(from, to []string) (string, error) {
	if len(from) == 0 {
		return "", nil
	}
	pending, err := s.Proposals(StatusPending)
	if err != nil {
		return "", err
	}
	overlap := func(a, b []string) bool {
		kb := sortedKeys(b)
		return slices.ContainsFunc(sortedKeys(a), func(k string) bool { return slices.Contains(kb, k) })
	}
	for _, p := range pending {
		if p.Kind != ProposalRetag || len(p.Payload.FromTags) == 0 {
			continue
		}
		if overlap(from, p.Payload.FromTags) || overlap(from, p.Payload.ToTags) || overlap(to, p.Payload.FromTags) {
			return p.ID, nil
		}
	}
	return "", nil
}
