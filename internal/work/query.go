package work

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// Page sizes and waits oni chose (계약 README 「oni가 정한 값」).
const (
	ListLimit      = 20
	ListLimitMax   = 50
	EventLimit     = 100
	EventLimitMax  = 200
	ChangesMax     = 500 // work_changes 한 응답에 담는 id 수. 넘으면 more
	WaitMax        = 30 * time.Second
	PollInterval   = 100 * time.Millisecond
	summaryScanMax = 5000
)

// ErrBadCursor is a page cursor oni cannot read or from another database.
var ErrBadCursor = errors.New("쪽 커서를 읽을 수 없습니다")

// EncodeSeq is the opaque ledger position readers pass back (seq, after).
func (s *Store) EncodeSeq(seq int64) string { return encodeSeq(s.dbID, seq) }

func encodeSeq(dbID string, seq int64) string {
	id, _ := hex.DecodeString(dbID)
	b := make([]byte, 0, 12)
	b = append(b, id...)
	b = binary.BigEndian.AppendUint64(b, uint64(seq))
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeSeq(s string) (string, int64, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != 12 {
		return "", 0, false
	}
	return hex.EncodeToString(b[:4]), int64(binary.BigEndian.Uint64(b[4:])), true
}

type pageCursor struct {
	Kind string `json:"k"`
	DB   string `json:"d"`
	Snap int64  `json:"s"`
	Pos  int64  `json:"p"`
	Sub  int    `json:"u,omitempty"`
	ID   string `json:"i,omitempty"`
	Host string `json:"h,omitempty"`
}

func (s *Store) encodeCursor(c pageCursor) string {
	c.DB = s.dbID
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Store) decodeCursor(raw, kind string) (*pageCursor, error) {
	if raw == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c pageCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.Kind != kind || c.DB != s.dbID {
		return nil, ErrBadCursor
	}
	return &c, nil
}

// Filter narrows lists of works and sessions.
type Filter struct {
	Project   string   // 비우면 모든 프로젝트
	CwdPrefix []string // 이 경로이거나 그 아래
	State     string   // 작업: open | done | "" ; 세션은 States
	States    []string
	Since     int64 // 밀리초. 이 시각 뒤에 갱신된 것만
	Cursor    string
	Limit     int
}

func (f Filter) limit(def, max int) int {
	if f.Limit <= 0 {
		return def
	}
	if f.Limit > max {
		return max
	}
	return f.Limit
}

func cwdCond(col string, prefixes []string) (string, []any) {
	if len(prefixes) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for _, p := range prefixes {
		p = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(p)), "/")
		if p == "" {
			p = "/"
		}
		// 경로 구분자 단위로 비교한다: /a/b는 /a/bc에 맞지 않는다
		sub := p + "/"
		if p == "/" {
			sub = "/"
		}
		parts = append(parts, "("+col+"=? OR substr("+col+",1,?)=?)")
		args = append(args, p, len(sub), sub)
	}
	return " AND (" + strings.Join(parts, " OR ") + ")", args
}

// WorkPage is a work_list page.
type WorkPage struct {
	Seq        int64
	Items      []WorkSummary
	NextCursor string
}

// ListWorks returns a page of work summaries, most recently changed first.
func (s *Store) ListWorks(f Filter, opt ReadOptions) (WorkPage, error) {
	cur, err := s.decodeCursor(f.Cursor, "w")
	if err != nil {
		return WorkPage{}, err
	}
	snap, err := s.LastSeq()
	if err != nil {
		return WorkPage{}, err
	}
	if cur != nil {
		snap = cur.Snap
	}
	cond, args := `updated_seq<=?`, []any{snap}
	if f.Project != "" {
		cond, args = cond+` AND project_scope=?`, append(args, f.Project)
	}
	c, a := cwdCond("cwd", f.CwdPrefix)
	cond, args = cond+c, append(args, a...)
	switch f.State {
	case "open":
		cond += ` AND closed_at=0`
	case "done":
		cond += ` AND closed_at>0`
	}
	if f.Since > 0 {
		cond, args = cond+` AND updated_at>?`, append(args, f.Since)
	}
	if cur != nil {
		cond, args = cond+` AND (updated_seq<? OR (updated_seq=? AND id>?))`, append(args, cur.Pos, cur.Pos, cur.ID)
	}
	limit := f.limit(ListLimit, ListLimitMax)
	rd := s.reader(opt)
	rows, err := rd.works(cond+` ORDER BY updated_seq DESC, id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return WorkPage{}, err
	}
	page := WorkPage{Seq: snap, Items: []WorkSummary{}}
	for i, w := range rows {
		if i == limit {
			last := rows[i-1]
			page.NextCursor = s.encodeCursor(pageCursor{Kind: "w", Snap: snap, Pos: last.updatedSeq, ID: last.id})
			break
		}
		g, err := rd.load(w)
		if err != nil {
			return WorkPage{}, err
		}
		page.Items = append(page.Items, rd.summaryDoc(g))
	}
	return page, nil
}

// GetWork returns one work's whole graph, with its children when asked.
func (s *Store) GetWork(id string, children bool, opt ReadOptions) (Work, int64, error) {
	seq, err := s.LastSeq()
	if err != nil {
		return Work{}, 0, err
	}
	rd := s.reader(opt)
	w, err := rd.getWork(id, children, 0)
	return w, seq, err
}

func (rd *reader) getWork(id string, children bool, depth int) (Work, error) {
	rows, err := rd.works(`id=?`, id)
	if err != nil {
		return Work{}, err
	}
	if len(rows) == 0 {
		return Work{}, ErrNotFound
	}
	g, err := rd.load(rows[0])
	if err != nil {
		return Work{}, err
	}
	doc := rd.workDoc(g)
	if children && depth < 16 {
		for _, st := range doc.Steps {
			for _, kid := range st.ChildWorkIDs {
				c, err := rd.getWork(kid, true, depth+1)
				if err != nil {
					return Work{}, err
				}
				doc.Children = append(doc.Children, c)
			}
		}
	}
	return doc, nil
}

// Summary returns one work's summary.
func (s *Store) Summary(id string, opt ReadOptions) (WorkSummary, int64, error) {
	seq, err := s.LastSeq()
	if err != nil {
		return WorkSummary{}, 0, err
	}
	rd := s.reader(opt)
	rows, err := rd.works(`id=?`, id)
	if err != nil || len(rows) == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return WorkSummary{}, 0, err
	}
	g, err := rd.load(rows[0])
	if err != nil {
		return WorkSummary{}, 0, err
	}
	return rd.summaryDoc(g), seq, nil
}

// OpenWorksAt lists open works whose cwd is dir (work_title without workId).
func (s *Store) OpenWorksAt(dir string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM works WHERE closed_at=0 AND cwd=? ORDER BY updated_seq DESC, id`, dir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetTitle records work_title's write as a ledger event.
func (s *Store) SetTitle(id, title string) (int64, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM works WHERE id=?`, id).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrNotFound
	}
	res, err := s.Append(NewEvent(KindTitle, "", "", 0, Title{Work: id, Title: title}))
	return res.Seq, err
}

// SessionPage is a work_sessions list page.
type SessionPage struct {
	Seq        int64
	Sessions   []Session
	NextCursor string
}

// ListSessions returns a page of sessions, most recently changed first.
func (s *Store) ListSessions(f Filter, opt ReadOptions) (SessionPage, error) {
	cur, err := s.decodeCursor(f.Cursor, "s")
	if err != nil {
		return SessionPage{}, err
	}
	snap, err := s.LastSeq()
	if err != nil {
		return SessionPage{}, err
	}
	if cur != nil {
		snap = cur.Snap
	}
	cond, args := `updated_seq<=?`, []any{snap}
	if f.Project != "" {
		cond, args = cond+` AND project_scope=?`, append(args, f.Project)
	}
	c, a := cwdCond("cwd", f.CwdPrefix)
	cond, args = cond+c, append(args, a...)
	pos, posID := int64(1<<62), ""
	if cur != nil {
		pos, posID = cur.Pos, cur.Host+"\x00"+cur.ID
	}
	want := map[string]bool{}
	for _, st := range f.States {
		want[st] = true
	}
	limit := f.limit(ListLimit, ListLimitMax)
	rd := s.reader(opt)
	page := SessionPage{Seq: snap, Sessions: []Session{}}
	scanned := 0
	// 상태는 읽을 때 계산하므로 거른 뒤 쪽을 채울 때까지 이어 읽는다
	for len(page.Sessions) <= limit && scanned < summaryScanMax {
		rows, err := rd.q.Query(`SELECT host, id, updated_seq FROM sessions WHERE `+cond+` AND (updated_seq<? OR (updated_seq=? AND host||char(0)||id>?))
			ORDER BY updated_seq DESC, host||char(0)||id LIMIT ?`, append(args, pos, pos, posID, limit+1)...)
		if err != nil {
			return SessionPage{}, err
		}
		type key struct {
			host, id string
			seq      int64
		}
		var batch []key
		for rows.Next() {
			var k key
			_ = rows.Scan(&k.host, &k.id, &k.seq)
			batch = append(batch, k)
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, k := range batch {
			scanned++
			pos, posID = k.seq, k.host+"\x00"+k.id
			sd := rd.session(k.host, k.id)
			if sd == nil {
				continue
			}
			doc := rd.sessionDoc(sd)
			if len(want) > 0 && !want[doc.State] {
				continue
			}
			if len(page.Sessions) == limit {
				prev := page.Sessions[limit-1]
				page.NextCursor = s.encodeCursor(pageCursor{Kind: "s", Snap: snap, Pos: rd.session(prev.Host, prev.SessionID).updatedSeq, ID: prev.SessionID, Host: prev.Host})
				return page, nil
			}
			page.Sessions = append(page.Sessions, doc)
		}
	}
	return page, nil
}

// SessionDetail is a work_sessions({sessionId}) page.
type SessionDetail struct {
	Seq        int64
	Session    Session
	Events     []SessionEvent
	Runs       []Run      // 첫 쪽에만
	Evidence   []Evidence // 첫 쪽에만
	First      bool
	NextCursor string
}

// SessionEvents returns one session and a page of its events, oldest first.
func (s *Store) SessionEvents(sessionID, cursor string, limit int, opt ReadOptions) (SessionDetail, error) {
	cur, err := s.decodeCursor(cursor, "e")
	if err != nil {
		return SessionDetail{}, err
	}
	var host string
	if cur != nil {
		host = cur.Host
	} else if err := s.db.QueryRow(`SELECT host FROM sessions WHERE id=? ORDER BY updated_seq DESC LIMIT 1`, sessionID).Scan(&host); errors.Is(err, sql.ErrNoRows) {
		return SessionDetail{}, ErrNotFound
	} else if err != nil {
		return SessionDetail{}, err
	}
	snap, err := s.LastSeq()
	if err != nil {
		return SessionDetail{}, err
	}
	if cur != nil {
		snap = cur.Snap
	}
	rd := s.reader(opt)
	sd := rd.session(host, sessionID)
	if sd == nil {
		return SessionDetail{}, ErrNotFound
	}
	if limit <= 0 {
		limit = EventLimit
	} else if limit > EventLimitMax {
		limit = EventLimitMax
	}
	d := SessionDetail{Seq: snap, Session: rd.sessionDoc(sd), Events: []SessionEvent{}, First: cur == nil}
	pos, sub := int64(0), 0
	if cur != nil {
		pos, sub = cur.Pos, cur.Sub
	}
	rows, err := s.db.Query(`SELECT seq,sub,at,kind,work_id,step_id,run_id,summary,transcript,agent_transcript FROM timeline
		WHERE host=? AND session=? AND seq<=? AND (seq>? OR (seq=? AND sub>?)) ORDER BY seq, sub LIMIT ?`, host, sessionID, snap, pos, pos, sub, limit+1)
	if err != nil {
		return SessionDetail{}, err
	}
	type tl struct {
		seq int64
		sub int
	}
	var last tl
	for rows.Next() {
		var t tl
		var at int64
		var e SessionEvent
		var tr, atr string
		if err := rows.Scan(&t.seq, &t.sub, &at, &e.Kind, &e.WorkID, &e.StepID, &e.RunID, &e.Summary, &tr, &atr); err != nil {
			rows.Close()
			return SessionDetail{}, err
		}
		if len(d.Events) == limit {
			d.NextCursor = s.encodeCursor(pageCursor{Kind: "e", Snap: snap, Pos: last.seq, Sub: last.sub, ID: sessionID, Host: host})
			break
		}
		e.Seq = encodeSeq(s.dbID, t.seq<<8|int64(t.sub&0xff))
		e.At = Stamp(at)
		if tr != "" || atr != "" {
			e.Ref = &EventRef{Transcript: tr, AgentTranscript: atr}
		}
		d.Events = append(d.Events, e)
		last = t
	}
	rows.Close()
	if d.First {
		runs, err := rd.runs(`host=? AND session=?`, host, sessionID)
		if err != nil {
			return SessionDetail{}, err
		}
		d.Runs = []Run{}
		for _, x := range runs {
			d.Runs = append(d.Runs, runDoc(x))
		}
		evs, err := rd.evidence(`host=? AND session=?`, host, sessionID)
		if err != nil {
			return SessionDetail{}, err
		}
		d.Evidence = []Evidence{}
		for _, e := range evs {
			d.Evidence = append(d.Evidence, evDoc(e))
		}
	}
	return d, nil
}

// Changes is a work_changes answer.
type Changes struct {
	Seq      int64
	Reset    bool
	More     bool
	WorkIDs  []string
	Sessions []string
}

// Changes returns works and sessions changed after the cursor, waiting up to
// wait for a change. A cursor that cannot continue gives Reset.
func (s *Store) Changes(ctx context.Context, after string, wait time.Duration) (Changes, error) {
	last, err := s.LastSeq()
	if err != nil {
		return Changes{}, err
	}
	db, from, ok := decodeSeq(after)
	var minSeq sql.NullInt64
	_ = s.db.QueryRow(`SELECT MIN(seq) FROM ledger`).Scan(&minSeq)
	if after == "" || !ok || db != s.dbID || from > last || (minSeq.Valid && from < minSeq.Int64-1) {
		return Changes{Seq: last, Reset: true}, nil
	}
	if wait > WaitMax {
		wait = WaitMax
	}
	deadline := time.Now().Add(wait)
	ver, _ := s.DataVersion(ctx)
	for {
		c, err := s.changesAfter(from)
		if err != nil || len(c.WorkIDs)+len(c.Sessions) > 0 || wait <= 0 {
			return c, err
		}
		for {
			if !time.Now().Before(deadline) {
				return c, nil
			}
			t := time.NewTimer(PollInterval)
			select {
			case <-ctx.Done():
				t.Stop()
				return c, nil
			case <-t.C:
			}
			v, err := s.DataVersion(ctx)
			if err != nil {
				return c, err
			}
			if v != ver {
				ver = v
				break
			}
		}
	}
}

func (s *Store) changesAfter(from int64) (Changes, error) {
	rows, err := s.db.Query(`SELECT seq, work_id, host, session FROM changes WHERE seq>? ORDER BY seq, work_id, host, session LIMIT ?`, from, ChangesMax*4)
	if err != nil {
		return Changes{}, err
	}
	defer rows.Close()
	c := Changes{Seq: from, WorkIDs: []string{}, Sessions: []string{}}
	seenW, seenS := map[string]bool{}, map[string]bool{}
	var cur, complete int64 = -1, from
	n := 0
	for rows.Next() {
		var seq int64
		var w, h, sess string
		if err := rows.Scan(&seq, &w, &h, &sess); err != nil {
			return Changes{}, err
		}
		n++
		if seq != cur {
			if cur >= 0 {
				complete = cur
			}
			// 사건 하나의 바뀐 것은 나누지 않는다. 더 담을 수 없으면 앞 사건까지만 돌려준다
			if len(seenW)+len(seenS) >= ChangesMax {
				c.Seq, c.More = complete, true
				return c, nil
			}
			cur = seq
		}
		if w != "" && !seenW[w] {
			seenW[w] = true
			c.WorkIDs = append(c.WorkIDs, w)
		}
		if sess != "" && !seenS[sess] {
			seenS[sess] = true
			c.Sessions = append(c.Sessions, sess)
		}
	}
	if n == ChangesMax*4 {
		// 행을 다 읽지 못해 마지막 사건이 잘렸을 수 있다. 그 사건은 다음 부름에서 다시 준다
		c.Seq, c.More = complete, true
	} else if cur >= 0 {
		c.Seq = cur
	}
	return c, rows.Err()
}

// Count returns open and all works.
func (s *Store) Count() (open, total int) {
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(closed_at=0),0), COUNT(*) FROM works`).Scan(&open, &total)
	return open, total
}
