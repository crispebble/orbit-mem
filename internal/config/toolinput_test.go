package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 도구 입력 문(명세 T-1): 기본은 꺼져 있고(켤지와 문턱은 평가로 정한다, T-3) 문턱의 출발점은 0.3이다. toolInputMinScore는
// 0 < x ≤ 1만 받고, 두 키가 없는 옛 설정 파일은 기본값으로 읽는다.
func TestToolInputGate_기본은_꺼짐이고_문턱은_0_3이며_범위를_검사한다(t *testing.T) {
	d := Default()
	if d.ToolInputGate || d.ToolInputMinScore != 0.3 || DefaultToolInputMinScore != 0.3 {
		t.Fatalf("default gate %v min %v", d.ToolInputGate, d.ToolInputMinScore)
	}
	for _, bad := range []float64{0, -0.1, 1.0001, math.NaN()} {
		c := Default()
		c.ToolInputMinScore = bad
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "toolInputMinScore") {
			t.Errorf("toolInputMinScore %v accepted or unclear: %v", bad, err)
		}
	}
	for _, good := range []float64{0.01, 0.5, 1} {
		c := Default()
		c.ToolInputMinScore = good
		if err := c.Validate(); err != nil {
			t.Errorf("toolInputMinScore %v: %v", good, err)
		}
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full","recallMinScore":0.42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.ToolInputGate || c.ToolInputMinScore != DefaultToolInputMinScore {
		t.Fatalf("old file = gate %v min %v err %v", c.ToolInputGate, c.ToolInputMinScore, err)
	}
	if err := os.WriteFile(p, []byte(`{"toolInputGate":true,"toolInputMinScore":0.45}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || !c.ToolInputGate || c.ToolInputMinScore != 0.45 {
		t.Fatalf("file = gate %v min %v err %v", c.ToolInputGate, c.ToolInputMinScore, err)
	}
}

// 순위 스위치(명세 R-1): 기본은 켬이고 문턱은 0.1이다(R-2). rankScoreMargin은 0 < x ≤ 1만 받고, 두 키가 없는 옛 설정 파일은
// 기본값(켬)으로 읽고, false를 적은 파일은 끈다.
func TestRankByScore_기본은_켬이고_문턱은_범위를_검사한다(t *testing.T) {
	d := Default()
	if !d.RankByScore || d.RankScoreMargin != DefaultRankScoreMargin {
		t.Fatalf("default rankByScore %v margin %v", d.RankByScore, d.RankScoreMargin)
	}
	for _, bad := range []float64{0, -0.1, 1.0001, math.NaN()} {
		c := Default()
		c.RankScoreMargin = bad
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "rankScoreMargin") {
			t.Errorf("rankScoreMargin %v accepted or unclear: %v", bad, err)
		}
	}
	for _, good := range []float64{0.01, 0.1, 1} {
		c := Default()
		c.RankScoreMargin = good
		if err := c.Validate(); err != nil {
			t.Errorf("rankScoreMargin %v: %v", good, err)
		}
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full","recallMinScore":0.42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || !c.RankByScore || c.RankScoreMargin != DefaultRankScoreMargin {
		t.Fatalf("old file = rankByScore %v margin %v err %v", c.RankByScore, c.RankScoreMargin, err)
	}
	if err := os.WriteFile(p, []byte(`{"rankByScore":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.RankByScore || c.RankScoreMargin != DefaultRankScoreMargin {
		t.Fatalf("off file = rankByScore %v margin %v err %v", c.RankByScore, c.RankScoreMargin, err)
	}
	if err := os.WriteFile(p, []byte(`{"rankByScore":true,"rankScoreMargin":0.2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || !c.RankByScore || c.RankScoreMargin != 0.2 {
		t.Fatalf("file = rankByScore %v margin %v err %v", c.RankByScore, c.RankScoreMargin, err)
	}
}
