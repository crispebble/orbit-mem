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

	// 같은 값(태그 공백·주제 공백 차이 포함)이거나 주지 않은 칸은 알리지 않는다.
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

// 같은 제목에 준 값만 비교한다: 기본값이 채워진 값이나 주지 않은 칸은 알리지 않고, ttlDays와 normal 명시는 알린다.
func TestSave_같은_제목_보존_칸은_준_값으로만_비교한다(t *testing.T) {
	s, _ := newTestStore(t)
	save := func(title string, mod func(*SaveInput)) SaveReport {
		in := SaveInput{Kind: KindFact, Title: title, Scope: ScopeGlobal, Source: Source{Kind: SourceSession, Ref: "s"}}
		mod(&in)
		_, rep, err := s.SaveWith(in)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	save("휘발 값", func(in *SaveInput) { in.Retention, in.TTLDays = RetentionVolatile, 1 })
	if rep := save("휘발 값", func(in *SaveInput) { in.Retention, in.TTLDays = RetentionVolatile, 6 }); !slices.Equal(rep.FieldsKept, []string{"ttlDays"}) {
		t.Errorf("ttl만 다른데 FieldsKept = %v", rep.FieldsKept)
	}
	if rep := save("휘발 값", func(in *SaveInput) { in.Retention, in.TTLDays = RetentionVolatile, 1 }); len(rep.FieldsKept) != 0 {
		t.Errorf("같은 ttl인데 알렸습니다: %v", rep.FieldsKept)
	}

	save("영구 값", func(in *SaveInput) { in.Retention, in.ReviewAfterDays = RetentionDurable, 30 })
	if rep := save("영구 값", func(in *SaveInput) { in.Retention = RetentionDurable }); len(rep.FieldsKept) != 0 {
		t.Errorf("주지 않은 재검토 주기(기본 90 채움)를 알렸습니다: %v", rep.FieldsKept)
	}
	if rep := save("영구 값", func(in *SaveInput) { in.Retention = "normal" }); !slices.Equal(rep.FieldsKept, []string{"retention"}) {
		t.Errorf("normal 명시를 알리지 않았습니다: %v", rep.FieldsKept)
	}

	save("태그 값", func(in *SaveInput) { in.Tags = []string{"가", "나"} })
	if rep := save("태그 값", func(in *SaveInput) { in.Tags = []string{"나", "가"} }); len(rep.FieldsKept) != 0 {
		t.Errorf("태그 순서만 다른데 알렸습니다: %v", rep.FieldsKept)
	}
}
