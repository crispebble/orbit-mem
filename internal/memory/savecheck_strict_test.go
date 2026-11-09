package memory

import (
	"slices"
	"testing"
)

// 막는 판정은 엄격하게, 막지 않을 때 가까운 기억을 붙인다(1-6 b6db, 사용자 결정 A). 같은 틀에 주제 낱말이 같아도 서술
// 내용(값·시점·도구·대상)이 다른 새 사실은 저장을 막지 않는다. 낱말이 많이 겹치니 가까운 기억으로는 보인다. 개발 자료
// holdout4_dup에서 잘못 막은 일곱 건(주제 낱말만 같은 새 사실)과 같은 꼴을 다른 낱말로 새로 썼다.
var newFactsSameTopicWords = []struct {
	name                string
	base, probe         [2]string // title, topic
	baseBody, probeBody string
}{
	{"값만 다른 같은 틀", [2]string{"이 서비스 관리 화면 포트는 9090이다", ""}, [2]string{"이 서비스 공개 화면 포트는 8080이다", ""}, "", ""},
	{"시점·도구가 다른 점검", [2]string{"web은 배포 전에 lint만 돌린다", "web 배포 전 점검"},
		[2]string{"web은 머지 전에 단위 시험을 돌린다", "web 머지 전 점검"}, "", ""},
	{"같은 틀 다른 칸", [2]string{"장애 보고에는 복구 시각을 적는다", ""}, [2]string{"장애 보고에는 영향 받은 사용자 수를 적는다", ""}, "", ""},
	{"같은 서술어 다른 대상", [2]string{"주간 보고는 두 쪽을 넘기지 않는다", "주간 보고 길이"}, [2]string{"회고는 한 시간을 넘기지 않는다", "회고 길이"}, "", ""},
}

func TestSaveCheck_주제_낱말만_같은_새_사실은_막지_않고_가까운_기억으로_보인다(t *testing.T) {
	for _, c := range newFactsSameTopicWords {
		s, _ := newTestStore(t)
		b := saveDoc(t, s, c.base[0], c.baseBody, c.base[1])
		in := checkIn(c.probe[0], c.probeBody, c.probe[1])
		sc := simScore(t, s, in, b.ID)
		t.Logf("%s: %.3f", c.name, sc)
		r := mustCheckMins(t, s, in, CheckMins{})
		if r.Verdict != CheckNew || len(r.Similar) != 0 {
			t.Errorf("%s: score %.3f blocked the save (min %.2f): %+v", c.name, sc, DefaultSimilarMin, r)
			continue
		}
		if !slices.Contains(nearIDs(r), b.ID) {
			t.Errorf("%s: score %.3f, not shown as close (floor %.2f): %+v", c.name, sc, DefaultNearMin, r.Near)
		}
	}
}
