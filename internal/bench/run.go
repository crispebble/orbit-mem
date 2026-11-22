package bench

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// Run runs every recall path over the set, each on its own fresh copy of the DB so the
// shown marks and the activity of one never reach another; the copy starts with no session
// state (memory.ClearSessionState). Cases run in file order and a session starts (layer A)
// before its first case and again at every start case (a resume, the start after a compaction:
// that start is the layer A of the cases after it). When the cases carry their times, the copy
// is put at the state of each case's time and the path's clock is moved there before the case
// (the session start takes the time of its first case); an expected memory made after the case
// is counted as not there yet (Outcome.NotYet), the answer itself is kept.
func Run(p *Prepared, factories ...Factory) (*Report, error) {
	set := p.Set
	parts := Partition(set)
	rep := &Report{About: set.About, Now: set.Now, Seed: string(set.Seed), Cases: len(set.Cases)}
	rep.Split = SplitInfo{
		Units:      units(set.Cases, parts, PartAll),
		TrainUnits: units(set.Cases, parts, PartTrain),
		TestUnits:  units(set.Cases, parts, PartTest),
		Test:       CountBundles(set.Cases, parts, PartTest),
	}
	rep.Split.PointsPerCase = points(rep.Split.Test)
	for _, f := range factories {
		run, err := runOne(p, f, parts)
		if err != nil {
			return nil, err
		}
		rep.Runs = append(rep.Runs, run)
	}
	return rep, nil
}

func runOne(p *Prepared, f Factory, parts []string) (run RunReport, err error) {
	set := p.Set
	db, err := p.Copy()
	if err != nil {
		return run, err
	}
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			os.Remove(db + suffix)
		}
	}()
	// 사본에 실제 세션이 남긴 묶음·표시·위임 글을 비운다: 같은 세션 id의 재생이 그 얼린 묶음을 받으면 잰 주입이 이
	// 실행의 회상이 아니다. 사본은 Prepare가 표지를 남긴 것이고, 비우기는 기억 표를 건드리지 않는다.
	if err := memory.ClearSessionState(db); err != nil {
		return run, err
	}
	var rew *memory.Rewinder
	if p.history != nil {
		if rew, err = p.history.Rewind(db); err != nil {
			return run, err
		}
		defer rew.Close()
	}
	env, err := f(db, set)
	if err != nil {
		return run, err
	}
	defer func() {
		if env.Close != nil {
			if cerr := env.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	}()
	if env.Recaller == nil {
		return run, errors.New("the recall path has no Recaller")
	}
	name := env.Recaller.Name()
	run.Name = name
	if rew != nil && env.SetNow == nil {
		return run, fmt.Errorf("%s: the cases carry their times and the recall path cannot move its clock", name)
	}
	var last time.Time
	started := map[string]*Shown{}
	outs := make([]Outcome, 0, len(set.Cases))
	for i, c := range set.Cases {
		fail := func(err error) (RunReport, error) { return run, fmt.Errorf("%s: case %q: %w", name, c.ID, err) }
		key, cwd := c.SessionKey(), set.CWDOf(c)
		o := Outcome{Case: c, Key: key, Part: parts[i]}
		if rew != nil {
			// 그 시각의 DB와 시계(재생 사례): 그 뒤에 만든 기억은 없고, 그 뒤의 고침·낡음은 그 전 판이다.
			t, _ := c.Time()
			if !t.Equal(last) {
				if err := rew.To(t.UnixMilli()); err != nil {
					return fail(err)
				}
				env.SetNow(t)
				last = t
			}
			for _, id := range c.Expect {
				if !p.history.Existed(id, t.UnixMilli()) {
					o.NotYet = append(o.NotYet, id)
				}
			}
		}
		// 세션 시작: 세션의 첫 사례 앞에 한 번, 그리고 시작 사례(재개·압축 뒤 등)마다 다시. 다시 부른 시작이 그 뒤 사례의
		// A층이다. 압축·지우기 뒤 시작은 훅처럼 세션 표시를 먼저 비운다.
		if k := c.CaseKind(); k != KindDuplicate {
			a, ok := started[key]
			if !ok || k == KindStart {
				if c.Resets() {
					r, can := env.Recaller.(Resetter)
					if !can {
						return fail(fmt.Errorf("a %s start needs a recall path that can reset a session", c.Source))
					}
					if err := r.Reset(key); err != nil {
						return fail(err)
					}
				}
				s, err := env.Recaller.Start(key, cwd)
				if err != nil {
					return fail(err)
				}
				s.IDs = nonNil(s.IDs)
				a = &s
				started[key] = a
				o.Started = a
				if !ok {
					run.Sessions = append(run.Sessions, SessionResult{Session: key, Seen: seen(s)})
				}
			}
			o.Start = *a
		}
		switch c.CaseKind() {
		case KindRequest:
			if c.Prompt != "" {
				s, err := env.Recaller.Request(key, cwd, c.Prompt)
				if err != nil {
					return fail(err)
				}
				s.IDs = nonNil(s.IDs)
				o.B1 = &s
				if len(c.Expect) > 0 && env.Finder != nil {
					if o.Found, err = env.Finder.Find(cwd, c.Prompt, FindLimit); err != nil {
						return fail(err)
					}
					o.Found, o.Found8 = nonNil(o.Found), true
				}
			}
			if c.Tool != "" {
				s, err := env.Recaller.Tool(key, cwd, c.Tool, c.Input)
				if err != nil {
					return fail(err)
				}
				s.IDs = nonNil(s.IDs)
				o.B2 = &s
			}
		case KindSubagent:
			s, err := env.Recaller.Subagent(key, cwd, c.Task)
			if err != nil {
				return fail(err)
			}
			s.IDs = nonNil(s.IDs)
			o.Sub = &s
		case KindDuplicate:
			if env.Similar == nil {
				return fail(errors.New("the recall path has no Similar for a duplicate case"))
			}
			meets, err := env.Similar.Similar(cwd, *c.Save)
			if err != nil {
				return fail(err)
			}
			o.Similar, o.Near = nonNil(meets.Block), nonNil(meets.Near)
		}
		outs = append(outs, o)
		run.Cases = append(run.Cases, caseResult(o))
	}
	run.Train, run.Test, run.All = Compute(filter(outs, PartTrain)), Compute(filter(outs, PartTest)), Compute(outs)
	return run, nil
}

func filter(outs []Outcome, part string) []Outcome {
	var out []Outcome
	for _, o := range outs {
		if o.Part == part {
			out = append(out, o)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
