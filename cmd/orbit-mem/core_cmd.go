package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
)

// cmdCore suggests the first core of each layer and pins the memories a person keeps (S3 명세 2.7c). The
// suggestion changes nothing; --pin goes through the store's pin (layer limit, the user's-words guard)
// and the version history keeps it.
func cmdCore(a *app, args []string) error {
	fs := a.flags("core")
	suggest := fs.Bool("suggest", false, i18n.T("flag.core.suggest"))
	scope := fs.String("scope", "", i18n.T("flag.core.scope"))
	pin := fs.String("pin", "", i18n.T("flag.core.pin"))
	quote := fs.String("quote", "", i18n.T("flag.core.quote"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	if *pin != "" {
		return corePin(a, s, *pin, *quote, *asJSON)
	}
	if !*suggest {
		return errUsage
	}
	layers := []string{memory.ScopeGlobal}
	if *scope != "" {
		sc, ok := place.ScopeOf(*scope)
		if !ok {
			return errUsage
		}
		layers = []string{sc}
	} else {
		// 제품 지도의 제품과, 지도에 없어도 확정 기억이 있는 제품 층.
		seen := map[string]bool{}
		for name := range cfg.Products {
			seen["product:"+name] = true
		}
		if ms, err := s.List(memory.StateConfirmed); err == nil {
			for _, m := range ms {
				if place.ProductOf(m.Scope) != "" {
					seen[m.Scope] = true
				}
			}
		}
		var products []string
		for sc := range seen {
			products = append(products, sc)
		}
		sort.Strings(products)
		layers = append(layers, products...)
	}
	var out []memory.CoreSuggestion
	for _, sc := range layers {
		sg, err := s.SuggestCore(sc)
		if err != nil {
			return err
		}
		if sc != memory.ScopeGlobal && len(sg.Picks) == 0 && len(sg.Pinned) == 0 && *scope == "" {
			continue
		}
		out = append(out, sg)
	}
	if *asJSON {
		return a.json(map[string]any{"layers": out})
	}
	for _, sg := range out {
		fmt.Fprintln(a.stdout, i18n.T("cli.core.layer", sg.AppliesTo, sg.Total, sg.Limit, len(sg.Pinned), len(sg.Picks), sg.Considered))
		for _, c := range sg.Pinned {
			fmt.Fprintln(a.stdout, i18n.T("cli.core.pinned", c.ID, c.Title, c.Runes))
		}
		var ids []string
		for _, c := range sg.Picks {
			fmt.Fprintln(a.stdout, i18n.T("cli.core.pick", c.ID, c.Title, c.Runes, c.Score, why(c)))
			ids = append(ids, c.ID)
		}
		if len(ids) > 0 {
			fmt.Fprintln(a.stdout, i18n.T("cli.core.apply", strings.Join(ids, ",")))
		}
	}
	return nil
}

// why says what a pick's score is made of.
func why(c memory.CoreCandidate) string {
	var parts []string
	if c.Corrections > 0 {
		parts = append(parts, i18n.T("cli.core.why_corrections", c.Corrections))
	}
	if c.Violated > 0 {
		parts = append(parts, i18n.T("cli.core.why_violated", c.Violated))
	}
	if c.Followed > 0 {
		parts = append(parts, i18n.T("cli.core.why_followed", c.Followed))
	}
	if c.Quote {
		parts = append(parts, i18n.T("cli.core.why_quote"))
	}
	return strings.Join(parts, "·")
}

// corePin pins the listed memories one by one and says what each became.
func corePin(a *app, s *memory.Store, list, quote string, asJSON bool) error {
	who := memory.Who{Actor: actorOf("", memory.ActorUser), UserQuote: strings.TrimSpace(quote)}
	type result struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
		Error  string `json:"error,omitempty"`
	}
	var res []result
	var errs []error
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		m, err := s.Pin(id, who)
		r := result{ID: id, Pinned: err == nil && m.Pinned}
		if err != nil {
			r.Error = err.Error()
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
		res = append(res, r)
	}
	if asJSON {
		if err := a.json(res); err != nil {
			return err
		}
	} else {
		for _, r := range res {
			if r.Pinned {
				fmt.Fprintln(a.stdout, i18n.T("cli.core.pin_ok", r.ID))
			}
		}
	}
	return errors.Join(errs...)
}
