package main

import (
	"fmt"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/place"
	"github.com/onidot-labs/orbit-mem/internal/project"
)

// productsOf is the product map of this PC's configuration (설계 · orbit-mem 2판 4.1). A configuration
// that does not load has no products: memories saved then default to product:? (this folder only).
func productsOf() place.Map {
	cfg, err := loadConfig()
	if err != nil {
		return nil
	}
	return cfg.Products
}

// cmdPlace shows where a folder stands in the product map: its scope key, its products and whether it is a
// folder of several products, so a person can check the map and fix a wrong line (S3 명세 2.2).
func cmdPlace(a *app, args []string) error {
	fs := a.flags("place")
	cwd := fs.String("cwd", "", i18n.T("flag.place.cwd"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(paths().Config())
	if err != nil {
		return err
	}
	p := sessionProject()
	if *cwd != "" {
		p = project.Resolve(*cwd)
	}
	pl := place.Resolve(p.Scope, cfg.Products)
	def := place.ScopeUnknown
	if pr := pl.Product(); pr != "" {
		def = "product:" + pr
	}
	if *asJSON {
		products := pl.Products
		if products == nil {
			products = []string{}
		}
		return a.json(map[string]any{"scope": p.Scope, "label": p.Label, "products": products, "multi": pl.Multi(), "defaultAppliesTo": def})
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.place.folder", p.Label, p.Scope))
	switch {
	case pl.Multi():
		fmt.Fprintln(a.stdout, i18n.T("cli.place.multi", strings.Join(pl.Products, ", ")))
	case len(pl.Products) == 1:
		fmt.Fprintln(a.stdout, i18n.T("cli.place.one", pl.Products[0]))
	default:
		fmt.Fprintln(a.stdout, i18n.T("cli.place.none"))
		if !strings.HasPrefix(p.Label, "/") && !strings.Contains(p.Label, ":") && p.Label != "" {
			// 원격이 있는 저장소는 폴더 줄로 찾지 않는다(S3 명세 2.2): 그 원격 키를 적으라고 알린다.
			fmt.Fprintln(a.stdout, i18n.T("cli.place.remote_hint", p.Label))
		}
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.place.default", def))
	return nil
}
