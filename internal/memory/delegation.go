package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 위임 글(설계 · orbit-mem 4.1): 세션이 서브에이전트에게 맡긴 일의 글을, 그 서브에이전트가 시작할 때 묶음을 만들
// 질의로 쓰려고 잠깐 둔다. 스키마 판 5를 그대로 두려고 세션 묶음 표(session_bundles)에 함께 두고, 세션 칸을 아래
// 꼴로 꾸민다. 세션 묶음 읽기(SessionBundle·SessionBundleAt)는 세션 칸이 정확히 같은 줄만 읽으므로 섞이지 않고,
// 다른 읽기(지표·회고·감사·활동 기록)는 이 표를 읽지 않는다. 오래된 세션 상태 정리(PruneSessionState)가 묶음과 같이
// 지운다.
//
//	기다림: \x1fdelegation\x1f<세션>\x1f<종류>\x1f<순번>   글 = 위임 글
//	가져감: \x1fdelegated\x1f<세션>\x1f<가져간 이>
//
// 앞의 구분 문자(0x1f)는 호스트의 세션 id에 나오지 않는 글자다. 구분 문자가 든 세션·종류는 남기지 않는다.
const (
	delegationSep     = "\x1f"
	delegationWaiting = delegationSep + "delegation" + delegationSep
	delegationTaken   = delegationSep + "delegated" + delegationSep
)

// DelegationTTL is how long a delegation waits for its subagent. A delegation whose subagent never started
// (the permission was denied, a hook blocked the call, the tool failed) leaves a waiting row; without a limit
// every later subagent of its kind in the session would get the task one before its own. Past the limit
// TakeDelegation skips and removes the row. 근거(계획 1-6 b10, 재생 묶음 19세션·위임 95건의 시각): 위임 도구
// 호출에서 서브에이전트 시작까지의 간격은 재생 자료에 없다(서브에이전트 기록은 담지 않는다). 보통은 훅 하나 사이(1초
// 미만)이고, 길어지는 것은 사람이 권한 확인을 기다릴 때다. 같은 세션·같은 종류의 다음 위임까지 간격(53건)은 최소 20초,
// 5% 29초, 10% 386초, 중앙 1,502초이고 300초 미만은 4건(30초 미만 3건은 바로 다시 맡긴 것)이다. 5분이면 권한 확인을
// 몇 분 기다린 위임도 짝을 잃지 않고, 남은 대기 행이 다음 같은 종류 위임을 밀어내는 경우는 간격 10% 지점(386초)보다 짧은
// 것뿐이다. 한도를 넘겨 시작한 서브에이전트는 위임 회상 없이 상시 핵심 제목 줄만 받는다(틀린 글보다 낫다).
const DelegationTTL = 5 * time.Minute

func delegationKeyOK(parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(p, delegationSep) {
			return false
		}
	}
	return true
}

func waitingPrefix(session, kind string) string {
	return delegationWaiting + session + delegationSep + kind + delegationSep
}

func takenKey(session, taker string) string { return delegationTaken + session + delegationSep + taker }

// serial is a key part no other delegation of the same session and kind has: the time, then random
// letters (the order is the row's, not this part's).
func serial(ms int64) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return strconv.FormatInt(ms, 10) + "-" + hex.EncodeToString(b)
}

// SaveDelegation keeps what a session handed a subagent of kind, until a subagent of that kind takes it.
// The caller cuts the text to the length it keeps. A delegation with no session is not kept.
func (s *Store) SaveDelegation(host, session, kind, text string) error {
	if session == "" || !delegationKeyOK(session, kind) {
		return nil
	}
	now := s.nowMs()
	_, err := s.db.Exec(`INSERT INTO session_bundles(host,session,text,memory_ids,at) VALUES(?,?,?,'[]',?)`,
		host, waitingPrefix(session, kind)+serial(now), text, now)
	return err
}

// TakeDelegation gives a subagent (taker) of kind the delegation it was started for: the one it took
// before, else the oldest one of the session and kind not taken yet and not older than DelegationTTL, which
// it takes (no other subagent gets it). Older ones of the session and kind are removed. Without one it gives
// false. An empty taker takes the oldest one every time.
func (s *Store) TakeDelegation(host, session, kind, taker string) (string, bool, error) {
	if session == "" || !delegationKeyOK(session, kind, taker) {
		return "", false, nil
	}
	if taker != "" {
		text, ok, err := s.takenDelegation(host, session, taker)
		if ok || err != nil {
			return text, ok, err
		}
	}
	prefix := waitingPrefix(session, kind)
	n := utf8.RuneCountInString(prefix)
	oldest := s.nowMs() - DelegationTTL.Milliseconds() // 이보다 앞에 적힌 대기 행은 시작되지 않은 위임이다
	if _, err := s.db.Exec(`DELETE FROM session_bundles WHERE host=? AND substr(session,1,?)=? AND at<?`, host, n, prefix, oldest); err != nil {
		return "", false, err
	}
	for range 16 { // 다른 프로세스가 같은 줄을 먼저 가져가면 다음 줄로
		var key string
		err := s.db.QueryRow(`SELECT session FROM session_bundles WHERE host=? AND substr(session,1,?)=? AND at>=? ORDER BY at, rowid LIMIT 1`,
			host, n, prefix, oldest).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		who := taker
		if who == "" {
			who = strings.TrimPrefix(key, prefix)
		}
		var text string
		err = s.db.QueryRow(`UPDATE session_bundles SET session=? WHERE host=? AND session=? RETURNING text`,
			takenKey(session, who), host, key).Scan(&text)
		switch {
		case err == nil:
			return text, true, nil
		case errors.Is(err, sql.ErrNoRows): // 먼저 가져갔다
			continue
		case taker != "" && strings.Contains(err.Error(), "UNIQUE"): // 같은 서브에이전트가 동시에 두 번 물었다
			return s.takenDelegation(host, session, taker)
		default:
			return "", false, err
		}
	}
	return "", false, nil
}

func (s *Store) takenDelegation(host, session, taker string) (string, bool, error) {
	var text string
	err := s.db.QueryRow(`SELECT text FROM session_bundles WHERE host=? AND session=?`, host, takenKey(session, taker)).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return text, err == nil, err
}

// DropDelegations forgets the delegations of a session, taken or not.
func (s *Store) DropDelegations(host, session string) error {
	if session == "" || !delegationKeyOK(session) {
		return nil
	}
	waiting := delegationWaiting + session + delegationSep
	taken := delegationTaken + session + delegationSep
	_, err := s.db.Exec(`DELETE FROM session_bundles WHERE host=? AND (substr(session,1,?)=? OR substr(session,1,?)=?)`,
		host, utf8.RuneCountInString(waiting), waiting, utf8.RuneCountInString(taken), taken)
	return err
}
