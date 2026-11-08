package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/config"
	"github.com/onidot-labs/orbit-mem/internal/memory"
)

// fakeTidy records the background tidy runs the session-start hook asks for.
func fakeTidy(t *testing.T) *[][]string {
	t.Helper()
	var mu sync.Mutex
	calls := &[][]string{}
	old := startBackground
	startBackground = func(args ...string) error {
		if len(args) > 0 && args[0] == "tidy" {
			mu.Lock()
			defer mu.Unlock()
			*calls = append(*calls, args)
		}
		return nil
	}
	t.Cleanup(func() { startBackground = old })
	return calls
}

// 기획 · orbit-mem 4.3 H1: 세션 시작 훅은 그날 정리가 아직이면 뒤에서(별도 프로세스) 정리를 띄우고, 문맥은 그대로 낸다.
func TestHook_세션_시작은_그날_정리가_아직이면_뒤에서_띄운다(t *testing.T) {
	home(t)
	seedRule(t)
	calls := fakeTidy(t)
	out, _, code := run(t, startInput, "hook", "session-start", "--host", "claude")
	if code != 0 || !strings.Contains(hookContext(t, out), "답은 존댓말") {
		t.Fatalf("code %d out %q", code, out)
	}
	if fmt.Sprint(*calls) != "[[tidy auto]]" {
		t.Fatalf("background = %v", *calls)
	}
}

// 끝난 기준 4: 같은 날 두 번째 세션에서는 정리가 돌지 않는다.
func TestHook_같은_날_두_번째_세션에서는_정리를_띄우지_않는다(t *testing.T) {
	h := home(t)
	seedRule(t)
	calls := fakeTidy(t)
	run(t, startInput, "hook", "session-start", "--host", "claude")
	// 첫 세션이 띄운 뒤 프로세스: 그날을 먼저 표시하고 정리한다.
	if _, errOut, code := run(t, "", "tidy", "auto"); code != 0 {
		t.Fatalf("tidy auto: %d %s", code, errOut)
	}
	run(t, strings.Replace(startInput, "s-1", "s-2", 1), "hook", "session-start", "--host", "codex")
	if len(*calls) != 1 {
		t.Fatalf("background = %v", *calls)
	}
	log, _ := os.ReadFile(filepath.Join(h, "logs", "tidy.log"))
	if strings.Count(string(log), "tidy auto: ran") != 1 {
		t.Fatalf("tidy.log = %q", log)
	}
	// 두 세션이 함께 띄웠어도 두 번째 뒤 프로세스는 아무것도 하지 않는다.
	if _, _, code := run(t, "", "tidy", "auto"); code != 0 {
		t.Fatal(code)
	}
	log, _ = os.ReadFile(filepath.Join(h, "logs", "tidy.log"))
	if strings.Count(string(log), "tidy auto: ran") != 1 {
		t.Fatalf("두 번째 뒤 프로세스가 돌았다: %q", log)
	}
}

func TestHook_다음_날에는_다시_띄운다(t *testing.T) {
	home(t)
	s := seedRule(t)
	calls := fakeTidy(t)
	yesterday := memory.Day(time.Now().AddDate(0, 0, -1))
	if err := s.SetMeta(memory.MetaTidyDay, yesterday); err != nil {
		t.Fatal(err)
	}
	run(t, startInput, "hook", "session-start", "--host", "claude")
	if len(*calls) != 1 {
		t.Fatalf("background = %v", *calls)
	}
}

// 기획 · orbit-mem 4.3 H4: 설정 autoTidy(기본 켜짐)로 끈다.
func TestHook_autoTidy를_끄면_정리를_띄우지도_돌리지도_않는다(t *testing.T) {
	h := home(t)
	seedRule(t)
	calls := fakeTidy(t)
	if cfg, err := loadConfig(); err != nil || !cfg.AutoTidy {
		t.Fatalf("기본 autoTidy = %v %v", cfg.AutoTidy, err)
	}
	if _, errOut, code := run(t, "", "config", "set", "autoTidy", "false"); code != 0 {
		t.Fatalf("config set: %d %s", code, errOut)
	}
	run(t, startInput, "hook", "session-start", "--host", "claude")
	if len(*calls) != 0 {
		t.Fatalf("background = %v", *calls)
	}
	if _, _, code := run(t, "", "tidy", "auto"); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(h, "logs", "tidy.log")); err == nil {
		t.Fatal("꺼져 있는데 정리가 돌았다")
	}
	if _, _, code := run(t, "", "config", "set", "autoTidy", "true"); code != 0 {
		t.Fatal(code)
	}
	run(t, startInput, "hook", "session-start", "--host", "claude")
	if len(*calls) != 1 {
		t.Fatalf("켠 뒤 background = %v", *calls)
	}
}

// 끝난 기준 4: inject가 full이 아니면 후보 기한 낡음이 돌지 않는다.
func TestTidy_inject가_full이_아니면_후보_기한_낡음을_넣지_않는다(t *testing.T) {
	for inj, want := range map[string]bool{config.InjectFull: true, config.InjectRules: false, config.InjectOff: false} {
		cfg := config.Default()
		cfg.Inject = inj
		if got := tidyOptions(cfg).ExpireCandidates; got != want {
			t.Fatalf("inject %s: ExpireCandidates = %v", inj, got)
		}
	}
	cfg := config.Default()
	cfg.CandidateExpiryDays, cfg.LedgerWindowDays = 10, 3
	if o := tidyOptions(cfg); o.CandidateExpiryDays != 10 || o.LedgerWindowDays != 3 {
		t.Fatalf("options = %+v", o)
	}
}

// 사람이 부르는 orbit-mem tidy는 날과 관계없이 지금 정리하고, 그날 표시도 남긴다.
func TestTidy_사람이_부르면_지금_정리하고_결과를_낸다(t *testing.T) {
	home(t)
	s := seed(t)
	out, errOut, code := run(t, "", "tidy", "--json")
	if code != 0 || !strings.Contains(out, `"expired"`) {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if s.Meta(memory.MetaTidyDay) != memory.Day(time.Now()) {
		t.Fatalf("meta = %q", s.Meta(memory.MetaTidyDay))
	}
	if out, _, code := run(t, "", "tidy"); code != 0 || out == "" {
		t.Fatalf("두 번째: code %d out %q", code, out)
	}
}

// 설치 전(기억 DB 없음)에는 뒤 프로세스가 DB를 만들지 않는다.
func TestTidy_auto는_설치_전에는_DB를_만들지_않는다(t *testing.T) {
	h := home(t)
	if _, _, code := run(t, "", "tidy", "auto"); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(h, "memory.db")); err == nil {
		t.Fatal("DB를 만들었다")
	}
}

// 끝난 기준 5(기획 · orbit-mem R5): 옛 회고 키가 남은 config.json으로 doctor 문제가 0이다. 키는 그대로 남고,
// 범위 밖이던 옛 값(retroHour 99 같은)도 읽기를 막지 않는다.
func TestDoctor_옛_회고_키가_남은_설정으로_문제가_없다(t *testing.T) {
	f := newFakePC(t) // claude·codex가 PATH에 없다
	os.WriteFile(filepath.Join(f.bin, "orbit-mem"), []byte("#!/bin/sh\n"), 0o755)
	seed(t)
	old := `{"language":"en","inject":"full","retroHost":"claude","retroModel":"","retroHour":99,"sessionRetro":true,"sessionRetroModel":"haiku",
"sessionRetroDailyMax":3,"graphTidyMemories":40,"graphTidyUntagged":20,"graphTidyProposals":20,"legacyMemories":30,"legacyBatches":1,
"claudePath":"/usr/local/bin/claude","codexPath":"","applyTargets":["/x/source"],"pluginSource":"/x/source","retiredNames":["old"],
"work":"on","workStaleAfter":"72h"}`
	cfgPath := filepath.Join(f.home, "config.json")
	if err := os.WriteFile(cfgPath, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "", "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor %d %s", code, errOut)
	}
	var rep struct {
		Problems []problem  `json:"problems"`
		Tidy     tidyStatus `json:"tidy"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(rep.Problems) != 0 || !rep.Tidy.Auto {
		t.Fatalf("problems = %+v tidy = %+v", rep.Problems, rep.Tidy)
	}
	// 설정을 바꿔 저장해도 옛 키는 지우지 않는다.
	if _, _, code := run(t, "", "config", "set", "autoTidy", "false"); code != 0 {
		t.Fatal(code)
	}
	b, _ := os.ReadFile(cfgPath)
	for _, k := range []string{`"retroHost"`, `"pluginSource"`, `"work"`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("%s가 사라졌다: %s", k, b)
		}
	}
}
