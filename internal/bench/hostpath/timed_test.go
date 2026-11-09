package hostpath_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
	"github.com/crispebble/orbit-mem/internal/bench/legacy"
)

// 1-6 b6r 2차: 재생 사례는 나중에 뜬 DB 위에서 돈다. 사례에 시각(at)이 있으면 그 시각에 있던 기억만 보이고, 그 뒤에 만든
// 정답은 「그 시각엔 없던 정답」으로 따로 센다. 옛 판·새 판 모두 같다.
const timedJSON = `{"about":"t","now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s",
"memories":[
 {"key":"pnpm","kind":"rule","scope":"project:/bench/app","quote":true,"title":"이 앱은 pnpm으로 설치한다","body":"npm install을 쓰지 않는다.","topic":"앱 패키지 설치","ageDays":40},
 {"key":"late","kind":"rule","scope":"project:/bench/app","quote":true,"title":"배포 창은 화요일 오전이다","body":"금요일 배포 금지.","topic":"배포 창","ageDays":2}
],
"cases":[
 {"id":"early","session":"s1","origin":"user","prompt":"배포 창 언제야 화요일 오전 맞지","expect":["late"],"at":"2026-10-25T09:00:00+09:00"},
 {"id":"early-pnpm","session":"s1","origin":"user","prompt":"pnpm으로 설치해 줘","expect":["pnpm"],"at":"2026-10-25T09:05:00+09:00"},
 {"id":"after","session":"s2","origin":"user","prompt":"배포 창 언제야 화요일 오전 맞지","expect":["late"],"at":"2026-11-03T08:00:00+09:00"}
]}`

func TestTimed_사례_시각에_없던_기억은_보이지_않고_없던_정답으로_센다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	set, err := bench.Parse([]byte(timedJSON))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := bench.Run(p, hostpath.Factory, legacy.Env)
	if err != nil {
		t.Fatal(err)
	}
	late := p.IDOf("late")
	for _, run := range rep.Runs {
		shown := map[string][]string{}
		for _, c := range run.Cases {
			if c.Request != nil {
				shown[c.ID] = c.Request.IDs
			}
		}
		for _, s := range run.Sessions {
			shown[s.Session] = s.IDs
		}
		if slices.Contains(shown["early"], late) || slices.Contains(shown["s1"], late) {
			t.Fatalf("%s: a memory made after the case was shown: %v", run.Name, shown)
		}
		if run.All.Requests.NotYet != 1 || run.Cases[0].NotYet[0] != late || len(run.Cases[2].NotYet) != 0 {
			t.Fatalf("%s: not yet %d %v", run.Name, run.All.Requests.NotYet, run.Cases)
		}
		if run.Name == hostpath.Name && !slices.Contains(shown["after"], late) {
			t.Fatalf("%s: the memory is there at the later case: %v", run.Name, shown)
		}
	}
}

func TestTimed_사례_시각은_모두에_있거나_모두에_없다(t *testing.T) {
	mixed := strings.Replace(timedJSON, `,"at":"2026-10-25T09:05:00+09:00"`, ``, 1)
	if _, err := bench.Parse([]byte(mixed)); err == nil {
		t.Fatal("a case without at in a timed set must be refused")
	}
	bad := strings.Replace(timedJSON, `"2026-10-25T09:05:00+09:00"`, `"어제"`, 1)
	if _, err := bench.Parse([]byte(bad)); err == nil {
		t.Fatal("at must be RFC3339")
	}
}

func TestTimed_시계를_못_옮기는_경로는_거절한다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	set, err := bench.Parse([]byte(timedJSON))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	still := func(db string, s *bench.Set) (bench.Env, error) {
		env, err := hostpath.Factory(db, s)
		env.SetNow = nil
		return env, err
	}
	if _, err := bench.Run(p, still); err == nil || !strings.Contains(err.Error(), "clock") {
		t.Fatalf("err %v", err)
	}
}

// 1-6 b6r 3차: 요청 훅이 「이 세션 중 바뀐 것」(B3) 줄을 넣으면 두 경로 모두 그 몫(News·NewsChars)을 따로 넘긴다. 세션
// 묶음을 만든 뒤 확정된 기억(late)이 소식 줄로 들고, 요청 회상(B1) 몫과 섞이지 않는다.
const newsJSON = `{"about":"t","now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s",
"memories":[
 {"key":"pnpm","kind":"rule","scope":"project:/bench/app","quote":true,"title":"이 앱은 pnpm으로 설치한다","body":"npm install을 쓰지 않는다.","topic":"앱 패키지 설치","ageDays":40},
 {"key":"late","kind":"rule","scope":"project:/bench/app","quote":true,"title":"배포 창은 화요일 오전이다","body":"금요일 배포 금지.","topic":"배포 창","ageDays":2},
 {"key":"deploy","kind":"fact","scope":"project:/bench/app","title":"운영 배포는 main 병합으로 일어난다","body":"감시기가 origin/main을 보고 배포한다.","ageDays":30}
],
"cases":[
 {"id":"first","session":"s1","origin":"user","prompt":"pnpm으로 설치해 줘","at":"2026-10-25T09:00:00+09:00"},
 {"id":"later","session":"s1","origin":"user","prompt":"운영 배포는 main 병합으로 일어나지? 감시기 배포","expect":["deploy"],"at":"2026-11-03T08:00:00+09:00"}
]}`

func TestNews_두_경로_모두_세션_소식_몫을_따로_넘긴다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	set, err := bench.Parse([]byte(newsJSON))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := bench.Run(p, hostpath.Factory, legacy.Env)
	if err != nil {
		t.Fatal(err)
	}
	late, deploy := p.IDOf("late"), p.IDOf("deploy")
	for _, run := range rep.Runs {
		var got *bench.Seen
		for _, c := range run.Cases {
			if c.ID == "later" {
				got = c.Request
			}
		}
		if got == nil || !slices.Equal(got.News, []string{late}) || got.NewsChars <= 0 || got.NewsChars >= got.Chars {
			t.Fatalf("%s: later request %+v, want the news of late apart from its recall", run.Name, got)
		}
		if !slices.Contains(got.IDs, deploy) || slices.Contains(got.News, deploy) {
			t.Fatalf("%s: later request %+v, want deploy recalled (not news)", run.Name, got)
		}
		recalled := 0 // 요청 회상이 넣은 id: 소식 줄의 id는 빼고 센다
		for _, c := range run.Cases {
			if c.Request != nil {
				recalled += len(c.Request.IDs) - len(c.Request.News)
			}
		}
		q := run.All.Requests
		if q.News.Requests != 1 || q.News.IDs != 1 || q.Correct != 1 || q.Injected != recalled {
			t.Fatalf("%s: news %+v, injected %d (recalled %d) correct %d, want the news apart from what was put in", run.Name, q.News, q.Injected, recalled, q.Correct)
		}
	}
}
