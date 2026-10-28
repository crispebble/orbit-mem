package retro

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/transcript"
)

// KindSession is the short retrospective of one session, run when it ends.
const KindSession = "session"

// StatusCapped means the day's session retrospectives hit the limit; the
// daily retrospective reads that session instead.
const StatusCapped = "capped"

// SessionEnd is a session the host's session-end hook queued.
type SessionEnd struct {
	Host       string `json:"host"`
	Session    string `json:"session"`
	Transcript string `json:"transcript,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	EndedAt    int64  `json:"endedAt"`
}

// Enqueue queues a session for its session-end retrospective. A session
// already waiting is updated, not queued twice. Sessions without an id and
// sessions run inside Orbit's own data directory (retrospectives) are not
// queued; it says whether the session was queued.
func Enqueue(s *memory.Store, p config.Paths, e SessionEnd) (bool, error) {
	if strings.TrimSpace(e.Session) == "" || (e.Cwd != "" && transcript.Excluded(e.Cwd, []string{p.Home})) {
		return false, nil
	}
	if e.EndedAt == 0 {
		e.EndedAt = s.Now().UnixMilli()
	}
	_, err := s.DB().Exec(`INSERT INTO session_ends(host,session,transcript,cwd,ended_at,retro_id) VALUES(?,?,?,?,?,'')
		ON CONFLICT(host,session) DO UPDATE SET transcript=excluded.transcript, cwd=excluded.cwd, ended_at=excluded.ended_at, retro_id=''`,
		e.Host, e.Session, e.Transcript, e.Cwd, e.EndedAt)
	return err == nil, err
}

// Pending lists the queued sessions, oldest first.
func Pending(s *memory.Store) ([]SessionEnd, error) {
	rows, err := s.DB().Query(`SELECT host,session,transcript,cwd,ended_at FROM session_ends WHERE retro_id='' ORDER BY ended_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionEnd
	for rows.Next() {
		var e SessionEnd
		if err := rows.Scan(&e.Host, &e.Session, &e.Transcript, &e.Cwd, &e.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func markEnd(s *memory.Store, e SessionEnd, retroID string) {
	_, _ = s.DB().Exec(`UPDATE session_ends SET retro_id=? WHERE host=? AND session=? AND ended_at=?`, retroID, e.Host, e.Session, e.EndedAt)
}

// DrainSessions runs the queued session-end retrospectives under the PC-wide
// lock. When another retrospective holds the lock it returns at once: that
// run, or the next daily catch-up, takes the queue.
func (r *Runner) DrainSessions(ctx context.Context) ([]Result, error) {
	unlock, err := r.lock()
	if errors.Is(err, ErrBusy) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer unlock()
	return r.drain(ctx)
}

// drain runs the queue until it is empty. Callers hold the lock.
func (r *Runner) drain(ctx context.Context) ([]Result, error) {
	var results []Result
	var errs []error
	done := map[string]bool{}
	for {
		pending, err := Pending(r.Store)
		if err != nil {
			return results, err
		}
		var next *SessionEnd
		for i := range pending {
			key := fmt.Sprintf("%s:%s:%d", pending[i].Host, pending[i].Session, pending[i].EndedAt)
			if !done[key] {
				done[key] = true
				next = &pending[i]
				break
			}
		}
		if next == nil || ctx.Err() != nil {
			return results, errors.Join(errs...)
		}
		res, err := r.RunSession(ctx, *next)
		results = append(results, res)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s:%s: %w", next.Host, next.Session, err))
		}
	}
}

// sessionsToday counts the session-end retrospectives started today that
// reached the host (not empty or capped ones).
func (r *Runner) sessionsToday() int {
	t := r.now()
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).UnixMilli()
	var n int
	_ = r.Store.DB().QueryRow(`SELECT COUNT(*) FROM retros WHERE kind=? AND status NOT IN (?,?) AND started_at>=?`, KindSession, StatusEmpty, StatusCapped, start).Scan(&n)
	return n
}

// RunSession runs the session-end retrospective of one queued session. Callers hold the lock.
func (r *Runner) RunSession(ctx context.Context, e SessionEnd) (Result, error) {
	res := Result{Kind: KindSession, Period: dayOf(r.now())}
	path := e.Transcript
	if path == "" {
		path = transcript.FindSession(r.Roots, e.Host, e.Session)
	}
	sess, ok := transcript.SessionFile(e.Host, path, []string{r.Paths.Home})
	if ok {
		sess = uncovered(r.Store, sess)
	}
	hasUser := false
	for _, t := range sess.Turns {
		hasUser = hasUser || t.Role == "user"
	}
	key := e.Host + ":" + e.Session
	if ok && sess.ID != "" {
		key = sess.Host + ":" + sess.ID
	}
	if !ok || !hasUser {
		res.ID = memory.NewID("r-")
		nowMs := r.now().UnixMilli()
		res.Status = StatusEmpty
		_, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,finished_at,summary,session) VALUES(?,?,?,?,?,?,?,?,?)`,
			res.ID, KindSession, res.Period, StatusEmpty, "", nowMs, nowMs, "새 발화 없음", key)
		markEnd(r.Store, e, res.ID)
		return res, err
	}
	if limit := r.Cfg.SessionRetroDailyMax; limit > 0 && r.sessionsToday() >= limit {
		res.Status = StatusCapped
		markEnd(r.Store, e, StatusCapped)
		return res, nil
	}
	mat, err := r.sessionMaterial(sess, e)
	if err != nil {
		return res, err
	}
	mat.Session = key
	res, err = r.launch(ctx, mat, r.Cfg.SessionModel())
	markEnd(r.Store, e, res.ID)
	return res, err
}

func (r *Runner) sessionMaterial(sess transcript.Session, e SessionEnd) (Material, error) {
	last := sess.Turns[0].At
	for _, t := range sess.Turns {
		if t.At.After(last) {
			last = t.At
		}
	}
	mat := Material{Kind: KindSession, Period: dayOf(last.In(r.loc()))}
	ss, note := transcript.Fit([]transcript.Session{sess}, maxSessionBytes)
	mat.Sessions, mat.Note = ss, note
	maskSessions(mat.Sessions)
	mat.Read = readRanges(mat.Sessions)
	ids := injectedIDs(r.Store, e.Session, sess.ID)
	for _, id := range ids {
		if m, err := r.Store.Get(id); err == nil {
			b := brief(m, true)
			b.Body = clipBytes(b.Body, 600)
			mat.Injected = append(mat.Injected, b)
		}
	}
	if err := r.commonMaterial(&mat); err != nil {
		return mat, err
	}
	return mat, nil
}

// injectedIDs lists the memories the hooks put into a session, in order.
func injectedIDs(s *memory.Store, sessions ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, sid := range sessions {
		if sid == "" {
			continue
		}
		rows, err := s.DB().Query(`SELECT data FROM events WHERE session=? AND kind IN (?,?) ORDER BY id`, sid, memory.EventInjectSessionStart, memory.EventInjectPrompt)
		if err != nil {
			continue
		}
		for rows.Next() {
			var data string
			if rows.Scan(&data) != nil {
				continue
			}
			for _, id := range memoryIDs(data) {
				if !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
		}
		rows.Close()
	}
	return out
}

// ReadRange is the span of a session's turns a retrospective read.
type ReadRange struct {
	Key  string `json:"key"` // 호스트:세션 id
	From int64  `json:"from"`
	To   int64  `json:"to"`
}

func sessionKey(s transcript.Session) string { return s.Host + ":" + s.ID }

func readRanges(ss []transcript.Session) []ReadRange {
	var out []ReadRange
	for _, s := range ss {
		if len(s.Turns) == 0 {
			continue
		}
		rr := ReadRange{Key: sessionKey(s), From: s.Turns[0].At.UnixMilli(), To: s.Turns[0].At.UnixMilli()}
		for _, t := range s.Turns {
			ms := t.At.UnixMilli()
			rr.From, rr.To = min(rr.From, ms), max(rr.To, ms)
		}
		out = append(out, rr)
	}
	return out
}

// uncovered drops the turns another retrospective already read.
func uncovered(s *memory.Store, sess transcript.Session) transcript.Session {
	rows, err := s.DB().Query(`SELECT from_at, to_at FROM retro_coverage WHERE session_key=?`, sessionKey(sess))
	if err != nil {
		return sess
	}
	var spans [][2]int64
	for rows.Next() {
		var a, b int64
		if rows.Scan(&a, &b) == nil {
			spans = append(spans, [2]int64{a, b})
		}
	}
	rows.Close()
	if len(spans) == 0 {
		return sess
	}
	var kept []transcript.Turn
	for _, t := range sess.Turns {
		ms := t.At.UnixMilli()
		read := false
		for _, sp := range spans {
			read = read || (ms >= sp[0] && ms <= sp[1])
		}
		if !read {
			kept = append(kept, t)
		}
	}
	sess.Turns = kept
	return sess
}

func dropCovered(s *memory.Store, ss []transcript.Session) []transcript.Session {
	var out []transcript.Session
	for _, sess := range ss {
		if sess = uncovered(s, sess); len(sess.Turns) > 0 {
			out = append(out, sess)
		}
	}
	return out
}

func recordCoverage(s *memory.Store, retroID string, rr []ReadRange) {
	for _, r := range rr {
		_, _ = s.DB().Exec(`INSERT INTO retro_coverage(session_key,from_at,to_at,retro_id) VALUES(?,?,?,?)`, r.Key, r.From, r.To, retroID)
	}
}

// SessionResult is what a finished session-end retrospective left, for the daily one.
type SessionResult struct {
	Session string   `json:"session"`
	Summary string   `json:"summary"`
	Flags   []string `json:"flags,omitempty"`
}

func sessionResults(s *memory.Store, period string) []SessionResult {
	rows, err := s.DB().Query(`SELECT session, summary, flags FROM retros WHERE kind=? AND status=? AND period=? ORDER BY started_at`, KindSession, StatusDone, period)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SessionResult
	for rows.Next() {
		var sr SessionResult
		var flags string
		if rows.Scan(&sr.Session, &sr.Summary, &flags) == nil {
			sr.Flags = parseFlags(flags)
			out = append(out, sr)
		}
	}
	return out
}

// SessionStatus reads the latest session-end retrospective of a session (호스트:세션 id).
func SessionStatus(s *memory.Store, key string) (RetroStatus, error) {
	st, err := scanStatus(s.DB().QueryRow(`SELECT `+statusCols+` FROM retros WHERE kind=? AND session=? ORDER BY started_at DESC LIMIT 1`, KindSession, key))
	if errors.Is(err, sql.ErrNoRows) {
		return st, fmt.Errorf("세션 끝 회고가 없습니다: %s", key)
	}
	return st, err
}
