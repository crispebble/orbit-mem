package memory

// 대체 정책(명세 · orbit-mem 기억 정확성과 회상 품질 개선 4.3 C절). 저장한 새 기억이 같은 범위·주제의 현재값이나 지목한 옛
// 기억(supersedes·Replaces)을 만나면, 대체할지·자동 대체할지·어긋남으로 둘지를 「종류 × 새 근거 × 옛 근거」 표 하나로 정한다.
// 조건문으로 흩어 두지 않는 까닭: 이번에 사실만 다르게 다루고, 종류나 출처가 앞으로 늘 수 있다(명세 6.2).
//
// 지키는 것:
//   - 규칙·결정은 사용자 말이 아닌 것으로 대체하지 않는다(C-4). 자동 모드를 켜도 같다.
//   - 사실은 서버가 근거를 다시 확인했을 때만 사용자 말 사실을 스스로 대체한다(C-1). 근거만 적힌 사실은 자동 모드를 켰을 때만
//     대체한다(C-8). 대화로 배운 사실은 대체하지 않는다(C-7). 사람의 방식·사정(user 범위) 사실은 대상이 아니다(C-1).
//   - 자동 대체는 원장에 자동 변경으로 남아 감사 목록에서 되돌린다(C-3, supersede.go의 recordAutoReplaceTx).
//   - 자동 대체는 같은 범위·주제의 옛 사실만이다(C-1). supersedes로 다른 자리를 지목해도 어긋남이다.
//   - 사람이 자동 대체를 되돌린 옛 기억은 어떤 새 기억으로든 사용자 말이 아니면 다시 대체하지 않는다(C-3 「다시 자동으로
//     대체되지 않고」). 제목만 바꿔 같은 근거로 다시 저장해도 같다.
//   - 표에 없는 조합은 어긋남이다: 표를 빠뜨려도 사용자 말이 밀려나지 않는다.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// Outcome is what a save does to one old memory it meets.
type Outcome int

const (
	// OutcomeSupersede puts the old memory out of date by the new one, as a save always did (no ledger line).
	OutcomeSupersede Outcome = iota
	// OutcomeAutoSupersede does the same as an automatic change: a proposal kept as auto and a ledger line, so
	// the audit list shows it and undoes it (C-3).
	OutcomeAutoSupersede
	// OutcomeConflict leaves the old memory as it is and marks the pair as going against each other.
	OutcomeConflict
	// OutcomeRefuse refuses the whole save: a conflict with the memory the save named in Replaces (supersede.go
	// turns OutcomeConflict into it there, as the save path always refused).
	OutcomeRefuse
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSupersede:
		return "supersede"
	case OutcomeAutoSupersede:
		return "auto_supersede"
	case OutcomeConflict:
		return "conflict"
	case OutcomeRefuse:
		return "refuse"
	}
	return "unknown"
}

// NewGround is what the save at hand stands on: only this save's own source (newGroundOf), never a source the
// memory got earlier — so an AI saving again a memory that once carried the user's words is not the user.
type NewGround int

const (
	GroundQuote     NewGround = iota // 사용자 말(대조를 통과했거나 대조할 기록이 없는 말, 사람의 통로, 사용자 말을 함께 준 Replaces)
	GroundRechecked                  // 검증 출처, 서버가 파일을 직접 읽어 발췌를 확인함(Source.Rechecked)
	GroundStated                     // 검증 출처, 근거(ref·발췌)는 적혔으나 서버가 다시 확인하지 못함(명령 출력·외부 문서)
	GroundTalk                       // 그 밖의 믿을 수 있는 출처(대화·파일·명령·근거 없는 검증)
	GroundUntrusted                  // 믿을 수 없는 출처(untrusted·system)
)

// OldGround is what the old memory stands on.
type OldGround int

const (
	OldQuote          OldGround = iota // 사용자 말 출처가 있다(user 범위가 아님)
	OldQuoteUserScope                  // 사용자 말 출처가 있고 user 범위(사람의 방식·사정)다
	OldOther                           // 사용자 말이 아니고 믿을 수 있다
	// OldUntrusted is an old memory whose sources are all untrusted or system. It keeps the trust rule that
	// such memories replace one another (D7): with OldOther alone the row 「믿을 수 없음 → 그 밖」 would make
	// two untrusted memories a conflict.
	OldUntrusted
)

// kindMixed is the kind decideReplace gets for a pair of two different kinds where the old one is not a rule or
// a decision (replaceKind): only the rows for every kind match it, so a fact never replaces the user's lesson.
const kindMixed = ""

// replaceRow is one row of the table: the kinds it covers (nil is every kind), the grounds of the new and the
// old memory, whether it holds only for an old memory of the new one's own place (SameSlot: the same scope and
// the same, non-empty topic), and the outcome with the auto mode off and on.
type replaceRow struct {
	Kinds    []string
	New      []NewGround
	Old      []OldGround
	SameSlot bool
	Off, On  Outcome
}

var (
	anyOld    = []OldGround{OldQuote, OldQuoteUserScope, OldOther, OldUntrusted}
	notQuote  = []NewGround{GroundRechecked, GroundStated, GroundTalk, GroundUntrusted}
	trustedNw = []NewGround{GroundRechecked, GroundStated, GroundTalk}
)

// replaceTable is read top to bottom; the first row that matches decides.
var replaceTable = []replaceRow{
	{nil, []NewGround{GroundQuote}, anyOld, false, OutcomeSupersede, OutcomeSupersede}, // 사용자 말은 무엇이든 대체한다(D1)
	{nil, notQuote, []OldGround{OldQuoteUserScope}, false, OutcomeConflict, OutcomeConflict},
	{[]string{KindRule, KindDecision}, notQuote, []OldGround{OldQuote}, false, OutcomeConflict, OutcomeConflict},
	// 자동 대체는 같은 범위·주제의 사실만이다(C-1): supersedes로 다른 자리를 지목하면 아래 줄(어긋남)로 떨어진다.
	{[]string{KindFact}, []NewGround{GroundRechecked}, []OldGround{OldQuote}, true, OutcomeAutoSupersede, OutcomeAutoSupersede},
	{[]string{KindFact}, []NewGround{GroundStated}, []OldGround{OldQuote}, true, OutcomeConflict, OutcomeAutoSupersede},
	{[]string{KindFact}, []NewGround{GroundTalk, GroundUntrusted}, []OldGround{OldQuote}, false, OutcomeConflict, OutcomeConflict},
	{nil, notQuote, []OldGround{OldQuote}, false, OutcomeConflict, OutcomeConflict},
	{nil, []NewGround{GroundUntrusted}, []OldGround{OldUntrusted}, false, OutcomeSupersede, OutcomeSupersede},
	{nil, []NewGround{GroundUntrusted}, []OldGround{OldOther}, false, OutcomeConflict, OutcomeConflict},
	{nil, trustedNw, []OldGround{OldOther, OldUntrusted}, false, OutcomeSupersede, OutcomeSupersede},
}

// replaceRowOf is the index of the first row of replaceTable that matches, or -1. sameSlot says the old memory
// has the new one's scope and its same, non-empty topic.
func replaceRowOf(kind string, nw NewGround, old OldGround, sameSlot bool) int {
	for i, r := range replaceTable {
		if (r.Kinds == nil || slices.Contains(r.Kinds, kind)) && slices.Contains(r.New, nw) && slices.Contains(r.Old, old) && (!r.SameSlot || sameSlot) {
			return i
		}
	}
	return -1
}

// decideReplace is the outcome of the table for a pair. sameSlot says the old memory has the new one's scope and
// same, non-empty topic. revertedOld is true when the person undid an automatic replacement of the old memory
// before, by any new memory (supersede_reverts): it is a conflict unless the new ground is the user's words —
// saving the same ground again under another title must not replace it again. A pair the table does not cover
// is a conflict. The trust levels are also checked by mayOverride in supersedeTx, and the more careful answer
// wins there.
func decideReplace(kind string, nw NewGround, old OldGround, sameSlot, autoMode, revertedOld bool) Outcome {
	if revertedOld && nw != GroundQuote {
		return OutcomeConflict
	}
	i := replaceRowOf(kind, nw, old, sameSlot)
	if i < 0 {
		return OutcomeConflict
	}
	if autoMode {
		return replaceTable[i].On
	}
	return replaceTable[i].Off
}

// replaceKind is the kind the table sees for a new memory of newKind meeting an old one of oldKind: the old kind
// when it is a rule or a decision (what C-4 keeps), the kind of both when they are the same, else kindMixed.
func replaceKind(newKind, oldKind string) string {
	switch {
	case oldKind == KindRule || oldKind == KindDecision:
		return oldKind
	case newKind == oldKind:
		return newKind
	}
	return kindMixed
}

// newGroundOf is the ground of a save's source. A quote is the user's words whether or not it could be checked
// against the recorded utterances: a quote not found there was already lowered to a session source before the
// save (mcpserver checkQuote), and one left unchecked comes from a host that records no utterances, which keeps
// the behavior of before (명세 S-4 「지금 동작을 유지」) — taking it as not the user would stop every user
// replacement on such a host. A verification source counts as a ground only with what was run (Ref) and its
// result (Excerpt), like the confirmation evidence ③.
func newGroundOf(src Source) NewGround {
	switch {
	case src.Kind == SourceQuote:
		return GroundQuote
	case Untrusted(SourceTrust(src)):
		return GroundUntrusted
	case src.Kind == SourceVerification && strings.TrimSpace(src.Ref) != "" && strings.TrimSpace(src.Excerpt) != "":
		if src.Rechecked {
			return GroundRechecked
		}
		return GroundStated
	}
	return GroundTalk
}

// oldGroundOf is the ground of old memory old (its trust filled, as getTx does).
func oldGroundOf(q querier, old Memory) (OldGround, error) {
	p, err := protectedTx(q, old.ID)
	switch {
	case err != nil:
		return OldOther, err
	case p && old.Scope == ScopeGlobal:
		return OldQuoteUserScope, nil
	case p:
		return OldQuote, nil
	case Untrusted(old.Trust):
		return OldUntrusted, nil
	}
	return OldOther, nil
}

// revertedOldTx says whether the person undid an automatic replacement of memory old before, by whatever new
// memory. Undoing that undo (ledger.go) takes the pair out again.
func revertedOldTx(q querier, old string) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM supersede_reverts WHERE old_id=?`, old).Scan(&n)
	return n > 0, err
}

// sameSlot says whether old is in the place of nw: the same scope and the same, non-empty topic (C-1 「같은
// 범위·주제」). A memory named in supersedes or met on the same-title path may be anywhere.
func sameSlot(nw, old Memory) bool {
	return nw.Topic != "" && old.Scope == nw.Scope && TopicKey(old.Topic) == TopicKey(nw.Topic)
}

// autoMode is the auto mode a save is decided under: the save's own (a server that reads the setting on every
// save), else the store's policy.
func (s *Store) autoMode(in SaveInput) bool {
	if in.AutoMode != nil {
		return *in.AutoMode
	}
	return s.Policy.AutoMode
}

// blockRevertedReplace is why confirmBlocked stops a memory the person put back to candidate by undoing its
// automatic replacement of another (C-3).
const blockRevertedReplace = "reverted_replace"

// AutoSupersede is one automatic replacement of a user's fact (replace.go OutcomeAutoSupersede) as the session
// start tells it: the old and new memory with the titles they have now (the old one's from before the change).
type AutoSupersede struct {
	LedgerID string
	At       int64
	OldID    string
	OldTitle string
	NewID    string
	NewTitle string // 새 기억을 읽을 수 없으면(지웠다) ""
}

// metaAutoNoticeAt keeps, in meta, the time (ms) of the newest automatic replacement the session start has told.
const metaAutoNoticeAt = "notice.auto_supersede_at"

// AutoNoticeAt is the time (ms) up to which automatic replacements were told at a session start, 0 when none was.
func (s *Store) AutoNoticeAt() int64 {
	n, _ := strconv.ParseInt(s.Meta(metaAutoNoticeAt), 10, 64)
	return n
}

// SetAutoNoticeAt keeps that the automatic replacements up to at (ms) were told. Only a real session start (a
// bundle built for a session) sets it: a request or a tool call never does, so the notice is told at the next
// session start and then not again (명세 C-2).
func (s *Store) SetAutoNoticeAt(at int64) error {
	return s.SetMeta(metaAutoNoticeAt, strconv.FormatInt(at, 10))
}

// AutoSupersedesSince lists the automatic replacements of a user's fact made after at (ms), oldest first, that
// the person has not undone: the ones the next session start tells (명세 C-2). It is the supersede lines of the
// ledger kept as auto whose proposal names the new memory (Payload.NewID), so the daily tidy's own automatic
// changes are not among them.
func (s *Store) AutoSupersedesSince(at int64) ([]AutoSupersede, error) {
	rows, err := s.db.Query(`SELECT `+prefixed("l.", ledgerCols)+`, p.title, p.payload FROM ledger l JOIN proposals p ON p.id=l.proposal_id
		WHERE p.status=? AND p.kind=? AND l.at>? AND l.revert_of='' AND l.reverted_by='' ORDER BY l.at, l.rowid`, StatusAuto, ProposalSupersede, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoSupersede
	for rows.Next() {
		var title, payload string
		e, err := scanLedgerWith(rows, &title, &payload)
		if err != nil {
			return nil, err
		}
		var pl Payload
		if json.Unmarshal([]byte(payload), &pl) != nil || pl.NewID == "" || len(e.Targets) == 0 {
			continue
		}
		a := AutoSupersede{LedgerID: e.ID, At: e.At, OldID: e.Targets[0], NewID: pl.NewID}
		if len(e.Before.Memories) > 0 {
			a.OldTitle = e.Before.Memories[0].Title
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if m, err := getTx(s.db, out[i].NewID); err == nil {
			out[i].NewTitle = m.Title
		}
	}
	return out, nil
}

// MarkConflictAsk is the session mark kind of an instruction to ask the user about a conflict that a recall line
// has already given. The mark's id is the pair, ConflictPairKey: a pair is asked once a session.
const MarkConflictAsk = "conflict.ask"

// ConflictPairKey is the session mark id of the conflict between an old memory and a newer value.
func ConflictPairKey(oldID, newID string) string { return oldID + ">" + newID }
