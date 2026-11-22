package memory

// memory_find 질의의 조사·어미 떼기(명세 Q-2): 요청 회상이 쓰는 같은 규칙(stemWord)을 찾기 질의의 어절마다 건다.

import (
	"strings"
	"testing"
)

func TestStripParticlesQuery_조사와_어미를_떼고_다른_글은_그대로_둔다(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"배포를", "배포"},
		{"배포를 확인해", "배포 확인"},
		{"pnpm으로 배포", "pnpm 배포"},
		{"꼴이 이상하다", "꼴 이상"},
		{"deploy checklist", "deploy checklist"},
		{"배포", "배포"},
		{"  ", "  "},
		{"", ""},
		{"???", "???"},
		{"node.js를 설치", "node.js 설치"},
		{"변경(pr", "변경 pr"},
		{"제대로 배포", "제대로 배포"},
	} {
		if got := StripParticlesQuery(c.in); got != c.want {
			t.Errorf("StripParticlesQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 조사가 아닌 끝 글자: 도구의 「구」, 이사의 「사」, 사이의 「이」 같은 낱말은 떼면 다른 낱말이 되거나 뜻이 바뀐다. 목록의
// 낱말은 모두 그대로여야 하고, 떼이는 것이 있으면 이름을 남긴다. 목록은 조사 목록과 무관하게 흔한 명사에서 골랐다.
func TestStripParticlesQuery_조사가_아닌_끝_글자는_떼지_않는다(t *testing.T) {
	nouns := strings.Fields(`도구 이사 사이 평가 추가 속도 경로 회의 정의 합의 모의 제도 지도 아이 마이크 구조 기구 수사 의사 감사 문서 저장
		주소 기록 규칙 결정 교훈 사실 일지 설정 환경 서버 프로그램 데이터 시스템 사용자 방식 시험 검증 배포 점검 확인 위임 요청 응답 호출
		경고 공유 연결 정리 기본 최근 상태 순위 별칭 색인 질의 조사 어미 이유 근거 출처 제목 본문 태그 주제 도메인 인터페이스 컨테이너
		폴더 저장소 브랜치 커밋 머지 리뷰 테스트 빌드 로그 토큰 비밀 권한 인증 암호 백업 복구 이행 스키마 버전 릴리스`)
	var changed []string
	for _, n := range nouns {
		if got := StripParticlesQuery(n); got != n {
			changed = append(changed, n+"→"+got)
		}
	}
	t.Logf("떼인 명사 %d/%d: %v", len(changed), len(nouns), changed)
	if len(changed) != 0 {
		t.Errorf("조사가 아닌 끝 글자를 뗐다: %v", changed)
	}
}

// 떼기는 끝 글자만 지우므로 질의의 토큰은 늘지 않는다: 떼인 낱말은 늘 원래 낱말의 앞부분이다. 「지」「다」「서」로 끝나는 말
// (메시지·레이다·보고서)은 조사로 잘못 보고 떼지만 앞부분만 남아, 처음 두 글자 토큰은 원래 질의에도 있다. 이 한계는 요청
// 회상의 규칙과 같다.
func TestStripParticlesQuery_떼인_낱말은_늘_원래_낱말의_앞부분이다(t *testing.T) {
	for _, w := range strings.Fields("메시지 이미지 스토리지 페이지 패키지 레이다 보고서 계약서 배포를 확인해서 pnpm으로 node.js를") {
		if got := StripParticlesQuery(w); got == "" || !strings.HasPrefix(w, got) {
			t.Errorf("%q → %q is not a prefix", w, got)
		}
	}
}
