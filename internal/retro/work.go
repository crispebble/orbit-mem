package retro

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
	"github.com/onidot-labs/orbit-mem/internal/transcript"
)

// KindWork is the retrospective of one finished OpenOrbit work (AU-5): it
// reads the work's record (what was done, the verification evidence, the
// user's words) instead of a transcript.
const KindWork = "work"

// WorkHost is the host of a work record's pseudo-session in the material.
const WorkHost = "openorbit"

// WorkStatuses are the ends a work can have.
var WorkStatuses = []string{"completed", "failed", "stopped", "cancelled"}

// ErrWorkInput means the work record handed to prepare is not usable.
var ErrWorkInput = i18n.Err("retro.err.work_input")

// WorkIn is the record of a finished work OpenOrbit hands to prepare.
type WorkIn struct {
	Ref       string           `json:"ref"`
	Cwd       string           `json:"cwd"`
	Title     string           `json:"title"`
	Status    string           `json:"status"`
	EndedAt   int64            `json:"endedAt"`
	Text      string           `json:"text"`
	UserWords []WorkWordIn     `json:"userWords"`
	Evidence  []WorkEvidenceIn `json:"evidence"`
	Recalled  []string         `json:"recalled"`
	Verified  bool             `json:"verified"`
}

// WorkWordIn is something the user typed during the work.
type WorkWordIn struct {
	Ref        string `json:"ref"`
	Text       string `json:"text"`
	At         int64  `json:"at"`
	Correction bool   `json:"correction"`
}

// WorkEvidenceIn is one line of the work's verification evidence.
type WorkEvidenceIn struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

// Validate checks what prepare needs: an id, an absolute folder, an end time
// and one of the four ends.
func (in WorkIn) Validate() error {
	switch {
	case strings.TrimSpace(in.Ref) == "":
		return i18n.Errorf("retro.err.work_ref", ErrWorkInput)
	case !filepath.IsAbs(in.Cwd):
		return i18n.Errorf("retro.err.work_cwd", ErrWorkInput)
	case in.EndedAt <= 0:
		return i18n.Errorf("retro.err.work_ended", ErrWorkInput)
	case !slices.Contains(WorkStatuses, in.Status):
		return i18n.Errorf("retro.err.work_status", ErrWorkInput, strings.Join(WorkStatuses, "·"))
	}
	return nil
}

// Work is the work part of a work retrospective's material.
type Work struct {
	Ref      string         `json:"ref"`
	Cwd      string         `json:"cwd"`
	Title    string         `json:"title"`
	Status   string         `json:"status"`
	Verified bool           `json:"verified"`
	EndedAt  int64          `json:"endedAt"`
	Text     string         `json:"text"`
	Evidence []WorkEvidence `json:"evidence,omitempty"`
	Words    []WorkWord     `json:"words,omitempty"`
}

// WorkEvidence is one verification evidence line, as the material shows it.
type WorkEvidence struct {
	Ref  string `json:"ref,omitempty"`
	Text string `json:"text"`
}

// WorkWord is one thing the user typed, as the material shows it.
type WorkWord struct {
	Ref        string `json:"ref,omitempty"`
	Text       string `json:"text"`
	At         int64  `json:"at"`
	Correction bool   `json:"correction,omitempty"`
}

const (
	maxWorkExisting = 60      // 「지금 있는 기억」 상한
	maxWorkEvidence = 200     // 검증 증거 줄 상한
	maxEvidenceLine = 4 << 10 // 증거 한 줄 상한
)

// workStaleAfter is how long a work retrospective may stay running or
// submitting before a new prepare of the same work end takes over: OpenOrbit's
// run timeout is 30 minutes, so a row older than this lost its runner.
const workStaleAfter = 45 * time.Minute

// workKey is what the retros row keeps to run a work's end once: the same work
// ending at the same time is the same retrospective.
func workKey(ref string, endedAt int64) string {
	return WorkHost + ":" + ref + "#" + strconv.FormatInt(endedAt, 10)
}

// activeWork says whether a retrospective of this work's end is running or done.
func (r *Keeper) activeWork(key string) bool {
	var n int
	_ = r.Store.DB().QueryRow(`SELECT COUNT(*) FROM retros WHERE kind=? AND session=? AND status IN (?,?,?)`,
		KindWork, key, StatusRunning, "submitting", StatusDone).Scan(&n)
	return n > 0
}

// closeStaleWork fails the runs of key still running or submitting after
// workStaleAfter: their runner died (reboot, kill, finish that never came), so
// they no longer hold the work's end. Their material goes as finish would do.
func (r *Keeper) closeStaleWork(key string) error {
	now := r.now()
	cutoff := now.Add(-workStaleAfter).UnixMilli()
	rows, err := r.Store.DB().Query(`SELECT id FROM retros WHERE kind=? AND session=? AND status IN (?,?) AND started_at<?`,
		KindWork, key, StatusRunning, "submitting", cutoff)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		// 같은 조건으로 닫는다: 그사이 끝난(done) 줄은 건드리지 않는다.
		if _, err := r.Store.DB().Exec(`UPDATE retros SET status=?, finished_at=?, error=? WHERE id=? AND status IN (?,?) AND started_at<?`,
			StatusFailed, now.UnixMilli(), i18n.T("retro.work_abandoned"), id, StatusRunning, "submitting", cutoff); err != nil {
			return err
		}
		os.RemoveAll(filepath.Join(r.Paths.RetroDir(), id))
	}
	return nil
}

// PrepareWork prepares the retrospective of one finished work for host. A work
// end already running or done is skipped (a failed or empty one runs again,
// and so does one running longer than workStaleAfter, which is failed first);
// an empty record finishes as empty.
func (r *Keeper) PrepareWork(host string, in WorkIn) (Prepared, error) {
	if err := in.Validate(); err != nil {
		return Prepared{Kind: KindWork}, err
	}
	in.Ref = strings.TrimSpace(in.Ref)
	period := dayOf(time.UnixMilli(in.EndedAt).In(r.loc()))
	key := workKey(in.Ref, in.EndedAt)
	if err := r.closeStaleWork(key); err != nil {
		return Prepared{Kind: KindWork, Period: period}, err
	}
	if r.activeWork(key) {
		return Prepared{Status: StatusSkipped, Kind: KindWork, Period: period}, nil
	}
	mat, err := r.workMaterial(in, period)
	if err != nil {
		return Prepared{Kind: KindWork, Period: period}, err
	}
	return r.beginOnce(mat, host, key)
}

func (r *Keeper) workMaterial(in WorkIn, period string) (Material, error) {
	mat := Material{Kind: KindWork, Period: period, Session: WorkHost + ":" + in.Ref}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return mat, nil // 읽을 기록이 없다
	}
	if len(text) > maxSessionBytes {
		text = clipBytes(text, maxSessionBytes)
		mat.Note = fmt.Sprintf("작업 기록이 길어 앞 %dKB만 실음", maxSessionBytes>>10)
	}
	end := time.UnixMilli(in.EndedAt)
	w := &Work{Ref: in.Ref, Cwd: in.Cwd, Title: memory.MaskSecrets(strings.TrimSpace(in.Title)), Status: in.Status, Verified: in.Verified,
		EndedAt: in.EndedAt, Text: memory.MaskSecrets(text)}
	for _, ev := range in.Evidence {
		if t := strings.TrimSpace(ev.Text); t != "" && len(w.Evidence) < maxWorkEvidence {
			w.Evidence = append(w.Evidence, WorkEvidence{Ref: ev.Ref, Text: memory.MaskSecrets(clipBytes(t, maxEvidenceLine))})
		}
	}
	// 기록은 가짜 세션 하나로도 싣는다: 인용·교정·사용 판정이 세션 회고와 같은 코드로 돈다.
	sess := transcript.Session{Host: WorkHost, ID: in.Ref, Cwd: in.Cwd}
	for _, u := range in.UserWords {
		t := strings.TrimSpace(u.Text)
		if t == "" {
			continue
		}
		at := u.At
		if at <= 0 {
			at = in.EndedAt
		}
		w.Words = append(w.Words, WorkWord{Ref: u.Ref, Text: memory.MaskSecrets(t), At: at, Correction: u.Correction})
		sess.Turns = append(sess.Turns, transcript.Turn{Role: "user", Text: t, At: time.UnixMilli(at)})
	}
	sess.Turns = append(sess.Turns, transcript.Turn{Role: "ai", Text: text, At: end})
	slices.SortStableFunc(sess.Turns, func(a, b transcript.Turn) int { return a.At.Compare(b.At) })
	mat.Sessions = []transcript.Session{sess}
	maskSessions(mat.Sessions)
	mat.Work = w
	for _, id := range in.Recalled {
		if m, err := r.Store.Get(id); err == nil && !slices.ContainsFunc(mat.Injected, func(b Brief) bool { return b.ID == id }) {
			b := brief(m, true)
			b.Body = clipBytes(b.Body, 600)
			mat.Injected = append(mat.Injected, b)
		}
	}
	active, err := r.Store.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		return mat, err
	}
	// 이 작업 폴더에서 보이는 저장소·제품 기억(제품 지도, 설계 · orbit-mem 2판 S3)과 전역 교훈.
	here := place.Resolve(in.Cwd, r.Cfg.Products)
	for _, m := range active { // 최근 것부터
		if len(mat.Existing) >= maxWorkExisting {
			break
		}
		local := m.Scope != memory.ScopeGlobal && here.Sees(m.Scope, m.Project)
		if m.Kind == memory.KindJournal || (!local && !(m.Scope == memory.ScopeGlobal && m.Kind == memory.KindLesson)) {
			continue
		}
		b := brief(m, true)
		b.Body = clipBytes(b.Body, 300)
		mat.Existing = append(mat.Existing, b)
	}
	return mat, r.rejectionsMaterial(&mat)
}

// beginOnce begins mat like begin, keyed so that only one retrospective of the
// key runs or is done: of several prepares racing, one runs and the others are
// skipped (the row is written in one statement).
func (r *Keeper) beginOnce(mat Material, host, key string) (Prepared, error) {
	res := Prepared{Kind: mat.Kind, Period: mat.Period, Session: mat.Session}
	nowMs := r.now().UnixMilli()
	res.ID = memory.NewID("r-")
	mat.ID = res.ID
	const guard = ` WHERE NOT EXISTS (SELECT 1 FROM retros WHERE kind=? AND session=? AND status IN ('running','submitting','done'))`
	skipped := Prepared{Status: StatusSkipped, Kind: mat.Kind, Period: mat.Period}
	if mat.empty() {
		out, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,finished_at,summary,session) SELECT ?,?,?,?,?,?,?,?,?`+guard,
			res.ID, mat.Kind, mat.Period, StatusEmpty, "", nowMs, nowMs, emptySummary(), key, mat.Kind, key)
		if err != nil {
			return res, err
		}
		if n, _ := out.RowsAffected(); n != 1 {
			return skipped, nil
		}
		res.Status = StatusEmpty
		return res, nil
	}
	dir, err := r.keepMaterial(mat)
	if err != nil {
		return res, err
	}
	out, err := r.Store.DB().Exec(`INSERT INTO retros(id,kind,period,status,host,started_at,session) SELECT ?,?,?,?,?,?,?`+guard,
		res.ID, mat.Kind, mat.Period, StatusRunning, host, nowMs, key, mat.Kind, key)
	if err != nil {
		os.RemoveAll(dir) // 줄 없이 재료만 남기지 않는다
		return res, err
	}
	if n, _ := out.RowsAffected(); n != 1 {
		os.RemoveAll(dir) // 다른 준비가 먼저 차지했다
		return skipped, nil
	}
	res.Status = StatusRunning
	return res, nil
}

// workEvidenceFound says whether evidence is, word spacing aside, in one of
// the work's verification evidence lines and long enough to say something.
func workEvidenceFound(w *Work, evidence string) bool {
	e := squash(evidence)
	if w == nil || len([]rune(e)) < 10 {
		return false
	}
	for _, line := range w.Evidence {
		if strings.Contains(squash(line.Text), e) {
			return true
		}
	}
	return false
}

// workTags normalises the procedure tag of a work retrospective's memory.
func workTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if strings.TrimSpace(t) == "절차" {
			t = memory.TagProcedure
		}
		out = append(out, t)
	}
	return out
}

func renderWork(b *strings.Builder, mat Material, loc *time.Location) {
	w := mat.Work
	if w == nil {
		return
	}
	verified := "아니오(검증 통과 기록 없음)"
	if w.Verified {
		verified = "예"
	}
	b.WriteString("## 작업 머리\n\n")
	fmt.Fprintf(b, "- 작업 id: %s\n- 제목: %s\n- 상태: %s\n- 폴더: %s\n- 끝난 때: %s\n- 검증 통과: %s\n\n",
		w.Ref, w.Title, w.Status, w.Cwd, time.UnixMilli(w.EndedAt).In(loc).Format("2006-01-02 15:04"), verified)
	b.WriteString("## 검증 증거 (줄마다 글자 그대로. 사실의 evidence는 여기서 그대로 옮긴다)\n\n")
	if len(w.Evidence) == 0 {
		b.WriteString("- 없음\n")
	}
	for _, e := range w.Evidence {
		line := strings.ReplaceAll(e.Text, "\n", "\n  ")
		if e.Ref != "" {
			fmt.Fprintf(b, "- %s: %s\n", e.Ref, line)
		} else {
			fmt.Fprintf(b, "- %s\n", line)
		}
	}
	b.WriteString("\n## 사용자 말 (글자 그대로. quote는 여기서 옮긴다)\n\n")
	if len(w.Words) == 0 {
		b.WriteString("- 없음\n")
	}
	for _, u := range w.Words {
		mark := ""
		if u.Correction {
			mark = "(교정) "
		}
		fmt.Fprintf(b, "- [%s] %s%s\n", time.UnixMilli(u.At).In(loc).Format("15:04"), mark, strings.ReplaceAll(u.Text, "\n", "\n  "))
	}
	b.WriteString("\n## 작업 기록\n\n" + w.Text + "\n\n")
}
