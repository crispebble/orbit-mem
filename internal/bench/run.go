package bench

import (
	"errors"
	"fmt"
	"os"
)

// Run runs every recall path over the set, each on its own fresh copy of the DB so the
// shown marks and the activity of one never reach another. Cases run in file order and a
// session starts (layer A) before its first case.
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
	started := map[string]*Shown{}
	outs := make([]Outcome, 0, len(set.Cases))
	for i, c := range set.Cases {
		fail := func(err error) (RunReport, error) { return run, fmt.Errorf("%s: case %q: %w", name, c.ID, err) }
		key, cwd := c.SessionKey(), set.CWDOf(c)
		o := Outcome{Case: c, Key: key, Part: parts[i]}
		if k := c.CaseKind(); k != KindDuplicate {
			a, ok := started[key]
			if !ok {
				s, err := env.Recaller.Start(key, cwd)
				if err != nil {
					return fail(err)
				}
				s.IDs = nonNil(s.IDs)
				a = &s
				started[key] = a
				run.Sessions = append(run.Sessions, SessionResult{Session: key, Seen: seen(s)})
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
				s, err := env.Recaller.Tool(key, cwd, c.Tool)
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
			if o.Similar, err = env.Similar.Similar(cwd, *c.Save); err != nil {
				return fail(err)
			}
			o.Similar = nonNil(o.Similar)
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
