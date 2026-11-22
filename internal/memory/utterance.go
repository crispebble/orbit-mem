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
// 남기지 않는다(표·사건·오류 글). 세션을 가리지 않고 대조하므로 MCP 서버가 세션을 몰라도 된다.
//
// 조각 크기와 통과 비율은 고정 묶음 시험(TestMatchQuote_고정_묶음을_조각_크기와_문턱이_가른다)으로 정했다. 두 낱말
// 조각에서는 생략 표시(…) 하나가 조각 하나만 잃고, 낱말 하나를 바꾸면 조각 둘을 잃는다. 세 낱말 조각은 짧은 발췌의 생략
// 하나에도 통과 비율 아래로 떨어졌다. 대조를 잘못 통과시키면 지어낸 말이 사용자 말로 확정되고(되돌리기 어렵다), 잘못
// 떨어뜨리면 후보로 저장되고 알림이 붙는다(AI가 말 그대로 다시 주면 된다). 그래서 문턱은 엄한 쪽(0.8)을 골랐다.
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
	// any long utterance (Review Focus 3).
	quoteMinWords = 3
	// quoteMatchMin is the share of an excerpt's pieces one utterance must have for the excerpt to match.
	quoteMatchMin = 0.8
	// maxUtteranceWords bounds the words of one utterance that are recorded (a long paste). Words past it are
	// not matched.
	maxUtteranceWords = 4000
)

// QuoteMatch is how the excerpt of a quote source compared with the user's recorded utterances.
type QuoteMatch string

const (
	// QuoteMatched: most of the excerpt's pieces are in one utterance of the last UtteranceDays days.
	QuoteMatched QuoteMatch = "matched"
	// QuoteNotFound: utterances are recorded, and none has the excerpt.
	QuoteNotFound QuoteMatch = "not_found"
	// QuoteUnchecked: no utterance of the last UtteranceDays days is recorded (a host that does not record
	// them, or none came yet), so the excerpt could not be compared.
	QuoteUnchecked QuoteMatch = "unchecked"
)

// quoteWords splits s into the words matching compares: width and case folded (Normalize), and anything
// that is not a letter, a digit or a mark taken as a space, so quotation marks, punctuation and an ellipsis
// do not change a match.
func quoteWords(s string) []string {
	return strings.FieldsFunc(Normalize(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
}

// pieceKeys are the plain keys of words: its shingles of quoteShingleWords words when it has at least
// quoteMinWords, else one key of all of it ("whole"). The two kinds never collide (their prefixes differ).
// The keys never leave this process: only their hashes are kept.
func pieceKeys(words []string) []string {
	if len(words) < quoteMinWords {
		return []string{"w\x00" + strings.Join(words, "\x1f")}
	}
	seen := map[string]bool{}
	var out []string
	for i := 0; i+quoteShingleWords <= len(words); i++ {
		k := "s\x00" + strings.Join(words[i:i+quoteShingleWords], "\x1f")
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
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
// messages) as the hashes of its pieces, for MatchQuote. The text itself is kept nowhere. An utterance
// without words is not recorded. host names the host it came from (kept with the record).
func (s *Store) RecordUtterance(host, text string) error {
	words := quoteWords(text)
	if len(words) == 0 {
		return nil
	}
	if len(words) > maxUtteranceWords {
		words = words[:maxUtteranceWords]
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
	for _, k := range pieceKeys(words) {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO utterance_shingles(hash, utterance_id) VALUES(?,?)`, pieceHash(salt, k), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MatchQuote compares the excerpt of a quote source with the utterances of the last UtteranceDays days:
// QuoteMatched when one utterance has at least quoteMatchMin of the excerpt's pieces (an excerpt of fewer
// than quoteMinWords words: when one utterance is all of it), QuoteNotFound when none does, and
// QuoteUnchecked when there is no utterance to compare with. It writes nothing.
func (s *Store) MatchQuote(excerpt string) (QuoteMatch, error) {
	since := s.nowMs() - UtteranceDays*dayMs
	var recorded int
	if err := s.db.QueryRow(`SELECT count(*) FROM utterances WHERE at >= ?`, since).Scan(&recorded); err != nil {
		return QuoteUnchecked, err
	}
	if recorded == 0 {
		return QuoteUnchecked, nil
	}
	words := quoteWords(excerpt)
	if len(words) == 0 {
		return QuoteNotFound, nil
	}
	tx, err := s.db.Begin() // 솔트와 조각을 한 읽기 시점에서 본다
	if err != nil {
		return QuoteUnchecked, err
	}
	defer tx.Rollback()
	salt, err := utteranceSaltTx(tx, false)
	if err != nil || salt == nil {
		return QuoteUnchecked, err
	}
	keys := pieceKeys(words)
	args := []any{since}
	for _, k := range keys {
		args = append(args, pieceHash(salt, k))
	}
	var best int
	err = tx.QueryRow(`SELECT COALESCE(MAX(n), 0) FROM (SELECT count(*) AS n FROM utterance_shingles p JOIN utterances u ON u.id = p.utterance_id
		WHERE u.at >= ? AND p.hash IN (?`+strings.Repeat(",?", len(keys)-1)+`) GROUP BY p.utterance_id)`, args...).Scan(&best)
	if err != nil {
		return QuoteUnchecked, err
	}
	if float64(best) >= quoteMatchMin*float64(len(keys)) {
		return QuoteMatched, nil
	}
	return QuoteNotFound, nil
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
