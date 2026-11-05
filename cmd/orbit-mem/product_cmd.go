package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/i18n"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
)

// Test seams around writing the product map inside the memory transaction: before the write (another
// writer changes config.json) and after it (a failure before the memory commit).
var (
	productRenameBeforeWrite = func() {}
	productRenameAfterWrite  = func() error { return nil }
)

// productRenameResult is product rename --json.
type productRenameResult struct {
	memory.ProductRename
	MemoryBackup string `json:"memoryBackup,omitempty"`
	ConfigBackup string `json:"configBackup,omitempty"`
}

// cmdProduct is orbit-mem product rename <old> <new>: it renames a key of the product map (config.json
// products) and moves the memories of that product (product:<old>) to the new name together.
//
// Order: check (old key present, new key absent, no memory at product:<new>) → copy the memory DB and
// config.json into backups → in one memory transaction move the memories, write config.json (only if it
// is still the bytes read at the start) and commit. A failure after the write puts the old bytes back.
func cmdProduct(a *app, args []string) error {
	fs := a.flags("product")
	dry := fs.Bool("dry-run", false, i18n.T("flag.product.dry_run"))
	asJSON := fs.Bool("json", false, i18n.T("flag.json"))
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 3 || pos[0] != "rename" || !place.ValidName(pos[1]) || !place.ValidName(pos[2]) || pos[1] == pos[2] {
		return errUsage
	}
	from, to := pos[1], pos[2]
	p := paths()
	orig, err := os.ReadFile(p.Config())
	if errors.Is(err, os.ErrNotExist) {
		return i18n.Errorf("cli.product.err.no_config", p.Config())
	}
	if err != nil {
		return err
	}
	next, err := renamedConfig(orig, from, to)
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	// 미리 보기를 먼저 돌려 거절할 일(새 자리에 기억이 있음)을 사본을 만들기 전에 거른다. 실제 실행은 같은 검사를
	// 트랜잭션 안에서 다시 한다(그사이 다른 프로세스가 저장했을 수 있다).
	res := productRenameResult{}
	if res.ProductRename, err = s.RenameProduct(from, to, true, nil); err != nil || *dry {
		if err != nil {
			return err
		}
		return printProductRename(a, res, *asJSON)
	}
	if err := os.MkdirAll(p.Backups(), 0o700); err != nil {
		return err
	}
	if res.MemoryBackup, err = s.Snapshot(p.Backups(), "memory-before-product-rename"); err != nil {
		return err
	}
	if res.ConfigBackup, err = keepConfigCopy(p.Backups(), orig, time.Now()); err != nil {
		return err
	}
	wrote := false
	res.ProductRename, err = s.RenameProduct(from, to, false, func() error {
		productRenameBeforeWrite()
		now, err := os.ReadFile(p.Config())
		if err != nil {
			return err
		}
		if !bytes.Equal(now, orig) {
			return i18n.Errorf("cli.product.err.config_changed", p.Config())
		}
		if err := writeFileAtomic(p.Config(), next); err != nil {
			return err
		}
		wrote = true
		return productRenameAfterWrite()
	})
	if err != nil {
		if wrote {
			if rerr := writeFileAtomic(p.Config(), orig); rerr != nil {
				return fmt.Errorf("%w; %s", err, i18n.T("cli.product.err.restore", p.Config(), res.ConfigBackup, rerr))
			}
		}
		return err
	}
	return printProductRename(a, res, *asJSON)
}

// renamedConfig is config.json with products.<from> renamed to products.<to>. Every other key, known or
// not, keeps its value; the file is written back in the form config.Save writes (sorted keys, two spaces).
func renamedConfig(orig []byte, from, to string) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(orig, &top); err != nil {
		return nil, err
	}
	var products map[string]json.RawMessage
	if raw, ok := top["products"]; ok {
		if err := json.Unmarshal(raw, &products); err != nil {
			return nil, err
		}
	}
	if _, ok := products[from]; !ok {
		return nil, i18n.Errorf("cli.product.err.no_old", from)
	}
	if _, ok := products[to]; ok {
		return nil, i18n.Errorf("cli.product.err.new_taken", to)
	}
	products[to] = products[from]
	delete(products, from)
	b, err := json.Marshal(products)
	if err != nil {
		return nil, err
	}
	top["products"] = b
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, err
	}
	out = append(out, '\n')
	// 쓰기 전에 orbit-mem이 읽을 수 있는 설정인지 본다(제품 지도 검사 포함).
	c := config.Default()
	if err := json.Unmarshal(out, &c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

// keepConfigCopy writes the config bytes to backups/config-before-product-rename-<time>.json, never over
// an existing file.
func keepConfigCopy(dir string, b []byte, now time.Time) (string, error) {
	base := "config-before-product-rename-" + now.Format("20060102T150405")
	for i := 1; ; i++ {
		name := base + ".json"
		if i > 1 {
			name = fmt.Sprintf("%s-%d.json", base, i)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(b); err != nil {
			f.Close()
			os.Remove(path)
			return "", err
		}
		if err := f.Close(); err != nil {
			os.Remove(path)
			return "", err
		}
		return path, nil
	}
}

// writeFileAtomic writes b to a temporary file next to path and renames it over path (0600).
func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func printProductRename(a *app, res productRenameResult, asJSON bool) error {
	if asJSON {
		return a.json(res)
	}
	head := i18n.T("cli.product.renamed", res.From, res.To, len(res.Memories))
	if res.DryRun {
		head = i18n.T("cli.product.would_rename", res.From, res.To, len(res.Memories))
	}
	fmt.Fprintln(a.stdout, head)
	for _, m := range res.Memories {
		pin := ""
		if m.Pinned {
			pin = " · pinned"
		}
		fmt.Fprintf(a.stdout, "- %s · %s (%s%s)\n", m.ID, m.Title, m.State, pin)
	}
	if res.MemoryBackup != "" {
		fmt.Fprintln(a.stdout, i18n.T("cli.product.backups", res.MemoryBackup, res.ConfigBackup))
	}
	return nil
}
