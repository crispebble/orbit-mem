package legacy_test

import (
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/legacy"
)

// findSet은 얼린 찾기(87ab0fb memory_find)를 보는 묶음이다. 점수는 87ab0fb Relevance로 손으로 셌다:
// 「운영 배포」는 deploy-title·deploy-body 둘 다 3점(낱말 둘 + 구절)이라 확정인 deploy-body가 앞이다(제목 점수보다 상태가 먼저).
// deploy-other는 다른 저장소, deploy-stale은 낡음이라 나오지 않는다.
const findSet = `{
  "now": "2026-11-03T09:00:00+09:00",
  "cwd": "/bench/app",
  "seed": "legacy-find",
  "memories": [
    {"key":"deploy-title","kind":"lesson","scope":"global","title":"운영 배포 전에 백업을 확인한다","ageDays":5},
    {"key":"deploy-body","kind":"fact","scope":"global","quote":true,"title":"서버 목록","body":"운영 배포 대상 서버는 둘이다.","ageDays":3},
    {"key":"deploy-other","kind":"fact","scope":"project:/bench/other","quote":true,"title":"운영 배포는 다른 저장소에서 한다","ageDays":1},
    {"key":"deploy-stale","kind":"fact","scope":"global","state":"stale","title":"운영 배포는 금요일에 한다","ageDays":2},
    {"key":"pnpm","kind":"rule","scope":"here","quote":true,"title":"이 앱은 pnpm으로 설치한다","body":"npm install을 쓰지 않는다.","ageDays":9},
    {"key":"cache-a","kind":"fact","scope":"global","quote":true,"title":"캐시 만료 시간","ageDays":7},
    {"key":"cache-b","kind":"fact","scope":"global","quote":true,"title":"캐시 만료 정책","ageDays":7}
  ],
  "cases": [{"id":"c1","prompt":"아무 요청"}]
}`

func openEnv(t *testing.T, p *bench.Prepared) bench.Env {
	t.Helper()
	db, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	env, err := legacy.Env(db, p.Set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Close(); err != nil {
			t.Error(err)
		}
	})
	if env.Finder == nil || env.Similar == nil {
		t.Fatalf("legacy.Env has no frozen finder or similar search: finder %v, similar %v", env.Finder, env.Similar)
	}
	return env
}

func TestEnv의_찾기는_87ab0fb_memory_find처럼_점수_상태_범위로_고른다(t *testing.T) {
	p := prepare(t, findSet)
	env := openEnv(t, p)
	for _, c := range []struct {
		query string
		limit int
		want  []string
	}{
		{"운영 배포", bench.FindLimit, []string{"deploy-body", "deploy-title"}},  // 같은 3점: 확정이 후보보다 앞
		{"배포를 확인", bench.FindLimit, []string{"deploy-title", "deploy-body"}}, // 한글 낱말 앞부분 0.5점: 1.5 > 0.5
		{"pnpm install", bench.FindLimit, []string{"pnpm"}},                  // 낱말 안의 부분 글자도 맞는다
		{"운영 배포", 1, []string{"deploy-body"}},                                // 상한
		{"운영 배포", 0, []string{"deploy-body", "deploy-title"}},                // 0은 기본 8
	} {
		got, err := env.Finder.Find("/bench/app", c.query, c.limit)
		if err != nil {
			t.Fatal(err)
		}
		if want := ids(p, c.want...); !slices.Equal(got, want) {
			t.Errorf("Find(%q, %d) = %v, want %v", c.query, c.limit, keysOfIDs(p, got), c.want)
		}
	}
}

func TestEnv의_찾기는_점수와_시각이_같으면_id_순이고_다시_불러도_같다(t *testing.T) {
	p := prepare(t, findSet)
	env := openEnv(t, p)
	first, err := env.Finder.Find("/bench/app", "캐시 만료", bench.FindLimit)
	if err != nil {
		t.Fatal(err)
	}
	want := ids(p, "cache-a", "cache-b")
	slices.Sort(want)
	if !slices.Equal(first, want) {
		t.Fatalf("Find = %v, want %v (by id)", keysOfIDs(p, first), want)
	}
	for range 5 {
		again, err := env.Finder.Find("/bench/app", "캐시 만료", bench.FindLimit)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(again, first) {
			t.Fatalf("Find moved: %v then %v", first, again)
		}
	}
}

func TestEnv의_찾기는_다른_폴더에서는_그_폴더의_기억을_본다(t *testing.T) {
	p := prepare(t, findSet)
	env := openEnv(t, p)
	got, err := env.Finder.Find("/bench/other", "운영 배포", bench.FindLimit)
	if err != nil {
		t.Fatal(err)
	}
	// 셋 다 3점이다. 확정(deploy-other·deploy-body)이 앞이고, 그 둘은 제목 점수(2 > 0) 순이다.
	if want := ids(p, "deploy-other", "deploy-body", "deploy-title"); !slices.Equal(got, want) {
		t.Errorf("Find in /bench/other = %v", keysOfIDs(p, got))
	}
}

// 87ab0fb에는 저장을 막는 판정이 없었다: find-proxy 상위 3은 막지 않는 가까운 목록이다(1-6 b6dn).
func TestEnv의_비슷한_기억은_초안_제목과_본문으로_찾은_상위_3이고_막지_않는_가까운_목록이다(t *testing.T) {
	p := prepare(t, findSet)
	env := openEnv(t, p)
	got, err := env.Similar.Similar("/bench/app", bench.Draft{Kind: "lesson", Title: "운영 배포 전 백업"})
	if err != nil {
		t.Fatal(err)
	}
	if want := ids(p, "deploy-title", "deploy-body"); !slices.Equal(got.Near, want) || len(got.Block) != 0 {
		t.Errorf("Similar = %v, want deploy-title, deploy-body", keysOfIDs(p, got.Near))
	}
	got, err = env.Similar.Similar("/bench/app", bench.Draft{Kind: "rule", Title: "설치", Body: "pnpm"})
	if err != nil {
		t.Fatal(err)
	}
	if want := ids(p, "pnpm"); !slices.Equal(got.Near, want) || len(got.Block) != 0 {
		t.Errorf("Similar with body = %v, want pnpm", keysOfIDs(p, got.Near))
	}
	// 상위 3까지만 센다.
	got, err = env.Similar.Similar("/bench/app", bench.Draft{Kind: "fact", Title: "운영 배포 캐시 만료"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Near) != bench.SimilarLimit {
		t.Errorf("Similar = %v, want %d ids", keysOfIDs(p, got.Near), bench.SimilarLimit)
	}
}

func keysOfIDs(p *bench.Prepared, list []string) []string {
	out := []string{}
	for _, id := range list {
		out = append(out, p.Keys[id])
	}
	return out
}
