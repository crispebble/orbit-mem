package memory

// 출처 등급(설계 9절, 결정 D6·D7, 수용 기준 L-9). 출처마다 얼마나 믿는지를 둔다: owner(사용자 본인 말),
// agent(호스트 AI가 배운 것), untrusted(남의 글, 웹·도구 출력, 채널의 다른 사람 메시지, 로그), system(cron·
// 자동화). 기억의 등급은 출처 등급 가운데 가장 높은 것이고 읽을 때 정한다. 출처가 untrusted·system뿐인
// 기억은 확정·핵심 승격이 안 되고 owner·agent 기억을 대체하지 못하며, 회상 때 「믿을 수 없음」 표시가 붙는다.
// 이 패키지는 저장한 쪽(호스트·명령)의 이름을 모른다: 통로의 상한(SaveInput.TrustLimit)은 조립 지점이 넘긴다.

import (
	"strings"

	"github.com/crispebble/orbit-mem/internal/i18n"
)

// Trust levels of sources.
const (
	TrustOwner     = "owner"     // 사용자 본인 말
	TrustAgent     = "agent"     // 호스트 AI가 배운 것
	TrustUntrusted = "untrusted" // 남의 글, 웹·도구 출력, 채널의 다른 사람 메시지, 로그
	TrustSystem    = "system"    // cron·자동화
)

// Trusts are the trust levels a source takes.
var Trusts = []string{TrustOwner, TrustAgent, TrustUntrusted, TrustSystem}

var (
	// ErrUntrusted means a memory whose sources are all untrusted or system was asked to be confirmed,
	// pinned, or to replace a memory of the owner or an agent.
	ErrUntrusted = i18n.Err("memory.err.untrusted")
	// ErrTrustNotStored means a lowered trust could not be written because the DB has no trust column
	// (a schema 4 copy was restored into it while this process had it open): the source is not saved
	// rather than saved as trusted. Opening the DB again migrates it.
	ErrTrustNotStored = i18n.Err("memory.err.trust_not_stored")
)

// trustRank orders the levels: untrusted and system are one group ("not to be believed").
func trustRank(t string) int {
	switch t {
	case TrustOwner:
		return 3
	case TrustAgent:
		return 2
	case TrustUntrusted, TrustSystem:
		return 1
	}
	return 0
}

func validTrust(t string) bool { return trustRank(t) > 0 }

// Untrusted says whether trust t is one not to be believed (untrusted or system).
func Untrusted(t string) bool { return t == TrustUntrusted || t == TrustSystem }

// kindTrust is the trust a source kind gives when none is written: the user's words are owner, the rest agent.
func kindTrust(kind string) string {
	if kind == SourceQuote {
		return TrustOwner
	}
	return TrustAgent
}

// ResolveTrust is the trust a new source gets. limit is the highest the channel it came by allows
// (empty is agent), requested what the writer asked for (empty is the limit). A quote of the user is
// always owner. The writer only lowers it: asking above the limit gives the limit.
func ResolveTrust(limit, kind, requested string) (string, error) {
	limit, requested = strings.TrimSpace(limit), strings.TrimSpace(requested)
	if limit == "" {
		limit = TrustAgent
	}
	if !validTrust(limit) || requested != "" && !validTrust(requested) {
		return "", i18n.Errorf("memory.err.trust_value", ErrInvalid, strings.Join(Trusts, "|"))
	}
	if kind == SourceQuote {
		return TrustOwner, nil
	}
	if requested == "" || trustRank(requested) > trustRank(limit) {
		return limit, nil
	}
	return requested, nil
}

// SourceTrust is the trust of a source as stored: empty (rows of older versions, or what its kind gives)
// reads by its kind, a quote is owner, and a value this version does not know is not believed.
func SourceTrust(src Source) string {
	switch {
	case src.Kind == SourceQuote:
		return TrustOwner
	case src.Trust == "":
		return kindTrust(src.Kind)
	case validTrust(src.Trust):
		return src.Trust
	}
	return TrustUntrusted
}

// storedTrust is what the trust column keeps for a source: empty when it is what the kind gives (so the
// rows stay as older versions write them), else the level, a value it does not know as untrusted.
func storedTrust(src Source) string {
	t := SourceTrust(src)
	if t == kindTrust(src.Kind) {
		return ""
	}
	return t
}

// higherTrust is the higher of a and b ("" is none); between untrusted and system untrusted is kept.
func higherTrust(a, b string) string {
	switch ra, rb := trustRank(a), trustRank(b); {
	case rb > ra:
		return b
	case rb == ra && b == TrustUntrusted:
		return b
	}
	return a
}

// TrustNote is the mark a recall line of m carries when m is not to be believed (its sources are all
// untrusted or system), or "".
func TrustNote(m Memory) string {
	if !Untrusted(m.Trust) {
		return ""
	}
	return i18n.T("memory.trust.note")
}

// noTrustColumn says whether err is SQLite's missing sources.trust column: a DB a schema 4 copy was
// restored into while this process had it open (like noIndexTable).
func noTrustColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such column: trust") || strings.Contains(msg, "has no column named trust")
}

// insertSourceTx writes one source row. A trust the kind gives is not written (the column keeps its
// default), so the usual row is the one older versions write. Without the column a raised trust (owner on
// a source that is not a quote) is kept as the kind's, and a lowered one is refused (ErrTrustNotStored).
func insertSourceTx(ex execQuerier, srcID, memoryID string, src Source, at int64) error {
	trust := storedTrust(src)
	if trust != "" {
		_, err := ex.Exec(`INSERT INTO sources(id,memory_id,kind,ref,excerpt,hash,at,trust) VALUES(?,?,?,?,?,?,?,?)`,
			srcID, memoryID, src.Kind, src.Ref, src.Excerpt, src.Hash, at, trust)
		if !noTrustColumn(err) {
			return err
		}
		if Untrusted(trust) {
			return ErrTrustNotStored
		}
	}
	_, err := ex.Exec(`INSERT INTO sources(id,memory_id,kind,ref,excerpt,hash,at) VALUES(?,?,?,?,?,?,?)`,
		srcID, memoryID, src.Kind, src.Ref, src.Excerpt, src.Hash, at)
	return err
}

// readTrusts is the trust of each memory whose sources match where (a condition on memory_id), from the
// kinds and trusts of its sources. A memory with no source is not in the map (agent, see fillTrust).
func readTrusts(q querier, where string, args ...any) (map[string]string, error) {
	rows, err := q.Query(`SELECT memory_id, kind, trust FROM sources WHERE `+where+` GROUP BY memory_id, kind, trust`, args...)
	if noTrustColumn(err) {
		rows, err = q.Query(`SELECT memory_id, kind, '' FROM sources WHERE `+where+` GROUP BY memory_id, kind`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id string
		var src Source
		if err := rows.Scan(&id, &src.Kind, &src.Trust); err != nil {
			return nil, err
		}
		out[id] = higherTrust(out[id], SourceTrust(src))
	}
	return out, rows.Err()
}

// fillTrust sets the trust of ms from their sources matching where; a memory without sources is agent.
func fillTrust(q querier, ms []Memory, where string, args ...any) error {
	if len(ms) == 0 {
		return nil
	}
	trusts, err := readTrusts(q, where, args...)
	if err != nil {
		return err
	}
	for i := range ms {
		ms[i].Trust = TrustAgent
		if t, ok := trusts[ms[i].ID]; ok {
			ms[i].Trust = t
		}
	}
	return nil
}

// memoryTrustTx is the trust of memory id now (agent without sources).
func memoryTrustTx(q querier, id string) (string, error) {
	ms := []Memory{{ID: id}}
	err := fillTrust(q, ms, `memory_id=?`, id)
	return ms[0].Trust, err
}

// trustOfAll is the highest trust of the memories ids (agent when none is given): what a memory made from
// them (a merge) is believed as.
func trustOfAll(q querier, ids []string) (string, error) {
	if len(ids) == 0 {
		return TrustAgent, nil
	}
	out := ""
	for _, id := range ids {
		t, err := memoryTrustTx(q, id)
		if err != nil {
			return "", err
		}
		out = higherTrust(out, t)
	}
	return out, nil
}

// resolveSources gives the sources of a save their trust under the save's limit.
func (in *SaveInput) resolveSources() error {
	t, err := ResolveTrust(in.TrustLimit, in.Source.Kind, in.Source.Trust)
	if err != nil {
		return err
	}
	in.Source.Trust = t
	for i := range in.Extra {
		if in.Extra[i].Trust, err = ResolveTrust(in.TrustLimit, in.Extra[i].Kind, in.Extra[i].Trust); err != nil {
			return err
		}
	}
	return nil
}
