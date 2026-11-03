package memory

// 보존 등급·재검토일·주제(기획 · Orbit Code K15). 휘발 기억은 정한 날이 지나면 회상에서 빠지고,
// 재검토일이 지난 기억은 낡음 후보가 되며, 같은 범위·주제의 여러 판은 최신 하나만 회상한다.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

// Retention classes. Normal is stored as "".
const (
	RetentionVolatile = "volatile" // 휘발: 1~7일 뒤 회상에서 빠진다
	RetentionNormal   = ""         // 보통(기본): 만료·재검토일 없음(주면 둔다)
	RetentionDurable  = "durable"  // 영구: 만료 없음, 재검토일(기본 90일)이 지나면 낡음 후보
)

// Lifecycle defaults.
const (
	DefaultVolatileDays = 7
	MaxVolatileDays     = 7
	DefaultReviewDays   = 90
	maxReviewDays       = 3650
)

// Freshness verdicts of a live memory.
const (
	FreshOK        = ""
	FreshExpired   = "expired"    // 휘발 기억의 날이 지남: 회상하지 않는다
	FreshReviewDue = "review_due" // 재검토일이 지남: 회상은 하되 낡음 후보로 표시한다
)

const dayMs = int64(24 * time.Hour / time.Millisecond)

// NormalizeRetention maps the names a caller may give to the stored class.
func NormalizeRetention(r string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "", "normal", "standard":
		return RetentionNormal, true
	case RetentionVolatile:
		return RetentionVolatile, true
	case RetentionDurable:
		return RetentionDurable, true
	}
	return "", false
}

// TopicKey is the stored form of a topic: normalized, spaces collapsed.
func TopicKey(topic string) string { return TitleKey(topic) }

func (in *SaveInput) validateLifecycle() error {
	r, ok := NormalizeRetention(in.Retention)
	if !ok {
		return i18n.Errorf("memory.err.retention", ErrInvalid)
	}
	in.Retention = r
	if r == RetentionVolatile {
		if in.TTLDays == 0 {
			in.TTLDays = DefaultVolatileDays
		}
		if in.TTLDays < 1 || in.TTLDays > MaxVolatileDays {
			return i18n.Errorf("memory.err.volatile_days", ErrInvalid, MaxVolatileDays)
		}
	} else if in.TTLDays != 0 {
		return i18n.Errorf("memory.err.ttl_volatile_only", ErrInvalid)
	}
	if r == RetentionDurable && in.ReviewAfterDays == 0 {
		in.ReviewAfterDays = DefaultReviewDays
	}
	if in.ReviewAfterDays < 0 || in.ReviewAfterDays > maxReviewDays {
		return i18n.Errorf("memory.err.review_days", ErrInvalid, maxReviewDays)
	}
	in.Topic = TopicKey(in.Topic)
	if len([]rune(in.Topic)) > 200 {
		return i18n.Errorf("memory.err.topic_size", ErrInvalid)
	}
	return nil
}

// lifecycle puts the checked lifecycle of in on a new memory made at at.
func (in SaveInput) lifecycle(m *Memory, at int64) {
	m.Retention, m.Topic, m.ReviewDays = in.Retention, in.Topic, in.ReviewAfterDays
	if in.Retention == RetentionVolatile {
		m.ExpiresAt = at + int64(in.TTLDays)*dayMs
	}
	if in.ReviewAfterDays > 0 {
		m.ReviewAt = at + int64(in.ReviewAfterDays)*dayMs
	}
}

// Freshness says whether a memory has run out (volatile past its days) or is
// due for review at now. Expiry comes first.
func (m Memory) Freshness(now time.Time) string {
	t := now.UnixMilli()
	switch {
	case m.ExpiresAt > 0 && t >= m.ExpiresAt:
		return FreshExpired
	case m.ReviewAt > 0 && t >= m.ReviewAt:
		return FreshReviewDue
	}
	return FreshOK
}

// AgeWeight is the age decay of recall ranking: full weight for 30 days since
// the memory last changed or came up again, half to 180 days, then a quarter.
// Relevance still leads; the weight only reorders memories of equal relevance.
func AgeWeight(m Memory, now time.Time) float64 {
	age := now.Sub(time.UnixMilli(m.UpdatedAt))
	switch {
	case age <= 30*24*time.Hour:
		return 1
	case age <= 180*24*time.Hour:
		return 0.5
	}
	return 0.25
}

// TopicHeads maps each (scope, topic) to its newest memory: the current value.
type TopicHeads map[string]string

func topicKeyOf(scope, topic string) string { return scope + "\x00" + topic }

// Superseding is the newer memory of m's topic that recall serves instead of
// m, or "" when m is the current value or has no topic.
func (h TopicHeads) Superseding(m Memory) string {
	if m.Topic == "" {
		return ""
	}
	if head := h[topicKeyOf(m.Scope, m.Topic)]; head != m.ID {
		return head
	}
	return ""
}

// TopicHeads reads the current value of every topic: the newest confirmed
// memory with that topic in its scope, so a candidate (what an AI learned)
// never hides a confirmed value or the user's own words. A topic with no
// confirmed memory takes its newest candidate or stale one, so an older value
// does not come back when the newest one is marked stale. Rejected (hidden)
// memories and volatile ones past their days do not count, so a temporary value
// leads only while it lasts.
func (s *Store) TopicHeads() (TopicHeads, error) {
	rows, err := s.db.Query(`SELECT scope, topic, id FROM memories WHERE topic<>'' AND state IN ('candidate','confirmed','stale')
		AND NOT (expires_at>0 AND expires_at<=?) ORDER BY state='confirmed' DESC, created_at DESC, id`, s.nowMs())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	h := TopicHeads{}
	for rows.Next() {
		var scope, topic, id string
		if err := rows.Scan(&scope, &topic, &id); err != nil {
			return nil, err
		}
		if k := topicKeyOf(scope, topic); h[k] == "" {
			h[k] = id
		}
	}
	return h, rows.Err()
}

// ReviewResult counts what ReviewDue did.
type ReviewResult struct {
	Expired  int `json:"expired"`  // 날이 지난 휘발 기억을 낡음으로 바꿈
	Proposed int `json:"proposed"` // 사람에게 낡음 정리를 물음(재검토일 지남, 사용자 말을 담은 휘발 기억)
}

// ReviewDue settles the lifecycle: an expired volatile memory is marked stale
// as an automatic change (in the ledger; one holding the user's words waits
// for the person), and a memory past its review date gets one pending stale
// proposal. Neither is proposed again once pending or rejected.
func (s *Store) ReviewDue() (ReviewResult, error) {
	var res ReviewResult
	live, err := s.List(StateCandidate, StateConfirmed)
	if err != nil {
		return res, err
	}
	now := s.Now()
	var errs []error
	for _, m := range live {
		switch m.Freshness(now) {
		case FreshExpired:
			out, err := s.SubmitChange(ProposalInput{Kind: ProposalStale, Title: "휘발 기억의 날이 지남: " + m.Title,
				Body: fmt.Sprintf("휘발 기억(%s)이 %s에 끝났습니다.", m.ID, Day(time.UnixMilli(m.ExpiresAt))), MemoryIDs: []string{m.ID}},
				Who{Actor: ActorAutomation})
			switch {
			case err == nil && out.Applied:
				res.Expired++
			case err == nil:
				res.Proposed++
			case !errors.Is(err, ErrDuplicateProposal) && !errors.Is(err, ErrAutoSkip):
				errs = append(errs, err)
			}
		case FreshReviewDue:
			_, err := s.Propose(ProposalInput{Kind: ProposalStale, Title: "재검토일이 지남: " + m.Title,
				Body: fmt.Sprintf("기억(%s)의 재검토일 %s이 지났습니다. 아직 맞으면 거절(또는 memory_update action=reviewed)하고, 틀리면 승인해 낡음으로 정리합니다.",
					m.ID, Day(time.UnixMilli(m.ReviewAt))), MemoryIDs: []string{m.ID}})
			switch {
			case err == nil:
				res.Proposed++
			case !errors.Is(err, ErrDuplicateProposal):
				errs = append(errs, err)
			}
		}
	}
	return res, errors.Join(errs...)
}

// Reviewed records that a memory was looked at and still holds: its review
// date moves on by its cadence (90 days when it has none) from now. It keeps
// the memory's content, so it is not a change to what the user said.
func (s *Store) Reviewed(id string, w Who) (Memory, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Memory{}, err
	}
	defer tx.Rollback()
	m, err := getTx(tx, id)
	if err != nil {
		return m, err
	}
	if m.State != StateCandidate && m.State != StateConfirmed {
		return m, i18n.Errorf("memory.err.review_live_only", ErrInvalid, id, m.State)
	}
	if m.ReviewDays <= 0 {
		m.ReviewDays = DefaultReviewDays
	}
	now := s.nowMs()
	m.ReviewAt = now + int64(m.ReviewDays)*dayMs
	m.Version++
	m.UpdatedAt = now
	reason := "재검토함"
	if w.actor() != ActorUser {
		reason += " (" + w.actor() + ")"
	}
	if err := writeTx(tx, m, "reviewed", reason, now); err != nil {
		return m, err
	}
	if err := tx.Commit(); err != nil {
		return m, err
	}
	return s.Get(id)
}
