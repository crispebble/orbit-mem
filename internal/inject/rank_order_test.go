package inject

// 순위 규칙의 성질(명세 R-1·R-3): 비교가 전순서라 입력 순서가 결과를 바꾸지 않고, 스위치를 끄고 재검토 지남이 없으면
// 05e2399의 순서(확정, 점수, 맞은 비율, 고친 시각, id)와 같고, 꺼져도 재검토 지남은 뒤로 간다.

import (
	"math"
	"math/rand"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 점수 격자 위의 모든 열쇠 조합에서 두 비교(before, beforeTool)가 전순서다: 꼬리를 번호로 대신한 less가 서로 다른 둘 가운데
// 하나만 앞이라 하고 이행적이다. 켠 쪽은 문턱 0.1과 격자 간격 0.05의 경계(차가 정확히 문턱)를 지난다.
func TestRank_비교는_전순서다(t *testing.T) {
	var keys []rankKey
	for i := 0; i <= 10; i++ {
		for mask := 0; mask < 8; mask++ {
			keys = append(keys, rankKey{score: float64(i) * 0.05, confirmed: mask&1 != 0, reviewDue: mask&2 != 0, rule: mask&4 != 0})
		}
	}
	for _, byScore := range []bool{true, false} {
		p := rankPolicy{byScore: byScore, margin: 0.1}
		for name, cmp := range map[string]func(a, b rankKey) (bool, bool){"before": p.before, "beforeTool": p.beforeTool} {
			less := func(i, j int) bool {
				if first, decided := cmp(keys[i], keys[j]); decided {
					return first
				}
				// 호출처의 꼬리: 점수(반올림), 그다음 번호(id를 대신한다).
				if si, sj := math.Round(keys[i].score*1e9), math.Round(keys[j].score*1e9); si != sj {
					return si > sj
				}
				return i < j
			}
			n := len(keys)
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					if i != j && less(i, j) == less(j, i) {
						t.Fatalf("%s byScore=%v: %+v and %+v are not ordered one way", name, byScore, keys[i], keys[j])
					}
					if !less(i, j) {
						continue
					}
					for k := 0; k < n; k++ {
						if less(j, k) && !less(i, k) {
							t.Fatalf("%s byScore=%v: %+v < %+v < %+v but not %+v < %+v", name, byScore, keys[i], keys[j], keys[k], keys[i], keys[k])
						}
					}
				}
			}
		}
	}
}

// rankMix saves n global memories (every second one confirmed, every third one past its review day) and gives each a
// score from a small grid, so scores tie, fall inside the margin and fall outside it.
func rankMix(t *testing.T, s *memory.Store, n int, rng *rand.Rand, withDue bool) []memory.RecallHit {
	t.Helper()
	var hits []memory.RecallHit
	for i := 0; i < n; i++ {
		due := withDue && i%3 == 0
		m := saveRank(t, s, "순위 시험 기억 "+string(rune('가'+i)), i%2 == 0, due)
		hits = append(hits, memory.RecallHit{Memory: m, Score: float64(rng.Intn(9)) * 0.05, Coverage: float64(rng.Intn(3)) * 0.25})
	}
	later(s, 48*time.Hour) // 재검토 날이 지난다
	return hits
}

// 입력 순서를 무작위로 섞어도 결과가 같다(꼬리가 id까지 가므로 순서가 하나로 정해진다).
func TestRank_입력_순서가_달라도_결과가_같다(t *testing.T) {
	s := store(t)
	rng := rand.New(rand.NewSource(7))
	hits := rankMix(t, s, 12, rng, true)
	for _, cfg := range []config.Config{ranked(), config.Default()} {
		want := rankIDs(t, s, cfg, slices.Clone(hits))
		for i := 0; i < 30; i++ {
			shuffled := slices.Clone(hits)
			rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
			if got := rankIDs(t, s, cfg, shuffled); !slices.Equal(got, want) {
				t.Fatalf("byScore %v: shuffle %d gave %v, want %v", cfg.RankByScore, i, got, want)
			}
		}
	}
}

// 스위치를 끄고 재검토 지남이 없으면 05e2399의 순서와 같다: 확정이 먼저, 점수, 맞은 비율, 고친 시각(새것 먼저), id.
func TestRank_꺼짐은_재검토_지남이_없으면_05e2399_순서와_같다(t *testing.T) {
	s := store(t)
	rng := rand.New(rand.NewSource(11))
	hits := rankMix(t, s, 12, rng, false)
	round := func(x float64) float64 { return math.Round(x * 1e9) }
	want := slices.Clone(hits)
	sort.SliceStable(want, func(i, j int) bool {
		a, c := want[i], want[j]
		if ca, cc := a.Memory.State == memory.StateConfirmed, c.Memory.State == memory.StateConfirmed; ca != cc {
			return ca
		}
		if round(a.Score) != round(c.Score) {
			return round(a.Score) > round(c.Score)
		}
		if round(a.Coverage) != round(c.Coverage) {
			return round(a.Coverage) > round(c.Coverage)
		}
		if a.Memory.UpdatedAt != c.Memory.UpdatedAt {
			return a.Memory.UpdatedAt > c.Memory.UpdatedAt
		}
		return a.Memory.ID < c.Memory.ID
	})
	var wantIDs []string
	for _, h := range want {
		wantIDs = append(wantIDs, h.Memory.ID)
	}
	for i := 0; i < 10; i++ {
		shuffled := slices.Clone(hits)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := rankIDs(t, s, config.Default(), shuffled); !slices.Equal(got, wantIDs) {
			t.Fatalf("shuffle %d gave %v, want %v", i, got, wantIDs)
		}
	}
}

// 스위치가 꺼져 있어도 재검토 지남은 같은 점수의 확정 기억 뒤로 간다(R-3은 스위치와 관계없다). 재검토 지남이 더 늦게 고쳐졌다.
func TestRank_꺼져도_재검토_지남은_같은_점수의_확정_기억_뒤다(t *testing.T) {
	s := store(t)
	fresh := saveRank(t, s, "최근 확정 기억", true, false)
	due := saveRank(t, s, "재검토 지난 확정 기억", true, true)
	later(s, 48*time.Hour)
	hits := []memory.RecallHit{{Memory: due, Score: 0.5, Coverage: 0.5}, {Memory: fresh, Score: 0.5, Coverage: 0.5}}
	cfg := config.Default()
	if cfg.RankByScore {
		t.Fatal("rankByScore is on by default")
	}
	if got, want := rankIDs(t, s, cfg, hits), []string{fresh.ID, due.ID}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}
