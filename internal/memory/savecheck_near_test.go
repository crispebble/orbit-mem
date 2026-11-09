package memory

import (
	"reflect"
	"slices"
	"testing"
)

// 가까운 기억(1-6 b6dn). 막지 않고 저장할 때(새 기억 판정)만 같은 범위의 기억 가운데 비슷함이 가까운 하한 이상인 것을
// 점수 순으로 SimilarLimit개까지 값으로 돌려준다. 막을 때(similar)·같은 제목·같은 주제일 때는 비운다.

func nearIDs(r SaveCheckResult) []string {
	var ids []string
	for _, h := range r.Near {
		ids = append(ids, h.ID)
	}
	return ids
}

func TestSaveCheck_가까운_기억은_하한_바로_위면_붙고_바로_아래면_안_붙는다(t *testing.T) {
	s, _ := newTestStore(t)
	m := saveDoc(t, s, "배포 스크립트는 드라이런을 먼저 돌린다", "운영 배포 전에 바뀔 것을 드라이런으로 본다", "")
	probe := checkIn("운영 배포 순서는 공지 뒤에 한다", "배포 공지를 먼저 올리고 나서 운영 배포를 시작한다", "")
	sc := simScore(t, s, probe, m.ID)
	if sc <= 0 || sc >= DefaultSimilarMin {
		t.Fatalf("probe score %v: want between 0 and the block threshold", sc)
	}
	above := mustCheckMins(t, s, probe, CheckMins{Near: sc - 1e-6})
	if above.Verdict != CheckNew || len(above.Similar) != 0 || !slices.Equal(nearIDs(above), []string{m.ID}) {
		t.Fatalf("just above the near floor: %+v", above)
	}
	if h := above.Near[0]; h.Title != m.Title || h.Kind != m.Kind || h.Score != sc {
		t.Fatalf("near item: %+v (score %v)", h, sc)
	}
	if below := mustCheckMins(t, s, probe, CheckMins{Near: sc + 1e-6}); below.Verdict != CheckNew || len(below.Near) != 0 {
		t.Fatalf("just below the near floor: %+v", below)
	}
}

func TestSaveCheck_가까운_기억은_점수_순_최대_3개이고_같은_범위의_후보_확정만_본다(t *testing.T) {
	s, _ := newTestStore(t)
	var all []Memory
	for _, ti := range []string{
		"배포 공지는 하루 전에 올린다",
		"배포 공지는 팀 채널에 올린다",
		"배포 일정은 금요일을 피한다",
		"배포 전에 백업을 확인한다",
		"배포 기록은 위키에 남긴다",
	} {
		all = append(all, saveDoc(t, s, ti, "", ""))
	}
	other := saveDocIn(t, s, "project:onidot", "배포 공지는 하루 전에 팀 채널에 올린다", "")
	stale := saveDoc(t, s, "배포 공지 하루 전 팀 채널", "", "")
	if _, err := s.SetState(stale.ID, StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	probe := checkIn("배포 공지는 하루 전에 팀 채널로 알린다", "", "")
	r := mustCheckMins(t, s, probe, CheckMins{Similar: 0.99, Near: 0.01})
	if r.Verdict != CheckNew || len(r.Similar) != 0 || len(r.Near) != SimilarLimit {
		t.Fatalf("near: %+v", r)
	}
	// 점수 순이고, 위 셋은 모든 같은 범위 기억 가운데 점수가 가장 높은 셋이다.
	var scores []float64
	for _, m := range all {
		scores = append(scores, simScore(t, s, probe, m.ID))
	}
	slices.Sort(scores)
	slices.Reverse(scores)
	for i, h := range r.Near {
		if h.Score != scores[i] {
			t.Fatalf("near %d score %v, want %v (all %v)", i, h.Score, scores[i], scores)
		}
		if h.ID == other.ID || h.ID == stale.ID {
			t.Fatalf("other scope or stale memory in near: %+v", r.Near)
		}
	}
}

func TestSaveCheck_막을_때_같은_제목_같은_주제_가까운_것이_없을_때는_가까운_기억이_없다(t *testing.T) {
	s, _ := newTestStore(t)
	body := "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"
	saveDoc(t, s, "배포 스크립트는 드라이런이 먼저", body, "")
	saveDoc(t, s, "배포 공지는 하루 전에 올린다", "", "배포 공지")
	// 막을 때: 다른 기억이 하한 위에 있어도 붙이지 않는다.
	blocked := mustCheckMins(t, s, checkIn("배포 스크립트는 드라이런을 먼저 돌린다", body, ""), CheckMins{Near: 0.01})
	if blocked.Verdict != CheckSimilar || len(blocked.Near) != 0 {
		t.Fatalf("blocked: %+v", blocked)
	}
	same := mustCheckMins(t, s, checkIn("배포 스크립트는 드라이런이 먼저", "", ""), CheckMins{Near: 0.01})
	if same.Verdict != CheckSameTitle || len(same.Near) != 0 {
		t.Fatalf("same title: %+v", same)
	}
	topic := mustCheckMins(t, s, checkIn("배포 알림 시각", "", "배포 공지"), CheckMins{Near: 0.01})
	if topic.Verdict != CheckSameTopic || len(topic.Near) != 0 {
		t.Fatalf("same topic: %+v", topic)
	}
	none := mustCheckMins(t, s, checkIn("커밋 메시지는 한글로 쓴다", "", ""), CheckMins{})
	if none.Verdict != CheckNew || none.Near != nil {
		t.Fatalf("nothing close: %+v", none)
	}
}

func TestSaveCheck_가까운_기억은_같은_입력이면_같다_기본_하한은_0_이하일_때다(t *testing.T) {
	s, _ := newTestStore(t)
	for _, ti := range []string{"배포 공지는 하루 전에 올린다", "배포 공지는 팀 채널에 올린다", "배포 전에 백업을 확인한다", "배포 기록은 위키에 남긴다"} {
		saveDoc(t, s, ti, "", "")
	}
	probe := checkIn("배포 공지는 하루 전에 팀 채널로 알린다", "", "")
	a := mustCheckMins(t, s, probe, CheckMins{Similar: 0.99})
	b := mustCheckMins(t, s, probe, CheckMins{Similar: 0.99, Near: -1})
	c := mustCheckMins(t, s, probe, CheckMins{Similar: 0.99, Near: DefaultNearMin})
	if len(a.Near) == 0 || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(a, c) {
		t.Fatalf("same input, other output:\n%+v\n%+v\n%+v", a, b, c)
	}
	for _, h := range a.Near {
		if h.Score < DefaultNearMin {
			t.Fatalf("below the default near floor: %+v", a.Near)
		}
	}
}

func mustCheckMins(t *testing.T, s *Store, in SaveInput, m CheckMins) SaveCheckResult {
	t.Helper()
	r, err := s.SaveCheck(in, m)
	if err != nil {
		t.Fatalf("save check %q: %v", in.Title, err)
	}
	return r
}
