package memory

// memory_find 질의의 조사·어미 떼기(명세 Q-2): 요청 회상이 쓰는 같은 규칙(stemWord)으로 어절마다 어간을 구해 원래 낱말 뒤에
// 더한다. 원래 낱말은 남는다.

import (
	"strings"
	"testing"
)

func TestStripParticlesQuery_원래_낱말_뒤에_뗀_어간을_더한다(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"배포를", "배포를 배포"},
		{"배포를 확인해", "배포를 배포 확인해 확인"},
		{"pnpm으로 배포", "pnpm으로 pnpm 배포"},
		{"꼴이 이상하다", "꼴이 꼴 이상하다 이상"},
		{"deploy checklist", "deploy checklist"},
		{"배포", "배포"},
		{"  ", "  "},
		{"", ""},
		{"???", "???"},
		{"node.js를 설치", "node.js를 node.js 설치"},
		{"변경(pr", "변경 pr"},
		{"제대로 배포", "제대로 배포"},
	} {
		if got := StripParticlesQuery(c.in); got != c.want {
			t.Errorf("StripParticlesQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 조사가 아닌 끝 글자: 도구의 「구」, 이사의 「사」, 사이의 「이」 같은 낱말은 어간이 더해지지 않아 질의가 그대로다. 목록은
// 조사 목록과 무관하게 흔한 명사에서 골랐다.
func TestStripParticlesQuery_조사가_아닌_끝_글자는_질의를_바꾸지_않는다(t *testing.T) {
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
	t.Logf("바뀐 명사 %d/%d: %v", len(changed), len(nouns), changed)
	if len(changed) != 0 {
		t.Errorf("조사가 아닌 끝 글자를 조사로 보았다: %v", changed)
	}
}

// 원래 낱말은 늘 질의에 남고 더해지는 어간은 그 앞부분이다: 「지」「다」「서」로 끝나는 말(메시지·레이다·보고서)을 조사로 잘못
// 봐도 원래 낱말의 조각(이지, 미지)을 잃지 않고, 토큰이 늘어 후보가 늘지도 않는다.
func TestStripParticlesQuery_원래_낱말이_남고_어간은_그_앞부분이다(t *testing.T) {
	for _, w := range strings.Fields("메시지 이미지 스토리지 페이지 패키지 레이다 보고서 계약서 배포를 확인해서 pnpm으로 node.js를") {
		got := strings.Fields(StripParticlesQuery(w))
		if len(got) == 0 || got[0] != w {
			t.Errorf("%q → %q: the word is not kept first", w, got)
			continue
		}
		if len(got) == 2 && !strings.HasPrefix(w, got[1]) {
			t.Errorf("%q → %q: the stem is not a prefix", w, got)
		}
	}
}
