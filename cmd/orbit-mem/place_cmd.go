package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
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
	suggest := fs.Bool("suggest", false, i18n.T("flag.place.suggest"))
	root := fs.String("root", "", i18n.T("flag.place.root"))
	move := fs.Bool("move", false, i18n.T("flag.place.move"))
	plan := fs.String("plan", "", i18n.T("flag.place.plan"))
	dry := fs.Bool("dry-run", false, i18n.T("flag.place.dry_run"))
	journals := fs.Bool("journals", false, i18n.T("flag.place.journals"))
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(paths().Config())
	if err != nil {
		return err
	}
	switch {
	case *suggest:
		return placeSuggest(a, *root, *asJSON)
	case *move:
		return placeMove(a, cfg, *asJSON)
	case *plan != "":
		return placePlan(a, cfg, *plan, *dry, *asJSON)
	case *journals:
		return placeJournals(a, *dry, *asJSON)
	}
	p := sessionProject()
	if *cwd != "" {
		p = project.Resolve(*cwd)
	}
	pl := place.At(p.Scope, p.Dir, cfg.Products)
	def := place.AppliesTo(pl.Default(p.Git, p.Temp, ""))
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

// placeSuggest prints a first product map from this PC's repositories and work folders (S3-R5). It does
// not write the configuration: a person puts it into config.json products and fixes the wrong lines.
func placeSuggest(a *app, root string, asJSON bool) error {
	home, _ := os.UserHomeDir()
	if root == "" {
		root = filepath.Join(home, "_workspace")
	}
	if _, err := os.Stat(root); err != nil {
		return err
	}
	remote := func(dir string) string {
		if p := project.Resolve(dir); p.Git && !strings.HasPrefix(p.Label, "/") && !filepath.IsAbs(p.Label) {
			return p.Label
		}
		return ""
	}
	m := place.Suggest([]string{root}, remote, filepath.ToSlash(home))
	if asJSON {
		return a.json(map[string]any{"products": m})
	}
	b, err := json.MarshalIndent(map[string]any{"products": m}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.place.suggest_head", root, len(m)))
	fmt.Fprintln(a.stdout, string(b))
	fmt.Fprintln(a.stdout, i18n.T("cli.place.suggest_next"))
	return nil
}

// placeMove moves the product:? memories the product map now places (S3-R5), as automatic changes the
// audit list shows and undoes.
func placeMove(a *app, cfg config.Config, asJSON bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	moved, err := s.MoveUnknown(cfg.Products)
	if asJSON {
		if moved == nil {
			moved = []memory.Moved{}
		}
		if jerr := a.json(moved); jerr != nil {
			return jerr
		}
		return err
	}
	if len(moved) == 0 {
		fmt.Fprintln(a.stdout, i18n.T("cli.place.move_none"))
	}
	for _, mv := range moved {
		fmt.Fprintln(a.stdout, i18n.T("cli.place.moved", mv.ID, mv.Title, mv.Folder, mv.To, mv.LedgerID))
	}
	return err
}

// placePlan applies (or, with --dry-run, previews) a migration plan an AI wrote (S4, 설계 · orbit-mem 2판 11절):
// each change is an automatic change the audit list shows and undoes.
func placePlan(a *app, cfg config.Config, file string, dry, asJSON bool) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var plan memory.Plan
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields() // 칸 이름 오타(topik 등)가 조용히 무시되지 않게
	if err := dec.Decode(&plan); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF { // 계획 하나 뒤에 다른 값이 붙어 있으면 거절한다
		return fmt.Errorf("%s: trailing data after the plan", file)
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	rep, err := s.ApplyPlan(plan, cfg.Products, dry)
	if asJSON {
		if jerr := a.json(rep); jerr != nil {
			return jerr
		}
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.place.plan_head", len(plan.Items), len(rep.Applied), len(rep.Skipped), len(rep.Conflicts), dry))
	if line := skipSummary(append(append([]memory.PlanResult{}, rep.Skipped...), rep.Conflicts...)); line != "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.place.plan_skips", line))
	}
	for _, r := range rep.Conflicts {
		fmt.Fprintln(a.stdout, i18n.T("cli.place.plan_conflict", r.ID, r.Skip))
	}
	return err
}

// placeJournals moves every live journal memory to the activity log (S4-8), one ledger line each.
func placeJournals(a *app, dry, asJSON bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	rep, err := s.JournalsToActivity(dry)
	if asJSON {
		if jerr := a.json(rep); jerr != nil {
			return jerr
		}
		return err
	}
	fmt.Fprintln(a.stdout, i18n.T("cli.place.journals_head", len(rep.Applied), len(rep.Skipped), dry))
	for _, r := range rep.Skipped {
		fmt.Fprintln(a.stdout, i18n.T("cli.place.plan_conflict", r.ID, r.Skip))
	}
	return err
}

// skipSummary counts the skip reasons by their head (the part before ":" or " by "): "already there 3, changed 1".
func skipSummary(rs []memory.PlanResult) string {
	n := map[string]int{}
	for _, r := range rs {
		head, _, _ := strings.Cut(r.Skip, ":")
		head, _, _ = strings.Cut(head, " by ")
		n[strings.TrimSpace(head)]++
	}
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return n[keys[i]] > n[keys[j]] || n[keys[i]] == n[keys[j]] && keys[i] < keys[j] })
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, n[k])
	}
	return strings.Join(parts, ", ")
}
