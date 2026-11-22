package bench_test

import (
	"slices"
	"testing"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/paths"
)

// publicRun runs one recall path over the public set and gives its report, with the ids of the memory keys.
func publicRun(t *testing.T, name string) (bench.RunReport, *bench.Set, func(key string) string) {
	t.Helper()
	return publicRunWith(t, factory(t, name))
}

// publicRunWith is publicRun with the factory itself, for a test that wraps a path.
func publicRunWith(t *testing.T, f bench.Factory) (bench.RunReport, *bench.Set, func(key string) string) {
	t.Helper()
	fixHome(t)
	set, err := bench.Read(publicSet)
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rep, err := bench.Run(p, f)
	if err != nil {
		t.Fatal(err)
	}
	return rep.Runs[0], set, p.IDOf
}

// startTexts records the bundle text of every session start a path gives, per session in call order, so a test
// can compare the text itself: ids and a char count alone do not tell a changed bundle from the same one.
type startTexts struct {
	bench.Recaller
	texts map[string][]string
}

func (s startTexts) StartFrom(session, cwd, source string) (bench.Shown, error) {
	var sh bench.Shown
	var err error
	if sf, ok := s.Recaller.(bench.SourcedStarter); ok {
		sh, err = sf.StartFrom(session, cwd, source)
	} else {
		sh, err = s.Recaller.Start(session, cwd)
	}
	s.texts[session] = append(s.texts[session], sh.Text)
	return sh, err
}

func (s startTexts) Reset(session string) error {
	if r, ok := s.Recaller.(bench.Resetter); ok {
		return r.Reset(session)
	}
	return nil
}

func resultOf(run bench.RunReport, id string) (bench.CaseResult, bool) {
	for _, c := range run.Cases {
		if c.ID == id {
			return c, true
		}
	}
	return bench.CaseResult{}, false
}

// 명세 7절 4: 평가 묶음의 폐기 주입 사례가 새 버전에서 0건이다. 사례는 세션 시작이 만든 묶음이 재개에 다시 가기 전에 그
// 묶음 안 기억이 낡거나 거절되는 꼴이다(사례의 changes). 0이 아무것도 재지 않아서 나온 0이 아니도록 같이 본다: 바뀐
// 기억은 그 세션의 앞 시작 묶음에 실려 있었고, 다시 간 묶음에는 없고, 그 시작에 현재 값이 아닌 id가 없다.
func TestPublicSet_폐기된_기억_주입_사례가_0건이다(t *testing.T) {
	run, set, idOf := publicRun(t, paths.Current)
	var changed []bench.Case
	for _, c := range set.Cases {
		if len(c.Changes) > 0 {
			changed = append(changed, c)
		}
	}
	if len(changed) < 4 {
		t.Fatalf("폐기 주입 사례(changes가 있는 재개 시작) %d개, 4개 이상이어야 한다", len(changed))
	}
	for _, c := range changed {
		res, ok := resultOf(run, c.ID)
		if !ok || res.Start == nil {
			t.Fatalf("사례 %s: 시작 결과가 없다", c.ID)
		}
		// 앞 시작: 이 사례 앞에서 같은 세션이 받은 시작들의 id의 합
		var before []string
		for _, r := range run.Cases {
			if r.ID == c.ID {
				break
			}
			if r.Session == c.Session && r.Start != nil {
				before = append(before, r.Start.IDs...)
			}
		}
		for _, ch := range c.Changes {
			id := idOf(ch.Memory)
			if !slices.Contains(before, id) {
				t.Errorf("사례 %s: %s(%s)가 앞 시작 묶음에 없었다: 재개가 재전달할 것이 없는 사례다", c.ID, ch.Memory, id)
			}
			if slices.Contains(res.Start.IDs, id) {
				t.Errorf("사례 %s: %s가 %s 뒤 재개 묶음에 다시 실렸다", c.ID, ch.Memory, ch.State)
			}
		}
		if len(res.Start.Dead) != 0 {
			t.Errorf("사례 %s: 재개 시작에 현재 값이 아닌 id %v (%v)", c.ID, res.Start.Dead, res.Start.DeadWhy)
		}
	}
	byPath := map[string]bench.PathStat{"start": run.All.Paths.Start, "request": run.All.Paths.Request, "tool": run.All.Paths.Tool, "subagent": run.All.Paths.Subagent}
	for name, p := range byPath {
		if p.Dead != 0 || p.DeadCalls != 0 {
			t.Errorf("경로 %s: 현재 값이 아닌 id %d개(호출 %d, 까닭 %v), 0이어야 한다", name, p.Dead, p.DeadCalls, p.DeadByWhy)
		}
	}
}

// 바뀐 기억이 없으면 재개는 얼린 묶음을 그대로 돌려준다(명세 F-1, 프롬프트 캐시). 압축 뒤에는 세션 표시를 비워 같은 기억을
// 다시 넣는다. 둘 다 폐기 주입 사례와 같은 묶음의 재개·압축 사례다.
func TestPublicSet_바뀐_것이_없는_재개는_같은_묶음이고_압축_뒤에는_다시_넣는다(t *testing.T) {
	texts := map[string][]string{}
	inner := factory(t, paths.Current)
	run, _, idOf := publicRunWith(t, func(db string, set *bench.Set) (bench.Env, error) {
		env, err := inner(db, set)
		env.Recaller = startTexts{Recaller: env.Recaller, texts: texts}
		return env, err
	})
	first, _ := resultOf(run, "ops-same-1")
	again, ok := resultOf(run, "ops-same-2")
	if first.Start == nil || !ok || again.Start == nil {
		t.Fatal("재개 사례의 시작 결과가 없다")
	}
	if !slices.Equal(first.Start.IDs, again.Start.IDs) || first.Start.Chars != again.Start.Chars || len(first.Start.IDs) == 0 {
		t.Errorf("바뀐 것이 없는 재개: 앞 시작 %v(%d자), 재개 %v(%d자), 같은 묶음이어야 한다", first.Start.IDs, first.Start.Chars, again.Start.IDs, again.Start.Chars)
	}
	if got := texts["s-ops-same"]; len(got) != 2 || got[0] == "" || got[0] != got[1] {
		t.Errorf("바뀐 것이 없는 재개: 묶음 글이 같아야 한다, 시작 글 %d개 %q", len(got), got)
	}
	req, ok := resultOf(run, "ops-cmp-4")
	if !ok || req.Request == nil || !slices.Contains(req.Request.IDs, idOf("ops-fact-backup")) {
		t.Errorf("압축 뒤 같은 요청: 회상이 %+v, 사실 ops-fact-backup을 다시 넣어야 한다", req.Request)
	}
}
