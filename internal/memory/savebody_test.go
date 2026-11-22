package memory

import (
	"slices"
	"testing"
)

// 같은 제목 경로는 본문 말고도 태그·주제·도구·보존 등급·재검토 주기를 받아 버린다. 달라서 반영되지 않은 칸을 알린다.
func TestSave_같은_제목_다른_칸은_FieldsKept로_알린다(t *testing.T) {
	s, _ := newTestStore(t)
	base := SaveInput{Kind: KindFact, Title: "배포 창은 화요일", Body: "화요일 오전", Scope: ScopeGlobal, Tags: []string{"배포"},
		Topic: "배포 창", Tools: []string{"Bash"}, Retention: RetentionDurable, ReviewAfterDays: 30, Source: Source{Kind: SourceSession, Ref: "s-1"}}
	if _, _, err := s.SaveWith(base); err != nil {
		t.Fatal(err)
	}

	// 같은 값(태그 순서·주제 대소문자·공백 차이 포함)이거나 주지 않은 칸은 알리지 않는다.
	same := base
	same.Tags, same.Topic, same.Source.Ref = []string{" 배포 "}, " 배포  창", "s-2"
	_, rep, err := s.SaveWith(same)
	if err != nil || len(rep.FieldsKept) != 0 || rep.BodyKept {
		t.Fatalf("같은 값인데 알렸습니다: %v %+v", err, rep)
	}
	none := SaveInput{Kind: KindFact, Title: "배포 창은 화요일", Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s-3"}}
	if _, rep, err = s.SaveWith(none); err != nil || len(rep.FieldsKept) != 0 {
		t.Fatalf("주지 않은 칸을 알렸습니다: %v %+v", err, rep)
	}

	diff := base
	diff.Tags, diff.Topic, diff.Tools, diff.Retention, diff.ReviewAfterDays, diff.Source.Ref = []string{"다른"}, "다른 주제", []string{"Edit"}, RetentionVolatile, 60, "s-4"
	_, rep, err = s.SaveWith(diff)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tags", "topic", "tools", "retention", "reviewAfterDays"}
	if !slices.Equal(rep.FieldsKept, want) {
		t.Errorf("FieldsKept = %v, want %v", rep.FieldsKept, want)
	}
}

// 같은 제목으로 다시 저장할 때 새 본문은 반영되지 않는다. 그 사실을 SaveReport가 알려야 한다(S-1).
func TestSave_같은_제목_다른_본문이면_BodyKept를_알린다(t *testing.T) {
	s, _ := newTestStore(t)
	first, _, err := s.SaveWith(SaveInput{Kind: KindFact, Title: "배포 창은 화요일", Body: "화요일 오전", Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "s-1"}})
	if err != nil {
		t.Fatal(err)
	}

	// 본문이 다르면 알린다. 출처는 지금처럼 더해진다.
	m, rep, err := s.SaveWith(SaveInput{Kind: KindFact, Title: "배포 창은 화요일", Body: "화요일 오후로 바뀜", Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "s-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != first.ID {
		t.Fatalf("같은 제목은 같은 기억이어야 합니다: %s != %s", m.ID, first.ID)
	}
	if !rep.BodyKept {
		t.Errorf("본문이 다른데 BodyKept가 false입니다")
	}
	if m.Body != "화요일 오전" {
		t.Errorf("본문이 바뀌었습니다: %q", m.Body)
	}
	if len(m.Sources) != 2 {
		t.Errorf("출처가 더해지지 않았습니다: %d개", len(m.Sources))
	}

	// 본문이 같으면 알리지 않는다(앞뒤 공백 차이는 같은 본문이다).
	_, rep, err = s.SaveWith(SaveInput{Kind: KindFact, Title: "배포 창은 화요일", Body: " 화요일 오전\n", Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "s-3"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.BodyKept {
		t.Errorf("본문이 같은데 BodyKept가 true입니다")
	}

	// 새 저장에 본문이 없으면 비교할 것이 없어 알리지 않는다.
	_, rep, err = s.SaveWith(SaveInput{Kind: KindFact, Title: "배포 창은 화요일", Scope: ScopeGlobal,
		Source: Source{Kind: SourceSession, Ref: "s-4"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.BodyKept {
		t.Errorf("새 본문이 없는데 BodyKept가 true입니다")
	}
}
