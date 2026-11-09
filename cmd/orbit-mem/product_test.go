package main

// orbit-mem product rename <옛> <새>: 제품 지도의 키와 그 제품에 묶인 기억(product:<옛>)을 함께 옮긴다.
// 시작 전에 기억 DB 사본과 설정 사본을 남기고, 옛 키가 없거나 새 키가 이미 있으면 거절하며, --dry-run은 아무것도 바꾸지 않는다.
// 모든 시험은 임시 ORBIT_HOME에서만 돈다.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/memory"
)

// productConfig is a hand-written config.json with a product map and a key orbit-mem does not know.
const productConfig = `{
  "inject": "full",
  "products": {
    "keep": {"match": ["github.com/me/keep"]},
    "old": {"match": ["github.com/me/old", "~/work/old"], "names": ["Old app"]}
  },
  "work": {"keepMe": true}
}
`

type renameFixture struct {
	home, config string
	core, plain  memory.Memory
	other        memory.Memory
}

func setupRename(t *testing.T) renameFixture {
	t.Helper()
	h := home(t)
	f := renameFixture{home: h, config: filepath.Join(h, "config.json")}
	if err := os.WriteFile(f.config, []byte(productConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := memory.Open(filepath.Join(h, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	save := func(title, scope string) memory.Memory {
		m, err := s.Save(memory.SaveInput{Kind: memory.KindDecision, Title: title, Scope: scope, Topic: title,
			Source: memory.Source{Kind: memory.SourceQuote, Excerpt: title}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	f.core, f.plain, f.other = save("핵심 결정", "product:old"), save("보통 결정", "product:old"), save("다른 결정", "product:keep")
	if f.core, err = s.Pin(f.core.ID, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f renameFixture) scopes(t *testing.T) map[string]string {
	t.Helper()
	s, err := memory.Open(filepath.Join(f.home, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out := map[string]string{}
	for _, m := range []memory.Memory{f.core, f.plain, f.other} {
		got, err := s.Get(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[m.ID] = got.Scope
	}
	return out
}

func (f renameFixture) backups(t *testing.T) []string {
	t.Helper()
	es, _ := os.ReadDir(filepath.Join(f.home, "backups"))
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestProductRename_설정_키와_기억을_함께_옮기고_사본을_남긴다(t *testing.T) {
	f := setupRename(t)
	out, errOut, code := run(t, "", "product", "rename", "old", "new", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	var res struct {
		From, To     string
		DryRun       bool
		Memories     []struct{ ID string }
		MemoryBackup string `json:"memoryBackup"`
		ConfigBackup string `json:"configBackup"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res.From != "product:old" || res.To != "product:new" || res.DryRun || len(res.Memories) != 2 {
		t.Fatalf("result = %s", out)
	}
	// 설정: 키만 바뀌고 match·names와 다른 키(모르는 키 포함)는 그대로다.
	var cfg map[string]json.RawMessage
	b, _ := os.ReadFile(f.config)
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	var products map[string]struct {
		Match []string `json:"match"`
		Names []string `json:"names"`
	}
	json.Unmarshal(cfg["products"], &products)
	if _, ok := products["old"]; ok || strings.Join(products["new"].Match, ",") != "github.com/me/old,~/work/old" ||
		strings.Join(products["new"].Names, ",") != "Old app" || len(products["keep"].Match) != 1 {
		t.Fatalf("products = %s", cfg["products"])
	}
	if string(cfg["inject"]) != `"full"` || !strings.Contains(string(cfg["work"]), "keepMe") {
		t.Fatalf("other keys changed: %s", b)
	}
	// 기억: 옛 제품 것만 옮겨 갔고 승격은 그대로다.
	sc := f.scopes(t)
	if sc[f.core.ID] != "product:new" || sc[f.plain.ID] != "product:new" || sc[f.other.ID] != "product:keep" {
		t.Fatalf("scopes = %v", sc)
	}
	if out, _, code := run(t, "", "place", "--cwd", f.home, "--json"); code != 0 || out == "" {
		t.Fatalf("config no longer loads: %d %s", code, out)
	}
	// 사본: 설정 사본은 바꾸기 전 바이트 그대로, 기억 사본은 옛 자리를 담는다.
	if res.ConfigBackup == "" || res.MemoryBackup == "" || filepath.Dir(res.ConfigBackup) != filepath.Join(f.home, "backups") {
		t.Fatalf("backups = %q %q", res.ConfigBackup, res.MemoryBackup)
	}
	if b, _ := os.ReadFile(res.ConfigBackup); string(b) != productConfig {
		t.Fatalf("config backup = %s", b)
	}
	snap, err := memory.Open(res.MemoryBackup)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if m, err := snap.Get(f.core.ID); err != nil || m.Scope != "product:old" || !m.Pinned {
		t.Fatalf("memory backup = %+v %v", m, err)
	}
	// 같은 일을 다시 하면 옛 키가 없어 거절한다.
	if _, _, code := run(t, "", "product", "rename", "old", "new"); code != 1 {
		t.Fatalf("second rename exit %d", code)
	}
}

func TestProductRename_미리_보기는_아무것도_바꾸지_않고_사본도_만들지_않는다(t *testing.T) {
	f := setupRename(t)
	before := f.scopes(t)
	out, errOut, code := run(t, "", "product", "rename", "old", "new", "--dry-run")
	if code != 0 || !strings.Contains(out, f.core.ID) || !strings.Contains(out, f.plain.ID) {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if b, _ := os.ReadFile(f.config); string(b) != productConfig {
		t.Fatalf("config changed: %s", b)
	}
	if after := f.scopes(t); after[f.core.ID] != before[f.core.ID] || after[f.plain.ID] != before[f.plain.ID] {
		t.Fatalf("scopes changed: %v", after)
	}
	if bs := f.backups(t); len(bs) != 0 {
		t.Fatalf("dry run made backups: %v", bs)
	}
}

func TestProductRename_옛_키가_없거나_새_키가_있거나_새_자리에_기억이_있으면_거절한다(t *testing.T) {
	cases := []struct {
		name string
		args []string
		prep func(t *testing.T, f renameFixture)
	}{
		{"옛 키 없음", []string{"missing", "new"}, nil},
		{"새 키 있음", []string{"old", "keep"}, nil},
		{"설정 파일 없음", []string{"old", "new"}, func(t *testing.T, f renameFixture) { os.Remove(f.config) }},
		{"새 자리에 기억", []string{"old", "new"}, func(t *testing.T, f renameFixture) {
			s, err := memory.Open(filepath.Join(f.home, "memory.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := s.Save(memory.SaveInput{Kind: memory.KindFact, Title: "이미 새 자리", Scope: "product:new", Source: memory.Source{Kind: memory.SourceSession, Ref: "x"}}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setupRename(t)
			if c.prep != nil {
				c.prep(t, f)
			}
			cfgBefore, _ := os.ReadFile(f.config)
			before := f.scopes(t)
			if _, errOut, code := run(t, "", append([]string{"product", "rename"}, c.args...)...); code != 1 || errOut == "" {
				t.Fatalf("exit %d: %s", code, errOut)
			}
			if b, _ := os.ReadFile(f.config); string(b) != string(cfgBefore) {
				t.Fatalf("config changed: %s", b)
			}
			if after := f.scopes(t); after[f.core.ID] != before[f.core.ID] || after[f.plain.ID] != before[f.plain.ID] {
				t.Fatalf("scopes changed: %v", after)
			}
			if bs := f.backups(t); len(bs) != 0 {
				t.Fatalf("refused rename made backups: %v", bs)
			}
		})
	}
	for _, args := range [][]string{{"product"}, {"product", "rename", "old"}, {"product", "move", "old", "new"}, {"product", "rename", "old", "Bad Name"}, {"product", "rename", "old", "old"}} {
		setupRename(t)
		if _, _, code := run(t, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// 설정을 쓴 뒤 기억 DB를 확정하기 전에 실패하면 설정을 원래 바이트로 되돌리고 기억도 옮기지 않는다.
func TestProductRename_설정을_쓴_뒤_실패하면_설정을_되돌린다(t *testing.T) {
	f := setupRename(t)
	prev := productRenameAfterWrite
	productRenameAfterWrite = func() error { return errors.New("injected failure") }
	t.Cleanup(func() { productRenameAfterWrite = prev })
	if _, errOut, code := run(t, "", "product", "rename", "old", "new"); code != 1 || !strings.Contains(errOut, "injected failure") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(f.config); string(b) != productConfig {
		t.Fatalf("config not restored: %s", b)
	}
	if sc := f.scopes(t); sc[f.core.ID] != "product:old" || sc[f.plain.ID] != "product:old" {
		t.Fatalf("scopes = %v", sc)
	}
}

// 사람이나 다른 명령이 그사이 설정을 바꿨으면 덮어쓰지 않고 멈추며 기억도 옮기지 않는다.
func TestProductRename_그사이_설정이_바뀌면_덮어쓰지_않는다(t *testing.T) {
	f := setupRename(t)
	changed := strings.Replace(productConfig, `"full"`, `"rules"`, 1)
	prev := productRenameBeforeWrite
	productRenameBeforeWrite = func() { os.WriteFile(f.config, []byte(changed), 0o600) }
	t.Cleanup(func() { productRenameBeforeWrite = prev })
	if _, errOut, code := run(t, "", "product", "rename", "old", "new"); code != 1 || errOut == "" {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(f.config); string(b) != changed {
		t.Fatalf("config overwritten: %s", b)
	}
	if sc := f.scopes(t); sc[f.core.ID] != "product:old" {
		t.Fatalf("scopes = %v", sc)
	}
}
