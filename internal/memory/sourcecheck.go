package memory

import (
	"regexp"
	"strings"

	"github.com/onidot-labs/openorbit/internal/i18n"
)

// Outcomes of checking a memory whose source file changed (설계 · Orbit 3차
// O3-1 「출처 파일 판정」). Each is kept as a source.check event for orb stats.
const (
	SourceKept   = "kept"   // 발췌가 바뀐 파일에 그대로 있음: 해시만 갱신
	SourceAbsent = "absent" // 파일이 없어짐: 근거가 지워졌을 뿐이라 제안하지 않음(2026-10-30 사용자 결정)
	SourceGone   = "gone"   // 발췌가 파일에서 사라짐: 낡음 제안
	SourceOK     = "ok"     // 발췌 없음, 회고 모델 판정: 아직 맞음
	SourceEdit   = "edit"   // 회고 모델 판정: 고쳐야 함(edit 제안)
	SourceStale  = "stale"  // 회고 모델 판정: 낡음(낡음 제안)
)

var spaces = regexp.MustCompile(`\s+`)

func squash(s string) string { return strings.TrimSpace(spaces.ReplaceAllString(s, " ")) }

// ExcerptIn says whether a source excerpt still stands in a file's content.
// Excerpts were stored with secrets masked, so the content is masked the same
// way; runs of white space count as one space.
func ExcerptIn(excerpt, content string) bool {
	e := squash(excerpt)
	return e != "" && strings.Contains(squash(MaskSecrets(content)), e)
}

// SetSourceHash records a source file's new hash after a check found the
// memory still stands on it. The memory keeps its version.
func (s *Store) SetSourceHash(sourceID, hash string) error {
	r, err := s.db.Exec(`UPDATE sources SET hash=? WHERE id=?`, hash, sourceID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return i18n.Errorf("memory.err.no_source", sourceID)
	}
	return nil
}

// legacySourceTitle starts the proposals the whole-file hash check made up to
// 0.3.8: one stale proposal per memory on any change of the file.
const legacySourceTitle = "출처 파일이 바뀜: "

// SettleLegacySourceProposals closes those pending proposals so the excerpt
// check judges their memories again. That check never moved their source
// hashes on, so it catches every one of them.
func (s *Store) SettleLegacySourceProposals() (int, error) {
	pending, err := s.Proposals(StatusPending)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range pending {
		if p.Kind != ProposalStale || p.RetroID != "" || !strings.HasPrefix(p.Title, legacySourceTitle) {
			continue
		}
		if err := s.finish(&p, StatusSettled, "새 출처 판정(발췌 비교)으로 다시 봄"); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
