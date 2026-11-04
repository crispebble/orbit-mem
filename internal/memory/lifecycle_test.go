package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const day = int64(24 * time.Hour / time.Millisecond)

func TestSave_휘발_기억은_기본_7일_뒤_만료되고_7일을_넘길_수_없다(t *testing.T) {
	s, c := newTestStore(t)
	at := c.t.UnixMilli()
	m, err := s.Save(SaveInput{Kind: KindFact, Title: "오늘 배포 창은 열려 있다", Scope: ScopeGlobal, Retention: RetentionVolatile,
		Source: Source{Kind: SourceSession}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Retention != RetentionVolatile || m.ExpiresAt != at+7*day || m.ReviewAt != 0 {
		t.Fatalf("got retention=%q expires=%d review=%d", m.Retention, m.ExpiresAt, m.ReviewAt)
	}
	short, err := s.Save(SaveInput{Kind: KindFact, Title: "사흘 짜리", Scope: ScopeGlobal, Retention: RetentionVolatile, TTLDays: 3,
		Source: Source{Kind: SourceSession}})
	if err != nil || short.ExpiresAt != at+3*day {
		t.Fatalf("ttl 3: %v %d", err, short.ExpiresAt)
	}
	for _, in := range []SaveInput{
		{Kind: KindFact, Title: "여드레", Retention: RetentionVolatile, TTLDays: 8},
		{Kind: KindFact, Title: "보통인데 만료", TTLDays: 3},
		{Kind: KindFact, Title: "모르는 등급", Retention: "forever"},
		{Kind: KindFact, Title: "재검토 음수", ReviewAfterDays: -1},
	} {
		in.Source = Source{Kind: SourceSession}
		if _, err := s.Save(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", in.Title, err)
		}
	}
}

func TestSave_영구_기억은_기본_90일_뒤_재검토하고_보통은_주어질_때만(t *testing.T) {
	s, c := newTestStore(t)
	at := c.t.UnixMilli()
	d, err := s.Save(SaveInput{Kind: KindDecision, Title: "엔진은 자체 포크", Retention: RetentionDurable, Source: Source{Kind: SourceSession}})
	if err != nil || d.ReviewAt != at+90*day || d.ExpiresAt != 0 {
		t.Fatalf("durable: %v review=%d expires=%d", err, d.ReviewAt, d.ExpiresAt)
	}
	d30, _ := s.Save(SaveInput{Kind: KindDecision, Title: "한 달 뒤 다시 본다", Retention: RetentionDurable, ReviewAfterDays: 30, Source: Source{Kind: SourceSession}})
	if d30.ReviewAt != at+30*day {
		t.Fatalf("durable 30: %d", d30.ReviewAt)
	}
	n, _ := s.Save(SaveInput{Kind: KindFact, Title: "보통 기억", Retention: "normal", Source: Source{Kind: SourceSession}})
	if n.Retention != "" || n.ReviewAt != 0 {
		t.Fatalf("normal: %q %d", n.Retention, n.ReviewAt)
	}
	n14, _ := s.Save(SaveInput{Kind: KindFact, Title: "보통인데 2주 뒤 확인", ReviewAfterDays: 14, Source: Source{Kind: SourceSession}})
	if n14.ReviewAt != at+14*day {
		t.Fatalf("normal 14: %d", n14.ReviewAt)
	}
}

func TestMemory_Freshness는_만료와_재검토일을_가른다(t *testing.T) {
	s, c := newTestStore(t)
	v, _ := s.Save(SaveInput{Kind: KindFact, Title: "휘발", Retention: RetentionVolatile, TTLDays: 2, Source: Source{Kind: SourceSession}})
	d, _ := s.Save(SaveInput{Kind: KindRule, Title: "영구", Retention: RetentionDurable, ReviewAfterDays: 10, Source: Source{Kind: SourceQuote, Excerpt: "영구"}})
	if v.Freshness(c.t) != FreshOK || d.Freshness(c.t) != FreshOK {
		t.Fatal("fresh at first")
	}
	later := c.t.Add(2 * 24 * time.Hour)
	if got := v.Freshness(later); got != FreshExpired {
		t.Fatalf("volatile at expiry = %q", got)
	}
	if got := d.Freshness(c.t.Add(11 * 24 * time.Hour)); got != FreshReviewDue {
		t.Fatalf("durable past review = %q", got)
	}
}

func TestSave_주제는_정규화해_남기고_같은_범위의_최신_판이_주제의_머리다(t *testing.T) {
	s, c := newTestStore(t)
	old, _ := s.Save(SaveInput{Kind: KindDecision, Title: "기본 모델은 sonnet", Topic: "  Orbit  Code 기본 모델 ", Source: Source{Kind: SourceSession}})
	if old.Topic != "orbit code 기본 모델" {
		t.Fatalf("topic = %q", old.Topic)
	}
	c.t = c.t.Add(time.Hour)
	cur, _ := s.Save(SaveInput{Kind: KindDecision, Title: "기본 모델은 opus", Topic: "orbit code 기본 모델", Source: Source{Kind: SourceSession}})
	other, _ := s.Save(SaveInput{Kind: KindDecision, Title: "다른 프로젝트 기본 모델", Topic: "orbit code 기본 모델", Scope: "project:x", Source: Source{Kind: SourceSession}})
	heads, err := s.TopicHeads()
	if err != nil {
		t.Fatal(err)
	}
	if heads.Superseding(old) != cur.ID || heads.Superseding(cur) != "" || heads.Superseding(other) != "" {
		t.Fatalf("heads = %+v", heads)
	}
	// 최신 판이 낡아도 옛 판이 다시 살아나지 않는다.
	if _, err := s.SetState(cur.ID, StateStale, "틀림"); err != nil {
		t.Fatal(err)
	}
	heads, _ = s.TopicHeads()
	if heads.Superseding(old) != cur.ID {
		t.Fatal("old value came back")
	}
}

func TestOpen_옛_판_DB에는_열을_더하고_사본을_남기며_옛_판이_쓴_줄은_기본값이다(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP INDEX memories_topic`); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"retention", "review_days", "review_at", "expires_at", "topic"} {
		if _, err := s.DB().Exec(`ALTER TABLE memories DROP COLUMN ` + col); err != nil {
			t.Fatal(err)
		}
	}
	// 옛 판 실행 파일이 쓰는 꼴 그대로 넣는다.
	if _, err := s.DB().Exec(`INSERT INTO memories(id,kind,scope,project,state,title,title_key,body,tags,version,created_at,updated_at,legacy_id)
		VALUES('m-old','fact','global','','candidate','옛 판','옛 판','','[]',1,1,1,NULL)`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := s.Get("m-old")
	if err != nil || m.Retention != "" || m.ReviewAt != 0 || m.ExpiresAt != 0 || m.Topic != "" {
		t.Fatalf("old row: %v %+v", err, m)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) != 1 {
		t.Fatalf("schema backup copies = %d", len(entries))
	}
	// 옛 판 실행 파일의 고치기(열 이름을 대어 UPDATE)는 새 열을 건드리지 않는다.
	v, _ := s.Save(SaveInput{Kind: KindFact, Title: "새 판", Retention: RetentionDurable, Topic: "t", Source: Source{Kind: SourceSession}})
	if _, err := s.DB().Exec(`UPDATE memories SET kind=?,scope=?,project=?,state=?,title=?,title_key=?,body=?,tags=?,version=?,updated_at=?,superseded_by=?,stale_reason=? WHERE id=?`,
		v.Kind, v.Scope, v.Project, v.State, v.Title, TitleKey(v.Title), "고침", "[]", v.Version+1, v.UpdatedAt, "", "", v.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(v.ID); got.Retention != RetentionDurable || got.Topic != "t" || got.ReviewAt == 0 {
		t.Fatalf("old writer dropped new fields: %+v", got)
	}
}

func TestReviewed는_재검토일을_다시_민다(t *testing.T) {
	s, c := newTestStore(t)
	d, _ := s.Save(SaveInput{Kind: KindDecision, Title: "기억 DB는 PC마다", Retention: RetentionDurable, ReviewAfterDays: 5, Source: Source{Kind: SourceSession}})
	c.t = c.t.Add(10 * 24 * time.Hour)
	m, err := s.Reviewed(d.ID, Who{Actor: ActorAI})
	if err != nil {
		t.Fatal(err)
	}
	if m.ReviewAt != c.t.UnixMilli()+5*day || m.Freshness(c.t) != FreshOK {
		t.Fatalf("review_at = %d", m.ReviewAt)
	}
}

func TestTopicHeads_후보는_확정된_주제를_가리지_못한다(t *testing.T) {
	s, c := newTestStore(t)
	user, _ := s.Save(SaveInput{Kind: KindRule, Title: "보고는 한국어로", Topic: "보고 언어", Source: Source{Kind: SourceQuote, Excerpt: "보고는 한국어로 해"}})
	c.t = c.t.Add(time.Hour)
	ai, _ := s.Save(SaveInput{Kind: KindRule, Title: "보고는 영어로", Topic: "보고 언어", Source: Source{Kind: SourceSession}})
	heads, _ := s.TopicHeads()
	if heads.Superseding(user) != "" || heads.Superseding(ai) != user.ID {
		t.Fatalf("heads = %+v", heads)
	}
	// 확정된 새 판은 옛 확정 판을 대신한다.
	c.t = c.t.Add(time.Hour)
	newer, _ := s.Save(SaveInput{Kind: KindRule, Title: "보고는 한국어 존댓말로", Topic: "보고 언어", Source: Source{Kind: SourceQuote, Excerpt: "존댓말로"}})
	heads, _ = s.TopicHeads()
	if heads.Superseding(user) != newer.ID {
		t.Fatalf("heads = %+v", heads)
	}
}
