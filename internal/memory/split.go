package memory

// 긴 기억 나누기(S4-9, 설계 · orbit-mem 2판 5-1): 한 기억에 주장 하나만 담는다. 1,000자 넘는 규칙·결정은 AI가 계획의
// split으로 주장마다 제목·주제·본문을 주고, 여기서는 그 나누기를 결정적으로 적용한다: 새 기억 N개(같은 종류·상태·
// 태그·출처)를 만들고 원래는 첫 조각이 대체한다(낡음 + 이유 「나눔」). 원장 한 줄이 남고 되돌리면 원래가 돌아오고
// 조각은 낡음이 된다.
//
// 지키는 것:
//   - 사용자 말 기억(출처 quote)은 자르기만 한다: 조각 본문을 이으면(공백 무시) 원래 본문과 같아야 한다.
//   - 조각은 둘 이상, 본문은 비지 않고 MaxClaimRunes 이하, 규칙·결정 조각은 주제가 있어야 한다.
//   - 조각의 주제는 그 자리의 다른 현재값이 쥐고 있으면 안 된다(어긋남), 조각끼리도 겹치지 않는다.
//   - 계획 뒤 바뀐 기억·상시 핵심·살아 있지 않은 기억은 건너뛰고, 사람이 되돌린 나누기는 다시 하지 않는다.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onidot-labs/orbit-mem/internal/place"
)

// MaxClaimRunes bounds the body of one claim (a rule or decision), 설계 · orbit-mem 2판 4.3.1.
const MaxClaimRunes = 1000

// ProposalSplit is a plan's split of one long memory into one memory per claim (S4-9).
const ProposalSplit = "split"

// SplitReason starts the stale reason of a split memory.
const SplitReason = "나눔"

func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// planSplit applies (or previews) one split item.
func (s *Store) planSplit(it PlanItem, m place.Map, dry bool) (PlanResult, error) {
	r := PlanResult{ID: it.ID, Action: "split", Basis: it.Basis}
	mem, err := getTx(s.db, it.ID)
	if err != nil {
		return r, err
	}
	r.From = place.AppliesTo(mem.Scope)
	if mem.State == StateStale && strings.HasPrefix(mem.StaleReason, SplitReason) {
		r.Skip = "already there"
		return r, nil
	}
	if mem.State != StateCandidate && mem.State != StateConfirmed {
		r.Skip = "not live: " + mem.State
		return r, nil
	}
	if it.Version != 0 && it.Version != mem.Version {
		r.Skip = fmt.Sprintf("changed: plan v%d, now v%d", it.Version, mem.Version)
		return r, nil
	}
	if mem.Pinned {
		r.Skip = "pinned: a person splits a core memory"
		return r, nil
	}
	scope, project := mem.Scope, mem.Project
	if it.AppliesTo != "" {
		sc, ok := place.ScopeOf(it.AppliesTo)
		if !ok {
			return r, fmt.Errorf("%w: appliesTo %q", ErrInvalid, it.AppliesTo)
		}
		if p := place.ProductOf(sc); p != "" {
			if _, ok := m[p]; !ok {
				r.Skip = "unknown product: " + p + " is not in the product map"
				return r, nil
			}
		}
		scope, project = sc, ""
		if strings.HasPrefix(sc, "project:") {
			project = strings.TrimPrefix(sc, "project:")
		}
		if sc == place.ScopeUnknown {
			project = mem.Project
		}
	}
	r.To = place.AppliesTo(scope)
	if why := checkParts(mem, it.Split); why != "" {
		r.Skip = why
		return r, nil
	}
	quote, err := s.quoteAt(mem.ID)
	if err != nil {
		return r, err
	}
	if quote != 0 {
		var joined strings.Builder
		for _, p := range it.Split {
			joined.WriteString(p.Body)
		}
		if squeeze(joined.String()) != squeeze(mem.Body) {
			r.Skip = "user quote: the parts of " + mem.ID + " are not a cut of the user's words (nothing added, nothing left out, same order)"
			return r, nil
		}
	}
	for _, p := range it.Split {
		if t := TopicKey(p.Topic); t != "" {
			if other := s.liveTopicHolder(scope, t, mem.ID); other != "" {
				r.Skip = "topic taken by " + other
				return r, nil
			}
		}
	}
	pi := ProposalInput{Kind: ProposalSplit, MemoryIDs: []string{mem.ID}}
	if err := s.notAgain(pi); errors.Is(err, ErrAutoSkip) || errors.Is(err, ErrDuplicateProposal) {
		r.Skip = "reverted before or already proposed"
		return r, nil
	} else if err != nil {
		return r, err
	}
	if dry {
		return r, nil
	}
	ids, err := s.splitTx(mem, it.Split, scope, project)
	if err != nil {
		return r, err
	}
	r.Parts = ids
	pi.Title = "이전 계획 나누기: " + mem.Title
	pi.Body = fmt.Sprintf("%s를 주장 하나씩 %d개로 나눔(%s → %s). 근거: %s", mem.ID, len(ids), r.From, r.To, it.Basis)
	pi.Payload = Payload{Actor: ActorAI}
	p, err := s.insertProposal(pi, StatusAuto)
	if err != nil {
		return r, err
	}
	if r.LedgerID, err = s.recordLedger(p, LedgerBefore{Memories: []Memory{mem}, Created: ids}, DecideResult{}); err != nil {
		return r, err
	}
	return r, nil
}

// checkParts says why parts cannot split mem ("" when they can).
func checkParts(mem Memory, parts []PlanPart) string {
	if len(parts) < 2 {
		return "split: needs two or more parts"
	}
	seen := map[string]bool{}
	for i, p := range parts {
		title := strings.TrimSpace(p.Title)
		switch {
		case title == "" || strings.ContainsAny(title, "\r\n") || utf8.RuneCountInString(title) > 200:
			return fmt.Sprintf("split: part %d needs a one-line title", i+1)
		case strings.TrimSpace(p.Body) == "":
			return fmt.Sprintf("split: part %d has no body", i+1)
		case utf8.RuneCountInString(strings.TrimSpace(p.Body)) > MaxClaimRunes:
			return fmt.Sprintf("split: part %d is over %d characters", i+1, MaxClaimRunes)
		}
		t := TopicKey(p.Topic)
		if t == "" && (mem.Kind == KindRule || mem.Kind == KindDecision) {
			return fmt.Sprintf("split: part %d of a %s needs a topic", i+1, mem.Kind)
		}
		if t != "" && seen[t] {
			return fmt.Sprintf("split: parts share the topic %q", t)
		}
		seen[t] = true
	}
	return ""
}

// splitTx makes the parts and supersedes mem in one transaction; it returns the new ids.
func (s *Store) splitTx(mem Memory, parts []PlanPart, scope, project string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.nowMs()
	type src struct {
		kind, ref, excerpt, hash string
		at                       int64
	}
	var srcs []src
	rows, err := tx.Query(`SELECT kind,ref,excerpt,hash,at FROM sources WHERE memory_id=? ORDER BY at, rowid`, mem.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x src
		if err := rows.Scan(&x.kind, &x.ref, &x.excerpt, &x.hash, &x.at); err != nil {
			rows.Close()
			return nil, err
		}
		srcs = append(srcs, x)
	}
	rows.Close()
	var ids []string
	for _, p := range parts {
		n := mem
		n.ID, n.Title, n.Body, n.Topic = NewID("m-"), strings.TrimSpace(p.Title), strings.TrimSpace(p.Body), TopicKey(p.Topic)
		n.Scope, n.Project, n.Version, n.UpdatedAt, n.Pinned = scope, project, 1, now, false
		n.SupersededBy, n.StaleReason, n.LegacyID = "", "", ""
		tagBytes, _ := json.Marshal(nonNil(n.Tags))
		tags := string(tagBytes)
		if _, err := tx.Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at,
			retention,review_days,review_at,expires_at,topic,tools) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			n.ID, n.Kind, n.Scope, n.Project, n.State, n.Title, TitleKey(n.Title), n.Body, tags, 1, n.CreatedAt, n.UpdatedAt,
			n.Retention, n.ReviewDays, n.ReviewAt, n.ExpiresAt, n.Topic, toolsJSON(n.Tools)); err != nil {
			return nil, err
		}
		for _, x := range srcs {
			if _, err := tx.Exec(`INSERT INTO sources(id,memory_id,kind,ref,excerpt,hash,at) VALUES(?,?,?,?,?,?,?)`,
				NewID("s-"), n.ID, x.kind, x.ref, x.excerpt, x.hash, x.at); err != nil {
				return nil, err
			}
		}
		if err := versionTx(tx, n, ProposalSplit, SplitReason+" "+mem.ID, now); err != nil {
			return nil, err
		}
		ids = append(ids, n.ID)
	}
	mem.State, mem.SupersededBy = StateStale, ids[0]
	mem.StaleReason = SplitReason + ": " + strings.Join(ids, ", ")
	mem.Version++
	mem.UpdatedAt = now
	if err := writeTx(tx, mem, ProposalSplit, mem.StaleReason, now); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}
