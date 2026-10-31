// Package migrate imports the old Knowledge records (a copy of the
// knowledge-auto-*.sqlite backup) into OpenOrbit memory once. The old database is
// opened read-only.
package migrate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// Options are the migration inputs.
type Options struct {
	From           string   // 옛 Knowledge SQLite 사본
	DryRun         bool     // 쓰지 않고 결과만 센다
	HostMemoryDirs []string // 호스트 기억 디렉터리. 바뀌지 않은 파일은 흡수한 것으로 표시한다
}

// Report counts what happened.
type Report struct {
	Records      int            `json:"records"`
	Imported     int            `json:"imported"`
	Skipped      int            `json:"skipped"` // 이미 옮김
	ByState      map[string]int `json:"byState"`
	ByKind       map[string]int `json:"byKind"`
	Sources      int            `json:"sources"`
	FilesSame    int            `json:"filesSame"`
	FilesChanged int            `json:"filesChanged"`
	FilesUnknown int            `json:"filesUnknown"`
	Proposals    int            `json:"proposals"`
	Absorbed     int            `json:"absorbed"`
}

type legacyRecord struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Status    string            `json:"status"`
	Lifecycle string            `json:"lifecycle"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Tags      []string          `json:"tags"`
	Sources   []json.RawMessage `json:"sources"`
	Links     struct {
		WorkIDs []string `json:"workIds"`
		PageIDs []string `json:"pageIds"`
	} `json:"links"`
	RetireReason string `json:"retireReason"`
	Created      struct {
		At int64 `json:"at"`
	} `json:"created"`
}

type legacyRef struct {
	Kind     string `json:"kind"`
	SourceID string `json:"sourceId"`
	ItemID   string `json:"itemId"`
	Revision string `json:"revision"`
}

type legacyObservation struct {
	Kind    string `json:"kind"`
	Excerpt string `json:"excerpt"`
	Locator struct {
		Path      string `json:"path"`
		Version   string `json:"version"`
		RunID     string `json:"runId"`
		SessionID string `json:"sessionId"`
		TurnID    string `json:"turnId"`
	} `json:"locator"`
	CapturedAt int64 `json:"capturedAt"`
}

// mapping per design 「기존 기억 이전」.
func mapRecord(r legacyRecord) (kind, state string, tags []string) {
	tags = append([]string{}, r.Tags...)
	switch r.Kind {
	case "FACT":
		kind = memory.KindFact
	case "DECISION":
		kind = memory.KindDecision
	case "LESSON":
		kind = memory.KindLesson
	case "CORRECTION":
		kind = memory.KindRule
	default: // ISSUE, NOTE, QUESTION: 후보로 옮기고 첫 회고가 재분류를 제안한다
		kind = memory.KindFact
		tags = append(tags, "옛:"+r.Kind)
	}
	switch {
	case r.Lifecycle == "RETIRED":
		state = memory.StateStale
	case r.Kind == "ISSUE" || r.Kind == "NOTE" || r.Kind == "QUESTION":
		state = memory.StateCandidate
	case r.Kind == "CORRECTION" || r.Status == "VERIFIED":
		state = memory.StateConfirmed
	default:
		state = memory.StateCandidate
	}
	if len(tags) > 10 { // 옛 태그 10개 + 옛:KIND가 넘치면 옛:KIND를 남긴다
		tags = append(tags[:9], tags[len(tags)-1])
	}
	return
}

var (
	dateVersion = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	hexVersion  = regexp.MustCompile(`^(sha256:)?[0-9a-f]{7,64}$`)
)

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// fileState says whether a FILE observation's file is still what was observed.
func fileState(path, version string) (state, hash string) {
	if !filepath.IsAbs(path) {
		return "unknown", ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return "changed", ""
	}
	cur, err := hashFile(path)
	if err != nil {
		return "unknown", ""
	}
	switch {
	case dateVersion.MatchString(version):
		day, err := time.ParseInLocation("2006-01-02", version[:10], time.Local)
		if err != nil {
			return "unknown", ""
		}
		if info.ModTime().Before(day.AddDate(0, 0, 1)) {
			return "same", cur
		}
		return "changed", ""
	case hexVersion.MatchString(version):
		v := strings.TrimPrefix(version, "sha256:")
		if strings.HasPrefix(cur, v) {
			return "same", cur
		}
		if strings.HasPrefix(version, "sha256:") {
			return "changed", ""
		}
		return "unknown", "" // git 커밋 해시일 수 있어 판단하지 않는다
	}
	return "unknown", ""
}

// Run imports every record not imported yet.
func Run(s *memory.Store, o Options) (Report, error) {
	rep := Report{ByState: map[string]int{}, ByKind: map[string]int{}}
	if _, err := os.Stat(o.From); err != nil {
		return rep, i18n.Errorf("migrate.err.no_copy", err)
	}
	old, err := sql.Open("sqlite", "file:"+filepath.ToSlash(o.From)+"?mode=ro")
	if err != nil {
		return rep, err
	}
	defer old.Close()
	observations := map[string]legacyObservation{}
	rows, err := old.Query(`SELECT id, body FROM observations`)
	if err != nil {
		return rep, i18n.Errorf("migrate.err.read_observations", err)
	}
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			rows.Close()
			return rep, err
		}
		var ob legacyObservation
		if json.Unmarshal([]byte(body), &ob) == nil {
			observations[id] = ob
		}
	}
	rows.Close()
	rows, err = old.Query(`SELECT body FROM records ORDER BY id`)
	if err != nil {
		return rep, i18n.Errorf("migrate.err.read_records", err)
	}
	var records []legacyRecord
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return rep, err
		}
		var r legacyRecord
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			rows.Close()
			return rep, i18n.Errorf("migrate.err.parse_records", err)
		}
		records = append(records, r)
	}
	rows.Close()
	rep.Records = len(records)
	hostDirs := map[string]bool{}
	for _, d := range o.HostMemoryDirs {
		hostDirs[filepath.Clean(d)] = true
	}
	for _, r := range records {
		var exists int
		_ = s.DB().QueryRow(`SELECT COUNT(*) FROM memories WHERE legacy_id=?`, r.ID).Scan(&exists)
		if exists > 0 {
			rep.Skipped++
			continue
		}
		kind, state, tags := mapRecord(r)
		var sources []memory.Source
		var absorbed [][2]string
		changed := []string{}
		for _, raw := range r.Sources {
			var ref legacyRef
			if json.Unmarshal(raw, &ref) != nil {
				continue
			}
			ob, ok := observations[ref.SourceID]
			switch {
			case ref.Kind == "PROVIDER_DOCUMENT" || (ref.ItemID != "" && !ok):
				sources = append(sources, memory.Source{Kind: memory.SourceLegacy, Ref: "wiki:" + ref.ItemID + "@" + ref.Revision, Excerpt: "옛 Wiki 문서"})
			case !ok:
				sources = append(sources, memory.Source{Kind: memory.SourceLegacy, Ref: "observation:" + ref.SourceID, Excerpt: "옛 관측(본문 없음)"})
			case ob.Kind == "USER":
				sources = append(sources, memory.Source{Kind: memory.SourceQuote, Ref: "session:" + ob.Locator.SessionID + "/" + ob.Locator.TurnID, Excerpt: ob.Excerpt, At: ob.CapturedAt})
			case ob.Kind == "COMMAND":
				sources = append(sources, memory.Source{Kind: memory.SourceCommand, Ref: ob.Locator.RunID, Excerpt: ob.Excerpt, At: ob.CapturedAt})
			case ob.Kind == "FILE":
				st, hash := fileState(ob.Locator.Path, ob.Locator.Version)
				switch st {
				case "same":
					rep.FilesSame++
					if hostDirs[filepath.Dir(ob.Locator.Path)] {
						rep.Absorbed++
						absorbed = append(absorbed, [2]string{ob.Locator.Path, hash})
					}
				case "changed":
					rep.FilesChanged++
					changed = append(changed, ob.Locator.Path)
				default:
					rep.FilesUnknown++
				}
				sources = append(sources, memory.Source{Kind: memory.SourceFile, Ref: ob.Locator.Path, Excerpt: ob.Excerpt, Hash: hash, At: ob.CapturedAt})
			}
		}
		for _, w := range r.Links.WorkIDs {
			sources = append(sources, memory.Source{Kind: memory.SourceLegacy, Ref: "work:" + w, Excerpt: "옛 Work 작업 " + w + "에서 나온 기억"})
		}
		for _, p := range r.Links.PageIDs {
			sources = append(sources, memory.Source{Kind: memory.SourceLegacy, Ref: "wiki:" + p, Excerpt: "옛 Wiki 문서 " + p + "와 연결된 기억"})
		}
		rep.Imported++
		rep.ByState[state]++
		rep.ByKind[kind]++
		rep.Sources += len(sources) + 1
		if len(changed) > 0 && state != memory.StateStale {
			rep.Proposals++
		}
		if o.DryRun {
			continue
		}
		createdDay := memory.Day(time.UnixMilli(r.Created.At))
		first := memory.Source{Kind: memory.SourceLegacy, Ref: "knowledge:" + r.ID, Excerpt: "옛 Knowledge " + r.Kind + "/" + r.Status, At: r.Created.At}
		// 기억과 출처는 한 트랜잭션으로 남긴다(중간에 끊겨도 반쪽 기록이 없다).
		m, err := s.Save(memory.SaveInput{Kind: kind, Title: r.Title, Body: r.Body, Tags: tags, Scope: memory.ScopeGlobal,
			State: state, LegacyID: r.ID, Source: first, Extra: sources, Day: createdDay, At: r.Created.At})
		if err != nil {
			return rep, i18n.Errorf("migrate.err.move_failed", r.ID, r.Title, err)
		}
		for _, ab := range absorbed {
			_, _ = s.DB().Exec(`INSERT INTO absorbed(path,hash,at) VALUES(?,?,?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash`, ab[0], ab[1], time.Now().UnixMilli())
		}
		if state == memory.StateStale && r.RetireReason != "" {
			reason := "옛 Knowledge에서 은퇴: " + r.RetireReason
			_, _ = s.Update(memory.UpdateInput{ID: m.ID, State: &state, Reason: reason, Action: "migrate"})
		}
		if len(changed) > 0 && state != memory.StateStale {
			_, _ = s.Propose(memory.ProposalInput{Kind: memory.ProposalStale, Title: i18n.TIn(i18n.KO, "migrate.source_changed_title", m.Title),
				Body:      i18n.TIn(i18n.KO, "migrate.source_changed_body", strings.Join(changed, ", ")),
				MemoryIDs: []string{m.ID}})
		}
	}
	if !o.DryRun {
		_ = s.SetMeta("migrated_from", o.From)
		_ = s.SetMeta("migrated_at", time.Now().Format(time.RFC3339))
	}
	return rep, nil
}
