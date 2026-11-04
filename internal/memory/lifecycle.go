package memory

// 보존 등급·재검토일·주제(기획 · Orbit Code K15). 휘발 기억은 정한 날이 지나면 회상에서 빠지고,
// 재검토일이 지난 기억은 회상에 「재검토일 지남」으로 표시되며, 같은 범위·주제의 여러 판은 최신 하나만 회상한다.
// 오래 쓰지 않은 후보는 낡음이 된다(설계 · orbit-mem 2판 18.4).

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
	Expired int `json:"expired"` // 날이 지난 휘발 기억을 낡음으로 바꿈
	Held    int `json:"held"`    // 날이 지났지만 사용자 말을 담아 그대로 둠(회상에서는 이미 빠진다)
}

// ReviewDue settles the lifecycle: an expired volatile memory is marked stale
// as an automatic change (in the ledger). One holding the user's words stays
// as it is: recall already leaves it out. A memory past its review date gets
// no proposal; recall marks it 「재검토일 지남」 and the AI checks it with the
// user while working (설계 · orbit-mem 2판 18.3). Old candidates go stale by
// ExpireCandidates.
func (s *Store) ReviewDue() (ReviewResult, error) {
	var res ReviewResult
	live, err := s.List(StateCandidate, StateConfirmed)
	if err != nil {
		return res, err
	}
	now := s.Now()
	var errs []error
	for _, m := range live {
		if m.Freshness(now) != FreshExpired {
			continue
		}
		out, err := s.SubmitChange(ProposalInput{Kind: ProposalStale, Title: "휘발 기억의 날이 지남: " + m.Title,
			Body: fmt.Sprintf("휘발 기억(%s)이 %s에 끝났습니다.", m.ID, Day(time.UnixMilli(m.ExpiresAt))), MemoryIDs: []string{m.ID}},
			Who{Actor: ActorAutomation})
		switch {
		case err == nil && out.Applied:
			res.Expired++
		case err == nil:
			res.Held++
		case !errors.Is(err, ErrDuplicateProposal) && !errors.Is(err, ErrAutoSkip):
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}

// DefaultCandidateExpiryDays is how long a candidate may go unused before it
// goes stale (설정 candidateExpiryDays).
const DefaultCandidateExpiryDays = 28

// maxExpirePerRun caps how many candidates one run marks stale, so a wrong
// guess is a short list in the audit, not a flood.
const maxExpirePerRun = 50

// activeDays is how many days of a window of days this PC must have been used
// for "not used" to mean something: a quarter of it, at most 7.
func activeDays(days int) int { return max(1, min(7, days/4)) }

// ExpireCandidates marks stale, as automatic changes kept in the ledger, the
// candidates nobody used for days: not changed, not put into a session or a
// request, not found or read, no usage verdict. A candidate holding the user's
// words is left alone. Nothing happens while the injection log is shorter than
// days (a new install has not had the chance to use them) or when this PC was
// used on fewer than activeDays(days) days of that window (back from a break,
// nothing had the chance either). At most maxExpirePerRun go stale at once.
// 0 days turns it off; so does the caller when recall is not injected
// (inject rules·off never puts candidates in). It returns how many went stale.
func (s *Store) ExpireCandidates(days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	last, _, err := s.LastUse()
	if err != nil {
		return 0, err
	}
	cutoff := s.Now().AddDate(0, 0, -days).UnixMilli()
	var earliest int64
	var active int
	if err := s.db.QueryRow(`SELECT COALESCE(MIN(at),0) FROM events WHERE kind IN (?,?)`, EventInjectSessionStart, EventInjectPrompt).Scan(&earliest); err != nil {
		return 0, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT day) FROM events WHERE at>=?`, cutoff).Scan(&active); err != nil {
		return 0, err
	}
	if earliest == 0 || earliest > cutoff || active < activeDays(days) {
		return 0, nil
	}
	cands, err := s.List(StateCandidate)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, m := range cands {
		if m.CreatedAt >= cutoff || m.UpdatedAt >= cutoff || last[m.ID] >= cutoff {
			continue
		}
		if p, err := s.Protected(m.ID); err != nil || p {
			continue
		}
		used := last[m.ID]
		if used < m.UpdatedAt {
			used = m.UpdatedAt
		}
		_, err := s.AutoApply(AutoInput{Kind: ProposalStale, Title: fmt.Sprintf("%d일 동안 쓰지 않은 후보: %s", days, m.Title),
			Body:      fmt.Sprintf("후보(%s)를 %s 뒤로 넣지도 찾지도 고치지도 않았습니다. 아직 맞으면 되돌립니다(후보로 돌아오고 다시 낡음으로 하지 않습니다).", m.ID, Day(time.UnixMilli(used))),
			MemoryIDs: []string{m.ID}, Who: Who{Actor: ActorAutomation}})
		switch {
		case err == nil:
			n++
			if n >= maxExpirePerRun {
				return n, errors.Join(errs...)
			}
		case !errors.Is(err, ErrAutoSkip) && !errors.Is(err, ErrDuplicateProposal) && !errors.Is(err, ErrProtected):
			errs = append(errs, fmt.Errorf("%s: %w", m.ID, err))
		}
	}
	return n, errors.Join(errs...)
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
