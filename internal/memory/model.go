package memory

import (
	"github.com/crispebble/orbit-mem/internal/i18n"
	"github.com/crispebble/orbit-mem/internal/place"
)

// Kinds of memory (설계 「기억 모델」).
const (
	KindRule     = "rule"     // 규칙: 내가 원하는 방식
	KindFact     = "fact"     // 사실
	KindDecision = "decision" // 결정
	KindLesson   = "lesson"   // 교훈
	KindJournal  = "journal"  // 일지: 그날 한 일, 회고가 쓴다
)

// States of memory.
const (
	StateCandidate = "candidate" // 후보
	StateConfirmed = "confirmed" // 확정
	StateStale     = "stale"     // 낡음
	StateHidden    = "hidden"    // 거절한 후보. 같은 제안이 다시 나오지 않게 남긴다
)

// Source kinds.
const (
	SourceQuote      = "quote"       // 내 말 인용
	SourceFile       = "file"        // 파일
	SourceCommand    = "command"     // 명령
	SourceSession    = "session"     // 대화 기록
	SourceHostMemory = "host_memory" // 호스트 기억
	SourceLegacy     = "legacy"      // 옛 Knowledge
	// SourceVerification is a fact the work's own verification checked (a test
	// or build run, or a file): a project fact with it is confirmed at once (A2
	// 증거 ③) only when the server read the file it names again (Source.Rechecked, E-4).
	SourceVerification = "verification"
	// SourceWork is a finished orbit-code work the work retrospective read (ref
	// is the work id): what came out of which works.
	SourceWork = "work"
)

// ScopeGlobal is the scope of a memory that applies everywhere.
const ScopeGlobal = place.ScopeGlobal

var (
	Kinds       = []string{KindRule, KindFact, KindDecision, KindLesson, KindJournal}
	States      = []string{StateCandidate, StateConfirmed, StateStale, StateHidden}
	SourceKinds = []string{SourceQuote, SourceFile, SourceCommand, SourceSession, SourceHostMemory, SourceLegacy, SourceVerification, SourceWork}
)

var (
	ErrNotFound        = i18n.Err("memory.err.not_found")
	ErrVersionConflict = i18n.Err("memory.err.version_conflict")
	ErrInvalid         = i18n.Err("memory.err.invalid")
)

// Source is where a memory came from.
type Source struct {
	ID      string `json:"id,omitempty"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
	Hash    string `json:"hash,omitempty"`
	At      int64  `json:"at,omitempty"`
	// Trust is how far the source is believed (owner·agent·untrusted·system, trust.go). Empty on the rows of
	// older versions and when it is what the kind gives (quote → owner, else agent): SourceTrust reads it.
	Trust string `json:"trust,omitempty"`
	// Rechecked says the server itself read the file a verification source names (Ref, an absolute path) and
	// found Excerpt in it when the source was saved (RecheckExcerpt). Only the assembly point sets it, never a
	// tool argument; it is stored in sources.rechecked and is what lets a verified fact be confirmed at once
	// (EvidenceVerifiedFact, E-4). It does not change what the fact replaces (replace.go). It is not read
	// back into Source nor written out with it: a copied or imported source is not rechecked.
	Rechecked bool `json:"-"`
}

// Memory is one remembered thing.
type Memory struct {
	ID           string       `json:"id"`
	Kind         string       `json:"kind"`
	Scope        string       `json:"scope"`
	Project      string       `json:"project,omitempty"` // 사람이 읽는 프로젝트 이름(원격 주소나 경로)
	State        string       `json:"state"`
	Title        string       `json:"title"`
	Body         string       `json:"body"`
	Tags         []string     `json:"tags"`
	Version      int          `json:"version"`
	CreatedAt    int64        `json:"createdAt"`
	UpdatedAt    int64        `json:"updatedAt"`
	SupersededBy string       `json:"supersededBy,omitempty"`
	StaleReason  string       `json:"staleReason,omitempty"`
	LegacyID     string       `json:"legacyId,omitempty"`
	Days         []string     `json:"days,omitempty"` // 후보가 나온 날
	Sources      []Source     `json:"sources,omitempty"`
	Links        []MemoryLink `json:"links,omitempty"`      // 살아 있는 명시 연결(읽을 때 붙는다, 판 스냅샷에는 없다)
	Retention    string       `json:"retention,omitempty"`  // 보존 등급: volatile·durable, 비면 보통(lifecycle.go)
	ReviewDays   int          `json:"reviewDays,omitempty"` // 재검토 주기(일)
	ReviewAt     int64        `json:"reviewAt,omitempty"`   // 재검토일(Unix ms): 마지막 확인 + 90일(휘발 제외). 지나면 재검토 후보
	ExpiresAt    int64        `json:"expiresAt,omitempty"`  // 휘발 기억이 회상에서 빠지는 때(Unix ms)
	Topic        string       `json:"topic,omitempty"`      // 주제 키: 같은 범위·주제는 최신 판만 회상
	Tools        []string     `json:"tools,omitempty"`      // 도구 트리거: 이 도구(이름 또는 접두*)를 부르기 직전에 회상
	// Aliases are other words for the same thing (a synonym, another spelling, the English name of a Korean
	// title) that only help it be found: they are indexed with the topic and tags and counted by the recall
	// score, but they are not part of the body (its size limit does not count them), and no recall line, find
	// line or session start carries them. At most MaxAliases of at most MaxAliasRunes each (aliases.go).
	// Version snapshots and the export keep them; the export leaves the key out when there are none.
	Aliases []string `json:"aliases,omitempty"`
	// AppliesTo is the scope as the tools show it (user, repo:<키>, product:<이름>, product:?), read from Scope.
	AppliesTo string `json:"appliesTo"`
	// Pinned is the always-on core (상시 핵심): put into every session start of its layer (설계 · orbit-mem 2판 4.4).
	Pinned bool `json:"pinned,omitempty"`
	// Trust is the highest trust of its sources (agent without any), worked out when it is read (trust.go).
	// It is not part of the snapshot or the export: the sources carry it.
	Trust string `json:"-"`
}

// Doc returns the searchable view.
func (m Memory) Doc() Doc { return Doc{ID: m.ID, Title: m.Title, Body: m.Body, Tags: m.Tags} }

// Version is one entry of a memory's history.
type Version struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
	At      int64  `json:"at"`
	Memory  Memory `json:"memory"`
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
