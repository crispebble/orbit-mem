package memory

import "testing"

func TestExcerptIn_공백과_비밀값_가림을_무시하고_찾는다(t *testing.T) {
	content := "# 규칙\n\n- 커밋은   경로를\n  명시한다.\n- token=sk-abcdefghijklmnopqrstuvwxyz0123\n"
	cases := []struct {
		excerpt string
		want    bool
	}{
		{"커밋은 경로를 명시한다.", true},
		{"- 커밋은 경로를\n명시한다.", true},
		{MaskSecrets("token=sk-abcdefghijklmnopqrstuvwxyz0123"), true},
		{"커밋은 경로를 명시하지 않는다.", false},
		{"   ", false},
	}
	for _, c := range cases {
		if got := ExcerptIn(c.excerpt, content); got != c.want {
			t.Errorf("ExcerptIn(%q) = %v", c.excerpt, got)
		}
	}
}

func TestSetSourceHash_출처_해시만_바꾸고_판을_올리지_않는다(t *testing.T) {
	s, _ := newTestStore(t)
	m, err := s.Save(SaveInput{Kind: KindFact, Title: "파일 근거", Scope: ScopeGlobal, State: StateConfirmed,
		Source: Source{Kind: SourceFile, Ref: "/x/rule.md", Excerpt: "발췌", Hash: "old"}})
	if err != nil {
		t.Fatal(err)
	}
	src := m.Sources[0]
	if err := s.SetSourceHash(src.ID, "new"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(m.ID)
	if got.Sources[0].Hash != "new" || got.Version != m.Version {
		t.Fatalf("got = %+v", got)
	}
	if err := s.SetSourceHash("s-없음", "x"); err == nil {
		t.Fatal("unknown source must fail")
	}
}
