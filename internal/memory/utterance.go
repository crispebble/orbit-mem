package memory

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"unicode"
)

// 사용자 발언 기록과 사용자 말 대조(명세 · orbit-mem 기억 정확성과 회상 품질 개선 S-4). 요청 회상 훅이 사람 발언으로 가른
// 글을 받을 때 낱말 조각의 해시만 남기고, memory_save의 사용자 말(quote) 출처 발췌를 그 기록과 맞춰 본다. 원문은 어디에도
// 남기지 않는다(표·사건·오류 글). 세션도 호스트도 가리지 않고 대조하므로 MCP 서버가 세션을 몰라도 된다.
//
// 대조 규칙(matchesUtterance). 발췌를 두 낱말 조각으로 나눠 한 발언의 기록과 견준다. 통과하려면 셋을 모두 만족해야 한다.
//   - 첫 조각과 끝 조각이 그 발언에 있다. 처음이나 끝 낱말을 바꾸거나 지어낸 머리·꼬리를 붙이면 그 끝의 조각 하나만
//     잃으므로, 비율만으로는 막지 못한다.
//   - 빠진 조각은 발췌에 생략 표시(「…」 「..」 「⋯」)가 있는 이음매에만 있고, 잇달은 두 조각이 함께 빠지지 않는다.
//     가운데 낱말 하나를 바꾸거나 끼우면 잇달은 조각 둘을 잃는다. 표시 없이 낱말 하나를 빼면(부정 「안」을 떼는 것 등)
//     이음매 조각 하나만 잃어 「…」 생략과 같아 보이므로, 생략은 표시한 곳만 인정한다.
//   - 맞은 조각이 quoteMatchMin(0.8) 이상이다.
//
// 조각 크기 2와 문턱 0.8은 고정 묶음 시험(TestMatchQuote_고정_묶음을_조각_크기와_문턱이_가른다)으로 정했다. 세 낱말 조각은
// 짧은 발췌의 생략 하나에도 비율 아래로 떨어졌다. 대조를 잘못 통과시키면 지어낸 말이 사용자 말로 확정되고, 잘못
// 떨어뜨리면 후보로 저장되고 알림이 붙는다(AI가 말 그대로 다시 주면 된다). 그래서 엄한 쪽을 골랐다.
//
// 남는 한계: 발언의 앞부분을 그대로 잘라 낸 발췌는 통과한다(「하지 마」의 「마」를 빼고 끝내면 부정이 사라진다).
// 낱말 사이의 기호·문장부호는 정규화로 사라져(「-」 「/」 「%」) 다른 기호로 바꿔도 맞는다. 생략 표시로 한 발언 안의
// 먼 두 곳을 이어 붙일 수 있다(비율이 그만큼 떨어진다).
//
// 해시는 HMAC-SHA256(솔트, 조각)의 앞 16바이트다. 솔트는 DB마다 처음 기록할 때 만들어 meta에 둔다. 솔트는 다른 DB와
// 미리 계산한 표를 함께 쓰지 못하게 할 뿐, 같은 DB 파일을 가진 사람이 짧은 발언이나 흔한 두 낱말을 맞춰 보는 것은 막지
// 못한다. 그래서 기록은 대조에 필요한 30일(UtteranceDays)만 둔다.

// UtteranceDays is how many days a recorded utterance is kept and matched against: a quote is matched only
// against what the user said in the last UtteranceDays days, and the daily tidy drops older records.
const UtteranceDays = 30

// metaUtteranceSalt is the meta key holding this DB's HMAC key of utterance pieces (hex). It is made by the
// first record and never changed: a new key would leave every kept hash unmatchable.
const metaUtteranceSalt = "utterance.salt"

const (
	// quoteShingleWords is how many words one piece (shingle) of an utterance or an excerpt has.
	quoteShingleWords = 2
	// quoteMinWords is the fewest words an excerpt is matched by its pieces. A shorter excerpt (「응」「해 줘」)
	// matches only an utterance that is all of it, so one or two common words never pass as a part of
	// any long utterance. A rejected memory comes back only by a quote of at least this many words.
	quoteMinWords = 3
	// quoteMatchMin is the share of an excerpt's pieces one utterance must have for the excerpt to match.
	quoteMatchMin = 0.8
	// maxUtteranceWords bounds the words of one utterance recorded from each end: a longer one keeps its first
	// and its last maxUtteranceWords words (a request written after a long paste is kept), not its middle.
	// It also bounds the words of an excerpt that is matched: a longer one is not found.
	maxUtteranceWords = 4000
	// matchChunk is how many piece hashes one query of MatchQuote sends, well below SQLite's variable limit.
	matchChunk = 500
	// maxExcerptBytes is how much of an excerpt a source keeps (addSourceTx): the match looks at that part.
	maxExcerptBytes = 8 << 10
)

// QuoteMatch is how the excerpt of a quote source compared with the user's recorded utterances.
type QuoteMatch string

const (
	// QuoteMatched: one utterance of the last UtteranceDays days has the excerpt (the rule in this file's head).
	QuoteMatched QuoteMatch = "matched"
	// QuoteNotFound: utterances are recorded, and none has the excerpt.
	QuoteNotFound QuoteMatch = "not_found"
	// QuoteUnchecked means one thing only: no utterance of the last UtteranceDays days is recorded (a host
	// that does not record them, or none came yet), so there was nothing to compare with. A failed check
	// is an error, not this.
	QuoteUnchecked QuoteMatch = "unchecked"
)

// storedExcerpt is the excerpt as a source keeps it: secrets masked, cut at maxExcerptBytes on a rune
// boundary. cut says it was cut (its last word may be a part of a word).
func storedExcerpt(s string) (string, bool) {
	s = MaskSecrets(s)
	if len(s) <= maxExcerptBytes {
		return s, false
	}
	return truncateBytes(s, maxExcerptBytes), true
}

// quoteWords splits s into the words matching compares: width and case folded (Normalize), and anything
// that is not a letter, a digit or a mark taken as a space, so quotation marks and punctuation do not
// change a match. marked[i] says an omission mark (「…」, two or more dots, 「⋯」) stands between word i and
// word i+1.
func quoteWords(s string) (words []string, marked []bool) {
	var word, gap strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		if len(words) > 0 {
			g := gap.String()
			marked = append(marked, strings.Contains(g, "..") || strings.ContainsAny(g, "…⋯‥"))
		}
		words = append(words, word.String())
		word.Reset()
		gap.Reset()
	}
	for _, r := range Normalize(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			word.WriteRune(r)
			continue
		}
		flush()
		gap.WriteRune(r)
	}
	flush()
	return words, marked
}

// shingleKeys are the plain keys of the pieces of words, in order (a piece that comes twice is there twice).
// wholeKey is the one key of all of words, for an utterance or an excerpt shorter than quoteMinWords. The two
// kinds never collide (their prefixes differ). The keys never leave this process: only their hashes are kept.
func shingleKeys(words []string) []string {
	var out []string
	for i := 0; i+quoteShingleWords <= len(words); i++ {
		out = append(out, "s\x00"+strings.Join(words[i:i+quoteShingleWords], "\x1f"))
	}
	return out
}

func wholeKey(words []string) string { return "w\x00" + strings.Join(words, "\x1f") }

// utteranceKeys are the keys recorded for an utterance of words: the whole of a short one, else the pieces
// of its first and its last maxUtteranceWords words (no piece across the part left out).
func utteranceKeys(words []string) []string {
	if len(words) < quoteMinWords {
		return []string{wholeKey(words)}
	}
	if len(words) <= 2*maxUtteranceWords {
		return shingleKeys(words)
	}
	return append(shingleKeys(words[:maxUtteranceWords]), shingleKeys(words[len(words)-maxUtteranceWords:])...)
}

func pieceHash(salt []byte, key string) string {
	h := hmac.New(sha256.New, salt)
	h.Write([]byte(key))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// utteranceSaltTx returns this DB's salt, making it when create is true and there is none yet. Of two
// processes making it together the first insert wins and both read that one back.
func utteranceSaltTx(tx *sql.Tx, create bool) ([]byte, error) {
	if create {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO meta(key,value) VALUES(?,?)`, metaUtteranceSalt, hex.EncodeToString(b)); err != nil {
			return nil, err
		}
	}
	var v string
	err := tx.QueryRow(`SELECT value FROM meta WHERE key=?`, metaUtteranceSalt).Scan(&v)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(v)
}

// RecordUtterance keeps what the user said (text, a person's words as the recall hook told them from host
// messages) as the hashes of its pieces, for MatchQuote. The text itself is kept nowhere; secrets are masked
// first, as an excerpt is. An utterance without words is not recorded. host names the host it came from
// (kept with the record).
func (s *Store) RecordUtterance(host, text string) error {
	words, _ := quoteWords(MaskSecrets(text))
	if len(words) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	salt, err := utteranceSaltTx(tx, true)
	if err != nil {
		return err
	}
	id := NewID("u-")
	if _, err := tx.Exec(`INSERT INTO utterances(id, host, at) VALUES(?,?,?)`, id, host, s.nowMs()); err != nil {
		return err
	}
	// 긴 발언은 조각이 수천 개다: 여러 줄을 한 문장으로 넣는다(요청 회상 훅이 응답 전에 기다린다).
	keys := utteranceKeys(words)
	for start := 0; start < len(keys); start += matchChunk {
		chunk := keys[start:min(start+matchChunk, len(keys))]
		args := make([]any, 0, 2*len(chunk))
		for _, k := range chunk {
			args = append(args, pieceHash(salt, k), id)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO utterance_shingles(hash, utterance_id) VALUES (?,?)`+strings.Repeat(",(?,?)", len(chunk)-1), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MatchQuote compares the excerpt of a quote source, as the source will keep it (storedExcerpt), with the
// utterances of the last UtteranceDays days. It gives QuoteMatched with the record time (Unix ms) of the
// latest utterance that has the excerpt, QuoteNotFound (time 0) when none has it or the excerpt has more than
// maxUtteranceWords words, and QuoteUnchecked (time 0) when there is no utterance to compare with. An error
// is neither: the caller must not take the quote as checked. It writes nothing.
func (s *Store) MatchQuote(excerpt string) (QuoteMatch, int64, error) {
	since := s.nowMs() - UtteranceDays*dayMs
	var recorded int
	if err := s.db.QueryRow(`SELECT count(*) FROM utterances WHERE at >= ?`, since).Scan(&recorded); err != nil {
		return "", 0, err
	}
	if recorded == 0 {
		return QuoteUnchecked, 0, nil
	}
	stored, cut := storedExcerpt(excerpt)
	words, marked := quoteWords(stored)
	if cut && len(words) > 0 { // 잘린 끝 낱말은 낱말의 일부일 수 있다
		words, marked = words[:len(words)-1], marked[:max(len(words)-2, 0)]
	}
	if len(words) == 0 || len(words) > maxUtteranceWords {
		return QuoteNotFound, 0, nil
	}
	keys := []string{wholeKey(words)}
	if len(words) >= quoteMinWords {
		keys = shingleKeys(words)
	}
	tx, err := s.db.Begin() // 솔트와 조각을 한 읽기 시점에서 본다
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	salt, err := utteranceSaltTx(tx, false)
	if err != nil {
		return "", 0, err
	}
	if salt == nil { // 발언은 있는데 솔트가 없다: 이 DB의 기록이 깨졌다
		return "", 0, sql.ErrNoRows
	}
	hashes := make([]string, len(keys))
	distinct := map[string]bool{}
	for i, k := range keys {
		hashes[i] = pieceHash(salt, k)
		distinct[hashes[i]] = true
	}
	has, at, err := utterancesWithTx(tx, since, distinct)
	if err != nil {
		return "", 0, err
	}
	var best int64
	found := false
	for u, set := range has {
		if (len(keys) == 1 && set[hashes[0]]) || (len(keys) > 1 && matchesUtterance(hashes, marked, set)) {
			found = true
			best = max(best, at[u])
		}
	}
	if !found {
		return QuoteNotFound, 0, nil
	}
	return QuoteMatched, best, nil
}

// utterancesWithTx finds, among the utterances recorded since, which of hashes each has (utterance id to
// its hashes) and when each was recorded. The hashes go in chunks of matchChunk.
func utterancesWithTx(tx *sql.Tx, since int64, hashes map[string]bool) (map[string]map[string]bool, map[string]int64, error) {
	list := make([]string, 0, len(hashes))
	for h := range hashes {
		list = append(list, h)
	}
	has, at := map[string]map[string]bool{}, map[string]int64{}
	for start := 0; start < len(list); start += matchChunk {
		chunk := list[start:min(start+matchChunk, len(list))]
		args := []any{since}
		for _, h := range chunk {
			args = append(args, h)
		}
		rows, err := tx.Query(`SELECT p.utterance_id, p.hash, u.at FROM utterance_shingles p JOIN utterances u ON u.id = p.utterance_id
			WHERE u.at >= ? AND p.hash IN (?`+strings.Repeat(",?", len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id, h string
			var t int64
			if err := rows.Scan(&id, &h, &t); err != nil {
				rows.Close()
				return nil, nil, err
			}
			if has[id] == nil {
				has[id] = map[string]bool{}
			}
			has[id][h], at[id] = true, t
		}
		err = rows.Close()
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return has, at, nil
}

// matchesUtterance applies the rule of this file's head to the excerpt's pieces in order (hashes; piece i
// spans words i and i+1, so marked[i] is the omission mark of its seam) and the pieces one utterance has.
func matchesUtterance(hashes []string, marked []bool, has map[string]bool) bool {
	last := len(hashes) - 1
	if !has[hashes[0]] || !has[hashes[last]] {
		return false
	}
	hit := 0
	for i, h := range hashes {
		if has[h] {
			hit++
			continue
		}
		if !marked[i] || !has[hashes[i+1]] { // 표시 없는 이음매, 또는 잇달아 빠진 조각(끝 조각은 위에서 있다)
			return false
		}
	}
	return float64(hit) >= quoteMatchMin*float64(len(hashes))
}

// PruneUtterances drops the utterances recorded more than days days ago with their pieces, and says how
// many it dropped.
func (s *Store) PruneUtterances(days int) (int, error) {
	before := s.nowMs() - int64(days)*dayMs
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM utterance_shingles WHERE utterance_id IN (SELECT id FROM utterances WHERE at < ?)`, before); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM utterances WHERE at < ?`, before)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// ReviveRefusal is why a save of a rejected (hidden) memory's title did not bring it back (S-2).
type ReviveRefusal string

const (
	// ReviveNotChecked: the source is not the user's quote checked against what the user said.
	ReviveNotChecked ReviveRefusal = "not_checked"
	// ReviveShortQuote: the quote has fewer than quoteMinWords words.
	ReviveShortQuote ReviveRefusal = "short_quote"
	// ReviveBeforeRejection: the utterance the quote matched was recorded before the memory was rejected (or
	// at the same time): the user has not said it again since.
	ReviveBeforeRejection ReviveRefusal = "before_rejection"
)

// reviveRefusal says why the save in may not bring back the rejected memory id, or "" when it may: a
// checked quote of at least quoteMinWords words that matched an utterance recorded after the rejection.
func reviveRefusal(tx *sql.Tx, id string, in SaveInput) (ReviveRefusal, error) {
	if in.Source.Kind != SourceQuote || !in.QuoteMatched {
		return ReviveNotChecked, nil
	}
	if words, _ := quoteWords(in.Source.Excerpt); len(words) < quoteMinWords {
		return ReviveShortQuote, nil
	}
	at, err := rejectedAtTx(tx, id)
	if err != nil {
		return "", err
	}
	if in.QuoteAt <= at {
		return ReviveBeforeRejection, nil
	}
	return "", nil
}

// rejectedAtTx is when memory id was rejected: the time of the first hidden version after its last version
// that was not hidden (correctedSince reads a rejection the same way). Not the update time: a hidden memory's
// update time moves when the same title comes again.
func rejectedAtTx(tx *sql.Tx, id string) (int64, error) {
	var at sql.NullInt64
	err := tx.QueryRow(`SELECT MIN(v.at) FROM memory_versions v WHERE v.memory_id=? AND json_extract(v.snapshot,'$.state')=?
		AND v.version > COALESCE((SELECT MAX(p.version) FROM memory_versions p WHERE p.memory_id=? AND json_extract(p.snapshot,'$.state')<>?), 0)`,
		id, StateHidden, id, StateHidden).Scan(&at)
	return at.Int64, err
}
