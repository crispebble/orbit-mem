package memory

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// FTS5 색인 가공 글과 MATCH 질의 글(설계 7절, ORBIT-25).

func TestIndexText_한글_어절은_겹치는_두_글자로_쪼갠다(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"설계서를", []string{"설계", "계서", "서를"}},
		{"설계", []string{"설계"}},
		{"설", []string{"설"}},
		{"기억 DB를 열다", []string{"기억", "db", "를", "열다"}},
		{"설계서를 읽다", []string{"설계", "계서", "서를", "읽다"}},
	}
	for _, c := range cases {
		if got := indexTokens(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("indexTokens(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIndexText_라틴과_숫자는_소문자_낱말로_둔다(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Deploy-Pipeline v2", []string{"deploy", "pipeline", "v2"}},
		{"snake_case 2026", []string{"snake", "case", "2026"}},
		{"Ünï", []string{"ünï"}},
	}
	for _, c := range cases {
		if got := indexTokens(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("indexTokens(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIndexText_한자와_가나도_겹치는_두_글자로_쪼갠다(t *testing.T) {
	if got, want := indexTokens("設計書"), []string{"設計", "計書"}; !reflect.DeepEqual(got, want) {
		t.Errorf("한자 = %q, want %q", got, want)
	}
	if got, want := indexTokens("デプロイ"), []string{"デプ", "プロ", "ロイ"}; !reflect.DeepEqual(got, want) {
		t.Errorf("가타카나 = %q, want %q", got, want)
	}
	// 글자 종류가 바뀌는 곳에서는 바이그램을 만들지 않는다.
	if got, want := indexTokens("設計です"), []string{"設計", "です"}; !reflect.DeepEqual(got, want) {
		t.Errorf("한자+히라가나 = %q, want %q", got, want)
	}
}

func TestIndexText_어절을_넘는_바이그램은_없다(t *testing.T) {
	got := indexTokens("설계 서를")
	want := []string{"설계", "서를"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIndexText_빈_글과_기호만은_토큰이_없다(t *testing.T) {
	for _, in := range []string{"", "   ", "!?-_()\"*:^", "\n\t"} {
		if got := indexTokens(in); len(got) != 0 {
			t.Errorf("indexTokens(%q) = %q, want none", in, got)
		}
		if got := indexText(in); got != "" {
			t.Errorf("indexText(%q) = %q, want empty", in, got)
		}
		if got := matchQuery(in, 60); got != "" {
			t.Errorf("matchQuery(%q) = %q, want empty", in, got)
		}
		if got := queryTokens(in, 60); len(got) != 0 {
			t.Errorf("queryTokens(%q) = %q, want none", in, got)
		}
	}
}

func TestMatchQuery_모든_토큰을_따옴표로_감싼다(t *testing.T) {
	cases := []struct{ in, want string }{
		{"설계서를", `"설계 계서 서를"`},
		{"설", `"설"*`},
		{"deploy", `"deploy"*`},
		{"설계서를 deploy", `"설계 계서 서를" OR "deploy"*`},
		{"DB를", `"db"* OR "를"*`},
	}
	for _, c := range cases {
		if got := matchQuery(c.in, 60); got != c.want {
			t.Errorf("matchQuery(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestMatchQuery_토큰_상한을_넘으면_뒤_어절을_버린다(t *testing.T) {
	var words []string
	for i := 0; i < 200; i++ {
		words = append(words, "word"+strings.Repeat("a", i%7))
	}
	q := strings.Join(words, " ")
	m := matchQuery(q, 10)
	if n := strings.Count(m, " OR ") + 1; n != 10 {
		t.Errorf("OR 항 수 = %d, want 10 (%s)", n, m)
	}
	if got := queryTokens(q, 10); len(got) > 10 {
		t.Errorf("queryTokens 수 = %d, want <= 10", len(got))
	}
	long := strings.Repeat("가나다라마바사", 30)
	if got := queryTokens(long, 20); len(got) > 20 {
		t.Errorf("긴 한 어절 토큰 수 = %d, want <= 20", len(got))
	}
	m = matchQuery(long, 20)
	if n := len(strings.Fields(strings.Trim(m, `"`))); n != 20 {
		t.Errorf("긴 한 어절 구 토큰 수 = %d, want 20", n)
	}
}

func TestMatchQuery_점수용_토큰은_중복이_없다(t *testing.T) {
	got := queryTokens("설계서 설계서 Deploy deploy", 60)
	want := []string{"설계", "계서", "deploy"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func ftsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitedb.FileURI(filepath.Join(t.TempDir(), "fts.db")))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE VIRTUAL TABLE t USING fts5(title, keys, body, tokenize='unicode61')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func ftsAdd(t *testing.T, db *sql.DB, title, keys, body string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO t(title, keys, body) VALUES (?, ?, ?)`, indexText(title), indexText(keys), indexText(body)); err != nil {
		t.Fatal(err)
	}
}

func ftsFind(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	m := matchQuery(q, 60)
	if m == "" {
		return nil
	}
	rows, err := db.Query(`SELECT title FROM t WHERE t MATCH ? ORDER BY rowid`, m)
	if err != nil {
		t.Fatalf("MATCH %s: %v", m, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMatchQuery_실제_FTS5에서_한글_낱말과_접두를_찾는다(t *testing.T) {
	db := ftsDB(t)
	ftsAdd(t, db, "설계서를", "", "")
	ftsAdd(t, db, "deployment", "", "")
	ftsAdd(t, db, "기억", "", "알약을 먹다")

	for _, q := range []string{"설계", "설계서", "설", "설계서를"} {
		if got := ftsFind(t, db, q); !reflect.DeepEqual(got, []string{indexText("설계서를")}) {
			t.Errorf("질의 %q → %q, want 설계서를", q, got)
		}
	}
	if got := ftsFind(t, db, "계설"); len(got) != 0 {
		t.Errorf("질의 계설 → %q, want none", got)
	}
	if got := ftsFind(t, db, "deploy"); !reflect.DeepEqual(got, []string{"deployment"}) {
		t.Errorf("질의 deploy → %q", got)
	}
	if got := ftsFind(t, db, "알약"); !reflect.DeepEqual(got, []string{indexText("기억")}) {
		t.Errorf("질의 알약 → %q", got)
	}
	if got := ftsFind(t, db, "기억 zzz"); !reflect.DeepEqual(got, []string{indexText("기억")}) {
		t.Errorf("OR 질의 → %q", got)
	}
}

func TestMatchQuery_FTS5_문법_글자가_든_질의도_오류가_없다(t *testing.T) {
	db := ftsDB(t)
	ftsAdd(t, db, "foo bar", "", "AND OR NEAR 설계")
	queries := []string{
		`foo-bar`, `AND`, `OR`, `NOT`, `NEAR`, `NEAR(a b)`, `a(b`, `a)b`, `x:y`, `title:foo`, `"quoted`,
		`say "hi" now`, `foo*`, `*`, `^foo`, `-foo`, `a OR`, `"`, `""`, `'`, `{keys}:x`, `foo AND bar OR baz`,
		`설계" OR "x`, `(설계)`, "a\x00b", `{`, `+ - * ^ : ( )`,
	}
	for _, q := range queries {
		m := matchQuery(q, 60)
		if m == "" {
			continue
		}
		rows, err := db.Query(`SELECT rowid FROM t WHERE t MATCH ?`, m)
		if err != nil {
			t.Errorf("질의 %q → MATCH %s: %v", q, m, err)
			continue
		}
		rows.Close()
	}
	if got := ftsFind(t, db, `NEAR`); !reflect.DeepEqual(got, []string{"foo bar"}) {
		t.Errorf("질의 NEAR → %q", got)
	}
}

func TestIndexText_unicode61이_다시_나눠도_같은_토큰이다(t *testing.T) {
	db := ftsDB(t)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE v USING fts5vocab(t, 'row')`); err != nil {
		t.Fatal(err)
	}
	in := "설계서를 읽고 기억 DB를 열다. Deploy-Pipeline v2 snake_case 設計書 デプロイ a"
	ftsAdd(t, db, "", "", in)
	rows, err := db.Query(`SELECT term FROM v`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	set := map[string]bool{}
	for _, tok := range indexTokens(in) {
		set[tok] = true
	}
	var want []string
	for tok := range set {
		want = append(want, tok)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unicode61 토큰 = %q\nindexTokens = %q", got, want)
	}
	if txt := indexText(in); strings.Join(indexTokens(in), " ") != txt {
		t.Errorf("indexText = %q", txt)
	}
}
